package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/pkg/errors"
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

// =================================== auth ===================================

func (r *userReactor) addAuthReq(req *userAuthReq) {
	select {
	case r.processAuthC <- req:
	case <-r.stopper.ShouldStop():
		return
	}
}

func (r *userReactor) processAuthLoop() {
	for {
		select {
		case req := <-r.processAuthC:
			r.processAuth(req)
		case <-r.stopper.ShouldStop():
			return
		}
	}

}

func (r *userReactor) processAuth(req *userAuthReq) {

	for _, msg := range req.messages {
		_, _ = r.handleAuth(req.uid, msg)
	}
	lastIndex := req.messages[len(req.messages)-1].Index
	r.reactorSub(req.uid).step(req.uid, &UserAction{
		ActionType: UserActionAuthResp,
		Reason:     ReasonSuccess,
		Index:      lastIndex,
	})

}

func (r *userReactor) handleAuth(uid string, msg *ReactorUserMessage) (wkproto.ReasonCode, error) {
	var (
		connectPacket = msg.InPacket.(*wkproto.ConnectPacket)
		devceLevel    wkproto.DeviceLevel
	)
	var connCtx *connContext
	if msg.FromNodeId == r.s.opts.Cluster.NodeId {
		fmt.Println("processAuth------>11")
		connCtx = r.getConnContextById(uid, msg.ConnId)
	} else {
		fmt.Println("processAuth------>22")
		sub := r.reactorSub(uid)
		connInfo := connInfo{
			connId:       r.s.engine.GenClientID(), // 分配一个本地的连接id
			proxyConnId:  msg.ConnId,               // 连接在代理节点的连接id
			uid:          uid,
			deviceId:     connectPacket.DeviceID,
			deviceFlag:   wkproto.DeviceFlag(connectPacket.DeviceFlag),
			protoVersion: connectPacket.Version,
		}
		connCtx = newConnContextProxy(msg.FromNodeId, connInfo, sub)
		sub.addConnContext(connCtx)
	}
	if connCtx == nil {
		r.Error("connCtx is nil", zap.String("uid", uid), zap.Int64("connId", msg.ConnId))
		return wkproto.ReasonSystemError, errors.New("connCtx is nil")
	}
	// -------------------- token verify --------------------
	if connectPacket.UID == r.s.opts.ManagerUID {
		if r.s.opts.ManagerTokenOn && connectPacket.Token != r.s.opts.ManagerToken {
			r.Error("manager token verify fail", zap.String("uid", uid), zap.String("token", connectPacket.Token))
			r.authResponseConnackAuthFail(connCtx)
			return wkproto.ReasonAuthFail, nil
		}
		devceLevel = wkproto.DeviceLevelSlave // 默认都是slave设备
	} else if r.s.opts.TokenAuthOn {
		if connectPacket.Token == "" {
			r.Error("token is empty")
			r.authResponseConnackAuthFail(connCtx)
			return wkproto.ReasonAuthFail, errors.New("token is empty")
		}
		user, err := r.s.store.GetUser(uid, connectPacket.DeviceFlag.ToUint8())
		if err != nil {
			r.Error("get user token err", zap.Error(err))
			r.authResponseConnackAuthFail(connCtx)
			return wkproto.ReasonAuthFail, err

		}
		if user.Token != connectPacket.Token {
			r.Error("token verify fail", zap.String("expectToken", user.Token), zap.String("actToken", connectPacket.Token), zap.Any("conn", connCtx))
			r.authResponseConnackAuthFail(connCtx)
			return wkproto.ReasonAuthFail, errors.New("token verify fail")
		}
		devceLevel = wkproto.DeviceLevel(user.DeviceLevel)
	} else {
		devceLevel = wkproto.DeviceLevelSlave // 默认都是slave设备
	}

	// -------------------- ban  --------------------
	userChannelInfo, err := r.s.store.GetChannel(uid, wkproto.ChannelTypePerson)
	if err != nil {
		r.Error("get user channel info err", zap.Error(err))
		r.authResponseConnackAuthFail(connCtx)
		return wkproto.ReasonAuthFail, err
	}
	ban := false
	if !wkdb.IsEmptyChannelInfo(userChannelInfo) {
		ban = userChannelInfo.Ban
	}
	if ban {
		r.Error("user is ban", zap.String("uid", uid))
		r.authResponseConnack(connCtx, wkproto.ReasonBan)
		return wkproto.ReasonBan, errors.New("user is ban")
	}

	// -------------------- get message encrypt key --------------------
	dhServerPrivKey, dhServerPublicKey := wkutil.GetCurve25519KeypPair() // 生成服务器的DH密钥对
	aesKey, aesIV, err := r.s.getClientAesKeyAndIV(connectPacket.ClientKey, dhServerPrivKey)
	if err != nil {
		r.Error("get client aes key and iv err", zap.Error(err))
		r.authResponseConnackAuthFail(connCtx)
		return wkproto.ReasonAuthFail, err
	}
	dhServerPublicKeyEnc := base64.StdEncoding.EncodeToString(dhServerPublicKey[:])

	// -------------------- same master kicks each other --------------------
	oldConns := r.s.userReactor.getConnContextByDeviceFlag(uid, connectPacket.DeviceFlag)
	if len(oldConns) > 0 {
		if devceLevel == wkproto.DeviceLevelMaster { // 如果设备是master级别，则把旧连接都踢掉
			for _, oldConn := range oldConns {
				if oldConn.connId == connCtx.connId { // 不能把自己踢了
					continue
				}
				fmt.Println("remove----------1")
				r.s.userReactor.removeConnContextById(oldConn.uid, oldConn.connId)
				if oldConn.deviceId != connectPacket.DeviceID {
					r.Info("same master kicks each other", zap.String("devceLevel", devceLevel.String()), zap.String("uid", uid), zap.String("deviceID", connectPacket.DeviceID), zap.String("oldDeviceID", oldConn.deviceId))
					r.s.response(oldConn, &wkproto.DisconnectPacket{
						ReasonCode: wkproto.ReasonConnectKick,
						Reason:     "login in other device",
					})
					r.s.timingWheel.AfterFunc(time.Second*5, func() {
						oldConn.close()
					})
				} else {
					r.s.timingWheel.AfterFunc(time.Second*4, func() {
						oldConn.close() // Close old connection
					})
				}
				r.Debug("close old conn", zap.Any("oldConn", oldConn))
			}
		} else if devceLevel == wkproto.DeviceLevelSlave { // 如果设备是slave级别，则把相同的deviceID踢掉
			for _, oldConn := range oldConns {
				if oldConn.connId != connCtx.connId && oldConn.deviceId == connectPacket.DeviceID {
					fmt.Println("remove----------2", oldConn.connId, connCtx.connId)
					r.s.userReactor.removeConnContextById(oldConn.uid, oldConn.connId)
					r.s.timingWheel.AfterFunc(time.Second*5, func() {
						oldConn.close()
					})
				}
			}
		}

	}

	// -------------------- set conn info --------------------
	timeDiff := time.Now().UnixNano()/1000/1000 - connectPacket.ClientTimestamp

	// connCtx := p.connContextPool.Get().(*connContext)

	lastVersion := connectPacket.Version
	hasServerVersion := false
	if connectPacket.Version > wkproto.LatestVersion {
		lastVersion = wkproto.LatestVersion
	}

	connCtx.aesIV = aesIV
	connCtx.aesKey = aesKey
	connCtx.deviceLevel = devceLevel
	connCtx.protoVersion = lastVersion
	connCtx.isAuth.Store(true)

	if connCtx.isRealConn {
		connCtx.conn.SetMaxIdle(r.s.opts.ConnIdleTime)
	}

	// -------------------- response connack --------------------

	if connectPacket.Version > 3 {
		hasServerVersion = true
	}

	r.Debug("Auth Success", zap.Any("conn", connCtx), zap.Uint8("protoVersion", connectPacket.Version), zap.Bool("hasServerVersion", hasServerVersion))
	connack := &wkproto.ConnackPacket{
		Salt:          aesIV,
		ServerKey:     dhServerPublicKeyEnc,
		ReasonCode:    wkproto.ReasonSuccess,
		TimeDiff:      timeDiff,
		ServerVersion: lastVersion,
		NodeId:        r.s.opts.Cluster.NodeId,
	}
	connack.HasServerVersion = hasServerVersion
	r.authResponse(connCtx, connack)
	// -------------------- user online --------------------
	// 在线webhook
	deviceOnlineCount := r.s.userReactor.getConnContextCountByDeviceFlag(uid, connectPacket.DeviceFlag)
	totalOnlineCount := r.s.userReactor.getConnContextCount(uid)
	r.s.webhook.Online(uid, connectPacket.DeviceFlag, connCtx.connId, deviceOnlineCount, totalOnlineCount)
	if totalOnlineCount <= 1 {
		r.s.trace.Metrics.App().OnlineUserCountAdd(1) // 统计在线用户数
	}
	r.s.trace.Metrics.App().OnlineDeviceCountAdd(1) // 统计在线设备数

	return wkproto.ReasonSuccess, nil
}

