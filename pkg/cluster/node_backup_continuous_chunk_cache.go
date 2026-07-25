package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"sync"
)

const (
	backupContinuousChunkKindMessage  = "message"
	backupContinuousChunkKindMetadata = "metadata"
)

// backupContinuousChunkCache retains at most one encoded oversized RPC page.
// Eviction only causes a deterministic source re-read; correctness never
// depends on this node-local optimization.
type backupContinuousChunkCache struct {
	mu            sync.Mutex
	key           [sha256.Size]byte
	body          []byte
	loadErr       error
	loading       bool
	leases        int
	evictWhenIdle bool
	changed       chan struct{}
}

func backupContinuousChunkKey(kind string, request any) ([sha256.Size]byte, error) {
	body, err := json.Marshal(struct {
		Kind    string `json:"kind"`
		Request any    `json:"request"`
	}{Kind: kind, Request: request})
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(body), nil
}

// chunk copies one bounded response chunk while a cross-key lease prevents a
// second full encoded page from being materialized. Existing same-key waiters
// join the lease and cannot be displaced by a later different key.
func (c *backupContinuousChunkCache) chunk(
	ctx context.Context,
	key [sha256.Size]byte,
	offset int,
	chunkBytes int,
	load func(context.Context) ([]byte, error),
) (int, []byte, bool, bool, error) {
	registered := false
	for {
		c.mu.Lock()
		if c.key == key {
			if !registered {
				c.leases++
				registered = true
			}
			if c.loading {
				changed := c.changedLocked()
				c.mu.Unlock()
				select {
				case <-ctx.Done():
					c.mu.Lock()
					c.releaseLocked()
					c.mu.Unlock()
					return 0, nil, false, false, ctx.Err()
				case <-changed:
					continue
				}
			}
			if c.loadErr != nil {
				err := c.loadErr
				c.releaseLocked()
				c.mu.Unlock()
				return 0, nil, false, false, err
			}
			if offset < 0 || chunkBytes <= 0 || offset >= len(c.body) {
				c.releaseLocked()
				c.mu.Unlock()
				return 0, nil, false, false, nil
			}
			end := min(len(c.body), offset+chunkBytes)
			data := append([]byte(nil), c.body[offset:end]...)
			total := len(c.body)
			done := end == total
			if done {
				c.evictWhenIdle = true
			}
			c.releaseLocked()
			c.mu.Unlock()
			return total, data, done, true, nil
		}
		if c.loading || c.leases > 0 {
			changed := c.changedLocked()
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return 0, nil, false, false, ctx.Err()
			case <-changed:
				continue
			}
		}
		c.key = key
		c.body = nil
		c.loadErr = nil
		c.loading = true
		c.leases = 1
		c.evictWhenIdle = false
		registered = true
		c.mu.Unlock()

		body, err := load(ctx)
		c.mu.Lock()
		c.body = body
		c.loadErr = err
		c.loading = false
		c.signalLocked()
		c.mu.Unlock()
	}
}

func (c *backupContinuousChunkCache) releaseLocked() {
	c.leases--
	if c.leases == 0 {
		if c.evictWhenIdle || c.loadErr != nil {
			c.key = [sha256.Size]byte{}
			c.body = nil
			c.loadErr = nil
			c.evictWhenIdle = false
		}
		c.signalLocked()
	}
}

func (c *backupContinuousChunkCache) changedLocked() <-chan struct{} {
	if c.changed == nil {
		c.changed = make(chan struct{})
	}
	return c.changed
}

func (c *backupContinuousChunkCache) signalLocked() {
	if c.changed != nil {
		close(c.changed)
		c.changed = nil
	}
}

func (c *backupContinuousChunkCache) clear() {
	c.mu.Lock()
	if c.loading || c.leases > 0 {
		c.evictWhenIdle = true
		c.body = nil
	} else {
		c.key = [sha256.Size]byte{}
		c.body = nil
		c.loadErr = nil
	}
	c.signalLocked()
	c.mu.Unlock()
}
