package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/capacity"
	"github.com/WuKongIM/WuKongIM/internal/bench/messageevent"
)

func TestWorkerCommandRequiresControlToken(t *testing.T) {
	t.Setenv("WK_BENCH_WORKER_TOKEN", "")
	var stderr bytes.Buffer

	code := runWithStderr([]string{"worker", "--listen", "127.0.0.1:0"}, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "--control-token is required") {
		t.Fatalf("expected control token error, got %q", stderr.String())
	}
}

func TestRootCommandHelpListsSubcommands(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"--help"}, &stderr)

	if code != 0 {
		t.Fatalf("expected help exit code 0, got %d stderr %q", code, stderr.String())
	}
	for _, want := range []string{"Usage:", "run", "worker", "validate", "doctor", "dev-sim", "capacity", "metrics"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected root help to contain %q, got %q", want, stderr.String())
		}
	}
}

func TestRootCommandUsesWkbenchName(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"--help"}, &stderr)

	if code != 0 {
		t.Fatalf("expected help exit code 0, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "wkbench") {
		t.Fatalf("expected root help to use wkbench name, got %q", stderr.String())
	}
}

func TestCapacityCommandHelpListsSubcommands(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"capacity", "--help"}, &stderr)

	if code != 0 {
		t.Fatalf("expected help exit code 0, got %d stderr %q", code, stderr.String())
	}
	for _, want := range []string{"Usage:", "send", "hot-channel", "activate-channels", "message-event"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected capacity help to contain %q, got %q", want, stderr.String())
		}
	}
}

func TestWorkerCommandAllowsExplicitInsecureControl(t *testing.T) {
	t.Setenv("WK_BENCH_WORKER_TOKEN", "")
	var stderr bytes.Buffer

	cfg, code := parseWorkerConfig([]string{"--listen", "127.0.0.1:0", "--insecure-control=true"}, &stderr)

	if code != 0 {
		t.Fatalf("expected parse success, got code %d and stderr %q", code, stderr.String())
	}
	if !cfg.server.InsecureControl {
		t.Fatalf("expected insecure control to be enabled")
	}
}

func TestWorkerCommandInsecureControlIgnoresEnvToken(t *testing.T) {
	t.Setenv("WK_BENCH_WORKER_TOKEN", "from-env")
	var stderr bytes.Buffer

	cfg, code := parseWorkerConfig([]string{"--listen", "127.0.0.1:0", "--insecure-control=true"}, &stderr)

	if code != 0 {
		t.Fatalf("expected parse success, got code %d and stderr %q", code, stderr.String())
	}
	if !cfg.server.InsecureControl {
		t.Fatalf("expected insecure control to be enabled")
	}
	if cfg.server.ControlToken != "" {
		t.Fatalf("expected insecure control to clear effective token, got %q", cfg.server.ControlToken)
	}
}

func TestRunCapacityRequiresSubcommand(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"capacity"}, &stderr)

	if code != exitConfig {
		t.Fatalf("expected config exit code, got %d", code)
	}
	for _, want := range []string{"Usage:", "send", "hot-channel", "activate-channels"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected capacity help to contain %q, got %q", want, stderr.String())
		}
	}
}

func TestRunCapacitySendRequiresAPI(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"capacity", "send"}, &stderr)

	if code != exitConfig {
		t.Fatalf("expected config exit code, got %d", code)
	}
	if !strings.Contains(stderr.String(), "--api is required") {
		t.Fatalf("expected api error, got %q", stderr.String())
	}
}

func TestRunCapacityHotChannelRequiresAPI(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"capacity", "hot-channel"}, &stderr)

	if code != exitConfig {
		t.Fatalf("expected config exit code, got %d", code)
	}
	if !strings.Contains(stderr.String(), "--api is required") {
		t.Fatalf("expected api error, got %q", stderr.String())
	}
}

func TestRunCapacityActivateChannelsRequiresAPI(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"capacity", "activate-channels"}, &stderr)

	if code != exitConfig {
		t.Fatalf("expected config exit code, got %d", code)
	}
	if !strings.Contains(stderr.String(), "--api is required") {
		t.Fatalf("expected api error, got %q", stderr.String())
	}
}

func TestRunCapacityMessageEventRequiresAPI(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"capacity", "message-event"}, &stderr)

	if code != exitConfig {
		t.Fatalf("expected config exit code, got %d", code)
	}
	if !strings.Contains(stderr.String(), "--api is required") {
		t.Fatalf("expected api error, got %q", stderr.String())
	}
}

func TestParseCapacityHotChannelConfig(t *testing.T) {
	var stderr bytes.Buffer

	cfg, code := parseCapacityHotChannelConfig([]string{
		"--api", "http://127.0.0.1:5001",
		"--gateway", "127.0.0.1:5100",
		"--senders", "32",
		"--start-qps", "1000",
		"--max-qps", "2000",
	}, &stderr)

	if code != 0 {
		t.Fatalf("expected parse success, got code %d stderr %q", code, stderr.String())
	}
	if got := strings.Join(cfg.APIAddrs, ","); got != "http://127.0.0.1:5001" {
		t.Fatalf("api addrs = %q", got)
	}
	if got := strings.Join(cfg.GatewayTCPAddrs, ","); got != "127.0.0.1:5100" {
		t.Fatalf("gateway addrs = %q", got)
	}
	if cfg.Senders != 32 {
		t.Fatalf("senders = %d, want 32", cfg.Senders)
	}
	if cfg.StartQPS != 1000 || cfg.MaxQPS != 2000 {
		t.Fatalf("qps range = %v..%v, want 1000..2000", cfg.StartQPS, cfg.MaxQPS)
	}
}

func TestParseCapacityMessageEventConfig(t *testing.T) {
	var stderr bytes.Buffer

	cfg, code := parseCapacityMessageEventConfig([]string{
		"--api", "http://127.0.0.1:5001,http://127.0.0.1:5002",
		"--run-id", "message-event-cli",
		"--channels", "100",
		"--streams-per-channel", "3",
		"--lanes-per-stream", "4",
		"--deltas-per-lane", "5",
		"--payload-bytes", "128",
		"--concurrency", "64",
		"--request-timeout", "3s",
		"--report-dir", "/tmp/message-event-report",
		"--warm-channels",
		"--warm-runtime",
	}, &stderr)

	if code != 0 {
		t.Fatalf("expected parse success, got code %d stderr %q", code, stderr.String())
	}
	if got := strings.Join(cfg.APIAddrs, ","); got != "http://127.0.0.1:5001,http://127.0.0.1:5002" {
		t.Fatalf("api addrs = %q", got)
	}
	if cfg.RunID != "message-event-cli" {
		t.Fatalf("run id = %q", cfg.RunID)
	}
	if cfg.Channels != 100 || cfg.StreamsPerChannel != 3 || cfg.LanesPerStream != 4 || cfg.DeltasPerLane != 5 {
		t.Fatalf("shape = %+v", cfg)
	}
	if cfg.PayloadBytes != 128 || cfg.Concurrency != 64 || cfg.RequestTimeout != 3*time.Second {
		t.Fatalf("payload/concurrency/timeout = %d/%d/%s", cfg.PayloadBytes, cfg.Concurrency, cfg.RequestTimeout)
	}
	if cfg.ReportDir != "/tmp/message-event-report" {
		t.Fatalf("report dir = %q", cfg.ReportDir)
	}
	if !cfg.WarmChannels {
		t.Fatalf("warm channels = false, want true")
	}
	if !cfg.WarmRuntime {
		t.Fatalf("warm runtime = false, want true")
	}
	if shape := cfg.Shape(); shape != (messageevent.Shape{Streams: 300, DeltaEvents: 6000, FinishEvents: 300, ExpectedDurableEvents: 1500, ExpectedFinishProposals: 300}) {
		t.Fatalf("shape = %+v", shape)
	}
}

