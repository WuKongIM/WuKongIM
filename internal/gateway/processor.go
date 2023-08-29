package gateway

import (
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway/proto"
	"github.com/WuKongIM/WuKongIM/internal/gatewaycommon"
	"github.com/WuKongIM/WuKongIM/internal/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type Processor struct {
	g *Gateway
	wklog.Log
	blockSeq   atomic.Uint64
	blockProto *proto.Proto
}

func NewProcessor(g *Gateway) *Processor {
	p := &Processor{
		g:          g,
		Log:        wklog.NewWKLog("Processor"),
		blockProto: proto.New(),
	}
	return p
}

// client connection auth
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
	node := p.g.logicClientManager.getLogicClientWithKey(uid)

	authResp, err := node.ClientAuth(&pb.AuthReq{
		ConnID:       conn.ID(),
		Uid:          uid,
		DeviceFlag:   uint32(connectPacket.DeviceFlag),
		DeviceID:     connectPacket.DeviceID,
		GatewayID:    p.g.opts.GatewayID(),
		ProtoVersion: uint32(connectPacket.Version),
		Token:        token,
		ClientKey:    connectPacket.ClientKey,
	})
	if err != nil {
		p.Error("auth error", zap.Error(err))
		p.responseConnackAuthFail(conn)
		return
	}
	devceLevel = wkproto.DeviceLevel(authResp.DeviceLevel)

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
					oldConn.(wknet.Conn).Close()
				})
			} else {
				p.g.timingWheel.AfterFunc(time.Second*4, func() {
					oldConn.(wknet.Conn).Close() // Close old connection
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
	conn.SetValue(aesKeyKey, authResp.AesKey)
	conn.SetValue(aesIVKey, authResp.AesIV)
	conn.SetDeviceLevel(devceLevelI)
	conn.SetMaxIdle(p.g.opts.ConnIdleTime)

	p.g.connManager.AddConn(conn)

	// -------------------- response connack --------------------

	p.Debug("Auth Success", zap.Any("conn", conn))
	p.dataOut(conn, &wkproto.ConnackPacket{
		Salt:       authResp.AesIV,
		ServerKey:  authResp.DhServerPublicKey,
		ReasonCode: wkproto.ReasonSuccess,
		TimeDiff:   timeDiff,
	})
}

func (p *Processor) handlePing(conn wknet.Conn) {
	p.dataOut(conn, &wkproto.PongPacket{})
}

// client deliverData
func (p *Processor) deliverData(conn wknet.Conn, data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}

	firstByte := data[0]
	if firstByte == byte(wkproto.PING) {
		p.handlePing(conn)
		return 1, nil
	}

	p.g.monitor.UpstreamTrafficAdd(len(data))
	p.g.monitor.InBytesAdd(int64(len(data)))

	cli := p.g.logicClientManager.getLogicClientWithKey(conn.UID())

	gatewayID := p.g.opts.GatewayID()

	size, err := cli.Write(&pb.Conn{
		Id:         conn.ID(),
		Uid:        conn.UID(),
		DeviceFlag: uint32(conn.DeviceFlag()),
		GatewayID:  gatewayID,
	}, data)
	if err != nil {
		p.Warn("Failed to Write the message for deliverData", zap.Error(err))
		return 0, nil
	}
	return size, nil

	// if p.blockSeq.Load() == 0 {
	// 	p.blockSeq.Inc()
	// }

	// blockData, err := p.blockProto.Encode(&proto.Block{
	// 	Seq:        p.blockSeq.Load(),
	// 	ConnID:     conn.ID(),
	// 	UID:        conn.UID(),
	// 	DeviceFlag: wkproto.DeviceFlag(conn.DeviceFlag()),
	// 	Data:       data,
	// })
	// if err != nil {
	// 	p.Warn("Failed to encode the message", zap.Error(err))
	// 	return nil
	// }
	// if len(blockData) == 0 {
	// 	return nil
	// }
	// nodeclient := p.g.nodeClientManager.getNodeClientWithKey(conn.UID())
	// err = nodeclient.Append(blockData)
	// if err != nil {
	// 	p.Warn("Failed to deliver the message", zap.Error(err))
	// 	return nil
	// }

}

func (p *Processor) OnWriteFromLogic(conn *pb.Conn, data []byte) error {
	c := p.g.connManager.GetConn(conn.Id)
	if c == nil {
		p.Debug("conn is nil", zap.Any("conn", conn))
		return nil
	}
	// 统计
	wnetCon, ok := c.(wknet.Conn)
	if !ok {
		p.Warn("conn is not wknet.Conn", zap.Any("conn", conn))
		return nil
	}
	connStats := wnetCon.ConnStats()
	dataLen := len(data)
	// p.s.monitor.DownstreamTrafficAdd(dataLen)
	// d.s.outBytes.Add(int64(dataLen))
	connStats.OutBytes.Add(int64(dataLen))

	wsConn, wsok := c.(wknet.IWSConn) // websocket连接
	if wsok {
		err := wsConn.WriteServerBinary(data)
		if err != nil {
			p.Warn("Failed to write the message", zap.Error(err))
		}

	} else {
		_, err := wnetCon.WriteToOutboundBuffer(data)
		if err != nil {
			p.Warn("Failed to write the message", zap.Error(err))
		}
	}
	wnetCon.WakeWrite()
	return nil
}

func (p *Processor) GetConnManager() gatewaycommon.ConnManager {

	return p.g.connManager
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
func (p *Processor) dataOut(gconn gatewaycommon.Conn, frames ...wkproto.Frame) {
	if len(frames) == 0 {
		return
	}
	conn := gconn.(wknet.Conn)

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
