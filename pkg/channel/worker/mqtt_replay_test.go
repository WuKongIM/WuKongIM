package worker

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

type mqttReplayWorkerStore struct {
	mqttSourceWorkerStore
	page         ch.MQTTReplayPage
	prepareErr   error
	panicPrepare bool
	foreign      bool
}
type mqttReplayWorkerFactory struct{ s *mqttReplayWorkerStore }

func (f mqttReplayWorkerFactory) ChannelStore(ch.ChannelKey, ch.ChannelID) (store.ChannelStore, error) {
	return f.s, nil
}
func (s *mqttReplayWorkerStore) LoadCommittedMQTTSource(_ context.Context, through uint64) (ch.MQTTSourceSnapshot, bool, error) {
	s.calls = append(s.calls, "source")
	generation := s.page.After.Generation
	if s.foreign {
		generation = "foreign"
	}
	return ch.MQTTSourceSnapshot{Generation: generation, CommittedThrough: through}, true, s.readErr
}
func (s *mqttReplayWorkerStore) PrepareMQTTReplay(_ context.Context, _ ch.MQTTReplayRange) (ch.MQTTReplayPage, error) {
	s.calls = append(s.calls, "prepare")
	if s.panicPrepare {
		panic("prepare")
	}
	return s.page, s.prepareErr
}

func TestMQTTReplayWorkerChecksSourceAndReleasesLease(t *testing.T) {
	for _, fault := range []string{"success", "checkpoint", "source", "foreign", "copy", "panic", "bad_result", "future_hw"} {
		t.Run(fault, func(t *testing.T) {
			gen := quorumlog.MQTTSourceGeneration(ch.CommandID{1})
			s := &mqttReplayWorkerStore{page: ch.MQTTReplayPage{
				Before:  ch.MQTTReplayPrefix{Generation: gen},
				After:   ch.MQTTReplayPrefix{Generation: gen, Through: 1, TotalBytes: 1, TotalStoredBytes: 1, Digest: [32]byte{1}},
				Records: []ch.MQTTReplayRecord{{Position: 1, ContentVersion: 1, MessageID: 7, AccountedBytes: 1, TotalBytes: 1, TotalStoredBytes: 1, ContentHash: [32]byte{1}, Digest: [32]byte{1}, Content: []byte{1}}},
			}}
			task := Task{Kind: TaskStoreMQTTReplay, Fence: ch.Fence{ChannelKey: "1:source", OpID: 5}, StoreMQTTReplay: &StoreMQTTReplayTask{
				Request: ch.MQTTReplayRequest{ChannelID: ch.ChannelID{ID: "source", Type: 1}, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1,
					Range: ch.MQTTReplayRange{Generation: gen, From: 1, Through: 1, Limit: 1, MaxBytes: 1024}}, CommittedThrough: 1}}
			switch fault {
			case "checkpoint":
				s.checkpointErr = ch.ErrNotReady
			case "source":
				s.readErr = ch.ErrLogConflict
			case "foreign":
				s.foreign = true
			case "copy":
				s.prepareErr = ch.ErrNotReady
			case "panic":
				s.panicPrepare = true
			case "bad_result":
				s.page.After.Through = 2
			case "future_hw":
				task.StoreMQTTReplay.CommittedThrough = 0
			}
			run := func() {
				res := task.Run(context.Background(), Deps{Stores: mqttReplayWorkerFactory{s}})
				require.Equal(t, task.Fence, res.Fence)
				if fault == "success" {
					require.NoError(t, res.Err)
					require.Equal(t, s.page, res.StoreMQTTReplay.Page)
				} else {
					require.Error(t, res.Err)
				}
			}
			if fault == "panic" {
				require.Panics(t, run)
			} else {
				run()
			}
			if fault == "future_hw" {
				require.Empty(t, s.calls)
			} else {
				require.Equal(t, "close", s.calls[len(s.calls)-1])
				require.Equal(t, uint64(1), s.through)
			}
			if fault == "checkpoint" {
				require.Equal(t, []string{"checkpoint", "close"}, s.calls)
			}
			if fault == "source" || fault == "foreign" {
				require.Equal(t, []string{"checkpoint", "source", "close"}, s.calls)
			}
		})
	}
	pools := &Pools{StoreCheckpoint: &Pool{}, StoreRead: &Pool{}}
	require.Same(t, pools.StoreCheckpoint, pools.poolFor(TaskStoreMQTTReplay))
}
