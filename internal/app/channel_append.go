package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	presenceusecase "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
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

// channelAppendRecipientResolver resolves UID authority targets from cluster hash-slot routes.
type channelAppendRecipientResolver struct {
	node recipientAuthorityRouteNode
}

type recipientAuthorityRouteNode interface {
	RouteKey(string) (cluster.Route, error)
}

type recipientAuthorityBatchNode interface {
	RouteAuthorities([]string) ([]cluster.RouteAuthority, error)
}

type recipientAuthorityPartialBatchNode interface {
	RouteAuthoritiesPartial([]string) ([]cluster.RouteAuthorityResult, error)
}

type recipientAuthorityBatchRouteNode interface {
	RouteKeys([]string) ([]cluster.Route, error)
}

type recipientAuthorityPartialBatchRouteNode interface {
	RouteKeysPartial([]string) ([]cluster.RouteKeyResult, error)
}

func (r channelAppendRecipientResolver) ResolveRecipientAuthority(ctx context.Context, uid string) (channelappend.RecipientAuthorityTarget, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return channelappend.RecipientAuthorityTarget{}, err
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return channelappend.RecipientAuthorityTarget{}, err
	}
	if r.node == nil {
		return channelappend.RecipientAuthorityTarget{}, channelappend.ErrRouteNotReady
	}
	route, err := r.node.RouteKey(uid)
	if err != nil {
		return channelappend.RecipientAuthorityTarget{}, fmt.Errorf("recipient route key uid=%q: %w", uid, channelAppendRouteError(err))
	}
	return channelAppendRecipientTargetFromRoute(route)
}

func (r channelAppendRecipientResolver) ResolveRecipientAuthorities(ctx context.Context, uids []string) ([]channelappend.RecipientAuthorityResult, error) {
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
		return nil, channelappend.ErrRouteNotReady
	}
	if authorityNode, ok := r.node.(recipientAuthorityPartialBatchNode); ok {
		authorities, err := authorityNode.RouteAuthoritiesPartial(uids)
		if err != nil {
			return nil, fmt.Errorf("recipient route authorities partial uidCount=%d sampleUID=%q: %w", len(uids), firstUIDForRouteLog(uids), channelAppendRouteError(err))
		}
		if len(authorities) != len(uids) {
			return nil, fmt.Errorf("recipient route authorities partial returned %d routes for %d uids sampleUID=%q: %w", len(authorities), len(uids), firstUIDForRouteLog(uids), channelappend.ErrRouteNotReady)
		}
		results := make([]channelappend.RecipientAuthorityResult, len(uids))
		for i, result := range authorities {
			if result.Err != nil {
				results[i].Err = channelAppendRouteError(result.Err)
				continue
			}
			target, err := channelAppendRecipientTargetFromAuthority(result.Authority)
			if err != nil {
				results[i].Err = err
				continue
			}
			results[i].Target = target
		}
		return results, nil
	}
	if authorityNode, ok := r.node.(recipientAuthorityBatchNode); ok {
		authorities, err := authorityNode.RouteAuthorities(uids)
		if err != nil {
			return nil, fmt.Errorf("recipient route authorities uidCount=%d sampleUID=%q: %w", len(uids), firstUIDForRouteLog(uids), channelAppendRouteError(err))
		}
		if len(authorities) != len(uids) {
			return nil, fmt.Errorf("recipient route authorities returned %d routes for %d uids sampleUID=%q: %w", len(authorities), len(uids), firstUIDForRouteLog(uids), channelappend.ErrRouteNotReady)
		}
		results := make([]channelappend.RecipientAuthorityResult, len(uids))
		for i := range uids {
			target, err := channelAppendRecipientTargetFromAuthority(authorities[i])
			if err != nil {
				results[i].Err = err
				continue
			}
			results[i].Target = target
		}
		return results, nil
	}
	if batchNode, ok := r.node.(recipientAuthorityPartialBatchRouteNode); ok {
		routes, err := batchNode.RouteKeysPartial(uids)
		if err != nil {
			return nil, fmt.Errorf("recipient route keys partial uidCount=%d sampleUID=%q: %w", len(uids), firstUIDForRouteLog(uids), channelAppendRouteError(err))
		}
		if len(routes) != len(uids) {
			return nil, fmt.Errorf("recipient route keys partial returned %d routes for %d uids sampleUID=%q: %w", len(routes), len(uids), firstUIDForRouteLog(uids), channelappend.ErrRouteNotReady)
		}
		results := make([]channelappend.RecipientAuthorityResult, len(uids))
		for i, result := range routes {
			if result.Err != nil {
				results[i].Err = channelAppendRouteError(result.Err)
				continue
			}
			target, err := channelAppendRecipientTargetFromRoute(result.Route)
			if err != nil {
				results[i].Err = err
				continue
			}
			results[i].Target = target
		}
		return results, nil
	}
	if batchNode, ok := r.node.(recipientAuthorityBatchRouteNode); ok {
		routes, err := batchNode.RouteKeys(uids)
		if err != nil {
			return nil, fmt.Errorf("recipient route keys uidCount=%d sampleUID=%q: %w", len(uids), firstUIDForRouteLog(uids), channelAppendRouteError(err))
		}
		if len(routes) != len(uids) {
			return nil, fmt.Errorf("recipient route keys returned %d routes for %d uids sampleUID=%q: %w", len(routes), len(uids), firstUIDForRouteLog(uids), channelappend.ErrRouteNotReady)
		}
		results := make([]channelappend.RecipientAuthorityResult, len(uids))
		for i := range uids {
			target, err := channelAppendRecipientTargetFromRoute(routes[i])
			if err != nil {
				results[i].Err = err
				continue
			}
			results[i].Target = target
		}
		return results, nil
	}
	results := make([]channelappend.RecipientAuthorityResult, len(uids))
	for i, uid := range uids {
		target, err := r.ResolveRecipientAuthority(ctx, uid)
		if err != nil {
			results[i].Err = err
			continue
		}
		results[i].Target = target
	}
	return results, nil
}

