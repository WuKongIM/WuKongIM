package app

import (
	"context"
	"errors"
	"fmt"

	channelusecase "github.com/WuKongIM/WuKongIM/internal/usecase/channel"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// managerChannelBusinessOperator adapts the channel usecase to the management-owned port.
type managerChannelBusinessOperator struct {
	channels *channelusecase.App
}

func newManagerChannelBusinessOperator(channels *channelusecase.App) managementusecase.ChannelBusinessOperator {
	if channels == nil {
		return nil
	}
	return managerChannelBusinessOperator{channels: channels}
}

func (o managerChannelBusinessOperator) GetMetadata(ctx context.Context, key managementusecase.BusinessChannelKey) (metadb.Channel, error) {
	return normalizeManagerChannelBusinessResult(o.channels.GetMetadata(ctx, channelBusinessKey(key)))
}

func (o managerChannelBusinessOperator) CreateMetadata(ctx context.Context, info managementusecase.BusinessChannelInfo) error {
	return normalizeManagerChannelBusinessError(o.channels.CreateMetadata(ctx, channelusecase.Info{
		ChannelID:   info.ChannelID,
		ChannelType: info.ChannelType,
		Ban:         info.Ban,
		Disband:     info.Disband,
		SendBan:     info.SendBan,
	}))
}

func (o managerChannelBusinessOperator) PatchMetadataFlags(ctx context.Context, key managementusecase.BusinessChannelKey, flags managementusecase.BusinessChannelFlags) error {
	return normalizeManagerChannelBusinessError(o.channels.PatchMetadataFlags(ctx, channelBusinessKey(key), channelusecase.BusinessFlags{
		Ban: flags.Ban, Disband: flags.Disband, SendBan: flags.SendBan,
	}))
}

func (o managerChannelBusinessOperator) HasSubscribers(ctx context.Context, key managementusecase.BusinessChannelKey) (bool, error) {
	return normalizeManagerChannelBusinessResult(o.channels.HasSubscribers(ctx, channelBusinessKey(key)))
}

func (o managerChannelBusinessOperator) HasAllowlist(ctx context.Context, key managementusecase.BusinessChannelKey) (bool, error) {
	return normalizeManagerChannelBusinessResult(o.channels.HasAllowlist(ctx, channelBusinessKey(key)))
}

func (o managerChannelBusinessOperator) HasDenylist(ctx context.Context, key managementusecase.BusinessChannelKey) (bool, error) {
	return normalizeManagerChannelBusinessResult(o.channels.HasDenylist(ctx, channelBusinessKey(key)))
}

func (o managerChannelBusinessOperator) ContainsSubscriber(ctx context.Context, key managementusecase.BusinessChannelKey, uid string) (bool, error) {
	return normalizeManagerChannelBusinessResult(o.channels.ContainsSubscriber(ctx, channelBusinessKey(key), uid))
}

func (o managerChannelBusinessOperator) ContainsAllowlistMember(ctx context.Context, key managementusecase.BusinessChannelKey, uid string) (bool, error) {
	return normalizeManagerChannelBusinessResult(o.channels.ContainsAllowlistMember(ctx, channelBusinessKey(key), uid))
}

func (o managerChannelBusinessOperator) ContainsDenylistMember(ctx context.Context, key managementusecase.BusinessChannelKey, uid string) (bool, error) {
	return normalizeManagerChannelBusinessResult(o.channels.ContainsDenylistMember(ctx, channelBusinessKey(key), uid))
}

func (o managerChannelBusinessOperator) ListSubscribersPage(ctx context.Context, req managementusecase.BusinessChannelMemberPageRequest) (managementusecase.BusinessChannelMemberPageResult, error) {
	return o.listMemberPage(ctx, req, o.channels.ListSubscribersPage)
}

func (o managerChannelBusinessOperator) ListAllowlistPage(ctx context.Context, req managementusecase.BusinessChannelMemberPageRequest) (managementusecase.BusinessChannelMemberPageResult, error) {
	return o.listMemberPage(ctx, req, o.channels.ListAllowlistPage)
}

func (o managerChannelBusinessOperator) ListDenylistPage(ctx context.Context, req managementusecase.BusinessChannelMemberPageRequest) (managementusecase.BusinessChannelMemberPageResult, error) {
	return o.listMemberPage(ctx, req, o.channels.ListDenylistPage)
}

func (o managerChannelBusinessOperator) MutateSubscribersCounted(ctx context.Context, key managementusecase.BusinessChannelKey, uids []string, add bool) (metadb.SubscriberMutationResult, error) {
	return normalizeManagerChannelBusinessResult(o.channels.MutateSubscribersCounted(ctx, channelusecase.SubscriberCommand{
		ChannelID: key.ChannelID, ChannelType: key.ChannelType, Subscribers: uids,
	}, add))
}

func (o managerChannelBusinessOperator) MutateAllowlistCounted(ctx context.Context, key managementusecase.BusinessChannelKey, uids []string, add bool) (metadb.SubscriberMutationResult, error) {
	return normalizeManagerChannelBusinessResult(o.channels.MutateAllowlistCounted(ctx, channelusecase.MemberCommand{
		ChannelKey: channelBusinessKey(key), UIDs: uids,
	}, add))
}

func (o managerChannelBusinessOperator) MutateDenylistCounted(ctx context.Context, key managementusecase.BusinessChannelKey, uids []string, add bool) (metadb.SubscriberMutationResult, error) {
	return normalizeManagerChannelBusinessResult(o.channels.MutateDenylistCounted(ctx, channelusecase.MemberCommand{
		ChannelKey: channelBusinessKey(key), UIDs: uids,
	}, add))
}

func (o managerChannelBusinessOperator) listMemberPage(
	ctx context.Context,
	req managementusecase.BusinessChannelMemberPageRequest,
	list func(context.Context, channelusecase.MemberListPageRequest) (channelusecase.MemberListPageResult, error),
) (managementusecase.BusinessChannelMemberPageResult, error) {
	page, err := list(ctx, channelusecase.MemberListPageRequest{
		ChannelKey: channelBusinessKey(req.BusinessChannelKey),
		AfterUID:   req.AfterUID,
		Limit:      req.Limit,
	})
	if err != nil {
		return managementusecase.BusinessChannelMemberPageResult{}, normalizeManagerChannelBusinessError(err)
	}
	result := managementusecase.BusinessChannelMemberPageResult{
		UIDs:       make([]string, 0, len(page.Members)),
		NextCursor: page.NextCursor,
		HasMore:    page.HasMore,
	}
	for _, member := range page.Members {
		result.UIDs = append(result.UIDs, member.UID)
	}
	return result, nil
}

func channelBusinessKey(key managementusecase.BusinessChannelKey) channelusecase.ChannelKey {
	return channelusecase.ChannelKey{ChannelID: key.ChannelID, ChannelType: key.ChannelType}
}

func normalizeManagerChannelBusinessResult[T any](value T, err error) (T, error) {
	return value, normalizeManagerChannelBusinessError(err)
}

func normalizeManagerChannelBusinessError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, clusterpkg.ErrNotStarted),
		errors.Is(err, clusterpkg.ErrStopping),
		errors.Is(err, clusterpkg.ErrRouteNotReady),
		errors.Is(err, clusterpkg.ErrNoSlotLeader),
		errors.Is(err, clusterpkg.ErrNotLeader),
		errors.Is(err, clusterpkg.ErrSlotNotFound),
		errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: %w", managementusecase.ErrBusinessChannelAuthorityUnavailable, err)
	default:
		return err
	}
}
