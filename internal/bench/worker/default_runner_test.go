package worker

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	benchworkload "github.com/WuKongIM/WuKongIM/internal/bench/workload"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/stretchr/testify/require"
)

func TestDefaultRunnerCooldownAcceptsMeasuredTaskCompletionAtDeadlineBoundary(t *testing.T) {
	done := make(chan struct{})
	task := &measuredTrafficTask{runID: "run-a", done: done}
	task.cancel = func() {
		close(done)
	}
	runner := &defaultWorkloadRunner{
		runID:        "run-a",
		measuredTask: task,
	}
	assignment := Assignment{
		RunID: "run-a",
		Scenario: model.Scenario{Run: model.RunConfig{
			Cooldown: time.Nanosecond,
		}},
	}

	err := runner.Cooldown(context.Background(), assignment)

	if err != nil {
		t.Fatalf("Cooldown() error = %v, want completed task accepted at deadline boundary", err)
	}
	if got := runner.currentMeasuredTrafficTask("run-a"); got != nil {
		t.Fatalf("measured task was not cleared: %#v", got)
	}
}

func TestNewDefaultWorkloadRunnerExposesMetricsReporter(t *testing.T) {
	runner := NewDefaultWorkloadRunner(nil)
	if runner == nil {
		t.Fatal("expected default workload runner")
	}
	if _, ok := runner.(MetricsReporter); !ok {
		t.Fatal("expected default workload runner to expose metrics")
	}
	if _, ok := runner.(ConnectionStatusReporter); !ok {
		t.Fatal("expected default workload runner to expose connection status")
	}
	if _, ok := runner.(LifecycleStatusReporter); !ok {
		t.Fatal("expected default workload runner to expose lifecycle status")
	}
}

func TestDefaultRunnerPrepareRejectsExternalTerminalCutWithoutReceiveDrainTrafficBeforeTargetMutation(t *testing.T) {
	targetCalls := 0
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer targetServer.Close()
	assignment := connectionOnlyAssignment(time.Second)
	assignment.Scenario.Run.ExternalTerminalCut = true
	assignment.Scenario.Identity.Token.Mode = "bench_api"
	assignment.Target.BenchAPI.Addrs = []string{targetServer.URL}

	err := NewDefaultWorkloadRunner(nil).Prepare(context.Background(), assignment)

	if err == nil || !strings.Contains(err.Error(), "external terminal cut requires receive-drain traffic") {
		t.Fatalf("Prepare() error = %v, want receive-drain traffic requirement", err)
	}
	if targetCalls != 0 {
		t.Fatalf("Prepare() made %d target calls before validating receive-drain traffic", targetCalls)
	}
}

func TestDefaultRunnerPrepareRejectsExternalTerminalCutWithoutRecvAckBeforeTargetMutation(t *testing.T) {
	targetCalls := 0
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer targetServer.Close()
	assignment := personShardAssignment()
	assignment.Scenario.Run.ExternalTerminalCut = true
	assignment.Scenario.Messages.Traffic[0].RecvAck = false
	assignment.Scenario.Identity.Token.Mode = "bench_api"
	assignment.Target.BenchAPI.Addrs = []string{targetServer.URL}

	err := NewDefaultWorkloadRunner(nil).Prepare(context.Background(), assignment)

	if err == nil || !strings.Contains(err.Error(), "external terminal cut requires recv_ack traffic") {
		t.Fatalf("Prepare() error = %v, want recv_ack traffic requirement", err)
	}
	if targetCalls != 0 {
		t.Fatalf("Prepare() made %d target calls before validating recv_ack traffic", targetCalls)
	}
}

func TestDefaultRunnerPrepareRejectsExternalTerminalCutUnsupportedFanoutShapeBeforeTargetMutation(t *testing.T) {
	targetCalls := 0
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer targetServer.Close()
	assignment := terminalCutExactGroupAssignment()
	assignment.Scenario.Identity.Token.Mode = "bench_api"
	assignment.Target.BenchAPI.Addrs = []string{targetServer.URL}
	assignment.Scenario.Channels.Profiles = append(assignment.Scenario.Channels.Profiles, model.ChannelProfile{
		Name: "second-group", ChannelType: model.ChannelTypeGroup, Members: model.MembersConfig{Count: 2},
	})

	err := NewDefaultWorkloadRunner(nil).Prepare(context.Background(), assignment)

	if err == nil || !strings.Contains(err.Error(), "exactly one group profile") {
		t.Fatalf("Prepare() error = %v, want exact group shape requirement", err)
	}
	if targetCalls != 0 {
		t.Fatalf("Prepare() made %d target calls before validating fanout proof shape", targetCalls)
	}
}

