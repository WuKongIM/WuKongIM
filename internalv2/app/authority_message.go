package app

import (
	"context"
	"errors"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
)

// senderAuthorityLocal validates fenced sender-authority RPC requests before local SEND submission.
type senderAuthorityLocal struct {
	localNodeID uint64
	resolver    message.UIDAuthorityResolver
	routes      clusterWriteReadyRuntime
	submitter   message.SenderAuthoritySubmitter
}

func (s senderAuthorityLocal) SendBatchForAuthority(ctx context.Context, target authority.Target, items []message.SendBatchItem) []message.SendBatchItemResult {
	results := make([]message.SendBatchItemResult, len(items))
	if err := s.validateTarget(ctx, target); err != nil {
		return fillSenderAuthorityResults(results, err)
	}
	if s.submitter == nil || s.resolver == nil {
		return fillSenderAuthorityResults(results, message.ErrRouteNotReady)
	}

	activeItems := make([]message.SendBatchItem, 0, len(items))
	activeIndexes := make([]int, 0, len(items))
	cancels := make([]context.CancelFunc, 0, len(items))
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()
	for i, item := range items {
		if item.Command.FromUID == "" {
			results[i] = message.SendBatchItemResult{Result: message.SendResult{Reason: message.ReasonAuthFail}}
			continue
		}
		itemCtx, cancel := senderAuthorityItemContext(ctx, item)
		if cancel != nil {
			cancels = append(cancels, cancel)
		}
		if err := itemCtx.Err(); err != nil {
			results[i] = message.SendBatchItemResult{Err: err}
			continue
		}
		item.Context = itemCtx
		current, err := s.resolver.ResolveUIDAuthority(itemCtx, item.Command.FromUID)
		if err != nil {
			results[i] = message.SendBatchItemResult{Err: err}
			continue
		}
		if err := compareSenderAuthorityTargets(target, current); err != nil {
			results[i] = message.SendBatchItemResult{Err: err}
			continue
		}
		activeIndexes = append(activeIndexes, i)
		activeItems = append(activeItems, item)
	}
	if len(activeItems) == 0 {
		return results
	}

	localResults := s.submitter.SendBatch(activeItems)
	if len(localResults) != len(activeItems) {
		return fillSenderAuthorityActiveResults(results, activeIndexes, message.ErrAppendResultMissing)
	}
	for i, result := range localResults {
		results[activeIndexes[i]] = result
	}
	return results
}

func (s senderAuthorityLocal) validateTarget(ctx context.Context, target authority.Target) error {
	if err := senderAuthorityContextError(ctx); err != nil {
		return err
	}
	if err := target.Validate(); err != nil {
		return message.ErrRouteNotReady
	}
	if !target.IsLocal(s.localNodeID) {
		return message.ErrNotLeader
	}
	if s.routes == nil {
		return nil
	}
	route, err := s.routes.RouteHashSlot(target.HashSlot)
	if err != nil {
		return mapAppSenderAuthorityRouteError(err)
	}
	if route.Leader == 0 {
		return message.ErrRouteNotReady
	}
	return compareSenderAuthorityTargets(target, senderAuthorityTargetFromRoute(route))
}

func senderAuthorityContextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func senderAuthorityItemContext(fallback context.Context, item message.SendBatchItem) (context.Context, context.CancelFunc) {
	base := item.Context
	if base == nil {
		base = fallback
	}
	if base == nil {
		base = context.Background()
	}
	if item.Deadline.IsZero() {
		return base, nil
	}
	if baseDeadline, ok := base.Deadline(); ok && !item.Deadline.Before(baseDeadline) {
		return base, nil
	}
	return context.WithDeadline(base, item.Deadline)
}

func senderAuthorityTargetFromRoute(route clusterv2.Route) authority.Target {
	return authority.Target{
		HashSlot:       route.HashSlot,
		SlotID:         route.SlotID,
		LeaderNodeID:   route.Leader,
		RouteRevision:  route.Revision,
		AuthorityEpoch: route.AuthorityEpoch,
	}
}

