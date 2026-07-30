package cluster

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

func TestNodeReadChannelCommittedClosesStoreOnPostAcquireEarlyError(t *testing.T) {
	tracking := newNodeTrackingStoreFactory(channelstore.NewMemoryFactory())
	node := &Node{channelStoreFactory: tracking}
	node.started.Store(true)

	_, err := node.ReadChannelCommitted(context.Background(), channelruntime.ChannelID{ID: "missing-meta-db", Type: 1}, channelstore.ReadCommittedRequest{})
	if !errors.Is(err, ErrNotStarted) {
		t.Fatalf("ReadChannelCommitted() error = %v, want %v", err, ErrNotStarted)
	}
	if got := tracking.Acquired(); got != 1 {
		t.Fatalf("ChannelStore acquisitions = %d, want 1", got)
	}
	if got := tracking.Closed(); got != 1 {
		t.Fatalf("ChannelStore closes = %d, want 1", got)
	}
}

type nodeTrackingStoreFactory struct {
	base     channelstore.Factory
	acquired atomic.Int64
	closed   atomic.Int64
}

func newNodeTrackingStoreFactory(base channelstore.Factory) *nodeTrackingStoreFactory {
	return &nodeTrackingStoreFactory{base: base}
}

func (f *nodeTrackingStoreFactory) ChannelStore(key channelruntime.ChannelKey, id channelruntime.ChannelID) (channelstore.ChannelStore, error) {
	store, err := f.base.ChannelStore(key, id)
	if err != nil {
		return nil, err
	}
	f.acquired.Add(1)
	return &nodeTrackingChannelStore{ChannelStore: store, parent: f}, nil
}

func (f *nodeTrackingStoreFactory) Acquired() int64 { return f.acquired.Load() }

func (f *nodeTrackingStoreFactory) Closed() int64 { return f.closed.Load() }

type nodeTrackingChannelStore struct {
	channelstore.ChannelStore
	parent    *nodeTrackingStoreFactory
	closeOnce sync.Once
}

func (s *nodeTrackingChannelStore) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.parent.closed.Add(1)
		err = s.ChannelStore.Close()
	})
	return err
}

func (s *nodeTrackingChannelStore) LookupIdempotency(ctx context.Context, fromUID string, clientMsgNo string) (channelstore.IdempotencyHit, bool, error) {
	lookup, ok := s.ChannelStore.(channelstore.IdempotencyLookup)
	if !ok {
		return channelstore.IdempotencyHit{}, false, channelruntime.ErrInvalidConfig
	}
	return lookup.LookupIdempotency(ctx, fromUID, clientMsgNo)
}
