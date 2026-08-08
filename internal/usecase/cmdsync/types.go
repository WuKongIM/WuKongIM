package cmdsync

import (
	"context"
	"errors"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	// ErrUIDRequired reports a missing user id in CMD sync commands.
	ErrUIDRequired = errors.New("internal/usecase/cmdsync: uid required")
	// ErrChannelRequired reports a missing source channel id in CMD binding commands.
	ErrChannelRequired = errors.New("internal/usecase/cmdsync: channel id required")
	// ErrChannelTypeRequired reports a missing source channel type in CMD binding commands.
	ErrChannelTypeRequired = errors.New("internal/usecase/cmdsync: channel type required")
	// ErrSequenceExhausted reports a command channel whose tail cannot produce a new start sequence.
	ErrSequenceExhausted = errors.New("internal/usecase/cmdsync: command channel sequence exhausted")
	// ErrStateStoreRequired reports a missing durable CMD state dependency.
	ErrStateStoreRequired = errors.New("internal/usecase/cmdsync: state store required")
	// ErrMessageStoreRequired reports a missing command-channel message dependency.
	ErrMessageStoreRequired = errors.New("internal/usecase/cmdsync: message store required")
	// ErrChannelDisbanded rejects CMD pulls from a terminal source channel.
	ErrChannelDisbanded = errors.New("internal/usecase/cmdsync: channel disbanded")
	// ErrStateCursorDidNotAdvance prevents an infinite scan when a state store
	// reports another page without advancing its stable cursor.
	ErrStateCursorDidNotAdvance = errors.New("internal/usecase/cmdsync: state cursor did not advance")
)

// SyncQuery is the /message/sync request after access-layer validation.
type SyncQuery struct {
	// UID identifies the user whose durable CMD messages are synced.
	UID string
	// MessageSeq is accepted for legacy compatibility but does not select state.
	MessageSeq uint64
	// Limit bounds the number of CMD messages returned.
	Limit int
}

// SyncAckCommand is the /message/syncack request after access-layer validation.
type SyncAckCommand struct {
	// UID identifies the user acknowledging the latest CMD sync generation.
	UID string
	// LastMessageSeq is accepted for legacy compatibility but does not select channels.
	LastMessageSeq uint64
}

// BindCommand creates or restores durable CMD discovery for one UID and source channel.
type BindCommand struct {
	UID         string
	ChannelID   string
	ChannelType uint8
}

// UnbindCommand tombstones durable CMD discovery for one UID and source channel.
type UnbindCommand struct {
	UID         string
	ChannelID   string
	ChannelType uint8
}

// SyncedMessage is a command-channel message returned by CMD sync.
type SyncedMessage struct {
	// MessageID is the globally unique durable message identifier.
	MessageID uint64
	// MessageSeq is the committed command-channel sequence.
	MessageSeq uint64
	// ChannelID is the client-facing source channel after suffix stripping.
	ChannelID string
	// ChannelType is the command-channel type.
	ChannelType uint8
	// FromUID identifies the sender user id.
	FromUID string
	// ClientMsgNo is the client idempotency key.
	ClientMsgNo string
	// ServerTimestampMS is the server append timestamp used for deterministic ordering.
	ServerTimestampMS int64
	// SyncOnce reports whether this message is an explicit one-shot command-sync entry.
	SyncOnce bool
	// Payload is the immutable message payload returned to the access adapter.
	Payload []byte
}

// SyncResult contains durable CMD messages ready for response mapping.
type SyncResult struct {
	// Messages contains client-facing messages with one command suffix stripped.
	Messages []SyncedMessage
}

// CommandChannelKey identifies one durable command channel log.
type CommandChannelKey struct {
	// ChannelID is the durable command-channel id, e.g. source____cmd.
	ChannelID string
	// ChannelType is the command-channel type.
	ChannelType uint8
}

// StateStore supplies the UID-owned CMD directory and persists acknowledgements.
type StateStore interface {
	ListUserCMDChannelMembershipPage(ctx context.Context, uid string, after metadb.UserCMDChannelMembershipCursor, limit int) ([]metadb.UserCMDChannelMembership, metadb.UserCMDChannelMembershipCursor, bool, error)
	UpsertUserCMDChannelMemberships(ctx context.Context, memberships []metadb.UserCMDChannelMembership) error
	AdvanceUserCMDChannelMembershipAcks(ctx context.Context, memberships []metadb.UserCMDChannelMembership) error
	TombstoneUserCMDChannelMemberships(ctx context.Context, memberships []metadb.UserCMDChannelMembership) error
}

// MessageStore loads authoritative messages from command-channel logs.
type MessageStore interface {
	CommandChannelTail(ctx context.Context, key CommandChannelKey) (uint64, error)
	LoadCommandMessages(ctx context.Context, key CommandChannelKey, fromSeq uint64, limit int) ([]SyncedMessage, error)
}

// Options configures the CMD sync usecase.
type Options struct {
	// States supplies CMD directory rows and persists acknowledgement progress.
	States StateStore
	// Messages loads command-channel messages.
	Messages MessageStore
	// Records stores the latest unacknowledged sync generation per UID.
	Records *SyncRecordCache
	// Now supplies wall-clock time for deterministic tests.
	Now func() time.Time
	// ActiveScanLimit bounds each CMD membership page; Sync continues until the
	// stable UID directory is exhausted while retaining only the result limit.
	ActiveScanLimit int
	// DefaultLimit is used when SyncQuery.Limit is not positive.
	DefaultLimit int
	// MaxLimit caps SyncQuery.Limit and record retention per generation.
	MaxLimit int
}