func compareSenderAuthorityTargets(expected, current authority.Target) error {
	if err := current.Validate(); err != nil {
		return message.ErrRouteNotReady
	}
	if expected == current {
		return nil
	}
	if current.LeaderNodeID != expected.LeaderNodeID {
		return message.ErrNotLeader
	}
	return message.ErrStaleRoute
}

func mapAppSenderAuthorityRouteError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, clusterv2.ErrRouteNotReady), errors.Is(err, clusterv2.ErrNoSlotLeader):
		return message.ErrRouteNotReady
	case errors.Is(err, clusterv2.ErrNotLeader):
		return message.ErrNotLeader
	default:
		return err
	}
}

func fillSenderAuthorityResults(results []message.SendBatchItemResult, err error) []message.SendBatchItemResult {
	for i := range results {
		results[i].Err = err
	}
	return results
}

func fillSenderAuthorityActiveResults(results []message.SendBatchItemResult, indexes []int, err error) []message.SendBatchItemResult {
	for _, index := range indexes {
		results[index].Err = err
	}
	return results
}

// authorityMessageUsecase routes sends through sender authority and keeps channel sync on the raw message app.
type authorityMessageUsecase struct {
	sender interface {
		Send(context.Context, message.SendCommand) (message.SendResult, error)
	}
	sync interface {
		SyncChannelMessages(context.Context, message.SyncChannelMessagesQuery) (message.SyncChannelMessagesResult, error)
	}
}

func (u authorityMessageUsecase) Send(ctx context.Context, cmd message.SendCommand) (message.SendResult, error) {
	if u.sender == nil {
		return message.SendResult{}, message.ErrRouteNotReady
	}
	return u.sender.Send(ctx, cmd)
}

func (u authorityMessageUsecase) SyncChannelMessages(ctx context.Context, query message.SyncChannelMessagesQuery) (message.SyncChannelMessagesResult, error) {
	if u.sync == nil {
		return message.SyncChannelMessagesResult{}, message.ErrRouteNotReady
	}
	return u.sync.SyncChannelMessages(ctx, query)
}

// nodeRPCRegistrar is the clusterv2 RPC registration surface used by authority adapters.
type nodeRPCRegistrar interface {
	RegisterRPC(uint8, clusterv2.NodeRPCHandler)
}

func registerNodeRPC(registrar nodeRPCRegistrar, serviceID uint8, handler nodeRPCHandlerFunc) {
	if registrar == nil {
		return
	}
	registrar.RegisterRPC(serviceID, handler)
}

var _ accessnode.SenderAuthority = senderAuthorityLocal{}

// observedSenderAuthorityResolver records sender-authority route decisions without exposing UID labels.
type observedSenderAuthorityResolver struct {
	localNodeID uint64
	next        message.UIDAuthorityResolver
	observer    authorityObserver
}

func (r observedSenderAuthorityResolver) ResolveUIDAuthority(ctx context.Context, uid string) (authority.Target, error) {
	if r.next == nil {
		err := message.ErrRouteNotReady
		r.observe(authority.Target{}, err)
		return authority.Target{}, err
	}
	target, err := r.next.ResolveUIDAuthority(ctx, uid)
	r.observe(target, err)
	return target, err
}

func (r observedSenderAuthorityResolver) observe(target authority.Target, err error) {
	if r.observer == nil {
		return
	}
	result := authorityResultFromError(err)
	if err == nil {
		if validateErr := target.Validate(); validateErr != nil {
			result = authorityResultRouteNotReady
		} else if target.IsLocal(r.localNodeID) {
			result = authorityResultLocal
		} else {
			result = authorityResultRemote
		}
	}
	r.observer.ObserveAuthoritySenderRoute(authoritySenderRouteEvent{Result: result})
}
