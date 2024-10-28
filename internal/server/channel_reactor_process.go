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
		sub.step(req.ch, &ChannelAction{
			UniqueNo:   req.ch.uniqueNo,
			ActionType: ChannelActionInitResp,
			Reason:     ReasonError,
		})
		return
	}
	_, err = req.ch.makeReceiverTag()
	if err != nil {
		r.Error("processInit: makeReceiverTag failed", zap.Error(err))
		sub.step(req.ch, &ChannelAction{
			UniqueNo:   req.ch.uniqueNo,
			ActionType: ChannelActionInitResp,
			LeaderId:   node.Id,
			Reason:     ReasonError,
		})
		return
	}
	sub.step(req.ch, &ChannelAction{
		UniqueNo:   req.ch.uniqueNo,
		ActionType: ChannelActionInitResp,
		LeaderId:   node.Id,
		Reason:     ReasonSuccess,
	})
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

	for i, msg := range req.messages {

		// 没有连接id解密不了（没有连接id，说明发送者的连接突然断开了，那么这条消息也没办法解密）
		if msg.FromConnId == 0 {
			r.Warn("msg fromConnId is 0", zap.String("uid", msg.FromUid), zap.String("deviceId", msg.FromDeviceId), zap.Int64("connId", msg.FromConnId))
			continue
		}

		if !msg.IsEncrypt { // 没有加密(系统api发的消息和其他节点转发过来的消息都是未加密的，所以不需要再进行解密操作了)，直接跳过解密过程
			continue
		}

		r.MessageTrace("解密消息", msg.SendPacket.ClientMsgNo, "processPayloadDecrypt", zap.Int64("messageId", msg.MessageId), zap.String("uid", msg.FromUid), zap.String("deviceId", msg.FromDeviceId), zap.Int64("connId", msg.FromConnId))

		var err error
		var decryptPayload []byte
		conn := r.s.userReactor.getConnContextById(msg.FromUid, msg.FromConnId)
		if conn != nil {
			decryptPayload, err = r.s.checkAndDecodePayload(msg.SendPacket, conn)
			if err != nil {
				r.Warn("decrypt payload error", zap.String("uid", msg.FromUid), zap.String("deviceId", msg.FromDeviceId), zap.Int64("connId", msg.FromConnId), zap.Error(err))
				r.MessageTrace("解密消息失败", msg.SendPacket.ClientMsgNo, "processPayloadDecrypt", zap.Error(err))
			}
		}
		if len(decryptPayload) > 0 {
			msg.SendPacket.Payload = decryptPayload
			msg.IsEncrypt = false
			req.messages[i] = msg
		}
	}

	sub := r.reactorSub(req.ch.key)
	sub.step(req.ch, &ChannelAction{
		Reason:     ReasonSuccess,
		UniqueNo:   req.ch.uniqueNo,
		ActionType: ChannelActionPayloadDecryptResp,
		Messages:   req.messages,
	})

}

