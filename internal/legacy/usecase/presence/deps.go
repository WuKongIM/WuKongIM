package presence

import "context"

type Router interface {
	SlotForKey(key string) uint64
}

type ActionDispatcher interface {
	ApplyRouteAction(ctx context.Context, action RouteAction) error
}

type Authoritative interface {
	RegisterAuthoritative(ctx context.Context, cmd RegisterAuthoritativeCommand) (RegisterAuthoritativeResult, error)
	UnregisterAuthoritative(ctx context.Context, cmd UnregisterAuthoritativeCommand) error
	HeartbeatAuthoritative(ctx context.Context, cmd HeartbeatAuthoritativeCommand) (HeartbeatAuthoritativeResult, error)
	ReplayAuthoritative(ctx context.Context, cmd ReplayAuthoritativeCommand) error
	EndpointsByUID(ctx context.Context, uid string) ([]Route, error)
	EndpointsByUIDs(ctx context.Context, uids []string) (map[string][]Route, error)
}