func channelAppendRecipientTargetFromRoute(route cluster.Route) (channelappend.RecipientAuthorityTarget, error) {
	return channelAppendRecipientTargetFromAuthority(cluster.RouteAuthority{
		HashSlot:       route.HashSlot,
		SlotID:         route.SlotID,
		LeaderNodeID:   route.Leader,
		LeaderTerm:     route.LeaderTerm,
		ConfigEpoch:    route.ConfigEpoch,
		RouteRevision:  route.Revision,
		AuthorityEpoch: route.AuthorityEpoch,
	})
}

func channelAppendRecipientTargetFromAuthority(authority cluster.RouteAuthority) (channelappend.RecipientAuthorityTarget, error) {
	if authority.LeaderNodeID == 0 {
		return channelappend.RecipientAuthorityTarget{}, fmt.Errorf("recipient route has no leader hashSlot=%d slotID=%d revision=%d authorityEpoch=%d: %w", authority.HashSlot, authority.SlotID, authority.RouteRevision, authority.AuthorityEpoch, channelappend.ErrRouteNotReady)
	}
	return channelappend.RecipientAuthorityTarget{
		HashSlot:       authority.HashSlot,
		SlotID:         authority.SlotID,
		LeaderNodeID:   authority.LeaderNodeID,
		LeaderTerm:     authority.LeaderTerm,
		ConfigEpoch:    authority.ConfigEpoch,
		RouteRevision:  authority.RouteRevision,
		AuthorityEpoch: authority.AuthorityEpoch,
	}, nil
}

func firstUIDForRouteLog(uids []string) string {
	if len(uids) == 0 {
		return ""
	}
	return uids[0]
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

// channelAppendPresenceResolver adapts presence lookups to channelappend flat routes.
type channelAppendPresenceResolver struct {
	presence *presenceusecase.App
}

func (r channelAppendPresenceResolver) EndpointsByUIDs(ctx context.Context, uids []string) ([]channelappend.Route, error) {
	if r.presence == nil || len(uids) == 0 {
		return nil, nil
	}
	routesByUID, err := r.presence.EndpointsByUIDs(ctx, uids)
	if err != nil {
		return nil, err
	}
	out := make([]channelappend.Route, 0)
	for _, routes := range routesByUID {
		out = appendChannelAppendRoutes(out, routes)
	}
	return out, nil
}

func (r channelAppendPresenceResolver) EndpointsByTargets(ctx context.Context, batches []channelappend.RecipientTargetBatch) []channelappend.RecipientTargetPresenceResult {
	results := make([]channelappend.RecipientTargetPresenceResult, len(batches))
	if len(batches) == 0 {
		return results
	}
	if r.presence == nil {
		for i := range results {
			results[i].Err = presenceusecase.ErrAuthorityUnavailable
		}
		return results
	}
	groups := make([]presenceusecase.EndpointLookupGroup, len(batches))
	for i, batch := range batches {
		uids := make([]string, 0, len(batch.Recipients))
		for _, recipient := range batch.Recipients {
			if recipient.UID != "" {
				uids = append(uids, recipient.UID)
			}
		}
		groups[i] = presenceusecase.EndpointLookupGroup{
			Target: presenceTargetFromRecipientTarget(batch.Target),
			UIDs:   uids,
		}
	}
	resolved := r.presence.EndpointsByTargets(ctx, groups)
	for i := range results {
		if i >= len(resolved) {
			results[i].Err = channelappend.ErrRecipientPresenceResultMissing
			continue
		}
		results[i].Err = resolved[i].Err
		results[i].Routes = channelAppendRoutesFromPresence(resolved[i].Routes)
	}
	return results
}

func presenceTargetFromRecipientTarget(target channelappend.RecipientAuthorityTarget) presenceusecase.RouteTarget {
	return presenceusecase.RouteTarget{
		HashSlot:       target.HashSlot,
		SlotID:         target.SlotID,
		LeaderNodeID:   target.LeaderNodeID,
		LeaderTerm:     target.LeaderTerm,
		ConfigEpoch:    target.ConfigEpoch,
		RouteRevision:  target.RouteRevision,
		AuthorityEpoch: target.AuthorityEpoch,
	}
}

func channelAppendRoutesFromPresence(routes []presenceusecase.Route) []channelappend.Route {
	out := make([]channelappend.Route, 0, len(routes))
	return appendChannelAppendRoutes(out, routes)
}

func appendChannelAppendRoutes(out []channelappend.Route, routes []presenceusecase.Route) []channelappend.Route {
	for _, route := range routes {
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

func channelAppendRouteError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, cluster.ErrNotLeader):
		return fmt.Errorf("%w: %w", channelappend.ErrNotLeader, err)
	case errors.Is(err, cluster.ErrRouteNotReady), errors.Is(err, cluster.ErrNoSlotLeader):
		return fmt.Errorf("%w: %w", channelappend.ErrRouteNotReady, err)
	default:
		return err
	}
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
