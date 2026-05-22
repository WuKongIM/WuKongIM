package channelplane

import "errors"

var (
	// ErrClosed reports that the channel plane is stopped or shutting down.
	ErrClosed = errors.New("channelplane: closed")
	// ErrNotStarted reports that AppendBatch was called before Start.
	ErrNotStarted = errors.New("channelplane: not started")
	// ErrOverloaded reports that a reactor or channel queue cannot accept more work.
	ErrOverloaded = errors.New("channelplane: overloaded")
	// ErrInvalidRequest reports a malformed append request.
	ErrInvalidRequest = errors.New("channelplane: invalid request")
	// ErrNoRemoteAppender reports that a remote channel leader cannot be reached by this plane.
	ErrNoRemoteAppender = errors.New("channelplane: no remote appender")
	// ErrPeerBackpressured reports that a remote peer lane cannot accept more appends.
	ErrPeerBackpressured = errors.New("channelplane: peer backpressured")
	// ErrStaleRoute reports that an effect completed for a route epoch that is no longer current.
	ErrStaleRoute = errors.New("channelplane: stale route")
)
