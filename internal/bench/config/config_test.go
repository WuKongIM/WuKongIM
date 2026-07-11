package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoadScenarioRequiresBenchAPIByTarget(t *testing.T) {
	target := Target{BenchAPI: BenchAPIConfig{Enabled: false}}
	scenario := Scenario{Version: "wkbench/v1", Run: RunConfig{ID: "bench-run"}}
	err := ValidateTargetScenario(target, scenario)
	require.ErrorContains(t, err, "bench_api.enabled")
}

func TestLoadTargetSpecShape(t *testing.T) {
	path := writeTempYAML(t, `
name: prod-like-3node
api:
  addrs: [http://127.0.0.1:5001]
gateway:
  tcp:
    addrs: [127.0.0.1:5100]
metrics:
  enabled: true
  addrs: [http://127.0.0.1:5001/metrics]
bench_api:
  enabled: true
`)

	target, err := LoadTarget(path)
	require.NoError(t, err)
	require.Equal(t, "prod-like-3node", target.Name)
	require.Equal(t, []string{"http://127.0.0.1:5001"}, target.API.Addrs)
	require.Equal(t, []string{"127.0.0.1:5100"}, target.Gateway.TCP.Addrs)
	require.True(t, target.Metrics.Enabled)
	require.Equal(t, []string{"http://127.0.0.1:5001/metrics"}, target.Metrics.Addrs)
	require.True(t, target.BenchAPI.Enabled)
}

func TestLoadTargetRejectsOldFlatKey(t *testing.T) {
	path := writeTempYAML(t, `
api_addrs: [http://127.0.0.1:5001]
bench_api:
  enabled: true
`)

	_, err := LoadTarget(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "api_addrs")
}

func TestLoadWorkerSetSpecShapeExpandsEnv(t *testing.T) {
	t.Setenv("WK_BENCH_WORKER_TOKEN", "worker-secret")
	path := writeTempYAML(t, `
workers:
  - id: bench-a
    addr: http://127.0.0.1:19090
    weight: 1
    control_token: ${WK_BENCH_WORKER_TOKEN}
    tags: [zone-a]
`)

	workers, err := LoadWorkerSet(path)
	require.NoError(t, err)
	require.Len(t, workers.Workers, 1)
	require.Equal(t, "bench-a", workers.Workers[0].ID)
	require.Equal(t, "http://127.0.0.1:19090", workers.Workers[0].Addr)
	require.Equal(t, 1.0, workers.Workers[0].Weight)
	require.Equal(t, "worker-secret", workers.Workers[0].ControlToken)
	require.Equal(t, []string{"zone-a"}, workers.Workers[0].Tags)
}

func TestLoadWorkerSetDecodesStrictClientProfile(t *testing.T) {
	path := writeTempYAML(t, `
workers:
  - id: bench-a
    addr: http://127.0.0.1:19090
    weight: 1
    insecure_control: true
    client:
      send_queue_capacity: 16
      max_inflight: 1
      read_buffer_size: 1024
      frame_buffer_size: 4
`)

	workers, err := LoadWorkerSet(path)
	require.NoError(t, err)
	require.NotNil(t, workers.Workers[0].Client)
	require.Equal(t, 16, workers.Workers[0].Client.SendQueueCapacity)
	require.Equal(t, 1, workers.Workers[0].Client.MaxInflight)
	require.Equal(t, 1024, workers.Workers[0].Client.ReadBufferSize)
	require.Equal(t, 4, workers.Workers[0].Client.FrameBufferSize)
}

func TestLoadWorkerSetRejectsUnknownClientProfileKey(t *testing.T) {
	path := writeTempYAML(t, `
workers:
  - id: bench-a
    addr: http://127.0.0.1:19090
    weight: 1
    insecure_control: true
    client:
      send_queue_capacity: 16
      max_inflight: 1
      read_buffer_size: 1024
      frame_buffer_size: 4
      receive_queue_capacity: 99
`)

	_, err := LoadWorkerSet(path)
	require.ErrorContains(t, err, "receive_queue_capacity")
}

