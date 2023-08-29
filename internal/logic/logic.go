package logic

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/RussellLuo/timingwheel"
	"github.com/WuKongIM/WuKongIM/internal/gatewaycommon"
	"github.com/WuKongIM/WuKongIM/internal/model"
	"github.com/WuKongIM/WuKongIM/internal/monitor"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/bwmarrin/snowflake"
	"go.uber.org/zap"
)

type Logic struct {
	store wkstore.Store
	wklog.Log
	gatewayManager   *gatewayManager
	frameWorkPool    *FrameWorkPool
	framePool        *FramePool // 对象池
	opts             *options.Options
	messageIDGen     *snowflake.Node   // 消息ID生成器
	systemUIDManager *SystemUIDManager // System uid management, system uid can send messages to everyone without any restrictions
	datasource       IDatasource       // 数据源（提供数据源 订阅者，黑名单，白名单这些数据可以交由第三方提供）
	channelManager   *ChannelManager   // channel manager

	monitor       monitor.IMonitor         // Data monitoring
	monitorServer *MonitorServer           // 监控服务
	timingWheel   *timingwheel.TimingWheel // Time wheel delay task

	retryQueue          *RetryQueue          // retry queue
	conversationManager *ConversationManager // conversation manager
	deliveryManager     *DeliveryManager     // 消息投递管理
	webhook             *Webhook             // webhook
}

func NewLogic(opts *options.Options) *Logic {
	storeCfg := wkstore.NewStoreConfig()
	storeCfg.DataDir = opts.DataDir
	storeCfg.DecodeMessageFnc = func(msg []byte) (wkstore.Message, error) {
		m := &model.Message{}
		err := m.Decode(msg)
		return m, err
	}
	messageIDGen, err := snowflake.NewNode(int64(opts.Cluster.NodeID))
	if err != nil {
		panic(err)
	}
	s := &Logic{
		Log:            wklog.NewWKLog("Logic"),
		store:          wkstore.NewFileStore(storeCfg),
		frameWorkPool:  NewFrameWorkPool(),
		framePool:      NewFramePool(),
		opts:           opts,
		messageIDGen:   messageIDGen,
		timingWheel:    timingwheel.NewTimingWheel(opts.TimingWheelTick, opts.TimingWheelSize),
		gatewayManager: newGatewayManager(),
	}
	s.systemUIDManager = NewSystemUIDManager(s)
	s.datasource = NewDatasource(s)
	s.monitor = monitor.GetMonitor() // 监控
	s.monitorServer = NewMonitorServer(s)
	s.conversationManager = NewConversationManager(s)
	s.deliveryManager = NewDeliveryManager(s)
	s.retryQueue = NewRetryQueue(s)
	s.webhook = NewWebhook(s)
	s.channelManager = NewChannelManager(s)
	return s
}

func (l *Logic) Start() error {
	err := l.store.Open()
	if err != nil {
		return err
	}
	l.conversationManager.Start()
	l.webhook.Start()
	l.retryQueue.Start()

	if l.opts.Monitor.On {
		l.monitor.Start()
		l.monitorServer.Start()
	}
	return nil
}

func (l *Logic) Stop() {
	l.conversationManager.Stop()

	l.retryQueue.Stop()

	l.webhook.Stop()

	if l.opts.Monitor.On {
		l.monitorServer.Stop()
		l.monitor.Stop()
	}

	err := l.store.Close()
	if err != nil {
		l.Error("store close err", zap.Error(err))
	}

	// close(l.stopChan)

}

