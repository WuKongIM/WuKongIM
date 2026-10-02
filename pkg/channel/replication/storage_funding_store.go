package replication

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

// storageFundingStore is separate from ReplicaStore.Sync: its receipt cannot vote.
type storageFundingStore interface {
	storageProtected(context.Context, LoadRequest, uint64) (bool, error)
	prepareStorage(context.Context, Mutation, uint64, bool) (channelstore.MQTTStoragePreparation, error)
}

func (a *storeAdapter) storageProtected(ctx context.Context, q LoadRequest, through uint64) (bool, error) {
	capability, ok := a.cfg.Factory.(interface{ MQTTStorageEnabled() bool })
	if !ok || !capability.MQTTStorageEnabled() {
		return false, nil
	}
	store, err := a.cfg.Factory.ChannelStore(q.ChannelKey, q.ChannelID)
	if err != nil {
		return false, err
	}
	defer store.Close()
	reader, ok := store.(interface {
		MQTTStorageProtection(context.Context) (bool, error)
	})
	if !ok {
		return false, ch.ErrInvalidConfig
	}
	found, err := reader.MQTTStorageProtection(ctx)
	return found, err
}
func (a *storeAdapter) prepareStorage(ctx context.Context, m Mutation, nonce uint64, cancel bool) (channelstore.MQTTStoragePreparation, error) {
	if !validMutation(m) {
		return channelstore.MQTTStoragePreparation{}, ch.ErrInvalidConfig
	}
	store, err := a.cfg.Factory.ChannelStore(m.ChannelKey, m.ChannelID)
	if err != nil {
		return channelstore.MQTTStoragePreparation{}, err
	}
	defer store.Close()
	port, ok := store.(channelstore.MQTTStoragePreparer)
	if !ok {
		return channelstore.MQTTStoragePreparation{}, ch.ErrInvalidConfig
	}
	return port.PrepareMQTTStorage(ctx, m.Manifest, m.Records, m.Committed, nonce, cancel)
}
