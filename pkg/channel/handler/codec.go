package handler

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/fnv"
	"io"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type messageView struct {
	Message     channel.Message
	PayloadHash uint64
}

var errUnknownMessageCodecVersion = errors.New("channelhandler: unknown message codec version")

const (
	framerFlagNoPersist uint8 = 1 << iota
	framerFlagRedDot
	framerFlagSyncOnce
	framerFlagDUP
	framerFlagHasServerVersion
	framerFlagEnd
)

func encodeMessage(message channel.Message) ([]byte, error) {
	return encodeMessageWithPayloadHash(message, hashPayload(message.Payload))
}

func encodeMessageWithPayloadHash(message channel.Message, payloadHash uint64) ([]byte, error) {
	var buf bytes.Buffer
	if err := buf.WriteByte(channel.DurableMessageCodecVersion); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.BigEndian, message.MessageID); err != nil {
		return nil, err
	}
	if err := buf.WriteByte(encodeFramerFlags(message.Framer)); err != nil {
		return nil, err
	}
	if err := buf.WriteByte(byte(message.Setting)); err != nil {
		return nil, err
	}
	if err := buf.WriteByte(byte(message.StreamFlag)); err != nil {
		return nil, err
	}
	if err := buf.WriteByte(message.ChannelType); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.BigEndian, message.Expire); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.BigEndian, message.ClientSeq); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.BigEndian, message.StreamID); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.BigEndian, message.Timestamp); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.BigEndian, payloadHash); err != nil {
		return nil, err
	}
	if err := writeString(&buf, message.MsgKey); err != nil {
		return nil, err
	}
	if err := writeString(&buf, message.ClientMsgNo); err != nil {
		return nil, err
	}
	if err := writeString(&buf, message.StreamNo); err != nil {
		return nil, err
	}
	if err := writeString(&buf, message.ChannelID); err != nil {
		return nil, err
	}
	if err := writeString(&buf, message.Topic); err != nil {
		return nil, err
	}
	if err := writeString(&buf, message.FromUID); err != nil {
		return nil, err
	}
	if err := writeBytes(&buf, message.Payload); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeMessage(payload []byte) (channel.Message, error) {
	view, err := decodeMessageView(payload)
	if err != nil {
		return channel.Message{}, err
	}
	msg := view.Message
	msg.Payload = append([]byte(nil), view.Message.Payload...)
	return msg, nil
}

func decodeMessageView(payload []byte) (messageView, error) {
	if len(payload) < channel.DurableMessageHeaderSize {
		return messageView{}, io.ErrUnexpectedEOF
	}
	if payload[0] != channel.DurableMessageCodecVersion {
		return messageView{}, errUnknownMessageCodecVersion
	}

	msg := channel.Message{
		MessageID:   binary.BigEndian.Uint64(payload[1:9]),
		Framer:      decodeFramerFlags(payload[9]),
		Setting:     frame.Setting(payload[10]),
		StreamFlag:  frame.StreamFlag(payload[11]),
		ChannelType: payload[12],
		Expire:      binary.BigEndian.Uint32(payload[13:17]),
		ClientSeq:   binary.BigEndian.Uint64(payload[17:25]),
		StreamID:    binary.BigEndian.Uint64(payload[25:33]),
		Timestamp:   int32(binary.BigEndian.Uint32(payload[33:37])),
	}
	view := messageView{Message: msg, PayloadHash: binary.BigEndian.Uint64(payload[37:45])}

	pos := channel.DurableMessageHeaderSize
	msgKey, nextPos, err := readSizedBytesView(payload, pos)
	if err != nil {
		return messageView{}, err
	}
	clientMsgNo, nextPos, err := readSizedBytesView(payload, nextPos)
	if err != nil {
		return messageView{}, err
	}
	streamNo, nextPos, err := readSizedBytesView(payload, nextPos)
	if err != nil {
		return messageView{}, err
	}
	channelID, nextPos, err := readSizedBytesView(payload, nextPos)
	if err != nil {
		return messageView{}, err
	}
	topic, nextPos, err := readSizedBytesView(payload, nextPos)
	if err != nil {
		return messageView{}, err
	}
	fromUID, nextPos, err := readSizedBytesView(payload, nextPos)
	if err != nil {
		return messageView{}, err
	}
	body, _, err := readSizedBytesView(payload, nextPos)
	if err != nil {
		return messageView{}, err
	}
	view.Message.MsgKey = string(msgKey)
	view.Message.ChannelID = string(channelID)
	view.Message.ClientMsgNo = string(clientMsgNo)
	view.Message.StreamNo = string(streamNo)
	view.Message.Topic = string(topic)
	view.Message.FromUID = string(fromUID)
	view.Message.Payload = body
	return view, nil
}

func decodeMessageRecord(record store.LogRecord) (channel.Message, error) {
	view, err := decodeMessageView(record.Payload)
	if err != nil {
		return channel.Message{}, err
	}
	msg := view.Message
	msg.MessageSeq = record.Offset + 1
	return msg, nil
}

func readSizedBytesView(payload []byte, pos int) ([]byte, int, error) {
	if len(payload)-pos < 4 {
		return nil, pos, io.ErrUnexpectedEOF
	}
	size := int(binary.BigEndian.Uint32(payload[pos : pos+4]))
	pos += 4
	if len(payload)-pos < size {
		return nil, pos, io.ErrUnexpectedEOF
	}
	return payload[pos : pos+size], pos + size, nil
}

func hashPayload(payload []byte) uint64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write(payload)
	return hasher.Sum64()
}

func writeString(buf *bytes.Buffer, value string) error {
	return writeBytes(buf, []byte(value))
}

func writeBytes(buf *bytes.Buffer, value []byte) error {
	if err := binary.Write(buf, binary.BigEndian, uint32(len(value))); err != nil {
		return err
	}
	_, err := buf.Write(value)
	return err
}

func encodeFramerFlags(framer frame.Framer) uint8 {
	var flags uint8
	if framer.NoPersist {
		flags |= framerFlagNoPersist
	}
	if framer.RedDot {
		flags |= framerFlagRedDot
	}
	if framer.SyncOnce {
		flags |= framerFlagSyncOnce
	}
	if framer.DUP {
		flags |= framerFlagDUP
	}
	if framer.HasServerVersion {
		flags |= framerFlagHasServerVersion
	}
	if framer.End {
		flags |= framerFlagEnd
	}
	return flags
}

func decodeFramerFlags(flags uint8) frame.Framer {
	return frame.Framer{
		NoPersist:        flags&framerFlagNoPersist != 0,
		RedDot:           flags&framerFlagRedDot != 0,
		SyncOnce:         flags&framerFlagSyncOnce != 0,
		DUP:              flags&framerFlagDUP != 0,
		HasServerVersion: flags&framerFlagHasServerVersion != 0,
		End:              flags&framerFlagEnd != 0,
	}
}