type payloadDecryptReq struct {
	ch       *channel
	messages []ReactorChannelMessage
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
	var err error
	for _, req := range reqs {

		if r.opts.Logger.TraceOn {
			for _, msg := range req.messages {
				r.MessageTrace("转发消息", msg.SendPacket.ClientMsgNo, "processForward")
			}
		}

		var newLeaderId uint64
		if !r.s.clusterServer.NodeIsOnline(req.leaderId) { // 如果领导不在线,重新获取领导
			timeoutCtx, cancel := context.WithTimeout(r.s.ctx, time.Second*1) // 需要快速返回，这样会进行下次重试，如果超时时间太长，会阻塞导致下次重试间隔太长
			defer cancel()
			newLeaderId, err = r.s.cluster.LeaderIdOfChannel(timeoutCtx, req.ch.channelId, req.ch.channelType)
			if err != nil {
				r.Warn("processForward: LeaderIdOfChannel error", zap.Error(err))
			} else {
				err = errors.New("leader change")
			}
		} else {
			if len(req.messages) == 1 {
				r.Debug("forward to leader", zap.Int64("messageId", req.messages[0].MessageId), zap.Uint64("leaderId", req.leaderId), zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))

			} else {
				r.Debug("forward to leader", zap.Uint64("leaderId", req.leaderId), zap.Int("msgCount", len(req.messages)), zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))
			}

			newLeaderId, err = r.handleForward(req)
			if err != nil {
				r.Warn("handleForward error", zap.Error(err))
			}
		}

		if err != nil {
			if r.opts.Logger.TraceOn {
				for _, msg := range req.messages {
					r.MessageTrace("转发消息失败", msg.SendPacket.ClientMsgNo, "processForward", zap.Error(err))
				}
			}
		}

		var reason Reason
		if err != nil {
			reason = ReasonError
		} else {
			reason = ReasonSuccess
		}
		if newLeaderId > 0 {
			if r.opts.Logger.TraceOn {
				for _, msg := range req.messages {
					r.MessageTrace("频道领导发生改变", msg.SendPacket.ClientMsgNo, "processForward", zap.Uint64("newLeaderId", newLeaderId), zap.Uint64("oldLeaderId", req.leaderId))
				}
			}
			r.Info("leader change", zap.Uint64("newLeaderId", newLeaderId), zap.Uint64("oldLeaderId", req.leaderId), zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))
			sub := r.reactorSub(req.ch.key)
			sub.step(req.ch, &ChannelAction{
				UniqueNo:   req.ch.uniqueNo,
				ActionType: ChannelActionLeaderChange,
				LeaderId:   newLeaderId,
			})
		}
		sub := r.reactorSub(req.ch.key)
		sub.step(req.ch, &ChannelAction{
			UniqueNo:   req.ch.uniqueNo,
			ActionType: ChannelActionForwardResp,
			Messages:   req.messages,
			Reason:     reason,
		})

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

	needChangeLeader, err := r.requestChannelFoward(req.leaderId, ChannelFowardReq{
		ChannelId:   req.ch.channelId,
		ChannelType: req.ch.channelType,
		Messages:    req.messages,
	})
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

func (r *channelReactor) requestChannelFoward(nodeId uint64, req ChannelFowardReq) (bool, error) {
	timeoutCtx, cancel := context.WithTimeout(r.s.ctx, time.Second*5)
	defer cancel()

	data, err := req.Marshal()
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
	messages []ReactorChannelMessage
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

	fromUidMap := map[string]wkproto.ReasonCode{}
	// 权限判断
	sub := r.reactorSub(req.ch.key)
	for i, msg := range req.messages {

		if msg.ReasonCode != wkproto.ReasonSuccess {
			r.Debug("msg reasonCode is not success, no permission check", zap.Uint64("messageId", uint64(msg.MessageId)), zap.String("fromUid", msg.FromUid), zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))
			continue
		}

		if msg.IsSystem { // 如果是系统发的消息，直接通过
			req.messages[i].ReasonCode = wkproto.ReasonSuccess
			continue
		}

		if _, ok := fromUidMap[msg.FromUid]; ok { // 已经判断过权限
			req.messages[i].ReasonCode = fromUidMap[msg.FromUid]
			continue
		}

		r.MessageTrace("权限验证", msg.SendPacket.ClientMsgNo, "processPermission")

		reasonCode, err := r.hasPermission(req.ch.channelId, req.ch.channelType, msg.FromUid, req.ch)
		if err != nil {
			r.Error("hasPermission error", zap.Error(err))
			req.messages[i].ReasonCode = wkproto.ReasonSystemError
			fromUidMap[msg.FromUid] = wkproto.ReasonSystemError
			continue
		}

		if reasonCode != wkproto.ReasonSuccess {
			r.MessageTrace("权限验证失败", msg.SendPacket.ClientMsgNo, "processPermission", zap.String("reasonCode", reasonCode.String()), zap.Error(errors.New("permission check failed")))
		}

		req.messages[i].ReasonCode = reasonCode
		fromUidMap[msg.FromUid] = reasonCode
	}
	// 返回成功
	lastMsg := req.messages[len(req.messages)-1]
	sub.step(req.ch, &ChannelAction{
		UniqueNo:   req.ch.uniqueNo,
		ActionType: ChannelActionPermissionCheckResp,
		Index:      lastMsg.Index,
		Messages:   req.messages,
		Reason:     ReasonSuccess,
	})
}

