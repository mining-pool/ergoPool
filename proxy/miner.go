package proxy

import (
	"github.com/maoxs2/ergoPool/rpc"
	"log"

)

func (s *ProxyServer) processShare(login, id, ip string, t *BlockTemplate, params *rpc.SolutionReq) (bool, bool) {
	//nonceHex := params[0]
	//hashNoNonce := params[1]
	hashNoNonce := t.Header
	//mixDigest := params[2]
	//nonce, _ := strconv.ParseUint(strings.Replace(nonceHex, "0x", "", -1), 16, 64)
	shareDiff := s.config.Proxy.Difficulty

	//maxUint256 := new(big.Int).Exp(big.NewInt(2), big.NewInt(256), big.NewInt(0))

	h, ok := t.headers[hashNoNonce]
	if !ok {
		log.Printf("Stale share from %v@%v", login, ip)
		return false, false
	}

	ok, err := s.rpc().SubmitSolution(params)
	if err != nil {
		log.Printf("Block submission failure at height %v for %v: %v", h.height, t.Header, err)
	} else if !ok {
		log.Printf("Block rejected at height %v for %v", h.height, t.Header)
	} else {
		s.fetchBlockTemplate()
		_, err := s.backend.WriteBlock(login, id, params, shareDiff, h.diff.Int64(), h.height, s.hashrateExpiration, t.Header)
		// if exist {
		// 	return true, false
		// }
		if err != nil {
			log.Println("Failed to insert block candidate into backend:", err)
		} else {
			log.Printf("Inserted block %v to backend", h.height)
		}
		log.Printf("Block found by miner %v@%v at height %d", login, ip, h.height)
		// return false, true
	}

	_, err = s.backend.WriteShare(login, id, params, shareDiff, h.height, s.hashrateExpiration)
	if err != nil {
		log.Println("Failed to insert share data into backend:", err)
	}

	return false, true
}