func TestDefaultRunnerFanoutProofSurvivesRebuildAndResetsForNewAssignment(t *testing.T) {
	pool := newWorkerPersonClientPool()
	runner := NewDefaultWorkloadRunner(pool.newClient).(*defaultWorkloadRunner)
	assignment := terminalCutExactGroupAssignment()
	runner.BeginAssignment(assignment)
	proof, err := fanoutProofForAssignment(assignment)
	require.NoError(t, err)
	require.NoError(t, runner.installAssignmentFanoutProof(assignment, proof))
	require.NoError(t, runner.Connect(context.Background(), assignment))
	first := runner.fanoutProof
	require.NotNil(t, first)
	first.ExpectGroup("msg-1", "group-a", "bench-u-0", []string{"bench-u-0", "bench-u-1"})

	require.NoError(t, runner.rebuildTrafficFromManager(context.Background(), assignment, runner.manager))
	require.Same(t, first, runner.fanoutProof)
	require.Equal(t, uint64(1), runner.fanoutProof.Snapshot().LogicalSendACKs)

	second := assignment
	second.AssignmentID = "terminal-cut-generation-2"
	runner.BeginAssignment(second)
	secondProof, err := fanoutProofForAssignment(second)
	require.NoError(t, err)
	require.NoError(t, runner.installAssignmentFanoutProof(second, secondProof))
	require.NotSame(t, first, runner.fanoutProof)
	require.Zero(t, runner.fanoutProof.Snapshot().LogicalSendACKs)
}

func TestDefaultRunnerStopRetainsUnsealedFanoutEvidenceUntilNextAssignment(t *testing.T) {
	proof, err := benchworkload.NewGroupFanoutProof(2)
	require.NoError(t, err)
	runner := &defaultWorkloadRunner{
		runID:                   "run-a",
		fanoutProof:             proof,
		fanoutProofAssignmentID: "generation-a",
		metrics:                 metrics.NewRegistry(),
	}

	require.NoError(t, runner.closeCurrent("run-a"))
	require.Same(t, proof, runner.fanoutProof,
		"an unsealed failed stop must not erase the assignment proof before final status is sampled")

	runner.beginRun("run-b", true)
	require.Nil(t, runner.fanoutProof)
	require.Empty(t, runner.fanoutProofAssignmentID)
}

func TestDefaultRunnerLifecycleStatusProjectsOnlyMeasuredTraffic(t *testing.T) {
	runner := &defaultWorkloadRunner{
		metrics: metrics.NewRegistry(),
		archivedWorkloadMetrics: []metrics.SnapshotData{{
			Counters: map[string]uint64{
				"workload_scheduler_planned_total{phase=warmup}": 99,
				"sendack_success_total{phase=warmup}":            99,
				"sendack_success_total{phase=cooldown}":          77,
				"workload_scheduler_planned_total{phase=run}":    1,
				"workload_scheduler_dispatched_total{phase=run}": 1,
				"logical_identity_total{phase=run}":              1,
				"logical_sent_total{phase=run}":                  1,
				"send_attempt_total{phase=run}":                  2,
				"attempt_record_total{phase=run}":                2,
				"retry_attempt_total{phase=run}":                 1,
				"sendack_success_total{phase=run}":               1,
				"logical_terminal_error_total{phase=run}":        0,
				"logical_correctness_error_total{phase=run}":     0,
				"retry_exhausted_total{phase=run}":               0,
				"client_msg_no_mismatch_total{phase=run}":        0,
			},
			Gauges: map[string]float64{
				"logical_remaining{phase=run}":           0,
				"configured_maximum_attempts{phase=run}": 4,
				"maximum_observed_attempts{phase=run}":   2,
			},
		}},
	}

	status := runner.LifecycleStatus()
	if status.Traffic.Planned != 1 || status.Traffic.Dispatched != 1 || status.Traffic.LogicalSent != 1 {
		t.Fatalf("measured logical projection = %+v", status.Traffic)
	}
	if status.Traffic.SendAttempts != 2 || status.Traffic.RetryAttempts != 1 || status.Traffic.SendACKs != 1 {
		t.Fatalf("measured attempt projection = %+v", status.Traffic)
	}
	if status.Traffic.WarmupSendACKs != 99 {
		t.Fatalf("warmup SENDACK projection = %d, want strict phase=warmup total 99", status.Traffic.WarmupSendACKs)
	}
	if !status.Traffic.StableClientMsgNo || !status.Traffic.RetryEvidenceComplete || status.Traffic.MaximumRetriesPerMessage != 3 {
		t.Fatalf("retry proof = %+v", status.Traffic)
	}
}

