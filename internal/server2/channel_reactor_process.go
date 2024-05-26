package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

// =================================== 初始化 ===================================

func (r *channelReactor) addInitReq(req *initReq) {
	select {
	case r.processInitC <- req:
	case <-r.stopper.ShouldStop():
		return
	}
}

func (r *channelReactor) processInitLoop() {
	for {
		select {
		case req := <-r.processInitC:
			r.processInit(req)
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *channelReactor) processInit(req *initReq) {
	timeoutCtx, cancel := context.WithTimeout(r.s.ctx, time.Second*5)
	defer cancel()
	node, err := r.s.cluster.LeaderOfChannel(timeoutCtx, req.ch.channelId, req.ch.channelType)
	sub := r.reactorSub(req.ch.key)
	if err != nil {
		r.Error("channel init failed", zap.Error(err))
		sub.step(req.ch, &ChannelAction{ActionType: ChannelActionInitResp, Reason: ReasonError})
		return
	}
	_, err = req.ch.makeReceiverTag()
	if err != nil {
		r.Error("processInit: makeReceiverTag failed", zap.Error(err))
		sub.step(req.ch, &ChannelAction{ActionType: ChannelActionInitResp, LeaderId: node.Id, Reason: ReasonError})
		return
	}
	sub.step(req.ch, &ChannelAction{ActionType: ChannelActionInitResp, LeaderId: node.Id, Reason: ReasonSuccess})
}

type initReq struct {
	ch *channel
}

// =================================== payload解密 ===================================

func (r *channelReactor) addPayloadDecryptReq(req *payloadDecryptReq) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	select {
	case r.processPayloadDecryptC <- req:
	case <-timeoutCtx.Done():
		r.Error("addPayloadDecryptReq timeout", zap.String("channelId", req.ch.channelId))
	case <-r.stopper.ShouldStop():
		return
	}
}

func (r *channelReactor) processPayloadDecryptLoop() {
	for {
		select {
		case req := <-r.processPayloadDecryptC:
			r.processPayloadDecrypt(req)
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *channelReactor) processPayloadDecrypt(req *payloadDecryptReq) {

	for _, msg := range req.messages {
		if !msg.IsEncrypt || msg.FromConnId == 0 { // 没有加密，直接跳过,没有连接id解密不了，也直接跳过
			continue
		}
		var err error
		var decryptPayload []byte
		conn := r.s.userReactor.getConnContextById(msg.FromUid, msg.FromConnId)
		if conn != nil {
			decryptPayload, err = r.s.checkAndDecodePayload(msg.SendPacket, conn)
			if err != nil {
				r.Warn("decrypt payload error", zap.String("uid", msg.FromUid), zap.String("deviceId", msg.FromDeviceId), zap.Int64("connId", msg.FromConnId), zap.Error(err))
			}
		}
		if len(decryptPayload) > 0 {
			msg.SendPacket.Payload = decryptPayload
			msg.IsEncrypt = false
		}
	}
	sub := r.reactorSub(req.ch.key)
	sub.step(req.ch, &ChannelAction{ActionType: ChannelActionPayloadDecryptResp, Messages: req.messages})

}

type payloadDecryptReq struct {
	ch       *channel
	messages []*ReactorChannelMessage
}

// =================================== 转发 ===================================

func (r *channelReactor) addForwardReq(req *forwardReq) {
	select {
	case r.processForwardC <- req:
	case <-r.stopper.ShouldStop():
		return
	}
}

func (r *channelReactor) processForwardLoop() {
	reqs := make([]*forwardReq, 0, 1024)
	done := false
	for {
		select {
		case req := <-r.processForwardC:
			reqs = append(reqs, req)
			// 取出所有req
			for !done {
				select {
				case req := <-r.processForwardC:
					exist := false
					for _, r := range reqs {
						if r.ch.channelId == req.ch.channelId && r.ch.channelType == req.ch.channelType {
							r.messages = append(r.messages, req.messages...)
							exist = true
							break
						}
					}
					if !exist {
						reqs = append(reqs, req)
					}
				default:
					done = true
				}
			}
			r.processForward(reqs)

			reqs = reqs[:0]
			done = false
		case <-r.stopper.ShouldStop():
			return
		}
	}

}

func (r *channelReactor) processForward(reqs []*forwardReq) {
	for _, req := range reqs {
		newLeaderId, err := r.handleForward(req)
		var reason Reason
		if err != nil {
			reason = ReasonError
			r.Error("handleForward error", zap.Error(err))
		} else {
			reason = ReasonSuccess
		}
		if newLeaderId > 0 {
			sub := r.reactorSub(req.ch.key)
			sub.step(req.ch, &ChannelAction{ActionType: ChannelActionLeaderChange, LeaderId: newLeaderId})
		}
		sub := r.reactorSub(req.ch.key)
		sub.step(req.ch, &ChannelAction{ActionType: ChannelActionForwardResp, Messages: req.messages, Reason: reason})
		if err != nil {
			r.Error("processForwardPool.Submit error", zap.Error(err))
		}

	}

}

func (r *channelReactor) handleForward(req *forwardReq) (uint64, error) {
	if len(req.messages) == 0 {
		return 0, nil
	}

	if req.leaderId == 0 {
		r.Warn("leaderId is 0", zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))
		return 0, errors.New("leaderId is 0")
	}

	needChangeLeader, err := r.requestChannelFoward(req.leaderId, req.messages)
	if err != nil {
		r.Error("requestChannelFoward error", zap.Error(err))
		return 0, err
	}
	if needChangeLeader { // 接受转发请求的节点并非频道领导节点，所以这里要重新获取频道领导
		// 重新获取频道领导
		timeoutCtx, cancel := context.WithTimeout(r.s.ctx, time.Second*5)
		defer cancel()
		node, err := r.s.cluster.LeaderOfChannel(timeoutCtx, req.ch.channelId, req.ch.channelType)
		if err != nil {
			r.Error("LeaderOfChannel error", zap.Error(err))
			return 0, err
		}
		return node.Id, errors.New("leader change")
	}

	return 0, nil
}

func (r *channelReactor) requestChannelFoward(nodeId uint64, messages []*ReactorChannelMessage) (bool, error) {
	timeoutCtx, cancel := context.WithTimeout(r.s.ctx, time.Second*5)
	defer cancel()

	messageSet := ReactorChannelMessageSet(messages)
	data, err := messageSet.Marshal()
	if err != nil {
		return false, err
	}
	resp, err := r.s.cluster.RequestWithContext(timeoutCtx, nodeId, "/wk/channelFoward", data)
	if err != nil {
		return false, err
	}
	if resp.Status == proto.Status(errCodeNotIsChannelLeader) { // 转发下去的节点不是频道领导，这时候要重新获取下领导节点
		return true, nil
	}

	if resp.Status != proto.Status_OK {
		var err error
		if len(resp.Body) > 0 {
			err = errors.New(string(resp.Body))
		} else {
			err = fmt.Errorf("requestChannelFoward failed, status[%d] error", resp.Status)
		}
		return false, err
	}
	return false, nil

}

type forwardReq struct {
	ch       *channel
	leaderId uint64
	messages []*ReactorChannelMessage
}

// =================================== 发送权限判断 ===================================
func (r *channelReactor) addPermissionReq(req *permissionReq) {
	select {
	case r.processPermissionC <- req:
	case <-r.stopper.ShouldStop():
		return
	}
}

func (r *channelReactor) processPermissionLoop() {
	for {
		select {
		case req := <-r.processPermissionC:
			r.processPermission(req)
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *channelReactor) processPermission(req *permissionReq) {

	// 权限判断
	sub := r.reactorSub(req.ch.key)
	reasonCode, err := r.hasPermission(req.ch.channelId, req.ch.channelType, req.fromUid, req.ch)
	if err != nil {
		r.Error("hasPermission error", zap.Error(err))
		// 返回错误
		lastMsg := req.messages[len(req.messages)-1]
		sub.step(req.ch, &ChannelAction{ActionType: ChannelActionPermissionCheckResp, Index: lastMsg.Index, Reason: ReasonError})
		return
	}
	reason := ReasonSuccess
	if reasonCode != wkproto.ReasonSuccess {
		reason = ReasonError
	}
	// 返回成功
	lastMsg := req.messages[len(req.messages)-1]
	sub.step(req.ch, &ChannelAction{ActionType: ChannelActionPermissionCheckResp, Index: lastMsg.Index, Reason: reason, ReasonCode: reasonCode})
}

func (r *channelReactor) hasPermission(channelId string, channelType uint8, uid string, ch *channel) (wkproto.ReasonCode, error) {

	if channelType == wkproto.ChannelTypeInfo || channelType == wkproto.ChannelTypePerson {
		return wkproto.ReasonSuccess, nil
	}

	systemAccount := r.s.systemUIDManager.SystemUID(uid)
	if systemAccount { // 如果是系统账号，则直接通过
		return wkproto.ReasonSuccess, nil
	}

	if ch.info.Ban { // 频道被封禁
		return wkproto.ReasonBan, nil
	}

	if ch.info.Disband { // 频道已解散
		return wkproto.ReasonDisband, nil
	}

	// 判断是否是黑名单内
	isDenylist, err := r.s.store.ExistDenylist(channelId, channelType, uid)
	if err != nil {
		r.Error("ExistDenylist error", zap.Error(err))
		return wkproto.ReasonSystemError, err
	}
	if isDenylist {
		return wkproto.ReasonInBlacklist, nil
	}

	// 判断是否是订阅者
	isSubscriber, err := r.s.store.ExistSubscriber(channelId, channelType, uid)
	if err != nil {
		r.Error("ExistSubscriber error", zap.Error(err))
		return wkproto.ReasonSystemError, err
	}
	if !isSubscriber {
		return wkproto.ReasonSubscriberNotExist, nil
	}

	// 判断是否在白名单内
	if !r.opts.WhitelistOffOfPerson || channelType != wkproto.ChannelTypePerson { // 如果不是个人频道或者个人频道白名单开关打开，则判断是否在白名单内
		hasAllowlist, err := r.s.store.HasAllowlist(channelId, channelType)
		if err != nil {
			r.Error("HasAllowlist error", zap.Error(err))
			return wkproto.ReasonSystemError, err
		}

		if hasAllowlist { // 如果频道有白名单，则判断是否在白名单内
			isAllowlist, err := r.s.store.ExistAllowlist(channelId, channelType, uid)
			if err != nil {
				r.Error("ExistAllowlist error", zap.Error(err))
				return wkproto.ReasonSystemError, err
			}
			if !isAllowlist {
				return wkproto.ReasonNotInWhitelist, nil
			}
		}
	}

	return wkproto.ReasonSuccess, nil
}

type permissionReq struct {
	fromUid  string
	ch       *channel
	messages []*ReactorChannelMessage
}

// =================================== 消息存储 ===================================

func (r *channelReactor) addStorageReq(req *storageReq) {
	select {
	case r.processStorageC <- req:
	case <-r.stopper.ShouldStop():
		return
	}
}

func (r *channelReactor) processStorageLoop() {

	reqs := make([]*storageReq, 0, 1024)
	done := false
	for {
		select {
		case req := <-r.processStorageC:
			reqs = append(reqs, req)

			// 取出所有req
			for !done {
				select {
				case req := <-r.processStorageC:
					exist := false
					for _, r := range reqs {
						if r.ch.channelId == req.ch.channelId && r.ch.channelType == req.ch.channelType {
							r.messages = append(r.messages, req.messages...)
							exist = true
							break
						}
					}
					if !exist {
						reqs = append(reqs, req)
					}
				default:
					done = true
				}
			}
			r.processStorage(reqs)

			reqs = reqs[:0]
			done = false

		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *channelReactor) processStorage(reqs []*storageReq) {
	r.Info("processStorage start...")
	for _, req := range reqs {
		sotreMessages := make([]wkdb.Message, 0, 1024)
		for _, msg := range req.messages {
			if msg.IsEncrypt {
				r.Warn("msg is encrypt, no storage", zap.Uint64("messageId", uint64(msg.MessageId)), zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))
				continue
			}
			sendPacket := msg.SendPacket
			sotreMessages = append(sotreMessages, wkdb.Message{
				RecvPacket: wkproto.RecvPacket{
					Framer: wkproto.Framer{
						RedDot:    sendPacket.Framer.RedDot,
						SyncOnce:  sendPacket.Framer.SyncOnce,
						NoPersist: sendPacket.Framer.NoPersist,
					},
					MessageID:   msg.MessageId,
					ClientMsgNo: sendPacket.ClientMsgNo,
					ClientSeq:   sendPacket.ClientSeq,
					FromUID:     msg.FromUid,
					ChannelID:   sendPacket.ChannelID,
					ChannelType: sendPacket.ChannelType,
					Expire:      sendPacket.Expire,
					Timestamp:   int32(time.Now().Unix()),
					Payload:     sendPacket.Payload,
				},
			})
		}
		// 存储消息
		results, err := r.s.store.AppendMessages(r.s.ctx, req.ch.channelId, req.ch.channelType, sotreMessages)
		if err != nil {
			r.Error("AppendMessages error", zap.Error(err))
		}
		if len(results) > 0 {
			for _, result := range results {
				for _, msg := range req.messages {
					if msg.MessageId == int64(result.LogId()) {
						msg.MessageId = int64(result.LogId())
						msg.MessageSeq = uint32(result.LogIndex())
						break
					}
				}
			}
		}
		var reason Reason
		var reasonCode wkproto.ReasonCode
		if err != nil {
			reason = ReasonError
			reasonCode = wkproto.ReasonSystemError
		} else {
			reason = ReasonSuccess
			reasonCode = wkproto.ReasonSuccess
		}
		sub := r.reactorSub(req.ch.key)
		sub.step(req.ch, &ChannelAction{ActionType: ChannelActionStorageResp, Messages: req.messages, Reason: reason, ReasonCode: reasonCode})

	}
	r.Info("processStorage done...")

}

type storageReq struct {
	ch       *channel
	messages []*ReactorChannelMessage
}

// =================================== 发送回执 ===================================

func (r *channelReactor) addSendackReq(req *sendackReq) {
	select {
	case r.processSendackC <- req:
	case <-r.stopper.ShouldStop():
		return
	}
}

func (r *channelReactor) processSendackLoop() {
	reqs := make([]*sendackReq, 0, 1024)
	done := false
	for {
		select {
		case req := <-r.processSendackC:
			reqs = append(reqs, req)
			// 取出所有req
			for !done {
				select {
				case req := <-r.processSendackC:
					reqs = append(reqs, req)
				default:
					done = true
				}
			}
			r.processSendack(reqs)

			reqs = reqs[:0]
			done = false
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *channelReactor) processSendack(reqs []*sendackReq) {
	var err error
	nodeFowardSendackPacketMap := map[uint64][]*ForwardSendackPacket{}
	for _, req := range reqs {
		for _, msg := range req.messages {
			sendack := &wkproto.SendackPacket{
				Framer:      msg.SendPacket.Framer,
				MessageID:   msg.MessageId,
				MessageSeq:  msg.MessageSeq,
				ClientSeq:   msg.SendPacket.ClientSeq,
				ClientMsgNo: msg.SendPacket.ClientMsgNo,
				ReasonCode:  req.reasonCode,
			}
			if msg.FromNodeId == r.opts.Cluster.NodeId { // 连接在本节点
				err = r.s.userReactor.writePacketByConnId(msg.FromUid, msg.FromConnId, sendack)
				if err != nil {
					r.Error("writePacketByConnId error", zap.Error(err))
				}
			} else { // 连接在其他节点，需要将消息转发出去
				packets := nodeFowardSendackPacketMap[msg.FromNodeId]
				packets = append(packets, &ForwardSendackPacket{
					Uid:     msg.FromUid,
					ConnId:  msg.FromConnId,
					Sendack: sendack,
				})
				nodeFowardSendackPacketMap[msg.FromNodeId] = packets
			}
		}
		// sub := r.reactorSub(req.ch.key)
		// sub.step(req.ch, &ChannelAction{ActionType: ChannelActionSendackResp, Messages: req.messages})
	}

	for nodeId, forwardSendackPackets := range nodeFowardSendackPacketMap {
		err = r.requestForwardSendack(nodeId, forwardSendackPackets)
		if err != nil {
			r.Error("requestForwardSendack error", zap.Error(err))
		}
		if err != nil {
			r.Error("channelForwardSendackPool.Submit error", zap.Error(err))
		}
	}
}

func (r *channelReactor) requestForwardSendack(nodeId uint64, packets []*ForwardSendackPacket) error {
	timeoutCtx, cancel := context.WithTimeout(r.s.ctx, time.Second*5)
	defer cancel()

	packetSet := ForwardSendackPacketSet(packets)
	data, err := packetSet.Marshal()
	if err != nil {
		return err
	}
	resp, err := r.s.cluster.RequestWithContext(timeoutCtx, nodeId, "/wk/forwardSendack", data)
	if err != nil {
		return err
	}
	if resp.Status != proto.Status_OK {
		var err error
		if len(resp.Body) > 0 {
			err = errors.New(string(resp.Body))
		} else {
			err = fmt.Errorf("requestForwardSendack failed, status[%d] error", resp.Status)
		}
		return err
	}
	return nil
}

type sendackReq struct {
	ch         *channel
	reasonCode wkproto.ReasonCode
	messages   []*ReactorChannelMessage
}

// =================================== 消息投递 ===================================

func (r *channelReactor) addDeliverReq(req *deliverReq) {
	select {
	case r.processDeliverC <- req:
	case <-r.stopper.ShouldStop():
		return
	}
}

func (r *channelReactor) processDeliverLoop() {
	reqs := make([]*deliverReq, 0, 1024)
	done := false
	for {
		select {
		case req := <-r.processDeliverC:
			reqs = append(reqs, req)
			// 取出所有req
			for !done {
				select {
				case req := <-r.processDeliverC:
					exist := false
					for _, r := range reqs {
						if r.channelId == req.channelId && r.channelType == req.channelType {
							r.messages = append(r.messages, req.messages...)
							exist = true
							break
						}
					}
					if !exist {
						reqs = append(reqs, req)
					}
				default:
					done = true
				}
			}
			r.processDeliver(reqs)

			reqs = reqs[:0]
			done = false
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *channelReactor) processDeliver(reqs []*deliverReq) {

	for _, req := range reqs {
		r.handleDeliver(req)
		sub := r.reactorSub(req.channelKey)
		reason := ReasonSuccess
		lastIndex := req.messages[len(req.messages)-1].Index
		sub.step(req.ch, &ChannelAction{ActionType: ChannelActionDeliverResp, Index: lastIndex, Reason: reason})
	}
}

func (r *channelReactor) handleDeliver(req *deliverReq) {
	r.s.deliverManager.deliver(req)
}

type deliverReq struct {
	ch          *channel
	channelId   string
	channelType uint8
	channelKey  string
	tagKey      string
	messages    []*ReactorChannelMessage
}
