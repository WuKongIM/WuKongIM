package server

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/server/cluster/rpc"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

func (p *Processor) SetRoutes() {
	p.s.cluster.Route("/wk/connect", p.handleConnectReq)
	p.s.cluster.Route("/wk/recvPacket", p.handleOnRecvPacketReq)
	p.s.cluster.Route("/wk/sendPacket", p.handleOnSendPacketReq)
	p.s.cluster.Route("/wk/connPing", p.handleOnConnPingReq)
	p.s.cluster.Route("/wk/recvackPacket", p.handleOnRecvackPacketReq)
}

func (p *Processor) handleClusterMessage(from uint64, msg *proto.Message) {
	switch ClusterMsgType(msg.MsgType) {
	case ClusterMsgTypeConnWrite: // 远程连接写入
		p.handleConnWrite(from, msg)
	case ClusterMsgTypeConnClose:
		p.handleConnClose(from, msg)
	}
}

func (p *Processor) handleConnectReq(c *wkserver.Context) {
	var req = &rpc.ConnectReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		p.Error("unmarshal connectReq err", zap.Error(err))
		c.WriteErr(err)
		return
	}
	from, err := p.getFrom(c)
	if err != nil {
		p.Error("get from err", zap.Error(err))
		c.WriteErr(err)
		return
	}
	leaderId, err := p.s.cluster.SlotLeaderIdOfChannel(req.Uid, wkproto.ChannelTypePerson)
	if err != nil {
		p.Error("IsLeaderNodeOfChannel err", zap.Error(err))
		c.WriteErr(err)
		return
	}
	if leaderId == from {
		p.Error("请求的节点已经是领导节点, handleConnectReq failed", zap.String("uid", req.Uid))
		c.WriteErr(fmt.Errorf("请求的节点已经是领导节点"))
		return
	}
	if leaderId != p.s.opts.Cluster.PeerID {
		p.Error("当前节点不是领导节点, handleConnectReq failed", zap.String("uid", req.Uid))
		c.WriteErr(fmt.Errorf("当前节点不是领导节点"))
		return
	}

	resp, err := p.OnConnectReq(req)
	if err != nil {
		p.Error("onConnectReq err", zap.Error(err))
		c.WriteErr(err)
		return
	}
	data, err := resp.Marshal()
	if err != nil {
		p.Error("marshal connectResp err", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.Write(data)
}

func (p *Processor) handleOnRecvPacketReq(c *wkserver.Context) {
	var req = &rpc.ForwardRecvPacketReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		p.Error("unmarshal forwardRecvPacketReq err", zap.Error(err))
		c.WriteErr(err)
		return
	}
	startTime := time.Now().UnixMilli()
	p.Debug("收到转发的RecvPacket", zap.String("fromNodeID", c.Conn().UID()))
	err = p.OnRecvPacket(req)
	if err != nil {
		p.Error("onRecvPacket err", zap.Error(err))
		c.WriteErr(err)
		return
	}
	p.Debug("处理RecvPacket耗时", zap.Int64("cost", time.Now().UnixMilli()-startTime))
	c.WriteOk()
}

