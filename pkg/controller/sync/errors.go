package sync

import "errors"

var (
	// ErrClusterIDMismatch indicates that a peer returned state for another cluster.
	ErrClusterIDMismatch = errors.New("controllerv2/sync: cluster id mismatch")
	// ErrStalePayload indicates that a peer returned state older than the local snapshot.
	ErrStalePayload = errors.New("controllerv2/sync: stale payload")
	// ErrHeaderMismatch indicates that response metadata does not match the payload.
	ErrHeaderMismatch = errors.New("controllerv2/sync: response header mismatch")
	// ErrNoReachablePeer indicates that no endpoint could provide usable state.
	ErrNoReachablePeer = errors.New("controllerv2/sync: no reachable peer")
)
