package server

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/cluster2/node/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/cluster2/node/event"
	"github.com/WuKongIM/WuKongIM/pkg/cluster2/slot"
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	rafttype "github.com/WuKongIM/WuKongIM/pkg/raft/types"
)

type Server struct {
	// 分布式配置服务
	cfgServer *clusterconfig.Server
	// 分布式事件
	eventServer *event.Server
	// 槽分布式服务
	slotServer *slot.Server

	opts *Options
}

func New(ievent event.IEvent, opts *Options) *Server {
	s := &Server{
		opts: opts,
	}

	s.cfgServer = clusterconfig.New(opts.ConfigOptions)
	s.eventServer = event.NewServer(ievent, opts.ConfigOptions, s.cfgServer)
	s.slotServer = slot.NewServer(slot.NewOptions(slot.WithNodeId(opts.ConfigOptions.NodeId), slot.WithDataDir(opts.DataDir), slot.WithTransport(opts.SlotTransport)))

	s.cfgServer.AddEventListener(s.slotServer)

	return s
}

func (s *Server) Start() error {
	err := s.slotServer.Start()
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

	return nil
}

func (s *Server) Stop() {
	s.eventServer.Stop()
	s.cfgServer.Stop()
	s.slotServer.Stop()
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

func (s *Server) ProposeToSlot(slotId uint32, reqs types.ProposeReqSet) (*types.ProposeResp, error) {

	return s.slotServer.Propose(slotId, reqs)
}
