package management

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/online"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestListConnectionsReturnsActiveLocalSessionsOrderedByConnectedAtDesc(t *testing.T) {
	registry := online.NewRegistry(online.RegistryOptions{ShardCount: 2})
	pending := online.OwnerRoute{UID: "pending", HashSlot: 1, OwnerNodeID: 1, SessionID: 10, ConnectedUnix: 100}
	older := online.OwnerRoute{
		UID: "u1", HashSlot: 1, OwnerNodeID: 1, SessionID: 11, DeviceID: "d1",
		DeviceFlag: uint8(frame.APP), DeviceLevel: uint8(frame.DeviceLevelMaster),
		Listener: "tcp", ConnectedUnix: 101,
	}
	newer := online.OwnerRoute{
		UID: "u2", HashSlot: 2, OwnerNodeID: 1, SessionID: 12, DeviceID: "d2",
		DeviceFlag: uint8(frame.WEB), DeviceLevel: uint8(frame.DeviceLevelSlave),
		Listener: "ws", ConnectedUnix: 102,
	}
	requireNoError(t, registry.RegisterPending(online.LocalSession{Route: pending}))
	requireNoError(t, registry.RegisterPending(online.LocalSession{Route: older, Session: connectionAddressHandle{remote: "10.0.0.1:5000", local: "127.0.0.1:7000"}}))
	requireNoError(t, registry.RegisterPending(online.LocalSession{Route: newer, Session: connectionAddressHandle{remote: "10.0.0.2:5000", local: "127.0.0.1:7100"}}))
	requireNoError(t, registry.MarkActive(older.SessionID))
	requireNoError(t, registry.MarkActive(newer.SessionID))
	app := New(Options{
		Cluster:     fakeNodeSnapshotReader{snapshot: singleUserSlotSnapshot(), nodeID: 1},
		Connections: registry,
	})

	got, err := app.ListConnections(context.Background(), ListConnectionsRequest{})
	if err != nil {
		t.Fatalf("ListConnections() error = %v", err)
	}

	want := []Connection{
		{
			NodeID: 1, SessionID: 12, UID: "u2", DeviceID: "d2", DeviceFlag: "web", DeviceLevel: "slave",
			SlotID: 1, State: "active", Listener: "ws", ConnectedAt: time.Unix(102, 0).UTC(),
			RemoteAddr: "10.0.0.2:5000", LocalAddr: "127.0.0.1:7100",
		},
		{
			NodeID: 1, SessionID: 11, UID: "u1", DeviceID: "d1", DeviceFlag: "app", DeviceLevel: "master",
			SlotID: 1, State: "active", Listener: "tcp", ConnectedAt: time.Unix(101, 0).UTC(),
			RemoteAddr: "10.0.0.1:5000", LocalAddr: "127.0.0.1:7000",
		},
	}
	if !sameConnections(got, want) {
		t.Fatalf("connections = %#v, want %#v", got, want)
	}
}

func TestGetConnectionReturnsLocalSessionDetailAndNotFound(t *testing.T) {
	registry := online.NewRegistry(online.RegistryOptions{})
	route := online.OwnerRoute{
		UID: "u1", HashSlot: 1, OwnerNodeID: 1, SessionID: 11, DeviceID: "d1",
		DeviceFlag: uint8(frame.PC), DeviceLevel: uint8(frame.DeviceLevelSlave),
		Listener: "tcp", ConnectedUnix: 101,
	}
	requireNoError(t, registry.RegisterPending(online.LocalSession{Route: route}))
	app := New(Options{
		Cluster:     fakeNodeSnapshotReader{snapshot: singleUserSlotSnapshot(), nodeID: 1},
		Connections: registry,
	})

	got, err := app.GetConnection(context.Background(), GetConnectionRequest{SessionID: 11})
	if err != nil {
		t.Fatalf("GetConnection() error = %v", err)
	}
	want := ConnectionDetail{
		NodeID: 1, SessionID: 11, UID: "u1", DeviceID: "d1", DeviceFlag: "pc", DeviceLevel: "slave",
		SlotID: 1, State: "pending", Listener: "tcp", ConnectedAt: time.Unix(101, 0).UTC(),
	}
	if got != want {
		t.Fatalf("connection detail = %#v, want %#v", got, want)
	}

	_, err = app.GetConnection(context.Background(), GetConnectionRequest{SessionID: 404})
	if err != meta.ErrNotFound {
		t.Fatalf("GetConnection(missing) error = %v, want %v", err, meta.ErrNotFound)
	}
}

func TestConnectionsUseRemoteReaderForNonLocalNode(t *testing.T) {
	remote := &fakeConnectionRemoteReader{
		connections: []Connection{{NodeID: 2, SessionID: 22, UID: "u2"}},
		detail:      ConnectionDetail{NodeID: 2, SessionID: 23, UID: "u3"},
	}
	app := New(Options{
		Cluster:           fakeNodeSnapshotReader{snapshot: singleUserSlotSnapshot(), nodeID: 1},
		Connections:       online.NewRegistry(online.RegistryOptions{}),
		RemoteConnections: remote,
	})

	items, err := app.ListConnections(context.Background(), ListConnectionsRequest{NodeID: 2})
	if err != nil {
		t.Fatalf("ListConnections(remote) error = %v", err)
	}
	if !sameConnections(items, remote.connections) || remote.listNodeID != 2 {
		t.Fatalf("remote list = %#v node=%d, want %#v node 2", items, remote.listNodeID, remote.connections)
	}

	detail, err := app.GetConnection(context.Background(), GetConnectionRequest{NodeID: 2, SessionID: 23})
	if err != nil {
		t.Fatalf("GetConnection(remote) error = %v", err)
	}
	if detail != remote.detail || remote.detailNodeID != 2 || remote.detailSessionID != 23 {
		t.Fatalf("remote detail = %#v node=%d session=%d, want %#v node 2 session 23", detail, remote.detailNodeID, remote.detailSessionID, remote.detail)
	}
}

type connectionAddressHandle struct {
	remote string
	local  string
}

func (h connectionAddressHandle) WriteDelivery(any) error { return nil }

func (h connectionAddressHandle) CloseSession(string) error { return nil }

func (h connectionAddressHandle) RemoteAddr() string { return h.remote }

func (h connectionAddressHandle) LocalAddr() string { return h.local }

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

type fakeConnectionRemoteReader struct {
	listNodeID      uint64
	detailNodeID    uint64
	detailSessionID uint64
	connections     []Connection
	detail          ConnectionDetail
}

func (f *fakeConnectionRemoteReader) NodeConnections(_ context.Context, nodeID uint64) ([]Connection, error) {
	f.listNodeID = nodeID
	return append([]Connection(nil), f.connections...), nil
}

func (f *fakeConnectionRemoteReader) NodeConnection(_ context.Context, nodeID, sessionID uint64) (ConnectionDetail, error) {
	f.detailNodeID = nodeID
	f.detailSessionID = sessionID
	return f.detail, nil
}
