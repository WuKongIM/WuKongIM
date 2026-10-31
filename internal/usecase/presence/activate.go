package presence

import (
	"context"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
)

// Activate registers a local session with the UID authority and promotes it locally after authority success.
func (a *App) Activate(ctx context.Context, cmd ActivateCommand) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if a.local == nil {
		return WrapActivationPhase(ActivationPhaseLocalRegister, ErrLocalRegistryUnavailable)
	}
	if a.authority == nil {
		return WrapActivationPhase(ActivationPhaseRegister, ErrAuthorityUnavailable)
	}

	routeProjection, err := a.ownerRoute(cmd)
	if err != nil {
		return WrapActivationPhase(ActivationPhaseTarget, err)
	}
	route := routeFromOwnerRoute(routeProjection)
	defer func() {
		if err != nil {
			if hash := ActivationRouteHash(route.OwnerNodeID, route.OwnerBootID, route.SessionID); hash != [16]byte{} {
				err = &activationRouteError{err: err, hash: hash}
			}
		}
	}()
	if err := a.local.RegisterPending(LocalSession{Route: routeProjection, Session: cmd.Session}); err != nil {
		return WrapActivationPhase(ActivationPhaseLocalRegister, err)
	}

	result, err := a.authority.RegisterRoute(ctx, route)
	if err != nil {
		a.rollbackLocal(cmd.SessionID)
		return WrapActivationPhase(ActivationPhaseRegister, err)
	}
	strict := false
	if client, ok := a.authority.(ReceiptAuthorityClient); ok {
		strict = client.RequiresActivationReceipt()
	}
	receipt, err := a.finishPendingRoute(ctx, result, strict)
	if err != nil {
		if strict {
			a.authority.EnqueueUnregister(ctx, route.Identity(), route.OwnerSeq)
		}
		a.rollbackLocal(cmd.SessionID)
		return err
	}
	if receipt != nil {
		local, ok := a.local.(ReceiptLocalRegistry)
		if !ok || !receipt.Valid() || receipt.Identity != route.Identity() || receipt.OwnerSeq != route.OwnerSeq {
			err = authority.ErrStaleGeneration
		} else {
			err = local.MarkActiveWithReceipt(cmd.SessionID, *receipt)
		}
	} else {
		err = a.local.MarkActive(cmd.SessionID)
	}
	if err != nil {
		a.authority.EnqueueUnregister(ctx, route.Identity(), route.OwnerSeq)
		a.rollbackLocal(cmd.SessionID)
		return WrapActivationPhase(ActivationPhaseLocalRecheck, err)
	}
	a.observeOnlineStatus(ctx, routeProjection)
	return nil
}

func (a *App) finishPendingRoute(ctx context.Context, result RegisterResult, strict bool) (*ActivationReceipt, error) {
	if result.PendingToken == "" {
		if strict && result.Receipt == nil {
			return nil, WrapActivationPhase(ActivationPhaseRegister, authority.ErrSlotIncomplete)
		}
		return result.Receipt, a.applyRouteActions(ctx, result.Actions)
	}
	if strict && (result.PendingClaim == nil || !result.PendingClaim.Valid() || result.Receipt != nil) {
		_ = a.authority.AbortRoute(ctx, result.PendingToken)
		return nil, WrapActivationPhase(ActivationPhaseRegister, authority.ErrSlotIncomplete)
	}
	if err := a.applyRouteActions(ctx, result.Actions); err != nil {
		_ = a.authority.AbortRoute(ctx, result.PendingToken)
		return nil, err
	}
	if strict {
		client := a.authority.(ReceiptAuthorityClient)
		receipt, err := client.CommitRouteWithReceipt(ctx, result.PendingToken)
		if err != nil {
			_ = a.authority.AbortRoute(ctx, result.PendingToken)
			return nil, WrapActivationPhase(ActivationPhaseCommit, err)
		}
		if !receipt.Claim.Equal(*result.PendingClaim) {
			return nil, WrapActivationPhase(ActivationPhaseCommit, authority.ErrStaleGeneration)
		}
		return &receipt, nil
	}
	if err := a.authority.CommitRoute(ctx, result.PendingToken); err != nil {
		_ = a.authority.AbortRoute(ctx, result.PendingToken)
		return nil, WrapActivationPhase(ActivationPhaseCommit, err)
	}
	return nil, nil
}

func (a *App) applyRouteActions(ctx context.Context, actions []RouteAction) error {
	if len(actions) == 0 {
		return nil
	}
	if a.ownerAction == nil {
		return WrapActivationPhase(ActivationPhaseAction, ErrOwnerActionUnavailable)
	}
	for _, action := range actions {
		if err := a.ownerAction.ApplyRouteAction(ctx, action); err != nil {
			return WrapActivationPhase(ActivationPhaseAction, err)
		}
	}
	return nil
}

func (a *App) rollbackLocal(sessionID uint64) {
	a.local.MarkClosingAndUnregister(sessionID)
}
