package codec

import (
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/pkg/errors"
)

func decodeSendack(f frame.Frame, data []byte, version uint8) (frame.Frame, error) {
	dec := NewDecoder(data)
	sendackPacket := &frame.SendackPacket{}
	sendackPacket.Framer = f.(frame.Framer)
	var err error

	// messageID
	if sendackPacket.MessageID, err = dec.Int64(); err != nil {
		return nil, errors.Wrap(err, "解码MessageId失败！")
	}
	// clientSeq
	var clientSeq uint32
	if clientSeq, err = dec.Uint32(); err != nil {
		return nil, errors.Wrap(err, "解码ClientSeq失败！")
	}
	sendackPacket.ClientSeq = uint64(clientSeq)

	body, err := dec.BinaryAll()
	if err != nil {
		return nil, errors.Wrap(err, "读取Sendack剩余数据失败！")
	}
	clientMsgNo, messageSeq, reasonCode, err := decodeSendackBody(body, version)
	if err != nil {
		return nil, err
	}
	sendackPacket.ClientMsgNo = clientMsgNo
	sendackPacket.MessageSeq = messageSeq
	sendackPacket.ReasonCode = reasonCode

	return sendackPacket, err
}

func encodeSendack(sendackPacket *frame.SendackPacket, enc *Encoder, version uint8) error {
	// 消息唯一ID
	enc.WriteInt64(sendackPacket.MessageID)
	// clientSeq
	enc.WriteUint32(uint32(sendackPacket.ClientSeq))
	// 消息序列号(客户端维护)
	if err := encodeMessageSeq(enc, version, sendackPacket.MessageSeq); err != nil {
		return err
	}
	// 原因代码
	enc.WriteUint8(sendackPacket.ReasonCode.Byte())
	// clientMsgNo 追加到尾部，保持旧客户端按 core 字段顺序解码仍然正确。
	if sendackPacket.ClientMsgNo != "" {
		enc.WriteString(sendackPacket.ClientMsgNo)
	}
	return nil
}

func encodeSendackSize(packet *frame.SendackPacket, version uint8) int {
	size := frame.MessageIDByteSize +
		frame.ClientSeqByteSize +
		messageSeqSize(version) +
		frame.ReasonCodeByteSize
	if packet.ClientMsgNo != "" {
		size += len(packet.ClientMsgNo) + frame.StringFixLenByteSize
	}
	return size
}

func decodeSendackBody(data []byte, version uint8) (string, uint64, frame.ReasonCode, error) {
	if clientMsgNo, messageSeq, reasonCode, err := decodeSendackBodyCoreFirst(data, version); err == nil {
		return clientMsgNo, messageSeq, reasonCode, nil
	}
	clientMsgNo, messageSeq, reasonCode, err := decodeSendackBodyClientMsgNoFirst(data, version)
	if err != nil {
		return "", 0, 0, errors.Wrap(err, "解码SendackBody失败！")
	}
	return clientMsgNo, messageSeq, reasonCode, nil
}

func decodeSendackBodyCoreFirst(data []byte, version uint8) (string, uint64, frame.ReasonCode, error) {
	dec := NewDecoder(data)
	messageSeq, err := decodeMessageSeq(dec, version)
	if err != nil {
		return "", 0, 0, errors.Wrap(err, "解码MessageSeq失败！")
	}
	reasonCode, err := dec.Uint8()
	if err != nil {
		return "", 0, 0, errors.Wrap(err, "解码ReasonCode失败！")
	}
	clientMsgNo := ""
	if dec.Len() > 0 {
		clientMsgNo, err = dec.String()
		if err != nil {
			return "", 0, 0, errors.Wrap(err, "解码ClientMsgNo失败！")
		}
	}
	if dec.Len() != 0 {
		return "", 0, 0, errors.New("sendack core-first body has unexpected trailing bytes")
	}
	return clientMsgNo, messageSeq, frame.ReasonCode(reasonCode), nil
}

func decodeSendackBodyClientMsgNoFirst(data []byte, version uint8) (string, uint64, frame.ReasonCode, error) {
	dec := NewDecoder(data)
	clientMsgNo, err := dec.String()
	if err != nil {
		return "", 0, 0, errors.Wrap(err, "解码ClientMsgNo失败！")
	}
	messageSeq, err := decodeMessageSeq(dec, version)
	if err != nil {
		return "", 0, 0, errors.Wrap(err, "解码MessageSeq失败！")
	}
	reasonCode, err := dec.Uint8()
	if err != nil {
		return "", 0, 0, errors.Wrap(err, "解码ReasonCode失败！")
	}
	if dec.Len() != 0 {
		return "", 0, 0, errors.New("sendack client-msg-no-first body has unexpected trailing bytes")
	}
	return clientMsgNo, messageSeq, frame.ReasonCode(reasonCode), nil
}
