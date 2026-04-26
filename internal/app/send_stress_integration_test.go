//go:build integration
// +build integration

package app

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	deliveryusecase "github.com/WuKongIM/WuKongIM/internal/usecase/delivery"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

func TestSendStressThreeNode(t *testing.T) {
	selection := selectSendStressThreeNodeRun(t)
	cfg := selection.cfg
	preset := selection.preset

	harness := newThreeNodeAppHarnessWithConfigMutator(t, func(appCfg *Config) {
		if selection.useAcceptancePreset {
			applySendPathTuning(t, appCfg, preset)
		}
		if cfg.Mode == sendStressModeThroughput {
			appCfg.Gateway.DefaultSession.AsyncSendDispatch = true
		}
	})
	leaderID := harness.waitForStableLeader(t, 1)
	leader := harness.apps[leaderID]

	targets := preloadSendStressTargets(t, harness, leader, cfg, selection.minISR, sendStressScenarioMultiTarget)
	assertSendStressAcceptanceMinISR(t, harness, leader, targets, selection.minISR)
	outcome, observed, records, failures := runSendStressWorkers(t, harness, targets, cfg, sendStressScenarioMultiTarget)
	verifySendStressCommittedRecords(t, harness, records)
	if cfg.Mode == sendStressModeThroughput {
		t.Logf("send stress baseline artifacts: log=%s cpu=%s block=%s", sendStressBaselineLogPath, sendStressBaselineCPUPath, sendStressBaselineBlockPath)
		t.Logf("%s", compareSendStressBaseline(observed))
	}

	t.Logf("send stress results: total=%d success=%d failed=%d error_rate=%.2f%%", outcome.Total, outcome.Success, outcome.Failed, outcome.ErrorRate())
	if len(failures) > 0 {
		t.Logf("send stress failures: %s", strings.Join(failures, " | "))
	}

	require.NotZero(t, outcome.Total)
	require.Equal(t, outcome.Total, outcome.Success)
	require.Zero(t, outcome.Failed)
	require.Len(t, records, int(outcome.Success))
}

func TestSendStressSingleHotChannelThreeNode(t *testing.T) {
	selection := selectSendStressThreeNodeRun(t)
	cfg := selection.cfg
	preset := selection.preset

	harness := newThreeNodeAppHarnessWithConfigMutator(t, func(appCfg *Config) {
		if selection.useAcceptancePreset {
			applySendPathTuning(t, appCfg, preset)
		}
		if cfg.Mode == sendStressModeThroughput {
			appCfg.Gateway.DefaultSession.AsyncSendDispatch = true
		}
	})
	leaderID := harness.waitForStableLeader(t, 1)
	leader := harness.apps[leaderID]

	targets := preloadSendStressTargets(t, harness, leader, cfg, selection.minISR, sendStressScenarioSingleHotChannel)
	require.Equal(t, 1, sendStressUniqueTargetCount(targets))
	assertSendStressAcceptanceMinISR(t, harness, leader, targets, selection.minISR)
	outcome, observed, records, failures := runSendStressWorkers(t, harness, targets, cfg, sendStressScenarioSingleHotChannel)
	verifySendStressCommittedRecordsForScenario(t, harness, records, sendStressScenarioSingleHotChannel)
	if cfg.Mode == sendStressModeThroughput {
		artifacts := sendStressArtifactsForScenario(sendStressScenarioSingleHotChannel)
		t.Logf("send stress hot-channel artifacts: label=%s log=%s cpu=%s block=%s", artifacts.Label, artifacts.LogPath, artifacts.CPUPath, artifacts.BlockPath)
		t.Logf("%s", compareSendStressBaseline(observed))
	}

	t.Logf("send stress hot-channel results: total=%d success=%d failed=%d error_rate=%.2f%%", outcome.Total, outcome.Success, outcome.Failed, outcome.ErrorRate())
	if len(failures) > 0 {
		t.Logf("send stress hot-channel failures: %s", strings.Join(failures, " | "))
	}

	require.NotZero(t, outcome.Total)
	require.Equal(t, outcome.Total, outcome.Success)
	require.Zero(t, outcome.Failed)
	require.Len(t, records, int(outcome.Success))
}

