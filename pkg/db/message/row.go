package message

import (
	"hash/crc32"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

var messagePayloadHashTable = crc32.MakeTable(crc32.Castagnoli)

type messageRow struct {
	MessageSeq  uint64
	MessageID   uint64
	FramerFlags uint8
	Setting     uint8
	StreamFlag  uint8
	MsgKey      string
	Expire      uint64
	ClientSeq   uint64
	ClientMsgNo string
	StreamNo    string
	StreamID    uint64
	Timestamp   int64
	ChannelID   string
	ChannelType uint8
	Topic       string
	FromUID     string
	PayloadHash uint64
	PayloadSize uint64
	Payload     []byte
}

func (r messageRow) validate() error {
	if r.MessageID == 0 {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

func normalizeMessageRow(row messageRow) messageRow {
	if row.PayloadHash == 0 && len(row.Payload) > 0 {
		row.PayloadHash = hashPayload(row.Payload)
	}
	if row.PayloadSize == 0 && len(row.Payload) > 0 {
		row.PayloadSize = uint64(len(row.Payload))
	}
	return row
}

func hashPayload(payload []byte) uint64 {
	return uint64(crc32.Checksum(payload, messagePayloadHashTable))
}
