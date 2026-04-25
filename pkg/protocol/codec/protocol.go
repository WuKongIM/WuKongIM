package codec

import (
	"bytes"
	"fmt"
	"io"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/pkg/errors"
)

var (
	// 长度不够
	errDecodeLength = errors.New("decode length error")
)

// Protocol Protocol
type Protocol interface {
	// DecodeFrame 解码消息 返回frame 和 数据大小 和 error
	DecodeFrame(data []byte, version uint8) (frame.Frame, int, error)
	// EncodeFrame 编码消息
	EncodeFrame(packet frame.Frame, version uint8) ([]byte, error)

	// WriteFrame 编码报文，并写入writer
	WriteFrame(w Writer, packet frame.Frame, version uint8) error
}

// WKroto 悟空IM协议对象
type WKProto struct {
	sync.RWMutex
}

// New 创建wukong协议对象
func New() *WKProto {
	return &WKProto{}
}

// PacketDecodeFunc 包解码函数
type PacketDecodeFunc func(f frame.Frame, remainingBytes []byte, version uint8) (frame.Frame, error)

// PacketEncodeFunc 包编码函数
type PacketEncodeFunc func(f frame.Frame, version uint8) ([]byte, error)

var packetDecodeMap = map[frame.FrameType]PacketDecodeFunc{
	frame.CONNECT:    decodeConnect,
	frame.CONNACK:    decodeConnack,
	frame.SEND:       decodeSend,
	frame.SENDACK:    decodeSendack,
	frame.RECV:       decodeRecv,
	frame.RECVACK:    decodeRecvack,
	frame.DISCONNECT: decodeDisConnect,
	frame.SUB:        decodeSub,
	frame.SUBACK:     decodeSuback,
	frame.EVENT:      decodeEvent,
}

// var packetEncodeMap = map[PacketType]PacketEncodeFunc{
// 	CONNECT:    encodeConnect,
// 	CONNACK:    encodeConnack,
// 	SEND:       encodeSend,
// 	SENDACK:    encodeSendack,
// 	RECV:       encodeRecv,
// 	RECVACK:    encodeRecvack,
// 	DISCONNECT: encodeDisConnect,
// }

// DecodePacketWithConn 解码包
func (l *WKProto) DecodePacketWithConn(conn io.Reader, version uint8) (frame.Frame, error) {
	framer, err := l.decodeFramerWithConn(conn)
	if err != nil {
		return nil, err
	}
	// l.Debug("解码消息！", zap.String("framer", framer.String()))
	if framer.GetFrameType() == frame.PING {
		return &frame.PingPacket{}, nil
	}
	if framer.GetFrameType() == frame.PONG {
		return &frame.PongPacket{}, nil
	}

	if framer.RemainingLength > MaxRemaingLength {
		return nil, errors.New(fmt.Sprintf("消息超出最大限制[%d]！", MaxRemaingLength))
		// panic(errors.New(fmt.Sprintf("消息超出最大限制[%d]！", MaxRemaingLength)))
	}

	body := make([]byte, framer.RemainingLength)
	_, err = io.ReadFull(conn, body)
	if err != nil {
		return nil, err
	}
	decodeFunc := packetDecodeMap[framer.GetFrameType()]
	if decodeFunc == nil {
		return nil, errors.New(fmt.Sprintf("不支持对[%s]包的解码！", framer.GetFrameType()))
	}

	f, err := decodeFunc(framer, body, version)
	if err != nil {
		return nil, errors.Wrap(err, fmt.Sprintf("解码包[%s]失败！", framer.GetFrameType()))
	}
	return f, nil
}

