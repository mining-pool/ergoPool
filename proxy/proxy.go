package proxy

import (
	"encoding/json"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"

	"github.com/maoxs2/ergoPool/policy"
	"github.com/maoxs2/ergoPool/rpc"
	"github.com/maoxs2/ergoPool/storage"
	"github.com/maoxs2/ergoPool/util"
)

type ProxyServer struct {
	config             *Config
	blockTemplate      atomic.Value
	upstream           int32
	upstreams          []*rpc.RPCClient
	backend            *storage.RedisClient
	diff               string
	policy             *policy.PolicyServer
	hashrateExpiration time.Duration
	failsCount         int64

	// Stratum
	sessionsMu sync.RWMutex
	sessions   map[*Session]struct{}
	timeout    time.Duration
}

type Session struct {
	ip  string
	enc *json.Encoder

	// Stratum
	sync.Mutex
	conn  *net.TCPConn
	login string
}

func NewProxy(cfg *Config, backend *storage.RedisClient) *ProxyServer {
	if len(cfg.Name) == 0 {
		log.Fatal("You must set instance name")
	}
	policy := policy.Start(&cfg.Proxy.Policy, backend)

	proxy := &ProxyServer{config: cfg, backend: backend, policy: policy}
	proxy.diff = util.GetTargetHex(cfg.Proxy.Difficulty)

	proxy.upstreams = make([]*rpc.RPCClient, len(cfg.Upstream))
	for i, v := range cfg.Upstream {
		proxy.upstreams[i] = rpc.NewRPCClient(v.Name, v.Url, v.Timeout)
		log.Printf("Upstream: %s => %s", v.Name, v.Url)
	}
	log.Printf("Default upstream: %s => %s", proxy.rpc().Name, proxy.rpc().Url)

	//if cfg.Proxy.Stratum.Enabled {
	//	proxy.sessions = make(map[*Session]struct{})
	//	go proxy.ListenTCP()
	//}

	proxy.fetchBlockTemplate()

	proxy.hashrateExpiration = util.MustParseDuration(cfg.Proxy.HashrateExpiration)

	refreshIntv := util.MustParseDuration(cfg.Proxy.BlockRefreshInterval)
	refreshTimer := time.NewTimer(refreshIntv)
	log.Printf("Set block refresh every %v", refreshIntv)

	checkIntv := util.MustParseDuration(cfg.UpstreamCheckInterval)
	checkTimer := time.NewTimer(checkIntv)

	stateUpdateIntv := util.MustParseDuration(cfg.Proxy.StateUpdateInterval)
	stateUpdateTimer := time.NewTimer(stateUpdateIntv)

	go func() {
		for {
			select {
			case <-refreshTimer.C:
				proxy.fetchBlockTemplate()
				refreshTimer.Reset(refreshIntv)
			}
		}
	}()

	go func() {
		for {
			select {
			case <-checkTimer.C:
				proxy.checkUpstreams()
				checkTimer.Reset(checkIntv)
			}
		}
	}()

	go func() {
		for {
			select {
			case <-stateUpdateTimer.C:
				t := proxy.currentBlockTemplate()
				if t != nil {
					err := backend.WriteNodeState(cfg.Name, t.Height, t.Difficulty)
					if err != nil {
						log.Printf("Failed to write node state to backend: %v", err)
						proxy.markSick()
					} else {
						proxy.markOk()
					}
				}
				stateUpdateTimer.Reset(stateUpdateIntv)
			}
		}
	}()

	return proxy
}

func (s *ProxyServer) Start() {
	log.Printf("Starting proxy on %v", s.config.Proxy.Listen)
	r := mux.NewRouter()
	r.Handle("/{login:[0-9a-zA-Z]{51}}/{id:[0-9a-zA-Z-_]{1,4}}/mining/candidate", s)
	r.Handle("/{login}/{id:[0-9a-zA-Z-_]{1,4}}/mining/candidate", s)
	r.Handle("/{login:[0-9a-zA-Z]{51}}/{id:[0-9a-zA-Z-_]{1,4}}/mining/solution", s)
	r.Handle("/{login:[0-9a-zA-Z]{51}}/mining/candidate", s)
	r.Handle("/{login}/mining/candidate", s)
	r.Handle("/{login:[0-9a-zA-Z]{51}}/mining/solution", s)
	srv := &http.Server{
		Addr:           s.config.Proxy.Listen,
		Handler:        r,
		MaxHeaderBytes: s.config.Proxy.LimitHeadersSize,
	}
	err := srv.ListenAndServe()
	if err != nil {
		log.Fatalf("Failed to start proxy: %v", err)
	}
}

func (s *ProxyServer) rpc() *rpc.RPCClient {
	i := atomic.LoadInt32(&s.upstream)
	return s.upstreams[i]
}

func (s *ProxyServer) checkUpstreams() {
	candidate := int32(0)
	backup := false

	for i, v := range s.upstreams {
		if v.Check() && !backup {
			candidate = int32(i)
			backup = true
		}
	}

	if s.upstream != candidate {
		log.Printf("Switching to %v upstream", s.upstreams[candidate].Name)
		atomic.StoreInt32(&s.upstream, candidate)
	}
}

func (s *ProxyServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	//if r.Method != "POST" {
	//	s.writeError(w, 405, "rpc: POST method required, received "+r.Method)
	//	return
	//}
	ip := s.remoteAddr(r)
	if !s.policy.IsBanned(ip) {
		s.handleClient(w, r, ip)
	}
}

