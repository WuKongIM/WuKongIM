package presence

import (
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type Route struct {
	UID         string
	NodeID      uint64
	BootID      uint64
	SessionID   uint64
	DeviceID    string
	DeviceFlag  uint8
	DeviceLevel uint8
	Listener    string
}

type RouteAction struct {
	UID       string
	NodeID    uint64
	BootID    uint64
	SessionID uint64
	Kind      string
	Reason    string
	DelayMS   int64
}

type GatewayLease struct {
	SlotID         uint64
	GatewayNodeID  uint64
	GatewayBootID  uint64
	RouteCount     int
	RouteDigest    uint64
	LeaseUntilUnix int64
}

type RegisterAuthoritativeCommand struct {
	SlotID uint64
	Route  Route
}

type RegisterAuthoritativeResult struct {
	Actions []RouteAction
}

type UnregisterAuthoritativeCommand struct {
	SlotID uint64
	Route  Route
}

type HeartbeatAuthoritativeCommand struct {
	Lease GatewayLease
}

type HeartbeatAuthoritativeResult struct {
	RouteCount  int
	RouteDigest uint64
	Mismatch    bool
}

type ReplayAuthoritativeCommand struct {
	Lease  GatewayLease
	Routes []Route
}

type ActivateCommand struct {
	UID         string
	DeviceID    string
	DeviceFlag  frame.DeviceFlag
	DeviceLevel frame.DeviceLevel
	Listener    string
	ConnectedAt time.Time
	Session     online.Session
}

type DeactivateCommand struct {
	UID       string
	SessionID uint64
}
