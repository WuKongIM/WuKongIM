package online

import (
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway/session"
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
	Session     session.Session
}

type OnlineConn = Conn
type Session = session.Session

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

type Registry interface {
	Register(conn OnlineConn) error
	Unregister(sessionID uint64)
	MarkClosing(sessionID uint64) (OnlineConn, bool)
	Connection(sessionID uint64) (OnlineConn, bool)
	ConnectionsByUID(uid string) []OnlineConn
	ActiveConnectionsBySlot(slotID uint64) []OnlineConn
	ActiveSlots() []SlotSnapshot
}

type Delivery interface {
	Deliver(recipients []OnlineConn, f frame.Frame) error
}
