package node

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func appendChannelMessage(dst []byte, msg channel.Message) []byte {
	dst = appendUvarint(dst, msg.MessageID)
	dst = appendUvarint(dst, msg.MessageSeq)
	dst = appendFrameFramer(dst, msg.Framer)
	dst = append(dst, byte(msg.Setting))
	dst = appendString(dst, msg.MsgKey)
	dst = appendUvarint(dst, uint64(msg.Expire))
	dst = appendUvarint(dst, msg.ClientSeq)
	dst = appendString(dst, msg.ClientMsgNo)
	dst = appendString(dst, msg.StreamNo)
	dst = appendUvarint(dst, msg.StreamID)
	dst = append(dst, byte(msg.StreamFlag))
	dst = appendNodeVarint(dst, int64(msg.Timestamp))
	dst = appendString(dst, msg.ChannelID)
	dst = append(dst, msg.ChannelType)
	dst = appendString(dst, msg.Topic)
	dst = appendString(dst, msg.FromUID)
	dst = appendOptionalBytes(dst, msg.Payload)
	return dst
}

func readChannelMessage(body []byte, offset int) (channel.Message, int, error) {
	var msg channel.Message
	var err error
	if msg.MessageID, offset, err = readUvarint(body, offset); err != nil {
		return channel.Message{}, offset, err
	}
	if msg.MessageSeq, offset, err = readUvarint(body, offset); err != nil {
		return channel.Message{}, offset, err
	}
	if msg.Framer, offset, err = readFrameFramer(body, offset); err != nil {
		return channel.Message{}, offset, err
	}
	if msg.Setting, offset, err = readFrameSetting(body, offset); err != nil {
		return channel.Message{}, offset, err
	}
	if msg.MsgKey, offset, err = readString(body, offset); err != nil {
		return channel.Message{}, offset, err
	}
	if msg.Expire, offset, err = readUint32(body, offset, "message expire"); err != nil {
		return channel.Message{}, offset, err
	}
	if msg.ClientSeq, offset, err = readUvarint(body, offset); err != nil {
		return channel.Message{}, offset, err
	}
	if msg.ClientMsgNo, offset, err = readString(body, offset); err != nil {
		return channel.Message{}, offset, err
	}
	if msg.StreamNo, offset, err = readString(body, offset); err != nil {
		return channel.Message{}, offset, err
	}
	if msg.StreamID, offset, err = readUvarint(body, offset); err != nil {
		return channel.Message{}, offset, err
	}
	if msg.StreamFlag, offset, err = readStreamFlag(body, offset); err != nil {
		return channel.Message{}, offset, err
	}
	if msg.Timestamp, offset, err = readInt32(body, offset, "message timestamp"); err != nil {
		return channel.Message{}, offset, err
	}
	if msg.ChannelID, offset, err = readString(body, offset); err != nil {
		return channel.Message{}, offset, err
	}
	if msg.ChannelType, offset, err = readByte(body, offset, "message channel type"); err != nil {
		return channel.Message{}, offset, err
	}
	if msg.Topic, offset, err = readString(body, offset); err != nil {
		return channel.Message{}, offset, err
	}
	if msg.FromUID, offset, err = readString(body, offset); err != nil {
		return channel.Message{}, offset, err
	}
	if msg.Payload, offset, err = readOptionalBytes(body, offset, "message payload"); err != nil {
		return channel.Message{}, offset, err
	}
	return msg, offset, nil
}

func appendFrameFramer(dst []byte, framer frame.Framer) []byte {
	dst = append(dst, byte(framer.FrameType))
	dst = appendUvarint(dst, uint64(framer.RemainingLength))
	dst = appendNodeBool(dst, framer.NoPersist)
	dst = appendNodeBool(dst, framer.RedDot)
	dst = appendNodeBool(dst, framer.SyncOnce)
	dst = appendNodeBool(dst, framer.DUP)
	dst = appendNodeBool(dst, framer.HasServerVersion)
	dst = appendNodeBool(dst, framer.End)
	dst = appendNodeVarint(dst, framer.FrameSize)
	return dst
}

func readFrameFramer(body []byte, offset int) (frame.Framer, int, error) {
	var framer frame.Framer
	var err error
	var frameType byte
	if frameType, offset, err = readByte(body, offset, "message frame type"); err != nil {
		return frame.Framer{}, offset, err
	}
	framer.FrameType = frame.FrameType(frameType)
	if framer.RemainingLength, offset, err = readUint32(body, offset, "message remaining length"); err != nil {
		return frame.Framer{}, offset, err
	}
	if framer.NoPersist, offset, err = readNodeBool(body, offset); err != nil {
		return frame.Framer{}, offset, err
	}
	if framer.RedDot, offset, err = readNodeBool(body, offset); err != nil {
		return frame.Framer{}, offset, err
	}
	if framer.SyncOnce, offset, err = readNodeBool(body, offset); err != nil {
		return frame.Framer{}, offset, err
	}
	if framer.DUP, offset, err = readNodeBool(body, offset); err != nil {
		return frame.Framer{}, offset, err
	}
	if framer.HasServerVersion, offset, err = readNodeBool(body, offset); err != nil {
		return frame.Framer{}, offset, err
	}
	if framer.End, offset, err = readNodeBool(body, offset); err != nil {
		return frame.Framer{}, offset, err
	}
	if framer.FrameSize, offset, err = readNodeVarint(body, offset); err != nil {
		return frame.Framer{}, offset, err
	}
	return framer, offset, nil
}

func appendOptionalBytes(dst []byte, value []byte) []byte {
	if value == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	return appendBytes(dst, value)
}

func readOptionalBytes(body []byte, offset int, label string) ([]byte, int, error) {
	marker, next, err := readNodeMarker(body, offset, label)
	if err != nil || marker == 0 {
		return nil, next, err
	}
	return readBytes(body, next)
}

func readByte(body []byte, offset int, label string) (byte, int, error) {
	if offset >= len(body) {
		return 0, offset, fmt.Errorf("access/node: short %s", label)
	}
	return body[offset], offset + 1, nil
}

func readFrameSetting(body []byte, offset int) (frame.Setting, int, error) {
	value, next, err := readByte(body, offset, "message setting")
	return frame.Setting(value), next, err
}

func readStreamFlag(body []byte, offset int) (frame.StreamFlag, int, error) {
	value, next, err := readByte(body, offset, "message stream flag")
	return frame.StreamFlag(value), next, err
}

func readUint32(body []byte, offset int, label string) (uint32, int, error) {
	value, next, err := readUvarint(body, offset)
	if err != nil {
		return 0, offset, err
	}
	if value > uint64(^uint32(0)) {
		return 0, offset, fmt.Errorf("access/node: %s overflows uint32", label)
	}
	return uint32(value), next, nil
}

func readInt32(body []byte, offset int, label string) (int32, int, error) {
	value, next, err := readNodeVarint(body, offset)
	if err != nil {
		return 0, offset, err
	}
	if value < -1<<31 || value > 1<<31-1 {
		return 0, offset, fmt.Errorf("access/node: %s overflows int32", label)
	}
	return int32(value), next, nil
}

func appendNodeInt(dst []byte, value int) []byte {
	return appendNodeVarint(dst, int64(value))
}

func readNodeInt(body []byte, offset int, label string) (int, int, error) {
	value, next, err := readNodeVarint(body, offset)
	if err != nil {
		return 0, offset, err
	}
	maxInt := int64(^uint(0) >> 1)
	minInt := -maxInt - 1
	if value < minInt || value > maxInt {
		return 0, offset, fmt.Errorf("access/node: %s overflows int", label)
	}
	return int(value), next, nil
}
