package presence

import "context"

// LocalRegistry stores owner-local route projections and local session records.
type LocalRegistry interface {
	RegisterPending(LocalSession) error
	MarkActive(sessionID uint64) error
	MarkClosingAndUnregister(sessionID uint64) (OwnerRoute, bool)
	MarkTouched(sessionID uint64, activityUnix int64) (OwnerRoute, bool)
	LocalSession(sessionID uint64) (LocalSession, bool)
	LocalSessionsByUID(uid string) []LocalSession
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

// OnlineStatusEvent describes a UID-level owner-local online status transition.
type OnlineStatusEvent struct {
	// UID is the authenticated user ID whose local online status changed.
	UID string
	// Online reports whether the UID has at least one owner-local session.
	Online bool
	// Value carries the legacy webhook status value, such as "uid-1" or "uid-0".
	Value string
}

// OnlineStatusObserver receives best-effort owner-local online status changes.
type OnlineStatusObserver interface {
	ObserveOnlineStatus(context.Context, OnlineStatusEvent) error
}
