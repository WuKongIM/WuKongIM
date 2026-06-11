package app

import (
	"errors"
	"fmt"
	"math"
	"runtime"
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
	// Channel configures channel management behavior.
	Channel ChannelConfig
	// Conversation configures conversation authority and list reads.
	Conversation ConversationConfig
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
	// HealthDebugEnabled exposes local debug snapshot endpoints on the API listener.
	HealthDebugEnabled bool
	// Diagnostics configures the bounded local diagnostics event store and sampling policy.
	Diagnostics DiagnosticsConfig

	diagnosticsEnabledSet         bool
	diagnosticsSampleRateSet      bool
	diagnosticsErrorSampleRateSet bool
}

// SetDiagnosticsExplicitFlags records which diagnostics values were explicitly configured.
func (c *ObservabilityConfig) SetDiagnosticsExplicitFlags(enabledSet, sampleRateSet, errorSampleRateSet bool) {
	if c == nil {
		return
	}
	c.diagnosticsEnabledSet = enabledSet
	c.diagnosticsSampleRateSet = sampleRateSet
	c.diagnosticsErrorSampleRateSet = errorSampleRateSet
}

// DiagnosticsConfig controls local diagnostics event retention and sampling.
type DiagnosticsConfig struct {
	// Enabled turns local diagnostics event capture on or off.
	Enabled bool
	// BufferSize is the maximum number of diagnostics events retained in memory.
	BufferSize int
	// SampleRate is the baseline keep probability for successful diagnostics events.
	SampleRate float64
	// SlowThreshold keeps successful events whose duration is at least this threshold.
	SlowThreshold time.Duration
	// ErrorSampleRate is the keep probability for diagnostics events with non-ok results.
	ErrorSampleRate float64
	// DeepSampleRate is the keep probability for expensive reactor/store detail sidecars.
	DeepSampleRate float64
	// DeepSlowThreshold enables lazy deep trace selection for slow reactor/store stages.
	DeepSlowThreshold time.Duration
	// DeepMaxItemsPerBatch bounds how many traced messages one deep batch may expand into events.
	DeepMaxItemsPerBatch int
	// DebugAPIEnabled enables local diagnostics debug HTTP endpoints on the API listener.
	DebugAPIEnabled bool
	// DebugMatches configures temporary high-priority sampling rules.
	DebugMatches []DiagnosticsDebugMatchConfig
}

