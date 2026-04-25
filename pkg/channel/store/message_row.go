package store

import (
	"hash/fnv"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	messageRowFramerFlagNoPersist uint8 = 1 << iota
	messageRowFramerFlagRedDot
	messageRowFramerFlagSyncOnce
	messageRowFramerFlagDUP
	messageRowFramerFlagHasServerVersion
	messageRowFramerFlagEnd
)

// messageRow is the structured form of one persisted message record.
type messageRow struct {
	MessageSeq  uint64
	MessageID   uint64
	FramerFlags uint8
	Setting     uint8
	StreamFlag  uint8
	MsgKey      string
	Expire      uint32
	ClientSeq   uint64
	ClientMsgNo string
	StreamNo    string
	StreamID    uint64
	Timestamp   int32
	ChannelID   string
	ChannelType uint8
	Topic       string
	FromUID     string
	Payload     []byte
	PayloadHash uint64
}

// messageRowFromChannelMessage projects a channel message into the structured row model.
func messageRowFromChannelMessage(msg channel.Message) messageRow {
	return messageRow{
		MessageSeq:  msg.MessageSeq,
		MessageID:   msg.MessageID,
		FramerFlags: encodeMessageRowFramerFlags(msg.Framer),
		Setting:     uint8(msg.Setting),
		StreamFlag:  uint8(msg.StreamFlag),
		MsgKey:      msg.MsgKey,
		Expire:      msg.Expire,
		ClientSeq:   msg.ClientSeq,
		ClientMsgNo: msg.ClientMsgNo,
		StreamNo:    msg.StreamNo,
		StreamID:    msg.StreamID,
		Timestamp:   msg.Timestamp,
		ChannelID:   msg.ChannelID,
		ChannelType: msg.ChannelType,
		Topic:       msg.Topic,
		FromUID:     msg.FromUID,
		Payload:     append([]byte(nil), msg.Payload...),
		PayloadHash: hashMessagePayload(msg.Payload),
	}
}

// toChannelMessage rebuilds the handler-facing message value from a structured row.
func (r messageRow) toChannelMessage() channel.Message {
	return channel.Message{
		MessageID:   r.MessageID,
		MessageSeq:  r.MessageSeq,
		Framer:      decodeMessageRowFramerFlags(r.FramerFlags),
		Setting:     frame.Setting(r.Setting),
		MsgKey:      r.MsgKey,
		Expire:      r.Expire,
		ClientSeq:   r.ClientSeq,
		ClientMsgNo: r.ClientMsgNo,
		StreamNo:    r.StreamNo,
		StreamID:    r.StreamID,
		StreamFlag:  frame.StreamFlag(r.StreamFlag),
		Timestamp:   r.Timestamp,
		ChannelID:   r.ChannelID,
		ChannelType: r.ChannelType,
		Topic:       r.Topic,
		FromUID:     r.FromUID,
		Payload:     append([]byte(nil), r.Payload...),
	}
}

func (r messageRow) validate() error {
	if r.MessageID == 0 {
		return channel.ErrInvalidArgument
	}
	return nil
}

func encodeMessageRowFramerFlags(framer frame.Framer) uint8 {
	var flags uint8
	if framer.NoPersist {
		flags |= messageRowFramerFlagNoPersist
	}
	if framer.RedDot {
		flags |= messageRowFramerFlagRedDot
	}
	if framer.SyncOnce {
		flags |= messageRowFramerFlagSyncOnce
	}
	if framer.DUP {
		flags |= messageRowFramerFlagDUP
	}
	if framer.HasServerVersion {
		flags |= messageRowFramerFlagHasServerVersion
	}
	if framer.End {
		flags |= messageRowFramerFlagEnd
	}
	return flags
}

func decodeMessageRowFramerFlags(flags uint8) frame.Framer {
	return frame.Framer{
		NoPersist:        flags&messageRowFramerFlagNoPersist != 0,
		RedDot:           flags&messageRowFramerFlagRedDot != 0,
		SyncOnce:         flags&messageRowFramerFlagSyncOnce != 0,
		DUP:              flags&messageRowFramerFlagDUP != 0,
		HasServerVersion: flags&messageRowFramerFlagHasServerVersion != 0,
		End:              flags&messageRowFramerFlagEnd != 0,
	}
}

func hashMessagePayload(payload []byte) uint64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write(payload)
	return hasher.Sum64()
}
