package server

import (
	"encoding/base64"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/server/cluster/rpc"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

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

	fmt.Println("connectReq--->", req)
	proxyConn := NewProxyClientConn(p.s, req.BelongPeerID, req.ConnID)

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
		messages = append(messages, &Message{
			RecvPacket:     recvPacket,
			fromDeviceFlag: wkproto.DeviceFlag(req.FromDeviceFlag),
			fromDeviceID:   req.FromDeviceID,
			large:          req.Large,
		})
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
		p.Error("prcocessChannelMessagesForLocal err", zap.Error(err))
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
func (p *Processor) OnConnectWriteReq(req *rpc.ConnectWriteReq) (rpc.Status, error) {
	conn := p.s.connManager.GetConn(req.ConnID)
	if conn == nil {
		p.Warn("conn not exist", zap.Int64("connID", req.ConnID))
		return rpc.Status_NotFound, nil
	}
	if conn.UID() != req.Uid && conn.DeviceFlag() != uint8(req.DeviceFlag) {
		p.Warn("conn uid or deviceFlag not match", zap.Int64("connID", req.ConnID), zap.String("connUID", conn.UID()), zap.Uint8("connDeviceFlag", conn.DeviceFlag()), zap.String("reqUID", req.Uid), zap.Uint8("reqDeviceFlag", uint8(req.DeviceFlag)))
		return rpc.Status_NotFound, nil

	}
	if len(req.Data) == 0 {
		p.Warn("conn write data is empty", zap.Int64("connID", req.ConnID))
		return rpc.Status_Success, nil
	}
	p.s.dispatch.dataOut(conn, req.Data)
	return rpc.Status_Success, nil
}

// OnConnPingReq 领导节点收到连接ping
func (p *Processor) OnConnPingReq(req *rpc.ConnPingReq) (rpc.Status, error) {
	proxyConn := p.s.connManager.GetProxyConn(req.BelongPeerID, req.ConnID)
	if proxyConn == nil {
		p.Warn("conn not exist", zap.Int64("connID", req.ConnID), zap.Uint64("belongPeerID", req.BelongPeerID))
		return rpc.Status_NotFound, nil
	}
	proxyConn.KeepLastActivity()
	return rpc.Status_Success, nil
}

func (p *Processor) OnSendSyncProposeReq(req *rpc.SendSyncProposeReq) (*rpc.SendSyncProposeResp, error) {
	data, err := p.s.clusterServer.SyncProposeToSlot(req.Slot, req.Data)
	if err != nil {
		p.Error("onSendSyncProposeReq sync propose to slot err", zap.Error(err))
		return nil, err
	}

	return &rpc.SendSyncProposeResp{
		Slot: req.Slot,
		Data: data,
	}, nil
}

// 处理本地订阅者消息
func (p *Processor) handleLocalSubscribersMessages(messages []*Message, large bool, subscribers []string, fromUID string, fromDeviceFlag wkproto.DeviceFlag, fromDeviceID string, channel *wkproto.Channel) error {
	if len(subscribers) == 0 {
		return nil
	}
	var err error
	//########## store messages in user queue ##########
	var messageSeqMap map[int64]uint32
	if len(messages) > 0 {
		messageSeqMap, err = p.storeMessageToUserQueueIfNeed(messages, subscribers, large)
		if err != nil {
			return err
		}
		for _, message := range messages {
			message.MessageSeq = messageSeqMap[message.MessageID]
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
	p.s.deliveryManager.startDeliveryMessages(messages, large, messageSeqMap, subscribers, fromUID, fromDeviceFlag, fromDeviceID)
	return nil
}

// store message to user queue if need
func (p *Processor) storeMessageToUserQueueIfNeed(messages []*Message, subscribers []string, large bool) (map[int64]uint32, error) {
	if len(messages) == 0 {
		return nil, nil
	}

	messageSeqMap := make(map[int64]uint32, len(messages))

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
			_, err := p.s.store.AppendMessagesOfUser(subscriber, storeMessages) // will fill messageSeq after store messages
			if err != nil {
				return nil, err
			}
			for _, storeMessage := range storeMessages {
				messageSeqMap[storeMessage.GetMessageID()] = storeMessage.GetSeq()
			}
		}
	}
	return messageSeqMap, nil
}

func (p *Processor) updateConversations(m *Message, subscribers []string) {
	p.s.conversationManager.PushMessage(m, subscribers)

}
