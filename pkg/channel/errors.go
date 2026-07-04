package channel

import (
	"errors"
	"strings"
)

const (
	channelErrorPrefix       = "channel:"
	legacyChannelErrorPrefix = "channelv2:"
)

var (
	// ErrInvalidConfig reports invalid construction or authoritative metadata.
	ErrInvalidConfig = errors.New("channel: invalid config")
	// ErrBackpressured reports that a bounded queue rejected new work.
	ErrBackpressured = errors.New("channel: backpressured")
	// ErrNotLeader reports that a write or leader RPC reached a non-leader node.
	ErrNotLeader = errors.New("channel: not leader")
	// ErrNotReady reports that a channel is not ready to serve the request.
	ErrNotReady = errors.New("channel: not ready")
	// ErrStaleMeta reports that a request was fenced by newer channel metadata.
	ErrStaleMeta = errors.New("channel: stale meta")
	// ErrWriteFenced reports that durable control-plane metadata is blocking new writes.
	ErrWriteFenced = errors.New("channel: write fenced")
	// ErrChannelNotFound reports that a channel is unknown or deleted locally.
	ErrChannelNotFound = errors.New("channel: channel not found")
	// ErrNotReplica reports that the local or requesting node is outside the channel replica set.
	ErrNotReplica = errors.New("channel: not replica")
	// ErrClosed reports that the cluster or one of its bounded workers is closed.
	ErrClosed = errors.New("channel: closed")
	// ErrTooManyChannels reports that local channel activation hit its limit.
	ErrTooManyChannels = errors.New("channel: too many channels")
)

// ErrorMatches reports whether err is or contains a promoted channel error, while
// still accepting legacy channelv2 remote error text during rolling upgrades.
func ErrorMatches(err error, sentinel error) bool {
	return errors.Is(err, sentinel) || (err != nil && ErrorMessageMatches(err.Error(), sentinel))
}

// ErrorMessageMatches reports whether message contains a promoted or legacy
// textual form for a channel error sentinel.
func ErrorMessageMatches(message string, sentinel error) bool {
	if message == "" || sentinel == nil {
		return false
	}
	if strings.Contains(message, sentinel.Error()) {
		return true
	}
	legacy := legacyErrorMessage(sentinel)
	return legacy != "" && strings.Contains(message, legacy)
}

// TrimErrorMessagePrefix removes a promoted or legacy channel error prefix from
// message when it exactly prefixes the remote error detail.
func TrimErrorMessagePrefix(message string, sentinel error) string {
	if message == "" || sentinel == nil {
		return message
	}
	for _, prefix := range []string{sentinel.Error(), legacyErrorMessage(sentinel)} {
		if prefix == "" {
			continue
		}
		if message == prefix {
			return ""
		}
		if strings.HasPrefix(message, prefix+": ") {
			return strings.TrimPrefix(message, prefix+": ")
		}
	}
	return message
}

func legacyErrorMessage(sentinel error) string {
	if sentinel == nil {
		return ""
	}
	msg := sentinel.Error()
	if !strings.HasPrefix(msg, channelErrorPrefix) {
		return ""
	}
	return legacyChannelErrorPrefix + strings.TrimPrefix(msg, channelErrorPrefix)
}
