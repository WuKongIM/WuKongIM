package raft

import etcdraft "go.etcd.io/raft/v3"

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
	// Degraded reports whether startup or apply processing hit a local durable failure.
	Degraded bool
	// ErrorReason describes the latest degradation cause.
	ErrorReason string
}

func initialStatus(cfg Config) Status {
	return Status{NodeID: cfg.NodeID, Role: RoleUnknown}
}

func cloneStatus(st Status) Status {
	if st.Role == "" {
		st.Role = RoleUnknown
	}
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
