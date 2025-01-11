package channel

import (
	"github.com/WuKongIM/WuKongIM/pkg/raft/raft"
	"github.com/WuKongIM/WuKongIM/pkg/raft/raftgroup"
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

type Channel struct {
	*raft.Node
	// 分布式配置
	cfg wkdb.ChannelClusterConfig
	s   *Server
	wklog.Log
	rg         *raftgroup.RaftGroup
	channelKey string
}

func createChannel(cfg wkdb.ChannelClusterConfig, s *Server, rg *raftgroup.RaftGroup) (*Channel, error) {
	channelKey := wkutil.ChannelToKey(cfg.ChannelId, cfg.ChannelType)
	ch := &Channel{
		cfg:        cfg,
		s:          s,
		Log:        wklog.NewWKLog("channel"),
		rg:         rg,
		channelKey: channelKey,
	}

	state, err := s.storage.GetState(cfg.ChannelId, cfg.ChannelType)
	if err != nil {
		ch.Error("get state failed", zap.String("channelKey", channelKey), zap.Error(err))
		return nil, err
	}

	lastLogStartIndex, err := s.storage.GetTermStartIndex(channelKey, state.LastTerm)
	if err != nil {
		ch.Error("get last term failed", zap.String("channelKey", channelKey), zap.Error(err))
		return nil, err
	}

	ch.Node = raft.NewNode(lastLogStartIndex, state, raft.NewOptions(raft.WithKey(channelKey), raft.WithNodeId(s.opts.NodeId)))

	return ch, nil
}

func (ch *Channel) switchConfig(cfg types.Config) error {

	return ch.rg.AddEventWait(ch.channelKey, types.Event{
		Type:   types.ConfChange,
		Config: cfg,
	})

}

func channelConfigToRaftConfig(cfg wkdb.ChannelClusterConfig) types.Config {

	return types.Config{
		MigrateFrom: cfg.MigrateFrom,
		MigrateTo:   cfg.MigrateTo,
		Replicas:    cfg.Replicas,
		Learners:    cfg.Learners,
		Term:        cfg.Term,
		Leader:      cfg.LeaderId,
	}
}
