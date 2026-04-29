package app

import (
	"context"
	"sync"
	"testing"
	"time"

	runtimechannelmeta "github.com/WuKongIM/WuKongIM/internal/runtime/channelmeta"
	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

var _ messageusecase.MetaInvalidator = (*channelMetaSync)(nil)

func TestMemoryGenerationStoreConcurrentAccess(t *testing.T) {
	store := newMemoryGenerationStore()
	keys := []channel.ChannelKey{
		"a",
		"b",
		"c",
		"d",
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				key := keys[(worker+i)%len(keys)]
				require.NoError(t, store.Store(key, uint64(worker+i)))
				_, err := store.Load(key)
				require.NoError(t, err)
			}
		}(worker)
	}
	wg.Wait()
}

func TestChannelMetaSyncInvalidateChannelMetaForwardsToResolver(t *testing.T) {
	id := channel.ChannelID{ID: "g-forward", Type: 2}
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	source := &fakeChannelMetaSource{get: map[channel.ChannelID]metadb.ChannelRuntimeMeta{id: {
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		ChannelEpoch: 1,
		LeaderEpoch:  1,
		Leader:       1,
		Replicas:     []uint64{1},
		ISR:          []uint64{1},
		MinISR:       1,
		Status:       uint8(channel.StatusActive),
		LeaseUntilMS: now.Add(time.Minute).UnixMilli(),
	}}}
	syncer := &channelMetaSync{resolver: runtimechannelmeta.NewSync(runtimechannelmeta.SyncOptions{
		Source:    source,
		Runtime:   &fakeChannelMetaCluster{},
		LocalNode: 1,
		Now:       func() time.Time { return now },
	})}

	_, err := syncer.RefreshChannelMeta(context.Background(), id)
	require.NoError(t, err)
	_, err = syncer.RefreshChannelMeta(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, 1, source.getCalls)

	syncer.InvalidateChannelMeta(id)

	_, err = syncer.RefreshChannelMeta(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, 2, source.getCalls)
}

type fakeChannelMetaSource struct {
	get      map[channel.ChannelID]metadb.ChannelRuntimeMeta
	getCalls int
}

func (f *fakeChannelMetaSource) GetChannelRuntimeMeta(_ context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error) {
	f.getCalls++
	return f.get[channel.ChannelID{ID: channelID, Type: uint8(channelType)}], nil
}

func (f *fakeChannelMetaSource) ListChannelRuntimeMeta(context.Context) ([]metadb.ChannelRuntimeMeta, error) {
	return nil, nil
}

type fakeChannelMetaCluster struct {
	mu             sync.Mutex
	applied        []channel.Meta
	removed        []channel.ChannelKey
	routingApplied []channel.Meta
	runtimeUpserts []channel.Meta
	runtimeRemoved []channel.ChannelKey
	channels       map[channel.ChannelKey]runtimechannelmeta.ChannelObserver
	applyErr       error
	removeErr      error
}

func (f *fakeChannelMetaCluster) ApplyMeta(meta channel.Meta) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, meta)
	return f.applyErr
}

func (f *fakeChannelMetaCluster) RemoveLocal(key channel.ChannelKey) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, key)
	return f.removeErr
}

func (f *fakeChannelMetaCluster) ApplyRoutingMeta(meta channel.Meta) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, meta)
	f.routingApplied = append(f.routingApplied, meta)
	return f.applyErr
}

func (f *fakeChannelMetaCluster) EnsureLocalRuntime(meta channel.Meta) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runtimeUpserts = append(f.runtimeUpserts, meta)
	return f.applyErr
}

func (f *fakeChannelMetaCluster) RemoveLocalRuntime(key channel.ChannelKey) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, key)
	f.runtimeRemoved = append(f.runtimeRemoved, key)
	return f.removeErr
}

func (f *fakeChannelMetaCluster) Channel(key channel.ChannelKey) (runtimechannelmeta.ChannelObserver, bool) {
	if f == nil {
		return nil, false
	}
	ch, ok := f.channels[key]
	return ch, ok
}