func (l *Logic) OnGatewayClientAuth(req *pb.AuthReq) (*pb.AuthResp, error) {
	var (
		uid         = req.Uid
		err         error
		devceLevelI uint8
		token       string
	)

	// -------------------- token check  --------------------
	if req.Uid == l.opts.ManagerUID {
		if l.opts.ManagerTokenOn && req.Token != l.opts.ManagerToken {
			l.Error("manager token verify fail", zap.String("uid", uid), zap.String("token", req.Token))
			return nil, errors.New("manager token verify fail")
		}
		devceLevelI = uint8(wkproto.DeviceLevelSlave) // 默认都是slave设备
	} else if l.opts.TokenAuthOn {
		if req.Token == "" {
			l.Error("token is empty")
			return nil, errors.New("token is empty")
		}
		token, devceLevelI, err = l.store.GetUserToken(uid, uint8(req.DeviceFlag))
		if err != nil {
			l.Error("get user token err", zap.Error(err))
			return nil, err
		}
		if token != req.Token {
			l.Error("token verify fail", zap.String("expectToken", token), zap.String("actToken", req.Token), zap.Any("conn", req))
			return nil, err
		}
	} else {
		devceLevelI = uint8(wkproto.DeviceLevelSlave) // 默认都是slave设备
	}

	// -------------------- ban  --------------------
	userChannelInfo, err := l.store.GetChannel(uid, wkproto.ChannelTypePerson)
	if err != nil {
		l.Error("get user channel info err", zap.Error(err))
		return nil, err
	}
	ban := false
	if userChannelInfo != nil {
		ban = userChannelInfo.Ban
	}
	if ban {
		l.Error("user is ban", zap.String("uid", uid))
		return nil, errors.New("user is ban")
	}

	// -------------------- get message encrypt key --------------------
	dhServerPrivKey, dhServerPublicKey := wkutil.GetCurve25519KeypPair() // 生成服务器的DH密钥对
	aesKey, aesIV, err := l.getClientAesKeyAndIV(req.ClientKey, dhServerPrivKey)
	if err != nil {
		l.Error("get client aes key and iv err", zap.Error(err))
		return nil, err
	}
	dhServerPublicKeyEnc := base64.StdEncoding.EncodeToString(dhServerPublicKey[:])

	// if l.opts.ClusterOn() { // 只有集群模式才需要添加conn，单机版不需要，因为单机版的connManager是使用的gateway的
	// 	conn := newClientConn(req.GatewayID, req.ConnID, uid, wkproto.DeviceFlag(req.DeviceFlag))
	// 	conn.deviceID = req.DeviceID
	// 	conn.deviceLevel = wkproto.DeviceLevel(devceLevelI)
	// 	conn.aesKey = aesKey
	// 	conn.aesIV = aesIV

	// 	gatewayCli := l.gatewayManager.get(req.GatewayID)
	// 	if gatewayCli != nil {
	// 		gatewayCli.AddConn(conn)
	// 	}
	// }
	return &pb.AuthResp{
		DeviceLevel:       uint32(devceLevelI),
		AesKey:            aesKey,
		AesIV:             aesIV,
		DhServerPublicKey: dhServerPublicKeyEnc,
	}, nil
}

func (l *Logic) OnGatewayClientClose(r *pb.ClientCloseReq) error {
	return nil
}

// OnGatewayClientWrite data write to  logic
func (l *Logic) OnGatewayClientWrite(conn *pb.Conn, data []byte) (int, error) {
	offset := 0
	frames := make([]wkproto.Frame, 0)
	for len(data) > offset {
		frame, size, err := l.opts.Proto.DecodeFrame(data[offset:], uint8(conn.ProtoVersion))
		if err != nil { //
			l.Warn("Failed to decode the message", zap.Error(err))
			return 0, err
		}
		if frame == nil {
			break
		}
		frames = append(frames, frame)
		offset += size
	}
	if len(frames) > 0 {
		err := l.handleFrames(conn, frames)
		if err != nil {
			return 0, err
		}
	}
	return offset, nil
}

func (l *Logic) handleFrames(conn *pb.Conn, frames []wkproto.Frame) error {
	fmt.Println("handleFrames---->", conn.String(), frames)
	gatewaycli := l.gatewayManager.get(conn.GatewayID)
	gatewayConn := gatewaycli.GetConn(conn.Id)
	if gatewayConn == nil {
		l.Error("gatewayConn is nil", zap.Int64("clientID", conn.Id))
		return gatewaycommon.ErrConnNotExist
	}

	SetProtoVersion(gatewayConn, int(conn.ProtoVersion))
	SetGatewayID(gatewayConn, conn.GatewayID)

	l.sameFrames(frames, func(s, e int, frs []wkproto.Frame) {

		l.frameWorkPool.Submit(func() { // 开启协程处理相同的frame
			l.processSameFrame(gatewayConn, frs[0].GetFrameType(), frs)
		})
	})
	return nil
}

