package message

import "github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"

const (
	fnv64aOffset = 14695981039346656037
	fnv64aPrime  = 1099511628211
)

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
	hash := uint64(fnv64aOffset)
	for _, b := range payload {
		hash ^= uint64(b)
		hash *= fnv64aPrime
	}
	return hash
}
