package transport

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// Client is a thin wrapper around Pool.
type Client struct {
	pool       *Pool
	inflightMu sync.Mutex
	inflight   map[rpcInflightKey]int
}

type rpcInflightKey struct {
	nodeID    NodeID
	serviceID uint8
}

func NewClient(pool *Pool) *Client {
	return &Client{
		pool:     pool,
		inflight: make(map[rpcInflightKey]int),
	}
}

func (c *Client) Send(nodeID NodeID, shardKey uint64, msgType uint8, body []byte) error {
	return c.pool.Send(nodeID, shardKey, msgType, body)
}

func (c *Client) RPC(ctx context.Context, nodeID NodeID, shardKey uint64, payload []byte) ([]byte, error) {
	return c.pool.RPC(ctx, nodeID, shardKey, payload)
}

func (c *Client) RPCService(ctx context.Context, nodeID NodeID, shardKey uint64, serviceID uint8, payload []byte) ([]byte, error) {
	startedAt := time.Now()
	inflight := c.adjustInflight(nodeID, serviceID, 1)
	c.observeRPCClient(RPCClientEvent{
		TargetNode: nodeID,
		ServiceID:  serviceID,
		Inflight:   inflight,
	})

	resp, err := c.RPC(ctx, nodeID, shardKey, encodeRPCServicePayload(serviceID, payload))

	inflight = c.adjustInflight(nodeID, serviceID, -1)
	c.observeRPCClient(RPCClientEvent{
		TargetNode: nodeID,
		ServiceID:  serviceID,
		Result:     rpcClientResult(err),
		Duration:   time.Since(startedAt),
		Inflight:   inflight,
	})
	return resp, err
}

func (c *Client) Stop() {
	if c.pool != nil {
		c.pool.Close()
	}
}

func (c *Client) observeRPCClient(event RPCClientEvent) {
	if c == nil || c.pool == nil || c.pool.cfg.Observer.OnRPCClient == nil {
		return
	}
	c.pool.cfg.Observer.OnRPCClient(event)
}

func (c *Client) adjustInflight(nodeID NodeID, serviceID uint8, delta int) int {
	if c == nil {
		return 0
	}
	key := rpcInflightKey{nodeID: nodeID, serviceID: serviceID}
	c.inflightMu.Lock()
	defer c.inflightMu.Unlock()
	if c.inflight == nil {
		c.inflight = make(map[rpcInflightKey]int)
	}
	next := c.inflight[key] + delta
	if next <= 0 {
		delete(c.inflight, key)
		return 0
	}
	c.inflight[key] = next
	return next
}

func rpcClientResult(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, ErrQueueFull):
		return "queue_full"
	case errors.Is(err, ErrStopped):
		return "stopped"
	case strings.Contains(err.Error(), "remote error"):
		return "remote_error"
	default:
		return "other"
	}
}
