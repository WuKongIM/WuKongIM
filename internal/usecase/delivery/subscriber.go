package delivery

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type ChannelSubscriberStore interface {
	SnapshotChannelSubscribers(ctx context.Context, channelID string, channelType int64) ([]string, error)
	ListChannelSubscribers(ctx context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error)
}

type SnapshotToken struct {
	id       channel.ChannelID
	snapshot []string
}

type SubscriberResolver interface {
	BeginSnapshot(ctx context.Context, id channel.ChannelID) (SnapshotToken, error)
	NextPage(ctx context.Context, token SnapshotToken, cursor string, limit int) ([]string, string, bool, error)
}

type SubscriberResolverOptions struct {
	Store ChannelSubscriberStore
}

type subscriberResolver struct {
	store ChannelSubscriberStore
}

func NewSubscriberResolver(opts SubscriberResolverOptions) SubscriberResolver {
	return &subscriberResolver{store: opts.Store}
}

func (r *subscriberResolver) BeginSnapshot(ctx context.Context, id channel.ChannelID) (SnapshotToken, error) {
	token := SnapshotToken{id: id}
	if id.Type == frame.ChannelTypePerson {
		left, right, err := DecodePersonChannel(id.ID)
		if err != nil {
			return SnapshotToken{}, err
		}
		token.snapshot = []string{left, right}
		return token, nil
	}
	if r.store == nil {
		return token, nil
	}
	snapshot, err := r.store.SnapshotChannelSubscribers(ctx, id.ID, int64(id.Type))
	if err != nil {
		return SnapshotToken{}, err
	}
	token.snapshot = snapshot
	return token, nil
}

func (r *subscriberResolver) NextPage(ctx context.Context, token SnapshotToken, cursor string, limit int) ([]string, string, bool, error) {
	if token.snapshot != nil {
		return nextSubscriberSnapshotPage(token.snapshot, cursor, limit)
	}
	if r.store == nil {
		return nil, cursor, true, nil
	}
	return r.store.ListChannelSubscribers(ctx, token.id.ID, int64(token.id.Type), cursor, limit)
}

func nextSubscriberSnapshotPage(uids []string, cursor string, limit int) ([]string, string, bool, error) {
	if limit <= 0 {
		return nil, "", false, ErrInvalidPersonChannel
	}
	start := 0
	if cursor != "" {
		for i, uid := range uids {
			if uid == cursor {
				start = i + 1
				break
			}
		}
	}
	if start >= len(uids) {
		return nil, cursor, true, nil
	}
	end := start + limit
	if end > len(uids) {
		end = len(uids)
	}
	page := append([]string(nil), uids[start:end]...)
	nextCursor := cursor
	if len(page) > 0 {
		nextCursor = page[len(page)-1]
	}
	return page, nextCursor, end >= len(uids), nil
}