func (r *userReactor) authResponse(connCtx *connContext, packet *wkproto.ConnackPacket) {
	if connCtx.isRealConn {
		fmt.Println("authResponse111---->", packet.ReasonCode)
		r.s.response(connCtx, packet)
	} else {
		fmt.Println("authResponse222----2>", packet.ReasonCode, connCtx.protoVersion)
		status, err := r.requestUserAuthResult(connCtx.realNodeId, &UserAuthResult{
			ReasonCode:   packet.ReasonCode,
			Uid:          connCtx.uid,
			DeviceId:     connCtx.deviceId,
			ConnId:       connCtx.proxyConnId,
			ServerKey:    packet.ServerKey,
			AesKey:       connCtx.aesKey,
			AesIV:        connCtx.aesIV,
			DeviceLevel:  connCtx.deviceLevel,
			ProtoVersion: connCtx.protoVersion,
		})
		if err != nil {
			r.Error("requestUserAuthResult error", zap.String("uid", connCtx.uid), zap.String("deviceId", connCtx.deviceId), zap.Error(err))
		}
		if status == proto.Status_NotFound { // 这个代号说明代理服务器不存在此连接了，所以这里也直接移除
			r.Error("requestUserAuthResult not found", zap.String("uid", connCtx.uid), zap.String("deviceId", connCtx.deviceId))
			r.removeConnContextById(connCtx.uid, connCtx.connId)
		}
	}
}

