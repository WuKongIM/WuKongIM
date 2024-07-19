package server

import (
	"encoding/base64"

	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
)

// func (s *Server) processAuth(conn wknet.Conn, connectPacket *wkproto.ConnectPacket) {

// 	err := s.authPool.Submit(func() {
// 		s.handleAuth(conn, connectPacket)
// 	})
// 	if err != nil {
// 		s.Error("authPool.Submit err", zap.Error(err))
// 		s.responseWithConn(conn, &wkproto.ConnackPacket{
// 			ReasonCode: wkproto.ReasonSystemError,
// 		})

// 	}
// }

// func (s *Server) handleAuth(conn wknet.Conn, connectPacket *wkproto.ConnectPacket) {

// 	var (
// 		uid                            = connectPacket.UID
// 		devceLevel wkproto.DeviceLevel = wkproto.DeviceLevelMaster
// 		err        error
// 		// devceLevelI uint8
// 	)
// 	if strings.TrimSpace(connectPacket.ClientKey) == "" {
// 		s.responseConnackAuthFail(conn)
// 		return
// 	}
// 	// -------------------- token verify --------------------
// 	if connectPacket.UID == s.opts.ManagerUID {
// 		if s.opts.ManagerTokenOn && connectPacket.Token != s.opts.ManagerToken {
// 			s.Error("manager token verify fail", zap.String("uid", uid), zap.String("token", connectPacket.Token))
// 			s.responseConnackAuthFail(conn)
// 			return
// 		}
// 		devceLevel = wkproto.DeviceLevelSlave // 默认都是slave设备
// 	} else if s.opts.TokenAuthOn {
// 		if connectPacket.Token == "" {
// 			s.Error("token is empty")
// 			s.responseConnackAuthFail(conn)
// 			return
// 		}
// 		user, err := s.store.GetUser(uid, connectPacket.DeviceFlag.ToUint8())
// 		if err != nil {
// 			s.Error("get user token err", zap.Error(err))
// 			s.responseConnackAuthFail(conn)
// 			return

// 		}
// 		if user.Token != connectPacket.Token {
// 			s.Error("token verify fail", zap.String("expectToken", user.Token), zap.String("actToken", connectPacket.Token), zap.Any("conn", conn))
// 			s.responseConnackAuthFail(conn)
// 			return
// 		}
// 		devceLevel = wkproto.DeviceLevel(user.DeviceLevel)
// 	} else {
// 		devceLevel = wkproto.DeviceLevelSlave // 默认都是slave设备
// 	}

// 	// -------------------- ban  --------------------
// 	userChannelInfo, err := s.store.GetChannel(uid, wkproto.ChannelTypePerson)
// 	if err != nil {
// 		s.Error("get user channel info err", zap.Error(err))
// 		s.responseConnackAuthFail(conn)
// 		return
// 	}
// 	ban := false
// 	if !wkdb.IsEmptyChannelInfo(userChannelInfo) {
// 		ban = userChannelInfo.Ban
// 	}
// 	if ban {
// 		s.Error("user is ban", zap.String("uid", uid))
// 		s.responseConnack(conn, 0, wkproto.ReasonBan)
// 		return
// 	}

// 	// -------------------- get message encrypt key --------------------
// 	dhServerPrivKey, dhServerPublicKey := wkutil.GetCurve25519KeypPair() // 生成服务器的DH密钥对
// 	aesKey, aesIV, err := s.getClientAesKeyAndIV(connectPacket.ClientKey, dhServerPrivKey)
// 	if err != nil {
// 		s.Error("get client aes key and iv err", zap.Error(err))
// 		s.responseConnackAuthFail(conn)
// 		return
// 	}
// 	dhServerPublicKeyEnc := base64.StdEncoding.EncodeToString(dhServerPublicKey[:])

