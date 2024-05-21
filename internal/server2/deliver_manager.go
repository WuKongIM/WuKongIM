package server

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type deliverManager struct {
	s *Server
	wklog.Log

	deliverrs []*deliverr // 投递者集合

	nextDeliverIndex int // 下一个投递者索引

	retryManager *retryManager
}

func newDeliverManager(s *Server) *deliverManager {

	d := &deliverManager{
		s:         s,
		Log:       wklog.NewWKLog("deliveryManager"),
		deliverrs: make([]*deliverr, s.opts.Deliver.Count),
	}
	return d
}

func (d *deliverManager) start() error {
	for i := 0; i < d.s.opts.Deliver.Count; i++ {
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
}

func (d *deliverManager) deliver(req *deliverReq) {
	d.handleDeliver(req)
}

func (d *deliverManager) handleDeliver(req *deliverReq) {

	retry := 0
	for {
		if retry > d.s.opts.Deliver.MaxRetry {
			d.Error("deliver reqC full, retry too many times", zap.Int("retry", retry))
			return
		}
		deliver := d.nextDeliver()
		select {
		case deliver.reqC <- req:
			return
		default:
			retry++
		}
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
	stopper *syncutil.Stopper
}

func newDeliverr(index int, dm *deliverManager) *deliverr {

	return &deliverr{
		stopper: syncutil.NewStopper(),
		reqC:    make(chan *deliverReq, 1024),
		Log:     wklog.NewWKLog(fmt.Sprintf("deliverr[%d]", index)),
		dm:      dm,
	}
}

func (d *deliverr) start() error {
	d.stopper.RunWorker(d.loop)
	return nil
}

func (d *deliverr) stop() {
	d.stopper.Stop()
}

func (d *deliverr) loop() {
	reqs := make([]*deliverReq, 0)
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
			reqs = reqs[:0]
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

func (d *deliverr) handleDeliverReq(req *deliverReq) {

	channelId, channelType := req.ch.channelId, req.ch.channelType
	tag := d.dm.s.tagManager.getReceiverTag(channelId, channelType)
	var err error
	if tag == nil {
		tag, err = req.ch.makeReceiverTag()
		if err != nil {
			d.Error("makeReceiverTag failed", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Error(err))
			return
		}
	}
	for _, nodeUser := range tag.users {
		if d.dm.s.opts.Cluster.NodeId == nodeUser.nodeId { // 只投递本节点的
			d.deliver(req, nodeUser.uids)
			break
		}
	}
}

func (d *deliverr) deliver(req *deliverReq, uids []string) {
	if len(uids) == 0 {
		return
	}
	offlineUids := make([]string, 0, len(uids)) // 离线用户
	for _, toUid := range uids {
		userHandler := d.dm.s.userReactor.getUser(toUid)
		if userHandler == nil { // 用户不在线
			offlineUids = append(offlineUids, toUid)
			continue
		}

		// 用户没有主设备在线，还是是要推送离线给业务端，比如有的场景，web在线，手机离线，这种情况手机需要收到离线。
		if !userHandler.hasMasterDevice() {
			offlineUids = append(offlineUids, toUid)
		}

		conns := userHandler.getConns()

		for _, conn := range conns {
			for _, message := range req.messages {

				if conn.uid == message.FromUid && conn.deviceId == message.FromDeviceId { // 自己发的不处理
					continue
				}

				sendPacket := message.SendPacket

				recvPacket := &wkproto.RecvPacket{
					Framer: wkproto.Framer{
						RedDot:    sendPacket.GetRedDot(),
						SyncOnce:  sendPacket.GetsyncOnce(),
						NoPersist: sendPacket.GetNoPersist(),
					},
					Setting:     sendPacket.Setting,
					MessageID:   message.MessageId,
					MessageSeq:  message.MessageSeq,
					ClientMsgNo: sendPacket.ClientMsgNo,
					StreamNo:    sendPacket.StreamNo,
					StreamFlag:  wkproto.StreamFlagIng,
					FromUID:     message.FromUid,
					Expire:      sendPacket.Expire,
					ChannelID:   sendPacket.ChannelID,
					ChannelType: sendPacket.ChannelType,
					Topic:       sendPacket.Topic,
					Timestamp:   int32(time.Now().Unix()),
					Payload:     sendPacket.Payload,
					// ---------- 以下不参与编码 ------------
					ClientSeq: sendPacket.ClientSeq,
				}

				// 这里需要把channelID改成fromUID 比如A给B发消息，B收到的消息channelID应该是A A收到的消息channelID应该是B
				if recvPacket.ChannelType == wkproto.ChannelTypePerson && recvPacket.ChannelID == toUid {
					recvPacket.ChannelID = recvPacket.FromUID
				}

				if toUid == recvPacket.FromUID { // 如果是自己则不显示红点
					recvPacket.RedDot = false
				}

				// payload内容加密
				payloadEnc, err := encryptMessagePayload(recvPacket.Payload, conn)
				if err != nil {
					d.Error("加密payload失败！", zap.Error(err))
					continue
				}
				recvPacket.Payload = payloadEnc

				// 对内容进行签名，防止中间人攻击
				signStr := recvPacket.VerityString()
				msgKey, err := makeMsgKey(signStr, conn)
				if err != nil {
					d.Error("生成MsgKey失败！", zap.Error(err))
					continue
				}
				recvPacket.MsgKey = msgKey

				recvPacketData, err := d.dm.s.opts.Proto.EncodeFrame(recvPacket, conn.protoVersion)
				if err != nil {
					d.Error("encode recvPacket failed", zap.String("uid", conn.uid), zap.String("channelId", recvPacket.ChannelID), zap.Uint8("channelType", recvPacket.ChannelType), zap.Error(err))
					continue
				}

				if !recvPacket.NoPersist { // 只有存储的消息才重试
					d.dm.s.retryManager.addRetry(&retryMessage{
						uid:            toUid,
						connId:         conn.connId,
						messageId:      message.MessageId,
						recvPacketData: recvPacketData,
					})
				}

				// 写入包
				err = conn.write(recvPacketData)
				if err != nil {
					d.Error("write recvPacket failed", zap.String("uid", conn.uid), zap.String("channelId", recvPacket.ChannelID), zap.Uint8("channelType", recvPacket.ChannelType), zap.Error(err))
					if !conn.isClosed() {
						conn.close() // 写入不进去就关闭连接，这样客户端会获取离线的，如果不关闭，会导致丢消息的假象
					}
				}
			}
		}

	}

	if len(offlineUids) > 0 { // 有离线用户，发送webhook
		for _, message := range req.messages {
			d.dm.s.webhook.notifyOfflineMsg(message, offlineUids)
		}
	}
}

// 加密消息
func encryptMessagePayload(payload []byte, conn *connContext) ([]byte, error) {
	aesKey, aesIV := conn.aesKey, conn.aesIV
	// 加密payload
	payloadEnc, err := wkutil.AesEncryptPkcs7Base64(payload, []byte(aesKey), []byte(aesIV))
	if err != nil {
		return nil, err
	}
	return payloadEnc, nil
}

func makeMsgKey(signStr string, conn *connContext) (string, error) {
	aesKey, aesIV := conn.aesKey, conn.aesIV
	// 生成MsgKey
	msgKeyBytes, err := wkutil.AesEncryptPkcs7Base64([]byte(signStr), []byte(aesKey), []byte(aesIV))
	if err != nil {
		wklog.Error("生成MsgKey失败！", zap.Error(err))
		return "", err
	}
	return wkutil.MD5(string(msgKeyBytes)), nil
}
