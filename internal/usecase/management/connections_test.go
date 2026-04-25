package management

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway/session"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestListConnectionsReturnsMappedItemsOrderedByConnectedAtDesc(t *testing.T) {
	reg := online.NewRegistry()
	base := time.Date(2026, 4, 23, 8, 0, 0, 0, time.UTC)

	require.NoError(t, reg.Register(online.OnlineConn{
		SessionID:   5,
		UID:         "u-1",
		DeviceID:    "device-a",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
		SlotID:      4,
		State:       online.LocalRouteStateActive,
		Listener:    "tcp",
		ConnectedAt: base.Add(2 * time.Minute),
		Session: session.New(session.Config{
			ID:         5,
			Listener:   "tcp",
			RemoteAddr: "10.0.0.5:5000",
			LocalAddr:  "127.0.0.1:7000",
		}),
	}))
	require.NoError(t, reg.Register(online.OnlineConn{
		SessionID:   3,
		UID:         "u-2",
		DeviceID:    "device-b",
		DeviceFlag:  frame.WEB,
		DeviceLevel: frame.DeviceLevelSlave,
		SlotID:      2,
		State:       online.LocalRouteStateActive,
		Listener:    "ws",
		ConnectedAt: base.Add(4 * time.Minute),
		Session: session.New(session.Config{
			ID:         3,
			Listener:   "ws",
			RemoteAddr: "10.0.0.3:5001",
			LocalAddr:  "127.0.0.1:7100",
		}),
	}))
	require.NoError(t, reg.Register(online.OnlineConn{
		SessionID:   8,
		UID:         "u-3",
		DeviceID:    "device-c",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelSlave,
		SlotID:      2,
		State:       online.LocalRouteStateActive,
		Listener:    "tcp",
		ConnectedAt: base.Add(4 * time.Minute),
		Session: session.New(session.Config{
			ID:         8,
			Listener:   "tcp",
			RemoteAddr: "10.0.0.8:5002",
			LocalAddr:  "127.0.0.1:7200",
		}),
	}))
	require.NoError(t, reg.Register(online.OnlineConn{
		SessionID:   12,
		UID:         "u-4",
		DeviceID:    "device-d",
		DeviceFlag:  frame.SYSTEM,
		DeviceLevel: frame.DeviceLevelMaster,
		SlotID:      9,
		State:       online.LocalRouteStateClosing,
		Listener:    "tcp",
		ConnectedAt: base.Add(5 * time.Minute),
		Session: session.New(session.Config{
			ID:         12,
			Listener:   "tcp",
			RemoteAddr: "10.0.0.12:5003",
			LocalAddr:  "127.0.0.1:7300",
		}),
	}))

	app := New(Options{Online: reg})

	got, err := app.ListConnections(context.Background())
	require.NoError(t, err)
	require.Equal(t, []connectionSummary{
		{
			SessionID:   3,
			UID:         "u-2",
			DeviceID:    "device-b",
			DeviceFlag:  "web",
			DeviceLevel: "slave",
			SlotID:      2,
			State:       "active",
			Listener:    "ws",
			RemoteAddr:  "10.0.0.3:5001",
			LocalAddr:   "127.0.0.1:7100",
		},
		{
			SessionID:   8,
			UID:         "u-3",
			DeviceID:    "device-c",
			DeviceFlag:  "app",
			DeviceLevel: "slave",
			SlotID:      2,
			State:       "active",
			Listener:    "tcp",
			RemoteAddr:  "10.0.0.8:5002",
			LocalAddr:   "127.0.0.1:7200",
		},
		{
			SessionID:   5,
			UID:         "u-1",
			DeviceID:    "device-a",
			DeviceFlag:  "app",
			DeviceLevel: "master",
			SlotID:      4,
			State:       "active",
			Listener:    "tcp",
			RemoteAddr:  "10.0.0.5:5000",
			LocalAddr:   "127.0.0.1:7000",
		},
	}, summarizeConnections(got))
	require.Equal(t, []time.Time{
		base.Add(4 * time.Minute),
		base.Add(4 * time.Minute),
		base.Add(2 * time.Minute),
	}, connectionTimes(got))
}

func TestGetConnectionReturnsMappedDetail(t *testing.T) {
	reg := online.NewRegistry()
	connectedAt := time.Date(2026, 4, 23, 8, 10, 0, 0, time.UTC)
	require.NoError(t, reg.Register(online.OnlineConn{
		SessionID:   101,
		UID:         "user-a",
		DeviceID:    "device-z",
		DeviceFlag:  frame.SYSTEM,
		DeviceLevel: frame.DeviceLevelMaster,
		SlotID:      11,
		State:       online.LocalRouteStateClosing,
		Listener:    "tcp",
		ConnectedAt: connectedAt,
		Session: session.New(session.Config{
			ID:         101,
			Listener:   "tcp",
			RemoteAddr: "172.16.0.1:5500",
			LocalAddr:  "127.0.0.1:7500",
		}),
	}))

	app := New(Options{Online: reg})

	got, err := app.GetConnection(context.Background(), 101)
	require.NoError(t, err)
	require.Equal(t, ConnectionDetail{
		SessionID:   101,
		UID:         "user-a",
		DeviceID:    "device-z",
		DeviceFlag:  "system",
		DeviceLevel: "master",
		SlotID:      11,
		State:       "closing",
		Listener:    "tcp",
		ConnectedAt: connectedAt,
		RemoteAddr:  "172.16.0.1:5500",
		LocalAddr:   "127.0.0.1:7500",
	}, got)
}

func TestGetConnectionReturnsNotFoundWhenMissing(t *testing.T) {
	app := New(Options{Online: online.NewRegistry()})

	_, err := app.GetConnection(context.Background(), 404)
	require.ErrorIs(t, err, controllermeta.ErrNotFound)
}

type connectionSummary struct {
	SessionID   uint64
	UID         string
	DeviceID    string
	DeviceFlag  string
	DeviceLevel string
	SlotID      uint64
	State       string
	Listener    string
	RemoteAddr  string
	LocalAddr   string
}

func summarizeConnections(items []Connection) []connectionSummary {
	out := make([]connectionSummary, 0, len(items))
	for _, item := range items {
		out = append(out, connectionSummary{
			SessionID:   item.SessionID,
			UID:         item.UID,
			DeviceID:    item.DeviceID,
			DeviceFlag:  item.DeviceFlag,
			DeviceLevel: item.DeviceLevel,
			SlotID:      item.SlotID,
			State:       item.State,
			Listener:    item.Listener,
			RemoteAddr:  item.RemoteAddr,
			LocalAddr:   item.LocalAddr,
		})
	}
	return out
}

func connectionTimes(items []Connection) []time.Time {
	out := make([]time.Time, 0, len(items))
	for _, item := range items {
		out = append(out, item.ConnectedAt)
	}
	return out
}
