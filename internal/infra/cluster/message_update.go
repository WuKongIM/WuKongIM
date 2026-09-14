package cluster

import (
	"context"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// MessageUpdateNode is the cluster capability required by payload editing.
type MessageUpdateNode interface {
	ApplyMessageUpdate(context.Context, metadb.MessageUpdateMutation) (metadb.MessageUpdateMutationResult, error)
	ReadMessageUpdatesBatch(context.Context, []metadb.MessageUpdateRead) ([]metadb.MessageUpdatePage, error)
	GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error)
}

// MessageUpdateStore maps the narrow usecase port to cluster metadata reads.
type MessageUpdateStore struct{ node MessageUpdateNode }

func NewMessageUpdateStore(node MessageUpdateNode) *MessageUpdateStore {
	return &MessageUpdateStore{node: node}
}

var _ message.UpdateStore = (*MessageUpdateStore)(nil)

func (s *MessageUpdateStore) ApplyMessageUpdate(ctx context.Context, q metadb.MessageUpdateMutation) (metadb.MessageUpdateMutationResult, error) {
	return s.node.ApplyMessageUpdate(ctx, q)
}
func (s *MessageUpdateStore) ReadMessageUpdatesBatch(ctx context.Context, q []metadb.MessageUpdateRead) ([]metadb.MessageUpdatePage, error) {
	return s.node.ReadMessageUpdatesBatch(ctx, q)
}
func (s *MessageUpdateStore) GetChannelRuntimeMeta(ctx context.Context, id string, typ int64) (metadb.ChannelRuntimeMeta, error) {
	return s.node.GetChannelRuntimeMeta(ctx, id, typ)
}
