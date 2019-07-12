package proxy

import (
	"log"
	"math/big"
	"regexp"

	"github.com/maoxs2/ergoPool/rpc"
)

// Allow only lowercase hexadecimal with 0x prefix
var noncePattern = regexp.MustCompile("^0x[0-9a-f]{16}$")
var hashPattern = regexp.MustCompile("^0x[0-9a-f]{64}$")
var workerPattern = regexp.MustCompile("^[0-9a-zA-Z-_]{1,4}$")

func (s *ProxyServer) handleGetWorkRPC(cs *Session) (map[string]string, *ErrorReply) {
	t := s.currentBlockTemplate()
	if t == nil || len(t.Header) == 0 || s.isSick() {
		return nil, &ErrorReply{Code: 0, Message: "Work not ready"}
	}
	// var b64 [8]byte
	// copy(b64[:], t.Difficulty.Bytes())

	twoExp256 := new(big.Int).Exp(big.NewInt(2), big.NewInt(256), big.NewInt(0))
	maxUint256 := twoExp256.Sub(twoExp256, big.NewInt(1))
	confDiff := new(big.Int).SetUint64(uint64(s.config.Proxy.Difficulty))
	target := new(big.Int).Div(maxUint256, confDiff)

	// fmt.Println(t.Difficulty)

	return map[string]string{
		"msg": t.Header,
		"b":   target.String(),
		"pk":  t.Seed,
	}, nil
}

func (s *ProxyServer) handleSubmitRPC(cs *Session, login, id string, params *rpc.SolutionReq) (bool, *ErrorReply) {
	if !workerPattern.MatchString(id) {
		id = "unknown"
	}

	if params == nil {
		s.policy.ApplyMalformedPolicy(cs.ip)
		log.Printf("Malformed params from %s@%s %v", login, cs.ip, params)
		return false, &ErrorReply{Code: -1, Message: "Invalid params"}
	}

	//if !noncePattern.MatchString(params[0]) || !hashPattern.MatchString(params[1]) || !hashPattern.MatchString(params[2]) {
	//	s.policy.ApplyMalformedPolicy(cs.ip)
	//	log.Printf("Malformed PoW result from %s@%s %v", login, cs.ip, params)
	//	return false, &ErrorReply{Code: -1, Message: "Malformed PoW result"}
	//}

	t := s.currentBlockTemplate()
	exist, validShare := s.processShare(login, id, cs.ip, t, params)
	ok := s.policy.ApplySharePolicy(cs.ip, !exist && validShare)

	if exist {
		log.Printf("Duplicate share from %s@%s %v", login, cs.ip, params)
		return false, &ErrorReply{Code: 22, Message: "Duplicate share"}
	}

	if !validShare {
		log.Printf("Invalid share from %s@%s", login, cs.ip)
		// Bad shares limit reached, return error and close
		if !ok {
			return false, &ErrorReply{Code: 23, Message: "Invalid share"}
		}
		return false, nil
	}
	log.Printf("Valid share from %s@%s", login, cs.ip)

	if !ok {
		return true, &ErrorReply{Code: -1, Message: "High rate of invalid shares"}
	}
	return true, nil
}

func (s *ProxyServer) handleGetBlockByNumberRPC() *rpc.GetBlockReplyPart {
	t := s.currentBlockTemplate()
	var reply *rpc.GetBlockReplyPart
	if t != nil {
		reply = t.GetPendingBlockCache
	}
	return reply
}

func (s *ProxyServer) handleUnknownRPC(cs *Session, m string) *ErrorReply {
	log.Printf("Unknown request method %s from %s", m, cs.ip)
	s.policy.ApplyMalformedPolicy(cs.ip)
	return &ErrorReply{Code: -3, Message: "Method not found"}
}
