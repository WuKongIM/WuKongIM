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

	deliveryusecase "github.com/WuKongIM/WuKongIM/internal/usecase/delivery"
	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

func TestRecvStressThreeNode(t *testing.T) {
	selection := selectRecvStressThreeNodeRun(t)
	cfg := selection.cfg

	harness := newThreeNodeAppHarnessWithConfigMutator(t, func(appCfg *Config) {
		if selection.useAcceptancePreset {
			applySendPathTuning(t, appCfg, selection.preset.Tuning)
		}
	})
	leaderID := harness.waitForStableLeader(t, 1)
	leader := harness.apps[leaderID]

	targets := preloadRecvStressTargets(t, harness, leader, cfg, selection.minISR)
	assertRecvStressAcceptanceMinISR(t, harness, leader, targets, selection.minISR)
	outcome, observed, records, failures := runRecvStressWorkers(t, harness, leader, targets, cfg)
	verifyRecvStressCommittedRecords(t, harness, records)

	t.Logf("recv stress results: total=%d success=%d failed=%d error_rate=%.2f%% qps=%.2f p50=%s p95=%s p99=%s", outcome.Total, outcome.Success, outcome.Failed, outcome.ErrorRate(), observed.QPS, observed.P50, observed.P95, observed.P99)
	if len(failures) > 0 {
		t.Logf("recv stress failures: %s", strings.Join(failures, " | "))
	}

	require.NotZero(t, outcome.Total)
	require.Equal(t, outcome.Total, outcome.Success)
	require.Zero(t, outcome.Failed)
	require.Len(t, records, int(outcome.Success))
}

func assertRecvStressAcceptanceMinISR(t *testing.T, harness *threeNodeAppHarness, leader *App, targets []recvStressTarget, minISR int) {
	t.Helper()
	require.NotNil(t, harness)
	require.NotNil(t, leader)
	require.NotEmpty(t, targets)

	for _, target := range targets {
		meta, err := leader.Store().GetChannelRuntimeMeta(context.Background(), target.ChannelID, int64(target.ChannelType))
		require.NoError(t, err)
		require.EqualValues(t, minISR, meta.MinISR, "leader store should preserve MinISR for %s", target.ChannelID)

		for _, app := range harness.appsWithLeaderFirst(leader.cfg.Node.ID) {
			meta, err := app.Store().GetChannelRuntimeMeta(context.Background(), target.ChannelID, int64(target.ChannelType))
			require.NoError(t, err)
			require.EqualValues(t, minISR, meta.MinISR, "app %d should preserve MinISR for %s", app.cfg.Node.ID, target.ChannelID)
		}
	}
}

func preloadRecvStressTargets(t *testing.T, harness *threeNodeAppHarness, leader *App, cfg recvStressConfig, minISR int) []recvStressTarget {
	t.Helper()
	require.NotNil(t, harness)
	require.NotNil(t, leader)
	require.NotZero(t, cfg.Recipients)

	leaderID := leader.cfg.Node.ID
	targets := make([]recvStressTarget, 0, cfg.Recipients)
	for idx := 0; idx < cfg.Recipients; idx++ {
		senderUID := fmt.Sprintf("recv-stress-sender-%03d", idx)
		recipientUID := fmt.Sprintf("recv-stress-recipient-%03d", idx)
		channelID := deliveryusecase.EncodePersonChannel(senderUID, recipientUID)
		channelEpoch := uint64(8000 + idx)
		meta := metadb.ChannelRuntimeMeta{
			ChannelID:    channelID,
			ChannelType:  int64(frame.ChannelTypePerson),
			ChannelEpoch: channelEpoch,
			LeaderEpoch:  channelEpoch,
			Replicas:     []uint64{1, 2, 3},
			ISR:          []uint64{1, 2, 3},
			Leader:       leaderID,
			MinISR:       int64(minISR),
			Status:       uint8(channel.StatusActive),
			Features:     uint64(channel.MessageSeqFormatLegacyU32),
			LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
		}
		require.NoError(t, leader.Store().UpsertChannelRuntimeMeta(context.Background(), meta))

		id := channel.ChannelID{ID: channelID, Type: frame.ChannelTypePerson}
		for _, app := range harness.appsWithLeaderFirst(leaderID) {
			_, err := app.channelMetaSync.RefreshChannelMeta(context.Background(), id)
			require.NoError(t, err)
		}

		targets = append(targets, recvStressTarget{
			SenderUID:     senderUID,
			RecipientUID:  recipientUID,
			ChannelID:     channelID,
			ChannelType:   frame.ChannelTypePerson,
			OwnerNodeID:   leaderID,
			ConnectNodeID: uint64((idx % len(harness.apps)) + 1),
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
	verifyRecvStressCommittedRecords(t, harness, warmupRecords)

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

	send := func(ctx context.Context, cmd messageusecase.SendCommand) (messageusecase.SendResult, error) {
		return owner.Message().Send(ctx, cmd)
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
			workerOutcome, workerRecords, workerFailures := runRecvStressWorker(client, send, worker, cfg, deadline)
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
		"recv stress metrics: duration=%s workers=%d recipients=%d active_target_count=%d total=%d success=%d failed=%d qps=%.2f p50=%s p95=%s p99=%s max=%s verification_count=%d verification_failures=%d",
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

func verifyRecvStressCommittedRecords(t *testing.T, harness *threeNodeAppHarness, records []recvStressRecord) {
	t.Helper()
	sendRecords := make([]sendStressRecord, 0, len(records))
	for _, record := range records {
		sendRecords = append(sendRecords, sendStressRecord{
			Worker:        record.Worker,
			Iteration:     record.Iteration,
			SenderUID:     record.SenderUID,
			RecipientUID:  record.RecipientUID,
			ChannelID:     record.ChannelID,
			ChannelType:   record.ChannelType,
			ClientSeq:     record.ClientSeq,
			ClientMsgNo:   record.ClientMsgNo,
			Payload:       record.Payload,
			MessageID:     record.MessageID,
			MessageSeq:    record.MessageSeq,
			AckLatency:    record.RecvLatency,
			OwnerNodeID:   record.OwnerNodeID,
			ConnectNodeID: record.ConnectNodeID,
		})
	}
	verifySendStressCommittedRecords(t, harness, sendRecords)
}
