package channel

import "context"

// MQTTSourceActivator establishes replicated source protection on a loaded,
// recovered leader. It does not grant subscription permission or authorize SUBACK.
type MQTTSourceActivator interface {
	EnsureMQTTSource(context.Context, MQTTSourceRequest) (MQTTSourceSnapshot, error)
}

// MQTTSourceRequest fences admission against authoritative routing selected by
// the caller. MessageID must come from the server allocator; its positive
// timestamp must remain stable when retrying an uncertain activation.
type MQTTSourceRequest struct {
	ChannelID               ChannelID
	ExpectedChannelEpoch    uint64
	ExpectedLeaderEpoch     uint64
	ExpectedRouteGeneration uint64
	MessageID               uint64
	ServerTimestampMS       int64
}

// Valid requires explicit identity and every authority fence, including on the
// already-protected fast path. The caller never chooses committed progress.
func (r MQTTSourceRequest) Valid() bool {
	return r.ChannelID.ID != "" && r.ChannelID.Type != 0 && r.ExpectedChannelEpoch != 0 &&
		r.ExpectedLeaderEpoch != 0 && r.ExpectedRouteGeneration != 0 && r.MessageID != 0 && r.ServerTimestampMS > 0
}

// MQTTSourceSnapshot binds the immutable protection identity to a reactor-
// admitted committed boundary. A subscription must persist its own chosen start
// at CommittedThrough; StartAfter is not permission to replay earlier messages.
type MQTTSourceSnapshot struct {
	Generation       string
	StartAfter       uint64
	CommittedThrough uint64
}
