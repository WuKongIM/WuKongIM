package app

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/channelwrite"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internalv2/runtime/delivery"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
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
		return channelwrite.RecipientAuthorityTarget{}, channelWriteRouteError(err)
	}
	if route.Leader == 0 {
		return channelwrite.RecipientAuthorityTarget{}, channelwrite.ErrRouteNotReady
	}
	return channelwrite.RecipientAuthorityTarget{
		HashSlot:       route.HashSlot,
		SlotID:         route.SlotID,
		LeaderNodeID:   route.Leader,
		RouteRevision:  route.Revision,
		AuthorityEpoch: route.AuthorityEpoch,
	}, nil
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

// channelWriteConversationProjector forwards recipient conversation patches to conversation authority routing.
type channelWriteConversationProjector struct {
	client interface {
		AdmitPatches(context.Context, []conversationusecase.ActivePatch) error
	}
}

func (p channelWriteConversationProjector) AdmitRecipientPatches(ctx context.Context, patches []channelwrite.ConversationPatch) error {
	if p.client == nil || len(patches) == 0 {
		return nil
	}
	out := make([]conversationusecase.ActivePatch, 0, len(patches))
	for _, patch := range patches {
		out = append(out, conversationusecase.ActivePatch{
			UID:          patch.UID,
			ChannelID:    patch.ChannelID,
			ChannelType:  patch.ChannelType,
			ReadSeq:      patch.ReadSeq,
			DeletedToSeq: patch.DeletedToSeq,
			ActiveAt:     patch.ActiveAt,
			UpdatedAt:    patch.UpdatedAt,
			SparseActive: patch.SparseActive,
			MessageSeq:   patch.MessageSeq,
		})
	}
	return p.client.AdmitPatches(ctx, out)
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
		return channelwrite.ErrNotLeader
	case errors.Is(err, clusterv2.ErrRouteNotReady), errors.Is(err, clusterv2.ErrNoSlotLeader):
		return channelwrite.ErrRouteNotReady
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
