package devsim

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/planner"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"gopkg.in/yaml.v3"
)

const configVersion = "wkbench/dev-sim/v1"

const (
	simulatorWorkerID         = "wk-sim"
	personProfileName         = "sim-person"
	groupProfileName          = "sim-group"
	personTrafficName         = "sim-person-send"
	groupTrafficName          = "sim-group-send"
	defaultPrepareBatchSize   = 1000
	defaultTrafficConcurrency = 64
)

const devSimRunDuration = 24 * time.Hour

// BenchInputs are the regular wkbench inputs derived from a compact dev-sim config.
type BenchInputs struct {
	// Target is the black-box WuKongIM target configuration.
	Target model.Target
	// Workers contains the single in-process worker used for deterministic planning.
	Workers []model.Worker
	// Scenario is the regular wkbench scenario executed by the simulator.
	Scenario model.Scenario
	// Plan is the deterministic single-worker shard plan for Scenario.
	Plan model.Plan
}

// Config describes a compact long-running development simulator setup.
type Config struct {
	// Version is the dev-sim schema version.
	Version string `json:"version" yaml:"version"`
	// Status controls the local status HTTP API.
	Status StatusConfig `json:"status" yaml:"status"`
	// Target contains black-box WuKongIM endpoints used by the simulator.
	Target TargetConfig `json:"target" yaml:"target"`
	// Identity controls deterministic simulated user, device, and message names.
	Identity IdentityConfig `json:"identity" yaml:"identity"`
	// Online controls how many simulated users stay connected.
	Online OnlineConfig `json:"online" yaml:"online"`
	// Profiles controls the compact person/group channel shapes.
	Profiles ProfilesConfig `json:"profiles" yaml:"profiles"`
	// Traffic controls the low-rate message workload.
	Traffic TrafficConfig `json:"traffic" yaml:"traffic"`
	// Retry controls readiness and reconnect retry timing.
	Retry RetryConfig `json:"retry" yaml:"retry"`
}

// StatusConfig controls the simulator status HTTP API.
type StatusConfig struct {
	// Listen is the TCP listen address for /healthz and /status.
	Listen string `json:"listen" yaml:"listen"`
}

// TargetConfig contains target HTTP and WKProto gateway endpoints.
type TargetConfig struct {
	// APIAddrs are target HTTP API base addresses.
	APIAddrs []string `json:"api_addrs" yaml:"api_addrs"`
	// GatewayTCPAddrs are target WKProto TCP gateway addresses.
	GatewayTCPAddrs []string `json:"gateway_tcp_addrs" yaml:"gateway_tcp_addrs"`
	// BenchAPIToken is an optional bearer token for benchmark preparation routes.
	BenchAPIToken string `json:"bench_api_token" yaml:"bench_api_token"`
}

// IdentityConfig controls deterministic simulator identity names.
type IdentityConfig struct {
	// UIDPrefix is prepended to generated user IDs.
	UIDPrefix string `json:"uid_prefix" yaml:"uid_prefix"`
	// DevicePrefix is prepended to generated device IDs.
	DevicePrefix string `json:"device_prefix" yaml:"device_prefix"`
	// ClientMsgPrefix is prepended to generated client message IDs.
	ClientMsgPrefix string `json:"client_msg_prefix" yaml:"client_msg_prefix"`
	// TokenMode selects token preparation behavior, for example none or bench_api.
	TokenMode string `json:"token_mode" yaml:"token_mode"`
}

// OnlineConfig controls simulated online users.
type OnlineConfig struct {
	// TotalUsers is the size of the generated online identity pool.
	TotalUsers int `json:"total_users" yaml:"total_users"`
	// ConnectRate limits gateway connection attempts.
	ConnectRate model.Rate `json:"connect_rate" yaml:"connect_rate"`
	// Heartbeat keeps idle simulated users active on the gateway.
	Heartbeat model.HeartbeatConfig `json:"heartbeat" yaml:"heartbeat"`
}

