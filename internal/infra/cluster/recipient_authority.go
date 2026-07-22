package cluster

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	pkgcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

type recipientAuthorityRouteNode interface {
	RouteKey(string) (pkgcluster.Route, error)
}

type recipientAuthorityBatchNode interface {
	RouteAuthorities([]string) ([]pkgcluster.RouteAuthority, error)
}

type recipientAuthorityPartialBatchNode interface {
	RouteAuthoritiesPartial([]string) ([]pkgcluster.RouteAuthorityResult, error)
}

type recipientAuthorityBatchRouteNode interface {
	RouteKeys([]string) ([]pkgcluster.Route, error)
}

type recipientAuthorityPartialBatchRouteNode interface {
	RouteKeysPartial([]string) ([]pkgcluster.RouteKeyResult, error)
}

const (
	// RecipientAuthorityResolveResultOK reports that every requested UID resolved.
	RecipientAuthorityResolveResultOK = "ok"
	// RecipientAuthorityResolveResultPartial reports mixed successful and failed UID results.
	RecipientAuthorityResolveResultPartial = "partial"
	// RecipientAuthorityResolveResultRouteNotReady reports only retryable route failures.
	RecipientAuthorityResolveResultRouteNotReady = "route_not_ready"
	// RecipientAuthorityResolveResultCanceled reports a canceled batch lookup.
	RecipientAuthorityResolveResultCanceled = "canceled"
	// RecipientAuthorityResolveResultDeadline reports an expired batch lookup deadline.
	RecipientAuthorityResolveResultDeadline = "deadline"
	// RecipientAuthorityResolveResultError reports an unclassified or mixed all-error result.
	RecipientAuthorityResolveResultError = "error"
)

// RecipientAuthorityResolveObservation describes one aligned recipient authority batch lookup.
type RecipientAuthorityResolveObservation struct {
	// Result is a bounded batch outcome.
	Result string
	// Items is the number of requested UID items.
	Items int
	// Targets is the number of distinct successful physical hash-slot targets.
	Targets int
	// Duration is the complete batch lookup latency.
	Duration time.Duration
}

// RecipientAuthorityResolveObserver receives one aggregate event per batch lookup.
type RecipientAuthorityResolveObserver interface {
	ObserveRecipientAuthorityResolve(RecipientAuthorityResolveObservation)
}

// RecipientAuthorityResolver adapts cluster UID routes to channelappend recipient targets.
// It prefers aligned lightweight authority snapshots and retains legacy batch and
// single-route capabilities for alternate cluster implementations.
type RecipientAuthorityResolver struct {
	node     recipientAuthorityRouteNode
	observer RecipientAuthorityResolveObserver
}

var _ channelappend.RecipientAuthorityResolver = (*RecipientAuthorityResolver)(nil)

// NewRecipientAuthorityResolver returns a cluster-backed resolver when node
// exposes the minimum single-key route capability; unsupported nodes return nil.
// Optional batch capability probing remains private to this adapter.
func NewRecipientAuthorityResolver(node any, observer RecipientAuthorityResolveObserver) *RecipientAuthorityResolver {
	routeNode, ok := node.(recipientAuthorityRouteNode)
	if !ok {
		return nil
	}
	return &RecipientAuthorityResolver{node: routeNode, observer: observer}
}

// ResolveRecipientAuthority resolves one UID through the cluster route table.
func (r *RecipientAuthorityResolver) ResolveRecipientAuthority(ctx context.Context, uid string) (channelappend.RecipientAuthorityTarget, error) {
	ctx, err := recipientAuthorityContext(ctx)
	if err != nil {
		return channelappend.RecipientAuthorityTarget{}, err
	}
	if r == nil || r.node == nil {
		return channelappend.RecipientAuthorityTarget{}, channelappend.ErrRouteNotReady
	}
	route, err := r.node.RouteKey(uid)
	if err != nil {
		return channelappend.RecipientAuthorityTarget{}, fmt.Errorf("recipient route key uid=%q: %w", uid, mapRecipientAuthorityRouteError(err))
	}
	return recipientAuthorityTargetFromRoute(route)
}

