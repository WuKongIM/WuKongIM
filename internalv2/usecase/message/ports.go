package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/channelappend"
)

// Submitter accepts channel append commands and returns item-aligned results.
type Submitter = channelappend.Submitter

// ChannelMessageReader owns compatible channel message sync reads.
type ChannelMessageReader interface {
	// SyncMessages returns one authoritative channel message page.
	SyncMessages(context.Context, ChannelMessageQuery) (ChannelMessagePage, error)
}