func (r *channelReactor) hasPermission(channelId string, channelType uint8, fromUid string, ch *channel) (wkproto.ReasonCode, error) {

	// 资讯频道是公开的，直接通过
	if channelType == wkproto.ChannelTypeInfo {
		return wkproto.ReasonSuccess, nil
	}

	// 如果发送者是系统账号，则直接通过
	systemAccount := r.s.systemUIDManager.SystemUID(fromUid)
	if systemAccount {
		return wkproto.ReasonSuccess, nil
	}

	// 如果是个人频道，则请求接受者是否接受发送者的消息
	if channelType == wkproto.ChannelTypePerson {
		uid1, uid2 := GetFromUIDAndToUIDWith(channelId)
		toUid := ""
		if uid1 == fromUid {
			toUid = uid2
		} else {
			toUid = uid1
		}
		// 如果接收者是系统账号，则直接通过
		systemAccount = r.s.systemUIDManager.SystemUID(toUid)
		if systemAccount {
			return wkproto.ReasonSuccess, nil
		}

		// 请求个人频道是否允许发送
		reasonCode, err := r.requestAllowSend(fromUid, toUid)
		if err != nil {
			return wkproto.ReasonSystemError, err
		}
		return reasonCode, nil
	}

	channelInfo := ch.info

	if channelInfo.Ban { // 频道被封禁
		return wkproto.ReasonBan, nil
	}

	if channelInfo.Disband { // 频道已解散
		return wkproto.ReasonDisband, nil
	}

	realChannelId := channelId

	if r.opts.IsCmdChannel(channelId) {
		realChannelId = r.opts.CmdChannelConvertOrginalChannel(channelId)
	}

	// 判断是否是黑名单内
	isDenylist, err := r.s.store.ExistDenylist(realChannelId, channelType, fromUid)
	if err != nil {
		r.Error("ExistDenylist error", zap.Error(err))
		return wkproto.ReasonSystemError, err
	}
	if isDenylist {
		return wkproto.ReasonInBlacklist, nil
	}

	// 判断是否是订阅者
	isSubscriber, err := r.s.store.ExistSubscriber(realChannelId, channelType, fromUid)
	if err != nil {
		r.Error("ExistSubscriber error", zap.Error(err))
		return wkproto.ReasonSystemError, err
	}
	if !isSubscriber {
		return wkproto.ReasonSubscriberNotExist, nil
	}

	// 判断是否在白名单内
	if !r.opts.WhitelistOffOfPerson || channelType != wkproto.ChannelTypePerson { // 如果不是个人频道或者个人频道白名单开关打开，则判断是否在白名单内
		hasAllowlist, err := r.s.store.HasAllowlist(realChannelId, channelType)
		if err != nil {
			r.Error("HasAllowlist error", zap.Error(err))
			return wkproto.ReasonSystemError, err
		}

		if hasAllowlist { // 如果频道有白名单，则判断是否在白名单内
			isAllowlist, err := r.s.store.ExistAllowlist(realChannelId, channelType, fromUid)
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

func (r *channelReactor) requestAllowSend(from, to string) (wkproto.ReasonCode, error) {

	leaderNode, err := r.s.cluster.SlotLeaderOfChannel(to, wkproto.ChannelTypePerson)
	if err != nil {
		return wkproto.ReasonSystemError, err
	}
	if leaderNode.Id == r.opts.Cluster.NodeId {
		return r.allowSend(from, to)
	}

	timeoutCtx, cancel := context.WithTimeout(r.s.ctx, time.Second*5)
	defer cancel()

	req := &allowSendReq{
		From: from,
		To:   to,
	}
	bodyBytes, err := req.Marshal()
	if err != nil {
		return wkproto.ReasonSystemError, err
	}

	resp, err := r.s.cluster.RequestWithContext(timeoutCtx, leaderNode.Id, "/wk/allowSend", bodyBytes)
	if err != nil {
		return wkproto.ReasonSystemError, err
	}
	if resp.Status == proto.Status_OK {
		return wkproto.ReasonSuccess, nil
	}
	if resp.Status == proto.Status_ERROR {
		return wkproto.ReasonSystemError, errors.New(string(resp.Body))
	}
	return wkproto.ReasonCode(resp.Status), nil
}

func (r *channelReactor) allowSend(from, to string) (wkproto.ReasonCode, error) {
	// 判断是否是黑名单内
	isDenylist, err := r.s.store.ExistDenylist(to, wkproto.ChannelTypePerson, from)
	if err != nil {
		r.Error("ExistDenylist error", zap.String("from", from), zap.String("to", to), zap.Error(err))
		return wkproto.ReasonSystemError, err
	}
	if isDenylist {
		return wkproto.ReasonInBlacklist, nil
	}

	if !r.opts.WhitelistOffOfPerson {
		// 判断是否在白名单内
		isAllowlist, err := r.s.store.ExistAllowlist(to, wkproto.ChannelTypePerson, from)
		if err != nil {
			r.Error("ExistAllowlist error", zap.Error(err))
			return wkproto.ReasonSystemError, err
		}
		if !isAllowlist {
			return wkproto.ReasonNotInWhitelist, nil
		}
	}

	return wkproto.ReasonSuccess, nil
}

type permissionReq struct {
	ch       *channel
	messages []ReactorChannelMessage
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
	for !r.stopped.Load() {
		select {
		case req := <-r.processStorageC:
			reqs = append(reqs, req)

			// 取出所有req
			for !done && !r.stopped.Load() {
				select {
				case req := <-r.processStorageC:
					exist := false
					for _, rq := range reqs {
						if rq.ch.channelId == req.ch.channelId && rq.ch.channelType == req.ch.channelType {
							rq.messages = append(rq.messages, req.messages...)
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

	for _, req := range reqs {
		messages := make([]wkdb.Message, 0, len(req.messages))
		sotreMessages := make([]wkdb.Message, 0, len(messages))
		// 将reactorChannelMessage转换为wkdb.Message
		for _, reactorMsg := range req.messages {

			if reactorMsg.ReasonCode != wkproto.ReasonSuccess {
				r.Debug("msg reasonCode is not success, no storage", zap.Uint64("messageId", uint64(reactorMsg.MessageId)), zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))
				continue

			}

			msg := wkdb.Message{
				RecvPacket: wkproto.RecvPacket{
					Framer: wkproto.Framer{
						RedDot:    reactorMsg.SendPacket.Framer.RedDot,
						SyncOnce:  reactorMsg.SendPacket.Framer.SyncOnce,
						NoPersist: reactorMsg.SendPacket.Framer.NoPersist,
					},
					Setting:     reactorMsg.SendPacket.Setting,
					MessageID:   reactorMsg.MessageId,
					ClientMsgNo: reactorMsg.SendPacket.ClientMsgNo,
					ClientSeq:   reactorMsg.SendPacket.ClientSeq,
					FromUID:     reactorMsg.FromUid,
					ChannelID:   req.ch.channelId,
					ChannelType: reactorMsg.SendPacket.ChannelType,
					Expire:      reactorMsg.SendPacket.Expire,
					Timestamp:   int32(time.Now().Unix()),
					Topic:       reactorMsg.SendPacket.Topic,
					StreamNo:    reactorMsg.SendPacket.StreamNo,
					Payload:     reactorMsg.SendPacket.Payload,
				},
			}
			messages = append(messages, msg)

			if reactorMsg.IsEncrypt {
				r.Warn("msg is encrypt, no storage", zap.Uint64("messageId", uint64(msg.MessageID)), zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))
				continue
			}
			if msg.NoPersist { // 不需要存储，跳过
				continue
			}
			sotreMessages = append(sotreMessages, msg)

		}

		reason := ReasonSuccess
		if len(sotreMessages) > 0 {
			// 存储消息
			if len(sotreMessages) == 1 {
				r.Debug("store message", zap.Uint64("messageId", uint64(sotreMessages[0].MessageID)), zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))
			} else {
				r.Debug("store messages", zap.Int("msgCount", len(sotreMessages)), zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))
			}
			if r.opts.Logger.TraceOn {
				for _, sotreMessage := range sotreMessages {
					r.MessageTrace("存储消息", sotreMessage.ClientMsgNo, "processStorage")
				}
			}

			results, err := r.s.store.AppendMessages(r.s.ctx, req.ch.channelId, req.ch.channelType, sotreMessages)
			if err != nil {
				r.Error("AppendMessages error", zap.Error(err))
				if r.opts.Logger.TraceOn {
					for _, sotreMessage := range sotreMessages {
						r.MessageTrace("存储消息失败", sotreMessage.ClientMsgNo, "processStorage", zap.Error(err))
					}
				}
			}
			if err != nil {
				reason = ReasonError
			} else {
				reason = ReasonSuccess
			}

			if len(results) > 0 {
				for _, result := range results {
					msgLen := len(req.messages)
					logId := int64(result.LogId())
					logIndex := uint32(result.LogIndex())
					for i := 0; i < msgLen; i++ {
						msg := req.messages[i]
						if msg.MessageId == logId {
							msg.MessageId = logId
							msg.MessageSeq = logIndex
							req.messages[i] = msg
							break
						}
					}
				}
			}
		}

		if r.opts.WebhookOn() {
			// 赋值messageeq
			for i, msg := range messages {
				for _, cmsg := range req.messages {
					if msg.MessageID == cmsg.MessageId {
						msg.MessageSeq = cmsg.MessageSeq
						messages[i] = msg
						break
					}
				}
			}

			// 将消息存储到webhook的推送队列内
			err := r.s.store.AppendMessageOfNotifyQueue(messages)
			if err != nil {
				r.Error("AppendMessageOfNotifyQueue error", zap.Error(err))
				reason = ReasonError
			}
		}
		// 返回存储结果
		r.respStoreResult(req, reason)

	}

}

func (r *channelReactor) respStoreResult(req *storageReq, reason Reason) {
	sub := r.reactorSub(req.ch.key)
	lastIndex := req.messages[len(req.messages)-1].Index
	sub.step(req.ch, &ChannelAction{
		UniqueNo:   req.ch.uniqueNo,
		ActionType: ChannelActionStorageResp,
		Index:      lastIndex,
		Reason:     reason,
		Messages:   req.messages,
	})
}

type storageReq struct {
	ch       *channel
	messages []ReactorChannelMessage
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

			if msg.FromUid == r.opts.SystemUID { // 如果是系统消息，不需要发送ack
				continue
			}
			r.MessageTrace("发送ack", msg.SendPacket.ClientMsgNo, "processSendack")

			sendack := &wkproto.SendackPacket{
				Framer:      msg.SendPacket.Framer,
				MessageID:   msg.MessageId,
				MessageSeq:  msg.MessageSeq,
				ClientSeq:   msg.SendPacket.ClientSeq,
				ClientMsgNo: msg.SendPacket.ClientMsgNo,
				ReasonCode:  msg.ReasonCode,
			}
			if msg.FromNodeId == r.opts.Cluster.NodeId { // 连接在本节点
				err = r.s.userReactor.writePacketByConnId(msg.FromUid, msg.FromConnId, sendack)
				if err != nil {
					r.Error("writePacketByConnId error", zap.Error(err), zap.Uint64("nodeId", msg.FromNodeId), zap.Int64("connId", msg.FromConnId))
				}
			} else { // 连接在其他节点，需要将消息转发出去
				packets := nodeFowardSendackPacketMap[msg.FromNodeId]
				packets = append(packets, &ForwardSendackPacket{
					Uid:      msg.FromUid,
					DeviceId: msg.FromDeviceId,
					ConnId:   msg.FromConnId,
					Sendack:  sendack,
				})
				nodeFowardSendackPacketMap[msg.FromNodeId] = packets
			}
		}
		lastMsg := req.messages[len(req.messages)-1]
		sub := r.reactorSub(req.ch.key)
		sub.step(req.ch, &ChannelAction{
			UniqueNo:   req.ch.uniqueNo,
			ActionType: ChannelActionSendackResp,
			Index:      lastMsg.Index,
			Reason:     ReasonSuccess,
		})
	}

	for nodeId, forwardSendackPackets := range nodeFowardSendackPacketMap {
		err = r.requestForwardSendack(nodeId, forwardSendackPackets)
		if err != nil {
			r.Error("requestForwardSendack error", zap.Error(err), zap.Uint64("nodeId", nodeId))
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
	ch       *channel
	messages []ReactorChannelMessage
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
					for _, rq := range reqs {
						if rq.channelId == req.channelId && rq.channelType == req.channelType {
							rq.messages = append(rq.messages, req.messages...)
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

		lastIndex := req.messages[len(req.messages)-1].Index // 最后一条消息的index

		deliverMessages := make([]ReactorChannelMessage, 0, len(req.messages))
		for _, msg := range req.messages {
			if msg.ReasonCode != wkproto.ReasonSuccess {
				r.Debug("msg reasonCode is not success, no deliver", zap.Uint64("messageId", uint64(msg.MessageId)), zap.String("channelId", req.channelId), zap.Uint8("channelType", req.channelType))
				continue
			}

			deliverMessages = append(deliverMessages, msg)

		}

		req.messages = deliverMessages

		if len(deliverMessages) > 0 {
			for _, deliverMessage := range deliverMessages {
				r.MessageTrace("投递消息", deliverMessage.SendPacket.ClientMsgNo, "processDeliver", zap.Int("batchCount", len(deliverMessages)))
			}
			// 投递消息
			r.handleDeliver(req)
		}

		sub := r.reactorSub(req.ch.key)
		reason := ReasonSuccess

		sub.step(req.ch, &ChannelAction{
			UniqueNo:   req.ch.uniqueNo,
			ActionType: ChannelActionDeliverResp,
			Index:      lastIndex,
			Reason:     reason,
		})
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
	messages    []ReactorChannelMessage
}

// =================================== 关闭请求 ===================================

func (r *channelReactor) addCloseReq(req *closeReq) {
	select {
	case r.processCloseC <- req:
	case <-r.stopper.ShouldStop():
		return
	}
}

func (r *channelReactor) processCloseLoop() {
	for {
		select {
		case req := <-r.processCloseC:
			r.processClose(req)
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *channelReactor) processClose(req *closeReq) {

	r.Info("channel close", zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))

	sub := r.reactorSub(req.ch.key)
	sub.removeChannel(req.ch.key)
}

type closeReq struct {
	ch *channel
}

// =================================== 检查tag的有效性 ===================================

func (r *channelReactor) addCheckTagReq(req *checkTagReq) {
	select {
	case r.processCheckTagC <- req:
	case <-r.stopper.ShouldStop():
		return
	}
}

func (r *channelReactor) processCheckTagLoop() {
	for {
		select {
		case req := <-r.processCheckTagC:
			r.processCheckTag(req)
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *channelReactor) processCheckTag(req *checkTagReq) {

	receiverTagKey := req.ch.receiverTagKey.Load()
	if receiverTagKey == "" { // 如果不存在tag则重新生成
		_, err := req.ch.makeReceiverTag()
		if err != nil {
			r.Error("makeReceiverTag failed", zap.Error(err))
		}
		return
	}

	// 检查tag是否有效
	tag := r.s.tagManager.getReceiverTag(receiverTagKey)
	if tag == nil {
		r.Info("tag is invalid", zap.String("receiverTagKey", receiverTagKey))
		_, err := req.ch.makeReceiverTag()
		if err != nil {
			r.Error("makeReceiverTag failed", zap.Error(err))
		}
		return
	}

	needMakeTag := false // 是否需要重新make tag
	for _, nodeUser := range tag.users {
		for _, uid := range nodeUser.uids {
			leaderId, err := r.s.cluster.SlotLeaderIdOfChannel(uid, wkproto.ChannelTypePerson)
			if err != nil {
				r.Error("processCheckTag: SlotLeaderIdOfChannel error", zap.Error(err))
				return
			}
			if leaderId != nodeUser.nodeId { // 如果当前用户不属于当前节点，则说明分布式配置有变化，需要重新生成tag
				needMakeTag = true
				break
			}
		}
	}
	if needMakeTag {
		_, err := req.ch.makeReceiverTag()
		if err != nil {
			r.Error("makeReceiverTag failed", zap.Error(err))
		} else {
			r.Info("makeReceiverTag success", zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))
		}
	}
}

type checkTagReq struct {
	ch *channel
}
