package node

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	cmdSyncOpSync    = "sync"
	cmdSyncOpSyncAck = "sync_ack"
)

type cmdSyncRPCRequest struct {
	Op    string                 `json:"op"`
	Query cmdsync.SyncQuery      `json:"query,omitempty"`
	Ack   cmdsync.SyncAckCommand `json:"ack,omitempty"`
}

type cmdSyncRPCResponse struct {
	Status   string            `json:"status"`
	Error    string            `json:"error,omitempty"`
	Messages []channel.Message `json:"messages,omitempty"`
}

func (a *Adapter) handleCMDSyncRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeCMDSyncRequest(body)
	if err != nil {
		return nil, err
	}
	if a == nil || a.cmdSync == nil {
		return encodeCMDSyncResponse(cmdSyncRPCResponse{Status: rpcStatusRejected, Error: "access/node: cmd sync usecase not configured"})
	}

	switch req.Op {
	case cmdSyncOpSync:
		result, err := a.cmdSync.Sync(ctx, req.Query)
		if err != nil {
			return encodeCMDSyncResponse(cmdSyncRPCResponse{Status: rpcStatusRejected, Error: err.Error()})
		}
		return encodeCMDSyncResponse(cmdSyncRPCResponse{Status: rpcStatusOK, Messages: append([]channel.Message(nil), result.Messages...)})
	case cmdSyncOpSyncAck:
		if err := a.cmdSync.SyncAck(ctx, req.Ack); err != nil {
			return encodeCMDSyncResponse(cmdSyncRPCResponse{Status: rpcStatusRejected, Error: err.Error()})
		}
		return encodeCMDSyncResponse(cmdSyncRPCResponse{Status: rpcStatusOK})
	default:
		return nil, fmt.Errorf("access/node: unknown cmd sync op %q", req.Op)
	}
}

func (c *Client) SyncCMD(ctx context.Context, nodeID uint64, query cmdsync.SyncQuery) (cmdsync.SyncResult, error) {
	resp, err := c.callCMDSync(ctx, nodeID, cmdSyncRPCRequest{Op: cmdSyncOpSync, Query: query})
	if err != nil {
		return cmdsync.SyncResult{}, err
	}
	return cmdsync.SyncResult{Messages: append([]channel.Message(nil), resp.Messages...)}, nil
}

func (c *Client) SyncAckCMD(ctx context.Context, nodeID uint64, cmd cmdsync.SyncAckCommand) error {
	_, err := c.callCMDSync(ctx, nodeID, cmdSyncRPCRequest{Op: cmdSyncOpSyncAck, Ack: cmd})
	return err
}

func (c *Client) callCMDSync(ctx context.Context, nodeID uint64, req cmdSyncRPCRequest) (cmdSyncRPCResponse, error) {
	if c == nil || c.cluster == nil {
		return cmdSyncRPCResponse{}, fmt.Errorf("access/node: cluster not configured")
	}
	if nodeID == 0 {
		return cmdSyncRPCResponse{}, fmt.Errorf("access/node: node id required")
	}
	body, err := encodeCMDSyncRequestBinary(req)
	if err != nil {
		return cmdSyncRPCResponse{}, err
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, cmdSyncRPCServiceID, body)
	if err != nil {
		return cmdSyncRPCResponse{}, err
	}
	resp, err := decodeCMDSyncResponse(respBody)
	if err != nil {
		return cmdSyncRPCResponse{}, err
	}
	if resp.Status != rpcStatusOK {
		if resp.Error != "" {
			return cmdSyncRPCResponse{}, fmt.Errorf("access/node: cmd sync rpc %s: %s", resp.Status, resp.Error)
		}
		return cmdSyncRPCResponse{}, fmt.Errorf("access/node: cmd sync rpc status %s", resp.Status)
	}
	return resp, nil
}
