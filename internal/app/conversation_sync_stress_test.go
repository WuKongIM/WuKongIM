//go:build integration
// +build integration

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	deliveryusecase "github.com/WuKongIM/WuKongIM/internal/usecase/delivery"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

const (
	conversationSyncStressEnv                   = "WK_CONVERSATION_SYNC_STRESS"
	conversationSyncStressDurationEnv           = "WK_CONVERSATION_SYNC_STRESS_DURATION"
	conversationSyncStressWorkersEnv            = "WK_CONVERSATION_SYNC_STRESS_WORKERS"
	conversationSyncStressUIDsEnv               = "WK_CONVERSATION_SYNC_STRESS_UIDS"
	conversationSyncStressChannelsPerUIDEnv     = "WK_CONVERSATION_SYNC_STRESS_CHANNELS_PER_UID"
	conversationSyncStressMessagesPerChannelEnv = "WK_CONVERSATION_SYNC_STRESS_MESSAGES_PER_CHANNEL"
	conversationSyncStressSyncLimitEnv          = "WK_CONVERSATION_SYNC_STRESS_SYNC_LIMIT"
	conversationSyncStressMsgCountEnv           = "WK_CONVERSATION_SYNC_STRESS_MSG_COUNT"
	conversationSyncStressWriteEveryEnv         = "WK_CONVERSATION_SYNC_STRESS_WRITE_EVERY"
	conversationSyncStressSeedEnv               = "WK_CONVERSATION_SYNC_STRESS_SEED"
)

type conversationSyncStressConfig struct {
	Enabled            bool
	Duration           time.Duration
	Workers            int
	UIDs               int
	ChannelsPerUID     int
	MessagesPerChannel int
	SyncLimit          int
	MsgCount           int
	WriteEvery         int
	Seed               int64
}

type conversationSyncLatencySummary struct {
	Count int
	P50   time.Duration
	P95   time.Duration
	P99   time.Duration
	Max   time.Duration
}

type conversationSyncStressOutcome struct {
	SyncTotal    uint64
	SyncSuccess  uint64
	SyncFailed   uint64
	WriteSuccess uint64
	WriteFailed  uint64
}

func (o conversationSyncStressOutcome) SyncErrorRate() float64 {
	if o.SyncTotal == 0 {
		return 0
	}
	return float64(o.SyncFailed) * 100 / float64(o.SyncTotal)
}

func (o conversationSyncStressOutcome) WriteErrorRate() float64 {
	total := o.WriteSuccess + o.WriteFailed
	if total == 0 {
		return 0
	}
	return float64(o.WriteFailed) * 100 / float64(total)
}

type conversationSyncStressChannel struct {
	UID         string
	PeerUID     string
	ChannelID   string
	ChannelType uint8
	OwnerNodeID uint64
	Replicas    []uint64
}

