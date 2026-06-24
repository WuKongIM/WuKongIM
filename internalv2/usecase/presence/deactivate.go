package presence

import "context"

// Deactivate removes the local route before queuing an authority unregister tombstone.
func (a *App) Deactivate(ctx context.Context, cmd DeactivateCommand) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if a.local == nil {
		return ErrLocalRegistryUnavailable
	}
	if a.authority == nil {
		return ErrAuthorityUnavailable
	}
	conn, ok := a.local.MarkClosingAndUnregister(cmd.SessionID)
	if !ok {
		return nil
	}
	if conn.UID == "" {
		conn.UID = cmd.UID
	}
	a.observeOfflineIfLastLocalSession(ctx, conn.UID)
	route := routeFromOwnerRoute(conn)
	a.authority.EnqueueUnregister(ctx, route.Identity(), route.OwnerSeq)
	return nil
}