// ProfilesConfig describes compact person and group channel counts.
type ProfilesConfig struct {
	// PersonChannels is the number of generated one-to-one channels.
	PersonChannels int `json:"person_channels" yaml:"person_channels"`
	// GroupChannels is the number of generated group channels.
	GroupChannels int `json:"group_channels" yaml:"group_channels"`
	// GroupMembers is the number of members per generated group channel.
	GroupMembers int `json:"group_members" yaml:"group_members"`
}

// TrafficConfig controls the simulator's low-rate message workload.
type TrafficConfig struct {
	// PayloadSizeBytes is the size of deterministic message payloads.
	PayloadSizeBytes int `json:"payload_size_bytes" yaml:"payload_size_bytes"`
	// PersonRatePerChannel is the per-channel rate for person traffic.
	PersonRatePerChannel model.Rate `json:"person_rate_per_channel" yaml:"person_rate_per_channel"`
	// GroupRatePerChannel is the per-channel rate for group traffic.
	GroupRatePerChannel model.Rate `json:"group_rate_per_channel" yaml:"group_rate_per_channel"`
	// Concurrency bounds in-flight send operations per traffic stream. Zero preserves sequential sends.
	Concurrency int `json:"concurrency" yaml:"concurrency"`
	// VerifyRecv selects receive verification mode for traffic.
	VerifyRecv string `json:"verify_recv" yaml:"verify_recv"`
	// Warmup is the reduced-rate traffic duration before the first measured run window.
	Warmup time.Duration `json:"warmup" yaml:"warmup"`
	// Window is the active traffic duration for each supervisor loop.
	Window time.Duration `json:"window" yaml:"window"`
	// Cooldown is the drain duration between traffic windows.
	Cooldown time.Duration `json:"cooldown" yaml:"cooldown"`
}

// RetryConfig controls readiness and runtime retry behavior.
type RetryConfig struct {
	// ReadinessTimeout is the maximum wait for initial target readiness.
	ReadinessTimeout time.Duration `json:"readiness_timeout" yaml:"readiness_timeout"`
	// RestartBackoff is the delay before reconnecting after a runtime error.
	RestartBackoff time.Duration `json:"restart_backoff" yaml:"restart_backoff"`
}