func (p *Processor) handleOnSendPacketReq(c *wkserver.Context) {
	var req = &rpc.ForwardSendPacketReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		p.Error("unmarshal forwardSendPacketReq err", zap.Error(err))
		c.WriteErr(err)
		return
	}
	var (
		fakeChannelID = req.ChannelID
	)
	if uint8(req.ChannelType) == wkproto.ChannelTypePerson {
		fakeChannelID = GetFakeChannelIDWith(req.FromUID, req.ChannelID)
	}
	isLeader, err := p.s.cluster.IsSlotLeaderOfChannel(fakeChannelID, uint8(req.ChannelType))
	if err != nil {
		p.Error("IsLeaderNodeOfChannel err", zap.Error(err))
		c.WriteErr(err)
		return
	}
	if !isLeader {
		p.Error("当前节点不是领导节点, handleOnSendPacketReq failed", zap.String("channel", req.ChannelID), zap.Uint32("channelType", req.ChannelType))
		c.WriteErr(fmt.Errorf("当前节点不是领导节点"))
		return
	}

	startTime := time.Now().UnixMilli()
	p.Debug("收到转发的SendPacket", zap.String("fromNodeID", c.Conn().UID()))
	resp, err := p.OnSendPacket(req)
	if err != nil {
		p.Error("onSendPacket err", zap.Error(err))
		c.WriteErr(err)
		return
	}
	data, err := resp.Marshal()
	if err != nil {
		p.Error("marshal forwardSendPacketResp err", zap.Error(err))
		c.WriteErr(err)
		return
	}
	p.Debug("处理SendPacket耗时", zap.Int64("cost", time.Now().UnixMilli()-startTime))
	c.Write(data)
}

func (p *Processor) handleConnWrite(from uint64, msg *proto.Message) {
	var req = &rpc.ConnectWriteReq{}
	err := req.Unmarshal(msg.Content)
	if err != nil {
		p.Error("unmarshal connectWriteReq err", zap.Error(err))
		return
	}
	_, err = p.OnConnectWriteReq(req)
	if err != nil {
		p.Error("onConnectWriteReq err", zap.Error(err))
		return
	}
}

func (p *Processor) handleConnClose(from uint64, msg *proto.Message) {
	var req = &rpc.ConnectCloseReq{}
	err := req.Unmarshal(msg.Content)
	if err != nil {
		p.Error("unmarshal connectCloseReq err", zap.Error(err))
		return
	}
	p.OnConnectCloseReq(req)
}

// func (p *Processor) handleOnConnectWriteReq(c *wkserver.Context) {
// 	var req = &rpc.ConnectWriteReq{}
// 	err := req.Unmarshal(c.Body())
// 	if err != nil {
// 		p.Error("unmarshal connectWriteReq err", zap.Error(err))
// 		c.WriteErr(err)
// 		return
// 	}
// 	status, err := p.OnConnectWriteReq(req)
// 	if err != nil {
// 		p.Error("onConnectWriteReq err", zap.Error(err))
// 		c.WriteErr(err)
// 		return
// 	}
// 	c.WriteStatus(status)
// }

