package presence

import "context"

// Activate registers a local session with the UID authority and promotes it locally after authority success.
func (a *App) Activate(ctx context.Context, cmd ActivateCommand) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if a.local == nil {
		return ErrLocalRegistryUnavailable
	}
	if a.authority == nil {
		return ErrAuthorityUnavailable
	}

	conn := a.onlineConn(cmd)
	route := routeFromConn(conn)
	if err := a.local.RegisterPending(conn); err != nil {
		return err
	}

	result, err := a.authority.RegisterRoute(ctx, route)
	if err != nil {
		a.rollbackLocal(cmd.SessionID)
		return err
	}
	if err := a.finishPendingRoute(ctx, result); err != nil {
		a.rollbackLocal(cmd.SessionID)
		return err
	}
	if err := a.local.MarkActive(cmd.SessionID); err != nil {
		a.authority.EnqueueUnregister(route.Identity(), route.OwnerSeq)
		a.rollbackLocal(cmd.SessionID)
		return err
	}
	return nil
}

func (a *App) finishPendingRoute(ctx context.Context, result RegisterResult) error {
	if result.PendingToken == "" {
		return a.applyRouteActions(result.Actions)
	}
	if err := a.applyRouteActions(result.Actions); err != nil {
		_ = a.authority.AbortRoute(ctx, result.PendingToken)
		return err
	}
	return a.authority.CommitRoute(ctx, result.PendingToken)
}

func (a *App) applyRouteActions(actions []RouteAction) error {
	toRemove := make([]uint64, 0, len(actions))
	for _, action := range actions {
		conn, ok := a.local.Connection(action.SessionID)
		if !ok || conn.UID != action.UID || conn.OwnerNodeID != action.OwnerNodeID || conn.OwnerBootID != action.OwnerBootID {
			continue
		}
		if conn.Session != nil {
			if err := conn.Session.CloseSession(action.Reason); err != nil {
				return err
			}
		}
		toRemove = append(toRemove, action.SessionID)
	}
	for _, sessionID := range toRemove {
		a.rollbackLocal(sessionID)
	}
	return nil
}

func (a *App) rollbackLocal(sessionID uint64) {
	a.local.MarkClosingAndUnregister(sessionID)
}