// 获取客户端的aesKey和aesIV
// dhServerPrivKey  服务端私钥
func (l *Logic) getClientAesKeyAndIV(clientKey string, dhServerPrivKey [32]byte) (string, string, error) {

	clientKeyBytes, err := base64.StdEncoding.DecodeString(clientKey)
	if err != nil {
		return "", "", err
	}

	var dhClientPubKeyArray [32]byte
	copy(dhClientPubKeyArray[:], clientKeyBytes[:32])

	// 获得DH的共享key
	shareKey := wkutil.GetCurve25519Key(dhServerPrivKey, dhClientPubKeyArray) // 共享key

	aesIV := wkutil.GetRandomString(16)
	aesKey := wkutil.MD5(base64.StdEncoding.EncodeToString(shareKey[:]))[:16]
	return aesKey, aesIV, nil
}

func (l *Logic) processMsgs(conn gatewaycommon.Conn, sendPackets []*wkproto.SendPacket) {
	var (
		sendackPackets       = make([]wkproto.Frame, 0, len(sendPackets)) // response sendack packets
		channelSendPacketMap = make(map[string][]*wkproto.SendPacket, 0)  // split sendPacket by channel
		// recvPackets          = make([]wkproto.RecvPacket, 0, len(sendpPackets)) // recv packets
	)

	// ########## split sendPacket by channel ##########
	for _, sendPacket := range sendPackets {
		channelKey := fmt.Sprintf("%s-%d", sendPacket.ChannelID, sendPacket.ChannelType)
		channelSendpackets := channelSendPacketMap[channelKey]
		if channelSendpackets == nil {
			channelSendpackets = make([]*wkproto.SendPacket, 0, len(sendackPackets))
		}
		channelSendpackets = append(channelSendpackets, sendPacket)
		channelSendPacketMap[channelKey] = channelSendpackets
	}

	// ########## process message for channel ##########
	for _, sendPackets := range channelSendPacketMap {
		firstSendPacket := sendPackets[0]
		channelSendackPackets, err := l.prcocessChannelMessages(conn, firstSendPacket.ChannelID, firstSendPacket.ChannelType, sendPackets)
		if err != nil {
			l.Error("process channel messages err", zap.Error(err))
		}
		if len(channelSendackPackets) > 0 {
			sendackPackets = append(sendackPackets, channelSendackPackets...)
		}
	}
	l.response(conn, sendackPackets...)
}