func TestSendStressHotColdSkewThreeNode(t *testing.T) {
	selection := selectSendStressThreeNodeRun(t)
	cfg := selection.cfg
	preset := selection.preset

	harness := newThreeNodeAppHarnessWithConfigMutator(t, func(appCfg *Config) {
		if selection.useAcceptancePreset {
			applySendPathTuning(t, appCfg, preset)
		}
		if cfg.Mode == sendStressModeThroughput {
			appCfg.Gateway.DefaultSession.AsyncSendDispatch = true
		}
	})
	leaderID := harness.waitForStableLeader(t, 1)
	leader := harness.apps[leaderID]

	targets := preloadSendStressTargets(t, harness, leader, cfg, selection.minISR, sendStressScenarioHotColdSkew)
	require.Greater(t, sendStressUniqueTargetCount(targets), 1)
	assertSendStressAcceptanceMinISR(t, harness, leader, targets, selection.minISR)
	outcome, observed, records, failures := runSendStressWorkers(t, harness, targets, cfg, sendStressScenarioHotColdSkew)
	verifySendStressCommittedRecordsForScenario(t, harness, records, sendStressScenarioHotColdSkew)
	hotSummary, coldSummary := summarizeSendStressHotColdLatencies(records)
	if cfg.Mode == sendStressModeThroughput {
		artifacts := sendStressArtifactsForScenario(sendStressScenarioHotColdSkew)
		t.Logf("send stress hot-cold artifacts: label=%s log=%s cpu=%s block=%s", artifacts.Label, artifacts.LogPath, artifacts.CPUPath, artifacts.BlockPath)
		t.Logf("%s", compareSendStressBaseline(observed))
	}

	t.Logf(
		"send stress hot-cold results: total=%d success=%d failed=%d error_rate=%.2f%% hot_count=%d hot_p95=%s cold_count=%d cold_p95=%s",
		outcome.Total,
		outcome.Success,
		outcome.Failed,
		outcome.ErrorRate(),
		hotSummary.Count,
		hotSummary.P95,
		coldSummary.Count,
		coldSummary.P95,
	)
	if len(failures) > 0 {
		t.Logf("send stress hot-cold failures: %s", strings.Join(failures, " | "))
	}

	require.NotZero(t, hotSummary.Count)
	require.NotZero(t, coldSummary.Count)
	require.NotZero(t, outcome.Total)
	require.Equal(t, outcome.Total, outcome.Success)
	require.Zero(t, outcome.Failed)
	require.Len(t, records, int(outcome.Success))
}

