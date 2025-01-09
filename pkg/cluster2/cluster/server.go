package cluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/cluster2/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster2/node/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/cluster2/node/event"
	"github.com/WuKongIM/WuKongIM/pkg/cluster2/slot"
	"github.com/WuKongIM/WuKongIM/pkg/cluster2/store"
	rafttype "github.com/WuKongIM/WuKongIM/pkg/raft/types"
)

type Server struct {
	// 分布式配置服务
	cfgServer *clusterconfig.Server
	// 分布式事件
	eventServer *event.Server
	// 槽分布式服务
	slotServer *slot.Server
	// channel分布式服务
	channelServer *channel.Server
	// 分布式存储
	store *store.Store
	// 配置
	opts *Options
}

func New(ievent event.IEvent, opts *Options) *Server {
	s := &Server{
		opts: opts,
	}

	// 节点分布式配置服务
	s.cfgServer = clusterconfig.New(opts.ConfigOptions)

	//节点事件服务
	s.eventServer = event.NewServer(ievent, opts.ConfigOptions, s.cfgServer)

	// 槽分布式服务
	s.slotServer = slot.NewServer(slot.NewOptions(
		slot.WithNodeId(opts.ConfigOptions.NodeId),
		slot.WithDataDir(opts.DataDir),
		slot.WithTransport(opts.SlotTransport),
		slot.WithNode(s.cfgServer),
		slot.WithOnApply(s.slotApplyLogs),
	))

	// 分布式存储
	s.store = store.New(store.NewOptions(
		store.WithDataDir(opts.DataDir),
		store.WithNodeId(opts.ConfigOptions.NodeId),
		store.WithSlotCount(opts.ConfigOptions.SlotCount),
		store.WithSlot(s.slotServer),
	))

	// 频道分布式服务
	s.channelServer = channel.NewServer(channel.NewOptions(
		channel.WithNodeId(opts.ConfigOptions.NodeId),
		channel.WithTransport(opts.ChannelTransport),
		channel.WithSlot(s.slotServer),
		channel.WithNode(s.cfgServer),
		channel.WithStore(s.store),
		channel.WithCluster(s),
	))

	// 添加事件监听
	s.cfgServer.AddEventListener(s.slotServer)

	return s
}

func (s *Server) Start() error {

	err := s.store.Open()
	if err != nil {
		return err
	}

	err = s.slotServer.Start()
	if err != nil {
		return err
	}

	err = s.eventServer.Start()
	if err != nil {
		return err
	}

	err = s.cfgServer.Start()
	if err != nil {
		return err
	}

	err = s.channelServer.Start()
	if err != nil {
		return err
	}

	return nil
}

func (s *Server) Stop() {
	s.eventServer.Stop()
	s.cfgServer.Stop()
	s.slotServer.Stop()
	s.channelServer.Stop()
	s.store.Close()
}

func (s *Server) NodeStep(event rafttype.Event) {
	s.eventServer.Step(event)
}

func (s *Server) AddSlotEvent(shardNo string, event rafttype.Event) {
	s.slotServer.AddEvent(shardNo, event)
}

func (s *Server) GetConfigServer() *clusterconfig.Server {
	return s.cfgServer
}

func (s *Server) WaitAllSlotReady(ctx context.Context, slotCount int) error {

	return s.slotServer.WaitAllSlotReady(ctx, slotCount)
}

func (s *Server) ProposeToSlotUntilApplied(slotId uint32, data []byte) (*rafttype.ProposeResp, error) {

	return s.slotServer.ProposeUntilApplied(slotId, data)
}

func (s *Server) slotApplyLogs(logs []rafttype.Log) error {
	return s.store.ApplySlotLogs(logs)
}