func TestLoadWorkerSetDecodesStrictTCPSourcePool(t *testing.T) {
	path := writeTempYAML(t, `
workers:
  - id: bench-a
    addr: http://127.0.0.1:19090
    weight: 1
    insecure_control: true
    tcp_source:
      ipv4_addrs: [127.0.0.1, 192.168.3.57]
      port_min: 1024
      port_max: 65535
`)

	workers, err := LoadWorkerSet(path)
	require.NoError(t, err)
	require.NotNil(t, workers.Workers[0].TCPSource)
	require.Equal(t, []string{"127.0.0.1", "192.168.3.57"}, workers.Workers[0].TCPSource.IPv4Addrs)
	require.Equal(t, 1024, workers.Workers[0].TCPSource.PortMin)
	require.Equal(t, 65535, workers.Workers[0].TCPSource.PortMax)
}

func TestLoadWorkerSetRejectsUnknownTCPSourcePoolKey(t *testing.T) {
	path := writeTempYAML(t, `
workers:
  - id: bench-a
    addr: http://127.0.0.1:19090
    weight: 1
    insecure_control: true
    tcp_source:
      ipv4_addrs: [127.0.0.1]
      port_min: 1024
      port_max: 65535
      reuse: true
`)

	_, err := LoadWorkerSet(path)
	require.ErrorContains(t, err, "reuse")
}

func TestValidateStaticConfigRequiresTargetAddresses(t *testing.T) {
	target := Target{BenchAPI: BenchAPIConfig{Enabled: true}}
	workers := WorkerSet{Workers: []Worker{{ID: "w1", Addr: "http://127.0.0.1:19090", Weight: 1, ControlToken: "secret"}}}

	err := ValidateStaticConfig(target, workers)

	require.ErrorContains(t, err, "target.api.addrs")
}

func TestValidateStaticConfigRequiresGatewayTCPAddrs(t *testing.T) {
	target := Target{API: TargetAPIConfig{Addrs: []string{"http://127.0.0.1:5001"}}, BenchAPI: BenchAPIConfig{Enabled: true}}
	workers := WorkerSet{Workers: []Worker{{ID: "w1", Addr: "http://127.0.0.1:19090", Weight: 1, ControlToken: "secret"}}}

	err := ValidateStaticConfig(target, workers)

	require.ErrorContains(t, err, "target.gateway.tcp.addrs")
}

func TestValidateStaticConfigRequiresWorkerAddr(t *testing.T) {
	target := validStaticTarget()
	workers := WorkerSet{Workers: []Worker{{ID: "w1", Weight: 1, ControlToken: "secret"}}}

	err := ValidateStaticConfig(target, workers)

	require.ErrorContains(t, err, "workers[0].addr")
}

func TestValidateStaticConfigRequiresWorkerTokenUnlessInsecure(t *testing.T) {
	target := validStaticTarget()
	workers := WorkerSet{Workers: []Worker{{ID: "w1", Addr: "http://127.0.0.1:19090", Weight: 1}}}

	err := ValidateStaticConfig(target, workers)

	require.ErrorContains(t, err, "control_token")
}

func TestValidateStaticConfigAllowsInsecureWorkerWithoutToken(t *testing.T) {
	target := validStaticTarget()
	workers := WorkerSet{Workers: []Worker{{ID: "w1", Addr: "http://127.0.0.1:19090", Weight: 1, InsecureControl: true}}}

	err := ValidateStaticConfig(target, workers)

	require.NoError(t, err)
}

func TestValidateStaticConfigAllowsOmittedWorkerClientProfile(t *testing.T) {
	target := validStaticTarget()
	workers := WorkerSet{Workers: []Worker{{ID: "w1", Addr: "http://127.0.0.1:19090", Weight: 1, InsecureControl: true}}}

	err := ValidateStaticConfig(target, workers)

	require.NoError(t, err)
}

