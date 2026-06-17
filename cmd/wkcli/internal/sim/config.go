package sim

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	defaultUsers          = 100
	defaultGroups         = 20
	defaultGroupMembers   = 10
	defaultRate           = "0.2/s"
	defaultPayloadSize    = "128B"
	defaultConnectRate    = 20
	defaultConcurrency    = 64
	defaultAckTimeout     = 5 * time.Second
	defaultOpTimeout      = 5 * time.Second
	defaultStatusListen   = "127.0.0.1:19091"
	defaultStatusInterval = 2 * time.Second
	defaultUIDPrefix      = "wkcli-sim-u"
	defaultDevicePrefix   = "wkcli-sim-d"
	defaultChannelPrefix  = "wkcli-sim-g"
	defaultRetryBackoff   = 2 * time.Second
)

// Rate describes a per-second simulation operation rate.
type Rate struct {
	// PerSecond is the normalized number of operations scheduled per second.
	PerSecond float64
}

// Config carries the wkcli sim command configuration after flag parsing.
type Config struct {
	// ContextDir is the directory containing persisted wkcli context files.
	ContextDir string
	// ContextName selects a persisted wkcli context.
	ContextName string
	// Servers are WuKongIM HTTP API server addresses.
	Servers []string
	// Gateways are WKProto TCP gateway addresses.
	Gateways []string
	// BenchToken is an optional bearer token for bench-capable APIs.
	BenchToken string
	// Users is the number of simulated users.
	Users int
	// Groups is the number of simulated group channels.
	Groups int
	// GroupMembers is the number of members assigned to each group.
	GroupMembers int
	// Rate is the normalized per-group message rate.
	Rate Rate
	// RatePerGroup is the raw per-group message rate flag value.
	RatePerGroup string
	// PayloadSize is the raw payload size flag value.
	PayloadSize string
	// PayloadBytes is the normalized payload size in bytes.
	PayloadBytes int
	// ConnectRate limits simulated client connects per second.
	ConnectRate int
	// Concurrency limits concurrent simulation work.
	Concurrency int
	// AckTimeout bounds SENDACK waits.
	AckTimeout time.Duration
	// OperationTimeout bounds non-SEND setup operations.
	OperationTimeout time.Duration
	// RunID scopes generated identities and idempotency keys to one run.
	RunID string
	// UIDPrefix prefixes generated user IDs.
	UIDPrefix string
	// DevicePrefix prefixes generated device IDs.
	DevicePrefix string
	// ChannelPrefix prefixes generated group channel IDs.
	ChannelPrefix string
	// StatusListen is the address used by the local status endpoint.
	StatusListen string
	// StatusInterval controls status snapshot emission.
	StatusInterval time.Duration
	// MaxRuntime optionally caps total simulation runtime.
	MaxRuntime time.Duration
	// RetryBackoff is the fixed delay between retryable operations.
	RetryBackoff time.Duration
	// JSON enables JSON command output.
	JSON bool
}

