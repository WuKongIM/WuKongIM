package app

import (
	"context"
	"sync"
	"testing"

	runtimechannelmeta "github.com/WuKongIM/WuKongIM/internal/runtime/channelmeta"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

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

type fakeChannelMetaSource struct {
	mu             sync.Mutex
	get            map[channel.ChannelID]metadb.ChannelRuntimeMeta
	list           []metadb.ChannelRuntimeMeta
	getErr         error
	listErr        error
	version        uint64
	getResults     []fakeChannelMetaGetResult
	getCalls       int
	listCalls      int
	getBlock       <-chan struct{}
	getStarted     chan<- struct{}
	getRespectsCtx bool
	lastGetMeta    metadb.ChannelRuntimeMeta
	upsertErr      error
	upserts        []metadb.ChannelRuntimeMeta
}

type fakeChannelMetaGetResult struct {
	meta metadb.ChannelRuntimeMeta
	err  error
}

func (f *fakeChannelMetaSource) GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error) {
	if f.getStarted != nil {
		select {
		case f.getStarted <- struct{}{}:
		default:
		}
	}
	if f.getBlock != nil {
		if f.getRespectsCtx {
			select {
			case <-f.getBlock:
			case <-ctx.Done():
				return metadb.ChannelRuntimeMeta{}, ctx.Err()
			}
		} else {
			<-f.getBlock
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	if f.getCalls <= len(f.getResults) {
		result := f.getResults[f.getCalls-1]
		if result.err != nil {
			return metadb.ChannelRuntimeMeta{}, result.err
		}
		f.lastGetMeta = result.meta
		return result.meta, nil
	}
	if f.getErr != nil {
		return metadb.ChannelRuntimeMeta{}, f.getErr
	}
	meta := f.get[channel.ChannelID{ID: channelID, Type: uint8(channelType)}]
	f.lastGetMeta = meta
	return meta, nil
}

func (f *fakeChannelMetaSource) ListChannelRuntimeMeta(context.Context) ([]metadb.ChannelRuntimeMeta, error) {
	f.mu.Lock()
	f.listCalls++
	f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]metadb.ChannelRuntimeMeta(nil), f.list...), nil
}

func (f *fakeChannelMetaSource) HashSlotTableVersion() uint64 {
	return f.version
}

func (f *fakeChannelMetaSource) UpsertChannelRuntimeMeta(_ context.Context, meta metadb.ChannelRuntimeMeta) error {
	f.upserts = append(f.upserts, meta)
	return f.upsertErr
}

func (f *fakeChannelMetaSource) UpsertChannelRuntimeMetaIfLocalLeader(_ context.Context, meta metadb.ChannelRuntimeMeta) error {
	f.upserts = append(f.upserts, meta)
	return f.upsertErr
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
