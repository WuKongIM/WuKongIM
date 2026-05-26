package handler

import (
	"encoding/binary"
	"errors"
	"io"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	store "github.com/WuKongIM/WuKongIM/pkg/db/message"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	fnv64aOffset = 14695981039346656037
	fnv64aPrime  = 1099511628211
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
	size, err := encodedMessageSize(message)
	if err != nil {
		return nil, err
	}
	payload := make([]byte, 0, size)
	payload = append(payload, channel.DurableMessageCodecVersion)
	payload = binary.BigEndian.AppendUint64(payload, message.MessageID)
	payload = append(payload, encodeFramerFlags(message.Framer), byte(message.Setting), byte(message.StreamFlag), message.ChannelType)
	payload = binary.BigEndian.AppendUint32(payload, message.Expire)
	payload = binary.BigEndian.AppendUint64(payload, message.ClientSeq)
	payload = binary.BigEndian.AppendUint64(payload, message.StreamID)
	payload = binary.BigEndian.AppendUint32(payload, uint32(message.Timestamp))
	payload = binary.BigEndian.AppendUint64(payload, payloadHash)
	payload = appendSizedString(payload, message.MsgKey)
	payload = appendSizedString(payload, message.ClientMsgNo)
	payload = appendSizedString(payload, message.StreamNo)
	payload = appendSizedString(payload, message.ChannelID)
	payload = appendSizedString(payload, message.Topic)
	payload = appendSizedString(payload, message.FromUID)
	payload = appendSizedBytes(payload, message.Payload)
	return payload, nil
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
	hash := uint64(fnv64aOffset)
	for _, b := range payload {
		hash ^= uint64(b)
		hash *= fnv64aPrime
	}
	return hash
}

func encodedMessageSize(message channel.Message) (int, error) {
	size := channel.DurableMessageHeaderSize
	for _, fieldSize := range [...]int{
		len(message.MsgKey),
		len(message.ClientMsgNo),
		len(message.StreamNo),
		len(message.ChannelID),
		len(message.Topic),
		len(message.FromUID),
		len(message.Payload),
	} {
		if fieldSize > int(^uint32(0)) {
			return 0, channel.ErrInvalidArgument
		}
		size += 4 + fieldSize
	}
	return size, nil
}

func appendSizedString(dst []byte, value string) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func appendSizedBytes(dst []byte, value []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
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
