package proxy

import (
	"fmt"
	"log"
	"math/big"
	"sync"

	"github.com/ethereum/go-ethereum/common/hexutil"

	"github.com/ethereum/go-ethereum/common"

	"github.com/maoxs2/ergoPool/rpc"
	"github.com/maoxs2/ergoPool/util"
)

const maxBacklog = 3

type heightDiffPair struct {
	diff   *big.Int
	height uint64
}

type BlockTemplate struct {
	sync.RWMutex
	Header               string
	Seed                 string
	Target               string
	Difficulty           *big.Int
	Height               uint64
	GetPendingBlockCache *rpc.GetBlockReplyPart
	nonces               map[string]bool
	headers              map[string]heightDiffPair
}

type Block struct {
	difficulty  *big.Int
	hashNoNonce common.Hash
	nonce       uint64
	mixDigest   common.Hash
	number      uint64
}

func (b Block) Difficulty() *big.Int     { return b.difficulty }
func (b Block) HashNoNonce() common.Hash { return b.hashNoNonce }
func (b Block) Nonce() uint64            { return b.nonce }
func (b Block) MixDigest() common.Hash   { return b.mixDigest }
func (b Block) NumberU64() uint64        { return b.number }

func (s *ProxyServer) fetchBlockTemplate() {
	srpc := s.rpc()
	t := s.currentBlockTemplate()
	pendingReply, height, diff, err := s.fetchPendingBlock()
	if err != nil {
		log.Printf("Error while refreshing pending block on %s: %s", srpc.Name, err)
		return
	}

	reply, err := srpc.GetWork()
	if err != nil {
		log.Printf("Error while refreshing block template on %s: %s", srpc.Name, err)
		return
	}
	// No need to update, we have fresh job
	if t != nil && t.Header == reply.Msg {
		return
	}

	pendingReply.Target = util.ToHex(s.config.Proxy.Difficulty)

	newTemplate := BlockTemplate{
		Header:               reply.Msg,
		Seed:                 reply.PK,
		Target:               reply.Target.String(),
		Height:               height,
		Difficulty:           new(big.Int).SetUint64(diff),
		GetPendingBlockCache: pendingReply,
		headers:              make(map[string]heightDiffPair),
	}
	// Copy job backlog and add current one
	newTemplate.headers[reply.Msg] = heightDiffPair{
		diff:   reply.Target,
		height: height,
	}
	if t != nil {
		for k, v := range t.headers {
			if v.height > height-maxBacklog {
				newTemplate.headers[k] = v
			}
		}
	}
	s.blockTemplate.Store(&newTemplate)
	log.Printf("New block to mine on %s at height %d / %s", srpc.Name, height, reply.Msg[0:10])

	// Stratum
	//if s.config.Proxy.Stratum.Enabled {
	//	go s.broadcastNewJobs()
	//}
}

func (s *ProxyServer) fetchPendingBlock() (*rpc.GetBlockReplyPart, uint64, uint64, error) {
	srpc := s.rpc()
	//info, err := srpc.GetPendingBlock()
	info := srpc.GetInfo()
	work, _ := srpc.GetWork()
	//if err != nil {
	//	log.Printf("Error while refreshing pending block on %s: %s", srpc.Name, err)
	//	return nil, 0, 0, err
	//}
	//blockNumber, err := strconv.ParseUint(strings.Replace(info["headersHeight"].(string), "0x", "", -1), 16, 64)
	blockNumber, ok := info["headersHeight"].(float64)
	if !ok {
		fmt.Println("failed to get header height info")
	}

	diff, ok := info["difficulty"].(float64)
	if !ok {
		fmt.Println("failed to get header diff info")
	}
	//if err != nil {
	//	log.Println("Can't parse pending block number")
	//	return nil, 0, 0, err
	//}

	//if err != nil {
	//	log.Println("Can't parse pending block difficulty")
	//	return nil, 0, 0, err
	//}
	reply := &rpc.GetBlockReplyPart{
		Number: hexutil.EncodeUint64(uint64(blockNumber)),
		Target: hexutil.EncodeUint64(work.Target.Uint64()),
	}

	// maxUint64 := new(big.Int).Exp(big.NewInt(2), big.NewInt(64), big.NewInt(0))
	// diff := new(big.Int).Div(maxUint64, work.Target)

	return reply, uint64(blockNumber), uint64(diff), nil
}