func (s *ProxyServer) remoteAddr(r *http.Request) string {
	if s.config.Proxy.BehindReverseProxy {
		ip := r.Header.Get("X-Forwarded-For")
		if len(ip) > 0 && net.ParseIP(ip) != nil {
			return ip
		}
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	return ip
}

func (s *ProxyServer) handleClient(w http.ResponseWriter, r *http.Request, ip string) {
	if r.ContentLength > s.config.Proxy.LimitBodySize {
		log.Printf("Socket flood from %s", ip)
		s.policy.ApplyMalformedPolicy(ip)
		http.Error(w, "Request too large", http.StatusExpectationFailed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.config.Proxy.LimitBodySize)
	defer r.Body.Close()

	cs := &Session{ip: ip, enc: json.NewEncoder(w)}
	dec := json.NewDecoder(r.Body)
	for {
		var req map[string]interface{}
		if err := dec.Decode(&req); err == io.EOF {
			cs.handleMessage(s, r, nil)
			break
		} else if err != nil {
			log.Printf("Malformed request from %v: %v", ip, err)
			s.policy.ApplyMalformedPolicy(ip)
			return
		}
		cs.handleMessage(s, r, req)
	}
}

func (cs *Session) handleMessage(s *ProxyServer, r *http.Request, req map[string]interface{}) {
	// if req.Id == nil {
	// 	log.Printf("Missing RPC id from %s", cs.ip)
	// 	s.policy.ApplyMalformedPolicy(cs.ip)
	// 	return
	// }

	vars := mux.Vars(r)
	login := vars["login"]

	// if !util.IsValidHexAddress(login) {
	// 	errReply := &ErrorReply{Code: -1, Message: "Invalid login"}
	// 	cs.sendError(req.Id, errReply)
	// 	return
	// }
	// if !s.policy.ApplyLoginPolicy(login, cs.ip) {
	// 	errReply := &ErrorReply{Code: -1, Message: "You are blacklisted"}
	// 	cs.sendError(req.Id, errReply)
	// 	return
	// }

	//if r.Method != "POST" {
	//	s.writeError(w, 405, "rpc: POST method required, received "+r.Method)
	//	return
	//}
	// Handle RPC methods
	switch r.Method {
	case "GET":
		reply, errReply := s.handleGetWorkRPC(cs)
		if errReply != nil {
			cs.sendError(errReply)
			break
		}
		cs.sendResult(reply)

	case "POST":
		log.Println("Get Sol: ", req)
		hashFloat, okD := req["d"].(float64)
		hash := big.NewFloat(hashFloat) //.Int(nil)

		pkHex, okPK := req["pk"].(string)
		wHex, okW := req["w"].(string)
		nHex, okN := req["n"].(string)

		if hash != nil && okD && okN && okPK && okW {

			var params = &rpc.SolutionReq{
				PK:   pkHex,
				W:    wHex,
				N:    nHex,
				Hash: hash, // d
			}
			// err := json.Unmarshal(req.Params, &params)
			// if err != nil {
			// 	log.Printf("Unable to parse params from %v", cs.ip)
			// 	s.policy.ApplyMalformedPolicy(cs.ip)
			// 	break
			// }

			reply, errReply := s.handleSubmitRPC(cs, login, vars["id"], params)
			if errReply != nil {
				cs.sendError(errReply)
				break
			}
			if !reply {
				cs.sendResult(map[string]string{
					"error": "Solution is invalid",
				})
			}

			cs.sendResult(map[string]string{
				"success": "Solution is valid",
			})

		} else {
			s.policy.ApplyMalformedPolicy(cs.ip)
			errReply := &ErrorReply{Code: -1, Message: "Malformed request"}
			cs.sendError(errReply)
		}

	//case "vns_getBlockByNumber":
	//	reply := s.handleGetBlockByNumberRPC()
	//	cs.sendResult(req.Id, reply)
	//case "vns_submitHashrate":
	//	cs.sendResult(req.Id, true)
	default:
		errReply := s.handleUnknownRPC(cs, "unknown")
		cs.sendError(errReply)
	}
}

func (cs *Session) sendResult(result map[string]string) error {
	//message := JSONRpcResp{Id: id, Version: "2.0", Error: nil, Result: result}
	// log.Println("sending result:", &result)
	return cs.enc.Encode(&result)
}

func (cs *Session) sendError(err *ErrorReply) error {
	// message := JSONRpcResp{Id: id, Version: "2.0", Error: reply}
	return cs.enc.Encode(&err)
}

func (s *ProxyServer) writeError(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
}

func (s *ProxyServer) currentBlockTemplate() *BlockTemplate {
	t := s.blockTemplate.Load()
	if t != nil {
		return t.(*BlockTemplate)
	} else {
		return nil
	}
}

func (s *ProxyServer) markSick() {
	atomic.AddInt64(&s.failsCount, 1)
}

func (s *ProxyServer) isSick() bool {
	x := atomic.LoadInt64(&s.failsCount)
	if s.config.Proxy.HealthCheck && x >= s.config.Proxy.MaxFails {
		return true
	}
	return false
}

func (s *ProxyServer) markOk() {
	atomic.StoreInt64(&s.failsCount, 0)
}
