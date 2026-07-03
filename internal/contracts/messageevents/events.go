// Package messageevents defines message usecase event contracts.
package messageevents

import "github.com/WuKongIM/WuKongIM/pkg/legacy/channel"

// MessageCommitted describes a durable channel-log message ready for side effects.
type MessageCommitted struct {
	// Message is the durable channel-log record committed by the message usecase.
	Message channel.Message
	// SenderSessionID identifies the sender session to skip during realtime fanout.
	SenderSessionID uint64
	// MessageScopedUIDs contains request-scoped subscribers used only for this message dispatch.
	MessageScopedUIDs []string
	// CMDConversationIntentSubmitted reports that request-scoped CMD sync intent was fully accepted.
	CMDConversationIntentSubmitted bool
}

// Clone returns an event copy safe for fanout subscribers to mutate independently.
func (e MessageCommitted) Clone() MessageCommitted {
	e.Message.Payload = append([]byte(nil), e.Message.Payload...)
	e.MessageScopedUIDs = append([]string(nil), e.MessageScopedUIDs...)
	return e
}
