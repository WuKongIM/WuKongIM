package cluster

import (
	"context"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ChannelMigrationStoreNode exposes the channel runtime migration store owned by cluster.
type ChannelMigrationStoreNode interface {
	ChannelMigrationStore() *channels.MigrationStore
}

// NewChannelMigrationStore adapts the cluster channel runtime migration store to manager usecases.
func NewChannelMigrationStore(node ChannelMigrationStoreNode) managementusecase.ChannelMigrationStore {
	if node == nil {
		return nil
	}
	return lazyChannelMigrationStore{node: node}
}

type lazyChannelMigrationStore struct {
	node ChannelMigrationStoreNode
}

func (s lazyChannelMigrationStore) CreateLeaderTransfer(ctx context.Context, req channels.CreateLeaderTransferRequest) (metadb.ChannelMigrationTask, error) {
	store, err := s.store()
	if err != nil {
		return metadb.ChannelMigrationTask{}, err
	}
	return store.CreateLeaderTransfer(ctx, req)
}

func (s lazyChannelMigrationStore) CreateReplicaReplace(ctx context.Context, req channels.CreateReplicaReplaceRequest) (metadb.ChannelMigrationTask, error) {
	store, err := s.store()
	if err != nil {
		return metadb.ChannelMigrationTask{}, err
	}
	return store.CreateReplicaReplace(ctx, req)
}

func (s lazyChannelMigrationStore) GetActive(ctx context.Context, id channelruntime.ChannelID) (metadb.ChannelMigrationTask, bool, error) {
	store, err := s.store()
	if err != nil {
		return metadb.ChannelMigrationTask{}, false, err
	}
	return store.GetActive(ctx, id)
}

func (s lazyChannelMigrationStore) Get(ctx context.Context, id channelruntime.ChannelID, taskID string) (metadb.ChannelMigrationTask, bool, error) {
	store, err := s.store()
	if err != nil {
		return metadb.ChannelMigrationTask{}, false, err
	}
	return store.Get(ctx, id, taskID)
}

func (s lazyChannelMigrationStore) ListActive(ctx context.Context, id channelruntime.ChannelID, limit int) ([]metadb.ChannelMigrationTask, error) {
	store, err := s.store()
	if err != nil {
		return nil, err
	}
	return store.ListActive(ctx, id, limit)
}

func (s lazyChannelMigrationStore) Abort(ctx context.Context, task metadb.ChannelMigrationTask, reason string) error {
	store, err := s.store()
	if err != nil {
		return err
	}
	return store.Abort(ctx, task, reason)
}

func (s lazyChannelMigrationStore) store() (*channels.MigrationStore, error) {
	if s.node == nil {
		return nil, managementusecase.ErrChannelMigrationUnavailable
	}
	store := s.node.ChannelMigrationStore()
	if store == nil {
		return nil, managementusecase.ErrChannelMigrationUnavailable
	}
	return store, nil
}
