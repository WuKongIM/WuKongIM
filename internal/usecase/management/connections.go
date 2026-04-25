package management

import (
	"context"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// Connection is the manager-facing local connection DTO.
type Connection struct {
	// SessionID is the gateway session identifier.
	SessionID uint64
	// UID is the authenticated user identifier.
	UID string
	// DeviceID is the client device identifier.
	DeviceID string
	// DeviceFlag is the stable manager-facing device flag string.
	DeviceFlag string
	// DeviceLevel is the stable manager-facing device level string.
	DeviceLevel string
	// SlotID is the local authoritative slot identifier for the user.
	SlotID uint64
	// State is the local runtime connection state string.
	State string
	// Listener is the listener name that accepted the connection.
	Listener string
	// ConnectedAt is the initial local connection time.
	ConnectedAt time.Time
	// RemoteAddr is the client remote address observed by the listener.
	RemoteAddr string
	// LocalAddr is the local listener address observed by the listener.
	LocalAddr string
}

// ConnectionDetail is the manager-facing local connection detail DTO.
type ConnectionDetail = Connection

// ListConnections returns manager-facing local active connections ordered by freshness.
func (a *App) ListConnections(ctx context.Context) ([]Connection, error) {
	_ = ctx
	if a == nil || a.online == nil {
		return nil, nil
	}

	slots := a.online.ActiveSlots()
	items := make([]Connection, 0)
	for _, slot := range slots {
		for _, conn := range a.online.ActiveConnectionsBySlot(slot.SlotID) {
			items = append(items, managerConnection(conn))
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].ConnectedAt.Equal(items[j].ConnectedAt) {
			return items[i].SessionID < items[j].SessionID
		}
		return items[i].ConnectedAt.After(items[j].ConnectedAt)
	})
	return items, nil
}

// GetConnection returns one manager-facing local connection detail DTO.
func (a *App) GetConnection(ctx context.Context, sessionID uint64) (ConnectionDetail, error) {
	_ = ctx
	if a == nil || a.online == nil {
		return ConnectionDetail{}, nil
	}

	conn, ok := a.online.Connection(sessionID)
	if !ok {
		return ConnectionDetail{}, controllermeta.ErrNotFound
	}
	return managerConnection(conn), nil
}

func managerConnection(conn online.OnlineConn) Connection {
	remoteAddr := ""
	localAddr := ""
	if conn.Session != nil {
		remoteAddr = conn.Session.RemoteAddr()
		localAddr = conn.Session.LocalAddr()
	}

	return Connection{
		SessionID:   conn.SessionID,
		UID:         conn.UID,
		DeviceID:    conn.DeviceID,
		DeviceFlag:  managerConnectionDeviceFlag(conn.DeviceFlag),
		DeviceLevel: managerConnectionDeviceLevel(conn.DeviceLevel),
		SlotID:      conn.SlotID,
		State:       managerConnectionState(conn.State),
		Listener:    conn.Listener,
		ConnectedAt: conn.ConnectedAt,
		RemoteAddr:  remoteAddr,
		LocalAddr:   localAddr,
	}
}

func managerConnectionDeviceFlag(flag frame.DeviceFlag) string {
	switch flag {
	case frame.APP:
		return "app"
	case frame.WEB:
		return "web"
	case frame.PC:
		return "pc"
	case frame.SYSTEM:
		return "system"
	default:
		return "unknown"
	}
}

func managerConnectionDeviceLevel(level frame.DeviceLevel) string {
	switch level {
	case frame.DeviceLevelMaster:
		return "master"
	case frame.DeviceLevelSlave:
		return "slave"
	default:
		return "unknown"
	}
}

func managerConnectionState(state online.LocalRouteState) string {
	switch state {
	case online.LocalRouteStateActive:
		return "active"
	case online.LocalRouteStateClosing:
		return "closing"
	default:
		return "unknown"
	}
}
