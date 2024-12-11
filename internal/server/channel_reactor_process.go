package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// =================================== 初始化 ===================================

func (r *channelReactor) addInitReq(req *initReq) {
	select {
	case r.processInitC <- req:
	default:
		r.Warn("processInitC is full, ignore", zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))
		req.sub.step(req.ch, &ChannelAction{
			UniqueNo:   req.ch.uniqueNo,
			ActionType: ChannelActionInitResp,
			Reason:     ReasonError,
		})
	}
}

func (r *channelReactor) processInitLoop() {
	reqs := make([]*initReq, 0)
	done := false
	for {
		select {
		case req := <-r.processInitC:
			reqs = append(reqs, req)
			// 取出所有req
			for !done {
				select {
				case req := <-r.processInitC:
					reqs = append(reqs, req)
				default:
					done = true
				}
			}
			r.processInits(reqs)
			reqs = reqs[:0]
			done = false
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *channelReactor) processInits(reqs []*initReq) {

	if len(reqs) == 1 {
		r.processInit(reqs[0])
		return
	}

	timeoutCtx, cancel := r.WithTimeout()
	defer cancel()
	eg, _ := errgroup.WithContext(timeoutCtx)
	eg.SetLimit(100)
	for _, req := range reqs {
		req := req
		eg.Go(func() error {
			r.processInit(req)
			return nil
		})
	}
	_ = eg.Wait()
}

func (r *channelReactor) processInit(req *initReq) {
	timeoutCtx, cancel := context.WithTimeout(r.s.ctx, time.Second*5)
	cfg, err := r.s.cluster.LoadOrCreateChannel(timeoutCtx, req.ch.channelId, req.ch.channelType)
	cancel()
	if err != nil {
		r.Error("channel init failed", zap.Error(err))
		req.sub.step(req.ch, &ChannelAction{
			UniqueNo:   req.ch.uniqueNo,
			ActionType: ChannelActionInitResp,
			Reason:     ReasonError,
		})
		return
	}

	// if cfg.LeaderId == r.s.opts.Cluster.NodeId { // 只有领导才需要makeReceiverTag
	// 	_, err = req.ch.makeReceiverTag()
	// 	if err != nil {
	// 		r.Error("processInit: makeReceiverTag failed", zap.Error(err))
	// 		req.sub.step(req.ch, &ChannelAction{
	// 			UniqueNo:   req.ch.uniqueNo,
	// 			ActionType: ChannelActionInitResp,
	// 			LeaderId:   cfg.LeaderId,
	// 			Reason:     ReasonError,
	// 		})
	// 		return
	// 	}
	// }

	if r.opts.IsLocalNode(cfg.LeaderId) {
		trace.GlobalTrace.Metrics.Cluster().ChannelActiveCountAdd(1)
	}

	req.sub.step(req.ch, &ChannelAction{
		UniqueNo:   req.ch.uniqueNo,
		ActionType: ChannelActionInitResp,
		LeaderId:   cfg.LeaderId,
		Reason:     ReasonSuccess,
	})
}

type initReq struct {
	ch  *channel
	sub *channelReactorSub
}

// =================================== payload解密 ===================================

func (r *channelReactor) addPayloadDecryptReq(req *payloadDecryptReq) {
	select {
	case r.processPayloadDecryptC <- req:
	default:
		r.Warn("processPayloadDecryptC is full, ignore", zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))
		actionType := ChannelActionPayloadDecryptResp
		if req.isStream {
			actionType = ChannelActionStreamPayloadDecryptResp
		}
		req.sub.step(req.ch, &ChannelAction{
			Reason:     ReasonError,
			UniqueNo:   req.ch.uniqueNo,
			ActionType: actionType,
			Messages:   req.messages,
		})

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
	err := r.processGoPool.Submit(func() {
		r.handlePayloadDecrypt(req)
	})
	if err != nil {
		r.Error("processPayloadDecrypt failed, submit error", zap.Error(err))
		actionType := ChannelActionPayloadDecryptResp
		if req.isStream {
			actionType = ChannelActionStreamPayloadDecryptResp
		}
		req.sub.step(req.ch, &ChannelAction{
			Reason:     ReasonError,
			UniqueNo:   req.ch.uniqueNo,
			ActionType: actionType,
			Messages:   req.messages,
		})
	}
}

func (r *channelReactor) handlePayloadDecrypt(req *payloadDecryptReq) {

	for i, msg := range req.messages {

		if !msg.IsEncrypt { // 没有加密(系统api发的消息和其他节点转发过来的消息都是未加密的，所以不需要再进行解密操作了)，直接跳过解密过程
			continue
		}

		// 没有连接id解密不了（没有连接id，说明发送者的连接突然断开了，那么这条消息也没办法解密）
		if msg.FromConnId == 0 {
			r.Debug("msg fromConnId is 0", zap.String("uid", msg.FromUid), zap.String("deviceId", msg.FromDeviceId), zap.Int64("connId", msg.FromConnId))
			req.messages[i].ReasonCode = wkproto.ReasonSenderOffline
			continue
		}

		msg.IsEncrypt = false // 设置为已解密

		r.MessageTrace("解密消息", msg.SendPacket.ClientMsgNo, "processPayloadDecrypt", zap.Int64("messageId", msg.MessageId), zap.String("uid", msg.FromUid), zap.String("deviceId", msg.FromDeviceId), zap.Int64("connId", msg.FromConnId))

		var err error
		var decryptPayload []byte
		conn := r.s.userReactor.getConnById(msg.FromUid, msg.FromConnId)
		if conn != nil && len(msg.SendPacket.Payload) > 0 {
			decryptPayload, err = r.s.checkAndDecodePayload(msg.SendPacket, conn)
			if err != nil {
				msg.ReasonCode = wkproto.ReasonPayloadDecodeError
				r.Warn("decrypt payload error", zap.String("uid", msg.FromUid), zap.String("deviceId", msg.FromDeviceId), zap.Int64("connId", msg.FromConnId), zap.Error(err))
				r.MessageTrace("解密消息失败", msg.SendPacket.ClientMsgNo, "processPayloadDecrypt", zap.Error(err))
			}
		} else {
			if conn == nil {
				msg.ReasonCode = wkproto.ReasonSenderOffline
			} else {
				msg.ReasonCode = wkproto.ReasonPayloadDecodeError
			}

		}
		if len(decryptPayload) > 0 {
			msg.SendPacket.Payload = decryptPayload
		}
		req.messages[i] = msg
	}

	actionType := ChannelActionPayloadDecryptResp
	if req.isStream {
		actionType = ChannelActionStreamPayloadDecryptResp
	}

	req.sub.step(req.ch, &ChannelAction{
		Reason:     ReasonSuccess,
		UniqueNo:   req.ch.uniqueNo,
		ActionType: actionType,
		Messages:   req.messages,
	})

}

type payloadDecryptReq struct {
	ch       *channel
	messages []ReactorChannelMessage
	isStream bool // 是流消息
	sub      *channelReactorSub
}

// =================================== 转发 ===================================

func (r *channelReactor) addForwardReq(req *forwardReq) {
	select {
	case r.processForwardC <- req:
	default:
		r.Warn("processForwardC is full, ignore", zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))
		req.sub.step(req.ch, &ChannelAction{
			UniqueNo:   req.ch.uniqueNo,
			ActionType: ChannelActionForwardResp,
			Reason:     ReasonError,
		})
	}
}

func (r *channelReactor) processForwardLoop() {
	reqs := make([]*forwardReq, 0)
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
			r.processForwards(reqs)

			reqs = reqs[:0]
			done = false
		case <-r.stopper.ShouldStop():
			return
		}
	}

}

