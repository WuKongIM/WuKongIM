package channel

import "errors"

var (
	// ErrInvalidConfig reports invalid construction or authoritative metadata.
	ErrInvalidConfig = errors.New("channelv2: invalid config")
	// ErrBackpressured reports that a bounded queue rejected new work.
	ErrBackpressured = errors.New("channelv2: backpressured")
	// ErrNotLeader reports that a write or leader RPC reached a non-leader node.
	ErrNotLeader = errors.New("channelv2: not leader")
	// ErrNotReady reports that a channel is not ready to serve the request.
	ErrNotReady = errors.New("channelv2: not ready")
	// ErrStaleMeta reports that a request was fenced by newer channel metadata.
	ErrStaleMeta = errors.New("channelv2: stale meta")
	// ErrWriteFenced reports that durable control-plane metadata is blocking new writes.
	ErrWriteFenced = errors.New("channelv2: write fenced")
	// ErrChannelNotFound reports that a channel is unknown or deleted locally.
	ErrChannelNotFound = errors.New("channelv2: channel not found")
	// ErrNotReplica reports that the local or requesting node is outside the channel replica set.
	ErrNotReplica = errors.New("channelv2: not replica")
	// ErrClosed reports that the cluster or one of its bounded workers is closed.
	ErrClosed = errors.New("channelv2: closed")
	// ErrTooManyChannels reports that local channel activation hit its limit.
	ErrTooManyChannels = errors.New("channelv2: too many channels")
)
