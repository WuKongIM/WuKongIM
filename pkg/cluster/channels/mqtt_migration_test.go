package channels

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

// This runtime returns exactly the supplied evidence, including absent receipts.
type mqttMigrationRuntime struct {
	fakeMigrationExecutorRuntime
	proof ch.RuntimeProbeChannel
	err   error
}

func (r *mqttMigrationRuntime) ProbeChannel(context.Context, uint64, string, uint8) (ch.RuntimeProbeChannel, error) {
	return r.proof, r.err
}

func mqttMigrationFixture(phase metadb.ChannelMigrationPhase, kind metadb.ChannelMigrationKind) (*MigrationExecutor, *fakeMigrationExecutorStore, *mqttMigrationRuntime, *metadb.ChannelRuntimeMeta) {
	now := time.Unix(100, 0)
	id := ch.ChannelID{ID: "mqtt-migration", Type: 2}
	task := testLeaderTransferExecutorTask(id)
	if kind == metadb.ChannelMigrationKindReplicaReplace {
		task = testReplicaReplaceExecutorTask(id)
	}
	task.Kind, task.Phase, task.Status = kind, phase, metadb.ChannelMigrationStatusRunning
	task.OwnerNodeID, task.OwnerLeaseUntilMS = 2, now.Add(time.Minute).UnixMilli()
	task.FenceToken, task.FenceVersion, task.FenceUntilMS = task.TaskID, 1, now.Add(time.Minute).UnixMilli()
	task.CutoverLEO, task.CutoverHW = 9, 9
	task.DrainedLeaderNode, task.DrainedChannelEpoch, task.DrainedLeaderEpoch = 1, 10, 20
	task.DrainedRuntimeGeneration, task.DrainedFenceVersion = 30, 1
	m := testMigrationRuntimeMeta(id)
	m.Leader, m.LeaderEpoch = 1, 20
	m.WriteFenceToken, m.WriteFenceVersion, m.WriteFenceUntilMS = task.TaskID, 1, task.FenceUntilMS
	m.WriteFenceReason = uint8(ch.WriteFenceReasonLeaderTransfer)
	role := ch.RoleFollower
	if phase == metadb.ChannelMigrationPhaseVerifyNewLeader {
		m.Leader, m.LeaderEpoch, role = task.TargetNode, 21, ch.RoleLeader
	}
	if kind == metadb.ChannelMigrationKindReplicaReplace {
		m.Replicas, m.ISR = []uint64{1, 2, 3, 4}, []uint64{1, 2, 3}
		m.WriteFenceReason = uint8(ch.WriteFenceReasonReplicaReplace)
		if phase == metadb.ChannelMigrationPhaseVerifyMembership {
			m.Replicas, m.ISR = []uint64{1, 2, 4}, []uint64{1, 2, 4}
		}
	}
	r := &mqttMigrationRuntime{proof: ch.RuntimeProbeChannel{
		ChannelID: id, ChannelEpoch: m.ChannelEpoch, LeaderEpoch: m.LeaderEpoch,
		Role: role, Status: ch.StatusActive, HW: 9, LEO: 9, CheckpointHW: 9,
		WriteFence:      ch.WriteFence{Token: task.TaskID, Version: 1, Reason: ch.WriteFenceReason(m.WriteFenceReason), Until: time.UnixMilli(m.WriteFenceUntilMS)},
		ReplayReadiness: &ch.MQTTReplayReadiness{CommittedThrough: 9, AnchorPosition: 8, RequiredThrough: 7, Covered: true},
	}}
	s := newFakeMigrationExecutorStore(task, &m, now)
	e := NewMigrationExecutor(MigrationExecutorConfig{LocalNode: 2, Source: fakeMigrationExecutorSource{store: s}, Store: s, Runtime: r, Meta: fakeMigrationExecutorMetaReader{meta: &m}, Clock: func() time.Time { return now }})
	return e, s, r, &m
}

func TestMQTTMigrationCutoverRequiresFreshReplayCoverage(t *testing.T) {
	stages := []struct {
		name  string
		phase metadb.ChannelMigrationPhase
		kind  metadb.ChannelMigrationKind
	}{
		{"transfer_catchup", metadb.ChannelMigrationPhaseFinalTargetCatchUp, metadb.ChannelMigrationKindLeaderTransfer},
		{"transfer_commit", metadb.ChannelMigrationPhaseCommitLeaderMeta, metadb.ChannelMigrationKindLeaderTransfer},
		{"transfer_verify", metadb.ChannelMigrationPhaseVerifyNewLeader, metadb.ChannelMigrationKindLeaderTransfer},
		{"failover_verify", metadb.ChannelMigrationPhaseVerifyNewLeader, metadb.ChannelMigrationKindLeaderFailover},
		{"replace_catchup", metadb.ChannelMigrationPhaseFinalTargetCatchUp, metadb.ChannelMigrationKindReplicaReplace},
		{"replace_promote", metadb.ChannelMigrationPhasePromoteAndRemove, metadb.ChannelMigrationKindReplicaReplace},
		{"replace_verify", metadb.ChannelMigrationPhaseVerifyMembership, metadb.ChannelMigrationKindReplicaReplace},
	}
	for _, stage := range stages {
		for _, mode := range []string{"covered", "native", "missing", "uncovered", "foreign_hw", "malformed", "recovering", "fence", "error"} {
			t.Run(stage.name+"/"+mode, func(t *testing.T) {
				e, s, r, _ := mqttMigrationFixture(stage.phase, stage.kind)
				task := s.task
				good := r.proof
				ready := *r.proof.ReplayReadiness
				r.proof.ReplayReadiness = &ready
				switch mode {
				case "native":
					r.proof.ReplayReadiness = &ch.MQTTReplayReadiness{CommittedThrough: 9, Covered: true}
				case "missing":
					r.proof.ReplayReadiness = nil
				case "uncovered":
					ready.Covered = false
				case "foreign_hw":
					ready.CommittedThrough++
				case "malformed":
					ready.RequiredThrough = ready.AnchorPosition
				case "recovering":
					r.proof.RecoveryRequired = true
				case "fence":
					r.proof.WriteFence.Version++
				case "error":
					r.err = ch.ErrNotReady
				}
				run := func() error {
					if stage.kind == metadb.ChannelMigrationKindReplicaReplace {
						return e.runReplicaReplacePhase(context.Background(), s.task)
					}
					return e.runLeaderTransferPhase(context.Background(), s.task)
				}
				err := run()
				if mode == "error" {
					require.ErrorIs(t, err, ch.ErrNotReady)
				} else {
					require.NoError(t, err)
				}
				if mode == "covered" || mode == "native" {
					require.Len(t, s.ops, 1)
					return
				}
				require.Empty(t, s.ops, "lag/absent evidence must yield without durable task churn")
				require.Equal(t, task, s.task, "waiting must remain runnable")
				r.proof, r.err = good, nil
				require.NoError(t, run())
				require.Len(t, s.ops, 1, "a later independent readiness proof must resume the same phase")
			})
		}
	}
}

func TestMQTTFailoverSelectsNativeLeaderBeforeWaitingForReplay(t *testing.T) {
	e, s, r, _ := mqttMigrationFixture(metadb.ChannelMigrationPhaseCommitLeaderMeta, metadb.ChannelMigrationKindLeaderFailover)
	r.proof.ReplayReadiness = nil
	r.err = ch.ErrNotLeader // The dead source cannot plan shared recovery yet.
	require.NoError(t, e.runLeaderTransferCommitLeaderMeta(context.Background(), s.task))
	require.Equal(t, []string{"commit_leader"}, s.ops)
}
