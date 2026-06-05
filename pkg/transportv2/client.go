package transportv2

import (
	"context"
	"net"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/conn"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/peer"
)

// Client owns outbound peer connection state.
type Client struct {
	cfg      ClientConfig
	peers    *peer.Manager
	observer *core.ObserverDrain
}

// NewClient validates config and builds a minimal client shell.
func NewClient(cfg ClientConfig) (*Client, error) {
	normalized, err := normalizeClientConfig(cfg)
	if err != nil {
		return nil, err
	}
	observer := core.NewObserverDrain(normalized.Observer)
	normalized.Observer = observer
	return &Client{
		cfg:      normalized,
		observer: observer,
		peers: peer.NewManager(peer.Config{
			Discovery: normalized.Discovery,
			PoolSize:  normalized.PoolSize,
			Limits:    normalized.Limits,
			Observer:  normalized.Observer,
			Dial:      clientDialFunc(normalized),
		}),
	}, nil
}

// Send copies payload and sends it as a data frame.
func (c *Client) Send(ctx context.Context, nodeID NodeID, shardKey uint64, pri Priority, serviceID uint16, payload []byte) error {
	return c.SendOwned(ctx, nodeID, shardKey, pri, serviceID, CopyOwnedBuffer(payload))
}

// SendOwned sends payload as a data frame and transfers ownership on success.
func (c *Client) SendOwned(ctx context.Context, nodeID NodeID, shardKey uint64, pri Priority, serviceID uint16, payload OwnedBuffer) error {
	peerConn, err := c.peers.Acquire(ctx, nodeID, shardKey)
	if err != nil {
		payload.Release()
		return err
	}
	return peerConn.Send(ctx, conn.Outbound{
		Kind:      FrameKindData,
		Priority:  pri,
		ServiceID: serviceID,
		Payload:   payload,
	})
}

// Notify copies payload and sends it as a notify frame.
func (c *Client) Notify(ctx context.Context, nodeID NodeID, shardKey uint64, pri Priority, serviceID uint16, payload []byte) error {
	return c.NotifyOwned(ctx, nodeID, shardKey, pri, serviceID, CopyOwnedBuffer(payload))
}

// NotifyOwned sends payload as a notify frame and transfers ownership on success.
func (c *Client) NotifyOwned(ctx context.Context, nodeID NodeID, shardKey uint64, pri Priority, serviceID uint16, payload OwnedBuffer) error {
	peerConn, err := c.peers.Acquire(ctx, nodeID, shardKey)
	if err != nil {
		payload.Release()
		return err
	}
	return peerConn.Send(ctx, conn.Outbound{
		Kind:      FrameKindNotify,
		Priority:  pri,
		ServiceID: serviceID,
		Payload:   payload,
	})
}

// Call copies payload, sends it as an RPC request, and waits for the response.
func (c *Client) Call(ctx context.Context, nodeID NodeID, shardKey uint64, pri Priority, serviceID uint16, payload []byte) ([]byte, error) {
	peerConn, err := c.peers.Acquire(ctx, nodeID, shardKey)
	if err != nil {
		return nil, err
	}
	return peerConn.Call(ctx, conn.Outbound{
		Priority:  pri,
		ServiceID: serviceID,
		Payload:   CopyOwnedBuffer(payload),
	})
}

// ClosePeer closes all outbound connection slots for nodeID.
func (c *Client) ClosePeer(nodeID NodeID) {
	c.peers.ClosePeer(nodeID)
}

// Stop releases client resources.
func (c *Client) Stop() {
	c.peers.Stop()
	c.observer.Stop()
}

// Stats returns a point-in-time client stats snapshot.
func (c *Client) Stats() Stats {
	return c.peers.Stats()
}

func clientDialFunc(cfg ClientConfig) func(context.Context, string, string) (net.Conn, error) {
	if cfg.Dialer != nil {
		return func(_ context.Context, network, addr string) (net.Conn, error) {
			return cfg.Dialer(network, addr, cfg.DialTimeout)
		}
	}
	var dialer net.Dialer
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialCtx, cancel := context.WithTimeout(ctx, cfg.DialTimeout)
		defer cancel()
		return dialer.DialContext(dialCtx, network, addr)
	}
}
