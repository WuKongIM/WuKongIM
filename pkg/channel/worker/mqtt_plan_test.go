package worker

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

type mqttPlanStore struct {
	mqttSourceWorkerStore
	state       ch.MQTTReplayAnchorState
	command     ch.CommandID
	readThrough uint64
}
type mqttPlanFactory struct{ s *mqttPlanStore }

func (f mqttPlanFactory) ChannelStore(ch.ChannelKey, ch.ChannelID) (store.ChannelStore, error) {
	return f.s, nil
}
func (s *mqttPlanStore) ReadMQTTReplayAnchors(_ context.Context, through uint64, command ch.CommandID) (ch.MQTTReplayAnchorState, error) {
	s.calls = append(s.calls, "plan")
	s.command, s.readThrough = command, through
	if s.panicRead {
		panic("plan")
	}
	return s.state, s.readErr
}

func TestMQTTPlanWorkerOwnsCapturedBoundaryAndLease(t *testing.T) {
	for _, mode := range []string{"success", "checkpoint", "read", "panic", "foreign", "hw", "zero_hw", "requested"} {
		t.Run(mode, func(t *testing.T) {
			gen := quorumlog.MQTTSourceGeneration(ch.CommandID{1})
			s := &mqttPlanStore{state: ch.MQTTReplayAnchorState{Source: ch.MQTTSourceSnapshot{Generation: gen, CommittedThrough: 8}}}
			task := Task{Kind: TaskStoreMQTTPlan, Fence: ch.Fence{ChannelKey: "1:plan", OpID: 7}, StoreMQTTPlan: &StoreMQTTPlanTask{Request: ch.MQTTReplayPlanRequest{ChannelID: ch.ChannelID{ID: "plan", Type: 1}, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, Generation: gen}, CommittedThrough: 8}}
			switch mode {
			case "checkpoint":
				s.checkpointErr = ch.ErrNotReady
			case "read":
				s.readErr = ch.ErrLogConflict
			case "panic":
				s.panicRead = true
			case "foreign":
				s.state.Source.Generation = "foreign"
			case "hw":
				s.state.Source.CommittedThrough = 9
			case "zero_hw":
				task.StoreMQTTPlan.CommittedThrough = 0
			case "requested":
				s.state.HasRequested = true
			}
			run := func() {
				res := task.Run(context.Background(), Deps{Stores: mqttPlanFactory{s}})
				require.Equal(t, task.Fence, res.Fence)
				if mode == "success" {
					require.NoError(t, res.Err)
					require.Equal(t, s.state.Source, res.StoreMQTTPlan.Plan.Source)
					require.False(t, res.StoreMQTTPlan.Plan.HasAnchor)
				} else {
					require.Error(t, res.Err)
					require.Nil(t, res.StoreMQTTPlan)
				}
			}
			if mode == "panic" {
				require.Panics(t, run)
			} else {
				run()
			}
			if mode == "zero_hw" {
				require.Empty(t, s.calls)
			} else {
				require.Equal(t, "close", s.calls[len(s.calls)-1])
				require.Equal(t, uint64(8), s.through)
				if mode != "checkpoint" {
					require.Equal(t, uint64(8), s.readThrough)
					require.Zero(t, s.command)
				}
			}
		})
	}
	p := &Pools{StoreCheckpoint: &Pool{}, StoreRead: &Pool{}}
	require.Same(t, p.StoreCheckpoint, p.poolFor(TaskStoreMQTTPlan))
}
