package server

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
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

// 请求节点对应tag的用户集合
func (d *deliverr) requestNodeChannelTag(nodeId uint64, req *tagReq) (*tagResp, error) {
	timeoutCtx, cancel := context.WithTimeout(d.dm.s.ctx, time.Second*5)
	defer cancel()
	data := req.Marshal()
	resp, err := d.dm.s.cluster.RequestWithContext(timeoutCtx, nodeId, "/wk/getNodeUidsByTag", data)
	if err != nil {
		return nil, err
	}
	if resp.Status != proto.Status_OK {
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
		leader, err := d.dm.s.cluster.LeaderOfChannelForRead(req.channelId, req.channelType)
		if err != nil {
			d.Error("getLeaderOfChannel failed", zap.String("channelId", req.channelId), zap.Uint8("channelType", req.channelType), zap.Error(err))
			return
		}
		if leader.Id == d.dm.s.opts.Cluster.NodeId {
			d.Error("getReceiverTag failed", zap.String("tagKey", req.tagKey), zap.String("channelId", req.channelId), zap.Uint8("channelType", req.channelType))
			return
		}
		tagResp, err := d.requestNodeChannelTag(leader.Id, &tagReq{
			channelId:   req.channelId,
			channelType: req.channelType,
			tagKey:      req.tagKey,
			nodeId:      d.dm.s.opts.Cluster.NodeId,
		})
		if err != nil {
			d.Error("requestNodeTag failed", zap.Error(err), zap.String("tagKey", req.tagKey), zap.String("channelId", req.channelId), zap.Uint8("channelType", req.channelType))
			return
		}
		tg = d.dm.s.tagManager.addOrUpdateReceiverTag(tagResp.tagKey, []*nodeUsers{
			{
				uids:   tagResp.uids,
				nodeId: d.dm.s.opts.Cluster.NodeId,
			},
		})
	}

	// ================== 投递消息 ==================
	for _, nodeUser := range tg.users {
		if d.dm.s.opts.Cluster.NodeId == nodeUser.nodeId { // 只投递本节点的
			// 更新最近会话
			d.dm.s.conversationManager.Push(req.channelId, req.channelType, nodeUser.uids, req.messages)

			// 投递消息
			d.deliver(req, nodeUser.uids)

		} else { // 非本节点的转发给对应节点去投递
			d.dm.nodeManager.deliver(nodeUser.nodeId, req)
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

		// 获取当前用户的所有连接
		conns := userHandler.getConns()

		for _, conn := range conns {
			for _, message := range req.messages {

				if conn.uid == message.FromUid && conn.deviceId == message.FromDeviceId { // 自己发的不处理
					continue
				}

				d.Debug("deliver message to user", zap.String("channelId", req.channelId), zap.Uint8("channelType", req.channelType), zap.Int64("connId", conn.connId), zap.String("uid", conn.uid), zap.String("deviceId", conn.deviceId))

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
