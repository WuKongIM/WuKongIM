package app

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
)

// channelAppendDeliverySubscriberSource is retained until the public test
// option moves from the legacy subscriber DTO to channelappend.
type channelAppendDeliverySubscriberSource struct {
	source runtimedelivery.ChannelSubscriberSource
}

func (s channelAppendDeliverySubscriberSource) NextSubscriberPage(ctx context.Context, req channelappend.SubscriberPageRequest) (channelappend.SubscriberPage, error) {
	if s.source == nil {
		return channelappend.SubscriberPage{Done: true}, nil
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 1
	}
	page, err := s.source.ListSubscribers(ctx, runtimedelivery.SubscriberPageRequest{
		ChannelID: req.ChannelID.ID, ChannelType: req.ChannelID.Type, Cursor: req.Cursor, Limit: limit,
	})
	if err != nil {
		return channelappend.SubscriberPage{}, err
	}
	recipients := make([]channelappend.Recipient, 0, len(page.UIDs))
	for _, uid := range page.UIDs {
		if uid != "" {
			recipients = append(recipients, channelappend.Recipient{UID: uid})
		}
	}
	return channelappend.SubscriberPage{Recipients: recipients, Cursor: page.NextCursor, Done: page.Done}, nil
}
