package server

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

// =================================== init ===================================

func (r *userReactor) addInitReq(req *userInitReq) {
	select {
	case r.processInitC <- req:
	case <-r.stopper.ShouldStop():
		return
	}
}

func (r *userReactor) processInitLoop() {
	for {
		select {
		case req := <-r.processInitC:
			r.processInit(req)
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *userReactor) processInit(req *userInitReq) {
	leaderId, err := r.s.cluster.SlotLeaderIdOfChannel(req.uid, wkproto.ChannelTypePerson)

	var reason = ReasonSuccess
	if err != nil {
		reason = ReasonError
		r.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.uid), zap.Uint8("channelType", wkproto.ChannelTypePerson))
	}

	r.reactorSub(req.uid).step(req.uid, &UserAction{
		ActionType: UserActionInitResp,
		LeaderId:   leaderId,
		Reason:     reason,
	})
}

type userInitReq struct {
	uid string
}

// =================================== ping ===================================

func (r *userReactor) addPingReq(req *pingReq) {
	select {
	case r.processPingC <- req:
	case <-r.stopper.ShouldStop():
		return
	}
}

func (r *userReactor) processPingLoop() {
	reqs := make([]*pingReq, 0, 100)
	done := false
	for {
		select {
		case req := <-r.processPingC:
			reqs = append(reqs, req)
			for !done {
				select {
				case req := <-r.processPingC:
					exist := false
					for _, r := range reqs {
						if r.uid == req.uid {
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
			r.processPing(reqs)
			done = false
			reqs = reqs[:0]
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *userReactor) processPing(reqs []*pingReq) {
	fmt.Println("processPing......")
	for _, req := range reqs {
		if len(req.messages) == 0 {
			continue
		}
		var reason = ReasonSuccess
		err := r.handlePing(req)
		if err != nil {
			r.Error("handlePing err", zap.Error(err))
			reason = ReasonError
		}

		lastMsg := req.messages[len(req.messages)-1]
		r.reactorSub(req.uid).step(req.uid, &UserAction{
			ActionType: UserActionPingResp,
			Reason:     reason,
			Index:      lastMsg.Index,
		})

	}
}

func (r *userReactor) handlePing(req *pingReq) error {
	for _, msg := range req.messages {
		conn := r.getConnContextById(msg.Uid, msg.ConnId)
		if conn == nil {
			r.Debug("conn not found", zap.String("uid", msg.Uid), zap.Int64("connId", msg.ConnId))
			continue
		}
		if !conn.isRealConn { // 不是真实连接可以忽略
			continue
		}
		err := r.s.userReactor.writePacket(conn, &wkproto.PongPacket{})
		if err != nil {
			r.Error("write pong packet error", zap.String("uid", req.uid), zap.Error(err))
		}
	}

	return nil
}

type pingReq struct {
	uid      string
	messages []*ReactorUserMessage
}

// =================================== recvack ===================================

func (r *userReactor) addRecvackReq(req *recvackReq) {
	select {
	case r.processRecvackC <- req:
	case <-r.stopper.ShouldStop():
		return
	}
}

func (r *userReactor) processRecvackLoop() {
	for {
		select {
		case req := <-r.processRecvackC:
			r.processRecvack(req)
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *userReactor) processRecvack(req *recvackReq) {

	// conn := r.getConnContext(req.fromUid, req.fromDeviceId)
	// if conn == nil {
	// 	return
	// }
	// ack := req.recvack
	// persist := !ack.NoPersist
	// if persist {
	// 	// 完成消息（移除重试队列里的消息）
	// 	r.Debug("移除重试队列里的消息！", zap.Uint32("messageSeq", ack.MessageSeq), zap.String("uid", conn.uid), zap.Int64("clientID", conn.id), zap.Uint8("deviceFlag", uint8(conn.deviceFlag)), zap.Uint8("deviceLevel", uint8(conn.deviceLevel)), zap.String("deviceID", conn.deviceId), zap.Bool("syncOnce", ack.SyncOnce), zap.Bool("noPersist", ack.NoPersist), zap.Int64("messageID", ack.MessageID))
	// 	err := r.s.retryManager.removeRetry(conn.id, ack.MessageID)
	// 	if err != nil {
	// 		r.Warn("移除重试队列里的消息失败！", zap.Error(err), zap.Uint32("messageSeq", ack.MessageSeq), zap.String("uid", conn.uid), zap.Int64("clientID", conn.id), zap.Uint8("deviceFlag", conn.deviceFlag.ToUint8()), zap.String("deviceID", conn.deviceId), zap.Int64("messageID", ack.MessageID))
	// 	}
	// }
	// if ack.SyncOnce && persist && wkproto.DeviceLevel(conn.deviceLevel) == wkproto.DeviceLevelMaster { // 写扩散和存储并且是master等级的设备才会更新游标
	// 	r.Debug("更新游标", zap.String("uid", conn.uid), zap.Uint32("messageSeq", ack.MessageSeq))
	// 	err := r.s.store.UpdateMessageOfUserCursorIfNeed(conn.uid, uint64(ack.MessageSeq))
	// 	if err != nil {
	// 		r.Warn("更新游标失败！", zap.Error(err), zap.String("uid", conn.uid), zap.Uint32("messageSeq", ack.MessageSeq))
	// 	}
	// }
}

type recvackReq struct {
	uid      string
	messages []*ReactorUserMessage
}

// =================================== write ===================================

func (r *userReactor) addWriteReq(req *writeReq) {
	select {
	case r.processWriteC <- req:
	case <-r.stopper.ShouldStop():
		return
	}
}

func (r *userReactor) processWriteLoop() {
	reqs := make([]*writeReq, 0, 100)
	done := false
	for {
		select {
		case req := <-r.processWriteC:
			reqs = append(reqs, req)
			for !done {
				select {
				case req := <-r.processWriteC:
					exist := false
					for _, r := range reqs {
						if r.uid == req.uid {
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
			r.processWrite(reqs)
			done = false
			reqs = reqs[:0]
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *userReactor) processWrite(reqs []*writeReq) {
	var reason Reason
	for _, req := range reqs {
		reason = ReasonSuccess
		err := r.handleWrite(req)
		if err != nil {
			r.Warn("handleWrite err", zap.Error(err))
			reason = ReasonError
		}
		lastMsg := req.messages[len(req.messages)-1]
		r.reactorSub(req.uid).step(req.uid, &UserAction{
			ActionType: UserActionRecvResp,
			Index:      lastMsg.Index,
			Reason:     reason,
		})
		// conn := r.getConnContext(req.toUid, req.toDeviceId)
		// if conn == nil || len(req.data) == 0 {
		// 	return
		// }
		// r.s.responseData(conn.conn, req.data)
	}

}

func (r *userReactor) handleWrite(req *writeReq) error {
	sub := r.reactorSub(req.uid)

	var connDataMap = map[string][]byte{}
	var deviceIdConnIdMap = map[string]int64{} // 设备id对应的连接id
	for _, msg := range req.messages {
		deviceIdConnIdMap[msg.DeviceId] = msg.ConnId
		data := connDataMap[msg.DeviceId]
		data = append(data, msg.OutBytes...)
		connDataMap[msg.DeviceId] = data
	}

	for deviceId, data := range connDataMap {
		conn := sub.getConnContext(req.uid, deviceId)
		if conn == nil {
			r.Debug("handleWrite: conn not found", zap.Int("dataLen", len(data)), zap.String("uid", req.uid), zap.String("deviceId", deviceId))
			continue
		}

		if conn.isRealConn { // 是真实节点直接返回数据
			connId := deviceIdConnIdMap[deviceId]
			if connId == conn.id {
				r.s.responseData(conn.conn, data)
			} else {
				r.Warn("connId not match", zap.String("uid", req.uid), zap.Int64("expectConnId", connId), zap.Int64("actConnId", conn.id))
			}

		} else { // 是代理连接，转发数据到真实连接
			err := r.fowardWriteReq(conn.realNodeId, &FowardWriteReq{})
			if err != nil {
				r.Error("fowardWriteReq error", zap.Error(err))
				return err
			}
		}
	}
	return nil
}

// 转发写请求
func (r *userReactor) fowardWriteReq(nodeId uint64, req *FowardWriteReq) error {
	timeoutCtx, cancel := context.WithTimeout(r.s.ctx, time.Second*5)
	defer cancel()
	data, err := req.Marshal()
	if err != nil {
		return err
	}
	resp, err := r.s.cluster.RequestWithContext(timeoutCtx, nodeId, "/wk/connWrite", data)
	if err != nil {
		return err
	}
	if resp.Status != proto.Status_OK {
		return fmt.Errorf("fowardWriteReq failed, status=%d", resp.Status)
	}
	return nil
}

type writeReq struct {
	uid      string
	messages []*ReactorUserMessage
}
