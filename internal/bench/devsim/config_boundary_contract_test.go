package devsim

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

func TestValidateConfigReportsEveryOperatorFacingBoundary(t *testing.T) {
	valid := defaultConfig()
	valid.Target.APIAddrs = []string{"http://node:5001"}
	valid.Target.GatewayTCPAddrs = []string{"node:5100"}
	tests := []struct {
		name     string
		mutate   func(*Config)
		wantText string
	}{
		{name: "version", mutate: func(c *Config) { c.Version = "v0" }, wantText: "version must be"},
		{name: "status listen", mutate: func(c *Config) { c.Status.Listen = "  " }, wantText: "status.listen is required"},
		{name: "API addresses", mutate: func(c *Config) { c.Target.APIAddrs = []string{" "} }, wantText: "target.api_addrs is required"},
		{name: "gateway addresses", mutate: func(c *Config) { c.Target.GatewayTCPAddrs = []string{" "} }, wantText: "target.gateway_tcp_addrs is required"},
		{name: "UID prefix", mutate: func(c *Config) { c.Identity.UIDPrefix = " " }, wantText: "identity.uid_prefix is required"},
		{name: "total users", mutate: func(c *Config) { c.Online.TotalUsers = 0 }, wantText: "online.total_users must be greater than zero"},
		{name: "heartbeat interval", mutate: func(c *Config) { c.Online.Heartbeat.Enabled = true; c.Online.Heartbeat.Interval = 0 }, wantText: "online.heartbeat.interval"},
		{name: "heartbeat timeout", mutate: func(c *Config) { c.Online.Heartbeat.Timeout = -time.Second }, wantText: "online.heartbeat.timeout"},
		{name: "person channel count", mutate: func(c *Config) { c.Profiles.PersonChannels = -1 }, wantText: "profiles.person_channels"},
		{name: "group channel count", mutate: func(c *Config) { c.Profiles.GroupChannels = -1 }, wantText: "profiles.group_channels"},
		{name: "group member count", mutate: func(c *Config) { c.Profiles.GroupMembers = -1 }, wantText: "profiles.group_members must not be negative"},
		{name: "person capacity", mutate: func(c *Config) { c.Online.TotalUsers = 4; c.Profiles.PersonChannels = 3 }, wantText: "requires two users per channel"},
		{name: "group capacity", mutate: func(c *Config) {
			c.Online.TotalUsers = 4
			c.Profiles.PersonChannels = 0
			c.Profiles.GroupChannels = 1
			c.Profiles.GroupMembers = 5
		}, wantText: "must not exceed online.total_users"},
		{name: "payload size", mutate: func(c *Config) { c.Traffic.PayloadSizeBytes = 0 }, wantText: "traffic.payload_size_bytes"},
		{name: "person rate", mutate: func(c *Config) { c.Traffic.PersonRatePerChannel.PerSecond = 0 }, wantText: "traffic.person_rate_per_channel"},
		{name: "group rate", mutate: func(c *Config) { c.Traffic.GroupRatePerChannel.PerSecond = 0 }, wantText: "traffic.group_rate_per_channel"},
		{name: "concurrency", mutate: func(c *Config) { c.Traffic.Concurrency = -1 }, wantText: "traffic.concurrency"},
		{name: "warmup", mutate: func(c *Config) { c.Traffic.Warmup = -time.Second }, wantText: "traffic.warmup"},
		{name: "window", mutate: func(c *Config) { c.Traffic.Window = 0 }, wantText: "traffic.window"},
		{name: "cooldown", mutate: func(c *Config) { c.Traffic.Cooldown = -time.Second }, wantText: "traffic.cooldown"},
		{name: "readiness timeout", mutate: func(c *Config) { c.Retry.ReadinessTimeout = 0 }, wantText: "retry.readiness_timeout"},
		{name: "restart backoff", mutate: func(c *Config) { c.Retry.RestartBackoff = 0 }, wantText: "retry.restart_backoff"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)
			err := validateConfig(cfg)
			if err == nil || !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("validateConfig() error = %v, want fragment %q", err, tt.wantText)
			}
		})
	}
	if err := validateConfig(valid); err != nil {
		t.Fatalf("validateConfig(valid) error = %v", err)
	}
}

