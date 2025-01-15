package cluster

import (
	rafttype "github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/panjf2000/gnet/v2"
	"go.uber.org/zap"
)

// 收到消息
func (s *Server) onMessage(conn gnet.Conn, m *proto.Message) {
	switch m.MsgType {
	case MsgTypeNode:
		s.onNodeMessage(conn, m)
	case MsgTypeSlot:
		s.onSlotMessage(conn, m)
	case MsgTypeChannel:
		s.onChannelMessage(conn, m)
	default:
		if s.onMessageFnc != nil {
			fromNodeId := s.uidToServerId(wkserver.GetUidFromContext(conn))
			err := s.onMessagePool.Submit(func() {
				s.onMessageFnc(fromNodeId, m)
			})
			if err != nil {
				s.Error("onMessage: submit onMessageFnc failed", zap.Error(err))
			}
		}

	}
}

// 节点消息
func (s *Server) onNodeMessage(_ gnet.Conn, m *proto.Message) {
	var event rafttype.Event
	err := event.Unmarshal(m.Content)
	if err != nil {
		s.Error("onNodeMessage: unmarshal event failed", zap.Error(err))
		return
	}
	s.eventServer.Step(event)
}

func (s *Server) onSlotMessage(_ gnet.Conn, m *proto.Message) {
	dec := wkproto.NewDecoder(m.Content)
	key, err := dec.String()
	if err != nil {
		s.Error("onSlotMessage: decode key failed", zap.Error(err))
		return
	}
	data, err := dec.BinaryAll()
	if err != nil {
		s.Error("onSlotMessage: decode data failed", zap.Error(err))
		return
	}
	var event rafttype.Event
	err = event.Unmarshal(data)
	if err != nil {
		s.Error("onSlotMessage: unmarshal event failed", zap.Error(err))
		return
	}
	s.slotServer.AddEvent(key, event)
}

// channel消息
func (s *Server) onChannelMessage(_ gnet.Conn, m *proto.Message) {
	dec := wkproto.NewDecoder(m.Content)
	key, err := dec.String()
	if err != nil {
		s.Error("onChannelMessage: decode key failed", zap.Error(err))
		return
	}
	data, err := dec.BinaryAll()
	if err != nil {
		s.Error("onChannelMessage: decode data failed", zap.Error(err))
		return
	}
	var event rafttype.Event
	err = event.Unmarshal(data)
	if err != nil {
		s.Error("onChannelMessage: unmarshal event failed", zap.Error(err))
		return
	}
	s.channelServer.AddEvent(key, event)
}
