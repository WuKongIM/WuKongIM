package server

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/lni/goutils/syncutil"
	"github.com/valyala/bytebufferpool"
	"go.uber.org/zap"
)

type deliverManager struct {
	s *Server
	wklog.Log

	deliverrs []*deliverr // 投递者集合

	nextDeliverIndex int // 下一个投递者索引

	nodeManager *nodeManager // 节点管理
}

func newDeliverManager(s *Server) *deliverManager {

	d := &deliverManager{
		s:           s,
		Log:         wklog.NewWKLog("deliveryManager"),
		deliverrs:   make([]*deliverr, s.opts.Deliver.DeliverrCount),
		nodeManager: newNodeManager(s),
	}
	return d
}

func (d *deliverManager) start() error {
	for i := 0; i < d.s.opts.Deliver.DeliverrCount; i++ {
		deliverr := newDeliverr(i, d)
		d.deliverrs[i] = deliverr
		err := deliverr.start()
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *deliverManager) stop() {
	for _, deliverr := range d.deliverrs {
		deliverr.stop()
	}

	d.nodeManager.stop()
}

func (d *deliverManager) deliver(req *deliverReq) {
	d.handleDeliver(req)
}

func (d *deliverManager) handleDeliver(req *deliverReq) {

	retry := 0
	var err error
	for {
		if retry > d.s.opts.Deliver.MaxRetry {
			d.Error("deliver reqC full, retry too many times", zap.Int("retry", retry))
			return
		}
		deliver := d.nextDeliver()
		err = deliver.request(req)
		if err != nil {
			retry++
			continue
		}
		return
	}
}

func (d *deliverManager) nextDeliver() *deliverr {
	i := d.nextDeliverIndex % len(d.deliverrs)
	d.nextDeliverIndex++
	return d.deliverrs[i]
}

type deliverr struct {
	reqC chan *deliverReq
	dm   *deliverManager
	wklog.Log
	stopper        *syncutil.Stopper
	recvPacketPool *sync.Pool
	index          int
}

func newDeliverr(index int, dm *deliverManager) *deliverr {

	return &deliverr{
		stopper: syncutil.NewStopper(),
		reqC:    make(chan *deliverReq, 1024),
		Log:     wklog.NewWKLog(fmt.Sprintf("deliverr[%d]", index)),
		dm:      dm,
		index:   index,
		recvPacketPool: &sync.Pool{
			New: func() interface{} {
				return &wkproto.RecvPacket{}
			},
		},
	}
}

func (d *deliverr) start() error {
	d.stopper.RunWorker(d.loop)
	return nil
}

func (d *deliverr) stop() {
	d.stopper.Stop()
}

func (d *deliverr) request(req *deliverReq) error {
	select {
	case d.reqC <- req:
	default:
		return fmt.Errorf("deliverr is full, index:%d", d.index)
	}
	return nil
}

func (d *deliverr) loop() {
	reqCapSize := 1024
	reqs := make([]*deliverReq, 0, reqCapSize)
	done := false
	for {
		select {
		case req := <-d.reqC:
			reqs = append(reqs, req)
			for !done {
				select {
				case req := <-d.reqC:
					reqs = append(reqs, req)
				default:
					done = true
				}
			}
			d.handleDeliverReqs(reqs)
			if len(reqs) > 0 {
				reqs = reqs[:0] // Reset the slice without reallocating
				if cap(reqs) > reqCapSize && len(reqs) < cap(reqs)/2 {
					reqs = make([]*deliverReq, 0, cap(reqs)/2) // Reduce capacity if too large
				}
			}
			done = false
		case <-d.stopper.ShouldStop():
			return
		}

	}
}

func (d *deliverr) handleDeliverReqs(req []*deliverReq) {
	for _, r := range req {
		d.handleDeliverReq(r)
	}
}

// 请求节点对应tag的用户集合
func (d *deliverr) requestNodeChannelTag(nodeId uint64, req *tagReq) (*tagResp, error) {
	timeoutCtx, cancel := context.WithTimeout(d.dm.s.ctx, time.Second*5)
	defer cancel()
	data := req.Marshal()
	resp, err := d.dm.s.cluster.RequestWithContext(timeoutCtx, nodeId, "/wk/getNodeUidsByTag", data)
	if err != nil {
		return nil, err
	}
	if resp.Status != proto.StatusOK {
		return nil, fmt.Errorf("requestNodeChannelTag failed, status: %d err:%s", resp.Status, string(resp.Body))
	}
	var tagResp = &tagResp{}
	err = tagResp.Unmarshal(resp.Body)
	if err != nil {
		return nil, err
	}
	return tagResp, nil
}
func (d *deliverr) handleDeliverReq(req *deliverReq) {

	// ================== 获取tag信息 ==================
	var tg = d.dm.s.tagManager.getReceiverTag(req.tagKey)
	if tg == nil {

		timeoutCtx, cancel := context.WithTimeout(d.dm.s.ctx, time.Second*5)
		leader, err := d.dm.s.cluster.LeaderOfChannel(timeoutCtx, req.channelId, req.channelType)
		cancel()
		if err != nil {
			d.Error("getLeaderOfChannel failed", zap.String("channelId", req.channelId), zap.Uint8("channelType", req.channelType), zap.Error(err))
			return
		}
		if leader.Id == d.dm.s.opts.Cluster.NodeId { // 如果本节点是leader并且tag不存在，则创建tag
			if d.dm.s.opts.Logger.TraceOn {
				for _, msg := range req.messages {
					d.MessageTrace("生成接收者tag", msg.SendPacket.ClientMsgNo, "makeReceiverTag")
				}

			}
			tg, err = req.ch.makeReceiverTag()
			if err != nil {
				d.Error("handleDeliverReq:makeReceiverTag failed", zap.String("tagKey", req.tagKey), zap.String("channelId", req.channelId), zap.Uint8("channelType", req.channelType))
				if d.dm.s.opts.Logger.TraceOn {
					for _, msg := range req.messages {
						d.MessageTrace("生成接收者tag失败", msg.SendPacket.ClientMsgNo, "makeReceiverTag", zap.Error(err))
					}

				}
				return
			}
		} else {
			if d.dm.s.opts.Logger.TraceOn {
				for _, msg := range req.messages {
					d.MessageTrace("请求接受者tag", msg.SendPacket.ClientMsgNo, "requestReceiverTag", zap.String("tagKey", req.tagKey), zap.Uint64("leaderId", leader.Id))
				}

			}
			if req.channelType == wkproto.ChannelTypePerson { // 个人频道
				tg, err = d.getPersonTag(req.channelId)
				if err != nil {
					d.Error("get person tag failed", zap.Error(err), zap.String("channelId", req.channelId))
				}
			} else {
				tagResp, err := d.requestNodeChannelTag(leader.Id, &tagReq{
					channelId:   req.channelId,
					channelType: req.channelType,
					tagKey:      req.tagKey,
					nodeId:      d.dm.s.opts.Cluster.NodeId,
				})
				if err != nil {
					d.Error("requestNodeTag failed", zap.Error(err), zap.String("tagKey", req.tagKey), zap.String("channelId", req.channelId), zap.Uint8("channelType", req.channelType))
					if d.dm.s.opts.Logger.TraceOn {
						for _, msg := range req.messages {
							d.MessageTrace("请求接受者tag失败", msg.SendPacket.ClientMsgNo, "requestReceiverTag", zap.String("tagKey", req.tagKey), zap.Error(err))
						}

					}
					return
				}
				tg = d.dm.s.tagManager.addOrUpdateReceiverTag(tagResp.tagKey, []*nodeUsers{
					{
						uids:   tagResp.uids,
						nodeId: d.dm.s.opts.Cluster.NodeId,
					},
				}, req.channelId, req.channelType)
			}

		}

	}

	// ================== 投递消息 ==================

	for _, nodeUser := range tg.users {

		// 本节点投递
		if d.dm.s.opts.Cluster.NodeId == nodeUser.nodeId {
			// 记录轨迹
			if d.dm.s.opts.Logger.TraceOn {
				for _, msg := range req.messages {
					d.MessageTrace("投递节点", msg.SendPacket.ClientMsgNo, "deliverNode", zap.Int("userCount", len(nodeUser.uids)))
				}
			}
			// 更新最近会话
			if d.dm.s.opts.Conversation.On {
				d.dm.s.conversationManager.Push(&conversationReq{
					channelId:   req.channelId,
					channelType: req.channelType,
					tagKey:      req.tagKey,
					messages:    req.messages,
				})
			}

			// 投递消息
			d.deliver(req, nodeUser.uids)

		} else {
			// 非本节点投递
			// 转发给对应的节点
			d.dm.nodeManager.deliver(nodeUser.nodeId, req)
		}
	}
}

// 获取个人频道的投递tag
func (d *deliverr) getPersonTag(fakeChannelId string) (*tag, error) {
	orgFakeChannelId := fakeChannelId
	if d.dm.s.opts.IsCmdChannel(fakeChannelId) {
		// 处理命令频道
		orgFakeChannelId = d.dm.s.opts.CmdChannelConvertOrginalChannel(fakeChannelId)
	}
	// 处理普通假个人频道
	u1, u2 := GetFromUIDAndToUIDWith(orgFakeChannelId)

	nodeUs := make([]*nodeUsers, 0, 2)

	u1NodeId, err := d.dm.s.cluster.SlotLeaderIdOfChannel(u1, wkproto.ChannelTypePerson)
	if err != nil {
		return nil, err
	}
	u2NodeId, err := d.dm.s.cluster.SlotLeaderIdOfChannel(u2, wkproto.ChannelTypePerson)
	if err != nil {
		return nil, err
	}

	if u1NodeId == d.dm.s.opts.Cluster.NodeId && u1NodeId == u2NodeId {
		nodeUs = append(nodeUs, &nodeUsers{
			nodeId: u1NodeId,
			uids:   []string{u1, u2},
		})
	} else if u1NodeId == d.dm.s.opts.Cluster.NodeId {
		nodeUs = append(nodeUs, &nodeUsers{
			nodeId: u1NodeId,
			uids:   []string{u1},
		})
	} else if u2NodeId == d.dm.s.opts.Cluster.NodeId {
		nodeUs = append(nodeUs, &nodeUsers{
			nodeId: u2NodeId,
			uids:   []string{u2},
		})
	}

	tg := &tag{
		key:         wkutil.GenUUID(),
		channelId:   fakeChannelId,
		channelType: wkproto.ChannelTypePerson,
		users:       nodeUs,
	}
	return tg, nil
}

type deliverUserSlice struct {
	offlineUids []string       // 离线用户(只要主设备不在线就算离线)
	toConns     []*connContext // 在线接受用户的连接对象
	onlineUsers []string       // 在线用户数量（只要一个客户端在线就算在线）
}

func (d *deliverUserSlice) reset() {
	d.offlineUids = d.offlineUids[:0]
	d.toConns = d.toConns[:0]
	d.onlineUsers = d.onlineUsers[:0]
}

var deliverSlicePool = &sync.Pool{
	New: func() any {
		return &deliverUserSlice{
			offlineUids: make([]string, 0),
			toConns:     make([]*connContext, 0),
			onlineUsers: make([]string, 0),
		}
	},
}

func (d *deliverr) deliver(req *deliverReq, uids []string) {
	if len(uids) == 0 {
		return
	}

	slices := deliverSlicePool.Get().(*deliverUserSlice)
	defer func() {
		slices.reset()
		deliverSlicePool.Put(slices)
	}()

	// // d.Info("start deliver message", zap.String("channelId", req.channelId), zap.Uint8("channelType", req.channelType), zap.Strings("uids", uids))
	// webhookOfflineUids := make([]string, 0, len(uids)) // 离线用户(只要主设备不在线就算离线)
	// toConns := make([]*connContext, 0)                 // 在线接受用户的连接对象
	// onlineUsers := make([]string, 0)                   // 在线用户数量（只要一个客户端在线就算在线）

	for _, toUid := range uids {
		userHandler := d.dm.s.userReactor.getUserHandler(toUid)
		if userHandler == nil { // 用户不在线
			slices.offlineUids = append(slices.offlineUids, toUid)
			continue
		}
		// 用户没有主设备在线，还是是要推送离线给业务端，比如有的场景，web在线，手机离线，这种情况手机需要收到离线。
		if !userHandler.hasMasterDevice() {
			slices.offlineUids = append(slices.offlineUids, toUid)
		}

		// 获取当前用户的所有连接
		conns := userHandler.getConns()

		if len(conns) == 0 {
			slices.offlineUids = append(slices.offlineUids, toUid)
		} else {
			for _, conn := range conns {
				if !conn.isAuth.Load() {
					continue
				}
				slices.toConns = append(slices.toConns, conn)
			}

			slices.onlineUsers = append(slices.onlineUsers, toUid)
		}
	}

	if d.dm.s.opts.Logger.TraceOn {
		for _, msg := range req.messages {
			if len(slices.onlineUsers) > 0 {
				existSendSelfDevice := false        // 存在发送者自己的连接
				existSendSelfNotSendDevice := false // 存在发送者自己但是不是发送设备
				for _, toConn := range slices.toConns {
					if toConn.uid == msg.FromUid && toConn.deviceId == msg.FromDeviceId {
						existSendSelfDevice = true
					}
					if toConn.uid == msg.FromUid && toConn.deviceId != msg.FromDeviceId {
						existSendSelfNotSendDevice = true
					}
				}

				// 如果仅仅是发送者自己的连接，不需要发送给自己
				if !existSendSelfDevice || existSendSelfNotSendDevice {
					d.MessageTrace("在线通知", msg.SendPacket.ClientMsgNo, "deliverOnline", zap.Int("userCount", len(slices.onlineUsers)), zap.String("uids", strings.Join(slices.onlineUsers, ",")), zap.Int("connCount", len(slices.toConns)))
				}
			}

			if len(slices.offlineUids) > 0 {
				d.MessageTrace("离线通知", msg.SendPacket.ClientMsgNo, "deliverOffline", zap.Int("userCount", len(slices.offlineUids)), zap.String("uids", strings.Join(slices.offlineUids, ",")))
			}
		}
	}

	// payload内容pool
	payloadBuffer := bytebufferpool.Get()
	defer bytebufferpool.Put(payloadBuffer)

	// recvPacket pool
	recvPacket := d.recvPacketPool.Get().(*wkproto.RecvPacket)
	defer d.releaseRecvPacket(recvPacket)

	// 签名buffer
	signBuffer := bytebufferpool.Get()
	defer bytebufferpool.Put(signBuffer)

	// aes加密buffer
	aesResultBuffer := bytebufferpool.Get()
	defer bytebufferpool.Put(aesResultBuffer)

	// 接受包
	recvPacketBuffer := bytebufferpool.Get()
	defer bytebufferpool.Put(recvPacketBuffer)

	// md5加密对象
	m5 := md5.New()

	var err error

	for _, message := range req.messages {

		sendPacket := message.SendPacket
		fromUid := message.FromUid
		// 如果发送者是系统账号，则不显示发送者
		if fromUid == d.dm.s.opts.SystemUID {
			fromUid = ""
		}

		recvPacket.Framer = wkproto.Framer{
			RedDot:    sendPacket.GetRedDot(),
			SyncOnce:  sendPacket.GetsyncOnce(),
			NoPersist: sendPacket.GetNoPersist(),
		}
		recvPacket.Setting = sendPacket.Setting
		recvPacket.MessageID = message.MessageId
		recvPacket.MessageSeq = message.MessageSeq
		recvPacket.ClientMsgNo = sendPacket.ClientMsgNo
		recvPacket.StreamNo = sendPacket.StreamNo
		recvPacket.StreamFlag = wkproto.StreamFlagIng
		recvPacket.FromUID = fromUid
		recvPacket.Expire = sendPacket.Expire
		recvPacket.ChannelID = sendPacket.ChannelID
		recvPacket.ChannelType = sendPacket.ChannelType
		recvPacket.Topic = sendPacket.Topic
		recvPacket.Timestamp = int32(time.Now().Unix())
		recvPacket.ClientSeq = sendPacket.ClientSeq
		if len(recvPacket.Payload) > 0 {
			recvPacket.Payload = recvPacket.Payload[:0]
		}

		for _, conn := range slices.toConns {
			if conn.uid == message.FromUid && conn.deviceId == message.FromDeviceId { // 自己发的不处理
				continue
			}

			// 这里需要把channelID改成fromUID 比如A给B发消息，B收到的消息channelID应该是A A收到的消息channelID应该是B
			recvPacket.ChannelID = sendPacket.ChannelID
			if recvPacket.ChannelType == wkproto.ChannelTypePerson && recvPacket.ChannelID == conn.uid {
				recvPacket.ChannelID = recvPacket.FromUID
			}

			// 红点设置
			recvPacket.RedDot = sendPacket.RedDot
			if conn.uid == recvPacket.FromUID { // 如果是自己则不显示红点
				recvPacket.RedDot = false
			}

			// payload内容加密
			payloadBuffer.Reset()

			if len(conn.aesIV) == 0 || len(conn.aesKey) == 0 {
				d.Error("aesIV or aesKey is empty", zap.String("uid", conn.uid), zap.String("deviceId", conn.deviceId), zap.String("channelId", recvPacket.ChannelID), zap.Uint8("channelType", recvPacket.ChannelType))
				continue
			}

			err = encryptMessagePayload(sendPacket.Payload, conn, payloadBuffer)
			if err != nil {
				d.Error("加密payload失败！", zap.Error(err), zap.String("uid", conn.uid), zap.String("channelId", recvPacket.ChannelID), zap.Uint8("channelType", recvPacket.ChannelType))
				continue
			}
			recvPacket.Payload = payloadBuffer.Bytes()

			// 对内容进行签名，防止中间人攻击
			signBuffer.Reset()
			recvPacket.VerityBytes(signBuffer)
			// 加密sign内容
			aesResultBuffer.Reset()
			err = writeAesEncrypt(aesResultBuffer, signBuffer, conn)
			if err != nil {
				d.Error("生成MsgKey失败！", zap.Error(err))
				continue
			}
			// m5加密一次
			m5.Reset()
			m5.Write(aesResultBuffer.Bytes())

			recvPacket.MsgKey = hex.EncodeToString(m5.Sum(nil))

			// 编码接受包
			recvPacketBuffer.Reset()
			err = d.dm.s.opts.Proto.WriteFrame(recvPacketBuffer, recvPacket, conn.protoVersion)
			if err != nil {
				d.Error("encode recvPacket failed", zap.String("uid", conn.uid), zap.String("channelId", recvPacket.ChannelID), zap.Uint8("channelType", recvPacket.ChannelType), zap.Error(err))
				continue
			}

			// 复制一份新的，避免多线程竞争
			recvPacketData := make([]byte, len(recvPacketBuffer.B))
			copy(recvPacketData, recvPacketBuffer.Bytes())

			if !recvPacket.NoPersist { // 只有存储的消息才重试
				d.dm.s.retryManager.addRetry(&retryMessage{
					uid:            conn.uid,
					connId:         conn.connId,
					messageId:      message.MessageId,
					recvPacketData: recvPacketData,
					channelId:      req.channelId,
					channelType:    req.channelType,
				})
			}

			trace.GlobalTrace.Metrics.App().RecvPacketCountAdd(1)
			trace.GlobalTrace.Metrics.App().RecvPacketBytesAdd(int64(len(recvPacketData)))

			// 写入包
			// d.Info("deliverr recvPacket", zap.String("uid", conn.uid), zap.String("channelId", recvPacket.ChannelID), zap.Uint8("channelType", recvPacket.ChannelType))
			err = conn.write(recvPacketData, wkproto.RECV)
			if err != nil {
				d.Error("write recvPacket failed", zap.String("uid", conn.uid), zap.String("channelId", recvPacket.ChannelID), zap.Uint8("channelType", recvPacket.ChannelType), zap.Error(err))
				if !conn.isClosed() {
					conn.close() // 写入不进去就关闭连接，这样客户端会获取离线的，如果不关闭，会导致丢消息的假象
				}
			}
		}
	}

	if len(slices.offlineUids) > 0 { // 有离线用户，发送webhook
		for _, message := range req.messages {
			d.dm.s.webhook.notifyOfflineMsg(message, slices.offlineUids)
		}
	}

	if d.dm.s.opts.Logger.TraceOn {
		for _, msg := range req.messages {
			d.MessageTrace("投递消息完成", msg.SendPacket.ClientMsgNo, "deliverMessageFinished")
		}
	}

}

func (d *deliverr) releaseRecvPacket(recvPacket *wkproto.RecvPacket) {
	// 重置 recvPacket 的字段，以便下次使用
	recvPacket.Reset()
	d.recvPacketPool.Put(recvPacket)
}

// 加密消息
func encryptMessagePayload(payload []byte, conn *connContext, resultBuff *bytebufferpool.ByteBuffer) error {
	aesKey, aesIV := conn.aesKey, conn.aesIV
	// 加密payload
	err := wkutil.AesEncryptPkcs7Base64ForPool(payload, aesKey, aesIV, resultBuff)
	if err != nil {
		return err
	}

	return nil
}

// 加密消息
func encryptMessagePayload2(payload []byte, conn *connContext) ([]byte, error) {
	aesKey, aesIV := conn.aesKey, conn.aesIV
	// 加密payload
	payloadEnc, err := wkutil.AesEncryptPkcs7Base64(payload, aesKey, aesIV)
	if err != nil {
		return nil, err
	}
	return payloadEnc, nil
}

func writeAesEncrypt(aesResultBuffer *bytebufferpool.ByteBuffer, signBuffer *bytebufferpool.ByteBuffer, conn *connContext) error {
	aesKey, aesIV := conn.aesKey, conn.aesIV

	// 生成MsgKey
	err := wkutil.AesEncryptPkcs7Base64ForPool(signBuffer.Bytes(), aesKey, aesIV, aesResultBuffer)
	if err != nil {
		wklog.Error("生成MsgKey失败！", zap.Error(err))
		return err
	}
	return nil
}
