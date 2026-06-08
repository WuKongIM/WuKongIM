package message

import "context"

// IdempotencyQuery identifies one canonical sender/client message key.
type IdempotencyQuery struct {
	// FromUID is the authenticated sender UID.
	FromUID string
	// ClientMsgNo is the sender-provided idempotency key.
	ClientMsgNo string
	// ChannelID is the canonical channel ID after request normalization.
	ChannelID string
	// ChannelType is the canonical channel type.
	ChannelType uint8
}

// SendIdempotencyLookup recovers a previously committed send result.
type SendIdempotencyLookup interface {
	// LookupSend returns a prior result for a canonical sender/client key.
	LookupSend(context.Context, IdempotencyQuery) (SendResult, bool, error)
}
