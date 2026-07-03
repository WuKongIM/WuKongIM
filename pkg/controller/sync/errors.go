package sync

import "errors"

var (
	// ErrClusterIDMismatch indicates that a peer returned state for another cluster.
	ErrClusterIDMismatch = errors.New("controller/sync: cluster id mismatch")
	// ErrStalePayload indicates that a peer returned state older than the local snapshot.
	ErrStalePayload = errors.New("controller/sync: stale payload")
	// ErrHeaderMismatch indicates that response metadata does not match the payload.
	ErrHeaderMismatch = errors.New("controller/sync: response header mismatch")
	// ErrNoReachablePeer indicates that no endpoint could provide usable state.
	ErrNoReachablePeer = errors.New("controller/sync: no reachable peer")
)