func TestLoadConfigRejectsUnreadableUnknownAndInvalidOverrides(t *testing.T) {
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "missing.yaml"), nil); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("LoadConfig(missing) error = %v", err)
	}

	dir := t.TempDir()
	unknownPath := filepath.Join(dir, "unknown.yaml")
	if err := os.WriteFile(unknownPath, []byte("version: wkbench/dev-sim/v1\nunknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(unknownPath, nil); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("LoadConfig(unknown field) error = %v", err)
	}

	validPath := filepath.Join(dir, "valid.yaml")
	if err := os.WriteFile(validPath, []byte("version: wkbench/dev-sim/v1\ntarget:\n  api_addrs: [http://node:5001]\n  gateway_tcp_addrs: [node:5100]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "integer", env: map[string]string{"WK_SIM_USERS": "many"}, want: "WK_SIM_USERS"},
		{name: "rate", env: map[string]string{"WK_SIM_RATE": "fast"}, want: "WK_SIM_RATE"},
		{name: "duration", env: map[string]string{"WK_SIM_WARMUP": "later"}, want: "WK_SIM_WARMUP"},
		{name: "post override validation", env: map[string]string{"WK_SIM_USERS": "0"}, want: "online.total_users"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := LoadConfig(validPath, tt.env); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadConfig() error = %v, want fragment %q", err, tt.want)
			}
		})
	}
	cfg, err := LoadConfig(validPath, map[string]string{"WK_SIM_USERS": " ", "WK_SIM_VERIFY_RECV": " full ", "WK_SIM_UID_PREFIX": " test-u "})
	if err != nil {
		t.Fatalf("LoadConfig(blank/trimmed overrides) error = %v", err)
	}
	if cfg.Online.TotalUsers != 20 || cfg.Traffic.VerifyRecv != "full" || cfg.Identity.UIDPrefix != "test-u" {
		t.Fatalf("effective overrides = %+v", cfg)
	}
}

func TestBuildBenchInputsKeepsProfileSpecificTrafficContracts(t *testing.T) {
	tests := []struct {
		name           string
		personChannels int
		groupChannels  int
		wantProfile    string
		wantVerify     string
	}{
		{name: "person only", personChannels: 2, wantProfile: personProfileName, wantVerify: "full"},
		{name: "group only", groupChannels: 1, wantProfile: groupProfileName, wantVerify: "sampled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConfig()
			cfg.Target.APIAddrs = []string{" http://node:5001 "}
			cfg.Target.GatewayTCPAddrs = []string{" node:5100 "}
			cfg.Profiles.PersonChannels = tt.personChannels
			cfg.Profiles.GroupChannels = tt.groupChannels
			inputs, err := cfg.BuildBenchInputs(" run-1 ")
			if err != nil {
				t.Fatalf("BuildBenchInputs() error = %v", err)
			}
			if len(inputs.Scenario.Channels.Profiles) != 1 || inputs.Scenario.Channels.Profiles[0].Name != tt.wantProfile {
				t.Fatalf("profiles = %+v", inputs.Scenario.Channels.Profiles)
			}
			if len(inputs.Scenario.Messages.Traffic) != 1 || inputs.Scenario.Messages.Traffic[0].Verify.Recv.Mode != tt.wantVerify {
				t.Fatalf("traffic = %+v", inputs.Scenario.Messages.Traffic)
			}
			if inputs.Scenario.Run.ID != "run-1" || inputs.Target.API.Addrs[0] != "http://node:5001" || inputs.Target.Gateway.TCP.Addrs[0] != "node:5100" {
				t.Fatalf("trimmed inputs = %+v", inputs)
			}
		})
	}
	if got := personVerifyMode(" none "); got != "none" {
		t.Fatalf("personVerifyMode(none) = %q", got)
	}
	if got := formatRate(model.Rate{PerSecond: 1.25}); got != "1.25/s" {
		t.Fatalf("formatRate() = %q", got)
	}
}