// ResolveRecipientAuthorities resolves an aligned UID batch while preferring
// the lightest cluster capability exposed by the concrete node.
func (r *RecipientAuthorityResolver) ResolveRecipientAuthorities(ctx context.Context, uids []string) (resolved []channelappend.RecipientAuthorityResult, resolveErr error) {
	var started time.Time
	if r != nil && r.observer != nil {
		started = time.Now()
		defer func() {
			r.observer.ObserveRecipientAuthorityResolve(RecipientAuthorityResolveObservation{
				Result:   recipientAuthorityResolveResult(resolved, resolveErr),
				Items:    len(uids),
				Targets:  recipientAuthorityResolveTargetCount(resolved),
				Duration: time.Since(started),
			})
		}()
	}
	ctx, err := recipientAuthorityContext(ctx)
	if err != nil {
		return nil, err
	}
	if r == nil || r.node == nil {
		return nil, channelappend.ErrRouteNotReady
	}
	if authorityNode, ok := r.node.(recipientAuthorityPartialBatchNode); ok {
		authorities, err := authorityNode.RouteAuthoritiesPartial(uids)
		if err != nil {
			return nil, fmt.Errorf("recipient route authorities partial uidCount=%d sampleUID=%q: %w", len(uids), firstRecipientUIDForRouteLog(uids), mapRecipientAuthorityRouteError(err))
		}
		if len(authorities) != len(uids) {
			return nil, fmt.Errorf("recipient route authorities partial returned %d routes for %d uids sampleUID=%q: %w", len(authorities), len(uids), firstRecipientUIDForRouteLog(uids), channelappend.ErrRouteNotReady)
		}
		results := make([]channelappend.RecipientAuthorityResult, len(uids))
		for i, result := range authorities {
			if result.Err != nil {
				results[i].Err = mapRecipientAuthorityRouteError(result.Err)
				continue
			}
			target, err := recipientAuthorityTargetFromAuthority(result.Authority)
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
			return nil, fmt.Errorf("recipient route authorities uidCount=%d sampleUID=%q: %w", len(uids), firstRecipientUIDForRouteLog(uids), mapRecipientAuthorityRouteError(err))
		}
		if len(authorities) != len(uids) {
			return nil, fmt.Errorf("recipient route authorities returned %d routes for %d uids sampleUID=%q: %w", len(authorities), len(uids), firstRecipientUIDForRouteLog(uids), channelappend.ErrRouteNotReady)
		}
		results := make([]channelappend.RecipientAuthorityResult, len(uids))
		for i := range uids {
			target, err := recipientAuthorityTargetFromAuthority(authorities[i])
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
			return nil, fmt.Errorf("recipient route keys partial uidCount=%d sampleUID=%q: %w", len(uids), firstRecipientUIDForRouteLog(uids), mapRecipientAuthorityRouteError(err))
		}
		if len(routes) != len(uids) {
			return nil, fmt.Errorf("recipient route keys partial returned %d routes for %d uids sampleUID=%q: %w", len(routes), len(uids), firstRecipientUIDForRouteLog(uids), channelappend.ErrRouteNotReady)
		}
		results := make([]channelappend.RecipientAuthorityResult, len(uids))
		for i, result := range routes {
			if result.Err != nil {
				results[i].Err = mapRecipientAuthorityRouteError(result.Err)
				continue
			}
			target, err := recipientAuthorityTargetFromRoute(result.Route)
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
			return nil, fmt.Errorf("recipient route keys uidCount=%d sampleUID=%q: %w", len(uids), firstRecipientUIDForRouteLog(uids), mapRecipientAuthorityRouteError(err))
		}
		if len(routes) != len(uids) {
			return nil, fmt.Errorf("recipient route keys returned %d routes for %d uids sampleUID=%q: %w", len(routes), len(uids), firstRecipientUIDForRouteLog(uids), channelappend.ErrRouteNotReady)
		}
		results := make([]channelappend.RecipientAuthorityResult, len(uids))
		for i := range uids {
			target, err := recipientAuthorityTargetFromRoute(routes[i])
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

func recipientAuthorityContext(ctx context.Context) (context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return ctx, nil
}

func recipientAuthorityResolveResult(results []channelappend.RecipientAuthorityResult, err error) string {
	if err != nil {
		return recipientAuthorityResolveErrorResult(err)
	}
	succeeded := 0
	failed := 0
	allRouteNotReady := true
	for _, result := range results {
		if result.Err == nil {
			succeeded++
			continue
		}
		failed++
		if recipientAuthorityResolveErrorResult(result.Err) != RecipientAuthorityResolveResultRouteNotReady {
			allRouteNotReady = false
		}
	}
	if failed == 0 {
		return RecipientAuthorityResolveResultOK
	}
	if succeeded > 0 {
		return RecipientAuthorityResolveResultPartial
	}
	if allRouteNotReady {
		return RecipientAuthorityResolveResultRouteNotReady
	}
	return RecipientAuthorityResolveResultError
}

func recipientAuthorityResolveErrorResult(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return RecipientAuthorityResolveResultCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return RecipientAuthorityResolveResultDeadline
	case errors.Is(err, channelappend.ErrRouteNotReady),
		errors.Is(err, channelappend.ErrStaleRoute),
		errors.Is(err, channelappend.ErrNotLeader),
		errors.Is(err, channelappend.ErrNotChannelAuthority):
		return RecipientAuthorityResolveResultRouteNotReady
	default:
		return RecipientAuthorityResolveResultError
	}
}

func recipientAuthorityResolveTargetCount(results []channelappend.RecipientAuthorityResult) int {
	// Cloud and default deployments use 256 physical hash slots. Keep that
	// common case allocation-free and allocate only for explicitly larger maps.
	var defaultSlots [4]uint64
	var overflow map[uint16]struct{}
	targets := 0
	for _, result := range results {
		if result.Err != nil || result.Target.LeaderNodeID == 0 {
			continue
		}
		hashSlot := result.Target.HashSlot
		if hashSlot < 256 {
			word := hashSlot / 64
			bit := uint64(1) << (hashSlot % 64)
			if defaultSlots[word]&bit == 0 {
				defaultSlots[word] |= bit
				targets++
			}
			continue
		}
		if overflow == nil {
			overflow = make(map[uint16]struct{})
		}
		if _, exists := overflow[hashSlot]; !exists {
			overflow[hashSlot] = struct{}{}
			targets++
		}
	}
	return targets
}

func recipientAuthorityTargetFromRoute(route pkgcluster.Route) (channelappend.RecipientAuthorityTarget, error) {
	return recipientAuthorityTargetFromAuthority(pkgcluster.RouteAuthority{
		HashSlot:       route.HashSlot,
		SlotID:         route.SlotID,
		LeaderNodeID:   route.Leader,
		LeaderTerm:     route.LeaderTerm,
		ConfigEpoch:    route.ConfigEpoch,
		RouteRevision:  route.Revision,
		AuthorityEpoch: route.AuthorityEpoch,
	})
}

func recipientAuthorityTargetFromAuthority(authority pkgcluster.RouteAuthority) (channelappend.RecipientAuthorityTarget, error) {
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

func mapRecipientAuthorityRouteError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, pkgcluster.ErrNotLeader):
		return fmt.Errorf("%w: %w", channelappend.ErrNotLeader, err)
	case errors.Is(err, pkgcluster.ErrRouteNotReady), errors.Is(err, pkgcluster.ErrNoSlotLeader):
		return fmt.Errorf("%w: %w", channelappend.ErrRouteNotReady, err)
	default:
		return err
	}
}

func firstRecipientUIDForRouteLog(uids []string) string {
	if len(uids) == 0 {
		return ""
	}
	return uids[0]
}
