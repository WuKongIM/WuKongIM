package node

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

type connectionsRequest struct {
	NodeID uint64
}

type connectionRequest struct {
	NodeID    uint64
	SessionID uint64
}

type connectionsResponse struct {
	Status string
	Items  []Connection
}

type connectionResponse struct {
	Status string
	Item   Connection
}

const rpcStatusNotFound = "not_found"

func (a *Adapter) handleConnectionsRPC(ctx context.Context, body []byte) ([]byte, error) {
	_ = ctx
	req, err := decodeConnectionsRequest(body)
	if err != nil {
		return nil, err
	}
	return encodeConnectionsResponse(connectionsResponse{
		Status: rpcStatusOK,
		Items:  a.localConnections(req.NodeID),
	})
}

func (a *Adapter) handleConnectionRPC(ctx context.Context, body []byte) ([]byte, error) {
	_ = ctx
	req, err := decodeConnectionRequest(body)
	if err != nil {
		return nil, err
	}
	conn, ok := a.localConnection(req.NodeID, req.SessionID)
	if !ok {
		return encodeConnectionResponse(connectionResponse{Status: rpcStatusNotFound})
	}
	return encodeConnectionResponse(connectionResponse{Status: rpcStatusOK, Item: conn})
}

func (a *Adapter) localConnections(nodeID uint64) []Connection {
	if a == nil || a.online == nil {
		return nil
	}
	if nodeID == 0 {
		nodeID = a.localNodeID
	}
	slots := a.online.ActiveSlots()
	items := make([]Connection, 0)
	for _, slot := range slots {
		for _, conn := range a.online.ActiveConnectionsBySlot(slot.SlotID) {
			items = append(items, nodeConnection(nodeID, conn))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ConnectedAt.Equal(items[j].ConnectedAt) {
			return items[i].SessionID < items[j].SessionID
		}
		return items[i].ConnectedAt.After(items[j].ConnectedAt)
	})
	return items
}

func (a *Adapter) localConnection(nodeID uint64, sessionID uint64) (Connection, bool) {
	if a == nil || a.online == nil {
		return Connection{}, false
	}
	if nodeID == 0 {
		nodeID = a.localNodeID
	}
	conn, ok := a.online.Connection(sessionID)
	if !ok {
		return Connection{}, false
	}
	return nodeConnection(nodeID, conn), true
}

func nodeConnection(nodeID uint64, conn online.OnlineConn) Connection {
	remoteAddr := ""
	localAddr := ""
	if conn.Session != nil {
		remoteAddr = conn.Session.RemoteAddr()
		localAddr = conn.Session.LocalAddr()
	}
	return Connection{
		NodeID:      nodeID,
		SessionID:   conn.SessionID,
		UID:         conn.UID,
		DeviceID:    conn.DeviceID,
		DeviceFlag:  nodeConnectionDeviceFlag(conn.DeviceFlag),
		DeviceLevel: nodeConnectionDeviceLevel(conn.DeviceLevel),
		SlotID:      conn.SlotID,
		State:       nodeConnectionState(conn.State),
		Listener:    conn.Listener,
		ConnectedAt: conn.ConnectedAt,
		RemoteAddr:  remoteAddr,
		LocalAddr:   localAddr,
	}
}

func nodeConnectionDeviceFlag(flag frame.DeviceFlag) string {
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

func nodeConnectionDeviceLevel(level frame.DeviceLevel) string {
	switch level {
	case frame.DeviceLevelMaster:
		return "master"
	case frame.DeviceLevelSlave:
		return "slave"
	default:
		return "unknown"
	}
}

func nodeConnectionState(state online.LocalRouteState) string {
	switch state {
	case online.LocalRouteStateActive:
		return "active"
	case online.LocalRouteStateClosing:
		return "closing"
	default:
		return "unknown"
	}
}

func (c *Client) Connections(ctx context.Context, nodeID uint64) ([]Connection, error) {
	if c == nil || c.cluster == nil {
		return nil, fmt.Errorf("access/node: cluster not configured")
	}
	body, err := encodeConnectionsRequestBinary(connectionsRequest{NodeID: nodeID})
	if err != nil {
		return nil, err
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, connectionsRPCServiceID, body)
	if err != nil {
		return nil, err
	}
	resp, err := decodeConnectionsResponse(respBody)
	if err != nil {
		return nil, err
	}
	if resp.Status != rpcStatusOK {
		return nil, fmt.Errorf("access/node: connections rpc status %s", resp.Status)
	}
	return resp.Items, nil
}

func (c *Client) Connection(ctx context.Context, nodeID uint64, sessionID uint64) (Connection, error) {
	if c == nil || c.cluster == nil {
		return Connection{}, fmt.Errorf("access/node: cluster not configured")
	}
	body, err := encodeConnectionRequestBinary(connectionRequest{NodeID: nodeID, SessionID: sessionID})
	if err != nil {
		return Connection{}, err
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, connectionRPCServiceID, body)
	if err != nil {
		return Connection{}, err
	}
	resp, err := decodeConnectionResponse(respBody)
	if err != nil {
		return Connection{}, err
	}
	switch resp.Status {
	case rpcStatusOK:
		return resp.Item, nil
	case rpcStatusNotFound:
		return Connection{}, controllermeta.ErrNotFound
	default:
		return Connection{}, fmt.Errorf("access/node: connection rpc status %s", resp.Status)
	}
}

func connectionUnixNano(t time.Time) uint64 {
	if t.IsZero() {
		return 0
	}
	return uint64(t.UnixNano())
}

func connectionTimeFromUnixNano(v uint64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(0, int64(v)).UTC()
}
