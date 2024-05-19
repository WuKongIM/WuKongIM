package server

import (
	"context"
	"fmt"
	"time"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

// =================================== ping ===================================

func (r *userReactor) addPingReq(req *pingReq) {
	select {
	case r.processPingC <- req:
	case <-r.stopper.ShouldStop():
		return
	}
}

func (r *userReactor) processPingLoop() {
	for {
		select {
		case req := <-r.processPingC:
			r.processPing(req)
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *userReactor) processPing(req *pingReq) {

	fmt.Println("processPing......")
	conn := r.getConnContextById(req.fromUid, req.fromConnId)
	if conn == nil {
		r.Warn("conn not found", zap.String("uid", req.fromUid), zap.Int64("connID", req.fromConnId))
		return
	}
	r.s.response(conn, &wkproto.PongPacket{}) // 先响应客户端的ping

	// leaderInfo, err := r.s.cluster.SlotLeaderOfChannel(conn.uid, wkproto.ChannelTypePerson) // 获取频道的领导节点
	// if err != nil {
	// 	r.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", conn.uid), zap.Uint8("channelType", wkproto.ChannelTypePerson))
	// 	return
	// }
	// leaderIsSelf := leaderInfo.Id == r.s.opts.Cluster.NodeId
	// if !leaderIsSelf { // 转发ping给领导节点
	// 	r.Debug("processPing to leader....", zap.String("uid", conn.uid), zap.Uint64("leader", leaderInfo.Id))
	// 	status, err := r.s.connPing(leaderInfo.Id, &rpc.ConnPingReq{
	// 		BelongPeerID: r.s.opts.Cluster.NodeId,
	// 		Uid:          conn.uid,
	// 		DeviceId:     conn.deviceId,
	// 	})
	// 	if err != nil {
	// 		r.Error("conn ping err", zap.Error(err))
	// 		return
	// 	}
	// 	if status == proto.Status_NotFound { // 连接不存在
	// 		r.Debug("conn not found on the leader node", zap.String("uid", conn.uid), zap.Int64("connID", conn.id))
	// 		conn.close() // 领导节点不存在此连接，关闭连接
	// 	}
	// }
}

type pingReq struct {
	fromConnId   int64
	fromUid      string
	fromDeviceId string
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

	conn := r.getConnContext(req.fromUid, req.fromDeviceId)
	if conn == nil {
		return
	}
	ack := req.recvack
	persist := !ack.NoPersist
	if persist {
		// 完成消息（移除重试队列里的消息）
		r.Debug("移除重试队列里的消息！", zap.Uint32("messageSeq", ack.MessageSeq), zap.String("uid", conn.uid), zap.Int64("clientID", conn.id), zap.Uint8("deviceFlag", uint8(conn.deviceFlag)), zap.Uint8("deviceLevel", uint8(conn.deviceLevel)), zap.String("deviceID", conn.deviceId), zap.Bool("syncOnce", ack.SyncOnce), zap.Bool("noPersist", ack.NoPersist), zap.Int64("messageID", ack.MessageID))
		err := r.s.retryManager.removeRetry(conn.id, ack.MessageID)
		if err != nil {
			r.Warn("移除重试队列里的消息失败！", zap.Error(err), zap.Uint32("messageSeq", ack.MessageSeq), zap.String("uid", conn.uid), zap.Int64("clientID", conn.id), zap.Uint8("deviceFlag", conn.deviceFlag.ToUint8()), zap.String("deviceID", conn.deviceId), zap.Int64("messageID", ack.MessageID))
		}
	}
	if ack.SyncOnce && persist && wkproto.DeviceLevel(conn.deviceLevel) == wkproto.DeviceLevelMaster { // 写扩散和存储并且是master等级的设备才会更新游标
		r.Debug("更新游标", zap.String("uid", conn.uid), zap.Uint32("messageSeq", ack.MessageSeq))
		err := r.s.store.UpdateMessageOfUserCursorIfNeed(conn.uid, uint64(ack.MessageSeq))
		if err != nil {
			r.Warn("更新游标失败！", zap.Error(err), zap.String("uid", conn.uid), zap.Uint32("messageSeq", ack.MessageSeq))
		}
	}
}

type recvackReq struct {
	fromUid      string
	fromDeviceId string
	recvack      *wkproto.RecvackPacket
}

// =================================== write ===================================

func (r *userReactor) addWriteReq(req *writeReq) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	select {
	case r.processWriteC <- req:
	case <-timeoutCtx.Done():
		r.Error("addWriteReq timeout", zap.String("toUid", req.toUid), zap.String("toDeviceId", req.toDeviceId))
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
						if r.toUid == req.toUid && r.toDeviceId == req.toDeviceId {
							r.data = append(r.data, req.data...)
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
	for _, req := range reqs {
		conn := r.getConnContext(req.toUid, req.toDeviceId)
		if conn == nil || len(req.data) == 0 {
			return
		}
		r.s.responseData(conn.conn, req.data)
	}

}

type writeReq struct {
	toUid      string
	toDeviceId string
	data       []byte
}