type conversationSyncStressRequest struct {
	UID         string `json:"uid"`
	Version     int64  `json:"version,omitempty"`
	LastMsgSeqs string `json:"last_msg_seqs,omitempty"`
	MsgCount    int    `json:"msg_count,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

type conversationSyncStressResponseItem struct {
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
	LastMsgSeq  uint32 `json:"last_msg_seq"`
	Version     int64  `json:"version"`
}

type conversationSyncStressKey struct {
	ChannelID   string
	ChannelType uint8
}

type conversationSyncStressUIDState struct {
	mu          sync.Mutex
	version     int64
	lastMsgSeqs map[conversationSyncStressKey]uint64
}

func TestConversationSyncStressConfigDefaultsAndOverrides(t *testing.T) {
	t.Setenv("WK_CONVERSATION_SYNC_STRESS", "1")
	t.Setenv("WK_CONVERSATION_SYNC_STRESS_DURATION", "1500ms")
	t.Setenv("WK_CONVERSATION_SYNC_STRESS_WORKERS", "7")
	t.Setenv("WK_CONVERSATION_SYNC_STRESS_UIDS", "24")
	t.Setenv("WK_CONVERSATION_SYNC_STRESS_CHANNELS_PER_UID", "9")
	t.Setenv("WK_CONVERSATION_SYNC_STRESS_MESSAGES_PER_CHANNEL", "4")
	t.Setenv("WK_CONVERSATION_SYNC_STRESS_SYNC_LIMIT", "12")
	t.Setenv("WK_CONVERSATION_SYNC_STRESS_MSG_COUNT", "3")
	t.Setenv("WK_CONVERSATION_SYNC_STRESS_WRITE_EVERY", "5")
	t.Setenv("WK_CONVERSATION_SYNC_STRESS_SEED", "42")

	cfg := loadConversationSyncStressConfig(t)
	if !cfg.Enabled {
		t.Fatalf("Enabled = false, want true")
	}
	if cfg.Duration != 1500*time.Millisecond {
		t.Fatalf("Duration = %s, want %s", cfg.Duration, 1500*time.Millisecond)
	}
	if cfg.Workers != 7 || cfg.UIDs != 24 || cfg.ChannelsPerUID != 9 || cfg.MessagesPerChannel != 4 {
		t.Fatalf("cfg = %#v", cfg)
	}
	if cfg.SyncLimit != 12 || cfg.MsgCount != 3 || cfg.WriteEvery != 5 || cfg.Seed != 42 {
		t.Fatalf("cfg = %#v", cfg)
	}
}

func TestConversationSyncStressLatencySummaryPercentiles(t *testing.T) {
	summary := summarizeConversationSyncLatencies([]time.Duration{
		90 * time.Millisecond,
		10 * time.Millisecond,
		70 * time.Millisecond,
		30 * time.Millisecond,
		50 * time.Millisecond,
	})

	if summary.Count != 5 {
		t.Fatalf("Count = %d, want 5", summary.Count)
	}
	if summary.P50 != 50*time.Millisecond {
		t.Fatalf("P50 = %s, want %s", summary.P50, 50*time.Millisecond)
	}
	if summary.P95 != 90*time.Millisecond {
		t.Fatalf("P95 = %s, want %s", summary.P95, 90*time.Millisecond)
	}
	if summary.P99 != 90*time.Millisecond {
		t.Fatalf("P99 = %s, want %s", summary.P99, 90*time.Millisecond)
	}
	if summary.Max != 90*time.Millisecond {
		t.Fatalf("Max = %s, want %s", summary.Max, 90*time.Millisecond)
	}
}

func TestSendConversationSyncStressMessageFallsBackToReplicaAfterNotLeader(t *testing.T) {
	t.Helper()

	var calls []uint64
	err := sendConversationSyncStressMessage(context.Background(), []uint64{2, 3}, 2, func(nodeID uint64) message.SendCommand {
		return message.SendCommand{ClientMsgNo: fmt.Sprintf("node-%d", nodeID)}
	}, func(_ context.Context, nodeID uint64, cmd message.SendCommand) error {
		calls = append(calls, nodeID)
		if nodeID == 2 {
			return channel.ErrNotLeader
		}
		if nodeID == 3 && cmd.ClientMsgNo == "node-3" {
			return nil
		}
		return fmt.Errorf("unexpected node %d command %+v", nodeID, cmd)
	})
	require.NoError(t, err)
	require.Equal(t, []uint64{2, 3}, calls)
}

func TestConversationSyncStressOutcomeSeparatesSyncAndWriteErrorRates(t *testing.T) {
	outcome := conversationSyncStressOutcome{
		SyncTotal:    12,
		SyncSuccess:  12,
		SyncFailed:   0,
		WriteSuccess: 8,
		WriteFailed:  2,
	}

	require.Equal(t, 0.0, outcome.SyncErrorRate())
	require.InDelta(t, 20.0, outcome.WriteErrorRate(), 0.001)
}

func TestConversationSyncStressThreeNode(t *testing.T) {
	cfg := loadConversationSyncStressConfig(t)
	requireConversationSyncStressEnabled(t, cfg)

	harness := newThreeNodeConversationSyncHarness(t)
	apiNode := harness.apps[1]
	groupLeaderID := harness.waitForStableLeader(t, 1)
	groupLeader := harness.apps[groupLeaderID]

	uidStates, channelsByUID, uidOrder := preloadConversationSyncStressData(t, harness, groupLeader, cfg)

	client := &http.Client{Timeout: 5 * time.Second}
	baseURL := "http://" + apiNode.API().Addr()

	for _, uid := range uidOrder {
		state := uidStates[uid]
		require.Eventually(t, func() bool {
			items, err := postConversationSyncStress(client, baseURL, state.snapshot(uid, cfg.SyncLimit, cfg.MsgCount))
			if err != nil {
				return false
			}
			if len(items) == 0 {
				return false
			}
			if err := validateConversationSyncStressResponse(items); err != nil {
				return false
			}
			state.apply(items)
			return true
		}, 8*time.Second, 50*time.Millisecond, "uid %s did not become visible in sync", uid)
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()

	var (
		wg            sync.WaitGroup
		syncTotal     atomic.Uint64
		syncSuccess   atomic.Uint64
		syncFailed    atomic.Uint64
		writeSuccess  atomic.Uint64
		writeFailed   atomic.Uint64
		msgCounter    atomic.Uint64
		latencyCh     = make(chan time.Duration, cfg.Workers*64)
		failureMu     sync.Mutex
		failureNotes  []string
		writeFailures []string
	)

	recordSyncFailure := func(err error) {
		syncFailed.Add(1)
		failureMu.Lock()
		if len(failureNotes) < 8 {
			failureNotes = append(failureNotes, err.Error())
		}
		failureMu.Unlock()
	}

	recordWriteFailure := func(err error) {
		writeFailed.Add(1)
		failureMu.Lock()
		if len(writeFailures) < 8 {
			writeFailures = append(writeFailures, err.Error())
		}
		failureMu.Unlock()
	}

	for worker := 0; worker < cfg.Workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()

			rng := rand.New(rand.NewSource(cfg.Seed + int64(worker)))
			for iter := 0; ; iter++ {
				if ctx.Err() != nil {
					return
				}

				uid := uidOrder[rng.Intn(len(uidOrder))]
				state := uidStates[uid]
				channels := channelsByUID[uid]

				if cfg.WriteEvery > 0 && iter%cfg.WriteEvery == 0 {
					channel := channels[rng.Intn(len(channels))]
					seq := msgCounter.Add(1)
					err := sendConversationSyncStressMessage(context.Background(), channel.Replicas, channel.OwnerNodeID, func(nodeID uint64) message.SendCommand {
						return message.SendCommand{
							FromUID:     channel.PeerUID,
							ChannelID:   channel.UID,
							ChannelType: channel.ChannelType,
							ClientMsgNo: fmt.Sprintf("conversation-sync-stress-live-%d-%d-%d-%d", worker, iter, nodeID, seq),
							Payload:     []byte(fmt.Sprintf("stress-live-%d", seq)),
						}
					}, func(ctx context.Context, nodeID uint64, cmd message.SendCommand) error {
						_, err := harness.apps[nodeID].Message().Send(ctx, cmd)
						return err
					})
					if err != nil {
						recordWriteFailure(fmt.Errorf("worker %d live write uid=%s peer=%s: %w", worker, channel.UID, channel.PeerUID, err))
						continue
					}
					writeSuccess.Add(1)
				}

				req := state.snapshot(uid, cfg.SyncLimit, cfg.MsgCount)
				syncTotal.Add(1)
				began := time.Now()
				items, err := postConversationSyncStress(client, baseURL, req)
				latency := time.Since(began)
				if err != nil {
					recordSyncFailure(fmt.Errorf("worker %d sync uid=%s: %w", worker, uid, err))
					continue
				}
				if err := validateConversationSyncStressResponse(items); err != nil {
					recordSyncFailure(fmt.Errorf("worker %d sync uid=%s invalid response: %w", worker, uid, err))
					continue
				}
				state.apply(items)
				syncSuccess.Add(1)
				latencyCh <- latency
			}
		}(worker)
	}

	wg.Wait()
	close(latencyCh)

	latencies := make([]time.Duration, 0, syncSuccess.Load())
	for latency := range latencyCh {
		latencies = append(latencies, latency)
	}
	elapsed := time.Since(start)
	summary := summarizeConversationSyncLatencies(latencies)
	outcome := conversationSyncStressOutcome{
		SyncTotal:    syncTotal.Load(),
		SyncSuccess:  syncSuccess.Load(),
		SyncFailed:   syncFailed.Load(),
		WriteSuccess: writeSuccess.Load(),
		WriteFailed:  writeFailed.Load(),
	}

	t.Logf(
		"conversation sync stress config: duration=%s workers=%d uids=%d channels_per_uid=%d messages_per_channel=%d limit=%d msg_count=%d write_every=%d seed=%d",
		cfg.Duration,
		cfg.Workers,
		cfg.UIDs,
		cfg.ChannelsPerUID,
		cfg.MessagesPerChannel,
		cfg.SyncLimit,
		cfg.MsgCount,
		cfg.WriteEvery,
		cfg.Seed,
	)
	t.Logf(
		"conversation sync stress results: sync_total=%d sync_success=%d sync_failed=%d write_success=%d write_failed=%d qps=%.1f sync_error_rate=%.2f%% write_error_rate=%.2f%% p50=%s p95=%s p99=%s max=%s",
		outcome.SyncTotal,
		outcome.SyncSuccess,
		outcome.SyncFailed,
		outcome.WriteSuccess,
		outcome.WriteFailed,
		float64(outcome.SyncSuccess)/elapsed.Seconds(),
		outcome.SyncErrorRate(),
		outcome.WriteErrorRate(),
		summary.P50,
		summary.P95,
		summary.P99,
		summary.Max,
	)
	if len(failureNotes) > 0 {
		t.Logf("conversation sync stress request failures: %s", strings.Join(failureNotes, " | "))
	}
	if len(writeFailures) > 0 {
		t.Logf("conversation sync stress live write failures: %s", strings.Join(writeFailures, " | "))
	}

	require.NotZero(t, outcome.SyncTotal, "stress test produced no sync requests")
	require.NotZero(t, outcome.SyncSuccess, "stress test produced no successful sync requests")
	require.Zero(t, outcome.SyncFailed, "stress test observed sync request failures")
	require.NotZero(t, outcome.WriteSuccess, "stress test produced no successful live writes")
}

func loadConversationSyncStressConfig(t *testing.T) conversationSyncStressConfig {
	t.Helper()

	cfg := conversationSyncStressConfig{
		Enabled:            envBool(conversationSyncStressEnv, false),
		Duration:           envDuration(t, conversationSyncStressDurationEnv, 5*time.Second),
		Workers:            envInt(t, conversationSyncStressWorkersEnv, max(4, runtime.GOMAXPROCS(0))),
		UIDs:               envInt(t, conversationSyncStressUIDsEnv, 18),
		ChannelsPerUID:     envInt(t, conversationSyncStressChannelsPerUIDEnv, 6),
		MessagesPerChannel: envInt(t, conversationSyncStressMessagesPerChannelEnv, 3),
		SyncLimit:          envInt(t, conversationSyncStressSyncLimitEnv, 32),
		MsgCount:           envInt(t, conversationSyncStressMsgCountEnv, 1),
		WriteEvery:         envInt(t, conversationSyncStressWriteEveryEnv, 6),
		Seed:               envInt64(t, conversationSyncStressSeedEnv, 20260408),
	}

	if cfg.Workers <= 0 {
		t.Fatalf("%s must be > 0, got %d", conversationSyncStressWorkersEnv, cfg.Workers)
	}
	if cfg.UIDs <= 0 {
		t.Fatalf("%s must be > 0, got %d", conversationSyncStressUIDsEnv, cfg.UIDs)
	}
	if cfg.ChannelsPerUID <= 0 {
		t.Fatalf("%s must be > 0, got %d", conversationSyncStressChannelsPerUIDEnv, cfg.ChannelsPerUID)
	}
	if cfg.MessagesPerChannel <= 0 {
		t.Fatalf("%s must be > 0, got %d", conversationSyncStressMessagesPerChannelEnv, cfg.MessagesPerChannel)
	}
	if cfg.SyncLimit <= 0 {
		t.Fatalf("%s must be > 0, got %d", conversationSyncStressSyncLimitEnv, cfg.SyncLimit)
	}
	if cfg.MsgCount < 0 {
		t.Fatalf("%s must be >= 0, got %d", conversationSyncStressMsgCountEnv, cfg.MsgCount)
	}
	if cfg.WriteEvery < 0 {
		t.Fatalf("%s must be >= 0, got %d", conversationSyncStressWriteEveryEnv, cfg.WriteEvery)
	}
	if cfg.Duration <= 0 {
		t.Fatalf("%s must be > 0, got %s", conversationSyncStressDurationEnv, cfg.Duration)
	}
	return cfg
}

func requireConversationSyncStressEnabled(t *testing.T, cfg conversationSyncStressConfig) {
	t.Helper()
	if !cfg.Enabled {
		t.Skip("set WK_CONVERSATION_SYNC_STRESS=1 to enable conversation sync stress test")
	}
}

func summarizeConversationSyncLatencies(latencies []time.Duration) conversationSyncLatencySummary {
	if len(latencies) == 0 {
		return conversationSyncLatencySummary{}
	}

	sorted := append([]time.Duration(nil), latencies...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})
	return conversationSyncLatencySummary{
		Count: len(sorted),
		P50:   percentileDuration(sorted, 0.50),
		P95:   percentileDuration(sorted, 0.95),
		P99:   percentileDuration(sorted, 0.99),
		Max:   sorted[len(sorted)-1],
	}
}

func percentileDuration(sorted []time.Duration, pct float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(float64(len(sorted))*pct)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func preloadConversationSyncStressData(t *testing.T, harness *threeNodeAppHarness, groupLeader *App, cfg conversationSyncStressConfig) (map[string]*conversationSyncStressUIDState, map[string][]conversationSyncStressChannel, []string) {
	t.Helper()

	uidStates := make(map[string]*conversationSyncStressUIDState, cfg.UIDs)
	channelsByUID := make(map[string][]conversationSyncStressChannel, cfg.UIDs)
	uidOrder := make([]string, 0, cfg.UIDs)

	var channelEpoch uint64 = 1000
	for uidIdx := 0; uidIdx < cfg.UIDs; uidIdx++ {
		uid := fmt.Sprintf("stress-user-%03d", uidIdx)
		uidOrder = append(uidOrder, uid)
		uidStates[uid] = newConversationSyncStressUIDState()

		for chIdx := 0; chIdx < cfg.ChannelsPerUID; chIdx++ {
			peerUID := fmt.Sprintf("stress-peer-%03d-%02d", uidIdx, chIdx)
			channelID := deliveryusecase.EncodePersonChannel(peerUID, uid)
			ownerNodeID := uint64(2 + ((uidIdx + chIdx) % 2))
			meta := metadb.ChannelRuntimeMeta{
				ChannelID:    channelID,
				ChannelType:  int64(frame.ChannelTypePerson),
				ChannelEpoch: channelEpoch,
				LeaderEpoch:  channelEpoch,
				Replicas:     []uint64{2, 3},
				ISR:          []uint64{2, 3},
				Leader:       ownerNodeID,
				MinISR:       1,
				Status:       uint8(channel.StatusActive),
				Features:     uint64(channel.MessageSeqFormatLegacyU32),
				LeaseUntilMS: time.Now().Add(time.Minute).UnixMilli(),
			}
			require.NoError(t, groupLeader.Store().UpsertChannelRuntimeMeta(context.Background(), meta))

			id := channel.ChannelID{ID: channelID, Type: frame.ChannelTypePerson}
			for _, nodeID := range []uint64{2, 3} {
				_, err := harness.apps[nodeID].channelMetaSync.RefreshChannelMeta(context.Background(), id)
				require.NoError(t, err)
			}

			channel := conversationSyncStressChannel{
				UID:         uid,
				PeerUID:     peerUID,
				ChannelID:   channelID,
				ChannelType: frame.ChannelTypePerson,
				OwnerNodeID: ownerNodeID,
				Replicas:    []uint64{2, 3},
			}
			channelsByUID[uid] = append(channelsByUID[uid], channel)

			for msgIdx := 0; msgIdx < cfg.MessagesPerChannel; msgIdx++ {
				_, err := harness.apps[ownerNodeID].Message().Send(context.Background(), message.SendCommand{
					FromUID:     peerUID,
					ChannelID:   uid,
					ChannelType: frame.ChannelTypePerson,
					ClientMsgNo: fmt.Sprintf("conversation-sync-stress-preload-%03d-%02d-%02d", uidIdx, chIdx, msgIdx),
					Payload:     []byte(fmt.Sprintf("preload-%03d-%02d-%02d", uidIdx, chIdx, msgIdx)),
				})
				require.NoError(t, err)
			}
			channelEpoch++
		}
	}
	return uidStates, channelsByUID, uidOrder
}

func newConversationSyncStressUIDState() *conversationSyncStressUIDState {
	return &conversationSyncStressUIDState{
		lastMsgSeqs: make(map[conversationSyncStressKey]uint64),
	}
}

func (s *conversationSyncStressUIDState) snapshot(uid string, limit, msgCount int) conversationSyncStressRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	req := conversationSyncStressRequest{
		UID:      uid,
		Version:  s.version,
		MsgCount: msgCount,
		Limit:    limit,
	}
	if len(s.lastMsgSeqs) == 0 {
		return req
	}

	keys := make([]conversationSyncStressKey, 0, len(s.lastMsgSeqs))
	for key := range s.lastMsgSeqs {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].ChannelID == keys[j].ChannelID {
			return keys[i].ChannelType < keys[j].ChannelType
		}
		return keys[i].ChannelID < keys[j].ChannelID
	})

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d:%d", key.ChannelID, key.ChannelType, s.lastMsgSeqs[key]))
	}
	req.LastMsgSeqs = strings.Join(parts, "|")
	return req
}

func (s *conversationSyncStressUIDState) apply(items []conversationSyncStressResponseItem) {
	if len(items) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, item := range items {
		if item.Version > s.version {
			s.version = item.Version
		}
		key := conversationSyncStressKey{
			ChannelID:   item.ChannelID,
			ChannelType: item.ChannelType,
		}
		if seq := uint64(item.LastMsgSeq); seq > s.lastMsgSeqs[key] {
			s.lastMsgSeqs[key] = seq
		}
	}
}

func sendConversationSyncStressMessage(ctx context.Context, replicas []uint64, ownerNodeID uint64, build func(nodeID uint64) message.SendCommand, send func(context.Context, uint64, message.SendCommand) error) error {
	order := make([]uint64, 0, len(replicas)+1)
	if ownerNodeID != 0 {
		order = append(order, ownerNodeID)
	}
	for _, nodeID := range replicas {
		if nodeID == 0 || nodeID == ownerNodeID {
			continue
		}
		order = append(order, nodeID)
	}

	var lastErr error
	for _, nodeID := range order {
		cmd := build(nodeID)
		err := send(ctx, nodeID, cmd)
		if err == nil {
			return nil
		}
		if errors.Is(err, channel.ErrNotLeader) {
			lastErr = err
			continue
		}
		return err
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("no replicas available for stress message")
}

func postConversationSyncStress(client *http.Client, baseURL string, req conversationSyncStressRequest) ([]conversationSyncStressResponseItem, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequest(http.MethodPost, baseURL+"/conversation/sync", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("post request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var payload bytes.Buffer
		_, _ = payload.ReadFrom(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(payload.String()))
	}

	var items []conversationSyncStressResponseItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return items, nil
}

func validateConversationSyncStressResponse(items []conversationSyncStressResponseItem) error {
	for idx, item := range items {
		if item.ChannelID == "" {
			return fmt.Errorf("item %d has empty channel_id", idx)
		}
		if item.ChannelType == 0 {
			return fmt.Errorf("item %d has zero channel_type", idx)
		}
		if item.LastMsgSeq == 0 {
			return fmt.Errorf("item %d has zero last_msg_seq", idx)
		}
		if item.Version == 0 {
			return fmt.Errorf("item %d has zero version", idx)
		}
	}
	return nil
}
