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
	"golang.org/x/sync/errgroup"
)

// =================================== init ===================================

func (r *userReactor) addInitReq(req *userInitReq) {
	select {
	case r.processInitC <- req:
	default:
		r.Warn("addInitReq: processInitC is full, ignore ", zap.String("uid", req.uid))
		req.sub.step(req.uid, UserAction{
			UniqueNo:   req.uniqueNo,
			ActionType: UserActionInitResp,
			Reason:     ReasonError,
		})
	}
}

func (r *userReactor) processInitLoop() {
	for !r.stopped.Load() {
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
		r.Error("processInit: 获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.uid), zap.Uint8("channelType", wkproto.ChannelTypePerson))
	}
	isLeader := leaderId == r.s.opts.Cluster.NodeId

	if isLeader {
		r.s.trace.Metrics.App().OnlineUserCountAdd(1)
	}

	req.sub.step(req.uid, UserAction{
		UniqueNo:   req.uniqueNo,
		ActionType: UserActionInitResp,
		LeaderId:   leaderId,
		Reason:     reason,
	})
}

type userInitReq struct {
	uniqueNo string
	uid      string
	sub      *userReactorSub
}

// =================================== auth ===================================

func (r *userReactor) addAuthReq(req *userAuthReq) {
	select {
	case r.processAuthC <- req:
	default:
		r.Warn("addAuthReq: processAuthC is full, ignore ", zap.String("uid", req.uid), zap.Int("msgCount", len(req.messages)))
		req.sub.step(req.uid, UserAction{
			UniqueNo:   req.uniqueNo,
			ActionType: UserActionAuthResp,
			Reason:     ReasonError,
		})
	}
}

func (r *userReactor) processAuthLoop() {
	reqs := make([]*userAuthReq, 0, 100)
	done := false
	for !r.stopped.Load() {
		select {
		case req := <-r.processAuthC:
			reqs = append(reqs, req)
			for !done {
				select {
				case rq := <-r.processAuthC:
					reqs = append(reqs, rq)
				default:
					done = true
				}
			}
			r.processAuths(reqs)
			reqs = reqs[:0]
			done = false
		case <-r.stopper.ShouldStop():
			return
		}
	}

}

func (r *userReactor) processAuths(reqs []*userAuthReq) {

	timeoutCtx, cancel := context.WithTimeout(r.s.ctx, time.Second*10)
	defer cancel()
	errgroup, _ := errgroup.WithContext(timeoutCtx)
	errgroup.SetLimit(200)
	for _, req := range reqs {
		req := req
		errgroup.Go(func() error {
			r.processAuth(req)
			return nil
		})
	}
	_ = errgroup.Wait()
}

func (r *userReactor) processAuth(req *userAuthReq) {

	for _, msg := range req.messages {
		r.Debug("processAuth", zap.String("uid", req.uid), zap.Int64("connId", msg.ConnId))
		_, _ = r.handleAuth(req.uid, msg)
	}
	lastIndex := req.messages[len(req.messages)-1].Index
	req.sub.step(req.uid, UserAction{
		UniqueNo:   req.uniqueNo,
		ActionType: UserActionAuthResp,
		Reason:     ReasonSuccess,
		Index:      lastIndex,
	})

}

func (r *userReactor) handleAuth(uid string, msg ReactorUserMessage) (wkproto.ReasonCode, error) {
	var (
		connectPacket = msg.InPacket.(*wkproto.ConnectPacket)
		devceLevel    wkproto.DeviceLevel
		isLocalConn   = msg.FromNodeId == r.s.opts.Cluster.NodeId // 是否是本地连接
	)
	var connCtx *connContext
	if isLocalConn { // 本地连接
		connCtx = r.getConnById(uid, msg.ConnId)
		if connCtx == nil {
			r.Error("connCtx is nil", zap.String("uid", uid), zap.Int64("connId", msg.ConnId))
			return wkproto.ReasonSystemError, errors.New("connCtx is nil")
		}
	} else {
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
		sub.addConnAndCreateUserHandlerIfNotExist(connCtx)
		r.Debug("auth: add conn", zap.Any("connCtx", connCtx))
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
		device, err := r.s.store.GetDevice(uid, connectPacket.DeviceFlag)
		if err != nil {
			r.Error("get device token err", zap.Error(err))
			r.authResponseConnackAuthFail(connCtx)
			return wkproto.ReasonAuthFail, err

		}
		if device.Token != connectPacket.Token {
			r.Error("token verify fail", zap.String("expectToken", device.Token), zap.String("actToken", connectPacket.Token), zap.Any("conn", connCtx))
			r.authResponseConnackAuthFail(connCtx)
			return wkproto.ReasonAuthFail, errors.New("token verify fail")
		}
		devceLevel = wkproto.DeviceLevel(device.DeviceLevel)
	} else {
		devceLevel = wkproto.DeviceLevelSlave // 默认都是slave设备
	}

	// -------------------- ban  --------------------
	userChannelInfo, err := r.s.store.GetChannel(uid, wkproto.ChannelTypePerson)
	if err != nil {
		r.Error("get device channel info err", zap.Error(err))
		r.authResponseConnackAuthFail(connCtx)
		return wkproto.ReasonAuthFail, err
	}
	ban := false
	if !wkdb.IsEmptyChannelInfo(userChannelInfo) {
		ban = userChannelInfo.Ban
	}
	if ban {
		r.Error("device is ban", zap.String("uid", uid))
		r.authResponseConnack(connCtx, wkproto.ReasonBan)
		return wkproto.ReasonBan, errors.New("device is ban")
	}

	// -------------------- get message encrypt key --------------------
	dhServerPrivKey, dhServerPublicKey := wkutil.GetCurve25519KeypPair() // 生成服务器的DH密钥对
	aesKey, aesIV, err := r.getClientAesKeyAndIV(connectPacket.ClientKey, dhServerPrivKey)
	if err != nil {
		r.Error("get client aes key and iv err", zap.Error(err))
		r.authResponseConnackAuthFail(connCtx)
		return wkproto.ReasonAuthFail, err
	}
	dhServerPublicKeyEnc := base64.StdEncoding.EncodeToString(dhServerPublicKey[:])

	// -------------------- same master kicks each other --------------------
	oldConns := r.s.userReactor.getConnsByDeviceFlag(uid, connectPacket.DeviceFlag)
	if len(oldConns) > 0 {
		if devceLevel == wkproto.DeviceLevelMaster { // 如果设备是master级别，则把旧连接都踢掉
			for _, oldConn := range oldConns {
				if oldConn.connId == connCtx.connId { // 不能把自己踢了
					continue
				}
				r.s.userReactor.removeConnById(oldConn.uid, oldConn.connId)
				if oldConn.deviceId != connectPacket.DeviceID {
					r.Info("auth: same master kicks each other", zap.String("devceLevel", devceLevel.String()), zap.String("uid", uid), zap.String("deviceID", connectPacket.DeviceID), zap.String("oldDeviceID", oldConn.deviceId))

					_ = oldConn.writeDirectlyPacket(&wkproto.DisconnectPacket{
						ReasonCode: wkproto.ReasonConnectKick,
						Reason:     "login in other device",
					})
					r.s.timingWheel.AfterFunc(time.Second*5, func(cn *connContext) func() {
						return func() {
							cn.close()
						}
					}(oldConn))
				} else {
					r.s.timingWheel.AfterFunc(time.Second*4, func(cn *connContext) func() {
						return func() {
							cn.close() // Close old connection
						}
					}(oldConn))
				}
				r.Info("auth: close old conn for master", zap.Any("oldConn", oldConn))
			}
		} else if devceLevel == wkproto.DeviceLevelSlave { // 如果设备是slave级别，则把相同的deviceID踢掉
			for _, oldConn := range oldConns {
				if oldConn.connId != connCtx.connId && oldConn.deviceId == connectPacket.DeviceID {
					r.s.userReactor.removeConnById(oldConn.uid, oldConn.connId)
					r.s.timingWheel.AfterFunc(time.Second*5, func(cn *connContext) func() {
						return func() {
							cn.close() // Close old connection
						}
					}(oldConn))
					r.Info("auth: close old conn for slave", zap.Any("oldConn", oldConn))
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

	r.Debug("auth: auth Success", zap.Any("conn", connCtx), zap.Uint8("protoVersion", connectPacket.Version), zap.Bool("hasServerVersion", hasServerVersion))
	connack := &wkproto.ConnackPacket{
		Salt:          string(aesIV),
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
	deviceOnlineCount := r.s.userReactor.getConnCountByDeviceFlag(uid, connectPacket.DeviceFlag)
	totalOnlineCount := r.s.userReactor.getConnCount(uid)
	r.s.webhook.Online(uid, connectPacket.DeviceFlag, connCtx.connId, deviceOnlineCount, totalOnlineCount)

	return wkproto.ReasonSuccess, nil
}

// 获取客户端的aesKey和aesIV
// dhServerPrivKey  服务端私钥
func (r *userReactor) getClientAesKeyAndIV(clientKey string, dhServerPrivKey [32]byte) ([]byte, []byte, error) {

	clientKeyBytes, err := base64.StdEncoding.DecodeString(clientKey)
	if err != nil {
		return nil, nil, err
	}

	var dhClientPubKeyArray [32]byte
	copy(dhClientPubKeyArray[:], clientKeyBytes[:32])

	// 获得DH的共享key
	shareKey := wkutil.GetCurve25519Key(dhServerPrivKey, dhClientPubKeyArray) // 共享key

	aesIV := wkutil.GetRandomString(16)
	aesKey := wkutil.MD5(base64.StdEncoding.EncodeToString(shareKey[:]))[:16]
	return []byte(aesKey), []byte(aesIV), nil
}

func (r *userReactor) authResponse(connCtx *connContext, packet *wkproto.ConnackPacket) {
	if connCtx.isRealConn {
		_ = connCtx.writeDirectlyPacket(packet)
	} else {
		status, err := r.requestUserAuthResult(connCtx.realNodeId, &UserAuthResult{
			ReasonCode:   packet.ReasonCode,
			Uid:          connCtx.uid,
			DeviceId:     connCtx.deviceId,
			ConnId:       connCtx.proxyConnId,
			ServerKey:    packet.ServerKey,
			AesKey:       string(connCtx.aesKey),
			AesIV:        string(connCtx.aesIV),
			DeviceLevel:  connCtx.deviceLevel,
			ProtoVersion: connCtx.protoVersion,
		})
		if err != nil {
			r.Error("requestUserAuthResult error", zap.String("uid", connCtx.uid), zap.String("deviceId", connCtx.deviceId), zap.Error(err))
		}
		if status == proto.StatusNotFound { // 这个代号说明代理服务器不存在此连接了，所以这里也直接移除
			r.Error("requestUserAuthResult not found", zap.String("uid", connCtx.uid), zap.String("deviceId", connCtx.deviceId))
			r.removeConnById(connCtx.uid, connCtx.connId)
			connCtx.close()
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
		return proto.StatusError, err
	}
	timeoutCtx, cancel := context.WithTimeout(r.s.ctx, time.Second*5)
	defer cancel()
	resp, err := r.s.cluster.RequestWithContext(timeoutCtx, nodeId, "/wk/userAuthResult", data)
	if err != nil {
		return proto.StatusError, err
	}

	return resp.Status, nil
}

type userAuthReq struct {
	uniqueNo string
	uid      string
	messages []ReactorUserMessage
	sub      *userReactorSub
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
	default:
		r.Warn("addPingReq: processPingC is full, ignore ", zap.String("uid", req.uid), zap.Int("msgCount", len(req.messages)))
		req.sub.step(req.uid, UserAction{
			UniqueNo:   req.uniqueNo,
			ActionType: UserActionPingResp,
			Reason:     ReasonError,
		})
	}
}

func (r *userReactor) processPingLoop() {
	reqs := make([]*pingReq, 0, 100)
	done := false
	for !r.stopped.Load() {
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
	for _, req := range reqs {
		r.handlePing(req)
	}
}

func (r *userReactor) handlePing(req *pingReq) {
	for _, msg := range req.messages {
		conn := r.getConnById(req.uid, msg.ConnId)
		if conn != nil && conn.isRealConn { // 不是真实连接可以忽略
			err := r.s.userReactor.writePacket(conn, &wkproto.PongPacket{})
			if err != nil {
				r.Error("write pong packet error", zap.String("uid", req.uid), zap.Error(err))
			}
		}
		lastMsg := req.messages[len(req.messages)-1]
		req.sub.step(req.uid, UserAction{
			UniqueNo:   req.uniqueNo,
			ActionType: UserActionPingResp,
			Reason:     ReasonSuccess,
			Index:      lastMsg.Index,
		})
	}
}

type pingReq struct {
	uniqueNo string
	uid      string
	messages []ReactorUserMessage
	sub      *userReactorSub
}

// =================================== recvack ===================================

func (r *userReactor) addRecvackReq(req *recvackReq) {
	select {
	case r.processRecvackC <- req:
	default:
		r.Warn("addRecvackReq: processRecvackC is full, ignore ", zap.String("uid", req.uid), zap.Int("msgCount", len(req.messages)))
		req.sub.step(req.uid, UserAction{
			UniqueNo:   req.uniqueNo,
			ActionType: UserActionRecvackResp,
			Reason:     ReasonError,
		})
	}
}

func (r *userReactor) processRecvackLoop() {
	const batchSize = 4096
	reqs := make([]*recvackReq, 0, batchSize)
	done := false
	for !r.stopped.Load() {
		select {
		case req := <-r.processRecvackC:
			reqs = append(reqs, req)

			for !done {
				select {
				case req := <-r.processRecvackC:
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
					if len(reqs) >= batchSize {
						done = true
					}
				default:
					done = true
				}
			}
			r.processRecvacks(reqs)
			done = false
			reqs = reqs[:0]
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *userReactor) processRecvacks(reqs []*recvackReq) {

	timeoutCtx, cancel := context.WithTimeout(r.s.ctx, time.Second*5)
	defer cancel()
	g, _ := errgroup.WithContext(timeoutCtx)
	g.SetLimit(1000)
	for _, req := range reqs {
		req := req
		g.Go(func() error {
			r.processRecvack(req)
			return nil
		})

	}
	_ = g.Wait()
}

func (r *userReactor) processRecvack(req *recvackReq) {
	for _, msg := range req.messages {
		recvackPacket := msg.InPacket.(*wkproto.RecvackPacket)
		if r.s.opts.Logger.TraceOn {
			r.Trace("消息回执", "processRecvack", zap.Int64("messageId", recvackPacket.MessageID), zap.Uint32("messageSeq", recvackPacket.MessageSeq), zap.String("uid", req.uid), zap.String("deviceId", msg.DeviceId), zap.Int64("connId", msg.ConnId))
		}

		persist := !recvackPacket.NoPersist                      // 是否需要持久化
		conn := r.s.userReactor.getConnById(req.uid, msg.ConnId) // 获取当前连接
		if conn != nil {
			isCmd := recvackPacket.SyncOnce                           // 是命令消息
			isMaster := conn.deviceLevel == wkproto.DeviceLevelMaster // 是master设备，只有master设备才能擦除指令消息
			if isCmd && persist && isMaster {
				currMsg := r.s.retryManager.retryMessage(msg.ConnId, recvackPacket.MessageID)
				if currMsg != nil {
					// 删除最近会话的缓存
					r.s.conversationManager.DeleteUserConversationFromCache(req.uid, currMsg.channelId, currMsg.channelType)
					if req.uid != r.s.opts.SystemUID {
						// 更新最近会话的已读位置
						err := r.s.store.DB().UpdateConversationIfSeqGreaterAsync(req.uid, currMsg.channelId, currMsg.channelType, uint64(recvackPacket.MessageSeq))
						if err != nil {
							r.Error("UpdateConversationIfSeqGreaterAsync failed", zap.Error(err), zap.String("uid", req.uid), zap.String("channelId", currMsg.channelId), zap.Uint8("channelType", currMsg.channelType), zap.Uint64("messageSeq", uint64(recvackPacket.MessageSeq)))
						}
					}
				}
			}
		} else {
			r.Debug("processRecvack: conn not found", zap.String("uid", req.uid), zap.Int64("connId", msg.ConnId))
		}

		if persist { // 只有需要持久化的消息才会重试
			// r.Debug("remove retry", zap.String("uid", req.uid), zap.Int64("connId", msg.ConnId), zap.Int64("messageID", recvackPacket.MessageID))
			err := r.s.retryManager.removeRetry(msg.ConnId, recvackPacket.MessageID)
			if err != nil {
				r.Warn("removeRetry error", zap.Error(err), zap.String("uid", req.uid), zap.String("deviceId", msg.DeviceId), zap.Int64("connId", msg.ConnId), zap.Int64("messageID", recvackPacket.MessageID))
			}
		}

	}
	lastMsg := req.messages[len(req.messages)-1]
	req.sub.step(req.uid, UserAction{
		UniqueNo:   req.uniqueNo,
		ActionType: UserActionRecvackResp,
		Index:      lastMsg.Index,
		Reason:     ReasonSuccess,
	})
}

type recvackReq struct {
	uniqueNo string
	uid      string
	messages []ReactorUserMessage
	sub      *userReactorSub
}

// =================================== write ===================================

func (r *userReactor) addWriteReq(req *writeReq) {
	select {
	case r.processWriteC <- req:
	default:
		r.Warn("addWriteReq: processWriteC is full, ignore ", zap.String("uid", req.uid), zap.Int("msgCount", len(req.messages)))
		req.sub.step(req.uid, UserAction{
			UniqueNo:   req.uniqueNo,
			ActionType: UserActionRecvResp,
			Reason:     ReasonError,
		})
	}
}

func (r *userReactor) processWriteLoop() {
	const batchSize = 2048
	reqs := make([]*writeReq, 0, batchSize)
	done := false
	for !r.stopped.Load() {
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
					if len(reqs) >= batchSize {
						done = true
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

	timeoutCtx, cancel := context.WithTimeout(r.s.ctx, time.Second*5)
	defer cancel()
	g, _ := errgroup.WithContext(timeoutCtx)
	g.SetLimit(1000)

	for _, req := range reqs {
		req := req
		g.Go(func() error {
			err := r.handleWrite(req)
			if err != nil {
				r.Warn("handleWrite err", zap.Error(err))
			}
			var maxIndex uint64
			for _, msg := range req.messages {
				if msg.Index > maxIndex {
					maxIndex = msg.Index
				}
			}
			req.sub.step(req.uid, UserAction{
				UniqueNo:   req.uniqueNo,
				ActionType: UserActionRecvResp,
				Index:      maxIndex,
				Reason:     ReasonSuccess,
			})
			return nil
		})
	}

	_ = g.Wait()

}

func (r *userReactor) handleWrite(req *writeReq) error {

	forwardWriteReqMap := map[uint64]FowardWriteReqSlice{} // 按照节点分组
	sub := req.sub
	for _, msg := range req.messages {
		conns := sub.getConnsByDeviceId(req.uid, msg.DeviceId)
		if len(conns) == 0 {
			r.Debug("handleWrite: conn not found", zap.Int("dataLen", len(msg.OutBytes)), zap.String("uid", req.uid), zap.String("deviceId", msg.DeviceId))
			continue
		}
		for _, conn := range conns {
			if conn.isRealConn { // 是真实节点直接返回数据
				_ = conn.writeDirectly(msg.OutBytes, 1)
			} else { // 是代理连接，转发数据到真实连接
				if !r.s.cluster.NodeIsOnline(conn.realNodeId) { // 节点没在线了，这里直接移除连接
					r.removeConnById(conn.uid, conn.connId)
					r.Warn("node not online", zap.Uint64("nodeId", conn.realNodeId), zap.String("uid", req.uid), zap.String("deviceId", msg.DeviceId))
					return errors.New("node not online")
				}

				forwardWriteReqMap[conn.realNodeId] = append(forwardWriteReqMap[conn.realNodeId], &FowardWriteReq{
					Uid:            req.uid,
					DeviceId:       msg.DeviceId,
					ConnId:         conn.proxyConnId,
					RecvFrameCount: 1,
					Data:           msg.OutBytes,
					localConnId:    conn.connId,
				})

			}
		}
	}

	for nodeId, reqs := range forwardWriteReqMap {
		for _, req := range reqs {
			_, err := r.fowardWriteReq(nodeId, req)
			if err != nil {
				r.Error("fowardWriteReq error", zap.Error(err))
				// 如果转发失败，这里直接移除连接
				sub.removeConnById(req.Uid, req.localConnId)
				continue
			}
		}

	}
	return nil
}

// 转发写请求
func (r *userReactor) fowardWriteReq(nodeId uint64, req *FowardWriteReq) (proto.Status, error) {
	timeoutCtx, cancel := context.WithTimeout(r.s.ctx, time.Second*2)
	defer cancel()
	data, err := req.Marshal()
	if err != nil {
		return 0, err
	}
	resp, err := r.s.cluster.RequestWithContext(timeoutCtx, nodeId, "/wk/connWrite", data)
	if err != nil {
		return 0, err
	}
	if resp.Status == proto.StatusOK {
		return proto.StatusOK, nil
	}
	return resp.Status, nil
}

type writeReq struct {
	uniqueNo string
	uid      string
	messages []ReactorUserMessage
	sub      *userReactorSub
}

// =================================== 转发userAction ===================================

func (r *userReactor) addForwardUserActionReq(action UserAction) {
	select {
	case r.processForwardUserActionC <- action:
	default:
		r.Warn("addForwardUserActionReq: processForwardUserActionC is full, ignore ", zap.String("uid", action.Uid), zap.String("actionType", action.ActionType.String()))
		sub := r.reactorSub(action.Uid)
		sub.step(action.Uid, UserAction{
			UniqueNo:   action.UniqueNo,
			ActionType: UserActionForwardResp,
			Uid:        action.Uid,
			Reason:     ReasonError,
		})
	}
}

func (r *userReactor) processForwardUserActionLoop() {
	const batchSize = 2048
	actions := make([]UserAction, 0, batchSize)
	done := false
	for !r.stopped.Load() {
		select {
		case req := <-r.processForwardUserActionC:
			actions = append(actions, req)
			for !done {
				select {
				case req := <-r.processForwardUserActionC:
					actions = append(actions, req)
					if len(actions) >= batchSize {
						done = true
					}
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

func (r *userReactor) processForwardUserAction(actions []UserAction) {

	r.handleForwardUserAction(actions)
}

func (r *userReactor) handleForwardUserAction(actions []UserAction) {
	userForwardActionMap := map[string][]UserAction{} // 用户对应的action
	userLeaderMap := map[string]uint64{}              // 用户对应的领导节点
	// 按照用户分组
	for _, action := range actions {
		forwardActions := userForwardActionMap[action.Uid]
		forwardActions = append(forwardActions, *action.Forward)
		userForwardActionMap[action.Uid] = forwardActions
		userLeaderMap[action.Uid] = action.LeaderId
	}

	for uid, fowardActions := range userForwardActionMap {
		leaderId := userLeaderMap[uid]

		var (
			err         error
			reason      = ReasonSuccess
			newLeaderId uint64
		)
		if !r.s.cluster.NodeIsOnline(leaderId) {
			// 重新获取频道领导
			newLeaderId, err = r.s.cluster.SlotLeaderIdOfChannel(uid, wkproto.ChannelTypePerson)
			if err != nil {
				r.Error("processForwardUserAction: SlotLeaderIdOfChannel error", zap.Error(err))
			}
			reason = ReasonError
		} else {
			newLeaderId, err = r.requestForwardUserAction(uid, leaderId, fowardActions)
			if err != nil {
				r.Error("handleForwardUserAction error", zap.Error(err))
				reason = ReasonError
			}
		}

		sub := r.reactorSub(uid)
		if newLeaderId > 0 {
			r.Info("leader change", zap.String("uid", uid), zap.Uint64("newLeaderId", newLeaderId), zap.Uint64("oldLeaderId", leaderId))
			sub.step(uid, UserAction{
				UniqueNo:   fowardActions[0].UniqueNo,
				ActionType: UserActionLeaderChange,
				LeaderId:   newLeaderId,
				Reason:     ReasonSuccess,
			})
		}
		for _, forwardAction := range fowardActions {
			lastMsg := forwardAction.Messages[len(forwardAction.Messages)-1]
			sub.step(uid, UserAction{
				UniqueNo:   forwardAction.UniqueNo,
				ActionType: UserActionForwardResp,
				Uid:        uid,
				Reason:     reason,
				Index:      lastMsg.Index,
				Forward:    &forwardAction,
			})
		}

	}

}

func (r *userReactor) requestForwardUserAction(uid string, leaderId uint64, actions []UserAction) (uint64, error) {
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

func (r *userReactor) forwardUserAction(nodeId uint64, actions []UserAction) (bool, error) {
	timeoutCtx, cancel := context.WithTimeout(r.s.ctx, time.Second*2)
	defer cancel()

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
	if resp.Status != proto.StatusOK {
		return false, fmt.Errorf("forwardUserAction failed, status=%d", resp.Status)
	}
	return false, nil
}

// =================================== node ping ===================================

func (r *userReactor) addNodePingReq(req *nodePingReq) {
	select {
	case r.processNodePingC <- req:
	default:
		r.Warn("addNodePingReq: processNodePingC is full, ignore ", zap.String("uid", req.uid), zap.Int("msgCount", len(req.messages)))

	}
}

func (r *userReactor) processNodePingLoop() {

	reqs := make([]*nodePingReq, 0, 10)
	done := false
	for !r.stopped.Load() {
		select {
		case req := <-r.processNodePingC:
			reqs = append(reqs, req)

			for !done {
				select {
				case req := <-r.processNodePingC:
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
			r.processNodePing(reqs)
			done = false
			reqs = reqs[:0]
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *userReactor) processNodePing(reqs []*nodePingReq) {
	r.handleNodePing(reqs)
}

func (r *userReactor) handleNodePing(reqs []*nodePingReq) {

	nodeIdMap := map[uint64][]*userConns{} // 按照节点分组
	for _, req := range reqs {
		for _, msg := range req.messages {
			userNodePings := nodeIdMap[msg.FromNodeId]
			exist := false
			for _, userNodePing := range userNodePings {
				if userNodePing.uid == req.uid {
					exist = true
					userNodePing.connIds = append(userNodePing.connIds, msg.ConnId)
					break
				}
			}
			if !exist {
				userNodePings = append(userNodePings, &userConns{
					uid:     req.uid,
					connIds: []int64{msg.ConnId},
				})
				nodeIdMap[msg.FromNodeId] = userNodePings
			}
		}
	}

	for nodeId, userNodePings := range nodeIdMap {
		req := &userNodePingReq{
			leaderId: r.s.opts.Cluster.NodeId,
			pings:    userNodePings,
		}
		data, err := req.Marshal()
		if err != nil {
			r.Error("userNodePingReq.Marshal error", zap.Error(err))
			return
		}
		msg := &proto.Message{
			MsgType: uint32(ClusterMsgTypeNodePing),
			Content: data,
		}
		err = r.s.cluster.Send(nodeId, msg)
		if err != nil {
			r.Error("cluster.Send error", zap.Error(err), zap.Uint64("nodeId", nodeId))
		}
	}

}

type nodePingReq struct {
	uid      string
	messages []ReactorUserMessage
}

type userNodePingReq struct {
	leaderId uint64
	pings    []*userConns
}

func (u *userNodePingReq) Marshal() ([]byte, error) {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteUint64(u.leaderId)
	encoder.WriteUint32(uint32(len(u.pings)))
	for _, ping := range u.pings {
		encoder.WriteString(ping.uid)
		encoder.WriteUint32(uint32(len(ping.connIds)))
		for _, connId := range ping.connIds {
			encoder.WriteInt64(connId)
		}
	}
	return encoder.Bytes(), nil
}

func (u *userNodePingReq) Unmarshal(data []byte) error {
	decoder := wkproto.NewDecoder(data)
	var err error
	if u.leaderId, err = decoder.Uint64(); err != nil {
		return err
	}
	pingCount, err := decoder.Uint32()
	if err != nil {
		return err
	}
	u.pings = make([]*userConns, 0, pingCount)
	for i := 0; i < int(pingCount); i++ {
		ping := &userConns{}
		if ping.uid, err = decoder.String(); err != nil {
			return err
		}
		connCount, err := decoder.Uint32()
		if err != nil {
			return err
		}
		ping.connIds = make([]int64, 0, connCount)
		for j := 0; j < int(connCount); j++ {
			connId, err := decoder.Int64()
			if err != nil {
				return err
			}
			ping.connIds = append(ping.connIds, connId)
		}
		u.pings = append(u.pings, ping)
	}
	return nil
}

// =================================== node pong ===================================

func (r *userReactor) addNodePongReq(req *nodePongReq) {
	select {
	case r.processNodePongC <- req:
	default:
		r.Warn("addNodePongReq: processNodePongC is full, ignore ", zap.String("uid", req.uid))
	}
}

func (r *userReactor) processNodePongLoop() {
	for !r.stopped.Load() {
		select {
		case req := <-r.processNodePongC:
			r.processNodePong(req)
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *userReactor) processNodePong(req *nodePongReq) {
	r.handleNodePong(req)
}

// TODO：可以优化成批量处理
func (r *userReactor) handleNodePong(req *nodePongReq) {

	userHandler := r.s.userReactor.getUserHandler(req.uid)
	if userHandler == nil {
		r.Warn("userHandler not found, not reply pong", zap.String("uid", req.uid))
		return
	}

	conns := userHandler.getConns()

	connIds := make([]int64, 0, len(conns))
	for _, conn := range conns {
		connIds = append(connIds, conn.connId)
	}

	userConnsReq := userConns{
		uid:     req.uid,
		connIds: connIds,
	}
	data, err := userConnsReq.Marshal()
	if err != nil {
		r.Error("userConnsReq.Marshal error", zap.Error(err))
		return
	}

	err = r.s.cluster.Send(req.leaderId, &proto.Message{
		MsgType: uint32(ClusterMsgTypeNodePong),
		Content: data,
	})
	if err != nil {
		r.Error("cluster.send failed", zap.Error(err), zap.String("uid", req.uid), zap.Uint64("leaderId", req.leaderId))
	}
}

type nodePongReq struct {
	uniqueNo string
	uid      string
	leaderId uint64
}

// 节点之间的ping，领导发送给从节点
type userConns struct {
	uid     string
	connIds []int64
}

func (u *userConns) Marshal() ([]byte, error) {
	encoder := wkproto.NewEncoder()
	defer encoder.End()
	encoder.WriteString(u.uid)
	for _, connId := range u.connIds {
		encoder.WriteInt64(connId)
	}
	return encoder.Bytes(), nil
}

func (u *userConns) Unmarshal(data []byte) error {
	decoder := wkproto.NewDecoder(data)
	var err error
	if u.uid, err = decoder.String(); err != nil {
		return err
	}
	u.connIds = make([]int64, 0)
	for decoder.Len() > 0 {
		connId, err := decoder.Int64()
		if err != nil {
			break
		}
		u.connIds = append(u.connIds, connId)
	}
	return nil
}

// =================================== proxy node timeout ===================================

func (r *userReactor) addProxyNodeTimeoutReq(req *proxyNodeTimeoutReq) {
	select {
	case r.processProxyNodeTimeoutC <- req:
	default:
		r.Warn("addProxyNodeTimeoutReq: processProxyNodeTimeoutC is full, ignore ", zap.String("uid", req.uid))
	}
}

func (r *userReactor) processProxyNodeTimeoutLoop() {
	for !r.stopped.Load() {
		select {
		case req := <-r.processProxyNodeTimeoutC:
			r.processProxyNodeTimeout(req)
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *userReactor) processProxyNodeTimeout(req *proxyNodeTimeoutReq) {
	for _, msg := range req.messages {
		r.Debug("proxy node timeout", zap.String("uid", req.uid), zap.Int64("connId", msg.ConnId), zap.Uint64("fromNodeId", msg.FromNodeId))
		r.s.userReactor.removeConnsByNodeId(req.uid, msg.FromNodeId)
	}
}

type proxyNodeTimeoutReq struct {
	uniqueNo string
	uid      string
	messages []ReactorUserMessage
}

// =================================== close ===================================

func (r *userReactor) addCloseReq(req *userCloseReq) {
	select {
	case r.processCloseC <- req:
	default:
		r.Warn("addCloseReq: processCloseC is full, ignore ", zap.String("uid", req.handler.uid))
	}

}

func (r *userReactor) processCloseLoop() {
	for !r.stopped.Load() {
		select {
		case req := <-r.processCloseC:
			r.processClose(req)
		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *userReactor) processClose(req *userCloseReq) {

	uid := req.handler.uid
	conns := req.handler.getConns()
	if len(conns) > 0 {
		// 如果用户处理者下面有连接，先移除连接，再移除用户处理者
		for _, conn := range conns {
			// 如果是本地连接，则移除后需要关闭连接，节省资源
			if conn.isRealConn {
				r.Info("close real conn", zap.String("uid", uid), zap.Int64("connId", conn.connId), zap.String("role", req.handler.role.String()))
				r.removeConnById(uid, conn.connId)
				conn.close()
			} else {
				r.Info("close proxy conn", zap.String("uid", uid), zap.Int64("connId", conn.connId))
				r.removeConnById(uid, conn.connId)
			}
		}
	}

	if req.handler.role == userRoleLeader {
		r.s.trace.Metrics.App().OnlineUserCountAdd(-1)
	}

	r.removeUserHandler(uid)

}

type userCloseReq struct {
	handler *userHandler
}

// =================================== 检查领导的正确性 ===================================

func (r *userReactor) addCheckLeaderReq(req *checkLeaderReq) {
	select {
	case r.processCheckLeaderC <- req:
	default:
		r.Warn("addCheckLeaderReq: processCheckLeaderC is full, ignore ", zap.String("uid", req.uid))
	}
}

func (r *userReactor) processCheckLeaderLoop() {
	batch := 1024
	reqs := make([]*checkLeaderReq, 0, batch)
	done := false
	for !r.stopped.Load() {
		select {
		case req := <-r.processCheckLeaderC:
			reqs = append(reqs, req)
			for !done {
				select {
				case req := <-r.processCheckLeaderC:
					reqs = append(reqs, req)
					if len(reqs) >= batch {
						done = true
					}
				default:
					done = true
				}
			}
			r.processCheckLeaders(reqs)
			done = false
			reqs = reqs[:0]

		case <-r.stopper.ShouldStop():
			return
		}
	}
}

func (r *userReactor) processCheckLeaders(reqs []*checkLeaderReq) {
	for _, req := range reqs {
		r.processCheckLeader(req)
	}
}

func (r *userReactor) processCheckLeader(req *checkLeaderReq) {

	leaderId, err := r.s.cluster.SlotLeaderIdOfChannel(req.uid, wkproto.ChannelTypePerson)
	if err != nil {
		r.Error("SlotLeaderIdOfChannel error", zap.Error(err))
		return
	}
	if leaderId != req.leaderId {
		r.Info("leader change", zap.String("uid", req.uid), zap.Uint64("newLeaderId", leaderId), zap.Uint64("oldLeaderId", req.leaderId))
		req.sub.step(req.uid, UserAction{
			UniqueNo:   req.uniqueNo,
			ActionType: UserActionLeaderChange,
			LeaderId:   leaderId,
		})
	}
}

type checkLeaderReq struct {
	uniqueNo string
	uid      string
	leaderId uint64
	sub      *userReactorSub
}