// LoadConfig reads a dev-sim YAML file, applies defaults and optional env overrides.
func LoadConfig(path string, env map[string]string) (Config, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	data = []byte(os.ExpandEnv(string(data)))
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := applyEnvOverrides(&cfg, env); err != nil {
		return Config{}, err
	}
	if err := validateConfig(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func defaultConfig() Config {
	return Config{
		Version: configVersion,
		Status:  StatusConfig{Listen: "127.0.0.1:19091"},
		Identity: IdentityConfig{
			UIDPrefix:       "sim-u",
			DevicePrefix:    "sim-d",
			ClientMsgPrefix: "sim-msg",
			TokenMode:       "none",
		},
		Online: OnlineConfig{
			TotalUsers:  20,
			ConnectRate: model.Rate{PerSecond: 10},
			Heartbeat: model.HeartbeatConfig{
				Enabled:  true,
				Interval: 30 * time.Second,
				Timeout:  5 * time.Second,
			},
		},
		Profiles: ProfilesConfig{
			PersonChannels: 5,
			GroupChannels:  2,
			GroupMembers:   10,
		},
		Traffic: TrafficConfig{
			PayloadSizeBytes:     128,
			PersonRatePerChannel: model.Rate{PerSecond: 0.2},
			GroupRatePerChannel:  model.Rate{PerSecond: 0.2},
			Concurrency:          defaultTrafficConcurrency,
			VerifyRecv:           "sampled",
			Warmup:               10 * time.Second,
			Window:               10 * time.Second,
			Cooldown:             time.Second,
		},
		Retry: RetryConfig{
			ReadinessTimeout: 2 * time.Minute,
			RestartBackoff:   5 * time.Second,
		},
	}
}

func applyEnvOverrides(cfg *Config, env map[string]string) error {
	if env == nil {
		return nil
	}
	if err := applyIntEnv(env, "WK_SIM_USERS", &cfg.Online.TotalUsers); err != nil {
		return err
	}
	if err := applyIntEnv(env, "WK_SIM_PERSON_CHANNELS", &cfg.Profiles.PersonChannels); err != nil {
		return err
	}
	if err := applyIntEnv(env, "WK_SIM_GROUP_CHANNELS", &cfg.Profiles.GroupChannels); err != nil {
		return err
	}
	if err := applyIntEnv(env, "WK_SIM_GROUP_MEMBERS", &cfg.Profiles.GroupMembers); err != nil {
		return err
	}
	if raw, ok := lookupEnvValue(env, "WK_SIM_RATE"); ok {
		rate, err := model.ParseRate(raw)
		if err != nil {
			return fmt.Errorf("WK_SIM_RATE: %w", err)
		}
		cfg.Traffic.PersonRatePerChannel = rate
		cfg.Traffic.GroupRatePerChannel = rate
	}
	if err := applyIntEnv(env, "WK_SIM_TRAFFIC_CONCURRENCY", &cfg.Traffic.Concurrency); err != nil {
		return err
	}
	if raw, ok := lookupEnvValue(env, "WK_SIM_WARMUP"); ok {
		warmup, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("WK_SIM_WARMUP: %w", err)
		}
		cfg.Traffic.Warmup = warmup
	}
	if raw, ok := lookupEnvValue(env, "WK_SIM_VERIFY_RECV"); ok {
		cfg.Traffic.VerifyRecv = strings.TrimSpace(raw)
	}
	if raw, ok := lookupEnvValue(env, "WK_SIM_UID_PREFIX"); ok {
		cfg.Identity.UIDPrefix = strings.TrimSpace(raw)
	}
	return nil
}

func applyIntEnv(env map[string]string, key string, out *int) error {
	raw, ok := lookupEnvValue(env, key)
	if !ok {
		return nil
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("%s: parse int: %w", key, err)
	}
	*out = value
	return nil
}

func lookupEnvValue(env map[string]string, key string) (string, bool) {
	raw, ok := env[key]
	if !ok {
		return "", false
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	return raw, true
}

func validateConfig(cfg Config) error {
	var problems []string
	if cfg.Version != configVersion {
		problems = append(problems, "version must be "+configVersion)
	}
	if strings.TrimSpace(cfg.Status.Listen) == "" {
		problems = append(problems, "status.listen is required")
	}
	if len(nonEmptyStrings(cfg.Target.APIAddrs)) == 0 {
		problems = append(problems, "target.api_addrs is required")
	}
	if len(nonEmptyStrings(cfg.Target.GatewayTCPAddrs)) == 0 {
		problems = append(problems, "target.gateway_tcp_addrs is required")
	}
	if strings.TrimSpace(cfg.Identity.UIDPrefix) == "" {
		problems = append(problems, "identity.uid_prefix is required")
	}
	if cfg.Online.TotalUsers <= 0 {
		problems = append(problems, "online.total_users must be greater than zero")
	}
	if cfg.Online.Heartbeat.Enabled && cfg.Online.Heartbeat.Interval <= 0 {
		problems = append(problems, "online.heartbeat.interval must be greater than zero when enabled")
	}
	if cfg.Online.Heartbeat.Timeout < 0 {
		problems = append(problems, "online.heartbeat.timeout must not be negative")
	}
	if cfg.Profiles.PersonChannels < 0 {
		problems = append(problems, "profiles.person_channels must not be negative")
	}
	if cfg.Profiles.GroupChannels < 0 {
		problems = append(problems, "profiles.group_channels must not be negative")
	}
	if cfg.Profiles.GroupMembers < 0 {
		problems = append(problems, "profiles.group_members must not be negative")
	}
	if cfg.Profiles.PersonChannels*2 > cfg.Online.TotalUsers {
		problems = append(problems, "profiles.person_channels requires two users per channel")
	}
	if cfg.Profiles.GroupChannels > 0 && cfg.Profiles.GroupMembers > cfg.Online.TotalUsers {
		problems = append(problems, "profiles.group_members must not exceed online.total_users")
	}
	if cfg.Traffic.PayloadSizeBytes <= 0 {
		problems = append(problems, "traffic.payload_size_bytes must be greater than zero")
	}
	if cfg.Traffic.PersonRatePerChannel.PerSecond <= 0 {
		problems = append(problems, "traffic.person_rate_per_channel must be greater than zero")
	}
	if cfg.Traffic.GroupRatePerChannel.PerSecond <= 0 {
		problems = append(problems, "traffic.group_rate_per_channel must be greater than zero")
	}
	if cfg.Traffic.Concurrency < 0 {
		problems = append(problems, "traffic.concurrency must not be negative")
	}
	if cfg.Traffic.Warmup < 0 {
		problems = append(problems, "traffic.warmup must not be negative")
	}
	if cfg.Traffic.Window <= 0 {
		problems = append(problems, "traffic.window must be greater than zero")
	}
	if cfg.Traffic.Cooldown < 0 {
		problems = append(problems, "traffic.cooldown must not be negative")
	}
	if cfg.Retry.ReadinessTimeout <= 0 {
		problems = append(problems, "retry.readiness_timeout must be greater than zero")
	}
	if cfg.Retry.RestartBackoff <= 0 {
		problems = append(problems, "retry.restart_backoff must be greater than zero")
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid dev-sim config: %s", strings.Join(problems, "; "))
	}
	return nil
}

func nonEmptyStrings(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

// BuildBenchInputs converts the compact simulator config into normal wkbench inputs.
func (cfg Config) BuildBenchInputs(runID string) (BenchInputs, error) {
	target := model.Target{
		Name: "docker-compose-dev-sim",
		API: model.TargetAPIConfig{
			Addrs: nonEmptyStrings(cfg.Target.APIAddrs),
		},
		Gateway: model.TargetGatewayConfig{
			TCP: model.TargetGatewayTCPConfig{Addrs: nonEmptyStrings(cfg.Target.GatewayTCPAddrs)},
		},
		BenchAPI: model.BenchAPIConfig{
			Enabled: true,
			Addrs:   nonEmptyStrings(cfg.Target.APIAddrs),
			Token:   strings.TrimSpace(cfg.Target.BenchAPIToken),
		},
	}
	workers := []model.Worker{{ID: simulatorWorkerID, Weight: 1, InsecureControl: true}}
	scenario := model.Scenario{
		Version: "wkbench/v1",
		Run: model.RunConfig{
			ID:       strings.TrimSpace(runID),
			Warmup:   cfg.Traffic.Warmup,
			Duration: devSimRunDuration,
			Cooldown: cfg.Traffic.Cooldown,
			FailFast: true,
		},
		Limits: model.LimitsConfig{Hard: model.HardLimitsConfig{
			MaxWorkerFailed:        -1,
			MaxConnectErrorRate:    -1,
			MaxSendackErrorRate:    -1,
			MaxRecvVerifyErrorRate: -1,
		}},
		Prepare: model.PrepareConfig{
			Concurrency: 4,
			Retry:       model.RetryConfig{MaxAttempts: 3, Backoff: time.Second},
		},
		Identity: model.IdentityConfig{
			UIDPrefix:       strings.TrimSpace(cfg.Identity.UIDPrefix),
			DevicePrefix:    strings.TrimSpace(cfg.Identity.DevicePrefix),
			ClientMsgPrefix: strings.TrimSpace(cfg.Identity.ClientMsgPrefix),
			Token:           model.TokenConfig{Mode: strings.TrimSpace(cfg.Identity.TokenMode)},
		},
		Online: model.OnlineConfig{
			TotalUsers:     cfg.Online.TotalUsers,
			ConnectRate:    cfg.Online.ConnectRate,
			GatewayBalance: "round_robin",
			Heartbeat:      cfg.Online.Heartbeat,
		},
		Messages: model.MessagesConfig{
			Payload: model.PayloadConfig{SizeBytes: cfg.Traffic.PayloadSizeBytes, Mode: "deterministic"},
		},
		Cleanup: model.CleanupConfig{Enabled: false},
	}
	if cfg.Profiles.PersonChannels > 0 {
		scenario.Channels.Profiles = append(scenario.Channels.Profiles, model.ChannelProfile{
			Name:        personProfileName,
			ChannelType: model.ChannelTypePerson,
			Count:       cfg.Profiles.PersonChannels,
			Online:      model.ChannelOnlineConfig{SenderRatio: 1, RecipientRatio: 1},
		})
		scenario.Messages.Traffic = append(scenario.Messages.Traffic, model.TrafficConfig{
			Name:           personTrafficName,
			ChannelRef:     personProfileName,
			RatePerChannel: cfg.Traffic.PersonRatePerChannel,
			Concurrency:    cfg.Traffic.Concurrency,
			RecvAck:        true,
			Verify:         model.VerifyConfig{Recv: model.RecvVerifyConfig{Mode: personVerifyMode(cfg.Traffic.VerifyRecv)}},
		})
	}
	if cfg.Profiles.GroupChannels > 0 {
		scenario.Channels.Profiles = append(scenario.Channels.Profiles, model.ChannelProfile{
			Name:        groupProfileName,
			ChannelType: model.ChannelTypeGroup,
			Count:       cfg.Profiles.GroupChannels,
			Members:     model.MembersConfig{Count: cfg.Profiles.GroupMembers, Overlap: "allowed"},
			Online:      model.ChannelOnlineConfig{MemberRatio: 1},
			Shard:       model.ShardConfig{Mode: "hash"},
			Prepare:     model.ChannelPrepareConfig{SubscribersBatchSize: defaultPrepareBatchSize},
		})
		scenario.Messages.Traffic = append(scenario.Messages.Traffic, model.TrafficConfig{
			Name:           groupTrafficName,
			ChannelRef:     groupProfileName,
			RatePerChannel: cfg.Traffic.GroupRatePerChannel,
			Concurrency:    cfg.Traffic.Concurrency,
			RecvAck:        true,
			Verify: model.VerifyConfig{Recv: model.RecvVerifyConfig{
				Mode:                 strings.TrimSpace(cfg.Traffic.VerifyRecv),
				SampleSizePerMessage: 1,
			}},
		})
	}
	plan, err := planner.Build(scenario, workers)
	if err != nil {
		return BenchInputs{}, err
	}
	return BenchInputs{Target: target, Workers: workers, Scenario: scenario, Plan: plan}, nil
}

// Snapshot builds a triage-friendly view of the effective simulator configuration.
func (cfg Config) Snapshot() ConfigSnapshot {
	return ConfigSnapshot{
		TargetAPIAddrs:       nonEmptyStrings(cfg.Target.APIAddrs),
		GatewayTCPAddrs:      nonEmptyStrings(cfg.Target.GatewayTCPAddrs),
		UIDPrefix:            strings.TrimSpace(cfg.Identity.UIDPrefix),
		DevicePrefix:         strings.TrimSpace(cfg.Identity.DevicePrefix),
		ClientMsgPrefix:      strings.TrimSpace(cfg.Identity.ClientMsgPrefix),
		TokenMode:            strings.TrimSpace(cfg.Identity.TokenMode),
		TotalUsers:           cfg.Online.TotalUsers,
		ConnectRate:          formatRate(cfg.Online.ConnectRate),
		PersonChannels:       cfg.Profiles.PersonChannels,
		GroupChannels:        cfg.Profiles.GroupChannels,
		GroupMembers:         cfg.Profiles.GroupMembers,
		PayloadSizeBytes:     cfg.Traffic.PayloadSizeBytes,
		PersonRatePerChannel: formatRate(cfg.Traffic.PersonRatePerChannel),
		GroupRatePerChannel:  formatRate(cfg.Traffic.GroupRatePerChannel),
		Concurrency:          cfg.Traffic.Concurrency,
		VerifyRecv:           strings.TrimSpace(cfg.Traffic.VerifyRecv),
		Warmup:               cfg.Traffic.Warmup.String(),
		Window:               cfg.Traffic.Window.String(),
		Cooldown:             cfg.Traffic.Cooldown.String(),
		ReadinessTimeout:     cfg.Retry.ReadinessTimeout.String(),
		RestartBackoff:       cfg.Retry.RestartBackoff.String(),
	}
}

func personVerifyMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "sampled" {
		return "full"
	}
	return mode
}

func formatRate(rate model.Rate) string {
	return fmt.Sprintf("%g/s", rate.PerSecond)
}
