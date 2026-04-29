//go:build integration
// +build integration

package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	deliveryusecase "github.com/WuKongIM/WuKongIM/internal/usecase/delivery"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestRecvStressThreeNode(t *testing.T) {
	selection := selectRecvStressThreeNodeRun(t)
	cfg := selection.cfg

	harness := newThreeNodeAppHarnessWithConfigMutator(t, func(appCfg *Config) {
		if cfg.Mode == recvStressModeThroughput {
			applyRecvStressThroughputTuning(appCfg, cfg)
		}
	})
	ownerID := harness.waitForStableLeader(t, 1)
	owner := harness.apps[ownerID]

	targets := preloadRecvStressTargets(t, harness, owner, cfg)
	outcome, observed, records, failures := runRecvStressWorkers(t, harness, owner, targets, cfg)

	t.Logf("recv stress results: total=%d success=%d failed=%d error_rate=%.2f%% qps=%.2f p50=%s p95=%s p99=%s", outcome.Total, outcome.Success, outcome.Failed, outcome.ErrorRate(), observed.QPS, observed.P50, observed.P95, observed.P99)
	if len(failures) > 0 {
		t.Logf("recv stress failures: %s", strings.Join(failures, " | "))
	}

	require.NotZero(t, outcome.Total)
	require.Equal(t, outcome.Total, outcome.Success)
	require.Zero(t, outcome.Failed)
	require.Len(t, records, int(outcome.Success))
}

func applyRecvStressThroughputTuning(appCfg *Config, cfg recvStressConfig) {
	if appCfg == nil {
		return
	}
	// Extreme receive benchmarks need enough outbound queue depth to avoid
	// measuring gateway queue overflow retries instead of local receive throughput.
	appCfg.Gateway.DefaultSession.WriteQueueSize = max(appCfg.Gateway.DefaultSession.WriteQueueSize, max(1024, cfg.MaxInflightPerWorker*4))
	appCfg.Gateway.DefaultSession.MaxOutboundBytes = max(appCfg.Gateway.DefaultSession.MaxOutboundBytes, 16<<20)
}

func preloadRecvStressTargets(t *testing.T, harness *threeNodeAppHarness, owner *App, cfg recvStressConfig) []recvStressTarget {
	t.Helper()
	require.NotNil(t, harness)
	require.NotNil(t, owner)
	require.NotZero(t, cfg.Recipients)

	ownerID := owner.cfg.Node.ID
	targets := make([]recvStressTarget, 0, cfg.Recipients)
	for idx := 0; idx < cfg.Recipients; idx++ {
		senderUID := fmt.Sprintf("recv-stress-sender-%03d", idx)
		recipientUID := fmt.Sprintf("recv-stress-recipient-%03d", idx)
		channelID := deliveryusecase.EncodePersonChannel(senderUID, recipientUID)
		targets = append(targets, recvStressTarget{
			SenderUID:    senderUID,
			RecipientUID: recipientUID,
			ChannelID:    channelID,
			ChannelType:  frame.ChannelTypePerson,
			OwnerNodeID:  ownerID,
			// Keep this stress script on the pure local receive path; remote
			// delivery RPC belongs to separate node-to-node delivery coverage.
			ConnectNodeID: ownerID,
		})
	}
	return targets
}

