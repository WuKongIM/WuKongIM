package presence

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
)

// ErrLocalRegistryUnavailable reports a missing local registry dependency.
var ErrLocalRegistryUnavailable = errors.New("internal/usecase/presence: local registry unavailable")

// ErrAuthorityUnavailable reports a missing authority client dependency.
var ErrAuthorityUnavailable = errors.New("internal/usecase/presence: authority client unavailable")

// ErrOwnerActionUnavailable reports a missing owner action client dependency.
var ErrOwnerActionUnavailable = errors.New("internal/usecase/presence: owner action client unavailable")

// ErrSessionNotActive reports that the local session disappeared before activation completed.
var ErrSessionNotActive = errors.New("internal/usecase/presence: session not active")

// RouteState records the owner-local lifecycle stage for a route.
type RouteState = online.RouteState

const (
	// RouteStatePending marks a session that is locally accepted but not authority-active.
	RouteStatePending = online.RouteStatePending
	// RouteStateActive marks a session that has completed authority registration.
	RouteStateActive = online.RouteStateActive
	// RouteStateClosing marks a session removed from the local route index.
	RouteStateClosing = online.RouteStateClosing
)

// ActivateCommand registers one authenticated gateway session with the presence authority.
type ActivateCommand struct {
	// UID is the authenticated user ID for this connection.
	UID string
	// DeviceID is the authenticated client device identifier.
	DeviceID string
	// DeviceFlag is the protocol device category for the session.
	DeviceFlag uint8
	// DeviceLevel is the protocol device conflict level for the session.
	DeviceLevel uint8
	// Listener records the gateway listener that accepted the session.
	Listener string
	// ConnectedUnix records when the gateway session was accepted locally.
	ConnectedUnix int64
	// SessionID is the owner-local gateway session identifier.
	SessionID uint64
	// Session lets local conflict actions close the concrete gateway session without importing gateway types.
	Session SessionHandle
}

// DeactivateCommand removes one owner-local session route.
type DeactivateCommand struct {
	// UID is the authenticated user ID for this connection.
	UID string
	// SessionID is the owner-local gateway session identifier.
	SessionID uint64
}

// TouchCommand records owner-observed activity for one local gateway session.
type TouchCommand struct {
	// SessionID is the owner-local gateway session identifier.
	SessionID uint64
	// ActivityUnix records the latest owner-observed client activity time.
	ActivityUnix int64
}

// SessionHandle closes a concrete gateway session through an entry-agnostic boundary.
type SessionHandle = online.SessionHandle

// OwnerRoute is the usecase DTO stored through the local owner route registry port.
type OwnerRoute = online.OwnerRoute

// LocalSession stores the local gateway session separately from the route projection.
type LocalSession = online.LocalSession

// RouteTarget fences an authority operation to one observed hash-slot route.
type RouteTarget = authority.RouteTarget

// RouteTargetResult preserves the aligned route-resolution outcome for one input UID.
type RouteTargetResult struct {
	// Target is populated with the complete authority fence when Err is nil.
	Target RouteTarget
	// Err records the routing failure for the corresponding input UID.
	Err error
}

// EndpointLookupGroup binds a bounded UID set to one exact authority target.
type EndpointLookupGroup struct {
	// Target is the complete authority fence observed for every UID in this group.
	Target RouteTarget
	// UIDs preserves the caller's lookup order within this authority target.
	UIDs []string
}

// EndpointLookupResult preserves the aligned outcome for one endpoint lookup group.
type EndpointLookupResult struct {
	// Routes contains all active routes returned for the corresponding group.
	Routes []Route
	// Err records the group-scoped lookup failure without aborting other groups.
	Err error
}

// Route identifies a virtual client connection known by the UID authority.
type Route = authority.Route

// RouteIdentity identifies one route independently from mutable route metadata.
type RouteIdentity = authority.RouteIdentity

// RouteAction asks an owner node to resolve an authority-side route conflict.
type RouteAction = authority.RouteAction

// PendingRouteToken names a conflict candidate waiting for action completion.
type PendingRouteToken = authority.PendingRouteToken

// RegisterResult describes immediate or pending authority registration work.
type RegisterResult = authority.RegisterResult