func (l *Logic) prcocessChannelMessages(conn gatewaycommon.Conn, channelID string, channelType uint8, sendPackets []*wkproto.SendPacket) ([]wkproto.Frame, error) {
	var (
		sendackPackets        = make([]wkproto.Frame, 0, len(sendPackets))  // response sendack packets
		messages              = make([]*model.Message, 0, len(sendPackets)) // recv packets
		err                   error
		respSendackPacketsFnc = func(sendPackets []*wkproto.SendPacket, reasonCode wkproto.ReasonCode) []wkproto.Frame {
			for _, sendPacket := range sendPackets {
				sendackPackets = append(sendackPackets, l.getSendackPacketWithSendPacket(sendPacket, reasonCode))
			}
			return sendackPackets
		}
		respSendackPacketsWithRecvFnc = func(messages []*model.Message, reasonCode wkproto.ReasonCode) []wkproto.Frame {
			for _, m := range messages {
				sendackPackets = append(sendackPackets, l.getSendackPacket(m, reasonCode))
			}
			return sendackPackets
		}
	)

	//########## get channel and assert permission ##########
	fakeChannelID := channelID
	if channelType == wkproto.ChannelTypePerson {
		fakeChannelID = GetFakeChannelIDWith(conn.UID(), channelID)
	}
	channel, err := l.channelManager.GetChannel(fakeChannelID, channelType)
	if err != nil {
		l.Error("getChannel is error", zap.Error(err), zap.String("fakeChannelID", fakeChannelID), zap.Uint8("channelType", channelType))
		return respSendackPacketsFnc(sendPackets, wkproto.ReasonSystemError), nil
	}
	if channel == nil {
		l.Error("the channel does not exist or has been disbanded", zap.String("channel_id", fakeChannelID), zap.Uint8("channel_type", channelType))
		return respSendackPacketsFnc(sendPackets, wkproto.ReasonChannelNotExist), nil
	}
	hasPerm, reasonCode := l.hasPermission(channel, conn.UID())
	if !hasPerm {
		return respSendackPacketsFnc(sendPackets, reasonCode), nil
	}

	// ########## message decrypt and message store ##########
	for _, sendPacket := range sendPackets {
		var messageID = l.genMessageID() // generate messageID

		if sendPacket.SyncOnce { // client not support send syncOnce message
			sendackPackets = append(sendackPackets, &wkproto.SendackPacket{
				Framer:      sendPacket.Framer,
				ClientSeq:   sendPacket.ClientSeq,
				ClientMsgNo: sendPacket.ClientMsgNo,
				MessageID:   messageID,
				ReasonCode:  wkproto.ReasonNotSupportHeader,
			})
			continue
		}
		decodePayload, err := l.checkAndDecodePayload(messageID, sendPacket, conn)
		if err != nil {
			l.Error("decode payload err", zap.Error(err))
			l.response(conn, &wkproto.SendackPacket{
				Framer:      sendPacket.Framer,
				ClientSeq:   sendPacket.ClientSeq,
				ClientMsgNo: sendPacket.ClientMsgNo,
				MessageID:   messageID,
				ReasonCode:  wkproto.ReasonPayloadDecodeError,
			})
			continue
		}

		messages = append(messages, &model.Message{
			RecvPacket: &wkproto.RecvPacket{
				Framer: wkproto.Framer{
					RedDot:    sendPacket.GetRedDot(),
					SyncOnce:  sendPacket.GetsyncOnce(),
					NoPersist: sendPacket.GetNoPersist(),
				},
				Setting:     sendPacket.Setting,
				MessageID:   messageID,
				ClientMsgNo: sendPacket.ClientMsgNo,
				StreamNo:    sendPacket.StreamNo,
				StreamFlag:  wkproto.StreamFlagIng,
				FromUID:     conn.UID(),
				ChannelID:   sendPacket.ChannelID,
				ChannelType: sendPacket.ChannelType,
				Topic:       sendPacket.Topic,
				Timestamp:   int32(time.Now().Unix()),
				Payload:     decodePayload,
				// ---------- 以下不参与编码 ------------
				ClientSeq: sendPacket.ClientSeq,
			},
			// fromDeviceFlag: wkproto.DeviceFlag(conn.DeviceFlag()),
			// fromDeviceID:   conn.DeviceID(),
			// large:          channel.Large,
		})
	}
	if len(messages) == 0 {
		return sendackPackets, nil
	}
	err = l.storeChannelMessagesIfNeed(conn.UID(), messages) // only have messageSeq after message save
	if err != nil {
		return respSendackPacketsWithRecvFnc(messages, wkproto.ReasonSystemError), err
	}
	//########## message store to queue ##########
	if l.opts.WebhookOn() {
		err = l.storeChannelMessagesToNotifyQueue(messages)
		if err != nil {
			return respSendackPacketsWithRecvFnc(messages, wkproto.ReasonSystemError), err
		}
	}

	//########## message put to channel ##########
	err = channel.Put(messages, nil, conn.UID(), wkproto.DeviceFlag(conn.DeviceFlag()), conn.DeviceID())
	if err != nil {
		return respSendackPacketsWithRecvFnc(messages, wkproto.ReasonSystemError), err
	}

	//########## respose ##########
	if len(messages) > 0 {
		for _, message := range messages {
			sendackPackets = append(sendackPackets, l.getSendackPacket(message, wkproto.ReasonSuccess))
		}

	}
	return sendackPackets, nil
}

// #################### subscribe ####################
func (l *Logic) processSubs(conn gatewaycommon.Conn, subPackets []*wkproto.SubPacket) {
	fmt.Println("subPackets--->", len(subPackets))
	for _, subPacket := range subPackets {
		l.processSub(conn, subPacket)
	}
}