// DecodePacket 解码包
func (l *WKProto) DecodeFrame(data []byte, version uint8) (frame.Frame, int, error) {
	framer, remainingLengthLength, err := l.decodeFramer(data)
	if err != nil {
		return nil, 0, nil
	}
	frameType := framer.GetFrameType()
	if frameType == frame.UNKNOWN {
		return nil, 0, nil
	}
	if frameType == frame.PING {
		return &frame.PingPacket{
			Framer: framer,
		}, 1, nil
	}
	if frameType == frame.PONG {
		return &frame.PongPacket{
			Framer: framer,
		}, 1, nil
	}

	if framer.RemainingLength > MaxRemaingLength {
		return nil, 0, fmt.Errorf("消息超出最大限制[%d]！", MaxRemaingLength)
	}
	msgLen := int(framer.RemainingLength) + 1 + remainingLengthLength
	if len(data) < msgLen {
		return nil, 0, nil
	}
	body := data[1+remainingLengthLength : msgLen]
	decodeFunc := packetDecodeMap[frameType]
	if decodeFunc == nil {
		return nil, 0, errors.New(fmt.Sprintf("不支持对[%s]包的解码！", frameType))
	}

	f, err := decodeFunc(framer, body, version)
	if err != nil {
		return nil, 0, errors.Wrap(err, fmt.Sprintf("解码包[%s]失败！", frameType))
	}
	return f, 1 + remainingLengthLength + int(framer.RemainingLength), nil
}

