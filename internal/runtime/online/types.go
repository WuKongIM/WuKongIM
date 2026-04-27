package online

import (
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

var ErrInvalidConnection = errors.New("runtime/online: invalid connection")

type Conn struct {
	SessionID   uint64
	UID         string
	DeviceID    string
	DeviceFlag  frame.DeviceFlag
	DeviceLevel frame.DeviceLevel
	SlotID      uint64
	State       LocalRouteState
	Listener    string
	ConnectedAt time.Time
	Session     Session
}

type OnlineConn = Conn

// SessionWriter is the minimal connection writer required by the online runtime.
type SessionWriter interface {
	WriteFrame(frame.Frame) error
	Close() error
	ID() uint64
	Listener() string
	RemoteAddr() string
	LocalAddr() string
	SetValue(key string, value any)
	Value(key string) any
}

type Session = SessionWriter

type LocalRouteState uint8

const (
	LocalRouteStateActive LocalRouteState = iota
	LocalRouteStateClosing
)

type SlotSnapshot struct {
	SlotID uint64
	Count  int
	Digest uint64
}

// Summary contains aggregate local online connection counts for drain safety.
type Summary struct {
	// Active counts sessions available for delivery.
	Active int
	// Closing counts sessions that are closing but not fully unregistered.
	Closing int
	// Total counts all sessions tracked by the registry.
	Total int
	// SessionsByListener groups tracked sessions by gateway listener.
	SessionsByListener map[string]int
}

type Registry interface {
	Register(conn OnlineConn) error
	Unregister(sessionID uint64)
	MarkClosing(sessionID uint64) (OnlineConn, bool)
	Connection(sessionID uint64) (OnlineConn, bool)
	ConnectionsByUID(uid string) []OnlineConn
	ActiveConnectionsBySlot(slotID uint64) []OnlineConn
	ActiveSlots() []SlotSnapshot
	Summary() Summary
}

type Delivery interface {
	Deliver(recipients []OnlineConn, f frame.Frame) error
}
