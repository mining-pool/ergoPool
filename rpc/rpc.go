package rpc

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/common"

	"github.com/maoxs2/ergoPool/util"
)

type RPCClient struct {
	sync.RWMutex
	Url         string
	Name        string
	sick        bool
	sickRate    int
	successRate int
	client      *http.Client
}

type GetBlockReply struct {
	Number     string `json:"number"`
	Hash       string `json:"hash"`
	Nonce      string `json:"nonce"`
	Miner      string `json:"miner"`
	Difficulty string `json:"difficulty"`

	Transactions []Tx `json:"transactions"`
	// https://github.com/ethereum/EIPs/issues/95
	SealFields []string `json:"sealFields"`
}

type GetBlockReplyPart struct {
	Number string `json:"number"`
	Target string `json:"difficulty"`
}

const receiptStatusSuccessful = "0x1"

type TxReceipt struct {
	TxHash    string `json:"transactionHash"`
	GasUsed   string `json:"gasUsed"`
	BlockHash string `json:"blockHash"`
	Status    string `json:"status"`
}

func (r *TxReceipt) Confirmed() bool {
	return len(r.BlockHash) > 0
}

// Use with previous method
func (r *TxReceipt) Successful() bool {
	if len(r.Status) > 0 {
		return r.Status == receiptStatusSuccessful
	}
	return true
}

type Tx struct {
	Gas      string `json:"gas"`
	GasPrice string `json:"gasPrice"`
	Hash     string `json:"hash"`
}

type JSONRpcResp struct {
	Id     *json.RawMessage       `json:"id"`
	Result *json.RawMessage       `json:"result"`
	Error  map[string]interface{} `json:"error"`
}

type CandidateResp struct {
	Msg    string   `json:"msg"`
	Target *big.Int `json:"b"`
	PK     string   `json:"pk"`
}

type SolutionReq struct {
	PK   string     `json:"pk"`
	W    string     `json:"w"`
	N    string     `json:"n"`
	Hash *big.Float `json:"d"`
}

func NewRPCClient(name, url, timeout string) *RPCClient {
	rpcClient := &RPCClient{Name: name, Url: url}
	timeoutIntv := util.MustParseDuration(timeout)
	rpcClient.client = &http.Client{
		Timeout: timeoutIntv,
	}
	return rpcClient
}

func (r *RPCClient) GetWork() (*CandidateResp, error) {
	req, err := http.NewRequest("GET", r.Url+"/mining/candidate", bytes.NewBuffer(nil))
	req.Header.Set("Accept", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		r.markSick()
		return nil, err
	}
	defer resp.Body.Close()

	var rpcResp *CandidateResp
	err = json.NewDecoder(resp.Body).Decode(&rpcResp)
	if err != nil {
		r.markSick()
		return nil, err
	}

	return rpcResp, err
}

func (r *RPCClient) GetInfo() map[string]interface{} {
	//var reply map[string]string
	rpcResp, _ := r.doGet("/info")
	//if err != nil {
	//	return rpcResp
	//}
	//var reply []string
	//err = json.Unmarshal(*rpcResp, &reply)

	return rpcResp
}

// TODO: in unlocker
// = get current block/height
func (r *RPCClient) GetPendingBlock() (*BlockHeader, error) {
	req, err := http.NewRequest("GET", r.Url+"/blocks/lastHeaders/1", bytes.NewBuffer(nil))
	req.Header.Set("Accept", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		r.markSick()
		return nil, err
	}
	defer resp.Body.Close()

	var blockResp []BlockHeader
	err = json.NewDecoder(resp.Body).Decode(&blockResp)
	if err != nil {
		r.markSick()
		return nil, err
	}

	return &blockResp[0], nil
}

type BlockHeader struct {
	Number string `json:"height"`
	Hash   string `json:"id"`
	PoWSol PoWSol `json:"powSolutions"`
}

type PoWSol struct {
	PublicKey string `json:"pk"`
	W         string `json:"w"`
	N         string `json:"n"`
	D         string `json:"d"`
}

// DONE: in unlocker
func (r *RPCClient) GetBlockByHeight(height int64) (*BlockHeader, error) {
	h := strconv.FormatInt(height, 10)
	req, err := http.NewRequest("GET", r.Url+"/blocks/at/"+h, bytes.NewBuffer(nil))
	req.Header.Set("Accept", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		r.markSick()
		return nil, err
	}
	defer resp.Body.Close()

	var blockResp []string
	err = json.NewDecoder(resp.Body).Decode(&blockResp)
	if err != nil {
		r.markSick()
		return nil, err
	}

	blockHeader := blockResp[0]

	req, err = http.NewRequest("GET", r.Url+"/blocks/"+blockHeader+"/header", bytes.NewBuffer(nil))
	req.Header.Set("Accept", "application/json")

	resp, err = r.client.Do(req)
	if err != nil {
		r.markSick()
		return nil, err
	}
	defer resp.Body.Close()

	var rpcResp *BlockHeader
	err = json.NewDecoder(resp.Body).Decode(&rpcResp)
	if err != nil {
		r.markSick()
		return nil, err
	}

	return rpcResp, err
}

//func (r *RPCClient) GetBlockByHash(hash string) (*GetBlockReply, error) {
//	params := []interface{}{hash, true}
//	return r.getBlockBy("vns_getBlockByHash", params)
//}

// TODO: in payer
func (r *RPCClient) GetTxReceipt(hash string) (*TxReceipt, error) {
	rpcResp, err := r.doPost(r.Url, "vns_getTransactionReceipt", []string{hash})
	if err != nil {
		return nil, err
	}
	if rpcResp.Result != nil {
		var reply *TxReceipt
		err = json.Unmarshal(*rpcResp.Result, &reply)
		return reply, err
	}
	return nil, nil
}

