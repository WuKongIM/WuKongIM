package cluster

import "errors"

var (
	// ErrInvalidConfig indicates that cluster configuration is incomplete or invalid.
	ErrInvalidConfig = errors.New("cluster: invalid config")
	// ErrNotStarted indicates that the node runtime has not been started or wired yet.
	ErrNotStarted = errors.New("cluster: not started")
	// ErrStopping indicates that the node is shutting down and rejects new foreground work.
	ErrStopping = errors.New("cluster: stopping")
	// ErrRouteNotReady indicates that no valid route snapshot is available.
	ErrRouteNotReady = errors.New("cluster: route not ready")
	// ErrNoSlotLeader indicates that a route exists but the Slot leader is unknown.
	ErrNoSlotLeader = errors.New("cluster: no slot leader")
	// ErrProposalResultUnsupported indicates that the configured proposer cannot return apply results.
	ErrProposalResultUnsupported = errors.New("cluster: proposal result unsupported")
	// ErrNotLeader indicates that the target node is not the leader for the requested operation.
	ErrNotLeader = errors.New("cluster: not leader")
	// ErrSlotNotFound indicates that the requested physical Slot is not available locally.
	ErrSlotNotFound = errors.New("cluster: slot not found")
	// ErrBackpressured indicates that a bounded foreground buffer rejected new work.
	ErrBackpressured = errors.New("cluster: backpressured")
)
