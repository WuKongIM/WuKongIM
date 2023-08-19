package transporter

import (
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/bwmarrin/snowflake"
	"go.uber.org/zap"
)

type processor struct {
	opts *Options
	wklog.Log
	connManager  *ConnManager
	messageIDGen *snowflake.Node // 消息ID生成器
	recvDataChan chan Ready
	t            *Transporter
}

func newProcessor(recvDataChan chan Ready, t *Transporter, opts *Options) *processor {
	messageIDGen, err := snowflake.NewNode(int64(opts.NodeID))
	if err != nil {
		panic(err)
	}
	return &processor{
		opts:         opts,
		messageIDGen: messageIDGen,
		Log:          wklog.NewWKLog("processor"),
		connManager:  NewConnManager(),
		recvDataChan: recvDataChan,
		t:            t,
	}
}

// #################### conn auth ####################
func (p *processor) processAuth(conn wknet.Conn, connectPacket *wkproto.ConnectPacket) {

	if strings.TrimSpace(connectPacket.Token) != p.opts.Token {
		p.responseConnackAuthFail(conn)
		return
	}

	// -------------------- get message encrypt key --------------------
	dhServerPrivKey, dhServerPublicKey := wkutil.GetCurve25519KeypPair() // 生成服务器的DH密钥对
	aesKey, aesIV, err := p.getClientAesKeyAndIV(connectPacket.ClientKey, dhServerPrivKey)
	if err != nil {
		p.Error("get client aes key and iv err", zap.Error(err))
		p.responseConnackAuthFail(conn)
		return
	}
	dhServerPublicKeyEnc := base64.StdEncoding.EncodeToString(dhServerPublicKey[:])

	conn.SetUID(connectPacket.UID)
	conn.SetProtoVersion(int(connectPacket.Version))
	conn.SetAuthed(true)
	conn.SetValue(aesKeyKey, aesKey)
	conn.SetValue(aesIVKey, aesIV)
	conn.SetMaxIdle(p.opts.ConnIdleTime)
	p.connManager.AddConn(conn)

	p.Debug("Auth Success", zap.Any("conn", conn))

	p.dataOut(conn, &wkproto.ConnackPacket{
		Salt:       aesIV,
		ServerKey:  dhServerPublicKeyEnc,
		ReasonCode: wkproto.ReasonSuccess,
	})
}

func (p *processor) processFrame(conn wknet.Conn, frame wkproto.Frame) {
	sendPacket, ok := frame.(*wkproto.SendPacket)
	if !ok {
		return
	}
	p.processSendPacket(conn, sendPacket)
}

func (p *processor) processSendPacket(conn wknet.Conn, sendPacket *wkproto.SendPacket) {

	var messageID = p.genMessageID() // generate messageID

	decodePayload, err := p.checkAndDecodePayload(messageID, sendPacket, conn)
	if err != nil {
		p.Error("decode payload err", zap.Error(err))
		p.dataOut(conn, &wkproto.SendackPacket{
			Framer:      sendPacket.Framer,
			ClientSeq:   sendPacket.ClientSeq,
			ClientMsgNo: sendPacket.ClientMsgNo,
			MessageID:   messageID,
			ReasonCode:  wkproto.ReasonPayloadDecodeError,
		})
		return
	}
	if p.recvDataChan != nil {
		go func() {
			resultChan := make(chan []byte)
			req := &CMDReq{}
			err = req.Unmarshal(decodePayload)
			if err != nil {
				p.Error("unmarshal err", zap.Error(err))
				return
			}
			p.recvDataChan <- Ready{
				Req:    req,
				Conn:   conn,
				Result: resultChan,
			}
			select {
			case result := <-resultChan:
				if result != nil {
					err = p.deliveryMsg(conn, sendPacket, messageID, result)
					if err != nil {
						p.Warn("delivery msg err", zap.Error(err))
					}
				}
			case <-p.t.stopped:
				close(resultChan)
				return
			}
		}()

	}
	p.dataOut(conn, &wkproto.SendackPacket{
		Framer:      sendPacket.Framer,
		ClientSeq:   sendPacket.ClientSeq,
		ClientMsgNo: sendPacket.ClientMsgNo,
		MessageID:   messageID,
		ReasonCode:  wkproto.ReasonSuccess,
	})

}

func (p *processor) deliveryMsg(recvConn wknet.Conn, sendPacket *wkproto.SendPacket, messageID int64, payload []byte) error {
	recvPacket := &wkproto.RecvPacket{
		Framer: wkproto.Framer{
			RedDot:    sendPacket.GetRedDot(),
			SyncOnce:  false,
			NoPersist: true,
		},
		Setting:     sendPacket.Setting,
		MessageID:   messageID,
		ClientMsgNo: sendPacket.ClientMsgNo,
		FromUID:     recvConn.UID(),
		ChannelID:   sendPacket.ChannelID,
		ChannelType: sendPacket.ChannelType,
		Topic:       sendPacket.Topic,
		Timestamp:   int32(time.Now().Unix()),
		Payload:     payload,
		ClientSeq:   sendPacket.ClientSeq,
	}

	payloadEnc, err := encryptMessagePayload(recvPacket.Payload, recvConn)
	if err != nil {
		p.Error("加密payload失败！", zap.Error(err))
		return err
	}
	recvPacket.Payload = payloadEnc
	signStr := recvPacket.VerityString()
	msgKey, err := makeMsgKey(signStr, recvConn)
	if err != nil {
		p.Error("生成MsgKey失败！", zap.Error(err))
		return err
	}
	recvPacket.MsgKey = msgKey

	p.dataOut(recvConn, recvPacket)

	return nil
}

