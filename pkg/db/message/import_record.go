package message

import (
	"bytes"

	"github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// EncodeMessageRecord produces an owned durable record without assigning new
// message identities or timestamps. Importers can use the ordinary exact-append
// API with this record instead of duplicating the private storage encoding.
func EncodeMessageRecord(msg channelcompat.Message, epoch uint64) (channelcompat.Record, error) {
	row := messageRow{
		MessageSeq: msg.MessageSeq, MessageID: msg.MessageID,
		FramerFlags: messageFramerFlags(msg.Framer), Setting: uint8(msg.Setting),
		StreamFlag: uint8(msg.StreamFlag), MsgKey: msg.MsgKey, Expire: uint64(msg.Expire),
		ClientSeq: msg.ClientSeq, ClientMsgNo: msg.ClientMsgNo, StreamNo: msg.StreamNo,
		StreamID: msg.StreamID, Timestamp: int64(msg.Timestamp), ServerTimestampMS: msg.ServerTimestampMS,
		ChannelID: msg.ChannelID, ChannelType: msg.ChannelType, Topic: msg.Topic,
		FromUID: msg.FromUID, Payload: msg.Payload, PublicationMetadata: msg.PublicationMetadata,
	}
	record, err := compatibilityRecordFromRow(row)
	record.Epoch = epoch
	return record, err
}

// DecodeMessageRecord validates the envelope and canonical durable payload,
// then returns an independently owned message with all persisted fields.
func DecodeMessageRecord(record channelcompat.Record) (channelcompat.Message, error) {
	row, err := decodeCompatibilityRecordPayload(record.Payload)
	if err != nil {
		return channelcompat.Message{}, err
	}
	if record.ID != row.MessageID || row.PayloadHash != hashPayload(row.Payload) {
		return channelcompat.Message{}, channelcompat.ErrCorruptValue
	}
	row.MessageSeq = record.Index
	canonical, err := compatibilityRecordFromRow(row)
	if err != nil {
		return channelcompat.Message{}, err
	}
	if !bytes.Equal(canonical.Payload, record.Payload) {
		return channelcompat.Message{}, channelcompat.ErrCorruptValue
	}
	return channelMessageFromRow(row), nil
}

func messageFramerFlags(f frame.Framer) uint8 {
	var flags uint8
	for index, set := range [...]bool{f.NoPersist, f.RedDot, f.SyncOnce, f.DUP, f.HasServerVersion, f.End} {
		if set {
			flags |= 1 << index
		}
	}
	return flags
}