func normalizeConfig(cfg Config) (Config, error) {
	cfg.Servers = dedupeValues(splitValues(cfg.Servers))
	cfg.Gateways = dedupeValues(splitValues(cfg.Gateways))
	if len(cfg.Servers) == 0 && len(cfg.Gateways) == 0 && strings.TrimSpace(cfg.ContextName) == "" {
		return Config{}, fmt.Errorf("target is required: set --server, --gateway, or --context")
	}
	if cfg.Users < 0 {
		return Config{}, fmt.Errorf("--users must be greater than zero")
	}
	if cfg.Users == 0 {
		cfg.Users = defaultUsers
	}
	if cfg.Groups < 0 {
		return Config{}, fmt.Errorf("--groups must be greater than zero")
	}
	if cfg.Groups == 0 {
		cfg.Groups = defaultGroups
	}
	if cfg.GroupMembers < 0 {
		return Config{}, fmt.Errorf("--group-members must be greater than zero")
	}
	if cfg.GroupMembers == 0 {
		cfg.GroupMembers = defaultGroupMembers
	}
	if strings.TrimSpace(cfg.RatePerGroup) == "" {
		cfg.RatePerGroup = defaultRate
	}
	rate, err := parseRate(cfg.RatePerGroup)
	if err != nil {
		return Config{}, err
	}
	cfg.Rate = rate
	if strings.TrimSpace(cfg.PayloadSize) == "" {
		cfg.PayloadSize = defaultPayloadSize
	}
	payloadBytes, err := parseByteSize(cfg.PayloadSize)
	if err != nil {
		return Config{}, err
	}
	cfg.PayloadBytes = payloadBytes
	if cfg.ConnectRate < 0 {
		return Config{}, fmt.Errorf("--connect-rate must be greater than zero")
	}
	if cfg.ConnectRate == 0 {
		cfg.ConnectRate = defaultConnectRate
	}
	if cfg.Concurrency < 0 {
		return Config{}, fmt.Errorf("--concurrency must be greater than zero")
	}
	if cfg.Concurrency == 0 {
		cfg.Concurrency = defaultConcurrency
	}
	if cfg.AckTimeout < 0 {
		return Config{}, fmt.Errorf("--ack-timeout must be greater than zero")
	}
	if cfg.AckTimeout == 0 {
		cfg.AckTimeout = defaultAckTimeout
	}
	if cfg.OperationTimeout < 0 {
		return Config{}, fmt.Errorf("--operation-timeout must be greater than zero")
	}
	if cfg.OperationTimeout == 0 {
		cfg.OperationTimeout = defaultOpTimeout
	}
	if strings.TrimSpace(cfg.RunID) == "" {
		runID, err := newRunID()
		if err != nil {
			return Config{}, err
		}
		cfg.RunID = runID
	}
	if strings.TrimSpace(cfg.UIDPrefix) == "" {
		cfg.UIDPrefix = defaultUIDPrefix
	}
	if strings.TrimSpace(cfg.DevicePrefix) == "" {
		cfg.DevicePrefix = defaultDevicePrefix
	}
	if strings.TrimSpace(cfg.ChannelPrefix) == "" {
		cfg.ChannelPrefix = defaultChannelPrefix
	}
	if strings.TrimSpace(cfg.StatusListen) == "" {
		cfg.StatusListen = defaultStatusListen
	}
	if cfg.StatusInterval < 0 {
		return Config{}, fmt.Errorf("--status-interval must be greater than zero")
	}
	if cfg.StatusInterval == 0 {
		cfg.StatusInterval = defaultStatusInterval
	}
	if cfg.MaxRuntime < 0 {
		return Config{}, fmt.Errorf("--max-runtime must be non-negative")
	}
	if cfg.RetryBackoff < 0 {
		return Config{}, fmt.Errorf("--retry-backoff must be greater than zero")
	}
	if cfg.RetryBackoff == 0 {
		cfg.RetryBackoff = defaultRetryBackoff
	}
	return cfg, nil
}

func parseRate(value string) (Rate, error) {
	trimmed := strings.TrimSpace(value)
	if strings.HasSuffix(trimmed, "/s") {
		trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, "/s"))
	}
	n, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || n <= 0 {
		return Rate{}, fmt.Errorf("invalid --rate %q", value)
	}
	return Rate{PerSecond: n}, nil
}

func parseByteSize(value string) (int, error) {
	trimmed := strings.TrimSpace(value)
	upper := strings.ToUpper(trimmed)
	multipliers := []struct {
		suffix string
		value  int64
	}{
		{"KIB", 1024},
		{"MIB", 1024 * 1024},
		{"GIB", 1024 * 1024 * 1024},
		{"KB", 1000},
		{"MB", 1000 * 1000},
		{"GB", 1000 * 1000 * 1000},
		{"B", 1},
	}
	number := upper
	multiplier := int64(1)
	for _, item := range multipliers {
		if strings.HasSuffix(upper, item.suffix) {
			number = strings.TrimSpace(strings.TrimSuffix(upper, item.suffix))
			multiplier = item.value
			break
		}
	}
	n, err := strconv.ParseFloat(number, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid --payload-size %q", value)
	}
	size := int64(n * float64(multiplier))
	if size <= 0 || size > int64(^uint(0)>>1) {
		return 0, fmt.Errorf("invalid --payload-size %q", value)
	}
	return int(size), nil
}

func splitValues(values []string) []string {
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
	}
	return out
}

func dedupeValues(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func newRunID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate sim run id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
