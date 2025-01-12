package channel

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
)

func (s *Server) ProposeBatchUntilAppliedTimeout(ctx context.Context, channelId string, channelType uint8, reqs types.ProposeReqSet) (types.ProposeRespSet, error) {

	channelKey := wkutil.ChannelToKey(channelId, channelType)
	rg := s.getRaftGroup(channelKey)

	// ========== 如果当前节点存在频道的raft，则直接提按 ==========
	raft := rg.GetRaft(channelKey)
	if raft != nil && raft.IsLeader() {
		return rg.ProposeBatchUntilAppliedTimeout(ctx, channelKey, reqs)
	}

	// ========== 如果不存在，则先从频道的槽领导获取频道的分布式配置，然后根据配置执行对应逻辑 ==========
	clusterConfig, err := s.opts.Cluster.GetOrCreateChannelClusterConfigFromSlotLeader(channelId, channelType)
	if err != nil {
		return nil, err
	}

	// 如果当前节点是频道的领导节点
	if clusterConfig.LeaderId == s.opts.NodeId {
		// 根据需要唤醒频道领导
		err = s.WakeLeaderIfNeed(clusterConfig)
		if err != nil {
			return nil, err
		}
		return rg.ProposeBatchUntilAppliedTimeout(ctx, channelKey, reqs)
	}

	// 向频道的领导节点请求提案
	return s.opts.RPC.RequestChannelProposeBatchUntilApplied(clusterConfig.LeaderId, channelId, channelType, reqs)

}

func (s *Server) SwitchConfig(channelId string, channelType uint8, cfg wkdb.ChannelClusterConfig) error {
	channelKey := wkutil.ChannelToKey(channelId, channelType)
	rg := s.getRaftGroup(channelKey)
	raft := rg.GetRaft(channelKey)
	if raft == nil {
		return nil
	}
	return raft.(*Channel).switchConfig(channelConfigToRaftConfig(cfg))

}
