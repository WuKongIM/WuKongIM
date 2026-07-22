package app

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
)

// channelAppendAuthorityLocal admits RPC-forwarded sends to the local authority reactor.
type channelAppendAuthorityLocal struct {
	group *channelappend.Group
}

func (l channelAppendAuthorityLocal) SubmitForAuthority(ctx context.Context, target channelappend.AuthorityTarget, items []channelappend.SendBatchItem) []channelappend.SendBatchItemResult {
	if l.group == nil {
		return channelAppendErrorResults(len(items), channelappend.ErrRouteNotReady)
	}
	future, err := l.group.SubmitLocal(ctx, target, items)
	if err != nil {
		return channelAppendErrorResults(len(items), err)
	}
	results, err := future.Wait(ctx)
	if err != nil {
		return channelAppendErrorResults(len(items), err)
	}
	if len(results) != len(items) {
		return channelAppendErrorResults(len(items), channelappend.ErrAppendResultMissing)
	}
	return results
}

// channelAppendSubscriberSource pages durable channel subscribers for channelappend.
type channelAppendSubscriberSource struct {
	node recipientSubscriberNode
}

func (s channelAppendSubscriberSource) NextSubscriberPage(ctx context.Context, req channelappend.SubscriberPageRequest) (channelappend.SubscriberPage, error) {
	if s.node == nil {
		return channelappend.SubscriberPage{Done: true}, nil
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 1
	}
	uids, cursor, done, err := s.node.ListChannelSubscribersPage(ctx, req.ChannelID.ID, int64(req.ChannelID.Type), req.Cursor, limit)
	if err != nil {
		return channelappend.SubscriberPage{}, err
	}
	recipients := make([]channelappend.Recipient, 0, len(uids))
	for _, uid := range uids {
		if uid != "" {
			recipients = append(recipients, channelappend.Recipient{UID: uid})
		}
	}
	return channelappend.SubscriberPage{Recipients: recipients, Cursor: cursor, Done: done}, nil
}

// channelAppendDeliverySubscriberSource adapts app-level delivery subscriber scans to channelappend.
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
		ChannelID:   req.ChannelID.ID,
		ChannelType: req.ChannelID.Type,
		Cursor:      req.Cursor,
		Limit:       limit,
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

// channelAppendOwnerPusher adapts owner-node delivery pushes to channelappend.
type channelAppendOwnerPusher struct {
	next     runtimedelivery.Pusher
	observer runtimedelivery.Observer
}

func (p channelAppendOwnerPusher) Push(ctx context.Context, cmd channelappend.PushCommand) (channelappend.PushResult, error) {
	startedAt := time.Now()
	defer func() {
		if recovered := recover(); recovered != nil {
			p.observe(
				cmd.OwnerNodeID,
				len(cmd.Routes),
				channelappend.PushResult{},
				fmt.Errorf("channel append owner push panic: %v", recovered),
				time.Since(startedAt),
			)
			panic(recovered)
		}
	}()
	if p.next == nil {
		result := channelappend.PushResult{Dropped: append([]channelappend.Route(nil), cmd.Routes...)}
		p.observe(cmd.OwnerNodeID, len(cmd.Routes), result, nil, time.Since(startedAt))
		return result, nil
	}
	result, err := p.next.Push(ctx, runtimedelivery.PushCommand{
		OwnerNodeID: cmd.OwnerNodeID,
		Envelope:    deliveryEnvelopeFromChannelAppend(cmd.Envelope),
		Routes:      deliveryRoutesFromChannelAppend(cmd.Routes),
	})
	if err != nil {
		p.observe(cmd.OwnerNodeID, len(cmd.Routes), channelappend.PushResult{}, err, time.Since(startedAt))
		return channelappend.PushResult{}, err
	}
	converted := channelappend.PushResult{
		Accepted:  channelAppendRoutesFromDelivery(result.Accepted),
		Retryable: channelAppendRoutesFromDelivery(result.Retryable),
		Dropped:   channelAppendRoutesFromDelivery(result.Dropped),
	}
	p.observe(cmd.OwnerNodeID, len(cmd.Routes), converted, nil, time.Since(startedAt))
	return converted, nil
}

func (p channelAppendOwnerPusher) observe(
	ownerNodeID uint64,
	routes int,
	result channelappend.PushResult,
	err error,
	duration time.Duration,
) {
	if p.observer == nil {
		return
	}
	resultLabel, errorClass := runtimedelivery.ClassifyPushObservation(len(result.Retryable), len(result.Dropped), err)
	p.observer.ObserveFanoutPush(runtimedelivery.FanoutPushEvent{
		OwnerNodeID: ownerNodeID,
		Result:      resultLabel,
		ErrorClass:  errorClass,
		Duration:    duration,
		Routes:      routes,
		Accepted:    len(result.Accepted),
		Retryable:   len(result.Retryable),
		Dropped:     len(result.Dropped),
	})
}

func channelAppendErrorResults(n int, err error) []channelappend.SendBatchItemResult {
	results := make([]channelappend.SendBatchItemResult, n)
	for i := range results {
		results[i].Err = err
	}
	return results
}

func deliveryEnvelopeFromChannelAppend(in channelappend.CommittedEnvelope) runtimedelivery.Envelope {
	return runtimedelivery.Envelope{
		MessageID:         in.MessageID,
		MessageSeq:        in.MessageSeq,
		ChannelID:         in.ChannelID,
		ChannelType:       in.ChannelType,
		FromUID:           in.FromUID,
		SenderNodeID:      in.SenderNodeID,
		SenderSessionID:   in.SenderSessionID,
		ClientMsgNo:       in.ClientMsgNo,
		RedDot:            in.RedDot,
		Payload:           append([]byte(nil), in.Payload...),
		MessageScopedUIDs: append([]string(nil), in.MessageScopedUIDs...),
	}
}

func deliveryRoutesFromChannelAppend(in []channelappend.Route) []runtimedelivery.Route {
	out := make([]runtimedelivery.Route, 0, len(in))
	for _, route := range in {
		out = append(out, runtimedelivery.Route{
			UID:         route.UID,
			OwnerNodeID: route.OwnerNodeID,
			OwnerBootID: route.OwnerBootID,
			OwnerSeq:    route.OwnerSeq,
			SessionID:   route.SessionID,
			DeviceID:    route.DeviceID,
			DeviceFlag:  route.DeviceFlag,
			DeviceLevel: route.DeviceLevel,
		})
	}
	return out
}

func channelAppendRoutesFromDelivery(in []runtimedelivery.Route) []channelappend.Route {
	out := make([]channelappend.Route, 0, len(in))
	for _, route := range in {
		out = append(out, channelappend.Route{
			UID:         route.UID,
			OwnerNodeID: route.OwnerNodeID,
			OwnerBootID: route.OwnerBootID,
			OwnerSeq:    route.OwnerSeq,
			SessionID:   route.SessionID,
			DeviceID:    route.DeviceID,
			DeviceFlag:  route.DeviceFlag,
			DeviceLevel: route.DeviceLevel,
		})
	}
	return out
}
