package replica

import (
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

type machineCompletion struct {
	RequestID uint64
	Result    channel.CommitResult
	Err       error
}

type machineResult struct {
	Effects     []machineEffect
	Completions []machineCompletion
	// Fetch carries the deterministic fetch response snapshot after loop-owned progress updates.
	Fetch *machineFetchProgressResult
	Err   error
}

type machineFetchProgressResult struct {
	// Result is the public fetch response before records are read from the log.
	Result channel.ReplicaFetchResult
	// ReadLog asks the facade to read records under a captured loop fence.
	ReadLog *readLogEffect
	// LeaderLEO fences the subsequent log read so Fetch never exposes newer durable records.
	LeaderLEO uint64
	// MatchOffset is the safe follower progress accepted by epoch-lineage rules.
	MatchOffset uint64
	// OldProgress is captured for diagnostics.
	OldProgress uint64
	// NeedsAdvance reports whether this fetch changed quorum progress.
	NeedsAdvance bool
	// ChannelKey is captured for diagnostics after the loop releases its lock.
	ChannelKey channel.ChannelKey
	// ReplicaID is the follower whose progress was updated.
	ReplicaID channel.NodeID
	// FetchOffset is the requested log offset.
	FetchOffset uint64
}

type replicaMachine struct {
	state replicaMachineState
}

func newReplicaMachine(state replicaMachineState) replicaMachine {
	state = state.clone()
	if state.progress == nil {
		state.progress = make(map[channel.NodeID]uint64)
	}
	return replicaMachine{state: state}
}

func (m *replicaMachine) Apply(event machineEvent) machineResult {
	switch ev := event.(type) {
	case machineAppendCommand:
		return m.applyAppend(ev)
	case machineCursorCommand:
		return m.applyCursor(ev)
	case machineAdvanceHWEvent:
		return m.applyAdvanceHW()
	case machineCheckpointStoredEvent:
		return m.applyCheckpointStored(ev)
	case machineTombstoneCommand:
		return m.applyTombstone()
	case machineCloseCommand:
		return m.applyClose()
	default:
		return machineResult{Err: channel.ErrInvalidArgument}
	}
}

func (m *replicaMachine) applyAppend(cmd machineAppendCommand) machineResult {
	wasFenced := m.state.replica.Role == channel.ReplicaRoleFencedLeader
	if err := m.appendable(cmd.Now); err != nil {
		result := machineResult{
			Err: err,
			Completions: []machineCompletion{{
				RequestID: cmd.RequestID,
				Err:       err,
			}},
		}
		if !wasFenced && errors.Is(err, channel.ErrLeaseExpired) && m.state.replica.Role == channel.ReplicaRoleFencedLeader {
			result.Effects = []machineEffect{publishStateEffect{State: m.state.replica}}
		}
		return result
	}
	if err := m.checkInvariant(); err != nil {
		return machineResult{Err: err}
	}
	if len(cmd.Records) == 0 {
		return machineResult{
			Completions: []machineCompletion{{
				RequestID: cmd.RequestID,
				Result: channel.CommitResult{
					BaseOffset:   m.state.replica.LEO,
					NextCommitHW: m.state.replica.HW,
				},
			}},
		}
	}

	effectID := m.nextEffectID()
	records := cloneRecords(cmd.Records)
	return machineResult{
		Effects: []machineEffect{appendLeaderBatchEffect{
			EffectID:       effectID,
			RequestIDs:     []uint64{cmd.RequestID},
			ChannelKey:     m.state.replica.ChannelKey,
			Epoch:          m.state.replica.Epoch,
			LeaderEpoch:    m.state.meta.LeaderEpoch,
			RoleGeneration: m.state.roleGeneration,
			LeaseUntil:     m.state.meta.LeaseUntil,
			Records:        records,
		}},
	}
}

func (m *replicaMachine) appendable(now time.Time) error {
	if err := m.mutableGuard(); err != nil {
		return err
	}
	if !m.state.replica.CommitReady {
		return channel.ErrNotReady
	}
	if !now.Before(m.state.meta.LeaseUntil) {
		previous := m.state.replica
		m.state.replica.Role = channel.ReplicaRoleFencedLeader
		m.state.roleGeneration++
		if err := m.checkInvariantWithPrevious(previous); err != nil {
			m.state.replica = previous
			m.state.roleGeneration--
			return err
		}
		return channel.ErrLeaseExpired
	}
	if len(m.state.meta.ISR) < m.state.meta.MinISR {
		return channel.ErrInsufficientISR
	}
	return nil
}

func (m *replicaMachine) applyCursor(cmd machineCursorCommand) machineResult {
	if err := m.leaderProgressGuard(); err != nil {
		return machineResult{Err: err}
	}
	if cmd.ChannelKey != "" && cmd.ChannelKey != m.state.replica.ChannelKey {
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if cmd.Epoch != m.state.replica.Epoch {
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if cmd.ReplicaID == 0 {
		return machineResult{Err: channel.ErrInvalidMeta}
	}
	decision := decideLineage(m.state.epochHistory, m.state.replica.LogStartOffset, m.state.replica.HW, m.state.replica.LEO, cmd.MatchOffset, cmd.OffsetEpoch)
	if decision.err != nil {
		return machineResult{Err: decision.err}
	}
	if decision.matchOffset > m.state.replica.LEO {
		return machineResult{Err: channel.ErrCorruptState}
	}
	if current := m.state.progress[cmd.ReplicaID]; decision.matchOffset <= current {
		return machineResult{}
	}
	previousProgress := m.state.progress[cmd.ReplicaID]
	m.state.progress[cmd.ReplicaID] = decision.matchOffset
	if err := m.checkInvariant(); err != nil {
		m.state.progress[cmd.ReplicaID] = previousProgress
		return machineResult{Err: err}
	}
	return machineResult{Effects: []machineEffect{publishStateEffect{State: m.state.replica}}}
}

func (m *replicaMachine) applyAdvanceHW() machineResult {
	if err := m.leaderProgressGuard(); err != nil {
		return machineResult{Err: err}
	}
	candidate, ok, err := quorumProgressCandidate(m.state.meta.ISR, m.state.progress, m.state.meta.MinISR, m.state.replica.HW, m.state.replica.LEO)
	if err != nil || !ok {
		return machineResult{Err: err}
	}

	previous := m.state.replica
	m.state.replica.HW = candidate
	checkpoint := channel.Checkpoint{
		Epoch:          m.state.replica.Epoch,
		LogStartOffset: m.state.replica.LogStartOffset,
		HW:             candidate,
	}
	if err := m.checkInvariantWithPrevious(previous); err != nil {
		m.state.replica = previous
		return machineResult{Err: err}
	}

	effectID := m.nextEffectID()
	m.state.checkpointEffectID = effectID
	return machineResult{
		Effects: []machineEffect{
			completeAppendEffect{},
			storeCheckpointEffect{
				EffectID:       effectID,
				ChannelKey:     m.state.replica.ChannelKey,
				Epoch:          m.state.replica.Epoch,
				LeaderEpoch:    m.state.meta.LeaderEpoch,
				RoleGeneration: m.state.roleGeneration,
				Checkpoint:     checkpoint,
				VisibleHW:      candidate,
				LEO:            m.state.replica.LEO,
			},
			publishStateEffect{State: m.state.replica},
		},
	}
}

func (m *replicaMachine) applyCheckpointStored(ev machineCheckpointStoredEvent) machineResult {
	if m.state.closed || m.state.replica.Role == channel.ReplicaRoleTombstoned {
		return machineResult{}
	}
	if m.state.checkpointEffectID == 0 || ev.EffectID != m.state.checkpointEffectID {
		return machineResult{}
	}
	if ev.ChannelKey != m.state.replica.ChannelKey {
		return machineResult{}
	}
	if ev.Epoch != m.state.replica.Epoch {
		return machineResult{}
	}
	if ev.LeaderEpoch != m.state.meta.LeaderEpoch {
		return machineResult{}
	}
	if ev.RoleGeneration != m.state.roleGeneration {
		return machineResult{}
	}
	if ev.Checkpoint.HW <= m.state.replica.CheckpointHW {
		return machineResult{}
	}

	previous := m.state.replica
	m.state.replica.CheckpointHW = ev.Checkpoint.HW
	if ev.Checkpoint.LogStartOffset > m.state.replica.LogStartOffset {
		m.state.replica.LogStartOffset = ev.Checkpoint.LogStartOffset
	}
	if err := m.checkInvariantWithPrevious(previous); err != nil {
		m.state.replica = previous
		return machineResult{Err: err}
	}
	m.state.checkpointEffectID = 0
	return machineResult{Effects: []machineEffect{publishStateEffect{State: m.state.replica}}}
}

func (m *replicaMachine) applyTombstone() machineResult {
	previous := m.state.replica
	m.state.replica.Role = channel.ReplicaRoleTombstoned
	m.state.roleGeneration++
	err := m.checkInvariantWithPrevious(previous)
	return machineResult{Effects: []machineEffect{
		publishStateEffect{State: m.state.replica},
		completeAppendEffect{Err: channel.ErrTombstoned},
	}, Err: err}
}

func (m *replicaMachine) applyClose() machineResult {
	previous := m.state.replica
	m.state.closed = true
	m.state.roleGeneration++
	err := m.checkInvariantWithPrevious(previous)
	return machineResult{Effects: []machineEffect{
		publishStateEffect{State: m.state.replica},
		completeAppendEffect{Err: channel.ErrNotLeader},
	}, Err: err}
}

func (m *replicaMachine) mutableGuard() error {
	if m.state.closed {
		return channel.ErrNotLeader
	}
	switch m.state.replica.Role {
	case channel.ReplicaRoleTombstoned:
		return channel.ErrTombstoned
	case channel.ReplicaRoleFencedLeader:
		return channel.ErrLeaseExpired
	case channel.ReplicaRoleLeader:
		return nil
	default:
		return channel.ErrNotLeader
	}
}

func (m *replicaMachine) leaderProgressGuard() error {
	if m.state.closed {
		return channel.ErrNotLeader
	}
	switch m.state.replica.Role {
	case channel.ReplicaRoleTombstoned:
		return channel.ErrTombstoned
	case channel.ReplicaRoleLeader, channel.ReplicaRoleFencedLeader:
		return nil
	default:
		return channel.ErrNotLeader
	}
}

func cloneRecords(records []channel.Record) []channel.Record {
	out := make([]channel.Record, len(records))
	for i, record := range records {
		out[i] = record
		out[i].Payload = append([]byte(nil), record.Payload...)
	}
	return out
}

func (m *replicaMachine) nextEffectID() uint64 {
	m.state.nextEffectID++
	return m.state.nextEffectID
}

func (m *replicaMachine) checkInvariant() error {
	return checkReplicaInvariant(replicaInvariantCheck{
		State:        m.state.replica,
		EpochHistory: m.state.epochHistory,
	})
}

func (m *replicaMachine) checkInvariantWithPrevious(previous channel.ReplicaState) error {
	return checkReplicaInvariant(replicaInvariantCheck{
		State:         m.state.replica,
		EpochHistory:  m.state.epochHistory,
		PreviousState: &previous,
	})
}
