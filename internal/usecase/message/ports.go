package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
)

// Submitter accepts channel append commands and returns item-aligned results.
type Submitter = channelappend.Submitter

// SendHook runs before an accepted send command enters channel append.
type SendHook interface {
	// BeforeSend returns a possibly mutated command or a rejection reason.
	BeforeSend(context.Context, SendCommand) (SendCommand, Reason, error)
}

// ChannelMessageReader owns compatible channel message sync reads.
type ChannelMessageReader interface {
	// SyncMessages returns one authoritative channel message page.
	SyncMessages(context.Context, ChannelMessageQuery) (ChannelMessagePage, error)
}
