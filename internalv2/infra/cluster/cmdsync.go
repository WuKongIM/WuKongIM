package cluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/cmdsync"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// CMDSyncNode exposes clusterv2 reads and writes needed by CMD sync.
type CMDSyncNode interface {
	ListConversationActivePage(context.Context, metadb.ConversationKind, string, metadb.ConversationActiveCursor, int) ([]metadb.ConversationState, metadb.ConversationActiveCursor, bool, error)
	UpsertConversationStatesBatch(context.Context, []metadb.ConversationState) error
	ReadChannelCommitted(context.Context, channelv2.ChannelID, channelstore.ReadCommittedRequest) (channelstore.ReadCommittedResult, error)
}

// CMDSyncStore adapts clusterv2 unified conversation projection rows to CMD sync.
type CMDSyncStore struct {
	node CMDSyncNode
}

var _ cmdsync.StateStore = (*CMDSyncStore)(nil)
var _ cmdsync.MessageStore = (*CMDSyncStore)(nil)

// NewCMDSyncStore creates a clusterv2-backed CMD sync store.
func NewCMDSyncStore(node CMDSyncNode) *CMDSyncStore {
	return &CMDSyncStore{node: node}
}

// ListConversationActiveView reads the UID-owned CMD projection view.
func (s *CMDSyncStore) ListConversationActiveView(ctx context.Context, uid string, limit int) ([]metadb.ConversationState, error) {
	if s == nil || s.node == nil {
		return nil, metadb.ErrNotFound
	}
	rows, _, _, err := s.node.ListConversationActivePage(ctx, metadb.ConversationKindCMD, uid, metadb.ConversationActiveCursor{}, limit)
	if err != nil {
		return nil, err
	}
	return cloneConversationStates(rows), nil
}

// UpsertConversationStates advances CMD-kind read state in the unified projection.
func (s *CMDSyncStore) UpsertConversationStates(ctx context.Context, states []metadb.ConversationState) error {
	if len(states) == 0 {
		return nil
	}
	if s == nil || s.node == nil {
		return metadb.ErrNotFound
	}
	cloned := cloneConversationStates(states)
	for i := range cloned {
		cloned[i].Kind = metadb.ConversationKindCMD
	}
	return s.node.UpsertConversationStatesBatch(ctx, cloned)
}

// LoadCommandMessages reads committed messages from one command-channel log.
func (s *CMDSyncStore) LoadCommandMessages(ctx context.Context, key cmdsync.CommandChannelKey, fromSeq uint64, limit int) ([]cmdsync.SyncedMessage, error) {
	if s == nil || s.node == nil {
		return nil, metadb.ErrNotFound
	}
	if limit <= 0 {
		limit = 1
	}
	if fromSeq == 0 {
		fromSeq = 1
	}
	read, err := s.node.ReadChannelCommitted(ctx, channelv2.ChannelID{ID: key.ChannelID, Type: key.ChannelType}, channelstore.ReadCommittedRequest{
		FromSeq:  fromSeq,
		MaxSeq:   maxUint64(),
		Limit:    limit,
		MaxBytes: maxInt(),
	})
	if err != nil {
		return nil, mapAppendError(err)
	}
	return cmdSyncedMessagesFromChannel(read.Messages), nil
}

func cmdSyncedMessagesFromChannel(in []channelv2.Message) []cmdsync.SyncedMessage {
	out := make([]cmdsync.SyncedMessage, 0, len(in))
	for _, msg := range in {
		out = append(out, cmdsync.SyncedMessage{
			MessageID:         msg.MessageID,
			MessageSeq:        msg.MessageSeq,
			ChannelID:         msg.ChannelID,
			ChannelType:       msg.ChannelType,
			FromUID:           msg.FromUID,
			ClientMsgNo:       msg.ClientMsgNo,
			ServerTimestampMS: msg.ServerTimestampMS,
			Payload:           append([]byte(nil), msg.Payload...),
		})
	}
	return out
}
