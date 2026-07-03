package devsim

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev-sim.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`version: wkbench/dev-sim/v1
status:
  listen: 127.0.0.1:19091
target:
  api_addrs: ["http://wk-node1:5001"]
  gateway_tcp_addrs: ["wk-node1:5100"]
`), 0o644))

	cfg, err := LoadConfig(path, nil)

	require.NoError(t, err)
	require.Equal(t, "wkbench/dev-sim/v1", cfg.Version)
	require.Equal(t, 20, cfg.Online.TotalUsers)
	require.True(t, cfg.Online.Heartbeat.Enabled)
	require.Equal(t, 30*time.Second, cfg.Online.Heartbeat.Interval)
	require.Equal(t, 5, cfg.Profiles.PersonChannels)
	require.Equal(t, "sim-u", cfg.Identity.UIDPrefix)
	require.Equal(t, defaultTrafficConcurrency, cfg.Traffic.Concurrency)
}

func TestConfigEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev-sim.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`version: wkbench/dev-sim/v1
target:
  api_addrs: ["http://wk-node1:5001"]
  gateway_tcp_addrs: ["wk-node1:5100"]
`), 0o644))

	cfg, err := LoadConfig(path, map[string]string{
		"WK_SIM_USERS":               "40",
		"WK_SIM_PERSON_CHANNELS":     "6",
		"WK_SIM_GROUP_CHANNELS":      "3",
		"WK_SIM_GROUP_MEMBERS":       "12",
		"WK_SIM_RATE":                "1.5/s",
		"WK_SIM_TRAFFIC_CONCURRENCY": "32",
		"WK_SIM_WARMUP":              "3s",
		"WK_SIM_VERIFY_RECV":         "none",
		"WK_SIM_UID_PREFIX":          "dev-u",
	})

	require.NoError(t, err)
	require.Equal(t, 40, cfg.Online.TotalUsers)
	require.Equal(t, 6, cfg.Profiles.PersonChannels)
	require.Equal(t, 3, cfg.Profiles.GroupChannels)
	require.Equal(t, 12, cfg.Profiles.GroupMembers)
	require.Equal(t, 1.5, cfg.Traffic.PersonRatePerChannel.PerSecond)
	require.Equal(t, 1.5, cfg.Traffic.GroupRatePerChannel.PerSecond)
	require.Equal(t, 32, cfg.Traffic.Concurrency)
	require.Equal(t, 3*time.Second, cfg.Traffic.Warmup)
	require.Equal(t, "none", cfg.Traffic.VerifyRecv)
	require.Equal(t, "dev-u", cfg.Identity.UIDPrefix)
}

func TestLoadConfigParsesDurationFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev-sim.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`version: wkbench/dev-sim/v1
target:
  api_addrs: ["http://wk-node1:5001"]
  gateway_tcp_addrs: ["wk-node1:5100"]
traffic:
  warmup: 750ms
  window: 250ms
  cooldown: 2s
retry:
  readiness_timeout: 3s
  restart_backoff: 4s
`), 0o644))

	cfg, err := LoadConfig(path, nil)

	require.NoError(t, err)
	require.Equal(t, 750*time.Millisecond, cfg.Traffic.Warmup)
	require.Equal(t, 250*time.Millisecond, cfg.Traffic.Window)
	require.Equal(t, 2*time.Second, cfg.Traffic.Cooldown)
	require.Equal(t, 3*time.Second, cfg.Retry.ReadinessTimeout)
	require.Equal(t, 4*time.Second, cfg.Retry.RestartBackoff)
}

func TestConfigDerivesBenchInputs(t *testing.T) {
	cfg := Config{
		Version: configVersion,
		Status:  StatusConfig{Listen: "127.0.0.1:19091"},
		Target: TargetConfig{
			APIAddrs:        []string{"http://wk-node1:5001"},
			GatewayTCPAddrs: []string{"wk-node1:5100"},
		},
		Identity: IdentityConfig{UIDPrefix: "sim-u", DevicePrefix: "sim-d", ClientMsgPrefix: "sim-msg", TokenMode: "none"},
		Online: OnlineConfig{
			TotalUsers:  20,
			ConnectRate: model.Rate{PerSecond: 10},
			Heartbeat:   model.HeartbeatConfig{Enabled: true, Interval: 30 * time.Second, Timeout: 5 * time.Second},
		},
		Profiles: ProfilesConfig{PersonChannels: 5, GroupChannels: 2, GroupMembers: 10},
		Traffic: TrafficConfig{
			PayloadSizeBytes:     128,
			PersonRatePerChannel: model.Rate{PerSecond: 0.2},
			GroupRatePerChannel:  model.Rate{PerSecond: 0.3},
			Concurrency:          16,
			VerifyRecv:           "sampled",
			Warmup:               mustDuration(t, "2s"),
			Window:               mustDuration(t, "10s"),
			Cooldown:             mustDuration(t, "1s"),
		},
		Retry: RetryConfig{ReadinessTimeout: mustDuration(t, "2m"), RestartBackoff: mustDuration(t, "5s")},
	}

	inputs, err := cfg.BuildBenchInputs("dev-sim-1")

	require.NoError(t, err)
	require.Equal(t, []string{"http://wk-node1:5001"}, inputs.Target.API.Addrs)
	require.True(t, inputs.Target.BenchAPI.Enabled)
	require.Equal(t, []string{"wk-node1:5100"}, inputs.Target.Gateway.TCP.Addrs)
	require.Len(t, inputs.Workers, 1)
	require.Equal(t, "wk-sim", inputs.Workers[0].ID)
	require.Equal(t, "dev-sim-1", inputs.Scenario.Run.ID)
	require.Equal(t, 24*time.Hour, inputs.Scenario.Run.Duration)
	require.Equal(t, 2*time.Second, inputs.Scenario.Run.Warmup)
	require.Equal(t, 20, inputs.Scenario.Online.TotalUsers)
	require.Equal(t, "round_robin", inputs.Scenario.Online.GatewayBalance)
	require.Equal(t, 30*time.Second, inputs.Scenario.Online.Heartbeat.Interval)
	require.Len(t, inputs.Scenario.Channels.Profiles, 2)
	require.Equal(t, "sim-person", inputs.Scenario.Channels.Profiles[0].Name)
	require.Equal(t, model.ChannelTypePerson, inputs.Scenario.Channels.Profiles[0].ChannelType)
	require.Equal(t, 5, inputs.Scenario.Channels.Profiles[0].Count)
	require.Equal(t, "sim-group", inputs.Scenario.Channels.Profiles[1].Name)
	require.Equal(t, 2, inputs.Scenario.Channels.Profiles[1].Count)
	require.Equal(t, 10, inputs.Scenario.Channels.Profiles[1].Members.Count)
	require.Len(t, inputs.Scenario.Messages.Traffic, 2)
	require.Equal(t, "sim-person-send", inputs.Scenario.Messages.Traffic[0].Name)
	require.Equal(t, 0.2, inputs.Scenario.Messages.Traffic[0].RatePerChannel.PerSecond)
	require.Equal(t, 16, inputs.Scenario.Messages.Traffic[0].Concurrency)
	require.Equal(t, "sim-group-send", inputs.Scenario.Messages.Traffic[1].Name)
	require.Equal(t, 0.3, inputs.Scenario.Messages.Traffic[1].RatePerChannel.PerSecond)
	require.Equal(t, 16, inputs.Scenario.Messages.Traffic[1].Concurrency)
}

func mustDuration(t *testing.T, raw string) time.Duration {
	t.Helper()
	d, err := time.ParseDuration(raw)
	require.NoError(t, err)
	return d
}
