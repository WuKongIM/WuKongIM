package mqttsession

import (
	"context"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"slices"
)

// readAnchoredOriginals proves a bounded original range for a durable cursor.
// Callers bracket it with their owner and receive-permission checks. This read
// grants no admission, network-send or source-retirement authority.
func readAnchoredOriginals(ctx context.Context, metadata ReplayMetadata, channels AccountingChannels, cursor meta.MQTTDeliveryCursor, from, through uint64, limit, maxBytes int) (ch.MQTTReplayConsumerPage, error) {
	var empty ch.MQTTReplayConsumerPage
	request, valid := replaySourceRequest(meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: cursor.Key.SourceID, Generation: cursor.Key.SourceGeneration})
	if !valid {
		return empty, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	placement, err := metadata.ResolveChannelMetaFresh(ctx, request.ChannelID)
	if err != nil {
		return empty, err
	}
	if !validReplayPlacement(placement, request.ChannelID) {
		return empty, ErrEvidence
	}
	placement.Replicas, placement.ISR = slices.Clone(placement.Replicas), slices.Clone(placement.ISR)
	request.ExpectedChannelEpoch, request.ExpectedLeaderEpoch, request.ExpectedRouteGeneration = placement.Epoch, placement.LeaderEpoch, placement.RouteGeneration
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	plan, err := channels.PlanChannelMQTTReplay(ctx, request)
	if err != nil {
		return empty, err
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if !plan.ValidFor(request) || plan.Source.StartAfter > cursor.StartAfter || plan.Source.CommittedThrough < cursor.AccountedThrough {
		return empty, ErrEvidence
	}
	if !plan.HasAnchor {
		return empty, ch.ErrNotReady
	}
	if plan.Anchor.Anchor.Through < cursor.AccountedThrough {
		return empty, ErrEvidence
	}
	q := ch.MQTTReplayConsumerRequest{AnchorPosition: plan.Anchor.Manifest.LastOffset, Request: ch.MQTTReplayRequest{ChannelID: request.ChannelID, ExpectedChannelEpoch: request.ExpectedChannelEpoch, ExpectedLeaderEpoch: request.ExpectedLeaderEpoch, ExpectedRouteGeneration: request.ExpectedRouteGeneration, Range: ch.MQTTReplayRange{Generation: cursor.Key.SourceGeneration, From: from, Through: through, Limit: limit, MaxBytes: maxBytes}}}
	if !q.Valid() {
		return empty, ErrInvalid
	}
	page, err := channels.ReadChannelMQTTReplay(ctx, q)
	if err != nil {
		return empty, err
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if !page.ValidFor(request.ChannelID, q.Request.Range) || page.Before.StartAfter != plan.Source.StartAfter || (page.After.Through == plan.Anchor.Anchor.Through && page.After != plan.Anchor.Prefix()) {
		return empty, ErrEvidence
	}
	current, err := metadata.ResolveChannelMetaFresh(ctx, request.ChannelID)
	if err != nil {
		return empty, err
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if !validReplayPlacement(current, request.ChannelID) || ch.MQTTReplayCopyAuthority(current) != ch.MQTTReplayCopyAuthority(placement) || current.WriteFence != placement.WriteFence {
		return empty, ErrEvidence
	}
	return page, nil
}
