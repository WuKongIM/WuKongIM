package transportv2

import (
	"fmt"
	"net"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	DefaultPoolSize    = 4
	DefaultDialTimeout = 5 * time.Second
)

// Dialer creates outbound network connections for the client.
type Dialer func(network, addr string, timeout time.Duration) (net.Conn, error)

// ClientConfig configures outbound peer connections and limits.
type ClientConfig struct {
	NodeID      NodeID
	Discovery   Discovery
	PoolSize    int
	DialTimeout time.Duration
	Dialer      Dialer
	Limits      Limits
	Observer    Observer
}

// ServerConfig configures inbound transport service state.
type ServerConfig struct {
	NodeID   NodeID
	Limits   Limits
	Observer Observer
	Logger   wklog.Logger
}

// DefaultLimits returns conservative transport bounds for a single-node cluster or multi-node cluster.
func DefaultLimits() Limits {
	return Limits{
		MaxFrameBodyBytes:     64 << 20,
		MaxQueuedBytesPerConn: 64 << 20,
		MaxQueuedItemsPerConn: 4096,
		MaxBatchBytes:         1 << 20,
		MaxBatchFrames:        64,
		DialFailureCooldown:   50 * time.Millisecond,
		WriteTimeout:          5 * time.Second,
		ReadIdleTimeout:       0,
	}
}

func normalizeClientConfig(cfg ClientConfig) (ClientConfig, error) {
	if cfg.Discovery == nil {
		return ClientConfig{}, fmt.Errorf("%w: discovery is required", ErrInvalidConfig)
	}
	if cfg.PoolSize < 0 {
		return ClientConfig{}, fmt.Errorf("%w: PoolSize must be non-negative", ErrInvalidConfig)
	}
	if cfg.PoolSize == 0 {
		cfg.PoolSize = DefaultPoolSize
	}
	if cfg.DialTimeout < 0 {
		return ClientConfig{}, fmt.Errorf("%w: DialTimeout must be non-negative", ErrInvalidConfig)
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = DefaultDialTimeout
	}
	if cfg.Limits == (core.Limits{}) {
		cfg.Limits = DefaultLimits()
	}
	if err := cfg.Limits.Validate(); err != nil {
		return ClientConfig{}, err
	}
	return cfg, nil
}

func normalizeServerConfig(cfg ServerConfig) (ServerConfig, error) {
	if cfg.Limits == (core.Limits{}) {
		cfg.Limits = DefaultLimits()
	}
	if err := cfg.Limits.Validate(); err != nil {
		return ServerConfig{}, err
	}
	if cfg.Logger == nil {
		cfg.Logger = wklog.NewNop()
	}
	return cfg, nil
}
