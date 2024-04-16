package cluster

import (
	"fmt"
	"strings"

	pb "github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/cpb"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterevent"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

type Server struct {
	opts               *Options
	clusterEventServer *clusterevent.Server // 分布式配置事件中心
	nodeManager        *nodeManager         // 节点管理者
	slotManager        *slotManager         // 槽管理者

	netServer *wkserver.Server
	wklog.Log
}

func New(opts *Options) *Server {

	s := &Server{
		opts:        opts,
		nodeManager: newNodeManager(opts),
		slotManager: newSlotManager(opts),
		Log:         wklog.NewWKLog(fmt.Sprintf("cluster[%d]", opts.NodeId)),
	}

	opts.Send = func(shardType ShardType, m reactor.Message) {
		node := s.nodeManager.node(m.To)
		if node == nil {
			s.Warn("send failed, node not exist", zap.Uint64("to", m.To))
			return
		}
		data, err := m.Marshal()
		if err != nil {
			s.Error("Marshal failed", zap.Error(err))
			return
		}

		var msgType uint32
		if shardType == ShardTypeSlot {
			msgType = MsgTypeSlot
		} else if shardType == ShardTypeConfig {
			msgType = MsgTypeConfig
		} else if shardType == ShardTypeChannel {
			msgType = MsgTypeChannel
		} else {
			s.Error("send failed, invalid shardType", zap.Uint8("shardType", uint8(shardType)))
			return
		}
		err = node.send(&proto.Message{
			MsgType: msgType,
			Content: data,
		})
		if err != nil {
			s.Error("send failed", zap.Error(err))
			return
		}
	}

	s.clusterEventServer = clusterevent.New(clusterevent.NewOptions(
		clusterevent.WithNodeId(opts.NodeId),
		clusterevent.WithInitNodes(opts.InitNodes),
		clusterevent.WithSlotCount(opts.SlotCount),
		clusterevent.WithSlotMaxReplicaCount(opts.SlotMaxReplicaCount),
		clusterevent.WithReady(s.onEvent),
		clusterevent.WithSend(s.send),
		clusterevent.WithConfigDir(opts.ConfigDir),
	))

	s.netServer = wkserver.New(opts.Addr, wkserver.WithMessagePoolOn(false))
	return s
}

func (s *Server) Start() error {

	if len(s.opts.InitNodes) > 0 {
		for nodeId, clusterAddr := range s.opts.InitNodes {
			s.nodeManager.addNode(s.newNodeByNodeInfo(nodeId, clusterAddr))
		}
	}

	// net server
	s.netServer.OnMessage(s.onMessage)
	err := s.netServer.Start()
	if err != nil {
		return err
	}

	// slot manager
	err = s.slotManager.start()
	if err != nil {
		return err
	}

	// cluster event server
	err = s.clusterEventServer.Start()
	if err != nil {
		return err
	}
	return nil
}

func (s *Server) Stop() {
	s.netServer.Stop()
	s.clusterEventServer.Stop()
	s.slotManager.stop()
}

func (s *Server) AddSlotMessage(m reactor.Message) {
	s.slotManager.addMessage(m)
}

func (s *Server) AddConfigMessage(m reactor.Message) {
	s.clusterEventServer.AddMessage(m)
}

func (s *Server) onEvent(msgs []clusterevent.Message) {
	s.Debug("收到分布式事件....")
	handled := false
	for _, msg := range msgs {
		handled = s.handleClusterEvent(msg)
		if handled {
			s.clusterEventServer.Step(msg)
		}
	}
}

func (s *Server) handleClusterEvent(m clusterevent.Message) bool {

	switch m.Type {
	case clusterevent.EventTypeNodeAdd: // 添加节点
		s.handleNodeAdd(m)
	case clusterevent.EventTypeNodeUpdate: // 修改节点
		s.handleNodeUpdate(m)
	case clusterevent.EventTypeSlotAdd: // 添加槽
		s.handleSlotAdd(m)
	}

	return true

}

func (s *Server) handleNodeAdd(m clusterevent.Message) {
	for _, node := range m.Nodes {
		if s.nodeManager.exist(node.Id) {
			continue
		}
		if strings.TrimSpace(node.ClusterAddr) != "" {
			s.nodeManager.addNode(s.newNodeByNodeInfo(node.Id, node.ClusterAddr))
		}

	}
}

func (s *Server) handleNodeUpdate(m clusterevent.Message) {
	for _, node := range m.Nodes {
		if !s.nodeManager.exist(node.Id) && strings.TrimSpace(node.ClusterAddr) != "" {
			s.nodeManager.addNode(s.newNodeByNodeInfo(node.Id, node.ClusterAddr))
			continue
		}
		n := s.nodeManager.node(node.Id)
		if n != nil && n.addr != node.ClusterAddr {
			s.nodeManager.removeNode(n.id)
			n.stop()
			s.nodeManager.addNode(s.newNodeByNodeInfo(node.Id, node.ClusterAddr))
			continue
		}

	}
}

func (s *Server) handleSlotAdd(m clusterevent.Message) {
	for _, slot := range m.Slots {
		if s.slotManager.exist(slot.Id) {
			continue
		}
		if !wkutil.ArrayContainsUint64(slot.Replicas, s.opts.NodeId) {
			continue
		}
		st := s.newSlot(slot)
		s.slotManager.add(st)

		if slot.Leader != 0 {
			if slot.Leader == s.opts.NodeId {
				st.becomeLeader(slot.Term)
			} else {
				st.becomeFollower(slot.Term, slot.Leader)
			}
		}

	}
}

func (s *Server) newNodeByNodeInfo(nodeID uint64, addr string) *node {
	n := newNode(nodeID, s.serverUid(s.opts.NodeId), addr, s.opts)
	n.start()
	return n
}

func (s *Server) newSlot(st *pb.Slot) *slot {

	slot := newSlot(st, s.opts)
	return slot

}

func (s *Server) serverUid(id uint64) string {
	return fmt.Sprintf("%d", id)
}

func (s *Server) send(m reactor.Message) {

	s.opts.Send(ShardTypeConfig, m)
}

func (s *Server) onMessage(c wknet.Conn, m *proto.Message) {
	msg, err := reactor.UnmarshalMessage(m.Content)
	if err != nil {
		s.Error("UnmarshalMessage failed", zap.Error(err))
		return
	}
	switch m.MsgType {
	case MsgTypeConfig:
		s.AddConfigMessage(msg)
	case MsgTypeSlot:
		s.AddSlotMessage(msg)
	case MsgTypeChannel:
	}
}