func (l *Logic) processSub(conn gatewaycommon.Conn, subPacket *wkproto.SubPacket) {

	channelIDUrl, err := url.Parse(subPacket.ChannelID)
	if err != nil {
		l.Warn("订阅的频道ID不合法！", zap.Error(err), zap.String("channelID", subPacket.ChannelID))
		return
	}
	channelID := channelIDUrl.Path
	if strings.TrimSpace(channelID) == "" {
		l.Warn("订阅的频道ID不能为空！", zap.String("channelID", subPacket.ChannelID))
		return
	}

	paramMap := map[string]interface{}{}

	values := channelIDUrl.Query()
	for key, value := range values {
		if len(value) > 0 {
			paramMap[key] = value[0]
		}
	}

	if subPacket.ChannelType != wkproto.ChannelTypeData {
		l.Warn("订阅的频道类型不正确！", zap.Uint8("channelType", subPacket.ChannelType))
		l.response(conn, l.getSuback(subPacket, channelID, wkproto.ReasonNotSupportChannelType))
		return
	}

	channel, err := l.channelManager.GetChannel(channelID, subPacket.ChannelType)
	if err != nil {
		l.Warn("获取频道失败！", zap.Error(err))
		l.response(conn, l.getSuback(subPacket, channelID, wkproto.ReasonSystemError))
		return
	}
	if channel == nil {
		l.Warn("频道不存在！", zap.String("channelID", channelID), zap.Uint8("channelType", subPacket.ChannelType))
		l.response(conn, l.getSuback(subPacket, channelID, wkproto.ReasonChannelNotExist))
		return
	}

	// connCtx := conn.Context().(*connContext)
	if subPacket.Action == wkproto.Subscribe {
		if strings.TrimSpace(subPacket.Param) != "" {
			paramM, _ := wkutil.JSONToMap(subPacket.Param)
			if len(paramM) > 0 {
				for k, v := range paramM {
					paramMap[k] = v
				}
			}
		}
		channel.AddSubscriber(conn.UID())
		// connCtx.subscribeChannel(channelID, subPacket.ChannelType, paramMap)
	} else {

		// if !l.connManager.ExistConnsWithUID(conn.UID()) {
		// 	channel.RemoveSubscriber(conn.UID())
		// }
		if subPacket.ChannelType == wkproto.ChannelTypeData && len(channel.GetAllSubscribers()) == 0 {
			l.channelManager.RemoveDataChannel(channelID, subPacket.ChannelType)
		}
		// connCtx.unscribeChannel(channelID, subPacket.ChannelType)
	}
	l.response(conn, l.getSuback(subPacket, channelID, wkproto.ReasonSuccess))
}

func (l *Logic) getSuback(subPacket *wkproto.SubPacket, channelID string, reasonCode wkproto.ReasonCode) *wkproto.SubackPacket {
	return &wkproto.SubackPacket{
		SubNo:       subPacket.SubNo,
		ChannelID:   channelID,
		ChannelType: subPacket.ChannelType,
		Action:      subPacket.Action,
		ReasonCode:  reasonCode,
	}
}

// #################### recv ack ####################
func (l *Logic) processRecvacks(conn gatewaycommon.Conn, acks []*wkproto.RecvackPacket) {
	if len(acks) == 0 {
		return
	}
	for _, ack := range acks {
		if !ack.NoPersist {
			// 完成消息（移除重试队列里的消息）
			err := l.retryQueue.finishMessage(GetGatewayID(conn), conn.ID(), ack.MessageID)
			if err != nil {
				l.Debug("移除重试队列里的消息失败！", zap.Error(err), zap.Uint32("messageSeq", ack.MessageSeq), zap.String("uid", conn.UID()), zap.Int64("clientID", conn.ID()), zap.Uint8("deviceFlag", conn.DeviceFlag()), zap.String("deviceID", conn.DeviceID()), zap.Int64("messageID", ack.MessageID))
			}
		}
		if ack.SyncOnce && !ack.NoPersist && wkproto.DeviceLevel(conn.DeviceLevel()) == wkproto.DeviceLevelMaster { // 写扩散和存储并且是master等级的设备才会更新游标
			err := l.store.UpdateMessageOfUserCursorIfNeed(conn.UID(), ack.MessageSeq)
			if err != nil {
				l.Warn("更新游标失败！", zap.Error(err), zap.String("uid", conn.UID()), zap.Uint32("messageSeq", ack.MessageSeq))
			}
		}
	}

}

