package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/channelwrite"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
	presenceusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

// channelWriteAuthorityLocal admits RPC-forwarded sends to the local authority reactor.
type channelWriteAuthorityLocal struct {
	group *channelwrite.Group
}

func (l channelWriteAuthorityLocal) SubmitForAuthority(ctx context.Context, target channelwrite.AuthorityTarget, items []channelwrite.SendBatchItem) []channelwrite.SendBatchItemResult {
	if l.group == nil {
		return channelWriteErrorResults(len(items), channelwrite.ErrRouteNotReady)
	}
	future, err := l.group.SubmitLocal(ctx, target, items)
	if err != nil {
		return channelWriteErrorResults(len(items), err)
	}
	results, err := future.Wait(ctx)
	if err != nil {
		return channelWriteErrorResults(len(items), err)
	}
	if len(results) != len(items) {
		return channelWriteErrorResults(len(items), channelwrite.ErrAppendResultMissing)
	}
	return results
}

// channelWriteRecipientRouter applies recipient batches with the channelwrite recipient processor.
type channelWriteRecipientRouter struct {
	processor *channelwrite.RecipientProcessor
}

func (r channelWriteRecipientRouter) DispatchRecipientBatch(ctx context.Context, _ channelwrite.RecipientAuthorityTarget, batch channelwrite.RecipientBatch) error {
	if r.processor == nil {
		return nil
	}
	return r.processor.ProcessRecipientBatch(ctx, batch)
}

// channelWriteRecipientResolver resolves UID authority targets from clusterv2 hash-slot routes.
type channelWriteRecipientResolver struct {
	node recipientAuthorityRouteNode
}

type recipientAuthorityRouteNode interface {
	RouteKey(string) (clusterv2.Route, error)
}

type recipientAuthorityBatchRouteNode interface {
	RouteKeys([]string) ([]clusterv2.Route, error)
}

func (r channelWriteRecipientResolver) ResolveRecipientAuthority(ctx context.Context, uid string) (channelwrite.RecipientAuthorityTarget, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return channelwrite.RecipientAuthorityTarget{}, err
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return channelwrite.RecipientAuthorityTarget{}, err
	}
	if r.node == nil {
		return channelwrite.RecipientAuthorityTarget{}, channelwrite.ErrRouteNotReady
	}
	route, err := r.node.RouteKey(uid)
	if err != nil {
		return channelwrite.RecipientAuthorityTarget{}, fmt.Errorf("recipient route key uid=%q: %w", uid, channelWriteRouteError(err))
	}
	return channelWriteRecipientTargetFromRoute(route)
}

func (r channelWriteRecipientResolver) ResolveRecipientAuthorities(ctx context.Context, uids []string) (map[string]channelwrite.RecipientAuthorityTarget, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.node == nil {
		return nil, channelwrite.ErrRouteNotReady
	}
	if batchNode, ok := r.node.(recipientAuthorityBatchRouteNode); ok {
		routes, err := batchNode.RouteKeys(uids)
		if err != nil {
			return nil, fmt.Errorf("recipient route keys uidCount=%d sampleUID=%q: %w", len(uids), firstUIDForRouteLog(uids), channelWriteRouteError(err))
		}
		if len(routes) != len(uids) {
			return nil, fmt.Errorf("recipient route keys returned %d routes for %d uids sampleUID=%q: %w", len(routes), len(uids), firstUIDForRouteLog(uids), channelwrite.ErrRouteNotReady)
		}
		targets := make(map[string]channelwrite.RecipientAuthorityTarget, len(uids))
		for i, uid := range uids {
			target, err := channelWriteRecipientTargetFromRoute(routes[i])
			if err != nil {
				return nil, fmt.Errorf("recipient route key uid=%q index=%d: %w", uid, i, err)
			}
			targets[uid] = target
		}
		return targets, nil
	}
	targets := make(map[string]channelwrite.RecipientAuthorityTarget, len(uids))
	for _, uid := range uids {
		target, err := r.ResolveRecipientAuthority(ctx, uid)
		if err != nil {
			return nil, err
		}
		targets[uid] = target
	}
	return targets, nil
}

func channelWriteRecipientTargetFromRoute(route clusterv2.Route) (channelwrite.RecipientAuthorityTarget, error) {
	if route.Leader == 0 {
		return channelwrite.RecipientAuthorityTarget{}, fmt.Errorf("recipient route has no leader hashSlot=%d slotID=%d revision=%d authorityEpoch=%d: %w", route.HashSlot, route.SlotID, route.Revision, route.AuthorityEpoch, channelwrite.ErrRouteNotReady)
	}
	return channelwrite.RecipientAuthorityTarget{
		HashSlot:       route.HashSlot,
		SlotID:         route.SlotID,
		LeaderNodeID:   route.Leader,
		RouteRevision:  route.Revision,
		AuthorityEpoch: route.AuthorityEpoch,
	}, nil
}

