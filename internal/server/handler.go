package server

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/bwmarrin/snowflake"
	"go.uber.org/zap"
)

type PacketHandler struct {
	s *Server
	wklog.Log
	messageIDGen *snowflake.Node // 消息ID生成器
}

// NewPacketHandler 创建处理者
func NewPacketHandler(s *Server) *PacketHandler {
	h := &PacketHandler{
		s:   s,
		Log: wklog.NewWKLog("Handler"),
	}
	var err error
	h.messageIDGen, err = snowflake.NewNode(int64(s.opts.NodeID))
	if err != nil {
		panic(err)
	}
	return h
}

func (s *PacketHandler) handleConnect2(c conn, connectPacket *wkproto.ConnectPacket) {
	if strings.TrimSpace(connectPacket.ClientKey) == "" {
		s.writeConnackAuthFail(c)
		return
	}

	c.SetVersion(connectPacket.Version)

	dhServerPrivKey, dhServerPublicKey := wkutil.GetCurve25519KeypPair() // 生成服务器的DH密钥对
	aesKey, aesIV, err := s.getClientAesKeyAndIV(connectPacket.ClientKey, dhServerPrivKey)
	if err != nil {
		s.Error("获取客户端的aesKey和aesIV失败！", zap.Error(err))
		s.writeConnackAuthFail(c)
		return
	}
	dhServerPublicKeyEnc := base64.StdEncoding.EncodeToString(dhServerPublicKey[:])
	timeDiff := time.Now().UnixNano()/1000/1000 - connectPacket.ClientTimestamp

	// 创建一个客户端
	cli := s.s.clientManager.GetFromPool()
	cli.uid = connectPacket.UID
	cli.deviceID = connectPacket.DeviceID
	cli.deviceFlag = connectPacket.DeviceFlag
	cli.deviceLevel = 1
	cli.conn = c
	cli.aesKey = aesKey
	cli.aesIV = aesIV
	cli.s = s.s
	cli.Start()

	s.s.clientManager.Add(cli)

	data, _ := s.s.opts.Proto.EncodeFrame(&wkproto.ConnackPacket{
		Salt:       aesIV,
		ServerKey:  dhServerPublicKeyEnc,
		ReasonCode: wkproto.ReasonSuccess,
		TimeDiff:   timeDiff,
	}, c.Version())

	c.Write(data)
}

// // 处理连接包
// func (s *PacketHandler) handleConnect(c *limContext) {
// 	c.c.SetAuthed(true)

// 	connectPacket := c.frame.(*wkproto.ConnectPacket)

// 	if strings.TrimSpace(connectPacket.ClientKey) == "" {
// 		s.writeConnackError(c.c, wkproto.ReasonClientKeyIsEmpty)
// 		return
// 	}

// 	c.c.SetVersion(connectPacket.Version)

// 	dhServerPrivKey, dhServerPublicKey := wkutil.GetCurve25519KeypPair() // 生成服务器的DH密钥对
// 	aesKey, aesIV, err := s.getClientAesKeyAndIV(connectPacket.ClientKey, dhServerPrivKey)
// 	if err != nil {
// 		s.Error("获取客户端的aesKey和aesIV失败！", zap.Error(err))
// 		s.writeConnackAuthFail(c.c)
// 		return
// 	}
// 	dhServerPublicKeyEnc := base64.StdEncoding.EncodeToString(dhServerPublicKey[:])
// 	timeDiff := time.Now().UnixNano()/1000/1000 - connectPacket.ClientTimestamp

// 	// 创建一个客户端
// 	cli := s.s.clientManager.GetFromPool()
// 	cli.uid = connectPacket.UID
// 	cli.deviceID = connectPacket.DeviceID
// 	cli.deviceFlag = connectPacket.DeviceFlag
// 	cli.deviceLevel = 1
// 	cli.conn = c.c
// 	cli.aesKey = aesKey
// 	cli.aesIV = aesIV
// 	cli.s = s.s
// 	cli.Start()

// 	s.s.clientManager.Add(cli)

// 	c.writePacket(&wkproto.ConnackPacket{
// 		Salt:       aesIV,
// 		ServerKey:  dhServerPublicKeyEnc,
// 		ReasonCode: wkproto.ReasonSuccess,
// 		TimeDiff:   timeDiff,
// 	})
// }

// 处理发送包
func (s *PacketHandler) handleSend(c *limContext) {

	cli := c.Client()
	if cli == nil {
		s.Warn("客户端不存在！")
		return
	}

	sendPackets := c.Frames()
	if len(sendPackets) == 0 {
		return
	}

	sendackPackets := make([]wkproto.Frame, 0, len(sendPackets))

	if !cli.Allow() {
		s.Warn("消息发送过快，限流处理！", zap.String("uid", cli.uid))
		for _, sendPacketFrame := range sendPackets {
			sendPacket := sendPacketFrame.(*wkproto.SendPacket)
			sendackPackets = append(sendackPackets, &wkproto.SendackPacket{
				ReasonCode:  wkproto.ReasonRateLimit,
				ClientSeq:   sendPacket.ClientSeq,
				ClientMsgNo: sendPacket.ClientMsgNo,
			})
		}
		c.writePacket(sendackPackets...)
		return
	}
	for _, sendPacketFrame := range sendPackets {
		sendPacket := sendPacketFrame.(*wkproto.SendPacket)
		sendackPackets = append(sendackPackets, &wkproto.SendackPacket{
			ReasonCode:  wkproto.ReasonSuccess,
			ClientSeq:   sendPacket.ClientSeq,
			ClientMsgNo: sendPacket.ClientMsgNo,
		})
		// fmt.Println("sendPacket.ClientSeq---->", sendPacket.ClientSeq)
	}

	c.writePacket(sendackPackets...)
}

// 处理收到消息回执
func (s *PacketHandler) handleRecvack(c *limContext) {
}

// 处理ping包
func (s *PacketHandler) handlePing(c *limContext) {
	fmt.Println("handlePing....")
	cli := c.Client()
	if cli != nil {
		data, _ := s.s.opts.Proto.EncodeFrame(&wkproto.PongPacket{}, cli.Version())

		dataLen := int64(len(data))
		cli.inMsgs.Inc()
		cli.inBytes.Add(dataLen)
		s.s.inMsgs.Inc()
		s.s.inBytes.Add(dataLen)

		cli.outMsgs.Inc()
		cli.outBytes.Add(dataLen)
		s.s.outMsgs.Inc()
		s.s.outMsgs.Add(dataLen)

		err := cli.Write(data)
		if err != nil {
			s.Error("write pong is fail", zap.Error(err))
		}
	}

}

func (s *PacketHandler) writeConnackError(c conn, resaon wkproto.ReasonCode) error {
	return s.writeConnack(c, 0, resaon)
}

func (s *PacketHandler) writeConnackAuthFail(c conn) error {
	return s.writeConnack(c, 0, wkproto.ReasonAuthFail)
}

func (s *PacketHandler) writeConnack(c conn, timeDiff int64, code wkproto.ReasonCode) error {
	data, err := s.s.opts.Proto.EncodeFrame(&wkproto.ConnackPacket{
		ReasonCode: code,
		TimeDiff:   timeDiff,
	}, c.Version())
	if err != nil {
		return err
	}

	c.Write(data)
	return nil
}

// 获取客户端的aesKey和aesIV
// dhServerPrivKey  服务端私钥
func (s *PacketHandler) getClientAesKeyAndIV(clientKey string, dhServerPrivKey [32]byte) (string, string, error) {

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