func TestTrafficStatusCombinesIndependentMeasuredStreamsWithoutSummingPolicyGauges(t *testing.T) {
	snapshot := metrics.SnapshotData{
		Counters: map[string]uint64{
			"sendack_success_total{phase=warmup,traffic=a}":     2,
			"sendack_success_total{phase=warmup,traffic=b}":     3,
			"sendack_success_total{phase=prewarmup,traffic=a}":  9,
			"logical_identity_total{phase=run,traffic=a}":       1,
			"logical_identity_total{phase=run,traffic=b}":       1,
			"logical_sent_total{phase=run,traffic=a}":           1,
			"logical_sent_total{phase=run,traffic=b}":           1,
			"send_attempt_total{phase=run,traffic=a}":           2,
			"send_attempt_total{phase=run,traffic=b}":           1,
			"attempt_record_total{phase=run,traffic=a}":         2,
			"attempt_record_total{phase=run,traffic=b}":         1,
			"retry_attempt_total{phase=run,traffic=a}":          1,
			"sendack_success_total{phase=run,traffic=a}":        1,
			"sendack_success_total{phase=run,traffic=b}":        1,
			"client_msg_no_mismatch_total{phase=run,traffic=a}": 0,
		},
		Gauges: map[string]float64{
			"logical_remaining{phase=run,traffic=a}":           0,
			"logical_remaining{phase=run,traffic=b}":           0,
			"configured_maximum_attempts{phase=run,traffic=a}": 4,
			"configured_maximum_attempts{phase=run,traffic=b}": 4,
			"maximum_observed_attempts{phase=run,traffic=a}":   2,
			"maximum_observed_attempts{phase=run,traffic=b}":   1,
		},
	}

	status := trafficStatusFromMetrics(snapshot)
	if status.LogicalSent != 2 || status.SendAttempts != 3 || status.RetryAttempts != 1 || status.SendACKs != 2 {
		t.Fatalf("combined traffic status = %+v", status)
	}
	if status.WarmupSendACKs != 5 {
		t.Fatalf("combined warmup SENDACKs = %d, want strict phase total 5", status.WarmupSendACKs)
	}
	if status.MaximumRetriesPerMessage != 3 || !status.RetryEvidenceComplete {
		t.Fatalf("combined retry policy = %+v", status)
	}
}

func TestMarkTargetUnavailablePreservesLocalTCPSourceErrors(t *testing.T) {
	sourceErr := &benchworkload.TCPSourceError{
		Kind: benchworkload.TCPSourceErrorUnavailable,
		Err:  &net.OpError{Op: "dial", Net: "tcp", Err: syscall.EADDRNOTAVAIL},
	}

	got := markTargetUnavailable(sourceErr)

	if got != sourceErr {
		t.Fatalf("markTargetUnavailable() = %T %v, want original local source error", got, got)
	}
	if errors.Is(got, errTargetUnavailable) {
		t.Fatal("local source error classified as target unavailable")
	}
}

func TestConnectionManagerConfigCopiesAssignmentClientProfile(t *testing.T) {
	profile := &model.WorkerClientConfig{
		SendQueueCapacity: 16,
		MaxInflight:       1,
		ReadBufferSize:    1024,
		FrameBufferSize:   4,
	}
	assignment := Assignment{
		Client: profile,
		Target: model.Target{Gateway: model.TargetGatewayConfig{TCP: model.TargetGatewayTCPConfig{Addrs: []string{"127.0.0.1:5100"}}}},
	}

	cfg := connectionManagerConfig(assignment, nil)

	if cfg.Client == nil || *cfg.Client != *profile {
		t.Fatalf("connection manager client profile = %#v, want %#v", cfg.Client, profile)
	}
	if cfg.Client == profile {
		t.Fatal("connection manager client profile aliases assignment profile")
	}
	profile.SendQueueCapacity = 999
	if cfg.Client.SendQueueCapacity != 16 {
		t.Fatalf("copied send queue capacity = %d, want 16 after source mutation", cfg.Client.SendQueueCapacity)
	}
}

func TestConnectionManagerConfigCopiesAssignmentTCPSourcePool(t *testing.T) {
	pool := &model.TCPSourceConfig{
		IPv4Addrs: []string{"127.0.0.1", "192.168.3.57"},
		PortMin:   1024,
		PortMax:   65535,
	}
	assignment := Assignment{
		TCPSource: pool,
		Target:    model.Target{Gateway: model.TargetGatewayConfig{TCP: model.TargetGatewayTCPConfig{Addrs: []string{"127.0.0.1:5100"}}}},
	}

	cfg := connectionManagerConfig(assignment, nil)

	if cfg.TCPSource == nil || cfg.TCPSource.PortMin != 1024 || cfg.TCPSource.PortMax != 65535 {
		t.Fatalf("connection manager tcp source pool = %#v, want %#v", cfg.TCPSource, pool)
	}
	if cfg.TCPSource == pool || &cfg.TCPSource.IPv4Addrs[0] == &pool.IPv4Addrs[0] {
		t.Fatal("connection manager tcp source pool aliases assignment pool")
	}
	pool.IPv4Addrs[0] = "10.0.0.1"
	if cfg.TCPSource.IPv4Addrs[0] != "127.0.0.1" {
		t.Fatalf("copied source address = %q, want 127.0.0.1 after source mutation", cfg.TCPSource.IPv4Addrs[0])
	}
}
