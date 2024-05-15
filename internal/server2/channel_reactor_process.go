package server

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

// =================================== 发送权限判断 ===================================
func (r *channelReactor) addPermissionReq(req *permissionReq) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	select {
	case r.processPermissionC <- req:
	case <-timeoutCtx.Done():
		r.Error("addPermissionReq timeout", zap.String("fromUid", req.fromUid), zap.String("channelId", req.ch.channelId))
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

	fmt.Println("权限判断", req.fromUid, req.ch.channelId)
	// 权限判断

	// 返回结果
	sub := r.reactorSub(req.ch.key)
	sub.step(req.ch, &ChannelAction{ActionType: ChannelActionPermissionResp, Messages: req.messages, Reason: ReasonSuccess})
}

type permissionReq struct {
	fromUid  string
	ch       *channel
	messages []*ReactorChannelMessage
}

// =================================== 消息存储 ===================================

func (r *channelReactor) addStorageReq(req *storageReq) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	select {
	case r.processStorageC <- req:
	case <-timeoutCtx.Done():
		r.Error("addStorageReq timeout", zap.String("channelId", req.ch.channelId))
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
	var gerr error
	for _, req := range reqs {
		sotreMessages := make([]wkdb.Message, 0, 1024)
		for _, msg := range req.messages {
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
		// fmt.Println("sotreMessages---->", len(sotreMessages))
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
		if gerr != nil {
			reason = ReasonError
		} else {
			reason = ReasonSuccess

		}
		sub := r.reactorSub(req.ch.key)
		sub.step(req.ch, &ChannelAction{ActionType: ChannelActionStorageResp, Messages: req.messages, Reason: reason})

	}

}

type storageReq struct {
	ch       *channel
	messages []*ReactorChannelMessage
}

// =================================== 消息投递 ===================================

func (r *channelReactor) addDeliverReq(req *deliverReq) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	select {
	case r.processDeliverC <- req:
	case <-timeoutCtx.Done():
		r.Error("addDeliverReq timeout", zap.String("channelId", req.ch.channelId))
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
					reqs = append(reqs, req)
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

	// 消息投递

	// 返回结果
	for _, req := range reqs {
		sub := r.reactorSub(req.ch.key)
		sub.step(req.ch, &ChannelAction{ActionType: ChannelActionDeliverResp, Messages: req.messages, Reason: ReasonSuccess})
	}
}

type deliverReq struct {
	ch       *channel
	messages []*ReactorChannelMessage
}

// =================================== 发送回执 ===================================

func (r *channelReactor) addSendackReq(req *sendackReq) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	select {
	case r.processSendackC <- req:
	case <-timeoutCtx.Done():
		r.Error("addSendackReq timeout", zap.String("channelId", req.ch.channelId))
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

	// 发送回执
	// 返回结果
	var err error
	for _, req := range reqs {
		var reasonCode wkproto.ReasonCode
		if req.reason == ReasonSuccess {
			reasonCode = wkproto.ReasonSuccess
		} else {
			reasonCode = wkproto.ReasonSystemError
		}
		for _, msg := range req.messages {
			err = r.s.userReactor.writePacketByDeviceId(msg.FromUid, msg.FromDeviceId, &wkproto.SendackPacket{
				Framer:      msg.SendPacket.Framer,
				MessageID:   msg.MessageId,
				MessageSeq:  msg.MessageSeq,
				ClientSeq:   msg.SendPacket.ClientSeq,
				ClientMsgNo: msg.SendPacket.ClientMsgNo,
				ReasonCode:  reasonCode,
			})
			if err != nil {
				r.Error("writePacketByDeviceId error", zap.Error(err))
			}
		}

		// sub := r.reactorSub(req.ch.key)
		// sub.step(req.ch, &ChannelAction{ActionType: ChannelActionSendackResp, Messages: req.messages})
	}
}

type sendackReq struct {
	ch       *channel
	reason   Reason
	messages []*ReactorChannelMessage
}
