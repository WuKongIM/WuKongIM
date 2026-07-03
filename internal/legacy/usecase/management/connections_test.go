package management

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/legacy/runtime/online"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/meta"
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
		Session: managementTestSession{
			id:         5,
			listener:   "tcp",
			remoteAddr: "10.0.0.5:5000",
			localAddr:  "127.0.0.1:7000",
		},
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
		Session: managementTestSession{
			id:         3,
			listener:   "ws",
			remoteAddr: "10.0.0.3:5001",
			localAddr:  "127.0.0.1:7100",
		},
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
		Session: managementTestSession{
			id:         8,
			listener:   "tcp",
			remoteAddr: "10.0.0.8:5002",
			localAddr:  "127.0.0.1:7200",
		},
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
		Session: managementTestSession{
			id:         12,
			listener:   "tcp",
			remoteAddr: "10.0.0.12:5003",
			localAddr:  "127.0.0.1:7300",
		},
	}))

	app := New(Options{LocalNodeID: 2, Online: reg})

	got, err := app.ListConnections(context.Background(), ListConnectionsRequest{NodeID: 2})
	require.NoError(t, err)
	require.Equal(t, []connectionSummary{
		{
			NodeID:      2,
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
			NodeID:      2,
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
			NodeID:      2,
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

func TestListConnectionsReadsSelectedRemoteNode(t *testing.T) {
	reader := &connectionReaderStub{
		connections: []Connection{{
			NodeID:      3,
			SessionID:   303,
			UID:         "remote-user",
			DeviceID:    "remote-device",
			DeviceFlag:  "web",
			DeviceLevel: "master",
			SlotID:      7,
			State:       "active",
			Listener:    "ws",
			ConnectedAt: time.Unix(300, 0),
			RemoteAddr:  "10.0.0.3:5000",
			LocalAddr:   "127.0.0.1:7300",
		}},
	}
	app := New(Options{LocalNodeID: 2, Online: online.NewRegistry(), Connections: reader})

	got, err := app.ListConnections(context.Background(), ListConnectionsRequest{NodeID: 3})

	require.NoError(t, err)
	require.Equal(t, uint64(3), reader.listNodeID)
	require.Equal(t, []uint64{303}, connectionIDs(got))
	require.Equal(t, uint64(3), got[0].NodeID)
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
		Session: managementTestSession{
			id:         101,
			listener:   "tcp",
			remoteAddr: "172.16.0.1:5500",
			localAddr:  "127.0.0.1:7500",
		},
	}))

	app := New(Options{LocalNodeID: 2, Online: reg})

	got, err := app.GetConnection(context.Background(), GetConnectionRequest{NodeID: 2, SessionID: 101})
	require.NoError(t, err)
	require.Equal(t, ConnectionDetail{
		NodeID:      2,
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

func TestGetConnectionReadsSelectedRemoteNode(t *testing.T) {
	reader := &connectionReaderStub{
		connection: ConnectionDetail{
			NodeID:      3,
			SessionID:   303,
			UID:         "remote-user",
			DeviceID:    "remote-device",
			DeviceFlag:  "web",
			DeviceLevel: "master",
			SlotID:      7,
			State:       "active",
			Listener:    "ws",
			ConnectedAt: time.Unix(300, 0),
			RemoteAddr:  "10.0.0.3:5000",
			LocalAddr:   "127.0.0.1:7300",
		},
	}
	app := New(Options{LocalNodeID: 2, Online: online.NewRegistry(), Connections: reader})

	got, err := app.GetConnection(context.Background(), GetConnectionRequest{NodeID: 3, SessionID: 303})

	require.NoError(t, err)
	require.Equal(t, uint64(3), reader.detailNodeID)
	require.Equal(t, uint64(303), reader.detailSessionID)
	require.Equal(t, reader.connection, got)
}

func TestGetConnectionReturnsNotFoundWhenMissing(t *testing.T) {
	app := New(Options{Online: online.NewRegistry()})

	_, err := app.GetConnection(context.Background(), GetConnectionRequest{SessionID: 404})
	require.ErrorIs(t, err, controllermeta.ErrNotFound)
}

type connectionSummary struct {
	NodeID      uint64
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
			NodeID:      item.NodeID,
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

func connectionIDs(items []Connection) []uint64 {
	out := make([]uint64, 0, len(items))
	for _, item := range items {
		out = append(out, item.SessionID)
	}
	return out
}

type connectionReaderStub struct {
	connections     []Connection
	connection      ConnectionDetail
	listNodeID      uint64
	detailNodeID    uint64
	detailSessionID uint64
}

func (r *connectionReaderStub) NodeConnections(_ context.Context, nodeID uint64) ([]Connection, error) {
	r.listNodeID = nodeID
	return append([]Connection(nil), r.connections...), nil
}

func (r *connectionReaderStub) NodeConnection(_ context.Context, nodeID uint64, sessionID uint64) (ConnectionDetail, error) {
	r.detailNodeID = nodeID
	r.detailSessionID = sessionID
	return r.connection, nil
}

type managementTestSession struct {
	id         uint64
	listener   string
	remoteAddr string
	localAddr  string
}

func (s managementTestSession) ID() uint64 {
	return s.id
}

func (s managementTestSession) Listener() string {
	return s.listener
}

func (s managementTestSession) RemoteAddr() string {
	return s.remoteAddr
}

func (s managementTestSession) LocalAddr() string {
	return s.localAddr
}

func (s managementTestSession) WriteFrame(frame.Frame) error {
	return nil
}

func (s managementTestSession) Close() error {
	return nil
}

func (s managementTestSession) SetValue(string, any) {}

func (s managementTestSession) Value(string) any {
	return nil
}
