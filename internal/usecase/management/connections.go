package management

import (
	"container/heap"
	"context"
	"errors"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/protocolmeta"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ErrConnectionReaderUnavailable reports that a requested node connection source is not available.
var ErrConnectionReaderUnavailable = errors.New("internal/usecase/management: connection reader unavailable")

// ConnectionReader exposes owner-local gateway sessions.
type ConnectionReader interface {
	// LocalSession returns one currently indexed owner-local session record.
	LocalSession(sessionID uint64) (online.LocalSession, bool)
	// RangeLocalSessions visits currently indexed owner-local session records.
	RangeLocalSessions(visit func(online.LocalSession) bool)
}

// RemoteConnectionReader reads connection inventory from another owner node.
type RemoteConnectionReader interface {
	// NodeConnections returns active connections currently registered on one cluster node.
	NodeConnections(ctx context.Context, req ListConnectionsRequest) (ListConnectionsResponse, error)
	// NodeConnection returns one connection currently registered on one cluster node.
	NodeConnection(ctx context.Context, nodeID, sessionID uint64) (ConnectionDetail, error)
}

// ConnectionListCursor identifies the last emitted connection in freshness order.
type ConnectionListCursor struct {
	// ConnectedAt is the connection timestamp of the last emitted row.
	ConnectedAt time.Time
	// SessionID disambiguates connections with the same timestamp.
	SessionID uint64
}

// ListConnectionsRequest selects the connection inventory to read.
type ListConnectionsRequest struct {
	// NodeID optionally filters to one owner node. Zero means the local node.
	NodeID uint64
	// Limit bounds the returned active connection rows. Zero uses the default.
	Limit int
	// Cursor resumes after the last row emitted by the previous page.
	Cursor ConnectionListCursor
}

// ListConnectionsResponse is one bounded connection inventory page.
type ListConnectionsResponse struct {
	// Total is the number of active connections in the selected node snapshot.
	Total int
	// Items contains the current page ordered by freshness.
	Items []Connection
	// HasMore reports whether another older page exists.
	HasMore bool
	// NextCursor resumes after the last item when HasMore is true.
	NextCursor ConnectionListCursor
}

// GetConnectionRequest selects one owner-local connection detail to read.
type GetConnectionRequest struct {
	// NodeID optionally filters to one owner node. Zero means the local node.
	NodeID uint64
	// SessionID is the gateway session identifier on the selected owner node.
	SessionID uint64
}

// ConnectionDetail is the manager-facing local connection detail DTO.
type ConnectionDetail = Connection

type connectionAddressSource interface {
	RemoteAddr() string
	LocalAddr() string
}

// ListConnections returns one manager-facing active connection page ordered by freshness.
func (a *App) ListConnections(ctx context.Context, req ListConnectionsRequest) (ListConnectionsResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return ListConnectionsResponse{}, err
	}
	if (req.Cursor.ConnectedAt.IsZero()) != (req.Cursor.SessionID == 0) {
		return ListConnectionsResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.connections == nil {
		return ListConnectionsResponse{}, ErrConnectionReaderUnavailable
	}
	limit := normalizeConnectionListLimit(req.Limit)
	localNodeID := a.localNodeID()
	if !a.connectionRequestTargetsLocal(req.NodeID, localNodeID) {
		if a.remoteConnections == nil {
			return ListConnectionsResponse{}, ErrConnectionReaderUnavailable
		}
		req.Limit = limit
		return a.remoteConnections.NodeConnections(ctx, req)
	}
	snapshot, err := a.localControlSnapshot(ctx)
	if err != nil {
		return ListConnectionsResponse{}, err
	}
	candidates := make(connectionPageHeap, 0, limit+1)
	heap.Init(&candidates)
	total := 0
	visited := 0
	var scanErr error
	a.connections.RangeLocalSessions(func(session online.LocalSession) bool {
		visited++
		if visited&255 == 0 {
			if err := ctxErr(ctx); err != nil {
				scanErr = err
				return false
			}
		}
		if session.State != online.RouteStateActive {
			return true
		}
		total++
		item := managerConnection(localNodeID, snapshot.HashSlots, session)
		if !connectionComesAfterCursor(item, req.Cursor) {
			return true
		}
		if candidates.Len() < limit+1 {
			heap.Push(&candidates, item)
			return true
		}
		if connectionLessFresh(candidates[0], item) {
			heap.Pop(&candidates)
			heap.Push(&candidates, item)
		}
		return true
	})
	if scanErr != nil {
		return ListConnectionsResponse{}, scanErr
	}
	if err := ctxErr(ctx); err != nil {
		return ListConnectionsResponse{}, err
	}
	items := []Connection(candidates)
	sort.Slice(items, func(i, j int) bool {
		return connectionComesBefore(items[i], items[j])
	})
	resp := ListConnectionsResponse{Total: total}
	if len(items) > limit {
		resp.HasMore = true
		items = items[:limit]
	}
	resp.Items = items
	if resp.HasMore {
		last := items[len(items)-1]
		resp.NextCursor = ConnectionListCursor{ConnectedAt: last.ConnectedAt, SessionID: last.SessionID}
	}
	return resp, nil
}

func normalizeConnectionListLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func connectionComesBefore(a, b Connection) bool {
	if a.ConnectedAt.Equal(b.ConnectedAt) {
		return a.SessionID < b.SessionID
	}
	return a.ConnectedAt.After(b.ConnectedAt)
}

func connectionLessFresh(a, b Connection) bool {
	if a.ConnectedAt.Equal(b.ConnectedAt) {
		return a.SessionID > b.SessionID
	}
	return a.ConnectedAt.Before(b.ConnectedAt)
}

func connectionComesAfterCursor(item Connection, cursor ConnectionListCursor) bool {
	if cursor == (ConnectionListCursor{}) {
		return true
	}
	if item.ConnectedAt.Equal(cursor.ConnectedAt) {
		return item.SessionID > cursor.SessionID
	}
	return item.ConnectedAt.Before(cursor.ConnectedAt)
}

type connectionPageHeap []Connection

func (h connectionPageHeap) Len() int { return len(h) }

func (h connectionPageHeap) Less(i, j int) bool { return connectionLessFresh(h[i], h[j]) }

func (h connectionPageHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *connectionPageHeap) Push(value any) {
	*h = append(*h, value.(Connection))
}

func (h *connectionPageHeap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	*h = old[:last]
	return value
}

// GetConnection returns one manager-facing local connection detail DTO.
func (a *App) GetConnection(ctx context.Context, req GetConnectionRequest) (ConnectionDetail, error) {
	if err := ctxErr(ctx); err != nil {
		return ConnectionDetail{}, err
	}
	if req.SessionID == 0 {
		return ConnectionDetail{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.connections == nil {
		return ConnectionDetail{}, ErrConnectionReaderUnavailable
	}
	localNodeID := a.localNodeID()
	if !a.connectionRequestTargetsLocal(req.NodeID, localNodeID) {
		if a.remoteConnections == nil {
			return ConnectionDetail{}, ErrConnectionReaderUnavailable
		}
		return a.remoteConnections.NodeConnection(ctx, req.NodeID, req.SessionID)
	}
	snapshot, err := a.localControlSnapshot(ctx)
	if err != nil {
		return ConnectionDetail{}, err
	}
	session, ok := a.connections.LocalSession(req.SessionID)
	if ok {
		return managerConnection(localNodeID, snapshot.HashSlots, session), nil
	}
	return ConnectionDetail{}, metadb.ErrNotFound
}

func managerConnection(localNodeID uint64, table control.HashSlotTable, session online.LocalSession) Connection {
	route := session.Route
	nodeID := route.OwnerNodeID
	if nodeID == 0 {
		nodeID = localNodeID
	}
	remoteAddr := ""
	localAddr := ""
	if addr, ok := session.Session.(connectionAddressSource); ok && addr != nil {
		remoteAddr = addr.RemoteAddr()
		localAddr = addr.LocalAddr()
	}
	return Connection{
		NodeID:      nodeID,
		SessionID:   route.SessionID,
		UID:         route.UID,
		DeviceID:    route.DeviceID,
		DeviceFlag:  managerDeviceFlag(protocolmeta.DeviceFlag(route.DeviceFlag)),
		DeviceLevel: managerConnectionDeviceLevel(protocolmeta.DeviceLevel(route.DeviceLevel)),
		SlotID:      uint64(slotIDForHashSlot(table, route.HashSlot)),
		State:       managerRouteState(session.State),
		Listener:    route.Listener,
		ConnectedAt: unixTime(route.ConnectedUnix),
		RemoteAddr:  remoteAddr,
		LocalAddr:   localAddr,
	}
}

func (a *App) connectionRequestTargetsLocal(nodeID, localNodeID uint64) bool {
	return nodeID == 0 || localNodeID == 0 || nodeID == localNodeID
}

func (a *App) localNodeID() uint64 {
	if a == nil || a.cluster == nil {
		return 0
	}
	return a.cluster.NodeID()
}

func (a *App) localControlSnapshot(ctx context.Context) (control.Snapshot, error) {
	if a == nil || a.cluster == nil {
		return control.Snapshot{}, nil
	}
	return a.cluster.LocalControlSnapshot(ctx)
}

func managerRouteState(state online.RouteState) string {
	switch state {
	case online.RouteStatePending:
		return "pending"
	case online.RouteStateActive:
		return "active"
	case online.RouteStateClosing:
		return "closing"
	default:
		return "unknown"
	}
}

func unixTime(sec int64) time.Time {
	if sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
