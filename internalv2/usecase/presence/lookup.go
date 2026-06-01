package presence

import "context"

// EndpointsByUID returns authoritative online routes for one UID.
func (a *App) EndpointsByUID(ctx context.Context, uid string) ([]Route, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if a.authority == nil {
		return nil, ErrAuthorityUnavailable
	}
	return a.authority.EndpointsByUID(ctx, uid)
}
