package node

import (
	"context"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

// ReadOwnerRouteCallsBounded prepays decoded ownership for one complete-call
// page. The caller owns continuation, compatibility selection and final fences.
func (c *Client) ReadOwnerRouteCallsBounded(ctx context.Context, owner uint64, calls []presence.RecoveryCall, start int, budget *presence.RecoveryResultBudget) (presence.OwnedOwnerRouteCallPage, error) {
	if c == nil || c.node == nil || owner == 0 || !presence.ValidOwnerRecoveryCalls(calls, start) {
		return presence.OwnedOwnerRouteCallPage{}, authority.ErrRouteNotReady
	}
	if err := ctx.Err(); err != nil {
		return presence.OwnedOwnerRouteCallPage{}, err
	}
	if budget == nil {
		return presence.OwnedOwnerRouteCallPage{}, presence.ErrRecoveryResultBudget
	}
	body, err := encodePresenceRPCRequestBinary(presenceRPCRequest{Op: presenceOpReadOwnerRouteCalls, RecoveryCalls: calls, RecoveryStart: start})
	if err != nil {
		return presence.OwnedOwnerRouteCallPage{}, err
	}
	reply, err := c.node.CallRPC(ctx, owner, PresenceOwnerRPCServiceID, body)
	if err != nil {
		if ownerRouteCallsUnsupported(err) {
			return presence.OwnedOwnerRouteCallPage{}, presence.ErrOwnerRouteCallsUnsupported
		}
		return presence.OwnedOwnerRouteCallPage{}, err
	}
	return decodeOwnedOwnerPage(ctx, reply, 3, calls, start, budget)
}

// ReadOwnerRoutesByTargetsBounded preserves operation 9 for one group and 10
// for multiple groups. Legacy decoding cannot bypass the shared result budget.
func (c *Client) ReadOwnerRoutesByTargetsBounded(ctx context.Context, owner uint64, groups []presence.EndpointLookupGroup, budget *presence.RecoveryResultBudget) (presence.OwnedOwnerRouteCallPage, error) {
	if c == nil || c.node == nil || owner == 0 || !presence.ValidOwnerRecoveryPage(groups) {
		return presence.OwnedOwnerRouteCallPage{}, authority.ErrRouteNotReady
	}
	if err := ctx.Err(); err != nil {
		return presence.OwnedOwnerRouteCallPage{}, err
	}
	if budget == nil {
		return presence.OwnedOwnerRouteCallPage{}, presence.ErrRecoveryResultBudget
	}
	op, version := presenceOpReadOwnerRoutesByTargets, byte(2)
	if len(groups) == 1 {
		op, version = presenceOpReadOwnerRoutes, 1
	}
	body, err := encodePresenceRPCRequestBinary(presenceRPCRequest{Op: op, EndpointGroups: groups})
	if err != nil {
		return presence.OwnedOwnerRouteCallPage{}, err
	}
	reply, err := c.node.CallRPC(ctx, owner, PresenceOwnerRPCServiceID, body)
	if err != nil {
		return presence.OwnedOwnerRouteCallPage{}, err
	}
	return decodeOwnedOwnerPage(ctx, reply, version, []presence.RecoveryCall{{Groups: groups}}, 0, budget)
}