func (r *channelReactor) processForwards(reqs []*forwardReq) {

	if len(reqs) == 1 {
		req := reqs[0]
		r.processForward(req)
		return
	}

	timeoutCtx, cancel := r.WithTimeout()
	defer cancel()
	eg, _ := errgroup.WithContext(timeoutCtx)
	eg.SetLimit(1000)
	for _, req := range reqs {
		req := req
		eg.Go(func() error {
			r.processForward(req)
			return nil
		})
	}
	_ = eg.Wait()
}

func (r *channelReactor) processForward(req *forwardReq) {
	var err error
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

	// var reason Reason
	// if err != nil {
	// 	reason = ReasonError
	// } else {
	// 	reason = ReasonSuccess
	// }
	if newLeaderId > 0 {
		if r.opts.Logger.TraceOn {
			for _, msg := range req.messages {
				r.MessageTrace("频道领导发生改变", msg.SendPacket.ClientMsgNo, "processForward", zap.Uint64("newLeaderId", newLeaderId), zap.Uint64("oldLeaderId", req.leaderId))
			}
		}
		r.Info("leader change", zap.Uint64("newLeaderId", newLeaderId), zap.Uint64("oldLeaderId", req.leaderId), zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))
		req.sub.step(req.ch, &ChannelAction{
			UniqueNo:   req.ch.uniqueNo,
			ActionType: ChannelActionLeaderChange,
			LeaderId:   newLeaderId,
		})
	}
	req.sub.step(req.ch, &ChannelAction{
		UniqueNo:   req.ch.uniqueNo,
		ActionType: ChannelActionForwardResp,
		Messages:   req.messages,
		Reason:     ReasonSuccess, // 直接返回成功防止不断重复转发，TODO：最好的办法是指定重试次数后再移除
	})
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
		r.Error("requestChannelFoward unkown error", zap.Error(err), zap.Uint64("nodeId", nodeId))
		return false, err
	}
	if resp.Status == proto.Status(errCodeNotIsChannelLeader) { // 转发下去的节点不是频道领导，这时候要重新获取下领导节点
		return true, nil
	}

	if resp.Status != proto.StatusOK {
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
	sub      *channelReactorSub
}

