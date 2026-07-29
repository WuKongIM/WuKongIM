package message

import (
	"context"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type blockingPermissionStore struct {
	started chan struct{}
	release chan struct{}
	channel metadb.Channel
}

func (s *blockingPermissionStore) GetChannelForPermission(context.Context, string, int64) (metadb.Channel, error) {
	close(s.started)
	<-s.release
	return s.channel, nil
}

func (s *blockingPermissionStore) ContainsChannelSubscriber(context.Context, string, int64, string) (bool, error) {
	return false, nil
}

func (s *blockingPermissionStore) HasChannelSubscribers(context.Context, string, int64) (bool, error) {
	return false, nil
}

func TestPermissionCacheRejectsLatePreRestoreLoad(t *testing.T) {
	store := &blockingPermissionStore{
		started: make(chan struct{}),
		release: make(chan struct{}),
		channel: metadb.Channel{
			ChannelID:   "before-restore",
			ChannelType: 2,
		},
	}
	cache := newPermissionCache(store, time.Minute, time.Now).(*permissionCache)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = cache.GetChannelForPermission(context.Background(), "room", 2)
	}()

	<-store.started
	cache.resetAfterRestore()
	close(store.release)
	<-done

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.channels) != 0 {
		t.Fatalf("channels cache size = %d, want late pre-restore load discarded", len(cache.channels))
	}
}
