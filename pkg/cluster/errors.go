package cluster

import "errors"

var (
	ErrNoLeader               = errors.New("raftcluster: no leader for slot")
	ErrNotLeader              = errors.New("raftcluster: not leader")
	ErrNotStarted             = errors.New("raftcluster: not started")
	ErrLeaderNotStable        = errors.New("raftcluster: leader not stable after retries")
	ErrSlotNotFound           = errors.New("raftcluster: slot not found")
	ErrHashSlotRequired       = errors.New("raftcluster: hash slot required")
	ErrHashSlotFenced         = errors.New("raftcluster: hash slot fenced")
	ErrRerouted               = errors.New("raftcluster: rerouted")
	ErrInvalidConfig          = errors.New("raftcluster: invalid config")
	ErrManualRecoveryRequired = errors.New("raftcluster: manual recovery required")
	ErrObservationNotReady    = errors.New("raftcluster: observation not ready")
)

type joinErrorCode string

const (
	joinErrorNone               joinErrorCode = ""
	joinErrorInvalidRequest     joinErrorCode = "invalid_request"
	joinErrorInvalidToken       joinErrorCode = "invalid_token"
	joinErrorNodeIDConflict     joinErrorCode = "node_id_conflict"
	joinErrorAddrConflict       joinErrorCode = "addr_conflict"
	joinErrorUnsupportedVersion joinErrorCode = "unsupported_version"
	joinErrorCommitTimeout      joinErrorCode = "commit_timeout"
	joinErrorTemporary          joinErrorCode = "temporary_unavailable"
)

type joinClusterError struct {
	Code    joinErrorCode
	Message string
}

func (e *joinClusterError) Error() string {
	if e == nil {
		return "raftcluster: join failed"
	}
	if e.Message != "" {
		return "raftcluster: join failed: " + e.Message
	}
	if e.Code != "" {
		return "raftcluster: join failed: " + string(e.Code)
	}
	return "raftcluster: join failed"
}

func (e *joinClusterError) Retryable() bool {
	if e == nil {
		return false
	}
	switch e.Code {
	case joinErrorCommitTimeout, joinErrorTemporary:
		return true
	default:
		return false
	}
}