// 加密消息
func encryptMessagePayload(payload []byte, conn wknet.Conn) ([]byte, error) {
	var (
		aesKey = conn.Value(aesKeyKey).(string)
		aesIV  = conn.Value(aesIVKey).(string)
	)
	// 加密payload
	payloadEnc, err := wkutil.AesEncryptPkcs7Base64(payload, []byte(aesKey), []byte(aesIV))
	if err != nil {
		return nil, err
	}
	return payloadEnc, nil
}

func makeMsgKey(signStr string, conn wknet.Conn) (string, error) {
	var (
		aesKey = conn.Value(aesKeyKey).(string)
		aesIV  = conn.Value(aesIVKey).(string)
	)
	// 生成MsgKey
	msgKeyBytes, err := wkutil.AesEncryptPkcs7Base64([]byte(signStr), []byte(aesKey), []byte(aesIV))
	if err != nil {
		wklog.Error("生成MsgKey失败！", zap.Error(err))
		return "", err
	}
	return wkutil.MD5(string(msgKeyBytes)), nil
}
func (p *processor) responseConnackAuthFail(c wknet.Conn) {
	p.responseConnack(c, 0, wkproto.ReasonAuthFail)
}

func (p *processor) responseConnack(c wknet.Conn, timeDiff int64, code wkproto.ReasonCode) {

	p.dataOut(c, &wkproto.ConnackPacket{
		ReasonCode: code,
		TimeDiff:   timeDiff,
	})
}

// 生成消息ID
func (p *processor) genMessageID() int64 {
	return p.messageIDGen.Generate().Int64()
}

func (p *processor) dataOut(conn wknet.Conn, frames ...wkproto.Frame) {
	if len(frames) == 0 {
		return
	}
	for _, frame := range frames {
		data, err := p.opts.proto.EncodeFrame(frame, uint8(conn.ProtoVersion()))
		if err != nil {
			p.Warn("Failed to encode the message", zap.Error(err))
		} else {
			_, err = conn.WriteToOutboundBuffer(data)
			if err != nil {
				p.Warn("Failed to write the message", zap.Error(err))
			}

		}
	}
	_ = conn.WakeWrite()
}

// 获取客户端的aesKey和aesIV
// dhServerPrivKey  服务端私钥
func (p *processor) getClientAesKeyAndIV(clientKey string, dhServerPrivKey [32]byte) (string, string, error) {

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

// decode payload
func (p *processor) checkAndDecodePayload(messageID int64, sendPacket *wkproto.SendPacket, c wknet.Conn) ([]byte, error) {
	var (
		aesKey = c.Value(aesKeyKey).(string)
		aesIV  = c.Value(aesIVKey).(string)
	)
	vail, err := p.sendPacketIsVail(sendPacket, c)
	if err != nil {
		return nil, err
	}
	if !vail {
		return nil, errors.New("sendPacket is illegal！")
	}
	// decode payload
	decodePayload, err := wkutil.AesDecryptPkcs7Base64(sendPacket.Payload, []byte(aesKey), []byte(aesIV))
	if err != nil {
		p.Error("Failed to decode payload！", zap.Error(err))
		return nil, err
	}

	return decodePayload, nil
}

// send packet is vail
func (p *processor) sendPacketIsVail(sendPacket *wkproto.SendPacket, c wknet.Conn) (bool, error) {
	var (
		aesKey = c.Value(aesKeyKey).(string)
		aesIV  = c.Value(aesIVKey).(string)
	)
	signStr := sendPacket.VerityString()
	actMsgKey, err := wkutil.AesEncryptPkcs7Base64([]byte(signStr), []byte(aesKey), []byte(aesIV))
	if err != nil {
		p.Error("msgKey is illegal！", zap.Error(err), zap.String("sign", signStr), zap.String("aesKey", aesKey), zap.String("aesIV", aesIV), zap.Any("conn", c))
		return false, err
	}
	actMsgKeyStr := sendPacket.MsgKey
	exceptMsgKey := wkutil.MD5(string(actMsgKey))
	if actMsgKeyStr != exceptMsgKey {
		p.Error("msgKey is illegal！", zap.String("except", exceptMsgKey), zap.String("act", actMsgKeyStr), zap.String("sign", signStr), zap.String("aesKey", aesKey), zap.String("aesIV", aesIV), zap.Any("conn", c))
		return false, errors.New("msgKey is illegal！")
	}
	return true, nil
}