func TestParseCapacityActivateChannelsConfig(t *testing.T) {
	var stderr bytes.Buffer

	cfg, code := parseCapacityActivateChannelsConfig([]string{
		"--api", "http://127.0.0.1:5001,http://127.0.0.1:5002",
		"--gateway", "127.0.0.1:5100",
		"--bench-token", "secret",
		"--run-id", "activate-test",
		"--channels", "1234",
		"--users", "2345",
		"--group-members", "12",
		"--prepare-rate", "321",
		"--connect-rate", "123",
		"--activation-concurrency", "345",
		"--activation-window", "3s",
		"--hold", "4s",
		"--hold-probe-interval", "500ms",
		"--probe-batch-size", "111",
		"--stable-p99", "250ms",
		"--max-sendack-error-rate", "0.01",
		"--max-connect-error-rate", "0.02",
		"--evict-after=true",
		"--report-dir", "/tmp/activate-report",
	}, &stderr)

	if code != 0 {
		t.Fatalf("expected parse success, got code %d stderr %q", code, stderr.String())
	}
	if got := strings.Join(cfg.APIAddrs, ","); got != "http://127.0.0.1:5001,http://127.0.0.1:5002" {
		t.Fatalf("api addrs = %q", got)
	}
	if got := strings.Join(cfg.GatewayTCPAddrs, ","); got != "127.0.0.1:5100" {
		t.Fatalf("gateway addrs = %q", got)
	}
	if cfg.BenchToken != "secret" || cfg.RunID != "activate-test" {
		t.Fatalf("token/run id = %q/%q", cfg.BenchToken, cfg.RunID)
	}
	if cfg.Channels != 1234 || cfg.Users != 2345 || cfg.GroupMembers != 12 {
		t.Fatalf("shape = channels %d users %d members %d", cfg.Channels, cfg.Users, cfg.GroupMembers)
	}
	if cfg.PrepareRatePerSecond != 321 || cfg.ConnectRatePerSecond != 123 {
		t.Fatalf("rates = prepare %.3f connect %.3f", cfg.PrepareRatePerSecond, cfg.ConnectRatePerSecond)
	}
	if cfg.ActivationConcurrency != 345 || cfg.ActivationWindow != 3*time.Second {
		t.Fatalf("activation = concurrency %d window %s", cfg.ActivationConcurrency, cfg.ActivationWindow)
	}
	if cfg.Hold != 4*time.Second || cfg.HoldProbeInterval != 500*time.Millisecond || cfg.ProbeBatchSize != 111 {
		t.Fatalf("hold/probe = %s %s %d", cfg.Hold, cfg.HoldProbeInterval, cfg.ProbeBatchSize)
	}
	if cfg.StableP99 != 250*time.Millisecond || cfg.MaxSendackErrorRate != 0.01 || cfg.MaxConnectErrorRate != 0.02 {
		t.Fatalf("limits = %s %.3f %.3f", cfg.StableP99, cfg.MaxSendackErrorRate, cfg.MaxConnectErrorRate)
	}
	if !cfg.EvictAfter || cfg.ReportDir != "/tmp/activate-report" {
		t.Fatalf("evict/report = %t %q", cfg.EvictAfter, cfg.ReportDir)
	}
}

