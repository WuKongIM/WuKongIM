package presence

import "context"

// Activate registers a local session with the UID authority and promotes it locally after authority success.
func (a *App) Activate(ctx context.Context, cmd ActivateCommand) error {
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
	if err := a.local.RegisterPending(LocalSession{Route: routeProjection, Session: cmd.Session}); err != nil {
		return WrapActivationPhase(ActivationPhaseLocalRegister, err)
	}

	result, err := a.authority.RegisterRoute(ctx, route)
	if err != nil {
		a.rollbackLocal(cmd.SessionID)
		return WrapActivationPhase(ActivationPhaseRegister, err)
	}
	if err := a.finishPendingRoute(ctx, result); err != nil {
		a.rollbackLocal(cmd.SessionID)
		return err
	}
	if err := a.local.MarkActive(cmd.SessionID); err != nil {
		a.authority.EnqueueUnregister(ctx, route.Identity(), route.OwnerSeq)
		a.rollbackLocal(cmd.SessionID)
		return WrapActivationPhase(ActivationPhaseLocalRecheck, err)
	}
	a.observeOnlineStatus(ctx, routeProjection)
	return nil
}

func (a *App) finishPendingRoute(ctx context.Context, result RegisterResult) error {
	if result.PendingToken == "" {
		return a.applyRouteActions(ctx, result.Actions)
	}
	if err := a.applyRouteActions(ctx, result.Actions); err != nil {
		_ = a.authority.AbortRoute(ctx, result.PendingToken)
		return err
	}
	if err := a.authority.CommitRoute(ctx, result.PendingToken); err != nil {
		_ = a.authority.AbortRoute(ctx, result.PendingToken)
		return WrapActivationPhase(ActivationPhaseCommit, err)
	}
	return nil
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
