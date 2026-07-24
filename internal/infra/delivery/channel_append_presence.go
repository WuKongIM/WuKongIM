package delivery

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	presenceusecase "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

// ChannelAppendPresenceResolver adapts entry-agnostic presence lookups to the
// channelappend delivery runtime while preserving exact authority fences.
type ChannelAppendPresenceResolver struct {
	presence *presenceusecase.App
}

var _ channelappend.PresenceResolver = (*ChannelAppendPresenceResolver)(nil)
var _ channelappend.RecipientTargetPresenceResolver = (*ChannelAppendPresenceResolver)(nil)

// NewChannelAppendPresenceResolver creates a delivery-runtime presence adapter.
func NewChannelAppendPresenceResolver(presence *presenceusecase.App) *ChannelAppendPresenceResolver {
	return &ChannelAppendPresenceResolver{presence: presence}
}

// EndpointsByUIDs returns flat channelappend routes for the requested UIDs.
func (r *ChannelAppendPresenceResolver) EndpointsByUIDs(ctx context.Context, uids []string) ([]channelappend.Route, error) {
	if r == nil || r.presence == nil || len(uids) == 0 {
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

// EndpointsByTargets resolves exact target batches and preserves aligned
// partial errors for the channelappend delivery worker.
func (r *ChannelAppendPresenceResolver) EndpointsByTargets(ctx context.Context, batches []channelappend.RecipientTargetBatch) []channelappend.RecipientTargetPresenceResult {
	results := make([]channelappend.RecipientTargetPresenceResult, len(batches))
	if len(batches) == 0 {
		return results
	}
	if r == nil || r.presence == nil {
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
