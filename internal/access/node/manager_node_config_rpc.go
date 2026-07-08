package node

import (
	"context"
	"errors"
	"fmt"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// ManagerNodeConfigRPCServiceID is the cluster RPC service for selected-node effective config reads.
const ManagerNodeConfigRPCServiceID uint8 = clusternet.RPCManagerNodeConfig

// HandleManagerNodeConfigRPC handles one encoded manager node config RPC payload.
func (a *Adapter) HandleManagerNodeConfigRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeManagerNodeConfigRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("manager node config rpc decode failed",
			wklog.Event("internal.access.node.manager_node_config_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.managerNodeConfig == nil {
		return encodeManagerNodeConfigResponse(managerNodeConfigRPCResponse{Status: rpcStatusUnavailable})
	}
	snapshot, err := a.managerNodeConfig.NodeConfigSnapshot(ctx, req.NodeID)
	status := managerNodeConfigRPCStatusForError(err)
	a.logManagerNodeConfigError(req, status, err)
	return encodeManagerNodeConfigResponse(managerNodeConfigRPCResponse{Status: status, Snapshot: snapshot})
}

// GetManagerNodeConfig reads one node's effective startup config through RPC.
func (c *Client) GetManagerNodeConfig(ctx context.Context, nodeID uint64) (managementusecase.NodeConfigSnapshot, error) {
	resp, err := c.callManagerNodeConfig(ctx, nodeID, managerNodeConfigRPCRequest{NodeID: nodeID})
	if err != nil {
		return managementusecase.NodeConfigSnapshot{}, err
	}
	if err := managerNodeConfigRPCErrorForStatus(resp.Status); err != nil {
		return managementusecase.NodeConfigSnapshot{}, err
	}
	return resp.Snapshot, nil
}

func (c *Client) callManagerNodeConfig(ctx context.Context, nodeID uint64, req managerNodeConfigRPCRequest) (managerNodeConfigRPCResponse, error) {
	if c == nil || c.node == nil {
		return managerNodeConfigRPCResponse{}, fmt.Errorf("internal/access/node: manager node config rpc client not configured")
	}
	body, err := encodeManagerNodeConfigRequest(req)
	if err != nil {
		return managerNodeConfigRPCResponse{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, ManagerNodeConfigRPCServiceID, body)
	if err != nil {
		return managerNodeConfigRPCResponse{}, err
	}
	return decodeManagerNodeConfigResponse(respBody)
}

func managerNodeConfigRPCStatusForError(err error) string {
	switch {
	case err == nil:
		return rpcStatusOK
	case errors.Is(err, context.Canceled):
		return rpcStatusContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return rpcStatusContextDeadlineExceeded
	case errors.Is(err, metadb.ErrInvalidArgument):
		return rpcStatusInvalidArgument
	case errors.Is(err, managementusecase.ErrNodeConfigUnavailable):
		return rpcStatusUnavailable
	default:
		return rpcStatusRejected
	}
}

func managerNodeConfigRPCErrorForStatus(status string) error {
	switch status {
	case rpcStatusOK:
		return nil
	case rpcStatusContextCanceled:
		return context.Canceled
	case rpcStatusContextDeadlineExceeded:
		return context.DeadlineExceeded
	case rpcStatusInvalidArgument:
		return metadb.ErrInvalidArgument
	case rpcStatusUnavailable, rpcStatusRejected:
		return managementusecase.ErrNodeConfigUnavailable
	default:
		return fmt.Errorf("internal/access/node: unknown manager node config rpc status %q", status)
	}
}

func (a *Adapter) logManagerNodeConfigError(req managerNodeConfigRPCRequest, status string, err error) {
	if err == nil || status != rpcStatusRejected {
		return
	}
	a.rpcLogger().Warn("manager node config rpc rejected",
		wklog.Event("internal.access.node.manager_node_config_rejected"),
		wklog.String("status", status),
		wklog.Uint64("nodeID", req.NodeID),
		wklog.Error(err),
	)
}
