package server

import (
	"context"
	"errors"
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
	// 转发userAction
	s.cluster.Route("/wk/userAction", s.handleUserAction)

	// 用户认证结果（这个是领导节点认证通过后，通知代理节点的结果）
	s.cluster.Route("/wk/userAuthResult", s.handleUserAuthResult)

	// 投递消息（将需要投递的消息转发给对应用户的逻辑节点）
	s.cluster.Route("/wk/deliver", s.handleDeliver)

	// 通过tag获取当前节点需要投递的用户集合
	s.cluster.Route("/wk/getNodeUidsByTag", s.getNodeUidsByTag)
}

func (s *Server) handleChannelForward(c *wkserver.Context) {
	var reactorChannelMessageSet = ReactorChannelMessageSet{}
	err := reactorChannelMessageSet.Unmarshal(c.Body())
	if err != nil {
		s.Error("handleChannelForward Unmarshal err", zap.Error(err))
		c.WriteErr(err)
		return
	}

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
			c.WriteErr(errors.New("conn not found"))
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
		c.WriteErr(errors.New("conn not found"))
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

func (s *Server) handleUserAction(c *wkserver.Context) {
	actions := UserActionSet{}
	err := actions.Unmarshal(c.Body())
	if err != nil {
		s.Error("handleUserAction Unmarshal err", zap.Error(err))
		c.WriteErr(err)
		return
	}

	if len(actions) == 0 {
		c.WriteOk()
		return
	}
	// actions 是同一批uid的操作，所以这里取第一个action的uid判断即可
	firstAction := actions[0]
	uid := firstAction.Uid
	leaderId, err := s.cluster.SlotLeaderIdOfChannel(uid, wkproto.ChannelTypePerson)
	if err != nil {
		s.Error("get leaderId failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	if leaderId != s.opts.Cluster.NodeId { // 当前节点不是leader
		s.Error("not is leader", zap.Uint64("leaderId", leaderId), zap.Uint64("currentNodeId", s.opts.Cluster.NodeId))
		c.WriteErrorAndStatus(err, proto.Status(errCodeNotIsUserLeader))
		return
	}
	sub := s.userReactor.reactorSub(uid)
	for _, action := range actions {
		userHandler := sub.getUser(uid)
		if userHandler == nil {
			uh := newUserHandler(uid, sub)
			sub.addUserIfNotExist(uh)
		}
		sub.step(action.Uid, action)
	}
	c.WriteOk()

}

func (s *Server) handleUserAuthResult(c *wkserver.Context) {
	authResult := &UserAuthResult{}
	err := authResult.Unmarshal(c.Body())
	if err != nil {
		s.Error("handleUserAuthResult Unmarshal err", zap.Error(err))
		c.WriteErr(err)
		return
	}

	sub := s.userReactor.reactorSub(authResult.Uid)
	connCtx := sub.getConnContextById(authResult.Uid, authResult.ConnId)
	if connCtx == nil {
		s.Error("handleUserAuthResult: conn not found", zap.String("uid", authResult.Uid), zap.Int64("connId", authResult.ConnId))
		c.WriteErrorAndStatus(err, proto.Status_NotFound)
		return
	}
	if connCtx.deviceId != authResult.DeviceId {
		s.Error("handleUserAuthResult: deviceId not match", zap.String("expect", connCtx.deviceId), zap.String("act", authResult.DeviceId))
		c.WriteErrorAndStatus(err, proto.Status_NotFound)
		return
	}

	if authResult.ReasonCode == wkproto.ReasonSuccess {
		connCtx.aesIV = authResult.AesIV
		connCtx.aesKey = authResult.AesKey
		connCtx.deviceLevel = authResult.DeviceLevel
		connCtx.deviceId = authResult.DeviceId
		connCtx.protoVersion = authResult.ProtoVersion
		connCtx.isAuth.Store(true)
		if connCtx.isRealConn {
			connCtx.conn.SetMaxIdle(s.opts.ConnIdleTime)
		}
		fmt.Println("authResult.ProtoVersion--->", authResult.ProtoVersion, s.opts.Cluster.NodeId)
		connack := &wkproto.ConnackPacket{
			ServerVersion: authResult.ProtoVersion,
			ServerKey:     authResult.ServerKey,
			Salt:          authResult.AesIV,
			ReasonCode:    authResult.ReasonCode,
			NodeId:        s.opts.Cluster.NodeId,
		}
		connack.HasServerVersion = authResult.ProtoVersion > 3 // 如果协议版本大于3，就返回serverVersion
		s.response(connCtx, connack)
	} else {
		connCtx.isAuth.Store(false)
		s.response(connCtx, &wkproto.ConnackPacket{
			ReasonCode: authResult.ReasonCode,
			NodeId:     s.opts.Cluster.NodeId,
		})
	}

	c.WriteOk()

}

func (s *Server) handleDeliver(c *wkserver.Context) {
	var channelMsgSet ChannelMessagesSet
	err := channelMsgSet.Unmarshal(c.Body())
	if err != nil {
		s.Error("handleDeliver Unmarshal err", zap.Error(err))
		c.WriteErr(err)
		return
	}
	for _, channelMsg := range channelMsgSet {
		s.deliverManager.deliver(&deliverReq{
			channelId:   channelMsg.ChannelId,
			channelType: channelMsg.ChannelType,
			channelKey:  ChannelToKey(channelMsg.ChannelId, channelMsg.ChannelType),
			messages:    channelMsg.Messages,
			tagKey:      channelMsg.TagKey,
		})
	}
	c.WriteOk()
}

func (s *Server) getNodeUidsByTag(c *wkserver.Context) {
	req := &tagReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		s.Error("getNodeUidsByTag Unmarshal err", zap.Error(err))
		c.WriteErr(err)
		return
	}

	if req.channelId == "" {
		c.WriteErr(ErrChannelIdIsEmpty)
		return
	}

	if req.nodeId == 0 {
		c.WriteErr(errors.New("node is 0"))
		return
	}

	if req.tagKey == "" {
		c.WriteErr(errors.New("tagKey is nil"))
		return
	}

	isLeader, err := s.cluster.IsLeaderOfChannel(s.ctx, req.channelId, req.channelType)
	if err != nil {
		s.Error("getNodeUidsByTag: IsLeaderOfChannel failed", zap.Error(err), zap.String("channelId", req.channelId), zap.Uint8("channelType", req.channelType))
		c.WriteErr(err)
		return
	}
	if !isLeader {
		s.Error("getNodeUidsByTag: not is leader", zap.String("channelId", req.channelId), zap.Uint8("channelType", req.channelType))
		c.WriteErrorAndStatus(err, proto.Status(errCodeNotIsChannelLeader))
		return
	}

	tag := s.tagManager.getReceiverTag(req.tagKey)
	if tag == nil {
		ch := s.channelReactor.loadOrCreateChannel(req.channelId, req.channelType)
		tag, err = ch.makeReceiverTag()
		if err != nil {
			s.Error("getNodeUidsByTag: makeReceiverTag failed", zap.Error(err), zap.String("channelId", req.channelId), zap.Uint8("channelType", req.channelType))
			c.WriteErr(err)
			return
		}
	}

	var uids []string
	for _, nodeUser := range tag.users {
		if nodeUser.nodeId == req.nodeId {
			uids = nodeUser.uids
			break
		}
	}
	var resp = &tagResp{
		tagKey: tag.key,
		uids:   uids,
	}
	c.Write(resp.Marshal())

}