// 	// -------------------- same master kicks each other --------------------
// 	oldConns := s.userReactor.getConnContextByDeviceFlag(uid, connectPacket.DeviceFlag)
// 	if len(oldConns) > 0 {
// 		if devceLevel == wkproto.DeviceLevelMaster { // 如果设备是master级别，则把旧连接都踢掉
// 			for _, oldConn := range oldConns {
// 				s.userReactor.removeConnContext(oldConn.uid, oldConn.deviceId)
// 				if oldConn.deviceId != connectPacket.DeviceID {
// 					s.Info("same master kicks each other", zap.String("devceLevel", devceLevel.String()), zap.String("uid", uid), zap.String("deviceID", connectPacket.DeviceID), zap.String("oldDeviceID", oldConn.deviceId))
// 					s.response(oldConn, &wkproto.DisconnectPacket{
// 						ReasonCode: wkproto.ReasonConnectKick,
// 						Reason:     "login in other device",
// 					})
// 					s.timingWheel.AfterFunc(time.Second*5, func() {
// 						oldConn.close()
// 					})
// 				} else {
// 					s.timingWheel.AfterFunc(time.Second*4, func() {
// 						oldConn.close() // Close old connection
// 					})
// 				}
// 				s.Debug("close old conn", zap.Any("oldConn", oldConn))
// 			}
// 		} else if devceLevel == wkproto.DeviceLevelSlave { // 如果设备是slave级别，则把相同的deviceID踢掉
// 			for _, oldConn := range oldConns {
// 				if oldConn.deviceId == connectPacket.DeviceID {
// 					s.userReactor.removeConnContext(oldConn.uid, oldConn.deviceId)
// 					s.timingWheel.AfterFunc(time.Second*5, func() {
// 						oldConn.close()
// 					})
// 				}
// 			}
// 		}

// 	}

// 	// -------------------- set conn info --------------------
// 	timeDiff := time.Now().UnixNano()/1000/1000 - connectPacket.ClientTimestamp

// 	// connCtx := p.connContextPool.Get().(*connContext)

// 	lastVersion := connectPacket.Version
// 	hasServerVersion := false
// 	if connectPacket.Version > wkproto.LatestVersion {
// 		lastVersion = wkproto.LatestVersion
// 	}

// 	connInfo := connInfo{
// 		connId:       conn.ID(),
// 		uid:          connectPacket.UID,
// 		deviceId:     connectPacket.DeviceID,
// 		deviceFlag:   wkproto.DeviceFlag(connectPacket.DeviceFlag),
// 		deviceLevel:  devceLevel,
// 		protoVersion: lastVersion,
// 		aesKey:       aesKey,
// 		aesIV:        aesIV,
// 	}

// 	sub := s.userReactor.reactorSub(connectPacket.UID)

// 	connCtx := newConnContext(connInfo, conn, sub)
// 	conn.SetContext(connCtx)
// 	conn.SetMaxIdle(s.opts.ConnIdleTime)

// 	s.userReactor.addConnContext(connCtx)

// 	// -------------------- response connack --------------------

// 	if connectPacket.Version > 3 {
// 		hasServerVersion = true
// 	}

// 	s.Debug("Auth Success", zap.Any("conn", conn), zap.Uint8("protoVersion", connectPacket.Version), zap.Bool("hasServerVersion", hasServerVersion))
// 	connack := &wkproto.ConnackPacket{
// 		Salt:          aesIV,
// 		ServerKey:     dhServerPublicKeyEnc,
// 		ReasonCode:    wkproto.ReasonSuccess,
// 		TimeDiff:      timeDiff,
// 		ServerVersion: lastVersion,
// 		NodeId:        s.opts.Cluster.NodeId,
// 	}
// 	connack.HasServerVersion = hasServerVersion
// 	s.response(connCtx, connack)
// 	// -------------------- user online --------------------
// 	// 在线webhook
// 	deviceOnlineCount := s.userReactor.getConnContextCountByDeviceFlag(uid, connectPacket.DeviceFlag)
// 	totalOnlineCount := s.userReactor.getConnContextCount(uid)
// 	s.webhook.Online(uid, connectPacket.DeviceFlag, conn.ID(), deviceOnlineCount, totalOnlineCount)
// 	if totalOnlineCount <= 1 {
// 		s.trace.Metrics.App().OnlineUserCountAdd(1) // 统计在线用户数
// 	}
// 	s.trace.Metrics.App().OnlineDeviceCountAdd(1) // 统计在线设备数
// }

// 获取客户端的aesKey和aesIV
// dhServerPrivKey  服务端私钥
func (s *Server) getClientAesKeyAndIV(clientKey string, dhServerPrivKey [32]byte) (string, string, error) {

	clientKeyBytes, err := base64.StdEncoding.DecodeString(clientKey)
	if err != nil {
		return "", "", err
	}

	var dhClientPubKeyArray [32]byte
	copy(dhClientPubKeyArray[:], clientKeyBytes[:32])

	// 获得DH的共享key
	shareKey := wkutil.GetCurve25519Key(dhServerPrivKey, dhClientPubKeyArray) // 共享key

	aesIV := wkutil.GetRandomString(16)
	aesKey := wkutil.MD5(base64.StdEncoding.EncodeToString(shareKey[:]))[:16]
	return aesKey, aesIV, nil
}
