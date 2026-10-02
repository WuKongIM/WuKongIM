package worker

import (
	"context"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
	"testing"
)

type mqttSourceWorkerFactory struct{ lease *mqttSourceWorkerStore }

func (f mqttSourceWorkerFactory) ChannelStore(ch.ChannelKey, ch.ChannelID) (store.ChannelStore, error) {
	return f.lease, nil
}

type mqttSourceWorkerStore struct {
	store.ChannelStore
	checkpointErr error
	readErr       error
	calls         []string
	through       uint64
	panicRead     bool
}

func (s *mqttSourceWorkerStore) StoreCheckpoint(_ context.Context, cp ch.Checkpoint) error {
	s.calls = append(s.calls, "checkpoint")
	s.through = cp.HW
	return s.checkpointErr
}
func (s *mqttSourceWorkerStore) LoadCommittedMQTTSource(_ context.Context, through uint64) (ch.MQTTSourceSnapshot, bool, error) {
	s.calls = append(s.calls, "source")
	if s.panicRead {
		panic("source read panic")
	}
	return ch.MQTTSourceSnapshot{Generation: "generation", CommittedThrough: through}, true, s.readErr
}
func (s *mqttSourceWorkerStore) Close() error { s.calls = append(s.calls, "close"); return nil }

func TestMQTTSourceWorkerPersistsBoundaryAndClosesLease(t *testing.T) {
	for _, mode := range []string{"success", "checkpoint-error", "read-error", "panic"} {
		t.Run(mode, func(t *testing.T) {
			s := &mqttSourceWorkerStore{}
			if mode == "checkpoint-error" {
				s.checkpointErr = ch.ErrNotReady
			}
			if mode == "read-error" {
				s.readErr = ch.ErrLogConflict
			}
			s.panicRead = mode == "panic"
			task := Task{Kind: TaskStoreMQTTSource, Fence: ch.Fence{ChannelKey: "1:source", OpID: 7}, StoreMQTTSource: &StoreMQTTSourceTask{ChannelID: ch.ChannelID{ID: "source", Type: 1}, CommittedThrough: 8}}
			run := func() {
				r := task.Run(context.Background(), Deps{Stores: mqttSourceWorkerFactory{s}})
				require.Equal(t, task.Fence, r.Fence)
				if mode == "success" {
					require.NoError(t, r.Err)
					require.True(t, r.StoreMQTTSource.Found)
					require.Equal(t, uint64(8), r.StoreMQTTSource.Snapshot.CommittedThrough)
				} else {
					require.Error(t, r.Err)
				}
			}
			if mode == "panic" {
				require.Panics(t, run)
			} else {
				run()
			}
			require.Equal(t, uint64(8), s.through)
			if mode == "checkpoint-error" {
				require.Equal(t, []string{"checkpoint", "close"}, s.calls)
			} else {
				require.Equal(t, []string{"checkpoint", "source", "close"}, s.calls)
			}
		})
	}
	pools := &Pools{StoreCheckpoint: &Pool{}, StoreRead: &Pool{}}
	require.Same(t, pools.StoreCheckpoint, pools.poolFor(TaskStoreMQTTSource))
}