func firstUIDForRouteLog(uids []string) string {
	if len(uids) == 0 {
		return ""
	}
	return uids[0]
}

// channelWriteSubscriberSource pages durable channel subscribers for channelwrite.
type channelWriteSubscriberSource struct {
	node recipientSubscriberNode
}

func (s channelWriteSubscriberSource) NextSubscriberPage(ctx context.Context, req channelwrite.SubscriberPageRequest) (channelwrite.SubscriberPage, error) {
	if s.node == nil {
		return channelwrite.SubscriberPage{Done: true}, nil
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 1
	}
	uids, cursor, done, err := s.node.ListChannelSubscribersPage(ctx, req.ChannelID.ID, int64(req.ChannelID.Type), req.Cursor, limit)
	if err != nil {
		return channelwrite.SubscriberPage{}, err
	}
	recipients := make([]channelwrite.Recipient, 0, len(uids))
	for _, uid := range uids {
		if uid != "" {
			recipients = append(recipients, channelwrite.Recipient{UID: uid})
		}
	}
	return channelwrite.SubscriberPage{Recipients: recipients, Cursor: cursor, Done: done}, nil
}

// channelWriteDeliverySubscriberSource adapts app-level delivery subscriber scans to channelwrite.
type channelWriteDeliverySubscriberSource struct {
	source runtimedelivery.ChannelSubscriberSource
}

func (s channelWriteDeliverySubscriberSource) NextSubscriberPage(ctx context.Context, req channelwrite.SubscriberPageRequest) (channelwrite.SubscriberPage, error) {
	if s.source == nil {
		return channelwrite.SubscriberPage{Done: true}, nil
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
		return channelwrite.SubscriberPage{}, err
	}
	recipients := make([]channelwrite.Recipient, 0, len(page.UIDs))
	for _, uid := range page.UIDs {
		if uid != "" {
			recipients = append(recipients, channelwrite.Recipient{UID: uid})
		}
	}
	return channelwrite.SubscriberPage{Recipients: recipients, Cursor: page.NextCursor, Done: page.Done}, nil
}

// channelWritePresenceResolver adapts presence lookups to channelwrite flat routes.
type channelWritePresenceResolver struct {
	presence *presenceusecase.App
}

func (r channelWritePresenceResolver) EndpointsByUIDs(ctx context.Context, uids []string) ([]channelwrite.Route, error) {
	if r.presence == nil || len(uids) == 0 {
		return nil, nil
	}
	routesByUID, err := r.presence.EndpointsByUIDs(ctx, uids)
	if err != nil {
		return nil, err
	}
	out := make([]channelwrite.Route, 0)
	for _, routes := range routesByUID {
		for _, route := range routes {
			out = append(out, channelwrite.Route{
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
	}
	return out, nil
}

// channelWriteOwnerPusher adapts owner-node delivery pushes to channelwrite.
type channelWriteOwnerPusher struct {
	next runtimedelivery.Pusher
}

func (p channelWriteOwnerPusher) Push(ctx context.Context, cmd channelwrite.PushCommand) (channelwrite.PushResult, error) {
	if p.next == nil {
		return channelwrite.PushResult{Dropped: append([]channelwrite.Route(nil), cmd.Routes...)}, nil
	}
	result, err := p.next.Push(ctx, runtimedelivery.PushCommand{
		OwnerNodeID: cmd.OwnerNodeID,
		Envelope:    deliveryEnvelopeFromChannelWrite(cmd.Envelope),
		Routes:      deliveryRoutesFromChannelWrite(cmd.Routes),
	})
	if err != nil {
		return channelwrite.PushResult{}, err
	}
	return channelwrite.PushResult{
		Accepted:  channelWriteRoutesFromDelivery(result.Accepted),
		Retryable: channelWriteRoutesFromDelivery(result.Retryable),
		Dropped:   channelWriteRoutesFromDelivery(result.Dropped),
	}, nil
}

func channelWriteErrorResults(n int, err error) []channelwrite.SendBatchItemResult {
	results := make([]channelwrite.SendBatchItemResult, n)
	for i := range results {
		results[i].Err = err
	}
	return results
}

func channelWriteRouteError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, clusterv2.ErrNotLeader):
		return fmt.Errorf("%w: %w", channelwrite.ErrNotLeader, err)
	case errors.Is(err, clusterv2.ErrRouteNotReady), errors.Is(err, clusterv2.ErrNoSlotLeader):
		return fmt.Errorf("%w: %w", channelwrite.ErrRouteNotReady, err)
	default:
		return err
	}
}

func deliveryEnvelopeFromChannelWrite(in channelwrite.CommittedEnvelope) runtimedelivery.Envelope {
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

func deliveryRoutesFromChannelWrite(in []channelwrite.Route) []runtimedelivery.Route {
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

func channelWriteRoutesFromDelivery(in []runtimedelivery.Route) []channelwrite.Route {
	out := make([]channelwrite.Route, 0, len(in))
	for _, route := range in {
		out = append(out, channelwrite.Route{
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