func TestRunCapacityActivateChannelsUsesRunnerAndWritesReport(t *testing.T) {
	origDiscover := discoverActivateChannelsTarget
	origNewRunner := newActivateChannelsRunner
	defer func() {
		discoverActivateChannelsTarget = origDiscover
		newActivateChannelsRunner = origNewRunner
	}()
	var discoveredCfg capacity.ActivateChannelsConfig
	var runnerCfg capacity.ActivateChannelsConfig
	discoverActivateChannelsTarget = func(_ context.Context, cfg capacity.ActivateChannelsConfig) (capacity.DiscoveredTarget, error) {
		discoveredCfg = cfg
		return capacity.DiscoveredTarget{}, nil
	}
	newActivateChannelsRunner = func(cfg capacity.ActivateChannelsConfig, discovered capacity.DiscoveredTarget) activateChannelsRunner {
		runnerCfg = cfg
		return fakeActivateChannelsRunner{result: capacity.ActivateChannelsResult{
			Status: capacity.StatusPassed,
			Config: capacity.ActivateChannelsReportConfig{
				RunID:                 cfg.RunID,
				Channels:              cfg.Channels,
				Users:                 cfg.Users,
				GroupMembers:          cfg.GroupMembers,
				PrepareRatePerSecond:  cfg.PrepareRatePerSecond,
				ConnectRatePerSecond:  cfg.ConnectRatePerSecond,
				ActivationConcurrency: cfg.ActivationConcurrency,
				ActivationWindow:      cfg.ActivationWindow,
				Hold:                  cfg.Hold,
				ProbeBatchSize:        cfg.ProbeBatchSize,
				EvictAfter:            cfg.EvictAfter,
			},
			Evaluation: capacity.ActivateChannelsEvaluation{
				Passed:            true,
				ActivationSuccess: uint64(cfg.Channels),
				ActiveLeaderTotal: cfg.Channels,
			},
			ReportDir: cfg.ReportDir,
		}}
	}
	reportDir := t.TempDir()
	var stderr bytes.Buffer

	code := runWithStderr([]string{
		"capacity", "activate-channels",
		"--api", "http://127.0.0.1:5001",
		"--gateway", "127.0.0.1:5100",
		"--run-id", "activate-cli",
		"--channels", "4",
		"--users", "8",
		"--group-members", "2",
		"--activation-window", "10ms",
		"--hold", "0s",
		"--report-dir", reportDir,
	}, &stderr)

	if code != 0 {
		t.Fatalf("expected success, got code %d stderr %q", code, stderr.String())
	}
	if discoveredCfg.RunID != "activate-cli" || runnerCfg.Channels != 4 {
		t.Fatalf("unexpected cfgs: discovered=%+v runner=%+v", discoveredCfg, runnerCfg)
	}
	if !strings.Contains(stderr.String(), "wkbench activate-channels") {
		t.Fatalf("expected console summary, got %q", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(reportDir, "activation_report.json")); err != nil {
		t.Fatalf("expected activation report: %v", err)
	}
	if _, err := os.Stat(filepath.Join(reportDir, "summary.md")); err != nil {
		t.Fatalf("expected summary: %v", err)
	}
}

type fakeActivateChannelsRunner struct {
	result capacity.ActivateChannelsResult
	err    error
}

func (f fakeActivateChannelsRunner) Run(context.Context) (capacity.ActivateChannelsResult, error) {
	return f.result, f.err
}

func TestMetricsClassifyReportsGatewayPressureFromPrometheusSnapshots(t *testing.T) {
	dir := t.TempDir()
	before := filepath.Join(dir, "before.prom")
	after := filepath.Join(dir, "after.prom")
	if err := os.WriteFile(before, []byte(`
wukongim_gateway_async_send_queue_depth 0
wukongim_gateway_async_send_queue_capacity 100
wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket{protocol="wkproto",le="0.01"} 0
wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket{protocol="wkproto",le="0.05"} 0
wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket{protocol="wkproto",le="+Inf"} 0
wukongim_channelv2_reactor_mailbox_depth{reactor_id="0",priority="normal"} 0
wukongim_channelv2_worker_queue_depth{pool="store_append"} 0
wukongim_channelv2_worker_inflight{pool="store_append"} 0
wukongim_channelv2_worker_inflight_peak{pool="store_append"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_apply",result="ok",le="0.01"} 0
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_apply",result="ok",le="+Inf"} 0
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="0.01"} 0
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="1"} 0
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="10"} 0
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="+Inf"} 0
`), 0o600); err != nil {
		t.Fatalf("write before: %v", err)
	}
	if err := os.WriteFile(after, []byte(`
wukongim_gateway_async_send_queue_depth 70
wukongim_gateway_async_send_queue_capacity 100
wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket{protocol="wkproto",le="0.01"} 10
wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket{protocol="wkproto",le="0.05"} 100
wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket{protocol="wkproto",le="+Inf"} 100
wukongim_channelv2_reactor_mailbox_depth{reactor_id="0",priority="normal"} 0
wukongim_channelv2_worker_queue_depth{pool="store_append"} 0
wukongim_channelv2_worker_inflight{pool="store_append"} 128
wukongim_channelv2_worker_inflight_peak{pool="store_append"} 256
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_apply",result="ok",le="0.01"} 100
wukongim_channelv2_append_stage_duration_seconds_bucket{stage="meta_apply",result="ok",le="+Inf"} 100
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="0.01"} 100
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="1"} 100
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="10"} 100
wukongim_storage_commit_request_duration_seconds_bucket{store="message",lane="append",result="ok",le="+Inf"} 100
`), 0o600); err != nil {
		t.Fatalf("write after: %v", err)
	}
	var stderr bytes.Buffer

	code := runWithStderr([]string{"metrics", "classify", "--before", before, "--after", after}, &stderr)

	if code != 0 {
		t.Fatalf("expected success, got code %d and stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "classification: gateway_dispatch") {
		t.Fatalf("expected gateway classification, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "gateway_queue_ratio: 0.700") {
		t.Fatalf("expected queue ratio in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_meta_apply_p99_seconds:") {
		t.Fatalf("expected channel stage metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_runtime_append_wait_p99_seconds:") {
		t.Fatalf("expected channel runtime append wait metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_append_batch_wait_p99_seconds:") {
		t.Fatalf("expected channel append batch wait metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_append_store_wait_p99_seconds:") {
		t.Fatalf("expected channel append store wait metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "storage_commit_request_p99_seconds:") {
		t.Fatalf("expected storage commit request metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "storage_commit_request_over_10s_count:") {
		t.Fatalf("expected storage commit request tail count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_worker_inflight{pool=\"store_append\"}: 128") {
		t.Fatalf("expected channel worker inflight in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_worker_inflight_peak{pool=\"store_append\"}: 256") {
		t.Fatalf("expected channel worker inflight peak in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "storage_commit_request_over_10s_count{lane=\"append\"}: 0") {
		t.Fatalf("expected storage commit request lane tail count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_append_post_store_commit_wait_p99_seconds:") {
		t.Fatalf("expected channel append post-store commit wait metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_append_quorum_follower_pull_wait_p99_seconds:") {
		t.Fatalf("expected channel append quorum follower pull wait metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_append_quorum_ack_offset_wait_p99_seconds:") {
		t.Fatalf("expected channel append quorum ack offset wait metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_append_quorum_hw_advance_wait_p99_seconds:") {
		t.Fatalf("expected channel append quorum HW advance wait metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_append_quorum_final_complete_p99_seconds:") {
		t.Fatalf("expected channel append quorum final complete metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_replication_follower_pull_hint_to_submit_p99_seconds:") {
		t.Fatalf("expected follower pull hint to submit metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_replication_follower_pull_rpc_p99_seconds:") {
		t.Fatalf("expected follower pull RPC metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_replication_follower_store_apply_p99_seconds:") {
		t.Fatalf("expected follower store apply metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_replication_follower_apply_to_ack_return_p99_seconds:") {
		t.Fatalf("expected follower apply to AckOffset return metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_meta_slot_read_p99_seconds:") {
		t.Fatalf("expected channel meta breakdown metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_meta_create_propose_p99_seconds:") {
		t.Fatalf("expected channel meta propose metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_meta_create_propose_forward_p99_seconds:") {
		t.Fatalf("expected channel meta propose forward metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_meta_create_slot_propose_wait_p99_seconds:") {
		t.Fatalf("expected channel meta Slot propose wait metric in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_meta_create_slot_raft_commit_wait_p99_seconds:") {
		t.Fatalf("expected channel meta Slot raft commit wait metric in output, got %q", stderr.String())
	}
}

func TestMetricsClassifyPromotesChannelRuntimeOutputKeys(t *testing.T) {
	dir := t.TempDir()
	before := filepath.Join(dir, "before.prom")
	after := filepath.Join(dir, "after.prom")
	if err := os.WriteFile(before, []byte(`
wukongim_channelv2_worker_inflight{pool="store_append"} 0
wukongim_channelv2_worker_inflight_peak{pool="store_append"} 0
wukongim_channelv2_worker_queue_depth{pool="store_append"} 0
`), 0o600); err != nil {
		t.Fatalf("write before: %v", err)
	}
	if err := os.WriteFile(after, []byte(`
wukongim_channelv2_worker_inflight{pool="store_append"} 128
wukongim_channelv2_worker_inflight_peak{pool="store_append"} 256
wukongim_channelv2_worker_queue_depth{pool="store_append"} 1
`), 0o600); err != nil {
		t.Fatalf("write after: %v", err)
	}
	var stderr bytes.Buffer

	code := runWithStderr([]string{"metrics", "classify", "--before", before, "--after", after}, &stderr)

	if code != 0 {
		t.Fatalf("expected success, got code %d and stderr %q", code, stderr.String())
	}
	output := stderr.String()
	for _, want := range []string{
		"classification: channel_append",
		"channel_worker_inflight{pool=\"store_append\"}: 128",
		"channel_worker_inflight_peak{pool=\"store_append\"}: 256",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected promoted channel key %q in output, got %q", want, output)
		}
	}
	for _, unwanted := range []string{
		"classification: channelv2_append",
		"channelv2_worker_inflight",
	} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("output should not expose legacy channelv2 report key %q, got %q", unwanted, output)
		}
	}
}

func TestMetricsClassifyReportsChannelRuntimePullBatchMetrics(t *testing.T) {
	dir := t.TempDir()
	before := filepath.Join(dir, "before.prom")
	after := filepath.Join(dir, "after.prom")
	beforeMetrics := `
wukongim_channel_pull_batch_items_bucket{result="ok",le="2"} 0
wukongim_channel_pull_batch_items_bucket{result="ok",le="4"} 0
wukongim_channel_pull_batch_items_bucket{result="ok",le="+Inf"} 0
wukongim_channel_pull_batch_records_bucket{result="ok",le="8"} 0
wukongim_channel_pull_batch_records_bucket{result="ok",le="16"} 0
wukongim_channel_pull_batch_records_bucket{result="ok",le="+Inf"} 0
wukongim_channel_pull_batch_payload_bytes_bucket{result="ok",le="256"} 0
wukongim_channel_pull_batch_payload_bytes_bucket{result="ok",le="512"} 0
wukongim_channel_pull_batch_payload_bytes_bucket{result="ok",le="+Inf"} 0
wukongim_channel_pull_batch_duration_seconds_bucket{stage="submit",result="ok",le="0.005"} 0
wukongim_channel_pull_batch_duration_seconds_bucket{stage="submit",result="ok",le="+Inf"} 0
wukongim_channel_pull_batch_duration_seconds_bucket{stage="await",result="ok",le="0.25"} 0
wukongim_channel_pull_batch_duration_seconds_bucket{stage="await",result="ok",le="+Inf"} 0
wukongim_channel_pull_batch_duration_seconds_bucket{stage="max_sequential_await",result="ok",le="0.1"} 0
wukongim_channel_pull_batch_duration_seconds_bucket{stage="max_sequential_await",result="ok",le="+Inf"} 0
wukongim_channel_pull_batch_duration_seconds_bucket{stage="total",result="ok",le="0.5"} 0
wukongim_channel_pull_batch_duration_seconds_bucket{stage="total",result="ok",le="+Inf"} 0
wukongim_runtime_pool_wait_duration_seconds_bucket{component="channel",pool="channelv2-rpc",queue="worker",priority="none",result="ok",le="0.1"} 0
wukongim_runtime_pool_wait_duration_seconds_bucket{component="channel",pool="channelv2-rpc",queue="worker",priority="none",result="ok",le="+Inf"} 0
wukongim_channel_leader_pull_stage_duration_seconds_bucket{stage="mailbox_wait",le="0.1"} 0
wukongim_channel_leader_pull_stage_duration_seconds_bucket{stage="mailbox_wait",le="+Inf"} 0
wukongim_channel_leader_pull_stage_duration_seconds_bucket{stage="ack_apply",le="0.005"} 0
wukongim_channel_leader_pull_stage_duration_seconds_bucket{stage="ack_apply",le="+Inf"} 0
wukongim_channel_leader_pull_stage_duration_seconds_bucket{stage="handler",le="0.25"} 0
wukongim_channel_leader_pull_stage_duration_seconds_bucket{stage="handler",le="+Inf"} 0
wukongim_channel_leader_pull_completed_waiters_bucket{le="1"} 0
wukongim_channel_leader_pull_completed_waiters_bucket{le="2"} 0
wukongim_channel_leader_pull_completed_waiters_bucket{le="+Inf"} 0
`
	if err := os.WriteFile(before, []byte(beforeMetrics), 0o600); err != nil {
		t.Fatalf("write before: %v", err)
	}
	if err := os.WriteFile(after, []byte(strings.ReplaceAll(beforeMetrics, "} 0", "} 10")), 0o600); err != nil {
		t.Fatalf("write after: %v", err)
	}
	var stderr bytes.Buffer

	code := runWithStderr([]string{"metrics", "classify", "--before", before, "--after", after}, &stderr)

	if code != 0 {
		t.Fatalf("expected success, got code %d and stderr %q", code, stderr.String())
	}
	for _, want := range []string{
		"channel_pull_batch_items_p50: 1.000",
		"channel_pull_batch_items_p99: 1.980",
		"channel_pull_batch_records_p50: 4.000",
		"channel_pull_batch_records_p99: 7.920",
		"channel_pull_batch_payload_bytes_p50: 128.000",
		"channel_pull_batch_payload_bytes_p99: 253.440",
		"channel_pull_batch_submit_p99_seconds: 0.004950",
		"channel_pull_batch_await_p99_seconds: 0.247500",
		"channel_pull_batch_max_sequential_await_p99_seconds: 0.099000",
		"channel_pull_batch_total_p99_seconds: 0.495000",
		"channel_worker_rpc_queue_wait_p99_seconds: 0.099000",
		"channel_leader_pull_mailbox_wait_p99_seconds: 0.099000",
		"channel_leader_pull_ack_apply_p99_seconds: 0.004950",
		"channel_leader_pull_handler_p99_seconds: 0.247500",
		"channel_leader_pull_completed_waiters_p50: 0.500",
		"channel_leader_pull_completed_waiters_p99: 0.990",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected PullBatch output %q, got %q", want, stderr.String())
		}
	}
}

func TestMetricsClassifyReportsMessageEventPressureFromPrometheusSnapshots(t *testing.T) {
	dir := t.TempDir()
	before := filepath.Join(dir, "before.prom")
	after := filepath.Join(dir, "after.prom")
	if err := os.WriteFile(before, []byte(`
wukongim_message_event_stream_cache_sessions 0
wukongim_message_event_stream_cache_open_lanes 0
wukongim_message_event_stream_cache_payload_bytes 0
wukongim_message_event_stream_cache_max_sessions 1024
wukongim_message_event_append_total{path="cache",event_type="stream.delta",result="ok"} 1
wukongim_message_event_append_total{path="finish_batch",event_type="stream.finish",result="cache_miss"} 0
wukongim_message_event_propose_total{path="finish_batch",result="ok"} 0
wukongim_message_event_append_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="finish_batch_build",le="0.01"} 0
wukongim_message_event_append_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="finish_batch_build",le="+Inf"} 0
wukongim_message_event_propose_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="slot_propose_wait",le="0.01"} 0
wukongim_message_event_propose_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="slot_propose_wait",le="+Inf"} 0
wukongim_message_event_propose_batch_events_bucket{path="finish_batch",result="ok",le="4"} 0
wukongim_message_event_propose_batch_events_bucket{path="finish_batch",result="ok",le="8"} 0
wukongim_message_event_propose_batch_events_bucket{path="finish_batch",result="ok",le="+Inf"} 0
`), 0o600); err != nil {
		t.Fatalf("write before: %v", err)
	}
	if err := os.WriteFile(after, []byte(`
wukongim_message_event_stream_cache_sessions 3
wukongim_message_event_stream_cache_open_lanes 5
wukongim_message_event_stream_cache_payload_bytes 2048
wukongim_message_event_stream_cache_max_sessions 1024
wukongim_message_event_append_total{path="cache",event_type="stream.delta",result="ok"} 9
wukongim_message_event_append_total{path="finish_batch",event_type="stream.finish",result="cache_miss"} 2
wukongim_message_event_propose_total{path="finish_batch",result="ok"} 2
wukongim_message_event_append_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="finish_batch_build",le="0.01"} 1
wukongim_message_event_append_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="finish_batch_build",le="+Inf"} 2
wukongim_message_event_propose_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="slot_propose_wait",le="0.01"} 0
wukongim_message_event_propose_stage_duration_seconds_bucket{path="finish_batch",result="ok",stage="slot_propose_wait",le="+Inf"} 2
wukongim_message_event_propose_batch_events_bucket{path="finish_batch",result="ok",le="4"} 1
wukongim_message_event_propose_batch_events_bucket{path="finish_batch",result="ok",le="8"} 2
wukongim_message_event_propose_batch_events_bucket{path="finish_batch",result="ok",le="+Inf"} 2
`), 0o600); err != nil {
		t.Fatalf("write after: %v", err)
	}
	var stderr bytes.Buffer

	code := runWithStderr([]string{"metrics", "classify", "--before", before, "--after", after}, &stderr)

	if code != 0 {
		t.Fatalf("expected success, got code %d and stderr %q", code, stderr.String())
	}
	for _, want := range []string{
		"message_event_stream_cache_sessions_max: 3",
		"message_event_stream_cache_open_lanes_max: 5",
		"message_event_stream_cache_payload_bytes_max: 2048",
		"message_event_append_count{path=\"cache\"}: 8",
		"message_event_append_count{result=\"cache_miss\"}: 2",
		"message_event_propose_count{path=\"finish_batch\"}: 2",
		"message_event_append_stage_p99_seconds{path=\"finish_batch\",stage=\"finish_batch_build\"}:",
		"message_event_propose_stage_p99_seconds{path=\"finish_batch\",stage=\"slot_propose_wait\"}:",
		"message_event_propose_batch_events_p99{path=\"finish_batch\"}:",
		"message_event_cache_miss_count: 2",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected message event output %q, got %q", want, stderr.String())
		}
	}
}

func TestMetricsClassifyReportsChannelRuntimePullHintCounters(t *testing.T) {
	dir := t.TempDir()
	before := filepath.Join(dir, "before.prom")
	after := filepath.Join(dir, "after.prom")
	if err := os.WriteFile(before, []byte(`
wukongim_channelv2_pull_hint_total{reason="append",result="submitted",error="none"} 1
wukongim_channelv2_pull_hint_total{reason="append",result="ok",error="none"} 1
wukongim_channelv2_pull_hint_total{reason="append",result="err",error="stale_meta"} 1
wukongim_channelv2_pull_hint_total{reason="append",result="err",error="canceled"} 0
wukongim_channelv2_pull_hint_total{reason="append",result="err",error="remote_error"} 0
wukongim_channelv2_pull_hint_receive_total{reason="append",stage="meta_resolve",result="err",error="channel_not_found"} 1
wukongim_channelv2_pull_hint_receive_total{reason="append",stage="meta_hint",result="ok",error="none"} 2
wukongim_channelv2_pending_meta_current{reactor_id="0"} 1
wukongim_channelv2_pending_meta_total{event="created",error="none"} 1
wukongim_channelv2_pending_meta_total{event="converted",error="none"} 0
wukongim_channelv2_pending_meta_total{event="released",error="timeout"} 2
wukongim_channelv2_pending_meta_total{event="released",error="not_ready"} 0
wukongim_channelv2_need_meta_pull_total{result="submitted",error="none"} 4
wukongim_channelv2_need_meta_pull_total{result="ok",error="none"} 2
wukongim_channelv2_need_meta_pull_total{result="retry",error="other"} 1
wukongim_channelv2_need_meta_pull_total{result="err",error="timeout"} 2
wukongim_channelv2_need_meta_pull_total{result="err",error="not_ready"} 0
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_need_meta_pull_rpc",result="ok",le="0.01"} 0
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_need_meta_pull_rpc",result="ok",le="0.05"} 0
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_need_meta_pull_rpc",result="ok",le="+Inf"} 0
`), 0o600); err != nil {
		t.Fatalf("write before: %v", err)
	}
	if err := os.WriteFile(after, []byte(`
wukongim_channelv2_pull_hint_total{reason="append",result="submitted",error="none"} 4
wukongim_channelv2_pull_hint_total{reason="append",result="ok",error="none"} 3
wukongim_channelv2_pull_hint_total{reason="append",result="err",error="stale_meta"} 5
wukongim_channelv2_pull_hint_total{reason="append",result="err",error="canceled"} 6
wukongim_channelv2_pull_hint_total{reason="append",result="err",error="remote_error"} 7
wukongim_channelv2_pull_hint_receive_total{reason="append",stage="meta_resolve",result="err",error="channel_not_found"} 9
wukongim_channelv2_pull_hint_receive_total{reason="append",stage="meta_hint",result="ok",error="none"} 13
wukongim_channelv2_pending_meta_current{reactor_id="0"} 4
wukongim_channelv2_pending_meta_total{event="created",error="none"} 9
wukongim_channelv2_pending_meta_total{event="converted",error="none"} 5
wukongim_channelv2_pending_meta_total{event="released",error="timeout"} 5
wukongim_channelv2_pending_meta_total{event="released",error="not_ready"} 2
wukongim_channelv2_need_meta_pull_total{result="submitted",error="none"} 14
wukongim_channelv2_need_meta_pull_total{result="ok",error="none"} 7
wukongim_channelv2_need_meta_pull_total{result="retry",error="other"} 4
wukongim_channelv2_need_meta_pull_total{result="err",error="timeout"} 6
wukongim_channelv2_need_meta_pull_total{result="err",error="not_ready"} 2
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_need_meta_pull_rpc",result="ok",le="0.01"} 2
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_need_meta_pull_rpc",result="ok",le="0.05"} 5
wukongim_channelv2_replication_stage_duration_seconds_bucket{stage="follower_need_meta_pull_rpc",result="ok",le="+Inf"} 5
`), 0o600); err != nil {
		t.Fatalf("write after: %v", err)
	}
	var stderr bytes.Buffer

	code := runWithStderr([]string{"metrics", "classify", "--before", before, "--after", after}, &stderr)

	if code != 0 {
		t.Fatalf("expected success, got code %d and stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pull_hint_submitted_count: 3") {
		t.Fatalf("expected PullHint submitted count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pull_hint_ok_count: 2") {
		t.Fatalf("expected PullHint ok count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pull_hint_err_count: 17") {
		t.Fatalf("expected PullHint err count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pull_hint_stale_meta_err_count: 4") {
		t.Fatalf("expected PullHint stale meta err count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pull_hint_canceled_err_count: 6") {
		t.Fatalf("expected PullHint canceled err count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pull_hint_remote_err_count: 7") {
		t.Fatalf("expected PullHint remote err count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pull_hint_receive_meta_resolve_err_count: 8") {
		t.Fatalf("expected PullHint receive meta resolve err count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pull_hint_receive_channel_not_found_err_count: 8") {
		t.Fatalf("expected PullHint receive channel not found err count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pull_hint_receive_meta_hint_ok_count: 11") {
		t.Fatalf("expected PullHint receive meta hint ok count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pending_meta_current_max: 4") {
		t.Fatalf("expected PendingMeta current gauge in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pending_meta_created_count: 8") {
		t.Fatalf("expected PendingMeta created count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pending_meta_converted_count: 5") {
		t.Fatalf("expected PendingMeta converted count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pending_meta_released_count: 5") {
		t.Fatalf("expected PendingMeta released count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_pending_meta_timeout_release_count: 3") {
		t.Fatalf("expected PendingMeta timeout release count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_need_meta_pull_submitted_count: 10") {
		t.Fatalf("expected NeedMeta submitted count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_need_meta_pull_ok_count: 5") {
		t.Fatalf("expected NeedMeta ok count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_need_meta_pull_retry_count: 3") {
		t.Fatalf("expected NeedMeta retry count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_need_meta_pull_err_count: 6") {
		t.Fatalf("expected NeedMeta err count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_need_meta_pull_timeout_err_count: 4") {
		t.Fatalf("expected NeedMeta timeout err count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_need_meta_pull_not_ready_err_count: 2") {
		t.Fatalf("expected NeedMeta not ready err count in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "channel_need_meta_pull_rpc_p99_seconds:") {
		t.Fatalf("expected NeedMeta pull RPC p99 in output, got %q", stderr.String())
	}
}

func TestMetricsClassifyReportsControllerRaftStepPressureFromPrometheusSnapshots(t *testing.T) {
	dir := t.TempDir()
	before := filepath.Join(dir, "before.prom")
	after := filepath.Join(dir, "after.prom")
	if err := os.WriteFile(before, []byte(`
wukongim_gateway_async_send_queue_depth 0
wukongim_gateway_async_send_queue_capacity 100
wukongim_controller_raft_step_queue_depth 0
wukongim_controller_raft_step_queue_capacity 1024
wukongim_controller_raft_step_enqueue_duration_seconds_bucket{result="err",le="0.25"} 0
wukongim_controller_raft_step_enqueue_duration_seconds_bucket{result="err",le="+Inf"} 0
`), 0o600); err != nil {
		t.Fatalf("write before: %v", err)
	}
	if err := os.WriteFile(after, []byte(`
wukongim_gateway_async_send_queue_depth 0
wukongim_gateway_async_send_queue_capacity 100
wukongim_controller_raft_step_queue_depth 1024
wukongim_controller_raft_step_queue_capacity 1024
wukongim_controller_raft_step_enqueue_duration_seconds_bucket{result="err",le="0.25"} 3
wukongim_controller_raft_step_enqueue_duration_seconds_bucket{result="err",le="+Inf"} 3
`), 0o600); err != nil {
		t.Fatalf("write after: %v", err)
	}
	var stderr bytes.Buffer

	code := runWithStderr([]string{"metrics", "classify", "--before", before, "--after", after}, &stderr)

	if code != 0 {
		t.Fatalf("expected success, got code %d and stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "classification: controller_raft_step") {
		t.Fatalf("expected controller classification, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "controller_raft_step_queue_ratio: 1.000") {
		t.Fatalf("expected controller queue ratio in output, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "controller_raft_step_enqueue_err_count: 3") {
		t.Fatalf("expected controller enqueue error count in output, got %q", stderr.String())
	}
}

func TestValidateCommandLoadsConfigsAndBuildsPlanWithoutNetwork(t *testing.T) {
	targetPath := writeWkbenchTempFile(t, `
name: target
api:
  addrs: [http://127.0.0.1:1]
gateway:
  tcp:
    addrs: [127.0.0.1:5100]
bench_api:
  enabled: true
`)
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: http://127.0.0.1:19090
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"validate", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 0 {
		t.Fatalf("expected validate success, got code %d stderr %q", code, stderr.String())
	}
}

func TestValidateCommandReturnsConfigExitCodeForInvalidConfig(t *testing.T) {
	targetPath := writeWkbenchTempFile(t, `
api:
  addrs: [http://127.0.0.1:1]
gateway:
  tcp:
    addrs: [127.0.0.1:5100]
bench_api:
  enabled: false
`)
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: http://127.0.0.1:19090
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"validate", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 1 {
		t.Fatalf("expected config exit code 1, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "bench_api.enabled") {
		t.Fatalf("expected bench_api.enabled error, got %q", stderr.String())
	}
}

func TestValidateCommandReturnsConfigExitCodeForMissingWorkerAddr(t *testing.T) {
	targetPath := writeWkbenchTempFile(t, validTargetYAML("http://127.0.0.1:1"))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"validate", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 1 {
		t.Fatalf("expected config exit code 1, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "workers[0].addr") {
		t.Fatalf("expected worker addr error, got %q", stderr.String())
	}
}

func TestValidateCommandReturnsConfigExitCodeForMissingWorkerToken(t *testing.T) {
	targetPath := writeWkbenchTempFile(t, validTargetYAML("http://127.0.0.1:1"))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: http://127.0.0.1:19090
    weight: 1
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"validate", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 1 {
		t.Fatalf("expected config exit code 1, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "control_token") {
		t.Fatalf("expected control token error, got %q", stderr.String())
	}
}

func TestDoctorCommandReturnsPreflightExitCodeForNetworkFailure(t *testing.T) {
	targetPath := writeWkbenchTempFile(t, `
api:
  addrs: [http://127.0.0.1:1]
gateway:
  tcp:
    addrs: [127.0.0.1:5100]
bench_api:
  enabled: true
`)
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: http://127.0.0.1:19090
    weight: 1
    insecure_control: true
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"doctor", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 2 {
		t.Fatalf("expected preflight exit code 2, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "preflight failed") {
		t.Fatalf("expected preflight error, got %q", stderr.String())
	}
}

func TestDoctorCommandRunsWithoutScenario(t *testing.T) {
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz":
			w.WriteHeader(http.StatusOK)
		case "/bench/v1/capabilities":
			writeWkbenchJSON(t, w, map[string]any{
				"enabled": true,
				"version": "bench/v1",
				"supports": map[string]any{
					"users_tokens_batch":        true,
					"channels_batch":            true,
					"channel_subscribers_batch": true,
					"snapshot":                  true,
					"channel_types":             []string{"group"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer targetSrv.Close()
	workerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requirePath(t, r, "/v1/info")
		requireHeader(t, r, "Authorization", "Bearer secret")
		writeWkbenchJSON(t, w, map[string]string{"worker": "wkbench"})
	}))
	defer workerSrv.Close()
	targetPath := writeWkbenchTempFile(t, validTargetYAML(targetSrv.URL))
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: `+workerSrv.URL+`
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"doctor", "--target", targetPath, "--workers", workersPath}, &stderr)

	if code != 0 {
		t.Fatalf("expected doctor success, got code %d stderr %q", code, stderr.String())
	}
}

func TestRunCommandCompletesWorkloadOrchestration(t *testing.T) {
	targetSrv := goodWkbenchTargetServer(t)
	defer targetSrv.Close()
	workerSrv := goodWkbenchWorkerServer(t, "secret")
	defer workerSrv.Close()
	targetPath := writeWkbenchTempFile(t, validTargetYAML(targetSrv.URL))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: `+workerSrv.URL+`
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"run", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 0 {
		t.Fatalf("expected run success, got code %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "wkbench workload orchestration completed") {
		t.Fatalf("expected workload orchestration note, got %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "fake/no-op") {
		t.Fatalf("did not expect stale fake/no-op note, got %q", stderr.String())
	}
}

func TestRunCommandPhasePollTimeoutAllowsSlowConnectPhase(t *testing.T) {
	targetSrv := goodWkbenchTargetServer(t)
	defer targetSrv.Close()
	workerSrv := delayedConnectWkbenchWorkerServer(t, "secret", 40*time.Millisecond)
	defer workerSrv.Close()
	targetPath := writeWkbenchTempFile(t, validTargetYAML(targetSrv.URL))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: `+workerSrv.URL+`
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"run", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath, "--phase-poll-timeout", "120ms"}, &stderr)

	if code != 0 {
		t.Fatalf("expected run success, got code %d stderr %q", code, stderr.String())
	}
}

func TestRunCommandPhasePollTimeoutFailsWhenPhaseExceedsConfiguredWindow(t *testing.T) {
	targetSrv := goodWkbenchTargetServer(t)
	defer targetSrv.Close()
	workerSrv := delayedConnectWkbenchWorkerServer(t, "secret", 40*time.Millisecond)
	defer workerSrv.Close()
	targetPath := writeWkbenchTempFile(t, validTargetYAML(targetSrv.URL))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: `+workerSrv.URL+`
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"run", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath, "--phase-poll-timeout", "5ms"}, &stderr)

	if code != exitWorker {
		t.Fatalf("expected worker exit code, got code %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "worker phase poll timeout") {
		t.Fatalf("expected phase poll timeout error, got %q", stderr.String())
	}
}

func TestRunCommandWritesReportDirectoryWhenConfigured(t *testing.T) {
	targetSrv := goodWkbenchTargetServer(t)
	defer targetSrv.Close()
	workerSrv := goodWkbenchWorkerServer(t, "secret")
	defer workerSrv.Close()
	reportDir := t.TempDir()
	targetPath := writeWkbenchTempFile(t, validTargetYAML(targetSrv.URL))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAMLWithReportDir(reportDir))
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: `+workerSrv.URL+`
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"run", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 0 {
		t.Fatalf("expected run success, got code %d stderr %q", code, stderr.String())
	}
	for _, rel := range []string{"report.json", "summary.md", "coordinator.log", "metrics/worker-1s.jsonl", "errors/samples.jsonl"} {
		if _, err := os.Stat(filepath.Join(reportDir, rel)); err != nil {
			t.Fatalf("expected report artifact %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(reportDir, "workers", "w1.report.json")); err != nil {
		t.Fatalf("expected worker report artifact: %v", err)
	}
}

func TestRunCommandReturnsInternalExitCodeWhenReportWriteFails(t *testing.T) {
	targetSrv := goodWkbenchTargetServer(t)
	defer targetSrv.Close()
	workerSrv := goodWkbenchWorkerServer(t, "secret")
	defer workerSrv.Close()
	reportDir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(reportDir, []byte("file blocks report dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	targetPath := writeWkbenchTempFile(t, validTargetYAML(targetSrv.URL))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAMLWithReportDir(reportDir))
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: `+workerSrv.URL+`
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"run", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 6 {
		t.Fatalf("expected internal exit code 6, got %d stderr %q", code, stderr.String())
	}
}

func TestRunCommandReturnsPreflightExitCodeForNetworkFailure(t *testing.T) {
	targetPath := writeWkbenchTempFile(t, validTargetYAML("http://127.0.0.1:1"))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: http://127.0.0.1:19090
    weight: 1
    insecure_control: true
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"run", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 2 {
		t.Fatalf("expected preflight exit code 2, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "preflight failed") {
		t.Fatalf("expected preflight error, got %q", stderr.String())
	}
}

func TestRunCommandReturnsWorkerExitCodeForPhaseFailure(t *testing.T) {
	targetSrv := goodWkbenchTargetServer(t)
	defer targetSrv.Close()
	workerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/info", "/v1/status":
			writeWkbenchJSON(t, w, map[string]any{"phase": "prepare", "assignment": map[string]string{"run_id": "bench-run", "worker_id": "w1"}})
		case "/v1/assign", "/v1/phase/prepare", "/v1/stop":
			writeWkbenchJSON(t, w, map[string]any{"phase": "prepare", "assignment": map[string]string{"run_id": "bench-run", "worker_id": "w1"}})
		case "/v1/phase/connect":
			http.Error(w, "connect failed", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer workerSrv.Close()
	targetPath := writeWkbenchTempFile(t, validTargetYAML(targetSrv.URL))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML())
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: `+workerSrv.URL+`
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"run", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 4 {
		t.Fatalf("expected worker exit code 4, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "worker run failed") {
		t.Fatalf("expected worker failure error, got %q", stderr.String())
	}
}

func TestRunCommandReturnsHardLimitExitCodeWhenCollectionAlsoFails(t *testing.T) {
	targetSrv := goodWkbenchTargetServer(t)
	defer targetSrv.Close()
	workerSrv := hardLimitAndCollectionFailureWorkerServer(t, "secret")
	defer workerSrv.Close()
	targetPath := writeWkbenchTempFile(t, validTargetYAML(targetSrv.URL))
	scenarioPath := writeWkbenchTempFile(t, validScenarioYAML()+`
limits:
  hard:
    max_sendack_error_rate: 0
`)
	workersPath := writeWkbenchTempFile(t, `
workers:
  - id: w1
    addr: `+workerSrv.URL+`
    weight: 1
    control_token: secret
`)
	var stderr bytes.Buffer

	code := runWithStderr([]string{"run", "--target", targetPath, "--scenario", scenarioPath, "--workers", workersPath}, &stderr)

	if code != 3 {
		t.Fatalf("expected hard-limit exit code 3, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "hard limit failed") {
		t.Fatalf("expected hard limit error, got %q", stderr.String())
	}
}

func TestValidateCommandRequiresConfigFlags(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"validate", "--target", "target.yaml"}, &stderr)

	if code != 1 {
		t.Fatalf("expected config exit code 1, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--scenario is required") {
		t.Fatalf("expected missing scenario error, got %q", stderr.String())
	}
}

func TestDevSimCommandReturnsConfigExitCodeForMissingConfig(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"dev-sim", "--config", filepath.Join(t.TempDir(), "missing.yaml")}, &stderr)

	if code != 1 {
		t.Fatalf("expected config exit code 1, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "config validation failed") {
		t.Fatalf("expected config validation error, got %q", stderr.String())
	}
}

func TestDevSimCommandHelp(t *testing.T) {
	var stderr bytes.Buffer

	code := runWithStderr([]string{"dev-sim", "--help"}, &stderr)

	if code != 0 {
		t.Fatalf("expected help exit code 0, got %d stderr %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "wkbench dev-sim") || !strings.Contains(stderr.String(), "--status-listen") {
		t.Fatalf("expected dev-sim help, got %q", stderr.String())
	}
}

func writeWkbenchTempFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func validScenarioYAML() string {
	return `
version: wkbench/v1
run:
  id: bench-run
online:
  total_users: 10
channels:
  profiles:
    - name: group-hot
      channel_type: group
      count: 1
      members:
        count: 5
messages:
  traffic:
    - name: hot-group-send
      channel_ref: group-hot
      rate_per_channel: 1/s
`
}

func validScenarioYAMLWithReportDir(reportDir string) string {
	return `
version: wkbench/v1
run:
  id: bench-run
  report_dir: ` + reportDir + `
online:
  total_users: 10
channels:
  profiles:
    - name: group-hot
      channel_type: group
      count: 1
      members:
        count: 5
messages:
  traffic:
    - name: hot-group-send
      channel_ref: group-hot
      rate_per_channel: 1/s
`
}

func validTargetYAML(apiAddr string) string {
	return `
name: target
api:
  addrs: [` + apiAddr + `]
gateway:
  tcp:
    addrs: [127.0.0.1:5100]
bench_api:
  enabled: true
`
}

func goodWkbenchTargetServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz":
			w.WriteHeader(http.StatusOK)
		case "/bench/v1/capabilities":
			writeWkbenchJSON(t, w, map[string]any{
				"enabled": true,
				"version": "bench/v1",
				"supports": map[string]any{
					"users_tokens_batch":        true,
					"channels_batch":            true,
					"channel_subscribers_batch": true,
					"snapshot":                  true,
					"channel_types":             []string{"group"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func goodWkbenchWorkerServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	phase := "assigned"
	assignment := map[string]string{"run_id": "bench-run", "worker_id": "w1"}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/info":
			writeWkbenchJSON(t, w, map[string]string{"worker": "wkbench"})
		case "/v1/assign":
			phase = "assigned"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/prepare":
			phase = "prepare"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/connect":
			phase = "connect"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/warmup":
			phase = "warmup"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/run":
			phase = "run"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/cooldown":
			phase = "cooldown"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/status":
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/metrics":
			writeWkbenchJSON(t, w, map[string]any{"counters": map[string]uint64{}, "gauges": map[string]float64{}, "histograms": map[string]any{}, "errors": []any{}})
		case "/v1/report":
			writeWkbenchJSON(t, w, map[string]any{"worker_id": "w1"})
		case "/v1/stop":
			phase = "stopped"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		default:
			http.NotFound(w, r)
		}
	}))
}

func delayedConnectWkbenchWorkerServer(t *testing.T, token string, delay time.Duration) *httptest.Server {
	t.Helper()
	phase := "assigned"
	completedPhase := ""
	activePhase := ""
	var connectStarted time.Time
	assignment := map[string]string{"run_id": "bench-run", "worker_id": "w1"}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/info":
			writeWkbenchJSON(t, w, map[string]string{"worker": "wkbench"})
		case "/v1/assign":
			phase = "assigned"
			completedPhase = ""
			activePhase = ""
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/prepare":
			phase = "prepare"
			completedPhase = "prepare"
			activePhase = ""
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "completed_phase": completedPhase, "assignment": assignment})
		case "/v1/phase/connect":
			activePhase = "connect"
			connectStarted = time.Now()
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "active_phase": activePhase, "completed_phase": completedPhase, "assignment": assignment})
		case "/v1/phase/warmup":
			phase = "warmup"
			completedPhase = "warmup"
			activePhase = ""
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "completed_phase": completedPhase, "assignment": assignment})
		case "/v1/phase/run":
			phase = "run"
			completedPhase = "run"
			activePhase = ""
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "completed_phase": completedPhase, "assignment": assignment})
		case "/v1/phase/cooldown":
			phase = "cooldown"
			completedPhase = "cooldown"
			activePhase = ""
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "completed_phase": completedPhase, "assignment": assignment})
		case "/v1/status":
			if activePhase == "connect" && time.Since(connectStarted) >= delay {
				phase = "connect"
				completedPhase = "connect"
				activePhase = ""
			}
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "active_phase": activePhase, "completed_phase": completedPhase, "assignment": assignment})
		case "/v1/metrics":
			writeWkbenchJSON(t, w, map[string]any{"counters": map[string]uint64{}, "gauges": map[string]float64{}, "histograms": map[string]any{}, "errors": []any{}})
		case "/v1/report":
			writeWkbenchJSON(t, w, map[string]any{"worker_id": "w1"})
		case "/v1/stop":
			phase = "stopped"
			activePhase = ""
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "completed_phase": completedPhase, "assignment": assignment})
		default:
			http.NotFound(w, r)
		}
	}))
}

func hardLimitAndCollectionFailureWorkerServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	phase := "assigned"
	assignment := map[string]string{"run_id": "bench-run", "worker_id": "w1"}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "missing auth", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/info":
			writeWkbenchJSON(t, w, map[string]string{"worker": "wkbench"})
		case "/v1/assign":
			phase = "assigned"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/prepare":
			phase = "prepare"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/connect":
			phase = "connect"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/warmup":
			phase = "warmup"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/run":
			phase = "run"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/phase/cooldown":
			phase = "cooldown"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/status":
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		case "/v1/metrics":
			writeWkbenchJSON(t, w, map[string]any{"counters": map[string]uint64{"person_send_success_total": 9, "person_send_error_total": 1}, "gauges": map[string]float64{}, "histograms": map[string]any{}, "errors": []any{}})
		case "/v1/report":
			http.Error(w, "report exploded", http.StatusInternalServerError)
		case "/v1/stop":
			phase = "stopped"
			writeWkbenchJSON(t, w, map[string]any{"phase": phase, "assignment": assignment})
		default:
			http.NotFound(w, r)
		}
	}))
}

func writeWkbenchJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatal(err)
	}
}

func requirePath(t *testing.T, r *http.Request, want string) {
	t.Helper()
	if r.URL.Path != want {
		t.Fatalf("path = %s, want %s", r.URL.Path, want)
	}
}

func requireHeader(t *testing.T, r *http.Request, key, want string) {
	t.Helper()
	if got := r.Header.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}