func assertSendStressAcceptanceMinISR(t *testing.T, harness *threeNodeAppHarness, leader *App, targets []sendStressTarget, minISR int) {
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

func preloadSendStressTargets(t *testing.T, harness *threeNodeAppHarness, leader *App, cfg sendStressConfig, minISR int, scenario sendStressScenario) []sendStressTarget {
	t.Helper()
	require.NotNil(t, harness)
	require.NotNil(t, leader)
	require.NotZero(t, cfg.Senders)

	leaderID := leader.cfg.Node.ID
	targets := make([]sendStressTarget, 0, cfg.Senders)
	upsertMeta := func(channelID string, channelType uint8, channelEpoch uint64) {
		t.Helper()

		meta := metadb.ChannelRuntimeMeta{
			ChannelID:    channelID,
			ChannelType:  int64(channelType),
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

		id := channel.ChannelID{ID: channelID, Type: channelType}
		for _, app := range harness.appsWithLeaderFirst(leaderID) {
			_, err := app.channelMetaSync.RefreshChannelMeta(context.Background(), id)
			require.NoError(t, err)
		}
	}
	switch scenario {
	case "", sendStressScenarioMultiTarget:
		for idx := 0; idx < cfg.Senders; idx++ {
			senderUID := fmt.Sprintf("stress-sender-%03d", idx)
			recipientUID := fmt.Sprintf("stress-recipient-%03d", idx)
			channelID := deliveryusecase.EncodePersonChannel(senderUID, recipientUID)
			channelType := frame.ChannelTypePerson
			channelEpoch := uint64(1000 + idx)
			upsertMeta(channelID, channelType, channelEpoch)

			targets = append(targets, sendStressTarget{
				SenderUID:     senderUID,
				RecipientUID:  recipientUID,
				ChannelID:     channelID,
				ChannelType:   channelType,
				OwnerNodeID:   leaderID,
				ConnectNodeID: leaderID,
			})
		}
	case sendStressScenarioSingleHotChannel:
		channelID := "stress-hot-group-channel"
		channelType := frame.ChannelTypeGroup
		channelEpoch := uint64(5000)
		upsertMeta(channelID, channelType, channelEpoch)

		for idx := 0; idx < cfg.Senders; idx++ {
			senderUID := fmt.Sprintf("stress-sender-%03d", idx)
			recipientUID := fmt.Sprintf("stress-group-member-%03d", idx)
			targets = append(targets, sendStressTarget{
				SenderUID:     senderUID,
				RecipientUID:  recipientUID,
				ChannelID:     channelID,
				ChannelType:   channelType,
				OwnerNodeID:   leaderID,
				ConnectNodeID: leaderID,
			})
		}
	case sendStressScenarioHotColdSkew:
		hotChannelID := "stress-hot-group-channel"
		upsertMeta(hotChannelID, frame.ChannelTypeGroup, 5000)

		hotSenders := max(1, cfg.Senders/4)
		if hotSenders > 8 {
			hotSenders = 8
		}
		for idx := 0; idx < cfg.Senders; idx++ {
			senderUID := fmt.Sprintf("stress-sender-%03d", idx)
			if idx < hotSenders {
				recipientUID := fmt.Sprintf("stress-hot-member-%03d", idx)
				targets = append(targets, sendStressTarget{
					SenderUID:     senderUID,
					RecipientUID:  recipientUID,
					ChannelID:     hotChannelID,
					ChannelType:   frame.ChannelTypeGroup,
					OwnerNodeID:   leaderID,
					ConnectNodeID: leaderID,
				})
				continue
			}

			recipientUID := fmt.Sprintf("stress-cold-recipient-%03d", idx)
			channelID := deliveryusecase.EncodePersonChannel(senderUID, recipientUID)
			upsertMeta(channelID, frame.ChannelTypePerson, uint64(7000+idx))
			targets = append(targets, sendStressTarget{
				SenderUID:     senderUID,
				RecipientUID:  recipientUID,
				ChannelID:     channelID,
				ChannelType:   frame.ChannelTypePerson,
				OwnerNodeID:   leaderID,
				ConnectNodeID: leaderID,
			})
		}
	default:
		t.Fatalf("unknown send stress scenario %q", scenario)
	}
	return targets
}

func runSendStressWorkers(t *testing.T, harness *threeNodeAppHarness, targets []sendStressTarget, cfg sendStressConfig, scenario sendStressScenario) (sendStressOutcome, sendStressObservedMetrics, []sendStressRecord, []string) {
	t.Helper()
	require.NotNil(t, harness)
	require.NotEmpty(t, targets)
	require.NotZero(t, cfg.Workers)

	activeTargetCount := sendStressActiveTargetCount(cfg, len(targets))
	uniqueTargetCount := sendStressUniqueTargetCount(targets)
	require.Positive(t, activeTargetCount)

	clients := make([]sendStressWorkerClient, 0, activeTargetCount)
	for worker := 0; worker < activeTargetCount; worker++ {
		target := targets[worker]
		app := harness.apps[target.ConnectNodeID]
		require.NotNil(t, app, "connect node %d is not running", target.ConnectNodeID)
		conn := runSendStressClient(t, app, target.SenderUID, cfg)
		clients = append(clients, sendStressWorkerClient{
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

	warmupRecords := warmupSendStressClients(t, clients, cfg)
	verifySendStressCommittedRecords(t, harness, warmupRecords)

	var total atomic.Uint64
	var success atomic.Uint64
	var failed atomic.Uint64
	var verificationFailures atomic.Uint64
	var mu sync.Mutex
	records := make([]sendStressRecord, 0, activeTargetCount*cfg.MessagesPerWorker)
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
				workerOutcome  sendStressOutcome
				workerRecords  []sendStressRecord
				workerFailures []string
				err            error
			)
			if cfg.Mode == sendStressModeThroughput {
				workerOutcome, workerRecords, workerFailures, err = runSendStressWorkerThroughput(client, worker, cfg, deadline)
			} else {
				workerOutcome, workerRecords, workerFailures = runSendStressWorkerLatency(client, worker, cfg, deadline)
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
				latencies = append(latencies, record.AckLatency)
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
	outcome := sendStressOutcome{
		Total:   total.Load(),
		Success: success.Load(),
		Failed:  failed.Load(),
	}
	latencySummary := summarizeSendStressLatencies(latencies)
	qps := 0.0
	if elapsed > 0 {
		qps = float64(outcome.Success) / elapsed.Seconds()
	}
	observed := sendStressObservedMetrics{
		QPS:                  qps,
		P50:                  latencySummary.P50,
		P95:                  latencySummary.P95,
		P99:                  latencySummary.P99,
		VerificationCount:    len(records),
		VerificationFailures: int(verificationFailures.Load()),
	}
	t.Logf(
		"send stress metrics: scenario=%s mode=%s max_inflight=%d duration=%s workers=%d senders=%d active_target_count=%d unique_target_count=%d total=%d success=%d failed=%d qps=%.2f p50=%s p95=%s p99=%s max=%s verification_count=%d verification_failures=%d",
		scenario,
		cfg.Mode,
		cfg.MaxInflightPerWorker,
		elapsed,
		cfg.Workers,
		cfg.Senders,
		activeTargetCount,
		uniqueTargetCount,
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
		t.Logf("send stress failure samples: %s", strings.Join(failures, " | "))
	}

	return outcome, observed, records, failures
}

func verifySendStressCommittedRecordsForScenario(t *testing.T, harness *threeNodeAppHarness, records []sendStressRecord, scenario sendStressScenario) {
	t.Helper()
	switch scenario {
	case sendStressScenarioSingleHotChannel:
		verifySendStressCommittedRecordsHotChannel(t, harness, records)
	default:
		verifySendStressCommittedRecords(t, harness, records)
	}
}

func verifySendStressCommittedRecordsHotChannel(t *testing.T, harness *threeNodeAppHarness, records []sendStressRecord) {
	t.Helper()
	require.NotNil(t, harness)

	plan, err := buildSendStressVerificationPlan(records)
	require.NoError(t, err)

	for _, app := range harness.orderedApps() {
		require.NotNil(t, app, "replica is not running")
		failure := waitForSendStressReplicaRangeMatch(app, plan, sendStressReplicaVerifyTimeout)
		require.Emptyf(t, failure, "send stress durable verification node=%d failed: %s", app.cfg.Node.ID, failure)
	}

	t.Logf("send stress durable verification: verification_count=%d verification_failures=%d", len(plan.OrderedSeqs), 0)
}

func loadSendStressReplicaMessages(app *App, plan sendStressVerificationPlan) (map[uint64]channel.Message, uint64, error) {
	if len(plan.OrderedSeqs) == 0 {
		return map[uint64]channel.Message{}, 0, nil
	}
	if app == nil {
		return nil, 0, fmt.Errorf("replica is not running")
	}

	key := channelhandler.KeyFromChannelID(plan.ChannelID)
	handle, ok := app.ISRRuntime().Channel(key)
	if !ok {
		return nil, 0, fmt.Errorf("channel=%s/%d node=%d missing runtime", plan.ChannelID.ID, plan.ChannelID.Type, app.cfg.Node.ID)
	}

	state := handle.Status()
	maxSeq := plan.OrderedSeqs[len(plan.OrderedSeqs)-1]
	if !state.CommitReady {
		return nil, state.HW, fmt.Errorf("channel=%s/%d node=%d not commit ready", plan.ChannelID.ID, plan.ChannelID.Type, app.cfg.Node.ID)
	}
	if state.HW < maxSeq {
		return nil, state.HW, fmt.Errorf("channel=%s/%d node=%d committed_hw=%d below max_seq=%d", plan.ChannelID.ID, plan.ChannelID.Type, app.cfg.Node.ID, state.HW, maxSeq)
	}

	store := channelStoreForID(app.ChannelLogDB(), plan.ChannelID)
	msgs, err := channelhandler.LoadNextRangeMsgs(store, state.HW, plan.OrderedSeqs[0], maxSeq, 0)
	if err != nil {
		return nil, state.HW, err
	}

	loaded := make(map[uint64]channel.Message, len(msgs))
	for _, msg := range msgs {
		loaded[msg.MessageSeq] = msg
	}
	return loaded, state.HW, nil
}

func waitForSendStressReplicaRangeMatch(app *App, plan sendStressVerificationPlan, timeout time.Duration) string {
	if len(plan.OrderedSeqs) == 0 {
		return ""
	}

	deadline := time.Now().Add(timeout)
	lastFailure := "replica verification timed out before any read"
	for {
		loaded, committedHW, err := loadSendStressReplicaMessages(app, plan)
		if err != nil {
			lastFailure = err.Error()
		} else {
			lastFailure = sendStressReplicaRangeMismatch(app.cfg.Node.ID, plan, loaded, committedHW)
			if lastFailure == "" {
				return ""
			}
		}
		if time.Now().After(deadline) {
			return lastFailure
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func runSendStressWorkerLatency(client sendStressWorkerClient, worker int, cfg sendStressConfig, deadline time.Time) (sendStressOutcome, []sendStressRecord, []string) {
	records := make([]sendStressRecord, 0, cfg.MessagesPerWorker)
	failures := make([]string, 0, 1)
	outcome := sendStressOutcome{}

	for iteration := 0; iteration < cfg.MessagesPerWorker; iteration++ {
		if time.Now().After(deadline) {
			break
		}

		outcome.Total++
		clientSeq := uint64(iteration + 2)
		clientMsgNo := fmt.Sprintf("send-stress-%d-%s-%02d-%d", worker, client.target.SenderUID, iteration, cfg.Seed)
		payload := []byte(fmt.Sprintf("send-stress payload worker=%d sender=%s recipient=%s iteration=%d seed=%d", worker, client.target.SenderUID, client.target.RecipientUID, iteration, cfg.Seed))
		record, failure, ok := executeSendStressAttempt(client, worker, "measure", iteration, clientSeq, clientMsgNo, payload, cfg.AckTimeout)
		if !ok {
			outcome.Failed++
			failures = append(failures, failure)
			break
		}
		outcome.Success++
		records = append(records, record)
	}
	return outcome, records, failures
}

func warmupSendStressClients(t *testing.T, clients []sendStressWorkerClient, cfg sendStressConfig) []sendStressRecord {
	t.Helper()
	require.NotEmpty(t, clients)

	ackTimeout := cfg.AckTimeout
	if ackTimeout < sendStressWarmupAckTimeout {
		ackTimeout = sendStressWarmupAckTimeout
	}

	startedAt := time.Now()
	var wg sync.WaitGroup
	var mu sync.Mutex
	records := make([]sendStressRecord, 0, len(clients))
	failures := make([]string, 0, len(clients))
	for worker, client := range clients {
		worker := worker
		client := client
		wg.Add(1)
		go func() {
			defer wg.Done()

			clientMsgNo := fmt.Sprintf("send-stress-warmup-%d-%s-%d", worker, client.target.SenderUID, cfg.Seed)
			payload := []byte(fmt.Sprintf("send-stress warmup worker=%d sender=%s recipient=%s seed=%d", worker, client.target.SenderUID, client.target.RecipientUID, cfg.Seed))
			record, failure, ok := executeSendStressAttempt(client, worker, "warmup", -1, 1, clientMsgNo, payload, ackTimeout)

			mu.Lock()
			defer mu.Unlock()
			if ok {
				records = append(records, record)
				return
			}
			failures = append(failures, failure)
		}()
	}
	wg.Wait()

	t.Logf("send stress warmup: workers=%d success=%d failed=%d timeout=%s duration=%s", len(clients), len(records), len(failures), ackTimeout, time.Since(startedAt))
	if len(failures) > 0 {
		t.Fatalf("send stress warmup failures: %s", strings.Join(failures, " | "))
	}
	return records
}

func executeSendStressAttempt(client sendStressWorkerClient, worker int, phase string, iteration int, clientSeq uint64, clientMsgNo string, payload []byte, ackTimeout time.Duration) (sendStressRecord, string, bool) {
	packet := &frame.SendPacket{
		ChannelID:   client.target.sendPacketChannelID(),
		ChannelType: client.target.ChannelType,
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		Payload:     payload,
	}

	sendStart := time.Now()
	if err := writeSendStressClientFrame(client, packet, ackTimeout); err != nil {
		return sendStressRecord{}, fmt.Sprintf("worker=%d sender=%s connect_node=%d phase=%s iteration=%d write error=%v", worker, client.target.SenderUID, client.target.ConnectNodeID, phase, iteration, err), false
	}

	sendack, framesBeforeAck, err := waitForSendStressSendack(client, ackTimeout)
	ackLatency := time.Since(sendStart)
	if err != nil {
		return sendStressRecord{}, fmt.Sprintf("worker=%d sender=%s connect_node=%d phase=%s iteration=%d readack error=%v", worker, client.target.SenderUID, client.target.ConnectNodeID, phase, iteration, err), false
	}
	if sendack.ClientSeq != clientSeq || sendack.ClientMsgNo != clientMsgNo {
		return sendStressRecord{}, fmt.Sprintf("worker=%d sender=%s connect_node=%d phase=%s iteration=%d ack_mismatch client_seq=%d/%d client_msg_no=%s/%s", worker, client.target.SenderUID, client.target.ConnectNodeID, phase, iteration, sendack.ClientSeq, clientSeq, sendack.ClientMsgNo, clientMsgNo), false
	}
	if sendack.ReasonCode != frame.ReasonSuccess || sendack.MessageID == 0 || sendack.MessageSeq == 0 {
		return sendStressRecord{}, fmt.Sprintf("worker=%d sender=%s connect_node=%d phase=%s iteration=%d reason=%s message_id=%d message_seq=%d", worker, client.target.SenderUID, client.target.ConnectNodeID, phase, iteration, sendack.ReasonCode, sendack.MessageID, sendack.MessageSeq), false
	}

	return sendStressRecord{
		Worker:          worker,
		Iteration:       iteration,
		SenderUID:       client.target.SenderUID,
		RecipientUID:    client.target.RecipientUID,
		ChannelID:       client.target.ChannelID,
		ChannelType:     client.target.ChannelType,
		ClientSeq:       clientSeq,
		ClientMsgNo:     clientMsgNo,
		Payload:         payload,
		MessageID:       sendack.MessageID,
		MessageSeq:      sendack.MessageSeq,
		AckLatency:      ackLatency,
		OwnerNodeID:     client.target.OwnerNodeID,
		ConnectNodeID:   client.target.ConnectNodeID,
		FramesBeforeAck: append([]string(nil), framesBeforeAck...),
	}, "", true
}

func verifySendStressCommittedRecords(t *testing.T, harness *threeNodeAppHarness, records []sendStressRecord) {
	t.Helper()
	require.NotNil(t, harness)

	verificationCount := 0
	for _, record := range records {
		id := channel.ChannelID{
			ID:   record.ChannelID,
			Type: record.ChannelType,
		}
		owner := harness.apps[record.OwnerNodeID]
		require.NotNil(t, owner, "owner node %d is not running", record.OwnerNodeID)

		ownerMsg := waitForAppCommittedMessage(t, owner, id, record.MessageSeq, 5*time.Second)
		require.Equalf(t, record.Payload, ownerMsg.Payload, "owner mismatch worker=%d iteration=%d sender=%s recipient=%s connect_node=%d message_seq=%d message_id=%d client_seq=%d client_msg_no=%s frames_before_ack=%v owner_from=%s owner_client_msg_no=%s owner_payload=%q", record.Worker, record.Iteration, record.SenderUID, record.RecipientUID, record.ConnectNodeID, record.MessageSeq, record.MessageID, record.ClientSeq, record.ClientMsgNo, record.FramesBeforeAck, ownerMsg.FromUID, ownerMsg.ClientMsgNo, string(ownerMsg.Payload))
		require.Equalf(t, record.SenderUID, ownerMsg.FromUID, "owner sender mismatch worker=%d iteration=%d message_seq=%d client_msg_no=%s frames_before_ack=%v owner_from=%s owner_payload=%q", record.Worker, record.Iteration, record.MessageSeq, record.ClientMsgNo, record.FramesBeforeAck, ownerMsg.FromUID, string(ownerMsg.Payload))
		require.Equalf(t, record.ClientMsgNo, ownerMsg.ClientMsgNo, "owner client_msg_no mismatch worker=%d iteration=%d message_seq=%d client_msg_no=%s frames_before_ack=%v owner_client_msg_no=%s owner_payload=%q", record.Worker, record.Iteration, record.MessageSeq, record.ClientMsgNo, record.FramesBeforeAck, ownerMsg.ClientMsgNo, string(ownerMsg.Payload))
		require.Equalf(t, record.ChannelID, ownerMsg.ChannelID, "owner channel mismatch worker=%d iteration=%d message_seq=%d client_msg_no=%s", record.Worker, record.Iteration, record.MessageSeq, record.ClientMsgNo)
		require.Equalf(t, record.ChannelType, ownerMsg.ChannelType, "owner channel type mismatch worker=%d iteration=%d message_seq=%d client_msg_no=%s", record.Worker, record.Iteration, record.MessageSeq, record.ClientMsgNo)

		for _, app := range harness.orderedApps() {
			msg := waitForAppCommittedMessage(t, app, id, record.MessageSeq, 5*time.Second)
			require.Equalf(t, record.Payload, msg.Payload, "replica mismatch node=%d worker=%d iteration=%d sender=%s recipient=%s connect_node=%d message_seq=%d message_id=%d client_seq=%d client_msg_no=%s frames_before_ack=%v replica_from=%s replica_client_msg_no=%s replica_payload=%q", app.cfg.Node.ID, record.Worker, record.Iteration, record.SenderUID, record.RecipientUID, record.ConnectNodeID, record.MessageSeq, record.MessageID, record.ClientSeq, record.ClientMsgNo, record.FramesBeforeAck, msg.FromUID, msg.ClientMsgNo, string(msg.Payload))
			require.Equalf(t, record.SenderUID, msg.FromUID, "replica sender mismatch node=%d worker=%d iteration=%d message_seq=%d client_msg_no=%s", app.cfg.Node.ID, record.Worker, record.Iteration, record.MessageSeq, record.ClientMsgNo)
			require.Equalf(t, record.ClientMsgNo, msg.ClientMsgNo, "replica client_msg_no mismatch node=%d worker=%d iteration=%d message_seq=%d client_msg_no=%s replica_client_msg_no=%s", app.cfg.Node.ID, record.Worker, record.Iteration, record.MessageSeq, record.ClientMsgNo, msg.ClientMsgNo)
			require.Equalf(t, record.ChannelID, msg.ChannelID, "replica channel mismatch node=%d worker=%d iteration=%d message_seq=%d client_msg_no=%s", app.cfg.Node.ID, record.Worker, record.Iteration, record.MessageSeq, record.ClientMsgNo)
			require.Equalf(t, record.ChannelType, msg.ChannelType, "replica channel type mismatch node=%d worker=%d iteration=%d message_seq=%d client_msg_no=%s", app.cfg.Node.ID, record.Worker, record.Iteration, record.MessageSeq, record.ClientMsgNo)
			require.Equalf(t, record.MessageSeq, msg.MessageSeq, "replica message_seq mismatch node=%d worker=%d iteration=%d client_msg_no=%s", app.cfg.Node.ID, record.Worker, record.Iteration, record.ClientMsgNo)
		}
		verificationCount++
	}

	t.Logf("send stress durable verification: verification_count=%d verification_failures=%d", verificationCount, 0)
}

func runSendStressClient(t *testing.T, app *App, senderUID string, cfg sendStressConfig) net.Conn {
	t.Helper()
	require.NotNil(t, app)
	require.NotEmpty(t, senderUID)
	require.NotZero(t, cfg.DialTimeout)

	conn, err := dialSendStressClient(app, senderUID, cfg)
	require.NoError(t, err)
	return conn
}

func dialSendStressClient(app *App, senderUID string, cfg sendStressConfig) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", app.Gateway().ListenerAddr("tcp-wkproto"), cfg.DialTimeout)
	if err != nil {
		return nil, err
	}

	if err := writeSendStressFrame(conn, &frame.ConnectPacket{
		Version:         frame.LatestVersion,
		UID:             senderUID,
		DeviceID:        senderUID + "-stress-device",
		DeviceFlag:      frame.APP,
		ClientTimestamp: time.Now().UnixMilli(),
	}, cfg.DialTimeout); err != nil {
		_ = conn.Close()
		return nil, err
	}

	f, err := readSendStressFrameWithin(conn, cfg.AckTimeout)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	connack, ok := f.(*frame.ConnackPacket)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("expected *frame.ConnackPacket, got %T", f)
	}
	if connack.ReasonCode != frame.ReasonSuccess {
		_ = conn.Close()
		return nil, fmt.Errorf("connect failed with reason %s", connack.ReasonCode)
	}
	return conn, nil
}

func waitForSendStressSendack(client sendStressWorkerClient, timeout time.Duration) (*frame.SendackPacket, []string, error) {
	deadline := time.Now().Add(timeout)
	framesBeforeAck := make([]string, 0, 2)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, framesBeforeAck, fmt.Errorf("timed out waiting for sendack")
		}

		f, err := readSendStressClientFrame(client, remaining)
		if err != nil {
			return nil, framesBeforeAck, err
		}

		switch pkt := f.(type) {
		case *frame.SendackPacket:
			return pkt, framesBeforeAck, nil
		case *frame.RecvPacket:
			framesBeforeAck = append(framesBeforeAck, fmt.Sprintf("recv(message_id=%d,message_seq=%d,client_seq=%d,client_msg_no=%s,payload=%q)", pkt.MessageID, pkt.MessageSeq, pkt.ClientSeq, pkt.ClientMsgNo, string(pkt.Payload)))
			if err := writeSendStressClientFrame(client, &frame.RecvackPacket{
				MessageID:  pkt.MessageID,
				MessageSeq: pkt.MessageSeq,
			}, remaining); err != nil {
				return nil, framesBeforeAck, err
			}
		default:
			return nil, framesBeforeAck, fmt.Errorf("unexpected frame while waiting for sendack: %T", f)
		}
	}
}
