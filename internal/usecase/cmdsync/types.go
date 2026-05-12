package cmdsync

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
)

var (
	// ErrUIDRequired reports a missing user id in CMD sync commands.
	ErrUIDRequired = errors.New("usecase/cmdsync: uid required")
	// ErrStateStoreRequired reports a missing durable CMD state dependency.
	ErrStateStoreRequired = errors.New("usecase/cmdsync: state store required")
	// ErrMessageStoreRequired reports a missing command-channel message dependency.
	ErrMessageStoreRequired = errors.New("usecase/cmdsync: message store required")
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

// StateStore persists UID-owned durable CMD sync state.
type StateStore interface {
	ListCMDConversationActive(ctx context.Context, uid string, limit int) ([]metadb.CMDConversationState, error)
	AdvanceCMDConversationReadSeq(ctx context.Context, patches []metadb.CMDConversationReadPatch) error
}

// MessageStore loads authoritative messages from command-channel logs.
type MessageStore interface {
	LoadCommandMessages(ctx context.Context, key CommandChannelKey, fromSeq uint64, limit int) ([]channel.Message, error)
}