func TestValidateStaticConfigRequiresCompletePositiveWorkerClientProfile(t *testing.T) {
	valid := WorkerClientConfig{
		SendQueueCapacity: 16,
		MaxInflight:       1,
		ReadBufferSize:    1024,
		FrameBufferSize:   4,
	}
	tests := []struct {
		name string
		edit func(*WorkerClientConfig)
		want string
	}{
		{name: "send queue", edit: func(cfg *WorkerClientConfig) { cfg.SendQueueCapacity = 0 }, want: "client.send_queue_capacity"},
		{name: "max inflight", edit: func(cfg *WorkerClientConfig) { cfg.MaxInflight = -1 }, want: "client.max_inflight"},
		{name: "read buffer", edit: func(cfg *WorkerClientConfig) { cfg.ReadBufferSize = 0 }, want: "client.read_buffer_size"},
		{name: "frame buffer", edit: func(cfg *WorkerClientConfig) { cfg.FrameBufferSize = 0 }, want: "client.frame_buffer_size"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := valid

			tt.edit(&profile)
			err := ValidateStaticConfig(validStaticTarget(), WorkerSet{Workers: []Worker{{
				ID: "w1", Addr: "http://127.0.0.1:19090", Weight: 1, InsecureControl: true, Client: &profile,
			}}})

			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestValidateStaticConfigAllowsOmittedWorkerTCPSourcePool(t *testing.T) {
	workers := WorkerSet{Workers: []Worker{{
		ID: "w1", Addr: "http://127.0.0.1:19090", Weight: 1, InsecureControl: true,
	}}}

	require.NoError(t, ValidateStaticConfig(validStaticTarget(), workers))
}

func TestValidateStaticConfigRequiresCompleteValidWorkerTCPSourcePool(t *testing.T) {
	valid := TCPSourceConfig{IPv4Addrs: []string{"127.0.0.1", "192.168.3.57"}, PortMin: 1024, PortMax: 65535}
	tests := []struct {
		name string
		edit func(*TCPSourceConfig)
		want string
	}{
		{name: "empty addresses", edit: func(cfg *TCPSourceConfig) { cfg.IPv4Addrs = nil }, want: "tcp_source.ipv4_addrs"},
		{name: "invalid address", edit: func(cfg *TCPSourceConfig) { cfg.IPv4Addrs = []string{"not-an-ip"} }, want: "valid IPv4"},
		{name: "ipv6 address", edit: func(cfg *TCPSourceConfig) { cfg.IPv4Addrs = []string{"::1"} }, want: "valid IPv4"},
		{name: "unspecified address", edit: func(cfg *TCPSourceConfig) { cfg.IPv4Addrs = []string{"0.0.0.0"} }, want: "must not be unspecified"},
		{name: "duplicate address", edit: func(cfg *TCPSourceConfig) { cfg.IPv4Addrs = []string{"127.0.0.1", "127.0.0.1"} }, want: "must be unique"},
		{name: "low port", edit: func(cfg *TCPSourceConfig) { cfg.PortMin = 1023 }, want: "tcp_source.port_min"},
		{name: "reversed ports", edit: func(cfg *TCPSourceConfig) { cfg.PortMin, cfg.PortMax = 2000, 1999 }, want: "must not exceed"},
		{name: "high port", edit: func(cfg *TCPSourceConfig) { cfg.PortMax = 65536 }, want: "tcp_source.port_max"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := valid
			pool.IPv4Addrs = append([]string(nil), valid.IPv4Addrs...)
			tt.edit(&pool)

			err := ValidateStaticConfig(validStaticTarget(), WorkerSet{Workers: []Worker{{
				ID: "w1", Addr: "http://127.0.0.1:19090", Weight: 1, InsecureControl: true, TCPSource: &pool,
			}}})

			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestLoadScenarioSpecShape(t *testing.T) {
	path := writeTempYAML(t, `
version: wkbench/v1
run:
  id: bench-run
  duration: 10m
  warmup: 30s
  cooldown: 15s
  random_seed: 42
  fail_fast: true
  report_dir: ./reports
limits:
  fail_on_soft: true
  hard:
    max_worker_failed: 1
    max_connect_error_rate: 0.01
    max_sendack_error_rate: 0.02
    max_recv_verify_error_rate: 0.03
  soft:
    max_sendack_p99: 200ms
    max_recv_p99: 300ms
prepare:
  concurrency: 8
  rate_limit: 100/s
  retry:
    max_attempts: 3
    backoff: 100ms
identity:
  uid_prefix: bench-u
  device_prefix: bench-d
  client_msg_prefix: bench-m
  token:
    mode: bench_api
online:
  total_users: 100
  connect_rate: 25.5/s
  gateway_balance: round_robin
  reconnect:
    enabled: true
    backoff: 1s
    max_attempts: 5
  heartbeat:
    enabled: true
    interval: 30s
    timeout: 5s
channels:
  profiles:
    - name: group-hot
      channel_type: group
      count: 10
      participants:
        pick: random
      members:
        count: 100
        pick: random
        overlap: allowed
      online:
        sender_ratio: 0.5
        recipient_ratio: 0.75
        member_ratio: 0.8
      shard:
        mode: hash
      prepare:
        subscribers_batch_size: 500
cleanup:
  enabled: false
  strategy: keep_data
messages:
  payload:
    size_bytes: 256
    mode: fixed
  traffic:
    - name: hot-group-send
      channel_ref: group-hot
      rate_per_channel: 5/s
      ack_timeout: 10s
      recv_timeout: 11s
      sender_pick: random_online
      recv_ack: true
      verify:
        recv:
          mode: sample
          sample_size_per_message: 3
`)

	scenario, err := LoadScenario(path)
	require.NoError(t, err)
	require.Equal(t, "wkbench/v1", scenario.Version)
	require.Equal(t, "bench-run", scenario.Run.ID)
	require.Equal(t, 10*time.Minute, scenario.Run.Duration)
	require.Equal(t, 30*time.Second, scenario.Run.Warmup)
	require.Equal(t, 15*time.Second, scenario.Run.Cooldown)
	require.Equal(t, int64(42), scenario.Run.RandomSeed)
	require.True(t, scenario.Limits.FailOnSoft)
	require.Equal(t, 1, scenario.Limits.Hard.MaxWorkerFailed)
	require.Equal(t, 0.01, scenario.Limits.Hard.MaxConnectErrorRate)
	require.Equal(t, 200*time.Millisecond, scenario.Limits.Soft.MaxSendackP99)
	require.Equal(t, 100.0, scenario.Prepare.RateLimit.PerSecond)
	require.Equal(t, "bench-u", scenario.Identity.UIDPrefix)
	require.Equal(t, "bench_api", scenario.Identity.Token.Mode)
	require.Equal(t, 100, scenario.Online.TotalUsers)
	require.Equal(t, 25.5, scenario.Online.ConnectRate.PerSecond)
	require.True(t, scenario.Online.Reconnect.Enabled)
	require.Equal(t, time.Second, scenario.Online.Reconnect.Backoff)
	require.Equal(t, "allowed", scenario.Channels.Profiles[0].Members.Overlap)
	require.False(t, scenario.Cleanup.Enabled)
	require.Equal(t, "keep_data", scenario.Cleanup.Strategy)
	require.Equal(t, 30*time.Second, scenario.Online.Heartbeat.Interval)
	require.Len(t, scenario.Channels.Profiles, 1)
	require.Equal(t, "group-hot", scenario.Channels.Profiles[0].Name)
	require.Equal(t, 500, scenario.Channels.Profiles[0].Prepare.SubscribersBatchSize)
	require.Equal(t, 256, scenario.Messages.Payload.SizeBytes)
	require.Len(t, scenario.Messages.Traffic, 1)
	require.Equal(t, 5.0, scenario.Messages.Traffic[0].RatePerChannel.PerSecond)
	require.Equal(t, 10*time.Second, scenario.Messages.Traffic[0].AckTimeout)
	require.Equal(t, 11*time.Second, scenario.Messages.Traffic[0].RecvTimeout)
	require.Equal(t, 3, scenario.Messages.Traffic[0].Verify.Recv.SampleSizePerMessage)
}

func TestLoadScenarioDefaultsOmittedHardErrorRatesToDisabledSentinel(t *testing.T) {
	path := writeTempYAML(t, `
version: wkbench/v1
run:
  id: bench-run
limits:
  hard:
    max_sendack_error_rate: 0
channels:
  profiles: []
messages:
  traffic: []
`)

	scenario, err := LoadScenario(path)

	require.NoError(t, err)
	require.Equal(t, 0.0, scenario.Limits.Hard.MaxSendackErrorRate)
	require.Equal(t, -1.0, scenario.Limits.Hard.MaxConnectErrorRate)
	require.Equal(t, -1.0, scenario.Limits.Hard.MaxRecvVerifyErrorRate)
}

func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func validStaticTarget() Target {
	return Target{
		API:      TargetAPIConfig{Addrs: []string{"http://127.0.0.1:5001"}},
		Gateway:  TargetGatewayConfig{TCP: TargetGatewayTCPConfig{Addrs: []string{"127.0.0.1:5100"}}},
		BenchAPI: BenchAPIConfig{Enabled: true},
	}
}
