package presence

import (
	"errors"

	authority "github.com/WuKongIM/WuKongIM/internalv2/runtime/presence"
)

// ErrLocalRegistryUnavailable reports a missing local registry dependency.
var ErrLocalRegistryUnavailable = errors.New("internalv2/usecase/presence: local registry unavailable")

// ErrAuthorityUnavailable reports a missing authority client dependency.
var ErrAuthorityUnavailable = errors.New("internalv2/usecase/presence: authority client unavailable")

// ErrSessionNotActive reports that the local session disappeared before activation completed.
var ErrSessionNotActive = errors.New("internalv2/usecase/presence: session not active")

// RouteState records the owner-local lifecycle stage for a route.
type RouteState uint8

const (
	// RouteStatePending marks a session that is locally accepted but not authority-active.
	RouteStatePending RouteState = iota
	// RouteStateActive marks a session that has completed authority registration.
	RouteStateActive
	// RouteStateClosing marks a session removed from the local route index.
	RouteStateClosing
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

// SessionHandle closes a concrete gateway session through an entry-agnostic boundary.
type SessionHandle interface {
	CloseSession(reason string) error
}

// OnlineConn is the usecase DTO stored through the local owner registry port.
type OnlineConn struct {
	// UID is the authenticated user ID for this connection.
	UID string
	// HashSlot is the user hash slot used by bounded rehydrate scans.
	HashSlot uint16
	// OwnerNodeID is the gateway node that owns the real session.
	OwnerNodeID uint64
	// OwnerBootID identifies the owner process generation that accepted this route.
	OwnerBootID uint64
	// OwnerSeq is the owner-local monotonic sequence used for authority fencing.
	OwnerSeq uint64
	// SessionID is the owner-local gateway session identifier.
	SessionID uint64
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
	// State records the owner-local lifecycle state.
	State RouteState
	// Session holds the entry-provided close handle for this route.
	Session SessionHandle
}

// RouteTarget fences an authority operation to one observed hash-slot route.
type RouteTarget = authority.RouteTarget

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

// RehydrateResult reports per-route replay outcome after an authority change.
type RehydrateResult = authority.RehydrateResult
