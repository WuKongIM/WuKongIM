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
	// Presence configures connection-route activation and authority touch behavior.
	Presence PresenceConfig
	// Delivery configures online message delivery fanout and owner-local ack tracking.
	Delivery DeliveryConfig
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

// PresenceConfig contains connection presence and route-authority touch settings.
type PresenceConfig struct {
	// ActivationTimeout bounds one gateway session activation against the UID authority.
	ActivationTimeout time.Duration
	// TouchFlushInterval controls how often owner-local activity is flushed to UID authorities.
	TouchFlushInterval time.Duration
	// TouchBatchSize limits owner-local touched routes drained in one flush.
	TouchBatchSize int
	// RouteTTL bounds authority-side route liveness since the latest observed activity.
	RouteTTL time.Duration
}

// DeliveryConfig contains online delivery fanout and recvack tracking settings.
type DeliveryConfig struct {
	// Enabled wires committed messages into the delivery runtime when true.
	Enabled bool
	// FanoutPageSize limits subscriber UIDs read by one fanout page.
	FanoutPageSize int
	// PushBatchSize limits owner-node route pushes produced by one delivery batch.
	PushBatchSize int
	// PendingAckTTL bounds stale pending recvack cleanup during delivery activity.
	PendingAckTTL time.Duration
}

func defaultPresenceConfig(cfg PresenceConfig) PresenceConfig {
	if cfg.ActivationTimeout == 0 {
		cfg.ActivationTimeout = 3 * time.Second
	}
	if cfg.TouchFlushInterval == 0 {
		cfg.TouchFlushInterval = time.Second
	}
	if cfg.TouchBatchSize == 0 {
		cfg.TouchBatchSize = 512
	}
	if cfg.RouteTTL == 0 {
		cfg.RouteTTL = 90 * time.Second
	}
	return cfg
}

func defaultDeliveryConfig(cfg DeliveryConfig) DeliveryConfig {
	if cfg.FanoutPageSize == 0 {
		cfg.FanoutPageSize = 512
	}
	if cfg.PushBatchSize == 0 {
		cfg.PushBatchSize = 512
	}
	if cfg.PendingAckTTL == 0 {
		cfg.PendingAckTTL = 30 * time.Second
	}
	return cfg
}

func validatePresenceConfig(cfg PresenceConfig) error {
	if cfg.ActivationTimeout < 0 {
		return fmt.Errorf("%w: presence activation timeout must be non-negative", ErrInvalidConfig)
	}
	if cfg.TouchFlushInterval < 0 {
		return fmt.Errorf("%w: presence touch flush interval must be non-negative", ErrInvalidConfig)
	}
	if cfg.TouchBatchSize < 0 {
		return fmt.Errorf("%w: presence touch batch size must be non-negative", ErrInvalidConfig)
	}
	if cfg.RouteTTL < 0 {
		return fmt.Errorf("%w: presence route ttl must be non-negative", ErrInvalidConfig)
	}
	return nil
}

func validateDeliveryConfig(cfg DeliveryConfig) error {
	if cfg.FanoutPageSize < 0 {
		return fmt.Errorf("%w: delivery fanout page size must be non-negative", ErrInvalidConfig)
	}
	if cfg.PushBatchSize < 0 {
		return fmt.Errorf("%w: delivery push batch size must be non-negative", ErrInvalidConfig)
	}
	if cfg.PendingAckTTL < 0 {
		return fmt.Errorf("%w: delivery pending ack ttl must be non-negative", ErrInvalidConfig)
	}
	return nil
}
