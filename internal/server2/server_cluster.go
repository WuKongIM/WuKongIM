package server

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

// handleClusterMessage 处理分布式消息（注意：不要再此方法里做耗时操作，如果耗时操作另起协程）
func (s *Server) handleClusterMessage(fromNodeId uint64, msg *proto.Message) {

	// switch ClusterMsgType(msg.MsgType) {
	// case ClusterMsgTypeConnWrite: // 远程连接写入
	// 	p.handleConnWrite(msg)
	// case ClusterMsgTypeConnClose:
	// 	p.handleConnClose(msg)
	// }
}

func (s *Server) setClusterRoutes() {
	// s.cluster.Route("/wk/connect", p.handleConnectReq)
	// s.cluster.Route("/wk/recvPacket", p.handleOnRecvPacketReq)
	// s.cluster.Route("/wk/sendPacket", p.handleOnSendPacketReq)
	// 转发ping
	// s.cluster.Route("/wk/connPing", s.handleOnConnPingReq)
	// 转发消息到频道的领导节点
	s.cluster.Route("/wk/channelFoward", s.handleChannelForward)

	// 转发sendack回执信息到源节点
	s.cluster.Route("/wk/forwardSendack", s.handleForwardSendack)

	// 转发连接写数据
	s.cluster.Route("/wk/connWrite", s.handleConnWrite)
}

func (s *Server) handleChannelForward(c *wkserver.Context) {
	var reactorChannelMessageSet = ReactorChannelMessageSet{}
	err := reactorChannelMessageSet.Unmarshal(c.Body())
	if err != nil {
		s.Error("handleChannelForward Unmarshal err", zap.Error(err))
		c.WriteErr(err)
		return
	}

	fmt.Println("handleChannelForward--->", len(reactorChannelMessageSet))

	if len(reactorChannelMessageSet) == 0 {
		c.WriteOk()
		return
	}

	firstMsg := reactorChannelMessageSet[0]
	fakeChannelId := firstMsg.SendPacket.ChannelID
	if firstMsg.SendPacket.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelId = GetFakeChannelIDWith(firstMsg.FromUid, reactorChannelMessageSet[0].SendPacket.ChannelID)
	}
	timeoutCtx, cancel := context.WithTimeout(s.ctx, time.Second*5)
	defer cancel()
	isLeader, err := s.cluster.IsLeaderOfChannel(timeoutCtx, fakeChannelId, firstMsg.SendPacket.ChannelType)
	if err != nil {
		s.Error("get is channel leader failed", zap.String("channelId", fakeChannelId), zap.Uint8("channelType", firstMsg.SendPacket.ChannelType))
		c.WriteErr(err)
		return
	}

	if !isLeader {
		s.Error("not is leader", zap.String("channelId", fakeChannelId), zap.Uint8("channelType", firstMsg.SendPacket.ChannelType))
		c.WriteErrorAndStatus(err, proto.Status(errCodeNotIsChannelLeader))
		return
	}
	lastMsg := reactorChannelMessageSet[len(reactorChannelMessageSet)-1]
	fmt.Println("handleChannelForward--->", lastMsg.MessageId)
	for _, reactorChannelMessage := range reactorChannelMessageSet {
		sendPacket := reactorChannelMessage.SendPacket
		// 提案频道消息
		err = s.channelReactor.proposeSend(reactorChannelMessage.FromUid, reactorChannelMessage.FromDeviceId, reactorChannelMessage.FromConnId, reactorChannelMessage.FromNodeId, false, sendPacket)
		if err != nil {
			s.Error("handleChannelForward: proposeSend failed")
			c.WriteErr(err)
			return
		}
	}

	c.WriteOk()

}

func (s *Server) handleForwardSendack(c *wkserver.Context) {
	var forwardSendackPacketSet = ForwardSendackPacketSet{}
	err := forwardSendackPacketSet.Unmarshal(c.Body())
	if err != nil {
		s.Error("handleForwardSendack Unmarshal err", zap.Error(err))
		c.WriteErr(err)
		return
	}

	if len(forwardSendackPacketSet) == 0 {
		c.WriteOk()
		return
	}

	for _, forwardSendackPacket := range forwardSendackPacketSet {
		conn := s.userReactor.getConnContextById(forwardSendackPacket.Uid, forwardSendackPacket.ConnId)
		if conn == nil {
			s.Error("handleForwardSendack: conn not found", zap.String("uid", forwardSendackPacket.Uid), zap.Int64("connId", forwardSendackPacket.ConnId))
			c.WriteErr(err)
			return
		}

		err = s.userReactor.writePacketByConnId(forwardSendackPacket.Uid, forwardSendackPacket.ConnId, forwardSendackPacket.Sendack)
		if err != nil {
			s.Error("handleForwardSendack: writePacketByConnId failed", zap.Error(err))
			c.WriteErr(err)
			return
		}
	}
	c.WriteOk()
}

func (s *Server) handleConnWrite(c *wkserver.Context) {
	var fowardWriteReq = &FowardWriteReq{}
	err := fowardWriteReq.Unmarshal(c.Body())
	if err != nil {
		s.Error("handleConnWrite Unmarshal err", zap.Error(err))
		c.WriteErr(err)
		return
	}

	if len(fowardWriteReq.Data) == 0 {
		c.WriteOk()
		return
	}

	conn := s.userReactor.getConnContextById(fowardWriteReq.Uid, fowardWriteReq.ConnId)
	if conn == nil {
		s.Error("handleConnWrite: conn not found", zap.String("uid", fowardWriteReq.Uid), zap.Int64("connId", fowardWriteReq.ConnId))
		c.WriteErr(err)
		return
	}
	if conn.conn == nil {
		s.Error("handleConnWrite: conn is nil", zap.String("uid", fowardWriteReq.Uid), zap.Int64("connId", fowardWriteReq.ConnId))
		c.WriteErr(err)
		return
	}
	s.responseData(conn.conn, fowardWriteReq.Data)
	c.WriteOk()
}
