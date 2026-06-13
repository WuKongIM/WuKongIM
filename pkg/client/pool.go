package client

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type poolClient interface {
	SendBatch(context.Context, []Message) ([]SendResult, error)
	Close() error
}

// Pool owns multiple WKProto sessions for tooling workloads.
type Pool struct {
	cfg PoolConfig
	mu  sync.RWMutex
	// clients stores connected sessions keyed by UID.
	clients map[string]poolClient
}

// NewPool creates an empty client pool.
func NewPool(cfg PoolConfig) (*Pool, error) {
	if len(cfg.Addrs) == 0 && cfg.Client.Addr == "" {
		return nil, ErrMissingAddr
	}
	if cfg.Balance == "" {
		cfg.Balance = defaultPoolBalanceRoundRobin
	}
	if cfg.Balance != defaultPoolBalanceRoundRobin {
		return nil, fmt.Errorf("client pool: unsupported balance %q", cfg.Balance)
	}
	return &Pool{cfg: cfg, clients: make(map[string]poolClient)}, nil
}

// Connect creates and connects one client for each identity.
func (p *Pool) Connect(ctx context.Context, identities []Identity) error {
	if p == nil {
		return ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	addrs := p.cfg.Addrs
	if len(addrs) == 0 {
		addrs = []string{p.cfg.Client.Addr}
	}
	if len(addrs) == 0 || addrs[0] == "" {
		return ErrMissingAddr
	}

	var throttle <-chan time.Time
	var ticker *time.Ticker
	if p.cfg.ConnectRatePerSecond > 0 {
		interval := time.Second / time.Duration(p.cfg.ConnectRatePerSecond)
		if interval <= 0 {
			interval = time.Nanosecond
		}
		ticker = time.NewTicker(interval)
		defer ticker.Stop()
		throttle = ticker.C
	}

	for i, identity := range identities {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i > 0 && throttle != nil {
			select {
			case <-throttle:
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		cfg := p.cfg.Client
		cfg.Addr = addrs[i%len(addrs)]
		if identity.Token != "" {
			cfg.Token = identity.Token
		}
		c, err := New(cfg)
		if err != nil {
			return err
		}
		if _, err = c.Connect(ctx, ConnectOptions{
			UID:        identity.UID,
			DeviceID:   identity.DeviceID,
			DeviceFlag: frame.APP,
			Token:      identity.Token,
		}); err != nil {
			_ = c.Close()
			return err
		}

		p.mu.Lock()
		if p.clients == nil {
			p.clients = make(map[string]poolClient)
		}
		old := p.clients[identity.UID]
		p.clients[identity.UID] = c
		p.mu.Unlock()
		if old != nil {
			_ = old.Close()
		}
	}
	return nil
}

// Client returns the concrete connected client for uid when it is owned by this pool.
func (p *Pool) Client(uid string) (*Client, bool) {
	if p == nil {
		return nil, false
	}
	p.mu.RLock()
	client := p.clients[uid]
	p.mu.RUnlock()
	c, ok := client.(*Client)
	return c, ok
}

// SendBatch sends routed messages through their UID sessions and preserves input order.
func (p *Pool) SendBatch(ctx context.Context, msgs []RoutedMessage) ([]SendResult, error) {
	if p == nil {
		return nil, ErrClosed
	}
	if len(msgs) == 0 {
		return nil, nil
	}

	results := make([]SendResult, len(msgs))
	type grouped struct {
		client  poolClient
		indexes []int
		msgs    []Message
	}
	groups := make(map[string]*grouped)

	p.mu.RLock()
	for i, msg := range msgs {
		client := p.clients[msg.UID]
		if client == nil {
			p.mu.RUnlock()
			return nil, fmt.Errorf("client pool: missing client for uid %q", msg.UID)
		}
		group := groups[msg.UID]
		if group == nil {
			group = &grouped{client: client}
			groups[msg.UID] = group
		}
		group.indexes = append(group.indexes, i)
		group.msgs = append(group.msgs, msg.Message)
	}
	p.mu.RUnlock()

	for _, group := range groups {
		groupResults, err := group.client.SendBatch(ctx, group.msgs)
		if err != nil {
			return results, err
		}
		if len(groupResults) != len(group.indexes) {
			return results, fmt.Errorf("client pool: result count mismatch")
		}
		for i, result := range groupResults {
			results[group.indexes[i]] = result
		}
	}
	return results, nil
}

// Close closes all clients in the pool and returns the first close error.
func (p *Pool) Close() error {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	clients := make([]poolClient, 0, len(p.clients))
	for _, client := range p.clients {
		clients = append(clients, client)
	}
	p.mu.RUnlock()

	var first error
	for _, client := range clients {
		if err := client.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