func (r *userReactor) authResponseConnack(connCtx *connContext, reasonCode wkproto.ReasonCode) {

	r.authResponse(connCtx, &wkproto.ConnackPacket{
		ReasonCode: reasonCode,
	})
}

func (r *userReactor) authResponseConnackAuthFail(connCtx *connContext) {
	r.authResponseConnack(connCtx, wkproto.ReasonAuthFail)
}

func (r *userReactor) requestUserAuthResult(nodeId uint64, result *UserAuthResult) (proto.Status, error) {
	data, err := result.Marshal()
	if err != nil {
		return proto.Status_ERROR, err
	}
	timeoutCtx, cancel := context.WithTimeout(r.s.ctx, time.Second*5)
	defer cancel()
	resp, err := r.s.cluster.RequestWithContext(timeoutCtx, nodeId, "/wk/userAuthResult", data)
	if err != nil {
		return proto.Status_ERROR, err
	}

	return resp.Status, nil
}

type userAuthReq struct {
	uid      string
	messages []*ReactorUserMessage
}

// 用户认证结果
type UserAuthResult struct {
	ReasonCode   wkproto.ReasonCode
	Uid          string // 用户id
	DeviceId     string // 设备id
	ConnId       int64  // 代理节点的连接id
	ServerKey    string // 服务器的DH公钥
	AesKey       string
	AesIV        string
	DeviceLevel  wkproto.DeviceLevel
	ProtoVersion uint8
}

func (u *UserAuthResult) Marshal() ([]byte, error) {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteUint8(uint8(u.ReasonCode))
	encoder.WriteString(u.Uid)
	encoder.WriteString(u.DeviceId)
	encoder.WriteInt64(u.ConnId)
	encoder.WriteString(u.ServerKey)
	encoder.WriteString(u.AesKey)
	encoder.WriteString(u.AesIV)
	encoder.WriteUint8(uint8(u.DeviceLevel))
	encoder.WriteUint8(u.ProtoVersion)
	return encoder.Bytes(), nil
}