// 处理同类型的frame集合
func (l *Logic) processSameFrame(conn gatewaycommon.Conn, frameType wkproto.FrameType, frames []wkproto.Frame) {
	switch frameType {
	case wkproto.PING: // ping
		// l.processPing(conn, frames[0].(*wkproto.PingPacket))
	case wkproto.SEND: // process send
		tmpFrames := l.framePool.GetSendPackets()
		for _, frame := range frames {
			tmpFrames = append(tmpFrames, frame.(*wkproto.SendPacket))
		}
		l.processMsgs(conn, tmpFrames)

		l.framePool.PutSendPackets(tmpFrames) // 注意：这里回收了，processMsgs里不要对tmpFrames进行异步操作，容易引起数据错乱

	case wkproto.RECVACK: // process recvack
		tmpFrames := l.framePool.GetRecvackPackets()
		for _, frame := range frames {
			tmpFrames = append(tmpFrames, frame.(*wkproto.RecvackPacket))
		}
		l.processRecvacks(conn, tmpFrames)

		l.framePool.PutRecvackPackets(tmpFrames)

	case wkproto.SUB: // 订阅
		tmpFrames := l.framePool.GetSubPackets()
		for _, frame := range frames {
			tmpFrames = append(tmpFrames, frame.(*wkproto.SubPacket))
		}
		l.processSubs(conn, tmpFrames)
		l.framePool.PutSubPackets(tmpFrames)
	}
}

// 将frames按照frameType分组，然后处理
func (l *Logic) sameFrames(frames []wkproto.Frame, callback func(s, e int, fs []wkproto.Frame)) {
	for i := 0; i < len(frames); {
		frame := frames[i]
		start := i
		end := i + 1
		for end < len(frames) {
			nextFrame := frames[end]
			if nextFrame.GetFrameType() == frame.GetFrameType() {
				end++
			} else {
				break
			}
		}
		callback(start, end, frames[start:end])
		i = end
	}
}

// if has permission for sender
func (l *Logic) hasPermission(channel *Channel, fromUID string) (bool, wkproto.ReasonCode) {
	if channel.ChannelType == wkproto.ChannelTypeCustomerService { // customer service channel
		return true, wkproto.ReasonSuccess
	}
	allow, reason := channel.Allow(fromUID)
	if !allow {
		l.Error("The user is not in the white list or in the black list", zap.String("fromUID", fromUID), zap.String("reason", reason.String()))
		return false, reason
	}
	if channel.ChannelType != wkproto.ChannelTypePerson && channel.ChannelType != wkproto.ChannelTypeInfo {
		if !channel.IsSubscriber(fromUID) && !channel.IsTmpSubscriber(fromUID) {
			l.Error("The user is not in the channel and cannot send messages to the channel", zap.String("fromUID", fromUID), zap.String("channel_id", channel.ChannelID), zap.Uint8("channel_type", channel.ChannelType))
			return false, wkproto.ReasonSubscriberNotExist
		}
	}
	return true, wkproto.ReasonSuccess
}

// store channel messages
func (l *Logic) storeChannelMessagesIfNeed(fromUID string, messages []*model.Message) error {
	if len(messages) == 0 {
		return nil
	}
	storeMessages := make([]wkstore.Message, 0, len(messages))
	for _, m := range messages {
		if m.NoPersist || m.SyncOnce {
			continue
		}
		if m.StreamIng() { // 流消息单独存储
			_, err := l.store.AppendStreamItem(m.ChannelID, m.ChannelType, m.StreamNo, &wkstore.StreamItem{
				ClientMsgNo: m.ClientMsgNo,
				StreamSeq:   m.StreamSeq,
				Blob:        m.Payload,
			})
			if err != nil {
				l.Error("store stream item err", zap.Error(err))
				return err
			}
			continue
		}
		storeMessages = append(storeMessages, m)
	}
	if len(storeMessages) == 0 {
		return nil
	}
	firstMessage := storeMessages[0].(*model.Message)
	fakeChannelID := firstMessage.ChannelID
	if firstMessage.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelID = GetFakeChannelIDWith(fromUID, firstMessage.ChannelID)
	}
	_, err := l.store.AppendMessages(fakeChannelID, firstMessage.ChannelType, storeMessages)
	if err != nil {
		l.Error("store message err", zap.Error(err))
		return err
	}
	return nil
}