// DiagnosticsDebugMatchConfig defines one temporary diagnostics sampling override rule.
type DiagnosticsDebugMatchConfig struct {
	// UID matches the sender UID when it is set.
	UID string `json:"uid,omitempty"`
	// ChannelKey matches the diagnostics-safe channel identifier when it is set.
	ChannelKey string `json:"channel_key,omitempty"`
	// ClientMsgNo matches the client message number when it is set.
	ClientMsgNo string `json:"client_msg_no,omitempty"`
	// TraceID matches the trace identifier when it is set.
	TraceID string `json:"trace_id,omitempty"`
	// TTLSeconds controls how long the temporary debug sampling rule stays active.
	TTLSeconds int `json:"ttl_seconds,omitempty"`
	// SampleRate is the keep probability applied when the rule matches.
	SampleRate float64 `json:"sample_rate,omitempty"`
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

// ChannelConfig contains channel management settings.
type ChannelConfig struct {
	// LargeGroupSubscriberThreshold marks a channel large when ordinary subscriber count exceeds it.
	LargeGroupSubscriberThreshold int
}

// ConversationConfig contains conversation authority and read-model settings.
type ConversationConfig struct {
	// MaxLastMessageConcurrency bounds concurrent channel tail reads for one conversation list request.
	MaxLastMessageConcurrency int
	// AuthorityCacheMaxRowsPerUID is retained for config compatibility; the runtime-backed authority currently does not enforce a per-UID cache bound.
	AuthorityCacheMaxRowsPerUID int
	// AuthorityCacheMaxRows bounds all unflushed authority cache rows on this node.
	AuthorityCacheMaxRows int
	// AuthorityListDBWindowMax is retained for config compatibility; the runtime-backed authority currently owns its active-view DB window internally.
	AuthorityListDBWindowMax int
	// AuthorityHandoffTimeout bounds how long a new authority waits for old-authority drain before explicit abandon.
	AuthorityHandoffTimeout time.Duration
	// AuthorityFlushInterval controls how often dirty authority active rows are flushed to durable storage.
	AuthorityFlushInterval time.Duration
	// AuthorityFlushBatchRows bounds dirty authority active rows flushed in one tick.
	AuthorityFlushBatchRows int
	// AuthorityAdmitBatchRows limits active rows in one authority admission batch.
	AuthorityAdmitBatchRows int
	// AuthorityAdmitConcurrency limits concurrent authority admission batches.
	AuthorityAdmitConcurrency int
}

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
	// ChannelWriteReactorCount is the number of channel-hashed authority reactors. Zero derives a CPU-aware default.
	ChannelWriteReactorCount int
	// ChannelWritePrepareWorkers is the per-reactor worker budget for send preparation. Zero uses the prepare-stage default.
	ChannelWritePrepareWorkers int
	// ChannelWriteAppendWorkers is the per-reactor worker budget for blocking durable append calls. Zero uses the append-stage default.
	ChannelWriteAppendWorkers int
	// ChannelWritePostCommitWorkers is the per-reactor worker budget for best-effort post-commit effects. Zero uses the post-commit-stage default.
	ChannelWritePostCommitWorkers int
	// ChannelWriteRecipientDispatchConcurrency bounds per-message recipient authority dispatch fanout inside one post-commit effect. Zero uses a bounded default.
	ChannelWriteRecipientDispatchConcurrency int
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
	if cfg.ChannelWriteReactorCount == 0 {
		cfg.ChannelWriteReactorCount = defaultChannelWriteReactorCount()
	}
	if cfg.ChannelWritePrepareWorkers == 0 {
		cfg.ChannelWritePrepareWorkers = defaultChannelWritePrepareWorkers()
	}
	if cfg.ChannelWriteAppendWorkers == 0 {
		cfg.ChannelWriteAppendWorkers = defaultChannelWriteAppendWorkers()
	}
	if cfg.ChannelWritePostCommitWorkers == 0 {
		cfg.ChannelWritePostCommitWorkers = defaultChannelWritePostCommitWorkers()
	}
	if cfg.ChannelWriteRecipientDispatchConcurrency == 0 {
		cfg.ChannelWriteRecipientDispatchConcurrency = defaultChannelWriteRecipientDispatchConcurrency()
	}
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

func defaultChannelConfig(cfg ChannelConfig) ChannelConfig {
	if cfg.LargeGroupSubscriberThreshold == 0 {
		cfg.LargeGroupSubscriberThreshold = 500
	}
	return cfg
}

func defaultChannelWriteReactorCount() int {
	return appMaxInt(4, runtime.GOMAXPROCS(0))
}

func defaultChannelWritePrepareWorkers() int {
	return 100
}

func defaultChannelWriteAppendWorkers() int {
	return 2000
}

func defaultChannelWritePostCommitWorkers() int {
	return 1000
}

func defaultChannelWriteRecipientDispatchConcurrency() int {
	return 100
}

func defaultConversationConfig(cfg ConversationConfig) ConversationConfig {
	if cfg.MaxLastMessageConcurrency == 0 {
		cfg.MaxLastMessageConcurrency = 32
	}
	if cfg.AuthorityCacheMaxRowsPerUID == 0 {
		cfg.AuthorityCacheMaxRowsPerUID = 4096
	}
	if cfg.AuthorityCacheMaxRows == 0 {
		cfg.AuthorityCacheMaxRows = 100000
	}
	if cfg.AuthorityListDBWindowMax == 0 {
		cfg.AuthorityListDBWindowMax = 1000
	}
	if cfg.AuthorityHandoffTimeout == 0 {
		cfg.AuthorityHandoffTimeout = 3 * time.Second
	}
	if cfg.AuthorityFlushInterval == 0 {
		cfg.AuthorityFlushInterval = time.Second
	}
	if cfg.AuthorityFlushBatchRows == 0 {
		cfg.AuthorityFlushBatchRows = 512
	}
	if cfg.AuthorityAdmitBatchRows == 0 {
		cfg.AuthorityAdmitBatchRows = 512
	}
	if cfg.AuthorityAdmitConcurrency == 0 {
		cfg.AuthorityAdmitConcurrency = 16
	}
	return cfg
}

func defaultObservabilityConfig(cfg ObservabilityConfig) ObservabilityConfig {
	if !cfg.diagnosticsEnabledSet {
		cfg.Diagnostics.Enabled = true
	}
	if cfg.Diagnostics.BufferSize <= 0 {
		cfg.Diagnostics.BufferSize = 50000
	}
	if cfg.Diagnostics.SampleRate == 0 && !cfg.diagnosticsSampleRateSet {
		cfg.Diagnostics.SampleRate = 0.01
	}
	if cfg.Diagnostics.SlowThreshold <= 0 {
		cfg.Diagnostics.SlowThreshold = 500 * time.Millisecond
	}
	if cfg.Diagnostics.ErrorSampleRate == 0 && !cfg.diagnosticsErrorSampleRateSet {
		cfg.Diagnostics.ErrorSampleRate = 1.0
	}
	if cfg.Diagnostics.DeepSlowThreshold == 0 {
		cfg.Diagnostics.DeepSlowThreshold = cfg.Diagnostics.SlowThreshold
	}
	if cfg.Diagnostics.DeepMaxItemsPerBatch == 0 {
		cfg.Diagnostics.DeepMaxItemsPerBatch = 16
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

func validateChannelConfig(cfg ChannelConfig) error {
	if cfg.LargeGroupSubscriberThreshold <= 0 {
		return fmt.Errorf("%w: channel large group subscriber threshold must be positive", ErrInvalidConfig)
	}
	return nil
}

func validateDeliveryConfig(cfg DeliveryConfig) error {
	if cfg.ChannelWriteReactorCount < 0 {
		return fmt.Errorf("%w: delivery channel write reactor count must be non-negative", ErrInvalidConfig)
	}
	if cfg.ChannelWritePrepareWorkers < 0 {
		return fmt.Errorf("%w: delivery channel write prepare workers must be non-negative", ErrInvalidConfig)
	}
	if cfg.ChannelWriteAppendWorkers < 0 {
		return fmt.Errorf("%w: delivery channel write append workers must be non-negative", ErrInvalidConfig)
	}
	if cfg.ChannelWritePostCommitWorkers < 0 {
		return fmt.Errorf("%w: delivery channel write post-commit workers must be non-negative", ErrInvalidConfig)
	}
	if cfg.ChannelWriteRecipientDispatchConcurrency < 0 {
		return fmt.Errorf("%w: delivery channel write recipient dispatch concurrency must be non-negative", ErrInvalidConfig)
	}
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

func validateConversationConfig(cfg ConversationConfig) error {
	if cfg.MaxLastMessageConcurrency < 0 {
		return fmt.Errorf("%w: conversation last message concurrency must be non-negative", ErrInvalidConfig)
	}
	if cfg.AuthorityCacheMaxRowsPerUID <= 0 {
		return fmt.Errorf("%w: conversation authority cache max rows per uid must be positive", ErrInvalidConfig)
	}
	if cfg.AuthorityCacheMaxRows <= 0 {
		return fmt.Errorf("%w: conversation authority cache max rows must be positive", ErrInvalidConfig)
	}
	if cfg.AuthorityListDBWindowMax <= 0 {
		return fmt.Errorf("%w: conversation authority list db window max must be positive", ErrInvalidConfig)
	}
	if cfg.AuthorityHandoffTimeout <= 0 {
		return fmt.Errorf("%w: conversation authority handoff timeout must be positive", ErrInvalidConfig)
	}
	if cfg.AuthorityFlushInterval <= 0 {
		return fmt.Errorf("%w: conversation authority flush interval must be positive", ErrInvalidConfig)
	}
	if cfg.AuthorityFlushBatchRows <= 0 {
		return fmt.Errorf("%w: conversation authority flush batch rows must be positive", ErrInvalidConfig)
	}
	if cfg.AuthorityAdmitBatchRows <= 0 {
		return fmt.Errorf("%w: conversation authority admit batch rows must be positive", ErrInvalidConfig)
	}
	if cfg.AuthorityAdmitConcurrency <= 0 {
		return fmt.Errorf("%w: conversation authority admit concurrency must be positive", ErrInvalidConfig)
	}
	return nil
}

func validateObservabilityConfig(cfg ObservabilityConfig) error {
	if !validDiagnosticsSampleRate(cfg.Diagnostics.SampleRate) {
		return fmt.Errorf("%w: diagnostics sample rate must be between 0 and 1", ErrInvalidConfig)
	}
	if !validDiagnosticsSampleRate(cfg.Diagnostics.ErrorSampleRate) {
		return fmt.Errorf("%w: diagnostics error sample rate must be between 0 and 1", ErrInvalidConfig)
	}
	if !validDiagnosticsSampleRate(cfg.Diagnostics.DeepSampleRate) {
		return fmt.Errorf("%w: diagnostics deep sample rate must be between 0 and 1", ErrInvalidConfig)
	}
	if cfg.Diagnostics.DeepSlowThreshold < 0 {
		return fmt.Errorf("%w: diagnostics deep slow threshold must be >= 0", ErrInvalidConfig)
	}
	if cfg.Diagnostics.DeepMaxItemsPerBatch < 0 {
		return fmt.Errorf("%w: diagnostics deep max items per batch must be >= 0", ErrInvalidConfig)
	}
	for _, match := range cfg.Diagnostics.DebugMatches {
		if !validDiagnosticsSampleRate(match.SampleRate) {
			return fmt.Errorf("%w: diagnostics debug match sample rate must be between 0 and 1", ErrInvalidConfig)
		}
		if match.TTLSeconds < 0 {
			return fmt.Errorf("%w: diagnostics debug match ttl seconds must be >= 0", ErrInvalidConfig)
		}
	}
	return nil
}

func validDiagnosticsSampleRate(rate float64) bool {
	return !math.IsNaN(rate) && !math.IsInf(rate, 0) && rate >= 0 && rate <= 1
}

func appMaxInt(a, b int) int {
	if a >= b {
		return a
	}
	return b
}
