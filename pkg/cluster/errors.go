package cluster

import "errors"

var (
	ErrNoLeader               = errors.New("raftcluster: no leader for slot")
	ErrNotLeader              = errors.New("raftcluster: not leader")
	ErrNotStarted             = errors.New("raftcluster: not started")
	ErrLeaderNotStable        = errors.New("raftcluster: leader not stable after retries")
	ErrSlotNotFound           = errors.New("raftcluster: slot not found")
	ErrHashSlotRequired       = errors.New("raftcluster: hash slot required")
	ErrRerouted               = errors.New("raftcluster: rerouted")
	ErrInvalidConfig          = errors.New("raftcluster: invalid config")
	ErrManualRecoveryRequired = errors.New("raftcluster: manual recovery required")
)
