package cmdsync

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	// ErrUIDRequired reports a missing user id in CMD sync commands.
	ErrUIDRequired = errors.New("usecase/cmdsync: uid required")
	// ErrStateStoreRequired reports a missing durable CMD state dependency.
	ErrStateStoreRequired = errors.New("usecase/cmdsync: state store required")
	// ErrMessageStoreRequired reports a missing command-channel message dependency.
	ErrMessageStoreRequired = errors.New("usecase/cmdsync: message store required")
	// ErrIntentRequired reports a missing or malformed pending conversation intent.
	ErrIntentRequired = errors.New("usecase/cmdsync: conversation intent required")
	// ErrConversationIntentStaleOwner reports an intent routed to a stale UID owner.
	ErrConversationIntentStaleOwner = errors.New("usecase/cmdsync: conversation intent stale owner")
)

// SyncQuery is the legacy /message/sync request after access-layer mapping.
type SyncQuery struct {
	// UID identifies the user whose durable CMD messages are synced.
	UID string
	// MessageSeq is accepted for compatibility but is not used as selection state.
	MessageSeq uint64
	// Limit is the maximum number of CMD messages to return.
	Limit int
}

// SyncAckCommand is the legacy /message/syncack request after validation.
type SyncAckCommand struct {
	// UID identifies the user acknowledging the latest CMD sync generation.
	UID string
	// LastMessageSeq is accepted for compatibility but does not select channels.
	LastMessageSeq uint64
}

// SyncResult contains durable CMD messages ready for legacy response mapping.
type SyncResult struct {
	// Messages contains client-facing messages with one command suffix stripped.
	Messages []channel.Message
}

// CommandChannelKey identifies one durable command channel log.
type CommandChannelKey struct {
	// ChannelID is the command-channel id, e.g. source____cmd.
	ChannelID string
	// ChannelType is the command-channel type.
	ChannelType uint8
}

// PendingStateStore persists flushed CMD conversation state.
type PendingStateStore interface {
	UpsertCMDConversationStates(ctx context.Context, states []metadb.CMDConversationState) error
}

// StateStore persists UID-owned durable CMD sync state. P2d-d adds the upsert
// method so syncack can create read progress for pending-only CMD conversations
// before removing pending entries.
type StateStore interface {
	ListCMDConversationActive(ctx context.Context, uid string, limit int) ([]metadb.CMDConversationState, error)
	AdvanceCMDConversationReadSeq(ctx context.Context, patches []metadb.CMDConversationReadPatch) error
	UpsertCMDConversationStates(ctx context.Context, states []metadb.CMDConversationState) error
}

// ConversationPendingStore provides owner-local pending overlays to sync/ack.
type ConversationPendingStore interface {
	ListPending(ctx context.Context, uid string, limit int) []PendingConversationView
	// MarkSynced removes a pending overlay only after durable read progress is persisted.
	MarkSynced(ctx context.Context, uid string, key CommandChannelKey, throughSeq uint64) error
}

// MessageStore loads authoritative messages from command-channel logs.
type MessageStore interface {
	LoadCommandMessages(ctx context.Context, key CommandChannelKey, fromSeq uint64, limit int) ([]channel.Message, error)
}
