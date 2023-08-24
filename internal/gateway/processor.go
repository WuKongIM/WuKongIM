package gateway

import (
	"encoding/base64"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/logicclient"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

type Processor struct {
	g *Gateway
	wklog.Log
}

func NewProcessor(g *Gateway) *Processor {
	return &Processor{
		g:   g,
		Log: wklog.NewWKLog("Processor"),
	}
}

func (p *Processor) auth(conn wknet.Conn, connectPacket *wkproto.ConnectPacket) {

	var (
		uid                             = connectPacket.UID
		devceLevel  wkproto.DeviceLevel = wkproto.DeviceLevelMaster
		err         error
		devceLevelI uint8
		token       string
	)
	if strings.TrimSpace(connectPacket.ClientKey) == "" {
		p.responseConnackAuthFail(conn)
		return
	}
	// -------------------- request logic --------------------
	authResp, err := p.g.logic.Auth(&logicclient.AuthReq{
		Uid:        uid,
		Token:      token,
		DeviceFlag: uint32(connectPacket.DeviceFlag),
	})
	if err != nil {
		p.Error("auth err", zap.Error(err))
		p.responseConnackAuthFail(conn)
		return
	}

	devceLevel = wkproto.DeviceLevel(authResp.DeviceLevel)

	// -------------------- get message encrypt key --------------------
	dhServerPrivKey, dhServerPublicKey := wkutil.GetCurve25519KeypPair() // 生成服务器的DH密钥对
	aesKey, aesIV, err := p.getClientAesKeyAndIV(connectPacket.ClientKey, dhServerPrivKey)
	if err != nil {
		p.Error("get client aes key and iv err", zap.Error(err))
		p.responseConnackAuthFail(conn)
		return
	}
	dhServerPublicKeyEnc := base64.StdEncoding.EncodeToString(dhServerPublicKey[:])

	// -------------------- same master kicks each other --------------------
	oldConns := p.g.connManager.GetConnsWith(uid, connectPacket.DeviceFlag)
	if len(oldConns) > 0 && devceLevel == wkproto.DeviceLevelMaster {
		for _, oldConn := range oldConns {
			p.g.connManager.RemoveConnWithID(oldConn.ID())
			if oldConn.DeviceID() != connectPacket.DeviceID {
				p.Info("same master kicks each other", zap.String("devceLevel", devceLevel.String()), zap.String("uid", uid), zap.String("deviceID", connectPacket.DeviceID), zap.String("oldDeviceID", oldConn.DeviceID()))
				p.dataOut(oldConn, &wkproto.DisconnectPacket{
					ReasonCode: wkproto.ReasonConnectKick,
					Reason:     "login in other device",
				})
				p.g.timingWheel.AfterFunc(time.Second*10, func() {
					oldConn.Close()
				})
			} else {
				p.g.timingWheel.AfterFunc(time.Second*4, func() {
					oldConn.Close() // Close old connection
				})
			}
			p.Debug("close old conn", zap.Any("oldConn", oldConn))
		}
	}
	// -------------------- set conn info --------------------
	timeDiff := time.Now().UnixNano()/1000/1000 - connectPacket.ClientTimestamp

	connCtx := newConnContext(p.g)
	conn.SetContext(connCtx)
	conn.SetProtoVersion(int(connectPacket.Version))
	conn.SetAuthed(true)
	conn.SetDeviceFlag(connectPacket.DeviceFlag.ToUint8())
	conn.SetDeviceID(connectPacket.DeviceID)
	conn.SetUID(connectPacket.UID)
	conn.SetValue(aesKeyKey, aesKey)
	conn.SetValue(aesIVKey, aesIV)
	conn.SetDeviceLevel(devceLevelI)
	conn.SetMaxIdle(p.g.opts.ConnIdleTime)

	p.g.connManager.AddConn(conn)

	// -------------------- response connack --------------------

	p.Debug("Auth Success", zap.Any("conn", conn))
	p.dataOut(conn, &wkproto.ConnackPacket{
		Salt:       aesIV,
		ServerKey:  dhServerPublicKeyEnc,
		ReasonCode: wkproto.ReasonSuccess,
		TimeDiff:   timeDiff,
	})
}

func (p *Processor) responseConnackAuthFail(c wknet.Conn) {
	p.responseConnack(c, 0, wkproto.ReasonAuthFail)
}

func (p *Processor) responseConnack(c wknet.Conn, timeDiff int64, code wkproto.ReasonCode) {

	p.dataOut(c, &wkproto.ConnackPacket{
		ReasonCode: code,
		TimeDiff:   timeDiff,
	})
}

// 数据统一出口
func (p *Processor) dataOut(conn wknet.Conn, frames ...wkproto.Frame) {
	if len(frames) == 0 {
		return
	}

	// 统计
	connStats := conn.ConnStats()
	p.g.monitor.DownstreamPackageAdd(len(frames))
	connStats.OutMsgs.Add(int64(len(frames)))

	wsConn, wsok := conn.(wknet.IWSConn) // websocket连接
	for _, frame := range frames {
		data, err := p.g.opts.Proto.EncodeFrame(frame, uint8(conn.ProtoVersion()))
		if err != nil {
			p.Warn("Failed to encode the message", zap.Error(err))
		} else {
			// 统计
			dataLen := len(data)
			p.g.monitor.DownstreamTrafficAdd(dataLen)
			connStats.OutBytes.Add(int64(dataLen))

			if wsok {
				err = wsConn.WriteServerBinary(data)
				if err != nil {
					p.Warn("Failed to write the message", zap.Error(err))
				}

			} else {
				_, err = conn.WriteToOutboundBuffer(data)
				if err != nil {
					p.Warn("Failed to write the message", zap.Error(err))
				}
			}

		}
	}
	_ = conn.WakeWrite()

}

// 获取客户端的aesKey和aesIV
// dhServerPrivKey  服务端私钥
func (p *Processor) getClientAesKeyAndIV(clientKey string, dhServerPrivKey [32]byte) (string, string, error) {

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
