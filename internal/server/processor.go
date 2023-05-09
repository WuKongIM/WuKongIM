package server

import (
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/limlog"
	"github.com/WuKongIM/WuKongIM/pkg/limnet"
	"github.com/WuKongIM/WuKongIM/pkg/limutil"
	"github.com/WuKongIM/WuKongIM/pkg/lmproto"
	"go.uber.org/zap"
)

type Processor struct {
	s               *Server
	connContextPool sync.Pool
	frameWorkPool   *FrameWorkPool
	limlog.Log
}

func NewProcessor(s *Server) *Processor {
	return &Processor{
		s:             s,
		Log:           limlog.NewLIMLog("Processor"),
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

func (p *Processor) process(conn limnet.Conn) {
	connCtx := conn.Context().(*connContext)
	frames := connCtx.popFrames()

	p.processFrames(conn, frames)

}

func (p *Processor) processFrames(conn limnet.Conn, frames []lmproto.Frame) {

	p.sameFrames(frames, func(s, e int, frs []lmproto.Frame) {
		// newFs := make([]lmproto.Frame, len(frs))
		// copy(newFs, frs)
		p.frameWorkPool.Submit(func() {
			p.processSameFrame(conn, frs[0].GetFrameType(), frs, s, e)
		})
		// go func(s1, e1 int, c limnet.Conn, fs []lmproto.Frame) {
		// 	p.processSameFrame(c, fs[0].GetFrameType(), fs, s1, e1)
		// }(s, e, conn, frs)

	})

}

func (p *Processor) sameFrames(frames []lmproto.Frame, callback func(s, e int, fs []lmproto.Frame)) {
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

func (p *Processor) processSameFrame(conn limnet.Conn, frameType lmproto.FrameType, frames []lmproto.Frame, s, e int) {
	switch frameType {
	case lmproto.PING: // ping
		p.processPing(conn, frames[0].(*lmproto.PingPacket))
	case lmproto.SEND: // process send
		// TODO: tmpFrames need optimize
		tmpFrames := make([]*lmproto.SendPacket, 0, len(frames))
		for _, frame := range frames {
			tmpFrames = append(tmpFrames, frame.(*lmproto.SendPacket))
		}
		p.processMsgs(conn, tmpFrames)
	case lmproto.SENDACK: // process sendack
		tmpFrames := make([]*lmproto.SendackPacket, 0, len(frames))
		for _, frame := range frames {
			tmpFrames = append(tmpFrames, frame.(*lmproto.SendackPacket))
		}
		p.processMsgAcks(conn, tmpFrames)
	case lmproto.RECVACK: // process recvack
		tmpFrames := make([]*lmproto.RecvackPacket, 0, len(frames))
		for _, frame := range frames {
			tmpFrames = append(tmpFrames, frame.(*lmproto.RecvackPacket))
		}
		p.processRecvacks(conn, tmpFrames)
	}
	conn.Context().(*connContext).finishFrames(len(frames))
}

// #################### conn auth ####################
func (p *Processor) processAuth(conn limnet.Conn, connectPacket *lmproto.ConnectPacket) {
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

	dhServerPrivKey, dhServerPublicKey := limutil.GetCurve25519KeypPair() // 生成服务器的DH密钥对
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

	p.response(conn, &lmproto.ConnackPacket{
		Salt:       aesIV,
		ServerKey:  dhServerPublicKeyEnc,
		ReasonCode: lmproto.ReasonSuccess,
		TimeDiff:   timeDiff,
	})

}

// #################### ping ####################
func (p *Processor) processPing(conn limnet.Conn, pingPacket *lmproto.PingPacket) {
	fmt.Println("ping--->", conn.Fd(), conn.UID())
	p.response(conn, &lmproto.PongPacket{})
}

// #################### messages ####################
func (p *Processor) processMsgs(conn limnet.Conn, msgs []*lmproto.SendPacket) {

	sendackPackets := make([]lmproto.Frame, 0, len(msgs))
	for _, msg := range msgs {
		sendackPackets = append(sendackPackets, &lmproto.SendackPacket{
			ReasonCode:  lmproto.ReasonSuccess,
			ClientSeq:   msg.ClientSeq,
			ClientMsgNo: msg.ClientMsgNo,
		})
	}
	p.response(conn, sendackPackets...)
}

// #################### message ack ####################
func (p *Processor) processMsgAcks(conn limnet.Conn, acks []*lmproto.SendackPacket) {

}

// #################### recv ack ####################
func (p *Processor) processRecvacks(conn limnet.Conn, acks []*lmproto.RecvackPacket) {

}

// #################### process conn close ####################
func (p *Processor) processClose(conn limnet.Conn, err error) {
	p.Debug("conn is close", zap.Error(err), zap.Any("conn", conn))
	if conn.Context() != nil {
		connCtx := conn.Context().(*connContext)
		connCtx.release()
		p.connContextPool.Put(connCtx)
	}
}

// #################### others ####################

func (p *Processor) response(conn limnet.Conn, frames ...lmproto.Frame) {
	p.s.dispatch.dataOut(conn, frames...)
}

func (p *Processor) responseConnackAuthFail(c limnet.Conn) {
	p.responseConnack(c, 0, lmproto.ReasonAuthFail)
}

func (p *Processor) responseConnack(c limnet.Conn, timeDiff int64, code lmproto.ReasonCode) {

	p.response(c, &lmproto.ConnackPacket{
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
	shareKey := limutil.GetCurve25519Key(dhServerPrivKey, dhClientPubKeyArray) // 共享key

	aesIV := limutil.GetRandomString(16)
	aesKey := limutil.MD5(base64.StdEncoding.EncodeToString(shareKey[:]))[:16]
	return aesKey, aesIV, nil
}
