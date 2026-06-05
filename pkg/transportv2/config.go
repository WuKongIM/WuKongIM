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
	// NodeID is the local cluster node ID used for outbound transport identity.
	NodeID NodeID
	// Discovery resolves remote node IDs before dialing; it is required.
	Discovery Discovery
	// PoolSize is the outbound connection count per peer; zero uses DefaultPoolSize.
	PoolSize int
	// DialTimeout bounds outbound dial attempts; zero uses DefaultDialTimeout.
	DialTimeout time.Duration
	// Dialer overrides the default network dial path; nil selects the package default.
	Dialer Dialer
	// Limits bounds frame, queue, batch, and timeout behavior; the all-zero value uses DefaultLimits.
	Limits Limits
	// Observer receives non-blocking transport observations; nil disables observation callbacks.
	Observer Observer
}

// ServerConfig configures inbound transport service state.
type ServerConfig struct {
	// NodeID is the local cluster node ID used for inbound transport identity.
	NodeID NodeID
	// Limits bounds frame, queue, batch, and timeout behavior; the all-zero value uses DefaultLimits.
	Limits Limits
	// Observer receives non-blocking transport observations; nil disables observation callbacks.
	Observer Observer
	// Logger records server diagnostics; nil uses wklog.NewNop().
	Logger wklog.Logger
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