// =================================== 发送权限判断 ===================================
func (r *channelReactor) addPermissionReq(req *permissionReq) {
	select {
	case r.processPermissionC <- req:
	default:
		r.Warn("processPermissionC is full, ignore", zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))
		req.sub.step(req.ch, &ChannelAction{
			UniqueNo:   req.ch.uniqueNo,
			ActionType: ChannelActionPermissionCheckResp,
			Reason:     ReasonError,
		})
	}
}

func (r *channelReactor) processPermissionLoop() {
	reqs := make([]*permissionReq, 0)
	done := false
	for {
		select {
		case req := <-r.processPermissionC:
			reqs = append(reqs, req)
			// 取出所有req
			for !done {
				select {
				case req := <-r.processPermissionC:
					reqs = append(reqs, req)
				default:
					done = true
				}
			}
			r.processPermissions(reqs)
			reqs = reqs[:0]
			done = false
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *channelReactor) processPermissions(reqs []*permissionReq) {

	for _, req := range reqs {
		req := req
		err := r.processGoPool.Submit(func() {
			r.processPermission(req)
		})
		if err != nil {
			r.Error("processPermission failed, submit error", zap.Error(err))
			req.sub.step(req.ch, &ChannelAction{
				UniqueNo:   req.ch.uniqueNo,
				ActionType: ChannelActionPermissionCheckResp,
				Reason:     ReasonError,
			})
		}
	}
}

func (r *channelReactor) processPermission(req *permissionReq) {

	fromUidMap := map[string]wkproto.ReasonCode{}
	// 权限判断
	for i, msg := range req.messages {
		if msg.ReasonCode != wkproto.ReasonSuccess {
			r.Debug("msg reasonCode is not success, no permission check", zap.Uint64("messageId", uint64(msg.MessageId)), zap.String("fromUid", msg.FromUid), zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))
			continue
		}

		if r.opts.IsSystemDevice(msg.FromDeviceId) { // 如果是系统发的消息，直接通过
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
			r.Info("permission check failed", zap.String("fromUid", msg.FromUid), zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType), zap.String("reasonCode", reasonCode.String()))
			r.MessageTrace("权限验证失败", msg.SendPacket.ClientMsgNo, "processPermission", zap.String("reasonCode", reasonCode.String()), zap.Error(errors.New("permission check failed")))
		}

		req.messages[i].ReasonCode = reasonCode
		fromUidMap[msg.FromUid] = reasonCode
	}
	// 返回成功
	lastMsg := req.messages[len(req.messages)-1]
	req.sub.step(req.ch, &ChannelAction{
		UniqueNo:   req.ch.uniqueNo,
		ActionType: ChannelActionPermissionCheckResp,
		Index:      lastMsg.Index,
		Messages:   req.messages,
		Reason:     ReasonSuccess,
	})
}