func (r *RPCClient) SubmitSolution(params *SolutionReq) (bool, error) {
	_, err := r.doPost(r.Url, "/mining/solution", params)
	if err != nil {
		return false, err
	}
	//var reply bool
	//err = json.Unmarshal(*rpcResp.Result, &reply)
	return true, err
}

// TODO: payer
func (r *RPCClient) GetBalance(address string) (*big.Int, error) {
	rpcResp, err := r.doPost(r.Url, "vns_getBalance", []string{address, "latest"})
	if err != nil {
		return nil, err
	}
	var reply string
	err = json.Unmarshal(*rpcResp.Result, &reply)
	if err != nil {
		return nil, err
	}
	return util.String2Big(reply), err
}

// TODO: in payer
func (r *RPCClient) Sign(from string, s string) (string, error) {
	hash := sha256.Sum256([]byte(s))
	rpcResp, err := r.doPost(r.Url, "vns_sign", []string{from, common.ToHex(hash[:])})
	var reply string
	if err != nil {
		return reply, err
	}
	err = json.Unmarshal(*rpcResp.Result, &reply)
	if err != nil {
		return reply, err
	}
	if util.IsZeroHash(reply) {
		err = errors.New("Can't sign message, perhaps account is locked")
	}
	return reply, err
}

// TODO: in payer
func (r *RPCClient) GetPeerCount() (int64, error) {
	rpcResp, err := r.doPost(r.Url, "net_peerCount", nil)
	if err != nil {
		return 0, err
	}
	var reply string
	err = json.Unmarshal(*rpcResp.Result, &reply)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.Replace(reply, "0x", "", -1), 16, 64)
}

// TODO: in payer
func (r *RPCClient) SendTransaction(from, to, gas, gasPrice, value string, autoGas bool) (string, error) {
	params := map[string]string{
		"address":  to,
		"value": value,
		"fee": gasPrice,
	}
	//if !autoGas {
	//	params["gas"] = gas
	//	params["gasPrice"] = gasPrice
	//}

	// req, err := http.NewRequest("POST", r.Url+"/wallet/payment/send", bytes.NewBuffer(nil))
	// req.Header.Set("Accept", "application/json")

	// resp, err := r.client.Do(req)
	// if err != nil {
	// 	r.markSick()
	// 	return nil, err
	// }
	// defer resp.Body.Close()

	// var blockResp []string
	// err = json.NewDecoder(resp.Body).Decode(&blockResp)
	// if err != nil {
	// 	r.markSick()
	// 	return nil, err
	// }

	rpcResp, err := r.doPost(r.Url, "vns_sendTransaction", []interface{}{params})
	var reply string
	if err != nil {
		return reply, err
	}
	err = json.Unmarshal(*rpcResp.Result, &reply)
	if err != nil {
		return reply, err
	}
	/* There is an inconsistence in a "standard". Geth returns error if it can't unlock signer account,
	 * but Parity returns zero hash 0x000... if it can't send tx, so we must handle this case.
	 * https://github.com/ethereum/wiki/wiki/JSON-RPC#returns-22
	 */
	if util.IsZeroHash(reply) {
		err = errors.New("transaction is not yet available")
	}
	return reply, err
}

func (r *RPCClient) doPost(url string, method string, params interface{}) (*JSONRpcResp, error) {
	//jsonReq := map[string]interface{}{"jsonrpc": "2.0", "method": method, "params": params, "id": 0}
	data, _ := json.Marshal(params)
	log.Println(string(data))

	req, err := http.NewRequest("POST", url+method, bytes.NewBuffer(data))
	req.Header.Set("Content-Length", (string)(len(data)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		r.markSick()
		return nil, err
	}
	defer resp.Body.Close()

	//var rpcResp *JSONRpcResp
	//err = json.NewDecoder(resp.Body).Decode(&rpcResp)
	//if err != nil {
	//	r.markSick()
	//	return nil, err
	//}
	//if rpcResp.Error != nil {
	//	r.markSick()
	//	return nil, errors.New(rpcResp.Error["message"].(string))
	//}

	if resp.StatusCode != 200 {
		var errMsgs map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errMsgs)
		detail, _ := errMsgs["detail"].(string)
		log.Println(detail)
		return nil, errors.New(string(detail))
	}

	return nil, nil
}

func (r *RPCClient) doGet(subUrl string) (map[string]interface{}, error) {
	req, err := http.NewRequest("GET", r.Url+subUrl, bytes.NewBuffer(nil))
	//req.Header.Set("Content-Length", (string)(len(data)))
	//req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		r.markSick()
		return nil, err
	}
	defer resp.Body.Close()

	var rpcResp map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&rpcResp)
	if err != nil {
		r.markSick()
		return nil, err
	}
	//if rpcResp.Error != nil {
	//	r.markSick()
	//	return nil, errors.New(rpcResp.Error["message"].(string))
	//}

	return rpcResp, err
}

func (r *RPCClient) Check() bool {
	_, err := r.GetWork()
	if err != nil {
		return false
	}
	r.markAlive()
	return !r.Sick()
}

func (r *RPCClient) Sick() bool {
	r.RLock()
	defer r.RUnlock()
	return r.sick
}

func (r *RPCClient) markSick() {
	r.Lock()
	r.sickRate++
	r.successRate = 0
	if r.sickRate >= 5 {
		r.sick = true
	}
	r.Unlock()
}

func (r *RPCClient) markAlive() {
	r.Lock()
	r.successRate++
	if r.successRate >= 5 {
		r.sick = false
		r.sickRate = 0
		r.successRate = 0
	}
	r.Unlock()
}