func (p *Processor) handleOnConnPingReq(c *wkserver.Context) {
	var req = &rpc.ConnPingReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		p.Error("unmarshal connPingReq err", zap.Error(err))
		c.WriteErr(err)
		return
	}
	status, err := p.OnConnPingReq(req)
	if err != nil {
		p.Error("onConnPingReq err", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.WriteStatus(status)
}

func (p *Processor) handleOnRecvackPacketReq(c *wkserver.Context) {
	var req = &rpc.RecvacksReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		p.Error("unmarshal recvacksReq err", zap.Error(err))
		c.WriteErr(err)
		return
	}
	err = p.OnRecvackPacket(req)
	if err != nil {
		p.Error("onRecvackPacket err", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.WriteOk()
}

func (p *Processor) getFrom(c *wkserver.Context) (uint64, error) {
	return strconv.ParseUint(c.Conn().UID(), 10, 64)
}

func (p *Processor) OnConnectReq(req *rpc.ConnectReq) (*rpc.ConnectResp, error) {

	frame, _, err := p.s.opts.Proto.DecodeFrame(req.ConnectPacketData, wkproto.LatestVersion)
	if err != nil {
		return nil, err
	}
	connectPacket := frame.(*wkproto.ConnectPacket)
	var (
		uid         = connectPacket.UID
		devceLevel  wkproto.DeviceLevel
		devceLevelI uint8
		token       string
	)

	if p.s.opts.TokenAuthOn {
		if connectPacket.Token == "" {
			p.Error("token is empty")
			return &rpc.ConnectResp{
				ReasonCode: uint32(wkproto.ReasonAuthFail),
			}, nil
		}
		token, devceLevelI, err = p.s.store.GetUserToken(uid, connectPacket.DeviceFlag.ToUint8())
		if err != nil {
			p.Error("get user token err", zap.Error(err))
			return &rpc.ConnectResp{
				ReasonCode: uint32(wkproto.ReasonSystemError),
			}, nil

		}
		if token != connectPacket.Token {
			p.Error("token verify fail", zap.String("expectToken", token), zap.String("actToken", connectPacket.Token))
			return &rpc.ConnectResp{
				ReasonCode: uint32(wkproto.ReasonAuthFail),
			}, nil
		}
		devceLevel = wkproto.DeviceLevel(devceLevelI)
	} else {
		devceLevel = wkproto.DeviceLevelSlave // 默认都是slave设备
	}
	// -------------------- ban  --------------------
	userChannelInfo, err := p.s.store.GetChannel(uid, wkproto.ChannelTypePerson)
	if err != nil {
		p.Error("get user channel info err", zap.Error(err))
		return &rpc.ConnectResp{
			ReasonCode: uint32(wkproto.ReasonSystemError),
		}, nil
	}
	ban := false
	if userChannelInfo != nil {
		ban = userChannelInfo.Ban
	}
	if ban {
		p.Error("user is ban", zap.String("uid", uid))
		return &rpc.ConnectResp{
			ReasonCode: uint32(wkproto.ReasonBan),
		}, nil
	}

	// -------------------- get message encrypt key --------------------
	dhServerPrivKey, dhServerPublicKey := wkutil.GetCurve25519KeypPair() // 生成服务器的DH密钥对
	aesKey, aesIV, err := p.getClientAesKeyAndIV(connectPacket.ClientKey, dhServerPrivKey)
	if err != nil {
		p.Error("get client aes key and iv err", zap.Error(err))
		return &rpc.ConnectResp{
			ReasonCode: uint32(wkproto.ReasonSystemError),
		}, nil
	}
	dhServerPublicKeyEnc := base64.StdEncoding.EncodeToString(dhServerPublicKey[:])

	proxyConn := NewProxyClientConn(p.s, req.BelongPeerID)

	connCtx := newConnContext(p.s)
	connCtx.init()
	connCtx.conn = proxyConn
	proxyConn.SetContext(connCtx)
	proxyConn.SetID(p.s.dispatch.engine.GenClientID())
	proxyConn.SetProtoVersion(int(connectPacket.Version))
	proxyConn.SetAuthed(true)
	proxyConn.SetDeviceFlag(connectPacket.DeviceFlag.ToUint8())
	proxyConn.SetDeviceID(connectPacket.DeviceID)
	proxyConn.SetUID(connectPacket.UID)
	proxyConn.SetValue(aesKeyKey, aesKey)
	proxyConn.SetValue(aesIVKey, aesIV)
	proxyConn.SetDeviceLevel(devceLevelI)
	proxyConn.SetMaxIdle(p.s.opts.ConnIdleTime)

	p.s.connManager.AddConn(proxyConn)

	// -------------------- user online --------------------
	// 在线webhook
	onlineCount, totalOnlineCount := p.s.connManager.GetConnCountWith(uid, connectPacket.DeviceFlag)
	p.s.webhook.Online(uid, connectPacket.DeviceFlag, proxyConn.ID(), onlineCount, totalOnlineCount)

	return &rpc.ConnectResp{
		ReasonCode:      uint32(wkproto.ReasonSuccess),
		DeviceLevel:     uint32(devceLevel),
		AesKey:          aesKey,
		AesIV:           aesIV,
		ServerPublicKey: dhServerPublicKeyEnc,
	}, nil
}

func (p *Processor) OnGetSubscribers(channelID string, channelType uint8) ([]string, error) {

	return nil, nil
}

func (p *Processor) OnRecvPacket(req *rpc.ForwardRecvPacketReq) error {

	if req.ProtoVersion > wkproto.LatestVersion {
		p.Error("proto version not support", zap.Uint32("requestVersion", req.ProtoVersion), zap.Uint32("latestVersion", wkproto.LatestVersion))
		return nil
	}
	messages := make([]*Message, 0)

	messageDatas := req.Messages
	var channel *wkproto.Channel
	for len(messageDatas) > 0 {
		f, size, err := p.s.opts.Proto.DecodeFrame(messageDatas, uint8(req.ProtoVersion))
		if err != nil {
			p.Error("decode recvPacket err", zap.Error(err))
			return err
		}
		recvPacket := f.(*wkproto.RecvPacket)
		messageDatas = messageDatas[size:]

		m := &Message{
			RecvPacket:     recvPacket,
			fromDeviceFlag: wkproto.DeviceFlag(req.FromDeviceFlag),
			fromDeviceID:   req.FromDeviceID,
			large:          req.Large,
		}
		messages = append(messages, m)
		if channel == nil {
			channel = &wkproto.Channel{
				ChannelID:   recvPacket.ChannelID,
				ChannelType: recvPacket.ChannelType,
			}
		}
	}
	return p.handleLocalSubscribersMessages(messages, req.Large, req.Subscribers, req.FromUID, wkproto.DeviceFlag(req.FromDeviceFlag), req.FromDeviceID, channel)
}

// OnSendPacket 领导节点收到发送数据请求
func (p *Processor) OnSendPacket(req *rpc.ForwardSendPacketReq) (*rpc.ForwardSendPacketResp, error) {
	sendackPackets, err := p.prcocessChannelMessagesForRemote(req)
	if err != nil {
		p.Error("prcocessChannelMessagesForRemote err", zap.Error(err))
		return nil, err
	}

	sendackPacketDatas := make([]byte, 0)
	if len(sendackPackets) > 0 {
		for _, sendackPacket := range sendackPackets {
			data, err := p.s.opts.Proto.EncodeFrame(sendackPacket, uint8(req.ProtoVersion))
			if err != nil {
				p.Error("encode sendackPacket err", zap.Error(err))
				return nil, err
			}
			sendackPacketDatas = append(sendackPacketDatas, data...)
		}
	}

	return &rpc.ForwardSendPacketResp{
		SendackPackets: sendackPacketDatas,
	}, nil
}

// OnConnectWriteReq 领导节点写回数据到连接所在节点
func (p *Processor) OnConnectWriteReq(req *rpc.ConnectWriteReq) (proto.Status, error) {
	conn := p.s.connManager.GetConnWithDeviceID(req.Uid, req.DeviceId)
	if conn == nil {
		p.Warn("conn not exist", zap.String("uid", req.Uid), zap.String("deviceId", req.DeviceId))
		return proto.Status_NotFound, nil
	}
	if len(req.Data) == 0 {
		p.Warn("conn write data is empty", zap.String("uid", req.Uid), zap.String("deviceId", req.DeviceId))
		return proto.Status_OK, nil
	}
	reminData := req.Data
	for len(reminData) > 0 {
		f, size, err := p.s.opts.Proto.DecodeFrame(reminData, uint8(conn.ProtoVersion()))
		if err != nil {
			p.Error("decode connectWriteReq err", zap.Error(err))
		}
		recv, ok := f.(*wkproto.RecvPacket)
		if ok {
			p.Debug("收到转发的RecvPacket", zap.Uint32("messageSeq", recv.MessageSeq))
		}
		reminData = reminData[size:]
	}
	p.s.dispatch.dataOut(conn, req.Data)
	return proto.Status_OK, nil
}

func (p *Processor) OnConnectCloseReq(req *rpc.ConnectCloseReq) {
	conn := p.s.connManager.GetConnWithDeviceID(req.Uid, req.DeviceId)
	if conn == nil {
		p.Warn("OnConnectCloseReq: conn not exist", zap.String("uid", req.Uid), zap.String("deviceId", req.DeviceId))
		return
	}
	p.processClose(conn)
}

// OnConnPingReq 领导节点收到连接ping
func (p *Processor) OnConnPingReq(req *rpc.ConnPingReq) (proto.Status, error) {
	p.Debug("收到Ping", zap.Uint64("fromNodeID", req.BelongPeerID), zap.String("uid", req.Uid), zap.String("deviceId", req.DeviceId))
	conn := p.s.connManager.GetConn(req.Uid, req.DeviceId)
	if conn == nil {
		p.Warn("conn not exist", zap.String("uid", req.Uid), zap.String("deviceId", req.DeviceId), zap.Uint64("belongPeerID", req.BelongPeerID))
		return proto.Status_NotFound, nil
	}
	proxyConn, ok := conn.(*ProxyClientConn)
	if ok && proxyConn != nil {
		proxyConn.KeepLastActivity()
	}

	return proto.Status_OK, nil
}

// func (p *Processor) OnSendSyncProposeReq(req *rpc.SendSyncProposeReq) (*rpc.SendSyncProposeResp, error) {
// 	data, err := p.s.clusterServer.SyncProposeToSlot(req.Slot, req.Data)
// 	if err != nil {
// 		p.Error("onSendSyncProposeReq sync propose to slot err", zap.Error(err))
// 		return nil, err
// 	}

// 	return &rpc.SendSyncProposeResp{
// 		Slot: req.Slot,
// 		Data: data,
// 	}, nil
// }

func (p *Processor) OnRecvackPacket(req *rpc.RecvacksReq) error {
	proxyConn := p.s.connManager.GetConn(req.Uid, req.DeviceId)
	if proxyConn == nil {
		p.Warn("conn not exist", zap.String("uid", req.Uid), zap.String("deviceId", req.DeviceId), zap.Uint64("belongPeerID", req.BelongPeerID))
		return nil
	}
	ackDatas := req.Data
	ackPackets := make([]*wkproto.RecvackPacket, 0)
	for len(ackDatas) > 0 {
		f, size, err := p.s.opts.Proto.DecodeFrame(ackDatas, uint8(proxyConn.ProtoVersion()))
		if err != nil {
			p.Error("decode recvPacket err", zap.Error(err))
			return err
		}
		ackPacket := f.(*wkproto.RecvackPacket)
		ackDatas = ackDatas[size:]
		ackPackets = append(ackPackets, ackPacket)
	}
	for _, ack := range ackPackets {
		persist := !ack.NoPersist
		if persist {
			// 完成消息（移除重试队列里的消息）
			p.Debug("移除重试队列里的消息！proxyConn", zap.Uint32("messageSeq", ack.MessageSeq), zap.String("uid", proxyConn.UID()), zap.Int64("clientID", proxyConn.ID()), zap.Uint8("deviceFlag", proxyConn.DeviceFlag()), zap.Uint8("deviceLevel", proxyConn.DeviceLevel()), zap.String("deviceID", proxyConn.DeviceID()), zap.Bool("syncOnce", ack.SyncOnce), zap.Bool("noPersist", ack.NoPersist), zap.Int64("messageID", ack.MessageID))
			err := p.s.retryQueue.finishMessage(proxyConn.UID(), proxyConn.DeviceID(), ack.MessageID)
			if err != nil {
				p.Warn("移除重试队列里的消息失败！", zap.Error(err), zap.Uint32("messageSeq", ack.MessageSeq), zap.String("uid", proxyConn.UID()), zap.Int64("clientID", proxyConn.ID()), zap.Uint8("deviceFlag", proxyConn.DeviceFlag()), zap.String("deviceID", proxyConn.DeviceID()), zap.Int64("messageID", ack.MessageID))
			}
		}
		if ack.SyncOnce && persist && wkproto.DeviceLevel(proxyConn.DeviceLevel()) == wkproto.DeviceLevelMaster { // 写扩散和存储并且是master等级的设备才会更新游标
			p.Debug("更新游标", zap.String("uid", proxyConn.UID()), zap.Uint32("messageSeq", ack.MessageSeq))
			err := p.s.store.UpdateMessageOfUserCursorIfNeed(proxyConn.UID(), ack.MessageSeq)
			if err != nil {
				p.Warn("更新游标失败！", zap.Error(err), zap.String("uid", proxyConn.UID()), zap.Uint32("messageSeq", ack.MessageSeq))
			}
		}
	}

	return nil
}

// 处理本地订阅者消息
func (p *Processor) handleLocalSubscribersMessages(messages []*Message, large bool, subscribers []string, fromUID string, fromDeviceFlag wkproto.DeviceFlag, fromDeviceID string, channel *wkproto.Channel) error {
	if len(subscribers) == 0 {
		return nil
	}
	var err error
	//########## store messages in user queue ##########
	var messageSeqMap map[string]uint32
	if len(messages) > 0 {
		messageSeqMap, err = p.storeMessageToUserQueueIfNeed(messages, subscribers, large)
		if err != nil {
			return err
		}
		for _, message := range messages {
			seq := messageSeqMap[fmt.Sprintf("%s-%d", message.ToUID, message.MessageID)]
			if seq != 0 {
				message.MessageSeq = seq
			}
		}
	}

	// ########## update conversation ##########
	if !large && channel.ChannelType != wkproto.ChannelTypeInfo { // 如果是大群 则不维护最近会话 几万人的大群，更新最近会话也太耗性能
		var lastMsg *Message
		for i := len(messages) - 1; i >= 0; i-- {
			m := messages[i]
			if !m.NoPersist && !m.SyncOnce {
				lastMsg = messages[i]
				break
			}
		}
		if lastMsg != nil {
			p.updateConversations(lastMsg, subscribers)
		}
	}

	//########## delivery messages ##########
	for _, m := range messages {
		fmt.Println("delivery messages----->", m.MessageSeq)

	}
	p.s.deliveryManager.startDeliveryMessages(messages, large, messageSeqMap, subscribers, fromUID, fromDeviceFlag, fromDeviceID)
	return nil
}

// store message to user queue if need
func (p *Processor) storeMessageToUserQueueIfNeed(messages []*Message, subscribers []string, large bool) (map[string]uint32, error) {
	if len(messages) == 0 {
		return nil, nil
	}

	messageSeqMap := make(map[string]uint32, len(messages))

	for _, subscriber := range subscribers {
		storeMessages := make([]wkstore.Message, 0, len(messages))
		for _, m := range messages {

			if m.NoPersist || !m.SyncOnce {
				continue
			}

			cloneMsg, err := m.DeepCopy()
			if err != nil {
				return nil, err
			}
			cloneMsg.ToUID = subscriber
			cloneMsg.large = large
			if m.ChannelType == wkproto.ChannelTypePerson && m.ChannelID == subscriber {
				cloneMsg.ChannelID = m.FromUID
			}
			storeMessages = append(storeMessages, cloneMsg)
		}
		if len(storeMessages) > 0 {
			err := p.s.store.AppendMessagesOfUser(subscriber, storeMessages) // will fill messageSeq after store messages
			if err != nil {
				return nil, err
			}
			for _, storeMessage := range storeMessages {
				messageSeqMap[fmt.Sprintf("%s-%d", subscriber, storeMessage.GetMessageID())] = storeMessage.GetSeq()
			}
		}
	}
	return messageSeqMap, nil
}

func (p *Processor) updateConversations(m *Message, subscribers []string) {
	fmt.Println("updateConversations------------------->")
	p.s.conversationManager.PushMessage(m, subscribers)

}
