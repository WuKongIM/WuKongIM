package presence

import "context"

// Touch records owner-local session activity for later batched authority updates.
func (a *App) Touch(ctx context.Context, cmd TouchCommand) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.local == nil {
		return ErrLocalRegistryUnavailable
	}
	a.local.MarkTouched(cmd.SessionID, cmd.ActivityUnix)
	return nil
}
