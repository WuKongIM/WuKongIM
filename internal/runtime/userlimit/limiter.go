// Package userlimit provides node-local user send rate limiting primitives.
package userlimit

import (
	"hash/fnv"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultBucketShards = 256
	defaultRuleName     = "default"
)

const (
	// ReasonRateExceeded reports that a bucket has no token available right now.
	ReasonRateExceeded = "rate_exceeded"
	// ReasonBucketLimit reports that the limiter refused to allocate another bucket.
	ReasonBucketLimit = "bucket_limit"
)

// Config controls the node-local user send limiter.
type Config struct {
	// Enabled decides whether AllowSend enforces token buckets.
	Enabled bool
	// RatePerSecond is the sustained number of sends admitted per UID per second.
	RatePerSecond float64
	// Burst is the maximum number of sends admitted immediately for one UID.
	Burst int
	// BucketShards controls lock striping for UID buckets. Zero uses a safe default.
	BucketShards int
	// IdleTTL is the duration after which an unused bucket can be evicted.
	IdleTTL time.Duration
	// MaxBuckets caps allocated UID buckets. Zero disables the cap.
	MaxBuckets int
	// SystemUIDBypass admits trusted system UID sends without allocating buckets.
	SystemUIDBypass bool
	// PluginBypass admits plugin-origin sends without allocating buckets.
	PluginBypass bool
	// RuleName identifies the rule in decisions and logs.
	RuleName string
}

// Request identifies one user send admission check.
type Request struct {
	UID         string
	ChannelID   string
	ChannelType uint8
	Origin      string
	IsSystemUID bool
}

// Decision reports whether a send may continue and why it was rejected.
type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
	RuleName   string
	Reason     string
}

// Limiter enforces node-local UID token buckets.
type Limiter struct {
	cfg     Config
	shards  []bucketShard
	buckets atomic.Int64
}

type bucketShard struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens     float64
	lastRefill time.Time
	lastSeen   time.Time
}

// New builds a limiter. Invalid enabled configs are normalized to disabled.
func New(cfg Config) *Limiter {
	if cfg.BucketShards <= 0 {
		cfg.BucketShards = defaultBucketShards
	}
	if cfg.RuleName == "" {
		cfg.RuleName = defaultRuleName
	}
	if cfg.RatePerSecond <= 0 || cfg.Burst <= 0 {
		cfg.Enabled = false
	}
	l := &Limiter{cfg: cfg, shards: make([]bucketShard, cfg.BucketShards)}
	for i := range l.shards {
		l.shards[i].buckets = make(map[string]*bucket)
	}
	return l
}

// AllowSend consumes one token for req.UID when available.
func (l *Limiter) AllowSend(now time.Time, req Request) Decision {
	if l == nil || !l.cfg.Enabled {
		return Decision{Allowed: true, RuleName: defaultRuleName}
	}
	if req.IsSystemUID && l.cfg.SystemUIDBypass {
		return Decision{Allowed: true, RuleName: l.cfg.RuleName}
	}
	if req.Origin == "plugin" && l.cfg.PluginBypass {
		return Decision{Allowed: true, RuleName: l.cfg.RuleName}
	}
	if req.UID == "" {
		return Decision{Allowed: true, RuleName: l.cfg.RuleName}
	}

	shard := &l.shards[l.shardIndex(req.UID)]
	shard.mu.Lock()
	defer shard.mu.Unlock()

	b := shard.buckets[req.UID]
	if b == nil {
		if l.cfg.MaxBuckets > 0 && l.buckets.Load() >= int64(l.cfg.MaxBuckets) {
			return Decision{Allowed: false, RuleName: l.cfg.RuleName, Reason: ReasonBucketLimit, RetryAfter: l.retryAfter(1)}
		}
		b = &bucket{tokens: float64(l.cfg.Burst), lastRefill: now, lastSeen: now}
		shard.buckets[req.UID] = b
		l.buckets.Add(1)
	}

	l.refill(now, b)
	b.lastSeen = now
	if b.tokens >= 1 {
		b.tokens--
		return Decision{Allowed: true, RuleName: l.cfg.RuleName}
	}
	return Decision{Allowed: false, RuleName: l.cfg.RuleName, Reason: ReasonRateExceeded, RetryAfter: l.retryAfter(1 - b.tokens)}
}

// ActiveBuckets returns the current number of allocated UID buckets.
func (l *Limiter) ActiveBuckets() int {
	if l == nil {
		return 0
	}
	return int(l.buckets.Load())
}

// StartJanitor periodically evicts idle buckets until the returned stop function is called.
func (l *Limiter) StartJanitor(interval time.Duration) func() {
	stop := make(chan struct{})
	if l == nil || interval <= 0 || l.cfg.IdleTTL <= 0 {
		return func() { close(stop) }
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case now := <-ticker.C:
				l.EvictIdle(now)
			case <-stop:
				return
			}
		}
	}()
	return func() { close(stop) }
}

// EvictIdle removes buckets that have not been used within IdleTTL.
func (l *Limiter) EvictIdle(now time.Time) int {
	if l == nil || l.cfg.IdleTTL <= 0 {
		return 0
	}
	evicted := 0
	for i := range l.shards {
		shard := &l.shards[i]
		shard.mu.Lock()
		for uid, b := range shard.buckets {
			if now.Sub(b.lastSeen) > l.cfg.IdleTTL {
				delete(shard.buckets, uid)
				evicted++
			}
		}
		shard.mu.Unlock()
	}
	if evicted > 0 {
		l.buckets.Add(int64(-evicted))
	}
	return evicted
}

func (l *Limiter) refill(now time.Time, b *bucket) {
	if !now.After(b.lastRefill) {
		return
	}
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens = math.Min(float64(l.cfg.Burst), b.tokens+elapsed*l.cfg.RatePerSecond)
	b.lastRefill = now
}

func (l *Limiter) retryAfter(deficit float64) time.Duration {
	if deficit <= 0 || l.cfg.RatePerSecond <= 0 {
		return 0
	}
	nanos := math.Ceil(deficit / l.cfg.RatePerSecond * float64(time.Second))
	return time.Duration(nanos)
}

func (l *Limiter) shardIndex(uid string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(uid))
	return h.Sum64() % uint64(len(l.shards))
}
