package clusterv2

import "errors"

var (
	// ErrInvalidConfig indicates that clusterv2 configuration is incomplete or invalid.
	ErrInvalidConfig = errors.New("clusterv2: invalid config")
	// ErrNotStarted indicates that the node runtime has not been started or wired yet.
	ErrNotStarted = errors.New("clusterv2: not started")
	// ErrStopping indicates that the node is shutting down and rejects new foreground work.
	ErrStopping = errors.New("clusterv2: stopping")
	// ErrRouteNotReady indicates that no valid route snapshot is available.
	ErrRouteNotReady = errors.New("clusterv2: route not ready")
	// ErrNoSlotLeader indicates that a route exists but the Slot leader is unknown.
	ErrNoSlotLeader = errors.New("clusterv2: no slot leader")
	// ErrNotLeader indicates that the target node is not the leader for the requested operation.
	ErrNotLeader = errors.New("clusterv2: not leader")
	// ErrSlotNotFound indicates that the requested physical Slot is not available locally.
	ErrSlotNotFound = errors.New("clusterv2: slot not found")
)
