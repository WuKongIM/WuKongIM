package messageevents

import "github.com/WuKongIM/WuKongIM/pkg/legacy/channel"

// MessageRealtime describes a non-durable message ready for realtime delivery.
type MessageRealtime struct {
	// Message is the transient message record delivered only through realtime routing.
	Message channel.Message
	// SenderSessionID identifies the sender session to skip during realtime fanout.
	SenderSessionID uint64
	// MessageScopedUIDs contains request-scoped subscribers used only for this realtime dispatch.
	MessageScopedUIDs []string
}

// Clone returns an event copy safe for downstream dispatchers to mutate independently.
func (e MessageRealtime) Clone() MessageRealtime {
	e.Message.Payload = append([]byte(nil), e.Message.Payload...)
	e.MessageScopedUIDs = append([]string(nil), e.MessageScopedUIDs...)
	return e
}
