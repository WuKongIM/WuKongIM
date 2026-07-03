package reactor

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

func TestLookupCommittedMessageUsesStoreWorkerWithoutBlockingReactor(t *testing.T) {
	factory := newBlockingLookupFactory()
	g, err := NewGroup(Config{LocalNode: 1, ReactorCount: 1, MailboxSize: 16, Store: factory})
	require.NoError(t, err)
	defer g.Close()
	defer factory.UnblockLookups()

	meta := testMeta("lookup-async", 1, 1)
	seedCommittedMessage(t, factory, meta, 101, "lookup")
	require.NoError(t, awaitSubmit(g, meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta}))

	lookupFuture, err := g.Submit(context.Background(), meta.Key, Event{
		Kind:      EventLookupCommittedMessage,
		Key:       meta.Key,
		Context:   context.Background(),
		MessageID: 101,
	})
	require.NoError(t, err)
	factory.waitLookupStarted(t)

	metaFuture, err := g.Submit(context.Background(), meta.Key, Event{Kind: EventApplyMeta, Key: meta.Key, Meta: meta})
	require.NoError(t, err)
	metaCtx, metaCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer metaCancel()
	_, err = metaFuture.Await(metaCtx)
	require.NoError(t, err)

	factory.UnblockLookups()
	result, err := lookupFuture.Await(context.Background())
	require.NoError(t, err)
	require.True(t, result.LookupFound)
	require.Equal(t, uint64(101), result.LookupMessage.MessageID)
	require.Equal(t, uint64(1), result.LookupMessage.MessageSeq)
}

type blockingLookupFactory struct {
	base    *store.MemoryFactory
	started chan struct{}
	unblock chan struct{}
}

func newBlockingLookupFactory() *blockingLookupFactory {
	return &blockingLookupFactory{
		base:    store.NewMemoryFactory(),
		started: make(chan struct{}, 8),
		unblock: make(chan struct{}),
	}
}

func (f *blockingLookupFactory) ChannelStore(key ch.ChannelKey, id ch.ChannelID) (store.ChannelStore, error) {
	base, err := f.base.ChannelStore(key, id)
	if err != nil {
		return nil, err
	}
	return &blockingLookupStore{ChannelStore: base, parent: f}, nil
}

func (f *blockingLookupFactory) waitLookupStarted(t *testing.T) {
	t.Helper()
	select {
	case <-f.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for LookupMessageByID to start")
	}
}

func (f *blockingLookupFactory) UnblockLookups() {
	select {
	case <-f.unblock:
	default:
		close(f.unblock)
	}
}

type blockingLookupStore struct {
	store.ChannelStore
	parent *blockingLookupFactory
}

func (s *blockingLookupStore) LookupMessageByID(ctx context.Context, messageID uint64) (ch.Message, bool, error) {
	select {
	case s.parent.started <- struct{}{}:
	default:
	}
	select {
	case <-s.parent.unblock:
	case <-ctx.Done():
		return ch.Message{}, false, ctx.Err()
	}
	lookup, ok := s.ChannelStore.(store.MessageLookup)
	if !ok {
		return ch.Message{}, false, ch.ErrInvalidConfig
	}
	return lookup.LookupMessageByID(ctx, messageID)
}
