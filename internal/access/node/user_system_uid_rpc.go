package node

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
)

const (
	systemUIDCacheOpAdd    = "add"
	systemUIDCacheOpRemove = "remove"
)

type systemUIDCacheRequest struct {
	Op   string
	UIDs []string
}

type systemUIDCacheResponse struct {
	Status string
}

func (a *Adapter) handleSystemUIDCacheRPC(_ context.Context, body []byte) ([]byte, error) {
	req, err := decodeSystemUIDCacheRequest(body)
	if err != nil {
		return nil, err
	}
	if a == nil || a.systemUIDCache == nil {
		return nil, fmt.Errorf("access/node: system uid cache not configured")
	}

	switch req.Op {
	case systemUIDCacheOpAdd:
		err = a.systemUIDCache.AddSystemUIDsToCache(req.UIDs)
	case systemUIDCacheOpRemove:
		err = a.systemUIDCache.RemoveSystemUIDsFromCache(req.UIDs)
	default:
		err = fmt.Errorf("access/node: unknown system uid cache op %q", req.Op)
	}
	if err != nil {
		return nil, err
	}
	return encodeSystemUIDCacheResponse(systemUIDCacheResponse{Status: rpcStatusOK})
}

func (c *Client) AddSystemUIDsToCache(ctx context.Context, nodeID uint64, uids []string) error {
	return c.callSystemUIDCache(ctx, nodeID, systemUIDCacheOpAdd, uids)
}

func (c *Client) RemoveSystemUIDsFromCache(ctx context.Context, nodeID uint64, uids []string) error {
	return c.callSystemUIDCache(ctx, nodeID, systemUIDCacheOpRemove, uids)
}

func (c *Client) callSystemUIDCache(ctx context.Context, nodeID uint64, op string, uids []string) error {
	if len(uids) == 0 {
		return nil
	}
	if c == nil || c.cluster == nil {
		return fmt.Errorf("access/node: cluster not configured")
	}
	if nodeID == 0 {
		return fmt.Errorf("access/node: node id required")
	}
	body, err := encodeSystemUIDCacheRequest(systemUIDCacheRequest{Op: op, UIDs: uids})
	if err != nil {
		return err
	}
	respBody, err := c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, systemUIDCacheRPCServiceID, body)
	if err != nil {
		return err
	}
	resp, err := decodeSystemUIDCacheResponse(respBody)
	if err != nil {
		return err
	}
	if resp.Status != rpcStatusOK {
		return fmt.Errorf("access/node: system uid cache rpc status %s", resp.Status)
	}
	return nil
}
