package management

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestConnectionsReturnUnavailableWithoutLocalReader(t *testing.T) {
	app := New(Options{})

	if _, err := app.ListConnections(context.Background(), ListConnectionsRequest{}); !errors.Is(err, ErrConnectionReaderUnavailable) {
		t.Fatalf("ListConnections() error = %v, want %v", err, ErrConnectionReaderUnavailable)
	}
	if _, err := app.GetConnection(context.Background(), GetConnectionRequest{SessionID: 1}); !errors.Is(err, ErrConnectionReaderUnavailable) {
		t.Fatalf("GetConnection() error = %v, want %v", err, ErrConnectionReaderUnavailable)
	}
}

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

	got, err := app.ListConnections(context.Background(), ListConnectionsRequest{Limit: 1})
	if err != nil {
		t.Fatalf("ListConnections() error = %v", err)
	}

	want := []Connection{
		{
			NodeID: 1, SessionID: 12, UID: "u2", DeviceID: "d2", DeviceFlag: "web", DeviceLevel: "slave",
			SlotID: 1, State: "active", Listener: "ws", ConnectedAt: time.Unix(102, 0).UTC(),
			RemoteAddr: "10.0.0.2:5000", LocalAddr: "127.0.0.1:7100",
		},
	}
	if got.Total != 2 || !got.HasMore || got.NextCursor != (ConnectionListCursor{ConnectedAt: want[0].ConnectedAt, SessionID: want[0].SessionID}) {
		t.Fatalf("page = %#v, want total 2 with a next cursor after session 12", got)
	}
	if !sameConnections(got.Items, want) {
		t.Fatalf("connections = %#v, want %#v", got.Items, want)
	}

	next, err := app.ListConnections(context.Background(), ListConnectionsRequest{Limit: 1, Cursor: got.NextCursor})
	if err != nil {
		t.Fatalf("ListConnections(next) error = %v", err)
	}
	if next.Total != 2 || next.HasMore || len(next.Items) != 1 || next.Items[0].SessionID != older.SessionID {
		t.Fatalf("next page = %#v, want final page with session %d and total 2", next, older.SessionID)
	}
}

func TestListConnectionsCursorUsesSessionIDForEqualTimestamps(t *testing.T) {
	registry := online.NewRegistry(online.RegistryOptions{ShardCount: 2})
	for _, sessionID := range []uint64{12, 10, 11} {
		route := online.OwnerRoute{
			UID: "u", HashSlot: 1, OwnerNodeID: 1, SessionID: sessionID, ConnectedUnix: 100,
		}
		requireNoError(t, registry.RegisterPending(online.LocalSession{Route: route}))
		requireNoError(t, registry.MarkActive(route.SessionID))
	}
	app := New(Options{
		Cluster:     fakeNodeSnapshotReader{snapshot: singleUserSlotSnapshot(), nodeID: 1},
		Connections: registry,
	})

	cursor := ConnectionListCursor{}
	for pageNumber, wantSessionID := range []uint64{10, 11, 12} {
		page, err := app.ListConnections(context.Background(), ListConnectionsRequest{Limit: 1, Cursor: cursor})
		if err != nil {
			t.Fatalf("ListConnections(page %d) error = %v", pageNumber+1, err)
		}
		if page.Total != 3 || len(page.Items) != 1 || page.Items[0].SessionID != wantSessionID {
			t.Fatalf("page %d = %#v, want session %d of total 3", pageNumber+1, page, wantSessionID)
		}
		cursor = page.NextCursor
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
		page:   ListConnectionsResponse{Total: 1, Items: []Connection{{NodeID: 2, SessionID: 22, UID: "u2"}}},
		detail: ConnectionDetail{NodeID: 2, SessionID: 23, UID: "u3"},
	}
	app := New(Options{
		Cluster:           fakeNodeSnapshotReader{snapshot: singleUserSlotSnapshot(), nodeID: 1},
		Connections:       online.NewRegistry(online.RegistryOptions{}),
		RemoteConnections: remote,
	})

	page, err := app.ListConnections(context.Background(), ListConnectionsRequest{NodeID: 2, Limit: 100})
	if err != nil {
		t.Fatalf("ListConnections(remote) error = %v", err)
	}
	if !sameConnections(page.Items, remote.page.Items) || remote.listReq != (ListConnectionsRequest{NodeID: 2, Limit: 100}) {
		t.Fatalf("remote page = %#v request=%#v, want %#v", page, remote.listReq, remote.page)
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
	listReq         ListConnectionsRequest
	detailNodeID    uint64
	detailSessionID uint64
	page            ListConnectionsResponse
	detail          ConnectionDetail
}

func (f *fakeConnectionRemoteReader) NodeConnections(_ context.Context, req ListConnectionsRequest) (ListConnectionsResponse, error) {
	f.listReq = req
	resp := f.page
	resp.Items = append([]Connection(nil), f.page.Items...)
	return resp, nil
}

func (f *fakeConnectionRemoteReader) NodeConnection(_ context.Context, nodeID, sessionID uint64) (ConnectionDetail, error) {
	f.detailNodeID = nodeID
	f.detailSessionID = sessionID
	return f.detail, nil
}