// EncodePacket 编码包
func (l *WKProto) EncodeFrame(f frame.Frame, version uint8) ([]byte, error) {
	buffer := bytes.NewBuffer([]byte{})
	err := l.encodeFrameWithWriter(buffer, f, version)
	if err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// encodeFrameWithWriter 编码包
func (l *WKProto) encodeFrameWithWriter(w Writer, f frame.Frame, version uint8) error {
	frameType := f.GetFrameType()

	enc := NewEncoderBuffer(w)
	defer enc.End()

	if frameType == frame.PING || frameType == frame.PONG {
		_ = enc.WriteByte(byte(int(frameType) << 4))
		return nil
	}

	var err error
	switch frameType {
	case frame.CONNECT:
		packet := f.(*frame.ConnectPacket)
		l.encodeFrame(packet, enc, uint32(encodeConnectSize(packet, version)))
		err = encodeConnect(packet, enc, version)
	case frame.CONNACK:
		packet := f.(*frame.ConnackPacket)
		l.encodeFrame(packet, enc, uint32(encodeConnackSize(packet, version)))
		err = encodeConnack(packet, enc, version)
	case frame.SEND:
		packet := f.(*frame.SendPacket)
		if len(packet.Payload) > PayloadMaxSize {
			return errors.New(fmt.Sprintf("消息负载超出最大限制[%d]！", PayloadMaxSize))
		}
		l.encodeFrame(packet, enc, uint32(encodeSendSize(packet, version)))
		err = encodeSend(packet, enc, version)
	case frame.SENDACK:
		packet := f.(*frame.SendackPacket)
		l.encodeFrame(packet, enc, uint32(encodeSendackSize(packet, version)))
		err = encodeSendack(packet, enc, version)
	case frame.RECV:
		packet := f.(*frame.RecvPacket)
		l.encodeFrame(packet, enc, uint32(encodeRecvSize(packet, version)))
		err = encodeRecv(packet, enc, version)
	case frame.RECVACK:
		packet := f.(*frame.RecvackPacket)
		l.encodeFrame(packet, enc, uint32(encodeRecvackSize(packet, version)))
		err = encodeRecvack(packet, enc, version)
	case frame.DISCONNECT:
		packet := f.(*frame.DisconnectPacket)
		l.encodeFrame(packet, enc, uint32(encodeDisConnectSize(packet, version)))
		err = encodeDisConnect(packet, enc, version)
	case frame.SUB:
		packet := f.(*frame.SubPacket)
		l.encodeFrame(packet, enc, uint32(encodeSubSize(packet, version)))
		err = encodeSub(packet, enc, version)
	case frame.SUBACK:
		packet := f.(*frame.SubackPacket)
		l.encodeFrame(packet, enc, uint32(encodeSubackSize(packet, version)))
		err = encodeSuback(packet, enc, version)
	case frame.EVENT:
		packet := f.(*frame.EventPacket)
		l.encodeFrame(packet, enc, uint32(encodeEventSize(packet, version)))
		err = encodeEvent(packet, enc, version)
	}
	if err != nil {
		return err
	}
	return nil
}

func (l *WKProto) WriteFrame(w Writer, packet frame.Frame, version uint8) error {

	return l.encodeFrameWithWriter(w, packet, version)
}

func (l *WKProto) encodeFrame(f frame.Frame, enc *Encoder, remainingLength uint32) {

	_ = enc.WriteByte(ToFixHeaderUint8(f))

	encodeVariable2(remainingLength, enc)
}

// func (l *WKProto) encodeFramer(f Frame, remainingLength uint32) ([]byte, error) {

// 	if f.GetFrameType() == PING || f.GetFrameType() == PONG {
// 		return []byte{byte(int(f.GetFrameType() << 4))}, nil
// 	}

// 	header := []byte{ToFixHeaderUint8(f)}

// 	if f.GetFrameType() == SEND {
// 		return []byte{1}, nil
// 	}

// 	varHeader := encodeVariable(remainingLength)

//		return append(header, varHeader...), nil
//	}
func (l *WKProto) decodeFramer(data []byte) (frame.Framer, int, error) {
	typeAndFlags := data[0]
	p := FramerFromUint8(typeAndFlags)
	var remainingLengthLength uint32 = 0 // 剩余长度的长度
	var err error
	if p.FrameType != frame.PING && p.FrameType != frame.PONG {
		p.RemainingLength, remainingLengthLength, err = decodeLength(data[1:])
		if err != nil {
			if errors.Is(err, errDecodeLength) {
				return frame.Framer{}, 0, nil
			}
			return frame.Framer{}, 0, err
		}
	}
	p.FrameSize = int64(len(data))
	return p, int(remainingLengthLength), nil
}

func (l *WKProto) decodeFramerWithConn(conn io.Reader) (frame.Framer, error) {
	b := make([]byte, 1)
	_, err := io.ReadFull(conn, b)
	if err != nil {
		return frame.Framer{}, err
	}
	typeAndFlags := b[0]
	p := FramerFromUint8(typeAndFlags)
	if p.FrameType != frame.PING && p.FrameType != frame.PONG {
		p.RemainingLength = uint32(decodeLengthWithConn(conn))
	}
	return p, nil
}

func encodeVariable(size uint32) []byte {
	ret := make([]byte, 0, 10)
	for size > 0 {
		digit := byte(size % 0x80)
		size /= 0x80
		if size > 0 {
			digit |= 0x80
		}
		ret = append(ret, digit)
	}
	return ret
}

func encodeVariable2(size uint32, enc *Encoder) {
	// ret := make([]byte, 0, 10)
	for size > 0 {
		digit := byte(size % 0x80)
		size /= 0x80
		if size > 0 {
			digit |= 0x80
		}
		_ = enc.WriteByte(digit)
	}
}
func decodeLength(data []byte) (uint32, uint32, error) {
	var rLength uint32
	var multiplier uint32
	offset := 0
	for multiplier < 27 { //fix: Infinite '(digit & 128) == 1' will cause the dead loop
		if offset >= len(data) {
			return 0, 0, errDecodeLength
		}
		digit := data[offset]
		rLength |= uint32(digit&127) << multiplier
		if (digit & 128) == 0 {
			break
		}
		multiplier += 7
		offset++
	}
	return rLength, uint32(offset + 1), nil
}
func decodeLengthWithConn(r io.Reader) int {
	var rLength uint32
	var multiplier uint32
	for multiplier < 27 { //fix: Infinite '(digit & 128) == 1' will cause the dead loop
		b := make([]byte, 1)
		_, _ = io.ReadFull(r, b)
		digit := b[0]
		rLength |= uint32(digit&127) << multiplier
		if (digit & 128) == 0 {
			break
		}
		multiplier += 7
	}
	return int(rLength)
}

func encodeBool(b bool) (i int) {
	if b {
		i = 1
	}
	return
}
