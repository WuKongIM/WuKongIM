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
	// Log configures application logging output.
	Log LogConfig
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

// LogConfig defines zap and lumberjack logging settings.
type LogConfig struct {
	// Level is the minimum log level accepted by the logger: debug, info, warn, or error.
	Level string
	// Dir is the directory where rolling log files are created.
	Dir string
	// MaxSize is the maximum size in megabytes before one log file is rotated.
	MaxSize int
	// MaxAge is the maximum number of days to retain rotated log files.
	MaxAge int
	// MaxBackups is the maximum number of rotated files retained for each log.
	MaxBackups int
	// Compress enables gzip compression for rotated log files.
	Compress bool
	// Console enables an additional stdout sink for interactive runs.
	Console bool
	// Format selects the file encoder format; json writes structured JSON and other values use console encoding.
	Format string

	compressSet bool
	consoleSet  bool
}

// SetExplicitFlags records whether log booleans were explicitly configured.
func (c *LogConfig) SetExplicitFlags(compressSet, consoleSet bool) {
	if c == nil {
		return
	}
	c.compressSet = compressSet
	c.consoleSet = consoleSet
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
	// PendingAckMaxPerSession limits owner-local pending recvacks for one UID/session.
	PendingAckMaxPerSession int
	// EventQueueSize bounds committed-message events waiting for asynchronous delivery fanout.
	EventQueueSize int
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
	if cfg.PendingAckMaxPerSession == 0 {
		cfg.PendingAckMaxPerSession = 1024
	}
	if cfg.EventQueueSize == 0 {
		cfg.EventQueueSize = 1024
	}
	return cfg
}

func defaultLogConfig(cfg LogConfig) LogConfig {
	if cfg.Level == "" {
		cfg.Level = "info"
	}
	if cfg.Dir == "" {
		cfg.Dir = "./logs"
	}
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = 100
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = 30
	}
	if cfg.MaxBackups <= 0 {
		cfg.MaxBackups = 10
	}
	if cfg.Format == "" {
		cfg.Format = "console"
	}
	if !cfg.Compress && !cfg.compressSet {
		cfg.Compress = true
	}
	if !cfg.Console && !cfg.consoleSet {
		cfg.Console = true
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
	if cfg.PendingAckMaxPerSession < 0 {
		return fmt.Errorf("%w: delivery pending ack max per session must be non-negative", ErrInvalidConfig)
	}
	if cfg.EventQueueSize < 0 {
		return fmt.Errorf("%w: delivery event queue size must be non-negative", ErrInvalidConfig)
	}
	return nil
}