func (r *channelReactor) hasPermission(channelId string, channelType uint8, fromUid string, ch *channel) (wkproto.ReasonCode, error) {

	realFakeChannelId := channelId
	if r.opts.IsCmdChannel(channelId) {
		realFakeChannelId = r.opts.CmdChannelConvertOrginalChannel(channelId)
	}

	// 资讯频道是公开的，直接通过
	if channelType == wkproto.ChannelTypeInfo {
		return wkproto.ReasonSuccess, nil
	}

	// 客服频道是公开的，直接通过
	if channelType == wkproto.ChannelTypeCustomerService {
		return wkproto.ReasonSuccess, nil
	}

	// 如果发送者是系统账号，则直接通过
	systemAccount := r.s.systemUIDManager.SystemUID(fromUid)
	if systemAccount {
		return wkproto.ReasonSuccess, nil
	}

	// 如果是个人频道，则请求接受者是否接受发送者的消息
	if channelType == wkproto.ChannelTypePerson {
		uid1, uid2 := GetFromUIDAndToUIDWith(realFakeChannelId)
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

	// 判断是否是黑名单内
	isDenylist, err := r.s.store.ExistDenylist(realFakeChannelId, channelType, fromUid)
	if err != nil {
		r.Error("ExistDenylist error", zap.Error(err))
		return wkproto.ReasonSystemError, err
	}
	if isDenylist {
		return wkproto.ReasonInBlacklist, nil
	}

	// 判断是否是订阅者
	isSubscriber, err := r.s.store.ExistSubscriber(realFakeChannelId, channelType, fromUid)
	if err != nil {
		r.Error("ExistSubscriber error", zap.Error(err))
		return wkproto.ReasonSystemError, err
	}
	if !isSubscriber {
		return wkproto.ReasonSubscriberNotExist, nil
	}

	// 判断是否在白名单内
	if !r.opts.WhitelistOffOfPerson || channelType != wkproto.ChannelTypePerson { // 如果不是个人频道或者个人频道白名单开关打开，则判断是否在白名单内
		hasAllowlist, err := r.s.store.HasAllowlist(realFakeChannelId, channelType)
		if err != nil {
			r.Error("HasAllowlist error", zap.Error(err))
			return wkproto.ReasonSystemError, err
		}

		if hasAllowlist { // 如果频道有白名单，则判断是否在白名单内
			isAllowlist, err := r.s.store.ExistAllowlist(realFakeChannelId, channelType, fromUid)
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
	if r.opts.IsLocalNode(leaderNode.Id) {
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
	if resp.Status == proto.StatusOK {
		return wkproto.ReasonSuccess, nil
	}
	if resp.Status == proto.StatusError {
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
	sub      *channelReactorSub
}

// =================================== 消息存储 ===================================

func (r *channelReactor) addStorageReq(req *storageReq) {

	select {
	case r.processStorageC <- req:
	default:
		r.Warn("addStorageReq channel reactor is full", zap.String("channelId", req.ch.channelId))
		req.sub.step(req.ch, &ChannelAction{
			UniqueNo:   req.ch.uniqueNo,
			ActionType: ChannelActionStorageResp,
			Reason:     ReasonError,
		})
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
					reqs = append(reqs, req)
				default:
					done = true
				}
			}
			r.processStorages(reqs)

			reqs = reqs[:0]
			done = false

		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *channelReactor) processStorages(reqs []*storageReq) {

	for _, req := range reqs {
		err := r.processGoPool.Submit(func(rq *storageReq) func() {
			return func() {
				r.processStorage(rq)
			}
		}(req))
		if err != nil {
			r.Error("processStorage failed, submit error", zap.Error(err))
			req.sub.step(req.ch, &ChannelAction{
				UniqueNo:   req.ch.uniqueNo,
				ActionType: ChannelActionStorageResp,
				Reason:     ReasonError,
			})
		}
	}
}

func (r *channelReactor) processStorage(req *storageReq) {

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

		timeoutCtx, cancel := context.WithTimeout(r.s.ctx, time.Second*10)
		results, err := r.s.store.AppendMessages(timeoutCtx, req.ch.channelId, req.ch.channelType, sotreMessages)
		cancel()
		if err != nil {
			lastMsg := sotreMessages[len(sotreMessages)-1]
			r.Error("AppendMessages error", zap.Error(err), zap.Int("msgCount", len(sotreMessages)), zap.Uint32("lastMsgSeq", lastMsg.MessageSeq), zap.Int64("lastMessageId", lastMsg.MessageID), zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))
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
						if reason == ReasonSuccess {
							msg.ReasonCode = wkproto.ReasonSuccess
						} else {
							msg.ReasonCode = wkproto.ReasonSystemError
						}

						req.messages[i] = msg
						break
					}
				}
			}
		}
	}

	if r.opts.WebhookOn(EventMsgNotify) && reason == ReasonSuccess {
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
			r.Error("AppendMessageOfNotifyQueue error", zap.Error(err), zap.Int("msgCount", len(messages)), zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))
		}
	}
	// 返回存储结果 TODO: 这里一直返回ReasonSuccess，如果存储失败，应该返回ReasonError，会导致死循环
	r.respStoreResult(req, ReasonSuccess)
}

