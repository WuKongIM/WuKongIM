package cluster

import (
	"context"
	"errors"
	"fmt"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

// SetGenerationAuthority fixes strict receipt mode before entry admission. A
// failed full-protocol operation never replays a legacy register or commit.
func (c *PresenceAuthorityClient) SetGenerationAuthority(local accessnode.PresenceGenerationAuthority) {
	c.fullGeneration = true
	c.localGeneration = local
}
func (c *PresenceAuthorityClient) RequiresActivationReceipt() bool {
	return c != nil && c.fullGeneration
}

func (c *PresenceAuthorityClient) registerGeneration(ctx context.Context, route presence.Route) (presence.RegisterResult, error) {
	var result presence.RegisterResult
	err := c.withFreshTarget(ctx, route.UID, func(target presence.RouteTarget) error {
		var err error
		if target.LeaderNodeID == c.node.NodeID() {
			if c.localGeneration == nil {
				return authority.ErrSlotIncomplete
			}
			result, err = c.localGeneration.RegisterRoute(ctx, target, route)
		} else {
			result, err = c.remote.RegisterPresenceGeneration(ctx, target, route)
		}
		return err
	})
	if err != nil {
		return presence.RegisterResult{}, err
	}
	if result.PendingToken != "" {
		if result.PendingClaim == nil || !result.PendingClaim.Valid() || result.Receipt != nil {
			return presence.RegisterResult{}, authority.ErrStaleGeneration
		}
		claim := *result.PendingClaim
		raw := string(result.PendingToken)
		// Include G: the Directory may reuse its local token after an identical-fence rebuild.
		result.PendingToken = presence.PendingRouteToken(fmt.Sprintf("%p/%d:%s/%d/%s", c, len(route.UID), route.UID, claim.Generation, raw))
		c.rememberPending(result.PendingToken, pendingRouteRef{uid: route.UID, rawToken: raw, claim: &claim, route: route})
	} else if result.Receipt == nil || !result.Receipt.Valid() || result.Receipt.Identity != route.Identity() || result.Receipt.OwnerSeq != route.OwnerSeq {
		return presence.RegisterResult{}, authority.ErrStaleGeneration
	}
	return result, nil
}

// CommitRouteWithReceipt keeps the exact accepting generation. A freshly
// resolved authority must never reinterpret an old candidate token.
func (c *PresenceAuthorityClient) CommitRouteWithReceipt(ctx context.Context, token presence.PendingRouteToken) (presence.ActivationReceipt, error) {
	if !c.fullGeneration {
		return presence.ActivationReceipt{}, authority.ErrSlotIncomplete
	}
	ref, ok := c.pendingRef(token)
	if !ok || ref.claim == nil {
		return presence.ActivationReceipt{}, authority.ErrStaleGeneration
	}
	var receipt presence.ActivationReceipt
	var err error
	if ref.claim.Target.LeaderNodeID == c.node.NodeID() {
		if c.localGeneration == nil {
			return receipt, authority.ErrSlotIncomplete
		}
		receipt, err = c.localGeneration.CommitRouteWithReceipt(ctx, *ref.claim, presence.PendingRouteToken(ref.rawToken))
	} else {
		receipt, err = c.remote.CommitPresenceGeneration(ctx, *ref.claim, ref.rawToken)
	}
	if err != nil {
		return receipt, err
	}
	if !receipt.Valid() || !receipt.Claim.Equal(*ref.claim) || receipt.Identity != ref.route.Identity() || receipt.OwnerSeq != ref.route.OwnerSeq {
		return presence.ActivationReceipt{}, authority.ErrStaleGeneration
	}
	c.forgetPending(token)
	return receipt, nil
}
func (c *PresenceAuthorityClient) abortGeneration(ctx context.Context, token presence.PendingRouteToken) error {
	ref, ok := c.pendingRef(token)
	if !ok || ref.claim == nil {
		return authority.ErrStaleGeneration
	}
	// This owner reference is terminal even when remote cleanup remains unknown;
	// no later call may turn an obsolete token into a new-generation candidate.
	defer c.forgetPending(token)
	if ref.claim.Target.LeaderNodeID == c.node.NodeID() {
		if c.localGeneration == nil {
			return authority.ErrSlotIncomplete
		}
		return c.localGeneration.AbortRouteWithClaim(ctx, *ref.claim, presence.PendingRouteToken(ref.rawToken))
	}
	return c.remote.AbortPresenceGeneration(ctx, *ref.claim, ref.rawToken)
}
func (c *PresenceAuthorityClient) observedGeneration(target presence.RouteTarget) (presence.GenerationClaim, error) {
	node, ok := c.node.(PresenceGenerationNode)
	if !ok {
		return presence.GenerationClaim{}, authority.ErrSlotIncomplete
	}
	snapshot, err := node.GetLocalPresenceGeneration(target.HashSlot)
	if err != nil {
		return presence.GenerationClaim{}, generationPortError(err)
	}
	claim := generationFromCluster(snapshot.Generation)
	t := claim.Target
	if !claim.Valid() || snapshot.OwnerSet.Epoch != claim.OwnerSetEpoch || t.HashSlot != target.HashSlot || t.SlotID != target.SlotID || t.LeaderNodeID != target.LeaderNodeID || t.LeaderTerm != target.LeaderTerm || t.ConfigEpoch != target.ConfigEpoch {
		return presence.GenerationClaim{}, authority.ErrStaleGeneration
	}
	return claim, nil
}
func (c *PresenceAuthorityClient) touchGeneration(ctx context.Context, target presence.RouteTarget, routes []presence.Route) error {
	claim, err := c.observedGeneration(target)
	if err != nil {
		return err
	}
	if target.LeaderNodeID == c.node.NodeID() {
		if c.localGeneration == nil {
			return authority.ErrSlotIncomplete
		}
		return c.localGeneration.TouchRoutes(ctx, claim, routes)
	}
	return c.remote.TouchPresenceGeneration(ctx, claim, routes)
}
func (c *PresenceAuthorityClient) unregisterGeneration(ctx context.Context, identity presence.RouteIdentity, seq uint64) error {
	return c.withFreshTarget(ctx, identity.UID, func(target presence.RouteTarget) error {
		claim, err := c.observedGeneration(target)
		if err != nil {
			return err
		}
		if target.LeaderNodeID == c.node.NodeID() {
			if c.localGeneration == nil {
				return authority.ErrSlotIncomplete
			}
			return c.localGeneration.UnregisterRoute(ctx, claim, identity, seq)
		}
		return c.remote.UnregisterPresenceGeneration(ctx, claim, identity, seq)
	})
}
func isGenerationRetryable(err error) bool {
	return errors.Is(err, authority.ErrStaleGeneration) || errors.Is(err, authority.ErrSlotIncomplete)
}
