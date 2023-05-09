package server

import (
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

type Processor struct {
	s               *Server
	connContextPool sync.Pool
	frameWorkPool   *FrameWorkPool
	wklog.Log
}

func NewProcessor(s *Server) *Processor {
	return &Processor{
		s:             s,
		Log:           wklog.NewWKLog("Processor"),
		frameWorkPool: NewFrameWorkPool(),
		connContextPool: sync.Pool{
			New: func() any {
				cc := newConnContext(s)
				cc.init()
				return cc
			},
		},
	}
}

func (p *Processor) process(conn wknet.Conn) {
	connCtx := conn.Context().(*connContext)
	frames := connCtx.popFrames()

	p.processFrames(conn, frames)

}

func (p *Processor) processFrames(conn wknet.Conn, frames []wkproto.Frame) {

	p.sameFrames(frames, func(s, e int, frs []wkproto.Frame) {
		// newFs := make([]wkproto.Frame, len(frs))
		// copy(newFs, frs)
		p.frameWorkPool.Submit(func() {
			p.processSameFrame(conn, frs[0].GetFrameType(), frs, s, e)
		})
		// p.processSameFrame(conn, frs[0].GetFrameType(), frs, s, e)

		// go func(s1, e1 int, c wknet.Conn, fs []wkproto.Frame) {
		// 	p.processSameFrame(c, fs[0].GetFrameType(), fs, s1, e1)
		// }(s, e, conn, frs)

	})

}

func (p *Processor) sameFrames(frames []wkproto.Frame, callback func(s, e int, fs []wkproto.Frame)) {
	for i := 0; i < len(frames); {
		frame := frames[i]
		start := i
		end := i + 1
		for end < len(frames) {
			nextFrame := frames[end]
			if nextFrame.GetFrameType() == frame.GetFrameType() {
				end++
			} else {
				break
			}
		}
		callback(start, end, frames[start:end])
		i = end
	}
}

func (p *Processor) processSameFrame(conn wknet.Conn, frameType wkproto.FrameType, frames []wkproto.Frame, s, e int) {
	switch frameType {
	case wkproto.PING: // ping
		p.processPing(conn, frames[0].(*wkproto.PingPacket))
	case wkproto.SEND: // process send
		// TODO: tmpFrames need optimize
		tmpFrames := make([]*wkproto.SendPacket, 0, len(frames))
		for _, frame := range frames {
			tmpFrames = append(tmpFrames, frame.(*wkproto.SendPacket))
		}
		p.processMsgs(conn, tmpFrames)
	case wkproto.SENDACK: // process sendack
		tmpFrames := make([]*wkproto.SendackPacket, 0, len(frames))
		for _, frame := range frames {
			tmpFrames = append(tmpFrames, frame.(*wkproto.SendackPacket))
		}
		p.processMsgAcks(conn, tmpFrames)
	case wkproto.RECVACK: // process recvack
		tmpFrames := make([]*wkproto.RecvackPacket, 0, len(frames))
		for _, frame := range frames {
			tmpFrames = append(tmpFrames, frame.(*wkproto.RecvackPacket))
		}
		p.processRecvacks(conn, tmpFrames)
	}
	conn.Context().(*connContext).finishFrames(len(frames))
}

// #################### conn auth ####################
func (p *Processor) processAuth(conn wknet.Conn, connectPacket *wkproto.ConnectPacket) {
	fmt.Println("#########processAuth##########")
	connCtx := p.connContextPool.Get().(*connContext)
	connCtx.init()
	connCtx.conn = conn
	conn.SetContext(connCtx)

	if strings.TrimSpace(connectPacket.ClientKey) == "" {
		p.responseConnackAuthFail(conn)
		return
	}
	conn.SetProtoVersion(int(connectPacket.Version))

	dhServerPrivKey, dhServerPublicKey := wkutil.GetCurve25519KeypPair() // 生成服务器的DH密钥对
	aesKey, aesIV, err := p.getClientAesKeyAndIV(connectPacket.ClientKey, dhServerPrivKey)
	if err != nil {
		p.Error("获取客户端的aesKey和aesIV失败！", zap.Error(err))
		p.responseConnackAuthFail(conn)
		return
	}
	dhServerPublicKeyEnc := base64.StdEncoding.EncodeToString(dhServerPublicKey[:])
	timeDiff := time.Now().UnixNano()/1000/1000 - connectPacket.ClientTimestamp

	conn.SetAuthed(true)
	conn.SetDeviceFlag(connectPacket.DeviceFlag.ToUint8())
	conn.SetDeviceID(connectPacket.DeviceID)
	conn.SetUID(connectPacket.UID)
	conn.SetValue(aesKeyKey, aesKey)
	conn.SetValue(aesIVKey, aesIV)
	conn.SetValue(deviceLevelKey, 1)

	// p.s.connManager.Add(conn)

	p.response(conn, &wkproto.ConnackPacket{
		Salt:       aesIV,
		ServerKey:  dhServerPublicKeyEnc,
		ReasonCode: wkproto.ReasonSuccess,
		TimeDiff:   timeDiff,
	})

}

// #################### ping ####################
func (p *Processor) processPing(conn wknet.Conn, pingPacket *wkproto.PingPacket) {
	fmt.Println("ping--->", conn.Fd(), conn.UID())
	p.response(conn, &wkproto.PongPacket{})
}

// #################### messages ####################
func (p *Processor) processMsgs(conn wknet.Conn, msgs []*wkproto.SendPacket) {

	sendackPackets := make([]wkproto.Frame, 0, len(msgs))
	for _, msg := range msgs {
		sendackPackets = append(sendackPackets, &wkproto.SendackPacket{
			ReasonCode:  wkproto.ReasonSuccess,
			ClientSeq:   msg.ClientSeq,
			ClientMsgNo: msg.ClientMsgNo,
		})
	}
	p.response(conn, sendackPackets...)
}

// #################### message ack ####################
func (p *Processor) processMsgAcks(conn wknet.Conn, acks []*wkproto.SendackPacket) {

}

// #################### recv ack ####################
func (p *Processor) processRecvacks(conn wknet.Conn, acks []*wkproto.RecvackPacket) {

}

// #################### process conn close ####################
func (p *Processor) processClose(conn wknet.Conn, err error) {
	p.Debug("conn is close", zap.Error(err), zap.Any("conn", conn))
	if conn.Context() != nil {
		connCtx := conn.Context().(*connContext)
		connCtx.release()
		p.connContextPool.Put(connCtx)
	}
}

// #################### others ####################

func (p *Processor) response(conn wknet.Conn, frames ...wkproto.Frame) {
	p.s.dispatch.dataOut(conn, frames...)
}

func (p *Processor) responseConnackAuthFail(c wknet.Conn) {
	p.responseConnack(c, 0, wkproto.ReasonAuthFail)
}

func (p *Processor) responseConnack(c wknet.Conn, timeDiff int64, code wkproto.ReasonCode) {

	p.response(c, &wkproto.ConnackPacket{
		ReasonCode: code,
		TimeDiff:   timeDiff,
	})
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