func (l *Logic) storeChannelMessagesToNotifyQueue(messages []*model.Message) error {
	if len(messages) == 0 {
		return nil
	}
	storeMessages := make([]wkstore.Message, 0, len(messages))
	for _, m := range messages {
		if m.StreamIng() { // 流消息不做通知（只通知开始和结束）
			continue
		}
		storeMessages = append(storeMessages, m)
	}
	return l.store.AppendMessageOfNotifyQueue(storeMessages)
}

// decode payload
func (l *Logic) checkAndDecodePayload(messageID int64, sendPacket *wkproto.SendPacket, c gatewaycommon.Conn) ([]byte, error) {
	var (
		aesKey = c.Value(aesKeyKey).(string)
		aesIV  = c.Value(aesIVKey).(string)
	)
	vail, err := l.sendPacketIsVail(sendPacket, c)
	if err != nil {
		return nil, err
	}
	if !vail {
		return nil, errors.New("sendPacket is illegal！")
	}
	// decode payload
	decodePayload, err := wkutil.AesDecryptPkcs7Base64(sendPacket.Payload, []byte(aesKey), []byte(aesIV))
	if err != nil {
		l.Error("Failed to decode payload！", zap.Error(err))
		return nil, err
	}

	return decodePayload, nil
}

// send packet is vail
func (l *Logic) sendPacketIsVail(sendPacket *wkproto.SendPacket, c gatewaycommon.Conn) (bool, error) {
	var (
		aesKey = c.Value(aesKeyKey).(string)
		aesIV  = c.Value(aesIVKey).(string)
	)
	signStr := sendPacket.VerityString()
	actMsgKey, err := wkutil.AesEncryptPkcs7Base64([]byte(signStr), []byte(aesKey), []byte(aesIV))
	if err != nil {
		l.Error("msgKey is illegal！", zap.Error(err), zap.String("sign", signStr), zap.String("aesKey", aesKey), zap.String("aesIV", aesIV), zap.Any("conn", c))
		return false, err
	}
	actMsgKeyStr := sendPacket.MsgKey
	exceptMsgKey := wkutil.MD5(string(actMsgKey))
	if actMsgKeyStr != exceptMsgKey {
		l.Error("msgKey is illegal！", zap.String("except", exceptMsgKey), zap.String("act", actMsgKeyStr), zap.String("sign", signStr), zap.String("aesKey", aesKey), zap.String("aesIV", aesIV), zap.Any("conn", c))
		return false, errors.New("msgKey is illegal！")
	}
	return true, nil
}

func (l *Logic) getSendackPacket(msg *model.Message, reasonCode wkproto.ReasonCode) *wkproto.SendackPacket {
	return &wkproto.SendackPacket{
		Framer:      msg.Framer,
		ClientMsgNo: msg.ClientMsgNo,
		ClientSeq:   msg.ClientSeq,
		MessageID:   msg.MessageID,
		MessageSeq:  msg.MessageSeq,
		ReasonCode:  reasonCode,
	}

}

func (l *Logic) getSendackPacketWithSendPacket(sendPacket *wkproto.SendPacket, reasonCode wkproto.ReasonCode) *wkproto.SendackPacket {
	return &wkproto.SendackPacket{
		Framer:      sendPacket.Framer,
		ClientMsgNo: sendPacket.ClientMsgNo,
		ClientSeq:   sendPacket.ClientSeq,
		ReasonCode:  reasonCode,
	}

}
func (l *Logic) response(conn gatewaycommon.Conn, frames ...wkproto.Frame) {
	l.dataOut(conn, frames...)
}

// 生成消息ID
func (l *Logic) genMessageID() int64 {
	return l.messageIDGen.Generate().Int64()
}

// Schedule 延迟任务
func (l *Logic) Schedule(interval time.Duration, f func()) *timingwheel.Timer {
	return l.timingWheel.ScheduleFunc(&everyScheduler{
		Interval: interval,
	}, f)
}
