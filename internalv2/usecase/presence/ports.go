package presence

import "context"

// LocalRegistry stores owner-local real gateway session routes.
type LocalRegistry interface {
	RegisterPending(OnlineConn) error
	MarkActive(sessionID uint64) error
	MarkClosingAndUnregister(sessionID uint64) (OnlineConn, bool)
	MarkTouched(sessionID uint64, activityUnix int64) (OnlineConn, bool)
	Connection(sessionID uint64) (OnlineConn, bool)
}

// AuthorityClient routes presence operations to the current UID authority.
type AuthorityClient interface {
	RegisterRoute(context.Context, Route) (RegisterResult, error)
	CommitRoute(context.Context, PendingRouteToken) error
	AbortRoute(context.Context, PendingRouteToken) error
	EnqueueUnregister(context.Context, RouteIdentity, uint64)
	EndpointsByUID(context.Context, string) ([]Route, error)
}

// OwnerActionClient applies conflict actions on the node that owns a real session.
type OwnerActionClient interface {
	ApplyRouteAction(context.Context, RouteAction) error
}