func runRecvStressWorkers(t *testing.T, harness *threeNodeAppHarness, owner *App, targets []recvStressTarget, cfg recvStressConfig) (recvStressOutcome, recvStressObservedMetrics, []recvStressRecord, []string) {
	t.Helper()
	require.NotNil(t, harness)
	require.NotNil(t, owner)
	require.NotEmpty(t, targets)

	activeTargetCount := recvStressActiveTargetCount(cfg, len(targets))
	require.Positive(t, activeTargetCount)

	clients := make([]recvStressWorkerClient, 0, activeTargetCount)
	for worker := 0; worker < activeTargetCount; worker++ {
		target := targets[worker]
		app := harness.apps[target.ConnectNodeID]
		require.NotNil(t, app, "connect node %d is not running", target.ConnectNodeID)
		conn := runRecvStressClient(t, app, target.RecipientUID, cfg)
		clients = append(clients, recvStressWorkerClient{
			target:  target,
			conn:    conn,
			reader:  newSendStressFrameReader(conn),
			writeMu: &sync.Mutex{},
		})
	}
	defer func() {
		for _, client := range clients {
			if client.conn != nil {
				_ = client.conn.Close()
			}
		}
	}()

	for _, client := range clients {
		waitForRecvStressRoute(t, owner, client.target.RecipientUID)
	}
	warmupRecords := warmupRecvStressClients(t, owner, clients, cfg)
	require.Len(t, warmupRecords, len(clients))

	var total atomic.Uint64
	var success atomic.Uint64
	var failed atomic.Uint64
	var verificationFailures atomic.Uint64
	var mu sync.Mutex
	records := make([]recvStressRecord, 0, activeTargetCount*cfg.MessagesPerWorker)
	latencies := make([]time.Duration, 0, activeTargetCount*cfg.MessagesPerWorker)
	failures := make([]string, 0, 8)
	appendFailure := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		if len(failures) >= 8 {
			return
		}
		failures = append(failures, fmt.Sprintf(format, args...))
	}

	submit := func(ctx context.Context, env deliveryruntime.CommittedEnvelope) error {
		return owner.deliveryRuntime.Submit(ctx, env)
	}
	startedAt := time.Now()
	deadline := startedAt.Add(cfg.Duration)
	var wg sync.WaitGroup
	for worker, client := range clients {
		worker := worker
		client := client
		wg.Add(1)
		go func() {
			defer wg.Done()
			var (
				workerOutcome  recvStressOutcome
				workerRecords  []recvStressRecord
				workerFailures []string
				err            error
			)
			if cfg.Mode == recvStressModeThroughput {
				workerOutcome, workerRecords, workerFailures, err = runRecvStressWorkerThroughput(client, submit, worker, cfg, deadline)
			} else {
				workerOutcome, workerRecords, workerFailures = runRecvStressWorker(client, submit, worker, cfg, deadline)
			}
			if err != nil {
				workerFailures = append(workerFailures, err.Error())
			}
			total.Add(workerOutcome.Total)
			success.Add(workerOutcome.Success)
			failed.Add(workerOutcome.Failed)

			mu.Lock()
			records = append(records, workerRecords...)
			for _, record := range workerRecords {
				latencies = append(latencies, record.RecvLatency)
			}
			mu.Unlock()
			for _, failure := range workerFailures {
				verificationFailures.Add(1)
				appendFailure("%s", failure)
			}
		}()
	}
	wg.Wait()

	elapsed := time.Since(startedAt)
	outcome := recvStressOutcome{
		Total:   total.Load(),
		Success: success.Load(),
		Failed:  failed.Load(),
	}
	latencySummary := summarizeSendStressLatencies(latencies)
	qps := 0.0
	if elapsed > 0 {
		qps = float64(outcome.Success) / elapsed.Seconds()
	}
	observed := recvStressObservedMetrics{
		QPS:                  qps,
		P50:                  latencySummary.P50,
		P95:                  latencySummary.P95,
		P99:                  latencySummary.P99,
		VerificationCount:    len(records),
		VerificationFailures: int(verificationFailures.Load()),
	}
	t.Logf(
		"recv stress metrics: mode=%s max_inflight_per_worker=%d duration=%s workers=%d recipients=%d active_target_count=%d total=%d success=%d failed=%d qps=%.2f p50=%s p95=%s p99=%s max=%s verification_count=%d verification_failures=%d",
		cfg.Mode,
		cfg.MaxInflightPerWorker,
		elapsed,
		cfg.Workers,
		cfg.Recipients,
		activeTargetCount,
		outcome.Total,
		outcome.Success,
		outcome.Failed,
		qps,
		latencySummary.P50,
		latencySummary.P95,
		latencySummary.P99,
		latencySummary.Max,
		observed.VerificationCount,
		observed.VerificationFailures,
	)
	if len(failures) > 0 {
		t.Logf("recv stress failure samples: %s", strings.Join(failures, " | "))
	}

	return outcome, observed, records, failures
}
