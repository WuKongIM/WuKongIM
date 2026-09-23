package channels

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

type mqttReadinessStore struct {
	channelstore.ChannelStore
	value         ch.MQTTReplayReadiness
	err           error
	after         func()
	closed        int
	checkpoints   []uint64
	checkpointErr error
}

func (s *mqttReadinessStore) ReadMQTTReplayReadiness(ctx context.Context, hw uint64) (ch.MQTTReplayReadiness, error) {
	if s.after != nil {
		s.after()
	}
	return s.value, s.err
}
func (s *mqttReadinessStore) Close() error { s.closed++; return nil }

type mqttReadinessFactory struct{ mqttCopyFactory }

func (*mqttReadinessFactory) SupportsMQTTReplayAnchors() bool { return true }

func TestMQTTReplicaReadinessAttachesOnlyFreshBoundedEvidence(t *testing.T) {
	for _, mode := range []string{"ready", "lagging", "missing_reader", "unsupported", "meta_changed", "members_changed", "fence_changed", "runtime_changed", "future_hw", "malformed", "read_error", "cancel", "no_fresh_reader"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			id := ch.ChannelID{ID: "readiness", Type: 2}
			m := ch.Meta{ID: id, Key: ch.ChannelKeyForID(id), Epoch: 1, LeaderEpoch: 2, RouteGeneration: 3, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
			proof := ch.RuntimeProbeChannel{ChannelID: id, ChannelEpoch: 1, LeaderEpoch: 2, Role: ch.RoleFollower, Status: ch.StatusActive, LEO: 10, HW: 10, CheckpointHW: 10}
			runtime := &repairActivationRuntime{}
			runtime.probe = ch.RuntimeProbeResult{Channels: []ch.RuntimeProbeChannel{proof}}
			source := &mqttFreshMeta{meta: m}
			handle := &mqttReadinessStore{value: ch.MQTTReplayReadiness{CommittedThrough: 10, AnchorPosition: 9, RequiredThrough: 8, Covered: true}}
			factory := &mqttReadinessFactory{mqttCopyFactory: mqttCopyFactory{handle: handle}}
			svc, err := NewService(Config{LocalNode: 2, Runtime: runtime, MetaSource: source})
			require.NoError(t, err)
			svc.store = factory
			switch mode {
			case "lagging":
				handle.value.Covered = false
			case "missing_reader":
				factory.handle = &mqttCopyStore{}
			case "unsupported":
				svc.store = nil
				proof.ReplayReadiness = &handle.value
			case "meta_changed":
				handle.after = func() { source.meta.LeaderEpoch++ }
			case "members_changed":
				handle.after = func() { source.meta.Replicas = []ch.NodeID{3, 2, 1} }
			case "fence_changed":
				handle.after = func() { source.meta.WriteFence = ch.WriteFence{Token: "transfer", Version: 1} }
			case "runtime_changed":
				handle.after = func() { runtime.probe.Channels[0].Role = ch.RoleLeader }
			case "future_hw":
				handle.value.CommittedThrough++
			case "malformed":
				handle.value.RequiredThrough = 9
			case "read_error":
				handle.err = ch.ErrNotReady
			case "cancel":
				handle.after = cancel
			case "no_fresh_reader":
				svc.metaSource = NewStaticMetaSource([]ch.Meta{m})
			}
			got, err := svc.attachMQTTReplayReadiness(ctx, m, proof)
			if mode == "ready" || mode == "lagging" {
				require.NoError(t, err)
				require.NotNil(t, got.ReplayReadiness)
				require.Equal(t, handle.value, *got.ReplayReadiness)
			} else if mode == "unsupported" {
				require.NoError(t, err)
				require.Nil(t, got.ReplayReadiness)
			} else {
				require.Error(t, err)
				require.Nil(t, got.ReplayReadiness)
			}
			if mode != "unsupported" && mode != "no_fresh_reader" && mode != "missing_reader" {
				require.Equal(t, 1, handle.closed)
			}
		})
	}
}

type mqttReadinessRefresh struct {
	calls []replication.Authority
	err   error
}

func (r *mqttReadinessRefresh) RequestCommittedReplicaRefresh(_ context.Context, a replication.Authority) error {
	r.calls = append(r.calls, a)
	return r.err
}
func (s *mqttReadinessStore) StoreCheckpoint(_ context.Context, cp ch.Checkpoint) error {
	s.checkpoints = append(s.checkpoints, cp.HW)
	return s.checkpointErr
}

func TestMQTTLeaderReadinessCheckpointsOnlyRecoveredCapturedHW(t *testing.T) {
	for _, mode := range []string{"ready", "fenced", "recovering", "checkpoint_error", "refresh_error"} {
		t.Run(mode, func(t *testing.T) {
			id := ch.ChannelID{ID: "leader-readiness", Type: 2}
			m := ch.Meta{ID: id, Key: ch.ChannelKeyForID(id), Epoch: 1, LeaderEpoch: 2, RouteGeneration: 3, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
			if mode == "fenced" {
				m.WriteFence = ch.WriteFence{Token: "migration", Version: 4}
			}
			proof := ch.RuntimeProbeChannel{ChannelID: id, ChannelEpoch: 1, LeaderEpoch: 2, Role: ch.RoleLeader, Status: ch.StatusActive, LEO: 10, HW: 10, CheckpointHW: 10, WriteFence: m.WriteFence}
			if mode == "recovering" {
				proof.RecoveryRequired = true
			}
			runtime := &repairActivationRuntime{}
			runtime.probe = ch.RuntimeProbeResult{Channels: []ch.RuntimeProbeChannel{proof}}
			handle := &mqttReadinessStore{value: ch.MQTTReplayReadiness{CommittedThrough: 10, Covered: true}}
			refresh := &mqttReadinessRefresh{}
			if mode == "checkpoint_error" {
				handle.checkpointErr = ch.ErrNotReady
			}
			if mode == "refresh_error" {
				refresh.err = ch.ErrStaleMeta
			}
			svc, err := NewService(Config{LocalNode: 1, Runtime: runtime, MetaSource: &mqttFreshMeta{meta: m}})
			require.NoError(t, err)
			svc.store = &mqttReadinessFactory{mqttCopyFactory: mqttCopyFactory{handle: handle}}
			svc.replicaCommitRefresh = refresh
			got, err := svc.attachMQTTReplayReadiness(context.Background(), m, proof)
			if mode == "ready" || mode == "fenced" {
				require.NoError(t, err)
				require.NotNil(t, got.ReplayReadiness)
				require.Equal(t, []uint64{10}, handle.checkpoints)
				require.Len(t, refresh.calls, 1)
				require.Equal(t, m.WriteFence, refresh.calls[0].WriteFence)
				require.Equal(t, []ch.NodeID{3}, refresh.calls[0].Learners)
			} else {
				require.Error(t, err)
				require.Nil(t, got.ReplayReadiness)
			}
			if mode == "recovering" {
				require.Empty(t, handle.checkpoints)
				require.Empty(t, refresh.calls)
			}
		})
	}
}
