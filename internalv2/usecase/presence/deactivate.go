package presence

import "context"

// Deactivate removes the local route before queuing an authority unregister tombstone.
func (a *App) Deactivate(_ context.Context, cmd DeactivateCommand) error {
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
	route := routeFromConn(conn)
	a.authority.EnqueueUnregister(route.Identity(), route.OwnerSeq)
	return nil
}
