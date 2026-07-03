package raft

import (
	"time"

	etcdraft "go.etcd.io/raft/v3"
)

const (
	// RoleUnknown reports that the local ControllerV2 Raft role is not yet known.
	RoleUnknown = "unknown"
	// RoleLeader reports that the local ControllerV2 Raft node is leader.
	RoleLeader = "leader"
	// RoleFollower reports that the local ControllerV2 Raft node is follower.
	RoleFollower = "follower"
	// RoleCandidate reports that the local ControllerV2 Raft node is campaigning.
	RoleCandidate = "candidate"
)

const (
	// LogCompactionSkipNotStarted reports that compaction was requested before service start.
	LogCompactionSkipNotStarted = "not_started"
	// LogCompactionSkipNoAppliedIndex reports that no materialized applied index is available.
	LogCompactionSkipNoAppliedIndex = "no_applied_index"
	// LogCompactionSkipNoMaterializedState reports that no state-machine snapshot revision exists.
	LogCompactionSkipNoMaterializedState = "no_materialized_state"
	// LogCompactionSkipUpToDate reports that the current snapshot already covers the target index.
	LogCompactionSkipUpToDate = "up_to_date"
)

const (
	// LogCompactionTriggerManual reports an operator-triggered local compaction attempt.
	LogCompactionTriggerManual = "manual"
	// LogCompactionTriggerAutomatic reports a policy-triggered local compaction attempt.
	LogCompactionTriggerAutomatic = "automatic"
)

// LogCompactionResult describes one local ControllerV2 Raft log compaction attempt.
type LogCompactionResult struct {
	// NodeID is the local ControllerV2 Raft node ID.
	NodeID uint64
	// AppliedIndex is the materialized applied index targeted by this attempt.
	AppliedIndex uint64
	// BeforeSnapshotIndex is the snapshot index observed before this attempt.
	BeforeSnapshotIndex uint64
	// AfterSnapshotIndex is the snapshot index observed after this attempt.
	AfterSnapshotIndex uint64
	// Compacted reports whether this attempt created a new snapshot.
	Compacted bool
	// SkippedReason is empty on success and describes why no snapshot was created.
	SkippedReason string
	// Error is the non-sensitive error string recorded for operator diagnostics.
	Error string
}

// LogCompactionStatus describes the latest local ControllerV2 Raft compaction attempt.
type LogCompactionStatus struct {
	// Enabled reports whether local ControllerV2 Raft snapshot compaction is enabled.
	Enabled bool
	// TriggerEntries is the applied-entry delta required before taking another automatic snapshot.
	TriggerEntries uint64
	// CheckInterval is the minimum interval between automatic compaction checks.
	CheckInterval time.Duration
	// LastTrigger is manual or automatic for the latest attempt.
	LastTrigger string
	// LastAttemptAt is when the latest attempt started.
	LastAttemptAt time.Time
	// LastSuccessAt is when the latest attempt created a snapshot successfully.
	LastSuccessAt time.Time
	// LastAppliedIndex is the materialized applied index targeted by the latest attempt.
	LastAppliedIndex uint64
	// BeforeSnapshotIndex is the snapshot index observed before the latest attempt.
	BeforeSnapshotIndex uint64
	// AfterSnapshotIndex is the snapshot index observed after the latest attempt.
	AfterSnapshotIndex uint64
	// Compacted reports whether the latest attempt created a new snapshot.
	Compacted bool
	// SkippedReason is empty on success and describes why the latest attempt skipped.
	SkippedReason string
	// LastError is the non-sensitive error string from the latest failed attempt.
	LastError string
	// LastErrorAt records when LastError was observed.
	LastErrorAt time.Time
}

// Status is a goroutine-safe snapshot of one ControllerV2 Raft service.
type Status struct {
	// NodeID is the local ControllerV2 Raft node ID.
	NodeID uint64
	// Role is leader, follower, candidate, or unknown.
	Role string
	// LeaderID is the leader currently known to the local Raft node.
	LeaderID uint64
	// Term is the local Raft term.
	Term uint64
	// CommitIndex is the local volatile Raft commit index.
	CommitIndex uint64
	// AppliedIndex is the local volatile Raft applied index.
	AppliedIndex uint64
	// Voters is the current Controller Raft voting set observed by the local RawNode.
	Voters []uint64
	// Learners is the current Controller Raft learner set observed by the local RawNode.
	Learners []uint64
	// FirstIndex is the first available local ControllerV2 Raft log index after snapshot compaction.
	FirstIndex uint64
	// LastIndex is the last available local ControllerV2 Raft log index or snapshot index.
	LastIndex uint64
	// SnapshotIndex is the latest persisted local ControllerV2 Raft snapshot index.
	SnapshotIndex uint64
	// SnapshotTerm is the latest persisted local ControllerV2 Raft snapshot term.
	SnapshotTerm uint64
	// Degraded reports whether startup or apply processing hit a local durable failure.
	Degraded bool
	// ErrorReason describes the latest degradation cause.
	ErrorReason string
	// Compaction describes the latest local ControllerV2 Raft log compaction attempt.
	Compaction LogCompactionStatus
}

func initialStatus(cfg Config) Status {
	return Status{
		NodeID: cfg.NodeID,
		Role:   RoleUnknown,
		Compaction: LogCompactionStatus{
			Enabled:        cfg.SnapshotCount > 0,
			TriggerEntries: cfg.SnapshotCount,
			CheckInterval:  cfg.SnapshotMinInterval,
		},
	}
}

func cloneStatus(st Status) Status {
	if st.Role == "" {
		st.Role = RoleUnknown
	}
	st.Voters = append([]uint64(nil), st.Voters...)
	st.Learners = append([]uint64(nil), st.Learners...)
	return st
}

func raftRoleName(state etcdraft.StateType) string {
	switch state {
	case etcdraft.StateLeader:
		return RoleLeader
	case etcdraft.StateFollower:
		return RoleFollower
	case etcdraft.StateCandidate, etcdraft.StatePreCandidate:
		return RoleCandidate
	default:
		return RoleUnknown
	}
}
