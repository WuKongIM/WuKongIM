package channelcompat

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

var (
	ErrInvalidArgument = errors.New("channel: invalid argument")
	ErrClosed          = errors.New("channel: closed")
	ErrCorruptState    = errors.New("channel: corrupt state")
	ErrEmptyState      = errors.New("channel: empty state")
	ErrCorruptValue    = errors.New("channel: corrupt value")
)

const (
	// DurableMessageCodecVersion and DurableMessageHeaderSize define the durable
	// message payload contract shared by log encoding and store-side apply-fetch
	// idempotency reconstruction.
	DurableMessageCodecVersion byte = 1
	DurableMessageHeaderSize        = 45
)

type ChannelKey string

type ChannelID struct {
	ID   string
	Type uint8
}

type Message struct {
	MessageID  uint64
	MessageSeq uint64
	Framer     frame.Framer
	Setting    frame.Setting
	MsgKey     string
	Expire     uint32
	ClientSeq  uint64

	ClientMsgNo string
	StreamNo    string
	StreamID    uint64
	StreamFlag  frame.StreamFlag
	Timestamp   int32
	ChannelID   string
	ChannelType uint8
	Topic       string
	FromUID     string
	// ServerTimestampMS is the server append timestamp in Unix milliseconds.
	ServerTimestampMS int64
	Payload           []byte
}

type Record struct {
	// ID is the message id carried by this channel log entry.
	ID uint64
	// Index is the 1-based channel log index. Fetched records preserve it.
	Index uint64
	// Epoch is the channel epoch that produced this log entry.
	Epoch uint64
	// Payload is the encoded durable message body.
	Payload []byte
	// SizeBytes is the encoded entry size used for batching and fetch budgets.
	SizeBytes int
}

type Checkpoint struct {
	Epoch          uint64
	LogStartOffset uint64
	HW             uint64
}

type EpochPoint struct {
	Epoch       uint64
	StartOffset uint64
}

type IdempotencyKey struct {
	ChannelID   ChannelID
	FromUID     string
	ClientMsgNo string
}

type IdempotencyEntry struct {
	MessageID  uint64
	MessageSeq uint64
	Offset     uint64
}

type ApplyFetchStoreRequest struct {
	PreviousCommittedHW uint64
	Records             []Record
	Checkpoint          *Checkpoint
}

// RetentionState records durable local retention progress for one channel.
type RetentionState struct {
	// LocalRetentionThroughSeq is the authoritative boundary adopted locally.
	LocalRetentionThroughSeq uint64
	// PhysicalRetentionThroughSeq is the highest adopted sequence physically removed locally.
	PhysicalRetentionThroughSeq uint64
	// RetainedMaxSeq is the durable LEO floor preserved after retained rows are removed.
	RetainedMaxSeq uint64
}

type Snapshot struct {
	ChannelKey ChannelKey
	Epoch      uint64
	EndOffset  uint64
	Payload    []byte
}
