package delivery

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	presenceusecase "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

// PresenceResolver adapts exact-target presence lookups to Online Delivery.
type PresenceResolver struct {
	presence *presenceusecase.App
}

var _ runtimedelivery.PlanPresenceResolver = (*PresenceResolver)(nil)

// NewPresenceResolver creates the exact-target Online Delivery presence adapter.
func NewPresenceResolver(presence *presenceusecase.App) *PresenceResolver {
	return &PresenceResolver{presence: presence}
}

// EndpointsByTargets preserves input alignment and exact authority fences.
func (r *PresenceResolver) EndpointsByTargets(ctx context.Context, batches []onlinedelivery.RecipientTargetBatch) []runtimedelivery.TargetPresenceResult {
	results := make([]runtimedelivery.TargetPresenceResult, len(batches))
	if len(batches) == 0 {
		return results
	}
	if r == nil || r.presence == nil {
		return results
	}
	groups := make([]presenceusecase.EndpointLookupGroup, len(batches))
	for i, batch := range batches {
		groups[i].Target = presenceTargetFromOnlineDeliveryTarget(batch.Target)
		groups[i].UIDs = make([]string, 0, len(batch.Recipients))
		for _, recipient := range batch.Recipients {
			groups[i].UIDs = append(groups[i].UIDs, recipient.UID)
		}
	}
	resolved := r.presence.EndpointsByTargets(ctx, groups)
	for i := range results {
		if i >= len(resolved) {
			results[i].Err = runtimedelivery.ErrPresenceResultMissing
			continue
		}
		results[i].Err = resolved[i].Err
		results[i].Routes = onlineDeliveryRoutesFromPresence(resolved[i].Routes)
	}
	return results
}

func onlineDeliveryRoutesFromPresence(routes []presenceusecase.Route) []onlinedelivery.Route {
	out := make([]onlinedelivery.Route, 0, len(routes))
	for _, route := range routes {
		out = append(out, onlinedelivery.Route{
			UID: route.UID, OwnerNodeID: route.OwnerNodeID, OwnerBootID: route.OwnerBootID,
			OwnerSeq: route.OwnerSeq, SessionID: route.SessionID, DeviceID: route.DeviceID,
			DeviceFlag: route.DeviceFlag, DeviceLevel: route.DeviceLevel,
		})
	}
	return out
}

func presenceTargetFromOnlineDeliveryTarget(target authority.Target) presenceusecase.RouteTarget {
	return presenceusecase.RouteTarget{
		HashSlot: target.HashSlot, SlotID: target.SlotID, LeaderNodeID: target.LeaderNodeID,
		LeaderTerm: target.LeaderTerm, ConfigEpoch: target.ConfigEpoch,
		RouteRevision: target.RouteRevision, AuthorityEpoch: target.AuthorityEpoch,
	}
}
