package online

import "errors"

// ErrInvalidConnection reports a malformed online connection registration.
var ErrInvalidConnection = errors.New("internalv2/runtime/online: invalid connection")

// ErrConnectionNotFound reports that a session ID is not registered locally.
var ErrConnectionNotFound = errors.New("internalv2/runtime/online: connection not found")

// RouteState records the owner-local lifecycle stage for a gateway session.
type RouteState uint8

const (
	// RouteStatePending marks a session that has connected but is not route-active yet.
	RouteStatePending RouteState = iota
	// RouteStateActive marks a session that can receive owner-local routed traffic.
	RouteStateActive
	// RouteStateClosing marks a session removed from local indexes during shutdown.
	RouteStateClosing
)

// OnlineConn describes one owner-local gateway session route.
type OnlineConn struct {
	// UID is the authenticated user ID for this connection.
	UID string
	// HashSlot is the user hash slot used by authority-change rehydrate scans.
	HashSlot uint16
	// OwnerNodeID is the gateway owner node that accepted this route.
	OwnerNodeID uint64
	// OwnerBootID identifies the owner node process generation that accepted this route.
	OwnerBootID uint64
	// OwnerSeq is the owner-local monotonic route sequence for conflict fencing.
	OwnerSeq uint64
	// SessionID is the owner-local gateway session identifier.
	SessionID uint64
	// DeviceID is the authenticated client device identifier.
	DeviceID string
	// DeviceFlag is the WuKong protocol device category for the session.
	DeviceFlag uint8
	// DeviceLevel is the WuKong protocol device conflict level for the session.
	DeviceLevel uint8
	// Listener records the gateway listener that accepted the session.
	Listener string
	// ConnectedUnix records when the gateway session was accepted locally.
	ConnectedUnix int64
	// State records the owner-local lifecycle state.
	State RouteState
	// Session holds the gateway context used for local writes and close handling.
	Session SessionHandle
}

// SessionHandle closes a concrete gateway session without importing entry packages.
type SessionHandle interface {
	// CloseSession closes the concrete session with a stable reason string.
	CloseSession(reason string) error
}

// RouteCursor resumes a bounded active-route scan after the last delivered session.
type RouteCursor struct {
	// LastSessionID is the highest session ID delivered by the previous page.
	LastSessionID uint64
}

// RegistryOptions configures the owner-local online registry.
type RegistryOptions struct {
	// ShardCount controls the number of session ID shards; values <= 0 use the default.
	ShardCount int
}
