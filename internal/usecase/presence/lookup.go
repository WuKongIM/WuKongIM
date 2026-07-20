package presence

import "context"

// BatchAuthorityClient optionally reads active authority routes for multiple UIDs.
type BatchAuthorityClient interface {
	EndpointsByUIDs(context.Context, []string) (map[string][]Route, error)
}

// TargetBatchAuthorityClient optionally reads endpoint groups through their exact authority targets.
// Implementations must return one result per input group in the same order.
type TargetBatchAuthorityClient interface {
	EndpointsByTargets(context.Context, []EndpointLookupGroup) []EndpointLookupResult
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

// EndpointsByTargets returns one endpoint lookup result per exact-target group.
func (a *App) EndpointsByTargets(ctx context.Context, groups []EndpointLookupGroup) []EndpointLookupResult {
	if ctx == nil {
		ctx = context.Background()
	}
	results := make([]EndpointLookupResult, len(groups))
	if a.authority == nil {
		for i := range results {
			results[i].Err = ErrAuthorityUnavailable
		}
		return results
	}
	if batch, ok := a.authority.(TargetBatchAuthorityClient); ok {
		return batch.EndpointsByTargets(ctx, groups)
	}
	for i, group := range groups {
		for _, uid := range group.UIDs {
			if uid == "" {
				continue
			}
			routes, err := a.authority.EndpointsByUID(ctx, uid)
			if err != nil {
				results[i].Err = err
				break
			}
			results[i].Routes = append(results[i].Routes, routes...)
		}
	}
	return results
}
