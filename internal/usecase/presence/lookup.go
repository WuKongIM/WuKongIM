package presence

import "context"

// BatchAuthorityClient optionally reads active authority routes for multiple UIDs.
type BatchAuthorityClient interface {
	EndpointsByUIDs(context.Context, []string) (map[string][]Route, error)
}

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

// EndpointsByUIDs returns authoritative online routes for multiple UIDs.
func (a *App) EndpointsByUIDs(ctx context.Context, uids []string) (map[string][]Route, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if a.authority == nil {
		return nil, ErrAuthorityUnavailable
	}
	if batch, ok := a.authority.(BatchAuthorityClient); ok {
		return batch.EndpointsByUIDs(ctx, uids)
	}
	out := make(map[string][]Route, len(uids))
	for _, uid := range uids {
		if uid == "" {
			continue
		}
		routes, err := a.authority.EndpointsByUID(ctx, uid)
		if err != nil {
			return nil, err
		}
		out[uid] = append(out[uid], routes...)
	}
	return out, nil
}
