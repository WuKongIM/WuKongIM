package presence

import "errors"

// ErrNotLeader reports that a RouteTarget no longer matches local authority state.
var ErrNotLeader = errors.New("internal/runtime/presence: not leader")

// ErrStaleRoute reports an owner route older than the stored owner-sequence fence.
var ErrStaleRoute = errors.New("internal/runtime/presence: stale route")

// ErrRouteNotReady reports that a pending route token cannot be committed or aborted.
var ErrRouteNotReady = errors.New("internal/runtime/presence: route not ready")

// RouteTarget fences an authority operation to one observed hash-slot route.
type RouteTarget struct {
	// HashSlot is the logical user hash slot owned by this authority.
	HashSlot uint16
	// SlotID is the cluster slot currently hosting the hash slot authority.
	SlotID uint32
	// LeaderNodeID is the node that the caller observed as the slot leader.
	LeaderNodeID uint64
	// LeaderTerm is the Slot Raft term observed for LeaderNodeID.
	LeaderTerm uint64
	// ConfigEpoch is the control-plane Slot config epoch.
	ConfigEpoch uint64
	// RouteRevision is the caller-observed routing-table revision.
	RouteRevision uint64
	// AuthorityEpoch is a local observation sequence kept for diagnostics only.
	AuthorityEpoch uint64
}

// Route identifies a virtual client connection known by the UID authority.
type Route struct {
	// UID is the authenticated user ID for this route.
	UID string
	// OwnerNodeID is the gateway node that owns the real client session.
	OwnerNodeID uint64
	// OwnerBootID identifies the owner node process generation.
	OwnerBootID uint64
	// OwnerSeq is the owner-local monotonic sequence for route fencing.
	OwnerSeq uint64
	// SessionID is the owner-local gateway session identifier.
	SessionID uint64
	// DeviceID is the authenticated client device identifier.
	DeviceID string
	// DeviceFlag is the WuKong protocol device category.
	DeviceFlag uint8
	// DeviceLevel is the WuKong protocol device conflict level.
	DeviceLevel uint8
	// Listener records the gateway listener that accepted the session.
	Listener string
	// ConnectedUnix records when the owner accepted the route.
	ConnectedUnix int64
	// LastSeenUnix records the latest authority-observed owner activity for TTL expiry.
	LastSeenUnix int64
}

// RouteAction asks an owner node to resolve an authority-side route conflict.
type RouteAction struct {
	// UID is the user whose existing route should be acted on.
	UID string
	// OwnerNodeID is the node that owns the conflicting route.
	OwnerNodeID uint64
	// OwnerBootID identifies the owner node process generation.
	OwnerBootID uint64
	// SessionID is the owner-local session to close or kick.
	SessionID uint64
	// Kind names the owner action to apply.
	Kind string
	// Reason explains why the authority requested the action.
	Reason string
	// DelayMS optionally delays the owner action.
	DelayMS int64
}

// RouteIdentity identifies one route independently from mutable route metadata.
type RouteIdentity struct {
	// UID is the authenticated user ID for this route.
	UID string
	// OwnerNodeID is the gateway node that owns the real client session.
	OwnerNodeID uint64
	// OwnerBootID identifies the owner node process generation.
	OwnerBootID uint64
	// SessionID is the owner-local gateway session identifier.
	SessionID uint64
}

// PendingRouteToken names a conflict candidate waiting for action completion.
type PendingRouteToken string

// RegisterResult describes immediate or pending authority registration work.
type RegisterResult struct {
	// PendingToken is set when a conflicting route must be committed later.
	PendingToken PendingRouteToken
	// Actions are owner-side operations required before committing a pending route.
	Actions []RouteAction
}

// Snapshot summarizes local authority route state for bench diagnostics.
type Snapshot struct {
	// Active counts active authority routes on this node.
	Active int
	// ByHashSlot groups active authority routes by hash slot.
	ByHashSlot map[uint16]int
	// TouchRoutesTotal counts touch route entries accepted by the target fence.
	TouchRoutesTotal uint64
	// ExpiredRoutesTotal counts routes expired by TTL cleanup.
	ExpiredRoutesTotal uint64
}

// DirectoryOptions configures the authority route directory.
type DirectoryOptions struct {
	// LocalNodeID optionally fences authority targets to this local node.
	LocalNodeID uint64
	// ShardCount controls the number of hash-slot shards; values <= 0 use the default.
	ShardCount int
}
