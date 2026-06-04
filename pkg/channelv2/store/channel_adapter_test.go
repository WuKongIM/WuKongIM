package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/stretchr/testify/require"
)

func TestMessageDBStoreAdapterContract(t *testing.T) {
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	testStoreContract(t, factory)
	testStoreCheckpointHWMonotonic(t, factory)
}

func TestMessageDBStoreAdapterCheckpointPreservesExistingFields(t *testing.T) {
	factory := NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() { _ = factory.Close() })
	cs, err := factory.ChannelStore(ch.ChannelKey("1:preserve"), ch.ChannelID{ID: "preserve", Type: 1})
	require.NoError(t, err)
	adapter := cs.(*messageDBChannelStoreAdapter)
	require.NoError(t, adapter.store.StoreCheckpoint(channel.Checkpoint{Epoch: 7, LogStartOffset: 2, HW: 5}))

	require.NoError(t, adapter.StoreCheckpoint(context.Background(), ch.Checkpoint{HW: 3}))
	current, err := adapter.store.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, channel.Checkpoint{Epoch: 7, LogStartOffset: 2, HW: 5}, current)

	require.NoError(t, adapter.StoreCheckpoint(context.Background(), ch.Checkpoint{HW: 8}))
	current, err = adapter.store.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, channel.Checkpoint{Epoch: 7, LogStartOffset: 2, HW: 8}, current)
}

func TestMessageDBFactoryOptionsDoesNotExposeCommitNoSync(t *testing.T) {
	_, ok := reflect.TypeOf(MessageDBFactoryOptions{}).FieldByName("CommitNoSync")
	require.False(t, ok)
}

func TestNewMessageDBFactoryWithOptionsConfiguresCommitCoordinatorTuning(t *testing.T) {
	factory := NewMessageDBFactoryWithOptions(t.TempDir(), MessageDBFactoryOptions{
		CommitFlushWindow: 500 * time.Microsecond,
		CommitMaxRequests: 32,
		CommitMaxRecords:  512,
		CommitMaxBytes:    256 * 1024,
	})
	t.Cleanup(func() { _ = factory.Close() })

	require.NotNil(t, factory.engine)
	cfg := factory.engine.CommitCoordinatorConfig()
	require.Equal(t, 500*time.Microsecond, cfg.FlushWindow)
	require.Equal(t, 32, cfg.MaxRequests)
	require.Equal(t, 512, cfg.MaxRecords)
	require.Equal(t, 256*1024, cfg.MaxBytes)
}

func TestStoreApplyFetchRecordsPrefersTrustedPath(t *testing.T) {
	store := &trustedApplyFetchRecorder{leo: 7}

	leo, err := storeApplyFetchRecords(store, channel.ApplyFetchStoreRequest{
		Records: []channel.Record{{ID: 1, Payload: []byte("a"), SizeBytes: 1}},
	})
	require.NoError(t, err)
	require.Equal(t, uint64(7), leo)
	require.Equal(t, 1, store.trustedCalls)
	require.Zero(t, store.strictCalls)
}

type trustedApplyFetchRecorder struct {
	leo          uint64
	strictCalls  int
	trustedCalls int
}

func (s *trustedApplyFetchRecorder) StoreApplyFetch(channel.ApplyFetchStoreRequest) (uint64, error) {
	s.strictCalls++
	return 0, errors.New("strict path should not be used")
}

func (s *trustedApplyFetchRecorder) StoreApplyFetchTrusted(channel.ApplyFetchStoreRequest) (uint64, error) {
	s.trustedCalls++
	return s.leo, nil
}