func (r *channelReactor) respStoreResult(req *storageReq, reason Reason) {
	lastIndex := req.messages[len(req.messages)-1].Index
	req.sub.step(req.ch, &ChannelAction{
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
	sub      *channelReactorSub
}

// =================================== 发送回执 ===================================

func (r *channelReactor) addSendackReq(req *sendackReq) {
	select {
	case r.processSendackC <- req:
	default:
		r.Warn("processSendackC is full, ignore", zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))
		req.sub.step(req.ch, &ChannelAction{
			UniqueNo:   req.ch.uniqueNo,
			ActionType: ChannelActionSendackResp,
			Reason:     ReasonError,
		})
	}
}

func (r *channelReactor) processSendackLoop() {
	reqs := make([]*sendackReq, 0)
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
			r.processSendacks(reqs)

			reqs = reqs[:0]
			done = false
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *channelReactor) processSendacks(reqs []*sendackReq) {

	for _, req := range reqs {
		req := req
		err := r.processGoPool.Submit(func() {
			r.processSendack(req)
		})
		if err != nil {
			r.Error("processSendack failed, submit error", zap.Error(err))
			req.sub.step(req.ch, &ChannelAction{
				UniqueNo:   req.ch.uniqueNo,
				ActionType: ChannelActionSendackResp,
				Reason:     ReasonError,
			})
		}
	}

}

func (r *channelReactor) processSendack(req *sendackReq) {
	nodeFowardSendackPacketMap := map[uint64][]*ForwardSendackPacket{}
	for _, msg := range req.messages {

		if r.opts.IsSystemDevice(msg.FromDeviceId) { // 系统发送的消息不需要sendack
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

			err := r.s.userReactor.writePacketByConnId(msg.FromUid, msg.FromConnId, sendack)
			if err != nil {
				r.Error("writePacketByConnId error", zap.Error(err), zap.Uint64("nodeId", msg.FromNodeId), zap.Int64("connId", msg.FromConnId))
			}
		} else { // 连接在其他节点，需要将消息转发出去
			nodeFowardSendackPacketMap[msg.FromNodeId] = append(nodeFowardSendackPacketMap[msg.FromNodeId], &ForwardSendackPacket{
				Uid:      msg.FromUid,
				DeviceId: msg.FromDeviceId,
				ConnId:   msg.FromConnId,
				Sendack:  sendack,
			})
		}
	}

	for nodeId, forwardSendackPackets := range nodeFowardSendackPacketMap {
		err := r.requestForwardSendack(nodeId, forwardSendackPackets)
		if err != nil {
			r.Error("requestForwardSendack error", zap.Error(err), zap.Uint64("nodeId", nodeId))
		}
	}

	lastMsg := req.messages[len(req.messages)-1]
	req.sub.step(req.ch, &ChannelAction{
		UniqueNo:   req.ch.uniqueNo,
		ActionType: ChannelActionSendackResp,
		Index:      lastMsg.Index,
		Reason:     ReasonSuccess,
	})
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
	if resp.Status != proto.StatusOK {
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
	sub      *channelReactorSub
}

// =================================== 消息投递 ===================================

func (r *channelReactor) addDeliverReq(req *deliverReq) {
	select {
	case r.processDeliverC <- req:
	default:
		r.Warn("processDeliverC is full, ignore", zap.String("channelId", req.channelId), zap.Uint8("channelType", req.channelType))
		actionType := ChannelActionDeliverResp
		if req.isStream {
			actionType = ChannelActionStreamDeliverResp
		}
		req.sub.step(req.ch, &ChannelAction{
			UniqueNo:   req.ch.uniqueNo,
			ActionType: actionType,
			Reason:     ReasonError,
		})
	}
}

func (r *channelReactor) processDeliverLoop() {
	reqs := make([]*deliverReq, 0, 100)
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
						if rq.channelId == req.channelId && rq.channelType == req.channelType && rq.isStream == req.isStream {
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
			r.processDelivers(reqs)

			reqs = reqs[:0]
			done = false
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *channelReactor) processDelivers(reqs []*deliverReq) {

	for _, req := range reqs {
		req := req
		err := r.processGoPool.Submit(func() {
			r.processDeliver(req)
		})
		if err != nil {
			r.Error("processDeliver failed, submit error", zap.Error(err))
			actionType := ChannelActionDeliverResp
			if req.isStream {
				actionType = ChannelActionStreamDeliverResp
			}
			req.sub.step(req.ch, &ChannelAction{
				UniqueNo:   req.ch.uniqueNo,
				ActionType: actionType,
				Reason:     ReasonError,
			})
		}
	}
}

func (r *channelReactor) processDeliver(req *deliverReq) {
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

	reason := ReasonSuccess

	actionType := ChannelActionDeliverResp
	if req.isStream {
		actionType = ChannelActionStreamDeliverResp
	}

	req.sub.step(req.ch, &ChannelAction{
		UniqueNo:   req.ch.uniqueNo,
		ActionType: actionType,
		Index:      lastIndex,
		Reason:     reason,
	})
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
	isStream    bool
	sub         *channelReactorSub
}

// =================================== 关闭请求 ===================================

func (r *channelReactor) addCloseReq(req *closeReq) {
	select {
	case r.processCloseC <- req:
	default:
		r.Warn("processCloseC is full, ignore", zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))
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

	r.Debug("channel close", zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))

	if r.opts.IsLocalNode(req.leaderId) {
		trace.GlobalTrace.Metrics.Cluster().ChannelActiveCountAdd(-1)
	}
	// 释放掉tagKey
	receiverTagKey := req.ch.receiverTagKey.Load()
	if receiverTagKey != "" {
		r.s.tagManager.releaseReceiverTagNow(receiverTagKey)
	}

	// 移除频道
	sub := r.reactorSub(req.ch.key)
	sub.removeChannel(req.ch.key)
}

type closeReq struct {
	ch       *channel
	leaderId uint64
}

// =================================== 检查tag的有效性 ===================================

func (r *channelReactor) addCheckTagReq(req *checkTagReq) {
	select {
	case r.processCheckTagC <- req:
	default:
		r.Debug("processCheckTagC is full, ignore", zap.String("channelId", req.ch.channelId), zap.Uint8("channelType", req.ch.channelType))
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
