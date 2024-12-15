package server

import (
	"encoding/base64"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

func (p *processUser) handleConnect(msg *reactor.UserMessage) (wkproto.ReasonCode, *wkproto.ConnackPacket, error) {
	var (
		conn          = msg.Conn
		connectPacket = msg.Frame.(*wkproto.ConnectPacket)
		devceLevel    wkproto.DeviceLevel
		uid           = connectPacket.UID
	)
	// -------------------- token verify --------------------
	if connectPacket.UID == p.s.opts.ManagerUID {
		if p.s.opts.ManagerTokenOn && connectPacket.Token != p.s.opts.ManagerToken {
			p.Error("manager token verify fail", zap.String("uid", uid), zap.String("token", connectPacket.Token))
			return wkproto.ReasonAuthFail, nil, nil
		}
		devceLevel = wkproto.DeviceLevelSlave // 默认都是slave设备
	} else if p.s.opts.TokenAuthOn {
		if connectPacket.Token == "" {
			p.Error("token is empty")
			return wkproto.ReasonAuthFail, nil, errors.New("token is empty")
		}
		device, err := p.s.store.GetDevice(uid, connectPacket.DeviceFlag)
		if err != nil {
			p.Error("get device token err", zap.Error(err))
			return wkproto.ReasonAuthFail, nil, err
		}
		if device.Token != connectPacket.Token {
			p.Error("token verify fail", zap.String("expectToken", device.Token), zap.String("actToken", connectPacket.Token))
			return wkproto.ReasonAuthFail, nil, errors.New("token verify fail")
		}
		devceLevel = wkproto.DeviceLevel(device.DeviceLevel)
	} else {
		devceLevel = wkproto.DeviceLevelSlave // 默认都是slave设备
	}

	// -------------------- ban  --------------------
	userChannelInfo, err := p.s.store.GetChannel(uid, wkproto.ChannelTypePerson)
	if err != nil {
		p.Error("get device channel info err", zap.Error(err))
		return wkproto.ReasonAuthFail, nil, err
	}
	ban := false
	if !wkdb.IsEmptyChannelInfo(userChannelInfo) {
		ban = userChannelInfo.Ban
	}
	if ban {
		p.Error("device is ban", zap.String("uid", uid))
		return wkproto.ReasonBan, nil, errors.New("device is ban")
	}

	// -------------------- get message encrypt key --------------------
	dhServerPrivKey, dhServerPublicKey := wkutil.GetCurve25519KeypPair() // 生成服务器的DH密钥对
	aesKey, aesIV, err := p.getClientAesKeyAndIV(connectPacket.ClientKey, dhServerPrivKey)
	if err != nil {
		p.Error("get client aes key and iv err", zap.Error(err))
		return wkproto.ReasonAuthFail, nil, err
	}
	dhServerPublicKeyEnc := base64.StdEncoding.EncodeToString(dhServerPublicKey[:])

	// -------------------- same master kicks each other --------------------
	oldConns := reactor.User.ConnsByDeviceFlag(uid, connectPacket.DeviceFlag)
	if len(oldConns) > 0 {
		if devceLevel == wkproto.DeviceLevelMaster { // 如果设备是master级别，则把旧连接都踢掉
			for _, oldConn := range oldConns {
				if oldConn.Equal(conn) { // 不能把自己踢了
					continue
				}
				if oldConn.DeviceId != connectPacket.DeviceID {
					p.Info("auth: same master kicks each other",
						zap.String("devceLevel", devceLevel.String()),
						zap.String("uid", uid),
						zap.String("deviceID", connectPacket.DeviceID),
						zap.String("oldDeviceId", oldConn.DeviceId),
					)
					reactor.User.Kick(oldConn, wkproto.ReasonConnectKick, "login in other device")
				} else {
					reactor.User.CloseConn(oldConn)
				}
				p.Info("auth: close old conn for master", zap.Any("oldConn", oldConn))
			}
		} else if devceLevel == wkproto.DeviceLevelSlave { // 如果设备是slave级别，则把相同的deviceId关闭
			for _, oldConn := range oldConns {
				if oldConn.ConnId != conn.ConnId && oldConn.DeviceId == connectPacket.DeviceID {
					reactor.User.CloseConn(oldConn)
					p.Info("auth: close old conn for slave", zap.Any("oldConn", oldConn))
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

	conn.AesIV = aesIV
	conn.AesKey = aesKey
	conn.Auth = true
	conn.ProtoVersion = lastVersion
	conn.DeviceLevel = devceLevel

	realConn := p.s.connManager.getConn(conn.ConnId)
	if realConn != nil {
		realConn.SetMaxIdle(p.s.opts.ConnIdleTime)
	}

	// -------------------- response connack --------------------

	if connectPacket.Version > 3 {
		hasServerVersion = true
	}

	p.Debug("auth: auth Success", zap.Uint8("protoVersion", connectPacket.Version), zap.Bool("hasServerVersion", hasServerVersion))
	connack := &wkproto.ConnackPacket{
		Salt:          string(aesIV),
		ServerKey:     dhServerPublicKeyEnc,
		ReasonCode:    wkproto.ReasonSuccess,
		TimeDiff:      timeDiff,
		ServerVersion: lastVersion,
		NodeId:        p.s.opts.Cluster.NodeId,
	}
	connack.HasServerVersion = hasServerVersion
	// -------------------- user online --------------------
	// 在线webhook
	deviceOnlineCount := reactor.User.ConnCountByDeviceFlag(uid, connectPacket.DeviceFlag)
	totalOnlineCount := reactor.User.ConnCountByUid(uid)
	p.s.webhook.Online(uid, connectPacket.DeviceFlag, conn.ConnId, deviceOnlineCount, totalOnlineCount)

	return wkproto.ReasonSuccess, connack, nil
}

// 获取客户端的aesKey和aesIV
// dhServerPrivKey  服务端私钥
func (p *processUser) getClientAesKeyAndIV(clientKey string, dhServerPrivKey [32]byte) ([]byte, []byte, error) {

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
