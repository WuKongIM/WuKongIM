package app

import (
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
)

var (
	// ErrInvalidConfig reports an app configuration that cannot start a runtime.
	ErrInvalidConfig = errors.New("internalv2/app: invalid config")
	// ErrAlreadyStarted reports a repeated Start call on a running app.
	ErrAlreadyStarted = errors.New("internalv2/app: already started")
	// ErrStopped reports a Start call after the app has been stopped.
	ErrStopped = errors.New("internalv2/app: stopped")
)

// Config contains phase-1 internalv2 app configuration.
type Config struct {
	// NodeID is the stable cluster node identity.
	NodeID uint64
	// DataDir is the root data directory for the node runtime.
	DataDir string
	// Cluster configures the clusterv2 runtime.
	Cluster clusterv2.Config
	// API configures the benchmark HTTP API exposed by the standalone v2 entry.
	API APIConfig
	// Gateway configures the client gateway runtime.
	Gateway GatewayConfig
	// Bench configures the benchmark-only HTTP API surface.
	Bench BenchConfig
	// Observability configures metrics and diagnostics surfaces.
	Observability ObservabilityConfig
	// Message configures message send behavior.
	Message MessageConfig
	// Presence configures connection-route activation and authority rehydrate behavior.
	Presence PresenceConfig
}

// APIConfig contains HTTP API settings for the standalone v2 entry.
type APIConfig struct {
	// ListenAddr is the HTTP API listen address. An empty value disables the API service.
	ListenAddr string
	// ExternalTCPAddr is the published WKProto TCP gateway address returned by bench capacity discovery.
	ExternalTCPAddr string
	// ExternalWSAddr is the published WebSocket gateway address returned by bench capacity discovery.
	ExternalWSAddr string
	// ExternalWSSAddr is the published secure WebSocket gateway address returned by bench capacity discovery.
	ExternalWSSAddr string
}

// GatewayConfig contains client gateway settings.
type GatewayConfig struct {
	// Listeners configures client-facing gateway listeners.
	Listeners []gateway.ListenerOptions
	// Session configures gateway session limits and batching.
	Session gateway.SessionOptions
	// Transport configures gateway transport runtime tuning.
	Transport gateway.TransportOptions
	// SendTimeout bounds each gateway-origin message send.
	SendTimeout time.Duration
}

// BenchConfig contains benchmark-only API settings.
type BenchConfig struct {
	// APIEnabled exposes unauthenticated /bench/v1/* routes for controlled benchmark environments.
	APIEnabled bool
	// APIMaxBatchSize limits top-level records accepted by one bench API mutation request.
	APIMaxBatchSize int
	// APIMaxPayloadBytes limits JSON request body bytes accepted by bench API mutations.
	APIMaxPayloadBytes int64
}

// ObservabilityConfig contains optional observability runtime settings.
type ObservabilityConfig struct {
	// MetricsEnabled exposes Prometheus metrics and wires runtime observers.
	MetricsEnabled bool
	// PProfEnabled exposes net/http/pprof endpoints on the API listener.
	PProfEnabled bool
}

// MessageConfig contains message usecase settings.
type MessageConfig struct{}

// PresenceConfig contains connection presence and route-authority worker settings.
type PresenceConfig struct {
	// ActivationTimeout bounds one gateway session activation against the UID authority.
	ActivationTimeout time.Duration
	// RehydrateBatchSize limits owner-local active routes replayed in one authority rehydrate call.
	RehydrateBatchSize int
	// RehydrateMaxInflightPerTarget limits concurrent rehydrate workers per authority target; only 1 is currently supported.
	RehydrateMaxInflightPerTarget int
}

func defaultPresenceConfig(cfg PresenceConfig) PresenceConfig {
	if cfg.ActivationTimeout <= 0 {
		cfg.ActivationTimeout = 3 * time.Second
	}
	if cfg.RehydrateBatchSize <= 0 {
		cfg.RehydrateBatchSize = 512
	}
	if cfg.RehydrateMaxInflightPerTarget <= 0 {
		cfg.RehydrateMaxInflightPerTarget = 1
	}
	return cfg
}

func validatePresenceConfig(cfg PresenceConfig) error {
	if cfg.RehydrateMaxInflightPerTarget != 1 {
		return fmt.Errorf("%w: presence rehydrate max inflight per target currently supports 1", ErrInvalidConfig)
	}
	return nil
}
