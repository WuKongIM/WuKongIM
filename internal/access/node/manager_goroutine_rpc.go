package node

import (
	"context"
	"encoding/json"
	"fmt"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

// ManagerGoroutineRPCServiceID is the cluster RPC service for node-local managed goroutine snapshots.
const ManagerGoroutineRPCServiceID uint8 = clusternet.RPCManagerGoroutines

type managerGoroutineRPCResponse struct {
	Status   string                     `json:"status"`
	Snapshot goruntimeregistry.Snapshot `json:"snapshot"`
}

// HandleManagerGoroutineRPC returns one bounded node-local snapshot.
func (a *Adapter) HandleManagerGoroutineRPC(_ context.Context, _ []byte) ([]byte, error) {
	response := managerGoroutineRPCResponse{Status: rpcStatusUnavailable}
	if a != nil && a.managerGoroutines != nil {
		response.Status = rpcStatusOK
		response.Snapshot = a.managerGoroutines.Snapshot()
	}
	return json.Marshal(response)
}

// ManagerGoroutineSnapshot reads one node-local managed goroutine snapshot.
func (c *Client) ManagerGoroutineSnapshot(ctx context.Context, nodeID uint64) (goruntimeregistry.Snapshot, error) {
	if c == nil || c.node == nil {
		return goruntimeregistry.Snapshot{}, fmt.Errorf("internal/access/node: manager goroutine rpc client not configured")
	}
	body, err := c.node.CallRPC(ctx, nodeID, ManagerGoroutineRPCServiceID, nil)
	if err != nil {
		return goruntimeregistry.Snapshot{}, err
	}
	var response managerGoroutineRPCResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return goruntimeregistry.Snapshot{}, fmt.Errorf("internal/access/node: decode manager goroutine response: %w", err)
	}
	if response.Status != rpcStatusOK {
		return goruntimeregistry.Snapshot{}, fmt.Errorf("internal/access/node: manager goroutine rpc status %q", response.Status)
	}
	return response.Snapshot, nil
}
