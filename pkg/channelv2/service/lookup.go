package service

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

// LookupCommittedMessage returns a locally visible committed message by id.
func (c *cluster) LookupCommittedMessage(ctx context.Context, id ch.ChannelID, messageID uint64) (ch.Message, bool, error) {
	if c == nil || c.group == nil {
		return ch.Message{}, false, ch.ErrClosed
	}
	return c.group.LookupCommittedMessage(ctx, id, messageID)
}
