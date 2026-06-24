package management

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/online"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// ErrConnectionReaderUnavailable reports that a requested node connection source is not available.
var ErrConnectionReaderUnavailable = errors.New("internalv2/usecase/management: connection reader unavailable")

// ConnectionReader exposes owner-local gateway session snapshots.
type ConnectionReader interface {
	// LocalSessions returns currently indexed owner-local session records.
	LocalSessions() []online.LocalSession
}

// RemoteConnectionReader reads connection inventory from another owner node.
type RemoteConnectionReader interface {
	// NodeConnections returns active connections currently registered on one cluster node.
	NodeConnections(ctx context.Context, nodeID uint64, limit int) ([]Connection, error)
	// NodeConnection returns one connection currently registered on one cluster node.
	NodeConnection(ctx context.Context, nodeID, sessionID uint64) (ConnectionDetail, error)
}

// ListConnectionsRequest selects the connection inventory to read.
type ListConnectionsRequest struct {
	// NodeID optionally filters to one owner node. Zero means the local node.
	NodeID uint64
	// Limit bounds the returned active connection rows. Zero uses the default.
	Limit int
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

// ListConnections returns manager-facing local active connections ordered by freshness.
func (a *App) ListConnections(ctx context.Context, req ListConnectionsRequest) ([]Connection, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if a == nil || a.connections == nil {
		return nil, nil
	}
	limit := normalizeConnectionListLimit(req.Limit)
	localNodeID := a.localNodeID()
	if !a.connectionRequestTargetsLocal(req.NodeID, localNodeID) {
		if a.remoteConnections == nil {
			return nil, ErrConnectionReaderUnavailable
		}
		return a.remoteConnections.NodeConnections(ctx, req.NodeID, limit)
	}
	snapshot, err := a.localControlSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]Connection, 0, limit)
	for _, session := range a.connections.LocalSessions() {
		if session.State != online.RouteStateActive {
			continue
		}
		item := managerConnection(localNodeID, snapshot.HashSlots, session)
		if len(items) < limit {
			items = append(items, item)
			continue
		}
		worst := 0
		for i := 1; i < len(items); i++ {
			if connectionLessFresh(items[i], items[worst]) {
				worst = i
			}
		}
		if connectionLessFresh(items[worst], item) {
			items[worst] = item
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return connectionComesBefore(items[i], items[j])
	})
	return items, nil
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

// GetConnection returns one manager-facing local connection detail DTO.
func (a *App) GetConnection(ctx context.Context, req GetConnectionRequest) (ConnectionDetail, error) {
	if err := ctxErr(ctx); err != nil {
		return ConnectionDetail{}, err
	}
	if req.SessionID == 0 {
		return ConnectionDetail{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.connections == nil {
		return ConnectionDetail{}, metadb.ErrNotFound
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
	for _, session := range a.connections.LocalSessions() {
		if session.Route.SessionID == req.SessionID {
			return managerConnection(localNodeID, snapshot.HashSlots, session), nil
		}
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
		DeviceFlag:  managerDeviceFlag(frame.DeviceFlag(route.DeviceFlag)),
		DeviceLevel: managerConnectionDeviceLevel(frame.DeviceLevel(route.DeviceLevel)),
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
