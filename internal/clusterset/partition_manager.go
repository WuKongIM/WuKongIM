package clusterset

import (
	"errors"
	"fmt"
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
)

type partitionManager struct {
	cfg *pb.PartitionConfig
	sync.RWMutex
}

func newPartitionManager(cfg *pb.PartitionConfig) *partitionManager {
	return &partitionManager{
		cfg: cfg,
	}
}

func (p *partitionManager) AddNode(node *pb.NodeConfig) {
	p.Lock()
	defer p.Unlock()
	if p.existNode(node.NodeID) {
		return
	}
	p.cfg.Nodes = append(p.cfg.Nodes, node)
}

func (p *partitionManager) existNode(nodeID uint64) bool {
	for _, node := range p.cfg.Nodes {
		if node.NodeID == nodeID {
			return true
		}
	}
	return false
}

func (p *partitionManager) getParitionConfigs() *pb.PartitionConfig {
	return p.cfg
}

// 获取领导节点的配置
func (p *partitionManager) getLeadNodeConfig() *pb.NodeConfig {
	p.RLock()
	defer p.RUnlock()
	for _, node := range p.cfg.Nodes {
		if node.Role == pb.Role_Leader {
			return node
		}
	}
	return nil
}

// 请求投票
func (p *partitionManager) requestVote(req *pb.VoteReq) (*pb.VoteResp, error) {
	leaderCfg := p.getLeadNodeConfig()
	if leaderCfg == nil {
		return nil, errors.New("no leader node")
	}
	if leaderCfg.NodeID == req.NodeID {
		return nil, errors.New("current node is leader")
	}
	cli := client.New(leaderCfg.ServerAddr, client.WithUID(fmt.Sprintf("%d", leaderCfg.NodeID)))
	defer cli.Close()

	resp, err := cli.RequestWithMessage("/requestVote", req)
	if err != nil {
		return nil, err
	}
	if resp.Status != proto.Status_OK {
		return nil, fmt.Errorf("request vote is error, status: %d", resp.Status)
	}
	voteResp := &pb.VoteResp{}
	err = voteResp.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return voteResp, nil
}