func (u *UserAuthResult) Unmarshal(data []byte) error {
	decoder := wkproto.NewDecoder(data)
	var reasonCode uint8
	var err error
	if reasonCode, err = decoder.Uint8(); err != nil {
		return err
	}
	u.ReasonCode = wkproto.ReasonCode(reasonCode)

	if u.Uid, err = decoder.String(); err != nil {
		return err
	}
	if u.DeviceId, err = decoder.String(); err != nil {
		return err
	}
	if u.ConnId, err = decoder.Int64(); err != nil {
		return err
	}
	if u.ServerKey, err = decoder.String(); err != nil {
		return err
	}
	if u.AesKey, err = decoder.String(); err != nil {
		return err
	}
	if u.AesIV, err = decoder.String(); err != nil {
		return err
	}
	var deviceLevel uint8
	if deviceLevel, err = decoder.Uint8(); err != nil {
		return err
	}
	u.DeviceLevel = wkproto.DeviceLevel(deviceLevel)

	// protoVersion
	if u.ProtoVersion, err = decoder.Uint8(); err != nil {
		return err
	}
	return nil
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
		conn := r.getConnContextById(req.uid, msg.ConnId)
		if conn == nil {
			r.Debug("conn not found", zap.String("uid", req.uid), zap.Int64("connId", msg.ConnId))
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
		var maxIndex uint64
		for _, msg := range req.messages {
			if msg.Index > maxIndex {
				maxIndex = msg.Index
			}
		}
		r.reactorSub(req.uid).step(req.uid, &UserAction{
			ActionType: UserActionRecvResp,
			Index:      maxIndex,
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

	fmt.Println("handleWrite---->", req.uid)
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
			fmt.Println("isRealConn....")
			connId := deviceIdConnIdMap[deviceId]
			if connId == conn.connId {
				fmt.Println("isRealConn222....", data)
				r.s.responseData(conn.conn, data)
			} else {
				r.Warn("connId not match", zap.String("uid", req.uid), zap.Int64("expectConnId", connId), zap.Int64("actConnId", conn.connId))
			}

		} else { // 是代理连接，转发数据到真实连接
			fmt.Println("fowardWriteReq---->")
			err := r.fowardWriteReq(conn.realNodeId, &FowardWriteReq{
				Uid:      req.uid,
				DeviceId: deviceId,
				ConnId:   conn.proxyConnId,
				Data:     data,
			})
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

// =================================== 转发userAction ===================================

func (r *userReactor) addForwardUserActionReq(action *UserAction) {
	select {
	case r.processForwardUserActionC <- action:
	case <-r.stopper.ShouldStop():
		return
	}
}

func (r *userReactor) processForwardUserActionLoop() {
	actions := make([]*UserAction, 0, 100)
	done := false
	for {
		select {
		case req := <-r.processForwardUserActionC:
			actions = append(actions, req)
			for !done {
				select {
				case req := <-r.processForwardUserActionC:
					actions = append(actions, req)
				default:
					done = true
				}
			}

			r.processForwardUserAction(actions)
			done = false
			actions = actions[:0]
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *userReactor) processForwardUserAction(actions []*UserAction) {
	fmt.Println("processForwardUserAction--->", len(actions))
	userForwardActionMap := map[string][]*UserAction{} // 用户对应的action
	userLeaderMap := map[string]uint64{}               // 用户对应的领导节点
	// 按照用户分组
	for _, action := range actions {
		forwardActions := userForwardActionMap[action.Uid]
		forwardActions = append(forwardActions, action.Forward)
		userForwardActionMap[action.Uid] = forwardActions
		userLeaderMap[action.Uid] = action.LeaderId
	}

	var reason Reason
	for uid, fowardActions := range userForwardActionMap {
		leaderId := userLeaderMap[uid]
		reason = ReasonSuccess
		newLeaderId, err := r.handleForwardUserAction(uid, leaderId, fowardActions)
		if err != nil {
			r.Error("handleForwardUserAction error", zap.Error(err))
			reason = ReasonError
		}
		sub := r.reactorSub(uid)
		if newLeaderId > 0 {
			sub.step(uid, &UserAction{
				ActionType: UserActionLeaderChange,
				LeaderId:   newLeaderId,
			})
		}
		messages := make([]*ReactorUserMessage, 0)
		for _, action := range fowardActions {
			messages = append(messages, action.Messages...)
		}
		sub.step(uid, &UserAction{
			ActionType: UserActionForwardResp,
			Uid:        uid,
			Reason:     reason,
			Messages:   messages,
		})

	}

}

func (r *userReactor) handleForwardUserAction(uid string, leaderId uint64, actions []*UserAction) (uint64, error) {
	needChangeLeader, err := r.forwardUserAction(leaderId, actions)
	if err != nil {
		return 0, err
	}
	if needChangeLeader {

		// 重新获取频道领导
		newLeaderId, err := r.s.cluster.SlotLeaderIdOfChannel(uid, wkproto.ChannelTypePerson)
		if err != nil {
			r.Error("handleForwardUserAction: SlotLeaderIdOfChannel error", zap.Error(err))
			return 0, err
		}
		return newLeaderId, errors.New("leader change")
	}
	return 0, nil
}

func (r *userReactor) forwardUserAction(nodeId uint64, actions []*UserAction) (bool, error) {
	fmt.Println("forwardUserAction---->", nodeId, actions)
	timeoutCtx, cancel := context.WithTimeout(r.s.ctx, time.Second*5)
	defer cancel()

	for _, action := range actions {
		fmt.Println("action---->", action.ActionType.String())
	}
	actionSet := UserActionSet(actions)

	data, err := actionSet.Marshal()
	if err != nil {
		return false, err
	}
	resp, err := r.s.cluster.RequestWithContext(timeoutCtx, nodeId, "/wk/userAction", data)
	if err != nil {
		return false, err
	}

	if resp.Status == proto.Status(errCodeNotIsUserLeader) {
		return true, nil
	}
	if resp.Status != proto.Status_OK {
		return false, fmt.Errorf("forwardUserAction failed, status=%d", resp.Status)
	}
	return false, nil
}
