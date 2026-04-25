package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway/testkit"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	codec "github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

const (
	sendStressEnv                  = "WK_SEND_STRESS"
	sendStressModeEnv              = "WK_SEND_STRESS_MODE"
	sendStressDurationEnv          = "WK_SEND_STRESS_DURATION"
	sendStressWorkersEnv           = "WK_SEND_STRESS_WORKERS"
	sendStressSendersEnv           = "WK_SEND_STRESS_SENDERS"
	sendStressMessagesPerWorkerEnv = "WK_SEND_STRESS_MESSAGES_PER_WORKER"
	sendStressMaxInflightEnv       = "WK_SEND_STRESS_MAX_INFLIGHT_PER_WORKER"
	sendStressDialTimeoutEnv       = "WK_SEND_STRESS_DIAL_TIMEOUT"
	sendStressAckTimeoutEnv        = "WK_SEND_STRESS_ACK_TIMEOUT"
	sendStressSeedEnv              = "WK_SEND_STRESS_SEED"
	sendStressWarmupAckTimeout     = 12 * time.Second
	sendStressThroughputInflight   = 32
	sendStressReplicaVerifyTimeout = 20 * time.Second
)

type sendStressMode string

const (
	sendStressModeLatency    sendStressMode = "latency"
	sendStressModeThroughput sendStressMode = "throughput"
)

type sendStressConfig struct {
	Enabled              bool
	Mode                 sendStressMode
	MaxInflightPerWorker int
	Duration             time.Duration
	Workers              int
	Senders              int
	MessagesPerWorker    int
	DialTimeout          time.Duration
	AckTimeout           time.Duration
	Seed                 int64
}

type sendStressLatencySummary struct {
	Count int
	P50   time.Duration
	P95   time.Duration
	P99   time.Duration
	Max   time.Duration
}

type sendStressObservedMetrics struct {
	Label                string
	QPS                  float64
	P50                  time.Duration
	P95                  time.Duration
	P99                  time.Duration
	VerificationCount    int
	VerificationFailures int
}

type sendStressTarget struct {
	SenderUID     string
	RecipientUID  string
	ChannelID     string
	ChannelType   uint8
	OwnerNodeID   uint64
	ConnectNodeID uint64
}

func (t sendStressTarget) sendPacketChannelID() string {
	if t.ChannelType == frame.ChannelTypeGroup {
		return t.ChannelID
	}
	return t.RecipientUID
}

type sendStressRecord struct {
	Worker          int
	Iteration       int
	SenderUID       string
	RecipientUID    string
	ChannelID       string
	ChannelType     uint8
	ClientSeq       uint64
	ClientMsgNo     string
	Payload         []byte
	MessageID       int64
	MessageSeq      uint64
	AckLatency      time.Duration
	OwnerNodeID     uint64
	ConnectNodeID   uint64
	FramesBeforeAck []string
}

type sendStressOutcome struct {
	Total   uint64
	Success uint64
	Failed  uint64
}

type sendStressScenario string

const (
	sendStressScenarioMultiTarget      sendStressScenario = "multi-target"
	sendStressScenarioSingleHotChannel sendStressScenario = "single-hot-channel"
	sendStressScenarioHotColdSkew      sendStressScenario = "hot-cold-skew"
)

func (s sendStressScenario) String() string {
	if s == "" {
		return string(sendStressScenarioMultiTarget)
	}
	return string(s)
}

type sendStressArtifactSet struct {
	Label     string
	LogPath   string
	CPUPath   string
	BlockPath string
}

type sendStressTargetKey struct {
	ChannelID   string
	ChannelType uint8
}

type sendStressVerificationPlan struct {
	ChannelID     channel.ChannelID
	OrderedSeqs   []uint64
	ExpectedBySeq map[uint64]sendStressRecord
}

type sendStressWorkerClient struct {
	target  sendStressTarget
	conn    net.Conn
	reader  *sendStressFrameReader
	writeMu *sync.Mutex
}

type sendStressAttemptResult struct {
	record  sendStressRecord
	failure string
	ok      bool
}

type sendStressPendingAttempt struct {
	client      sendStressWorkerClient
	worker      int
	phase       string
	iteration   int
	clientSeq   uint64
	clientMsgNo string
	payload     []byte
	startedAt   time.Time
	ch          chan sendStressAttemptResult
	onComplete  func(sendStressAttemptResult)
}

type sendStressInflightTracker struct {
	mu      sync.Mutex
	pending map[uint64]*sendStressPendingAttempt
}

type sendStressFrameReader struct {
	conn    net.Conn
	codec   codec.Protocol
	buf     []byte
	scratch []byte
}

func newSendStressFrameReader(conn net.Conn) *sendStressFrameReader {
	return &sendStressFrameReader{
		conn:    conn,
		codec:   codec.New(),
		scratch: make([]byte, 4096),
	}
}

func (r *sendStressFrameReader) ReadWithin(timeout time.Duration) (frame.Frame, error) {
	if r == nil || r.conn == nil {
		return nil, fmt.Errorf("send stress frame reader: nil connection")
	}
	deadline := time.Now().Add(timeout)
	for {
		if len(r.buf) > 0 {
			f, size, err := r.codec.DecodeFrame(r.buf, frame.LatestVersion)
			if err != nil {
				return nil, err
			}
			if f != nil && size > 0 {
				copy(r.buf, r.buf[size:])
				r.buf = r.buf[:len(r.buf)-size]
				return f, nil
			}
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, &net.OpError{Err: os.ErrDeadlineExceeded}
		}
		if err := r.conn.SetReadDeadline(time.Now().Add(remaining)); err != nil {
			return nil, err
		}
		n, err := r.conn.Read(r.scratch)
		_ = r.conn.SetReadDeadline(time.Time{})
		if n > 0 {
			r.buf = append(r.buf, r.scratch[:n]...)
		}
		if err != nil {
			if isSendStressTimeout(err) {
				continue
			}
			return nil, err
		}
	}
}

func (o sendStressOutcome) ErrorRate() float64 {
	if o.Total == 0 {
		return 0
	}
	return float64(o.Failed) * 100 / float64(o.Total)
}

func loadSendStressConfig(t *testing.T) sendStressConfig {
	t.Helper()

	cfg, err := loadSendStressConfigFromEnv(os.LookupEnv)
	require.NoError(t, err)
	return cfg
}

func loadSendStressConfigFromEnv(lookup func(string) (string, bool)) (sendStressConfig, error) {
	enabled, ok, err := parseSendStressEnabled(lookupEnvValue(lookup, sendStressEnv))
	if err != nil {
		return sendStressConfig{}, fmt.Errorf("parse %s: %w", sendStressEnv, err)
	}
	if !ok {
		enabled = false
	}
	mode, ok, err := parseSendStressMode(lookupEnvValue(lookup, sendStressModeEnv))
	if err != nil {
		return sendStressConfig{}, fmt.Errorf("parse %s: %w", sendStressModeEnv, err)
	}
	if !ok {
		mode = sendStressModeLatency
	}

	duration, err := sendStressEnvDuration(lookup, sendStressDurationEnv, 5*time.Second)
	if err != nil {
		return sendStressConfig{}, err
	}
	workers, err := sendStressEnvInt(lookup, sendStressWorkersEnv, max(4, runtime.GOMAXPROCS(0)))
	if err != nil {
		return sendStressConfig{}, err
	}
	messagesPerWorker, err := sendStressEnvInt(lookup, sendStressMessagesPerWorkerEnv, 50)
	if err != nil {
		return sendStressConfig{}, err
	}
	dialTimeout, err := sendStressEnvDuration(lookup, sendStressDialTimeoutEnv, 3*time.Second)
	if err != nil {
		return sendStressConfig{}, err
	}
	ackTimeout, err := sendStressEnvDuration(lookup, sendStressAckTimeoutEnv, 5*time.Second)
	if err != nil {
		return sendStressConfig{}, err
	}
	seed, err := sendStressEnvInt64(lookup, sendStressSeedEnv, 20260408)
	if err != nil {
		return sendStressConfig{}, err
	}

	cfg := sendStressConfig{
		Enabled:              enabled,
		Mode:                 mode,
		Duration:             duration,
		Workers:              workers,
		MessagesPerWorker:    messagesPerWorker,
		DialTimeout:          dialTimeout,
		AckTimeout:           ackTimeout,
		Seed:                 seed,
		MaxInflightPerWorker: 1,
	}
	if cfg.Mode == sendStressModeThroughput {
		maxInflight, err := sendStressEnvInt(lookup, sendStressMaxInflightEnv, sendStressThroughputInflight)
		if err != nil {
			return sendStressConfig{}, err
		}
		cfg.MaxInflightPerWorker = maxInflight
	}
	if value, ok := lookup(sendStressSendersEnv); !ok || strings.TrimSpace(value) == "" {
		cfg.Senders = max(8, cfg.Workers)
	} else {
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return sendStressConfig{}, fmt.Errorf("parse %s: %w", sendStressSendersEnv, err)
		}
		cfg.Senders = parsed
	}
	if err := validateSendStressConfig(cfg); err != nil {
		return sendStressConfig{}, err
	}
	return cfg, nil
}

func lookupEnvValue(lookup func(string) (string, bool), name string) string {
	value, _ := lookup(name)
	return value
}

func sendStressEnvDuration(lookup func(string) (string, bool), name string, fallback time.Duration) (time.Duration, error) {
	value, ok := lookup(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}

func sendStressEnvInt(lookup func(string) (string, bool), name string, fallback int) (int, error) {
	value, ok := lookup(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}

func sendStressEnvInt64(lookup func(string) (string, bool), name string, fallback int64) (int64, error) {
	value, ok := lookup(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}

func validateSendStressConfig(cfg sendStressConfig) error {
	switch cfg.Mode {
	case "", sendStressModeLatency:
		cfg.Mode = sendStressModeLatency
	case sendStressModeThroughput:
	default:
		return fmt.Errorf("%s must be one of %q or %q, got %q", sendStressModeEnv, sendStressModeLatency, sendStressModeThroughput, cfg.Mode)
	}
	if cfg.Workers <= 0 {
		return fmt.Errorf("%s must be > 0, got %d", sendStressWorkersEnv, cfg.Workers)
	}
	if cfg.Senders <= 0 {
		return fmt.Errorf("%s must be > 0, got %d", sendStressSendersEnv, cfg.Senders)
	}
	if cfg.Senders < cfg.Workers {
		return fmt.Errorf("%s must be >= %s, got %d < %d", sendStressSendersEnv, sendStressWorkersEnv, cfg.Senders, cfg.Workers)
	}
	if cfg.MessagesPerWorker <= 0 {
		return fmt.Errorf("%s must be > 0, got %d", sendStressMessagesPerWorkerEnv, cfg.MessagesPerWorker)
	}
	if cfg.Duration <= 0 {
		return fmt.Errorf("%s must be > 0, got %s", sendStressDurationEnv, cfg.Duration)
	}
	if cfg.DialTimeout <= 0 {
		return fmt.Errorf("%s must be > 0, got %s", sendStressDialTimeoutEnv, cfg.DialTimeout)
	}
	if cfg.AckTimeout <= 0 {
		return fmt.Errorf("%s must be > 0, got %s", sendStressAckTimeoutEnv, cfg.AckTimeout)
	}
	if cfg.Mode == sendStressModeThroughput && cfg.MaxInflightPerWorker <= 0 {
		return fmt.Errorf("%s must be > 0, got %d", sendStressMaxInflightEnv, cfg.MaxInflightPerWorker)
	}
	if cfg.Mode != sendStressModeThroughput {
		cfg.MaxInflightPerWorker = 1
	}
	return nil
}

func parseSendStressEnabled(value string) (bool, bool, error) {
	if strings.TrimSpace(value) == "" {
		return false, false, nil
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true, true, nil
	case "0", "false", "no", "off":
		return false, true, nil
	default:
		return false, true, strconv.ErrSyntax
	}
}

func parseSendStressMode(value string) (sendStressMode, bool, error) {
	if strings.TrimSpace(value) == "" {
		return "", false, nil
	}
	switch sendStressMode(strings.ToLower(strings.TrimSpace(value))) {
	case sendStressModeLatency:
		return sendStressModeLatency, true, nil
	case sendStressModeThroughput:
		return sendStressModeThroughput, true, nil
	default:
		return "", true, strconv.ErrSyntax
	}
}

func newSendStressInflightTracker() *sendStressInflightTracker {
	return &sendStressInflightTracker{
		pending: make(map[uint64]*sendStressPendingAttempt),
	}
}

func (t *sendStressInflightTracker) Start(client sendStressWorkerClient, worker int, phase string, iteration int, clientSeq uint64, clientMsgNo string, payload []byte) <-chan sendStressAttemptResult {
	return t.startAt(client, worker, phase, iteration, clientSeq, clientMsgNo, payload, time.Now(), nil)
}

func (t *sendStressInflightTracker) startAt(client sendStressWorkerClient, worker int, phase string, iteration int, clientSeq uint64, clientMsgNo string, payload []byte, startedAt time.Time, onComplete func(sendStressAttemptResult)) <-chan sendStressAttemptResult {
	ch := make(chan sendStressAttemptResult, 1)

	t.mu.Lock()
	t.pending[clientSeq] = &sendStressPendingAttempt{
		client:      client,
		worker:      worker,
		phase:       phase,
		iteration:   iteration,
		clientSeq:   clientSeq,
		clientMsgNo: clientMsgNo,
		payload:     payload,
		startedAt:   startedAt,
		ch:          ch,
		onComplete:  onComplete,
	}
	t.mu.Unlock()
	return ch
}

func (t *sendStressInflightTracker) Complete(sendack *frame.SendackPacket, framesBeforeAck []string) error {
	if sendack == nil {
		return fmt.Errorf("send stress inflight tracker: nil sendack")
	}

	t.mu.Lock()
	attempt, ok := t.pending[sendack.ClientSeq]
	if ok {
		delete(t.pending, sendack.ClientSeq)
	}
	t.mu.Unlock()
	if !ok {
		return fmt.Errorf("send stress inflight tracker: unexpected sendack client_seq=%d", sendack.ClientSeq)
	}
	if sendack.ClientMsgNo != attempt.clientMsgNo {
		t.finish(attempt, sendStressAttemptResult{
			failure: fmt.Sprintf("worker=%d sender=%s connect_node=%d phase=%s iteration=%d ack_mismatch client_seq=%d client_msg_no=%s/%s", attempt.worker, attempt.client.target.SenderUID, attempt.client.target.ConnectNodeID, attempt.phase, attempt.iteration, sendack.ClientSeq, sendack.ClientMsgNo, attempt.clientMsgNo),
		})
		return nil
	}
	if sendack.ReasonCode != frame.ReasonSuccess || sendack.MessageID == 0 || sendack.MessageSeq == 0 {
		t.finish(attempt, sendStressAttemptResult{
			failure: fmt.Sprintf("worker=%d sender=%s connect_node=%d phase=%s iteration=%d reason=%s message_id=%d message_seq=%d", attempt.worker, attempt.client.target.SenderUID, attempt.client.target.ConnectNodeID, attempt.phase, attempt.iteration, sendack.ReasonCode, sendack.MessageID, sendack.MessageSeq),
		})
		return nil
	}

	t.finish(attempt, sendStressAttemptResult{
		ok: true,
		record: sendStressRecord{
			Worker:          attempt.worker,
			Iteration:       attempt.iteration,
			SenderUID:       attempt.client.target.SenderUID,
			RecipientUID:    attempt.client.target.RecipientUID,
			ChannelID:       attempt.client.target.ChannelID,
			ChannelType:     attempt.client.target.ChannelType,
			ClientSeq:       attempt.clientSeq,
			ClientMsgNo:     attempt.clientMsgNo,
			Payload:         attempt.payload,
			MessageID:       sendack.MessageID,
			MessageSeq:      sendack.MessageSeq,
			AckLatency:      time.Since(attempt.startedAt),
			OwnerNodeID:     attempt.client.target.OwnerNodeID,
			ConnectNodeID:   attempt.client.target.ConnectNodeID,
			FramesBeforeAck: append([]string(nil), framesBeforeAck...),
		},
	})
	return nil
}

func (t *sendStressInflightTracker) Fail(clientSeq uint64, failure string) error {
	t.mu.Lock()
	attempt, ok := t.pending[clientSeq]
	if ok {
		delete(t.pending, clientSeq)
	}
	t.mu.Unlock()
	if !ok {
		return fmt.Errorf("send stress inflight tracker: missing client_seq=%d", clientSeq)
	}
	t.finish(attempt, sendStressAttemptResult{failure: failure})
	return nil
}

func (t *sendStressInflightTracker) FailAll(format string, args ...any) {
	t.mu.Lock()
	pending := make([]*sendStressPendingAttempt, 0, len(t.pending))
	for clientSeq, attempt := range t.pending {
		delete(t.pending, clientSeq)
		pending = append(pending, attempt)
	}
	t.mu.Unlock()

	failure := fmt.Sprintf(format, args...)
	for _, attempt := range pending {
		t.finish(attempt, sendStressAttemptResult{failure: failure})
	}
}

func (t *sendStressInflightTracker) Pending() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.pending)
}

func (t *sendStressInflightTracker) finish(attempt *sendStressPendingAttempt, result sendStressAttemptResult) {
	attempt.ch <- result
	close(attempt.ch)
	if attempt.onComplete != nil {
		attempt.onComplete(result)
	}
}

func requireSendStressEnabled(t *testing.T, cfg sendStressConfig) {
	t.Helper()
	if !cfg.Enabled {
		t.Skip("set WK_SEND_STRESS=1 to enable send stress test")
	}
}

func summarizeSendStressLatencies(latencies []time.Duration) sendStressLatencySummary {
	if len(latencies) == 0 {
		return sendStressLatencySummary{}
	}

	sorted := append([]time.Duration(nil), latencies...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})
	return sendStressLatencySummary{
		Count: len(sorted),
		P50:   percentileSendStressDuration(sorted, 0.50),
		P95:   percentileSendStressDuration(sorted, 0.95),
		P99:   percentileSendStressDuration(sorted, 0.99),
		Max:   sorted[len(sorted)-1],
	}
}

func percentileSendStressDuration(sorted []time.Duration, pct float64) time.Duration {
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

const (
	sendStressBaselineLogPath   = "/tmp/send-stress-postfix.log"
	sendStressBaselineCPUPath   = "tmp/profiles/send-stress-postfix.cpu.out"
	sendStressBaselineBlockPath = "tmp/profiles/send-stress-postfix.block.out"
)

var sendStressTransport5000Baseline = sendStressObservedMetrics{
	Label:                "2026-04-19-send-stress-postfix",
	QPS:                  4210.29,
	P50:                  476435459 * time.Nanosecond,
	P95:                  962106917 * time.Nanosecond,
	P99:                  1109378583 * time.Nanosecond,
	VerificationFailures: 0,
}

func compareSendStressBaseline(observed sendStressObservedMetrics) string {
	guardrailStatus := "fail"
	if observed.P95 <= time.Duration(float64(sendStressTransport5000Baseline.P95)*1.15) {
		guardrailStatus = "pass"
	}
	return fmt.Sprintf(
		"baseline=%s baseline_qps=%.2f baseline_p50=%s baseline_p95=%s baseline_p99=%s qps_delta=%+.2f%% p50_delta=%+.2f%% p95_delta=%+.2f%% p99_delta=%+.2f%% verification_count=%d verification_failures=%d p95_guardrail=%s",
		sendStressTransport5000Baseline.Label,
		sendStressTransport5000Baseline.QPS,
		sendStressTransport5000Baseline.P50,
		sendStressTransport5000Baseline.P95,
		sendStressTransport5000Baseline.P99,
		percentDelta(observed.QPS, sendStressTransport5000Baseline.QPS),
		percentDelta(float64(observed.P50), float64(sendStressTransport5000Baseline.P50)),
		percentDelta(float64(observed.P95), float64(sendStressTransport5000Baseline.P95)),
		percentDelta(float64(observed.P99), float64(sendStressTransport5000Baseline.P99)),
		observed.VerificationCount,
		observed.VerificationFailures,
		guardrailStatus,
	)
}

func sendStressArtifactsForScenario(scenario sendStressScenario) sendStressArtifactSet {
	switch scenario {
	case sendStressScenarioSingleHotChannel:
		return sendStressArtifactSet{
			Label:     "2026-04-19-send-stress-single-hot-channel",
			LogPath:   "/tmp/send-stress-single-hot-channel.log",
			CPUPath:   "tmp/profiles/send-stress-single-hot-channel.cpu.out",
			BlockPath: "tmp/profiles/send-stress-single-hot-channel.block.out",
		}
	case sendStressScenarioHotColdSkew:
		return sendStressArtifactSet{
			Label:     "2026-04-20-send-stress-hot-cold-skew",
			LogPath:   "/tmp/send-stress-hot-cold-skew.log",
			CPUPath:   "tmp/profiles/send-stress-hot-cold-skew.cpu.out",
			BlockPath: "tmp/profiles/send-stress-hot-cold-skew.block.out",
		}
	default:
		return sendStressArtifactSet{
			Label:     "2026-04-19-send-stress-postfix",
			LogPath:   sendStressBaselineLogPath,
			CPUPath:   sendStressBaselineCPUPath,
			BlockPath: sendStressBaselineBlockPath,
		}
	}
}

func percentDelta(observed, baseline float64) float64 {
	if baseline == 0 {
		return 0
	}
	return (observed - baseline) * 100 / baseline
}

func sendStressActiveTargetCount(cfg sendStressConfig, totalTargets int) int {
	if totalTargets <= 0 {
		return 0
	}
	if cfg.Mode == sendStressModeThroughput {
		return totalTargets
	}
	if cfg.Workers <= 0 {
		return 0
	}
	return min(cfg.Workers, totalTargets)
}

func summarizeSendStressHotColdLatencies(records []sendStressRecord) (sendStressLatencySummary, sendStressLatencySummary) {
	hotLatencies := make([]time.Duration, 0, len(records))
	coldLatencies := make([]time.Duration, 0, len(records))
	for _, record := range records {
		if record.ChannelID == "stress-hot-group-channel" {
			hotLatencies = append(hotLatencies, record.AckLatency)
			continue
		}
		coldLatencies = append(coldLatencies, record.AckLatency)
	}
	return summarizeSendStressLatencies(hotLatencies), summarizeSendStressLatencies(coldLatencies)
}

func sendStressUniqueTargetCount(targets []sendStressTarget) int {
	if len(targets) == 0 {
		return 0
	}
	unique := make(map[sendStressTargetKey]struct{}, len(targets))
	for _, target := range targets {
		unique[sendStressTargetKey{
			ChannelID:   target.ChannelID,
			ChannelType: target.ChannelType,
		}] = struct{}{}
	}
	return len(unique)
}

func TestSendStressConfigDefaultsAndOverrides(t *testing.T) {
	acceptance := sendStressAcceptancePreset()
	clearSendStressConfigEnv(t)
	defaultCfg := loadSendStressConfig(t)
	require.False(t, defaultCfg.Enabled)
	require.Equal(t, sendStressModeLatency, defaultCfg.Mode)
	require.Equal(t, 5*time.Second, defaultCfg.Duration)
	require.Equal(t, max(4, runtime.GOMAXPROCS(0)), defaultCfg.Workers)
	require.Equal(t, max(8, defaultCfg.Workers), defaultCfg.Senders)
	require.Equal(t, 50, defaultCfg.MessagesPerWorker)
	require.Equal(t, 1, defaultCfg.MaxInflightPerWorker)
	require.Equal(t, 3*time.Second, defaultCfg.DialTimeout)
	require.Equal(t, 5*time.Second, defaultCfg.AckTimeout)

	t.Run("acceptance preset overrides", func(t *testing.T) {
		clearSendStressConfigEnv(t)
		applySendStressAcceptanceConfigEnv(t, acceptance.Benchmark)

		cfg := loadSendStressConfig(t)
		require.True(t, cfg.Enabled)
		require.Equal(t, acceptance.Benchmark.Mode, cfg.Mode)
		require.Equal(t, acceptance.Benchmark.Duration, cfg.Duration)
		require.Equal(t, acceptance.Benchmark.Workers, cfg.Workers)
		require.Equal(t, acceptance.Benchmark.Senders, cfg.Senders)
		require.Equal(t, acceptance.Benchmark.MessagesPerWorker, cfg.MessagesPerWorker)
		require.Equal(t, acceptance.Benchmark.MaxInflightPerWorker, cfg.MaxInflightPerWorker)
		require.Equal(t, acceptance.Benchmark.DialTimeout, cfg.DialTimeout)
		require.Equal(t, acceptance.Benchmark.AckTimeout, cfg.AckTimeout)
		require.EqualValues(t, acceptance.Benchmark.Seed, cfg.Seed)
	})

	enabled, ok, err := parseSendStressEnabled("")
	require.NoError(t, err)
	require.False(t, enabled)
	require.False(t, ok)

	enabled, ok, err = parseSendStressEnabled("maybe")
	require.Error(t, err)
	require.True(t, ok)
	require.False(t, enabled)

	err = validateSendStressConfig(sendStressConfig{
		Workers:           0,
		Senders:           1,
		MessagesPerWorker: 1,
		Duration:          time.Second,
		DialTimeout:       time.Second,
		AckTimeout:        time.Second,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), sendStressWorkersEnv)

	err = validateSendStressConfig(sendStressConfig{
		Workers:           2,
		Senders:           1,
		MessagesPerWorker: 1,
		Duration:          time.Second,
		DialTimeout:       time.Second,
		AckTimeout:        time.Second,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), sendStressSendersEnv)

	err = validateSendStressConfig(sendStressConfig{
		Workers:           2,
		Senders:           2,
		MessagesPerWorker: 0,
		Duration:          time.Second,
		DialTimeout:       time.Second,
		AckTimeout:        time.Second,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), sendStressMessagesPerWorkerEnv)

}

func TestLoadSendStressConfigRejectsInvalidEnvWithoutSubprocess(t *testing.T) {
	env := map[string]string{
		sendStressEnv:        "1",
		sendStressWorkersEnv: "0",
		sendStressSendersEnv: "1",
	}

	_, err := loadSendStressConfigFromEnv(func(name string) (string, bool) {
		value, ok := env[name]
		return value, ok
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), sendStressWorkersEnv)
}

func TestSelectSendStressThreeNodeRunUsesAcceptancePresetWhenOnlyOptedIn(t *testing.T) {
	clearSendStressConfigEnv(t)
	t.Setenv(sendStressEnv, "1")

	selection := selectSendStressThreeNodeRun(t)
	preset := sendStressAcceptancePreset()

	require.True(t, selection.useAcceptancePreset)
	require.Equal(t, preset.Benchmark.Mode, selection.cfg.Mode)
	require.Equal(t, preset.Benchmark.Duration, selection.cfg.Duration)
	require.Equal(t, preset.Benchmark.Workers, selection.cfg.Workers)
	require.Equal(t, preset.Benchmark.Senders, selection.cfg.Senders)
	require.Equal(t, preset.Benchmark.MessagesPerWorker, selection.cfg.MessagesPerWorker)
	require.Equal(t, preset.Benchmark.MaxInflightPerWorker, selection.cfg.MaxInflightPerWorker)
	require.Equal(t, preset.Benchmark.DialTimeout, selection.cfg.DialTimeout)
	require.Equal(t, preset.Benchmark.AckTimeout, selection.cfg.AckTimeout)
	require.EqualValues(t, preset.Benchmark.Seed, selection.cfg.Seed)
	require.Equal(t, preset.MinISR, selection.minISR)
	expected := preset.Benchmark
	expected.Enabled = true
	require.Equal(t, expected, selection.cfg)
}

func TestSelectSendStressThreeNodeRunPreservesExplicitEnvSelection(t *testing.T) {
	clearSendStressConfigEnv(t)
	t.Setenv(sendStressEnv, "1")
	t.Setenv(sendStressModeEnv, string(sendStressModeThroughput))
	t.Setenv(sendStressDurationEnv, "21s")
	t.Setenv(sendStressWorkersEnv, "9")
	t.Setenv(sendStressSendersEnv, "11")
	t.Setenv(sendStressMessagesPerWorkerEnv, "13")
	t.Setenv(sendStressMaxInflightEnv, "17")
	t.Setenv(sendStressDialTimeoutEnv, "4s")
	t.Setenv(sendStressAckTimeoutEnv, "5s")
	t.Setenv(sendStressSeedEnv, "42")

	selection := selectSendStressThreeNodeRun(t)

	require.False(t, selection.useAcceptancePreset)
	require.Equal(t, sendStressModeThroughput, selection.cfg.Mode)
	require.Equal(t, 21*time.Second, selection.cfg.Duration)
	require.Equal(t, 9, selection.cfg.Workers)
	require.Equal(t, 11, selection.cfg.Senders)
	require.Equal(t, 13, selection.cfg.MessagesPerWorker)
	require.Equal(t, 17, selection.cfg.MaxInflightPerWorker)
	require.Equal(t, 4*time.Second, selection.cfg.DialTimeout)
	require.Equal(t, 5*time.Second, selection.cfg.AckTimeout)
	require.EqualValues(t, 42, selection.cfg.Seed)
	require.Equal(t, 3, selection.minISR)
}

func TestSelectSendStressThreeNodeRunUsesAcceptancePresetWhenExplicitEnvMatchesPreset(t *testing.T) {
	clearSendStressConfigEnv(t)
	preset := sendStressAcceptancePreset()

	t.Setenv(sendStressEnv, "1")
	t.Setenv(sendStressModeEnv, string(preset.Benchmark.Mode))
	t.Setenv(sendStressDurationEnv, preset.Benchmark.Duration.String())
	t.Setenv(sendStressWorkersEnv, strconv.Itoa(preset.Benchmark.Workers))
	t.Setenv(sendStressSendersEnv, strconv.Itoa(preset.Benchmark.Senders))
	t.Setenv(sendStressMessagesPerWorkerEnv, strconv.Itoa(preset.Benchmark.MessagesPerWorker))
	t.Setenv(sendStressMaxInflightEnv, strconv.Itoa(preset.Benchmark.MaxInflightPerWorker))
	t.Setenv(sendStressDialTimeoutEnv, preset.Benchmark.DialTimeout.String())
	t.Setenv(sendStressAckTimeoutEnv, preset.Benchmark.AckTimeout.String())
	t.Setenv(sendStressSeedEnv, strconv.FormatInt(preset.Benchmark.Seed, 10))

	selection := selectSendStressThreeNodeRun(t)

	require.True(t, selection.useAcceptancePreset)
	require.Equal(t, preset.MinISR, selection.minISR)
	expected := preset.Benchmark
	expected.Enabled = true
	require.Equal(t, expected, selection.cfg)
}

func TestSendStressConfigDefaultsToLatencyMode(t *testing.T) {
	clearSendStressConfigEnv(t)

	cfg := loadSendStressConfig(t)

	require.Equal(t, sendStressModeLatency, cfg.Mode)
	require.Equal(t, 1, cfg.MaxInflightPerWorker)
}

func TestSendStressConfigParsesThroughputModeAndInflightOverride(t *testing.T) {
	clearSendStressConfigEnv(t)
	t.Setenv(sendStressModeEnv, string(sendStressModeThroughput))
	t.Setenv(sendStressMaxInflightEnv, "7")

	cfg := loadSendStressConfig(t)

	require.Equal(t, sendStressModeThroughput, cfg.Mode)
	require.Equal(t, 7, cfg.MaxInflightPerWorker)
}

func TestSendStressConfigDefaultsThroughputModeToMultiInflight(t *testing.T) {
	clearSendStressConfigEnv(t)
	t.Setenv(sendStressModeEnv, string(sendStressModeThroughput))

	cfg := loadSendStressConfig(t)

	require.Equal(t, sendStressModeThroughput, cfg.Mode)
	require.Equal(t, sendStressThroughputInflight, cfg.MaxInflightPerWorker)
}

func TestValidateSendStressConfigRejectsInvalidThroughputInflight(t *testing.T) {
	err := validateSendStressConfig(sendStressConfig{
		Mode:                 sendStressModeThroughput,
		MaxInflightPerWorker: 0,
		Workers:              2,
		Senders:              2,
		MessagesPerWorker:    1,
		Duration:             time.Second,
		DialTimeout:          time.Second,
		AckTimeout:           time.Second,
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), sendStressMaxInflightEnv)
}

func TestSendStressLatencySummaryPercentiles(t *testing.T) {
	summary := summarizeSendStressLatencies([]time.Duration{
		90 * time.Millisecond,
		10 * time.Millisecond,
		70 * time.Millisecond,
		30 * time.Millisecond,
		50 * time.Millisecond,
	})

	require.Equal(t, 5, summary.Count)
	require.Equal(t, 50*time.Millisecond, summary.P50)
	require.Equal(t, 90*time.Millisecond, summary.P95)
	require.Equal(t, 90*time.Millisecond, summary.P99)
	require.Equal(t, 90*time.Millisecond, summary.Max)
}

func TestSendStressBenchmarkComparisonUsesPinnedTransportBaseline(t *testing.T) {
	got := compareSendStressBaseline(sendStressObservedMetrics{
		QPS:                  5005.5,
		P50:                  430 * time.Millisecond,
		P95:                  980 * time.Millisecond,
		P99:                  1100 * time.Millisecond,
		VerificationCount:    1600,
		VerificationFailures: 0,
	})
	require.Equal(t, "baseline=2026-04-19-send-stress-postfix baseline_qps=4210.29 baseline_p50=476.435459ms baseline_p95=962.106917ms baseline_p99=1.109378583s qps_delta=+18.89% p50_delta=-9.75% p95_delta=+1.86% p99_delta=-0.85% verification_count=1600 verification_failures=0 p95_guardrail=pass", got)
}

func TestSendStressActiveTargetCountUsesAllSendersInThroughputMode(t *testing.T) {
	require.Equal(t, 128, sendStressActiveTargetCount(sendStressConfig{Mode: sendStressModeThroughput, Workers: 32}, 128))
	require.Equal(t, 32, sendStressActiveTargetCount(sendStressConfig{Mode: sendStressModeLatency, Workers: 32}, 128))
	require.Equal(t, 16, sendStressActiveTargetCount(sendStressConfig{Mode: sendStressModeLatency, Workers: 32}, 16))
}

func TestSendStressTargetSendPacketChannelIDUsesRecipientForPersonAndChannelForGroup(t *testing.T) {
	personTarget := sendStressTarget{
		SenderUID:    "sender-a",
		RecipientUID: "recipient-a",
		ChannelID:    "sender-a-recipient-a",
		ChannelType:  frame.ChannelTypePerson,
	}
	groupTarget := sendStressTarget{
		SenderUID:    "sender-b",
		RecipientUID: "group-member",
		ChannelID:    "group-fixed",
		ChannelType:  frame.ChannelTypeGroup,
	}

	require.Equal(t, "recipient-a", personTarget.sendPacketChannelID())
	require.Equal(t, "group-fixed", groupTarget.sendPacketChannelID())
}

func TestSendStressUniqueTargetCountCollapsesHotChannelTargets(t *testing.T) {
	targets := []sendStressTarget{
		{ChannelID: "group-fixed", ChannelType: frame.ChannelTypeGroup},
		{ChannelID: "group-fixed", ChannelType: frame.ChannelTypeGroup},
		{ChannelID: "group-fixed", ChannelType: frame.ChannelTypeGroup},
	}

	require.Equal(t, 1, sendStressUniqueTargetCount(targets))
}

func TestSendStressArtifactsAreScenarioSpecific(t *testing.T) {
	multi := sendStressArtifactsForScenario(sendStressScenarioMultiTarget)
	hot := sendStressArtifactsForScenario(sendStressScenarioSingleHotChannel)
	hotCold := sendStressArtifactsForScenario(sendStressScenarioHotColdSkew)

	require.Equal(t, "2026-04-19-send-stress-postfix", multi.Label)
	require.Equal(t, "/tmp/send-stress-postfix.log", multi.LogPath)
	require.Equal(t, "tmp/profiles/send-stress-postfix.cpu.out", multi.CPUPath)
	require.Equal(t, "tmp/profiles/send-stress-postfix.block.out", multi.BlockPath)

	require.Equal(t, "2026-04-19-send-stress-single-hot-channel", hot.Label)
	require.Equal(t, "/tmp/send-stress-single-hot-channel.log", hot.LogPath)
	require.Equal(t, "tmp/profiles/send-stress-single-hot-channel.cpu.out", hot.CPUPath)
	require.Equal(t, "tmp/profiles/send-stress-single-hot-channel.block.out", hot.BlockPath)

	require.Equal(t, "2026-04-20-send-stress-hot-cold-skew", hotCold.Label)
	require.Equal(t, "/tmp/send-stress-hot-cold-skew.log", hotCold.LogPath)
	require.Equal(t, "tmp/profiles/send-stress-hot-cold-skew.cpu.out", hotCold.CPUPath)
	require.Equal(t, "tmp/profiles/send-stress-hot-cold-skew.block.out", hotCold.BlockPath)
}

func TestSelectSendStressThreeNodeRunUsesAcceptancePresetByDefault(t *testing.T) {
	preset := sendStressAcceptancePreset()
	applySendStressAcceptanceConfigEnv(t, preset.Benchmark)

	selection := selectSendStressThreeNodeRun(t)

	require.True(t, selection.useAcceptancePreset)
}

func TestSendStressHotColdLatencySummarySeparatesHotAndColdRecords(t *testing.T) {
	records := []sendStressRecord{
		{ChannelID: "stress-hot-group-channel", AckLatency: 10 * time.Millisecond},
		{ChannelID: "stress-hot-group-channel", AckLatency: 20 * time.Millisecond},
		{ChannelID: "stress-cold-group-channel-1", AckLatency: 90 * time.Millisecond},
		{ChannelID: "stress-cold-group-channel-2", AckLatency: 120 * time.Millisecond},
	}

	hot, cold := summarizeSendStressHotColdLatencies(records)
	require.Equal(t, 2, hot.Count)
	require.Equal(t, 20*time.Millisecond, hot.P95)
	require.Equal(t, 2, cold.Count)
	require.Equal(t, 120*time.Millisecond, cold.P95)
}

func TestBuildSendStressVerificationPlanRejectsDuplicateMessageSeq(t *testing.T) {
	_, err := buildSendStressVerificationPlan([]sendStressRecord{
		{ChannelID: "group-fixed", ChannelType: frame.ChannelTypeGroup, MessageSeq: 7},
		{ChannelID: "group-fixed", ChannelType: frame.ChannelTypeGroup, MessageSeq: 7},
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate message_seq")
}

func TestSendStressReplicaRangeMismatchReportsReplicaDetails(t *testing.T) {
	plan, err := buildSendStressVerificationPlan([]sendStressRecord{
		{
			Worker:        4,
			Iteration:     19,
			SenderUID:     "stress-sender-004",
			RecipientUID:  "stress-group-member-004",
			ChannelID:     "stress-hot-group-channel",
			ChannelType:   frame.ChannelTypeGroup,
			ClientSeq:     88,
			ClientMsgNo:   "msg-88",
			Payload:       []byte("expected payload"),
			MessageID:     321,
			MessageSeq:    6059,
			ConnectNodeID: 3,
		},
	})
	require.NoError(t, err)

	mismatch := sendStressReplicaRangeMismatch(2, plan, map[uint64]channel.Message{
		6059: {
			MessageSeq:  6059,
			ChannelID:   "stress-hot-group-channel",
			ChannelType: frame.ChannelTypeGroup,
			FromUID:     "stress-sender-002",
			ClientMsgNo: "msg-55",
			Payload:     []byte("other payload"),
		},
	}, 6059)

	require.Contains(t, mismatch, "node=2")
	require.Contains(t, mismatch, "message_seq=6059")
	require.Contains(t, mismatch, "replica_from=stress-sender-002")
	require.Contains(t, mismatch, "client_msg_no=msg-88")
}

func TestSendStressReplicaRangeMismatchDetectsReplicaMessageIDDifferences(t *testing.T) {
	plan, err := buildSendStressVerificationPlan([]sendStressRecord{
		{
			Worker:        9,
			Iteration:     42,
			SenderUID:     "stress-sender-009",
			RecipientUID:  "stress-group-member-009",
			ChannelID:     "stress-hot-group-channel",
			ChannelType:   frame.ChannelTypeGroup,
			ClientSeq:     123,
			ClientMsgNo:   "msg-123",
			Payload:       []byte("same payload"),
			MessageID:     777001,
			MessageSeq:    9102,
			ConnectNodeID: 2,
		},
	})
	require.NoError(t, err)

	mismatch := sendStressReplicaRangeMismatch(3, plan, map[uint64]channel.Message{
		9102: {
			MessageID:   777002,
			MessageSeq:  9102,
			ChannelID:   "stress-hot-group-channel",
			ChannelType: frame.ChannelTypeGroup,
			FromUID:     "stress-sender-009",
			ClientMsgNo: "msg-123",
			Payload:     []byte("same payload"),
		},
	}, 9102)

	require.Contains(t, mismatch, "message_id=777001")
	require.Contains(t, mismatch, "replica_message_id=777002")
}

func TestSendStressOutcomeErrorRate(t *testing.T) {
	outcome := sendStressOutcome{Total: 10, Success: 8, Failed: 2}
	require.InDelta(t, 20.0, outcome.ErrorRate(), 0.001)
}

func TestSendStressThroughputTrackerCompletesOutOfOrderAcks(t *testing.T) {
	client := sendStressWorkerClient{
		target: sendStressTarget{
			SenderUID:     "sender",
			RecipientUID:  "recipient",
			ChannelID:     "channel",
			ChannelType:   frame.ChannelTypePerson,
			OwnerNodeID:   1,
			ConnectNodeID: 1,
		},
	}
	tracker := newSendStressInflightTracker()
	first := tracker.Start(client, 0, "measure", 0, 2, "m2", []byte("two"))
	second := tracker.Start(client, 0, "measure", 1, 3, "m3", []byte("three"))

	require.NoError(t, tracker.Complete(&frame.SendackPacket{
		ClientSeq:   3,
		ClientMsgNo: "m3",
		ReasonCode:  frame.ReasonSuccess,
		MessageID:   103,
		MessageSeq:  203,
	}, nil))
	require.NoError(t, tracker.Complete(&frame.SendackPacket{
		ClientSeq:   2,
		ClientMsgNo: "m2",
		ReasonCode:  frame.ReasonSuccess,
		MessageID:   102,
		MessageSeq:  202,
	}, nil))

	secondResult := <-second
	require.True(t, secondResult.ok)
	require.EqualValues(t, 3, secondResult.record.ClientSeq)
	require.EqualValues(t, 203, secondResult.record.MessageSeq)

	firstResult := <-first
	require.True(t, firstResult.ok)
	require.EqualValues(t, 2, firstResult.record.ClientSeq)
	require.EqualValues(t, 202, firstResult.record.MessageSeq)
}

func TestSendStressFrameReaderPreservesPartialFrameAcrossTimeout(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	reader := newSendStressFrameReader(clientConn)
	ack := &frame.SendackPacket{
		ClientSeq:   9,
		ClientMsgNo: "m9",
		ReasonCode:  frame.ReasonSuccess,
		MessageID:   109,
		MessageSeq:  209,
	}
	payload, err := codec.New().EncodeFrame(ack, frame.LatestVersion)
	require.NoError(t, err)
	require.Greater(t, len(payload), 4)

	go func() {
		_, _ = serverConn.Write(payload[:3])
		time.Sleep(80 * time.Millisecond)
		_, _ = serverConn.Write(payload[3:])
	}()

	_, err = reader.ReadWithin(20 * time.Millisecond)
	require.Error(t, err)
	require.True(t, isSendStressTimeout(err))

	f, err := reader.ReadWithin(time.Second)
	require.NoError(t, err)
	got, ok := f.(*frame.SendackPacket)
	require.True(t, ok)
	require.EqualValues(t, ack.ClientSeq, got.ClientSeq)
	require.Equal(t, ack.ClientMsgNo, got.ClientMsgNo)
	require.EqualValues(t, ack.MessageID, got.MessageID)
	require.EqualValues(t, ack.MessageSeq, got.MessageSeq)
}

func TestRunSendStressWorkersThroughputModeCapsInflightPerWorker(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	cfg := sendStressConfig{
		Mode:                 sendStressModeThroughput,
		MaxInflightPerWorker: 2,
		MessagesPerWorker:    4,
		AckTimeout:           2 * time.Second,
	}
	client := sendStressWorkerClient{
		target: sendStressTarget{
			SenderUID:     "sender",
			RecipientUID:  "recipient",
			ChannelID:     "channel",
			ChannelType:   frame.ChannelTypePerson,
			OwnerNodeID:   1,
			ConnectNodeID: 1,
		},
		conn:    clientConn,
		reader:  newSendStressFrameReader(clientConn),
		writeMu: &sync.Mutex{},
	}

	serverStarted := make(chan struct{}, cfg.MessagesPerWorker)
	releaseAcks := make(chan struct{})
	serverErr := make(chan error, 1)
	var maxInflight atomic.Int64
	go func() {
		serverErr <- runContinuousSendStressAckServerWithRelease(serverConn, serverStarted, releaseAcks, &maxInflight)
	}()

	done := make(chan struct{})
	var (
		records  []sendStressRecord
		failures []string
		outcome  sendStressOutcome
		runErr   error
	)
	go func() {
		outcome, records, failures, runErr = runSendStressWorkerThroughput(client, 0, cfg, time.Now().Add(50*time.Millisecond))
		close(done)
	}()

	for i := 0; i < cfg.MaxInflightPerWorker; i++ {
		select {
		case <-serverStarted:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for initial inflight sends")
		}
	}

	select {
	case <-serverStarted:
		t.Fatal("worker exceeded max inflight before acknowledgements were released")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseAcks)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for throughput worker to finish")
	}

	require.NoError(t, runErr)
	require.NoError(t, clientConn.Close())
	require.NoError(t, <-serverErr)
	require.Empty(t, failures)
	require.NotEmpty(t, records)
	require.Greater(t, outcome.Total, uint64(cfg.MaxInflightPerWorker))
	require.Greater(t, outcome.Success, uint64(cfg.MaxInflightPerWorker))
	require.Zero(t, outcome.Failed)
	require.LessOrEqual(t, maxInflight.Load(), int64(cfg.MaxInflightPerWorker))
}

func TestRunContinuousSendStressAckServerTreatsClosedPipeAsCleanShutdown(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- runContinuousSendStressAckServer(serverConn)
	}()

	require.NoError(t, writeSendStressFrame(clientConn, &frame.SendPacket{
		ChannelID:   "recipient",
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   1,
		ClientMsgNo: "closed-pipe",
		Payload:     []byte("closed pipe"),
	}, time.Second))
	require.NoError(t, clientConn.Close())

	require.NoError(t, <-serverErr)
}

func TestRunSendStressWorkerThroughputUsesDurationInsteadOfMessageBudget(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	cfg := sendStressConfig{
		Mode:                 sendStressModeThroughput,
		MaxInflightPerWorker: 1,
		MessagesPerWorker:    1,
		AckTimeout:           time.Second,
		Seed:                 7,
	}
	client := sendStressWorkerClient{
		target: sendStressTarget{
			SenderUID:     "sender",
			RecipientUID:  "recipient",
			ChannelID:     "channel",
			ChannelType:   frame.ChannelTypePerson,
			OwnerNodeID:   1,
			ConnectNodeID: 1,
		},
		conn:    clientConn,
		reader:  newSendStressFrameReader(clientConn),
		writeMu: &sync.Mutex{},
	}

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- runContinuousSendStressAckServer(serverConn)
	}()

	outcome, records, failures, err := runSendStressWorkerThroughput(client, 0, cfg, time.Now().Add(250*time.Millisecond))
	require.NoError(t, err)
	require.Empty(t, failures)
	require.Greater(t, outcome.Total, uint64(cfg.MessagesPerWorker))
	require.Greater(t, outcome.Success, uint64(cfg.MessagesPerWorker))
	require.Equal(t, int(outcome.Success), len(records))
	require.Greater(t, len(records), cfg.MessagesPerWorker)

	require.NoError(t, clientConn.Close())
	require.NoError(t, <-serverErr)
}

func selectSendStressThreeNodeRun(t *testing.T) sendStressThreeNodeRunSelection {
	t.Helper()

	preset := sendStressAcceptancePreset()
	cfg := loadSendStressConfig(t)
	requireSendStressEnabled(t, cfg)

	expectedAcceptance := preset.Benchmark
	expectedAcceptance.Enabled = true

	if !sendStressThreeNodeHasExplicitTuningEnv() || cfg == expectedAcceptance {
		cfg = expectedAcceptance
		return sendStressThreeNodeRunSelection{
			cfg:                 cfg,
			preset:              preset,
			minISR:              preset.MinISR,
			useAcceptancePreset: true,
		}
	}

	return sendStressThreeNodeRunSelection{
		cfg:                 cfg,
		preset:              preset,
		minISR:              3,
		useAcceptancePreset: false,
	}
}

type sendStressThreeNodeRunSelection struct {
	cfg                 sendStressConfig
	preset              sendStressAcceptanceSpec
	minISR              int
	useAcceptancePreset bool
}

func sendStressThreeNodeHasExplicitTuningEnv() bool {
	for _, name := range []string{
		sendStressModeEnv,
		sendStressDurationEnv,
		sendStressWorkersEnv,
		sendStressSendersEnv,
		sendStressMessagesPerWorkerEnv,
		sendStressMaxInflightEnv,
		sendStressDialTimeoutEnv,
		sendStressAckTimeoutEnv,
		sendStressSeedEnv,
	} {
		if value, ok := os.LookupEnv(name); ok && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func clearSendStressConfigEnv(t *testing.T) {
	t.Helper()

	for _, name := range []string{
		sendStressEnv,
		sendStressModeEnv,
		sendStressDurationEnv,
		sendStressWorkersEnv,
		sendStressSendersEnv,
		sendStressMessagesPerWorkerEnv,
		sendStressMaxInflightEnv,
		sendStressDialTimeoutEnv,
		sendStressAckTimeoutEnv,
		sendStressSeedEnv,
	} {
		name := name
		if value, ok := os.LookupEnv(name); ok {
			if err := os.Unsetenv(name); err != nil {
				t.Fatalf("clear %s: %v", name, err)
			}
			t.Cleanup(func() {
				if err := os.Setenv(name, value); err != nil {
					t.Fatalf("restore %s: %v", name, err)
				}
			})
		}
	}
}

func applySendStressAcceptanceConfigEnv(t *testing.T, preset sendStressConfig) {
	t.Helper()

	clearSendStressConfigEnv(t)
	t.Setenv(sendStressEnv, "1")
	t.Setenv(sendStressModeEnv, string(preset.Mode))
	t.Setenv(sendStressDurationEnv, preset.Duration.String())
	t.Setenv(sendStressWorkersEnv, strconv.Itoa(preset.Workers))
	t.Setenv(sendStressSendersEnv, strconv.Itoa(preset.Senders))
	t.Setenv(sendStressMessagesPerWorkerEnv, strconv.Itoa(preset.MessagesPerWorker))
	t.Setenv(sendStressMaxInflightEnv, strconv.Itoa(preset.MaxInflightPerWorker))
	t.Setenv(sendStressDialTimeoutEnv, preset.DialTimeout.String())
	t.Setenv(sendStressAckTimeoutEnv, preset.AckTimeout.String())
	t.Setenv(sendStressSeedEnv, strconv.FormatInt(preset.Seed, 10))
}

func buildSendStressVerificationPlan(records []sendStressRecord) (sendStressVerificationPlan, error) {
	if len(records) == 0 {
		return sendStressVerificationPlan{}, nil
	}

	plan := sendStressVerificationPlan{
		ChannelID: channel.ChannelID{
			ID:   records[0].ChannelID,
			Type: records[0].ChannelType,
		},
		OrderedSeqs:   make([]uint64, 0, len(records)),
		ExpectedBySeq: make(map[uint64]sendStressRecord, len(records)),
	}
	for _, record := range records {
		if record.ChannelID != plan.ChannelID.ID || record.ChannelType != plan.ChannelID.Type {
			return sendStressVerificationPlan{}, fmt.Errorf("mixed verification channels: %s/%d != %s/%d", record.ChannelID, record.ChannelType, plan.ChannelID.ID, plan.ChannelID.Type)
		}
		if _, exists := plan.ExpectedBySeq[record.MessageSeq]; exists {
			return sendStressVerificationPlan{}, fmt.Errorf("duplicate message_seq=%d for channel=%s/%d", record.MessageSeq, record.ChannelID, record.ChannelType)
		}
		plan.OrderedSeqs = append(plan.OrderedSeqs, record.MessageSeq)
		plan.ExpectedBySeq[record.MessageSeq] = record
	}
	sort.Slice(plan.OrderedSeqs, func(i, j int) bool {
		return plan.OrderedSeqs[i] < plan.OrderedSeqs[j]
	})
	return plan, nil
}

func sendStressReplicaRangeMismatch(nodeID uint64, plan sendStressVerificationPlan, loaded map[uint64]channel.Message, committedHW uint64) string {
	for _, seq := range plan.OrderedSeqs {
		record := plan.ExpectedBySeq[seq]
		msg, ok := loaded[seq]
		if !ok {
			return fmt.Sprintf("replica missing node=%d worker=%d iteration=%d sender=%s recipient=%s connect_node=%d message_seq=%d message_id=%d client_seq=%d client_msg_no=%s committed_hw=%d", nodeID, record.Worker, record.Iteration, record.SenderUID, record.RecipientUID, record.ConnectNodeID, record.MessageSeq, record.MessageID, record.ClientSeq, record.ClientMsgNo, committedHW)
		}
		if bytes.Equal(record.Payload, msg.Payload) &&
			record.SenderUID == msg.FromUID &&
			record.ClientMsgNo == msg.ClientMsgNo &&
			record.ChannelID == msg.ChannelID &&
			record.ChannelType == msg.ChannelType &&
			record.MessageID == int64(msg.MessageID) &&
			record.MessageSeq == msg.MessageSeq {
			continue
		}
		return fmt.Sprintf("replica mismatch node=%d worker=%d iteration=%d sender=%s recipient=%s connect_node=%d message_seq=%d message_id=%d client_seq=%d client_msg_no=%s committed_hw=%d frames_before_ack=%v replica_from=%s replica_client_msg_no=%s replica_payload=%q replica_channel=%s replica_channel_type=%d replica_message_id=%d", nodeID, record.Worker, record.Iteration, record.SenderUID, record.RecipientUID, record.ConnectNodeID, record.MessageSeq, record.MessageID, record.ClientSeq, record.ClientMsgNo, committedHW, record.FramesBeforeAck, msg.FromUID, msg.ClientMsgNo, string(msg.Payload), msg.ChannelID, msg.ChannelType, msg.MessageID)
	}
	return ""
}

func runSendStressWorkerThroughput(client sendStressWorkerClient, worker int, cfg sendStressConfig, deadline time.Time) (sendStressOutcome, []sendStressRecord, []string, error) {
	if cfg.MaxInflightPerWorker <= 0 {
		return sendStressOutcome{}, nil, nil, fmt.Errorf("%s must be > 0, got %d", sendStressMaxInflightEnv, cfg.MaxInflightPerWorker)
	}

	var (
		outcome  sendStressOutcome
		records  = make([]sendStressRecord, 0, cfg.MessagesPerWorker)
		failures = make([]string, 0, 1)
		mu       sync.Mutex
	)
	appendResult := func(result sendStressAttemptResult) {
		mu.Lock()
		defer mu.Unlock()
		if result.ok {
			outcome.Success++
			records = append(records, result.record)
			return
		}
		outcome.Failed++
		if len(failures) < 8 {
			failures = append(failures, result.failure)
		}
	}

	tracker := newSendStressInflightTracker()
	slots := make(chan struct{}, cfg.MaxInflightPerWorker)
	writerDone := make(chan struct{})
	readerErrCh := make(chan error, 1)
	stopWriter := make(chan struct{})
	var stopWriterOnce sync.Once
	stop := func() {
		stopWriterOnce.Do(func() {
			close(stopWriter)
		})
	}

	go func() {
		readerErrCh <- readSendStressThroughputAcks(client, cfg.AckTimeout, tracker, writerDone, stop)
	}()

	nextClientSeq := uint64(2)
	for iteration := 0; ; iteration++ {
		if time.Now().After(deadline) {
			break
		}
		stopped := false
		select {
		case <-stopWriter:
			stopped = true
		default:
		}
		if stopped {
			break
		}

		slots <- struct{}{}
		mu.Lock()
		outcome.Total++
		mu.Unlock()

		clientSeq := nextClientSeq
		nextClientSeq++
		clientMsgNo := fmt.Sprintf("send-stress-%d-%s-%02d-%d", worker, client.target.SenderUID, iteration, cfg.Seed)
		payload := []byte(fmt.Sprintf("send-stress payload worker=%d sender=%s recipient=%s iteration=%d seed=%d", worker, client.target.SenderUID, client.target.RecipientUID, iteration, cfg.Seed))
		packet := &frame.SendPacket{
			ChannelID:   client.target.sendPacketChannelID(),
			ChannelType: client.target.ChannelType,
			ClientSeq:   clientSeq,
			ClientMsgNo: clientMsgNo,
			Payload:     payload,
		}
		_ = tracker.startAt(client, worker, "measure", iteration, clientSeq, clientMsgNo, payload, time.Now(), func(result sendStressAttemptResult) {
			appendResult(result)
			<-slots
		})
		if err := writeSendStressClientFrame(client, packet, cfg.AckTimeout); err != nil {
			_ = tracker.Fail(clientSeq, fmt.Sprintf("worker=%d sender=%s connect_node=%d phase=%s iteration=%d write error=%v", worker, client.target.SenderUID, client.target.ConnectNodeID, "measure", iteration, err))
			stop()
			break
		}
	}

	close(writerDone)
	readerErr := <-readerErrCh
	if readerErr != nil {
		return outcome, records, failures, readerErr
	}
	return outcome, records, failures, nil
}

func readSendStressThroughputAcks(client sendStressWorkerClient, ackTimeout time.Duration, tracker *sendStressInflightTracker, writerDone <-chan struct{}, stopWriter func()) error {
	for {
		if tracker.Pending() == 0 {
			select {
			case <-writerDone:
				return nil
			default:
			}
		}

		f, err := readSendStressClientFrame(client, minSendStressDuration(ackTimeout, 200*time.Millisecond))
		if err != nil {
			if isSendStressTimeout(err) {
				select {
				case <-writerDone:
					if tracker.Pending() == 0 {
						return nil
					}
				default:
				}
				continue
			}
			tracker.FailAll("throughput ack reader error: %v", err)
			stopWriter()
			return err
		}

		switch pkt := f.(type) {
		case *frame.SendackPacket:
			if err := tracker.Complete(pkt, nil); err != nil {
				tracker.FailAll("throughput ack reader mismatch: %v", err)
				stopWriter()
				return err
			}
		case *frame.RecvPacket:
			if err := writeSendStressClientFrame(client, &frame.RecvackPacket{
				MessageID:  pkt.MessageID,
				MessageSeq: pkt.MessageSeq,
			}, ackTimeout); err != nil {
				tracker.FailAll("throughput recvack write error: %v", err)
				stopWriter()
				return err
			}
		default:
			err := fmt.Errorf("unexpected frame while waiting for throughput sendack: %T", f)
			tracker.FailAll("%v", err)
			stopWriter()
			return err
		}
	}
}

func writeSendStressClientFrame(client sendStressWorkerClient, f frame.Frame, timeout time.Duration) error {
	if client.writeMu == nil {
		return writeSendStressFrame(client.conn, f, timeout)
	}
	client.writeMu.Lock()
	defer client.writeMu.Unlock()
	return writeSendStressFrame(client.conn, f, timeout)
}

func writeSendStressFrame(conn net.Conn, f frame.Frame, timeout time.Duration) error {
	switch pkt := f.(type) {
	case *frame.ConnectPacket:
		client, err := appWKProtoClientForConnErr(conn)
		if err != nil {
			return err
		}
		f, err = client.UseClientKey(pkt)
		if err != nil {
			return err
		}
	case *frame.SendPacket:
		if value, ok := appWKProtoClients.Load(conn); ok {
			client := value.(*testkit.WKProtoClient)
			cloned := *pkt
			if err := client.EncryptSendPacket(&cloned); err != nil {
				return err
			}
			f = &cloned
		}
	}

	payload, err := codec.New().EncodeFrame(f, frame.LatestVersion)
	if err != nil {
		return err
	}
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	defer func() {
		_ = conn.SetWriteDeadline(time.Time{})
	}()
	_, err = conn.Write(payload)
	return err
}

func minSendStressDuration(a, b time.Duration) time.Duration {
	if a <= 0 {
		return b
	}
	if b <= 0 || a < b {
		return a
	}
	return b
}

func isSendStressTimeout(err error) bool {
	var netErr net.Error
	return err != nil && errors.As(err, &netErr) && netErr.Timeout()
}

func readSendStressClientFrame(client sendStressWorkerClient, timeout time.Duration) (frame.Frame, error) {
	if client.reader != nil {
		return client.reader.ReadWithin(timeout)
	}
	return readSendStressFrameWithin(client.conn, timeout)
}

func readSendStressFrameWithin(conn net.Conn, timeout time.Duration) (frame.Frame, error) {
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	defer func() {
		_ = conn.SetReadDeadline(time.Time{})
	}()
	f, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
	if err != nil {
		return nil, err
	}
	if value, ok := appWKProtoClients.Load(conn); ok {
		client := value.(*testkit.WKProtoClient)
		switch pkt := f.(type) {
		case *frame.ConnackPacket:
			if err := client.ApplyConnack(pkt); err != nil {
				return nil, err
			}
		case *frame.RecvPacket:
			if err := client.DecryptRecvPacket(pkt); err != nil {
				return nil, err
			}
		}
	}
	return f, nil
}

func isSendStressClosedConnError(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe)
}

func runContinuousSendStressAckServer(conn net.Conn) error {
	for {
		f, err := readSendStressFrameWithin(conn, 5*time.Second)
		if err != nil {
			if isSendStressClosedConnError(err) {
				return nil
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			return err
		}
		send, ok := f.(*frame.SendPacket)
		if !ok {
			return fmt.Errorf("expected *frame.SendPacket, got %T", f)
		}
		if err := writeSendStressFrame(conn, &frame.SendackPacket{
			ClientSeq:   send.ClientSeq,
			ClientMsgNo: send.ClientMsgNo,
			ReasonCode:  frame.ReasonSuccess,
			MessageID:   int64(1000 + send.ClientSeq),
			MessageSeq:  send.ClientSeq,
		}, time.Second); err != nil {
			if isSendStressClosedConnError(err) {
				return nil
			}
			return err
		}
	}
}

func runContinuousSendStressAckServerWithRelease(conn net.Conn, started chan<- struct{}, releaseAcks <-chan struct{}, maxInflightBeforeRelease *atomic.Int64) error {
	type ackEnvelope struct {
		packet *frame.SendackPacket
	}

	acks := make(chan ackEnvelope, 64)
	readerErrCh := make(chan error, 1)
	var currentInflight atomic.Int64
	var releaseObserved atomic.Bool

	go func() {
		defer close(acks)
		for {
			f, err := readSendStressFrameWithin(conn, 5*time.Second)
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
					readerErrCh <- nil
					return
				}
				var netErr net.Error
				if errors.As(err, &netErr) && netErr.Timeout() {
					continue
				}
				readerErrCh <- err
				return
			}
			send, ok := f.(*frame.SendPacket)
			if !ok {
				readerErrCh <- fmt.Errorf("expected *frame.SendPacket, got %T", f)
				return
			}
			inflight := currentInflight.Add(1)
			if maxInflightBeforeRelease != nil && !releaseObserved.Load() {
				for {
					prev := maxInflightBeforeRelease.Load()
					if inflight <= prev || maxInflightBeforeRelease.CompareAndSwap(prev, inflight) {
						break
					}
				}
			}
			started <- struct{}{}
			acks <- ackEnvelope{
				packet: &frame.SendackPacket{
					ClientSeq:   send.ClientSeq,
					ClientMsgNo: send.ClientMsgNo,
					ReasonCode:  frame.ReasonSuccess,
					MessageID:   int64(1000 + send.ClientSeq),
					MessageSeq:  send.ClientSeq,
				},
			}
		}
	}()

	pending := make([]ackEnvelope, 0, 8)
	released := false
	flushPending := func() error {
		for len(pending) > 0 {
			env := pending[0]
			pending = pending[1:]
			if err := writeSendStressFrame(conn, env.packet, 5*time.Second); err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
					return nil
				}
				return err
			}
			currentInflight.Add(-1)
		}
		return nil
	}

	releaseCh := releaseAcks
	for {
		select {
		case env, ok := <-acks:
			if !ok {
				if !released && releaseCh != nil {
					<-releaseCh
					releaseObserved.Store(true)
					released = true
					releaseCh = nil
				}
				if err := flushPending(); err != nil {
					return err
				}
				return <-readerErrCh
			}
			if released {
				if err := writeSendStressFrame(conn, env.packet, 5*time.Second); err != nil {
					if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
						return nil
					}
					return err
				}
				currentInflight.Add(-1)
				continue
			}
			pending = append(pending, env)
		case <-releaseCh:
			releaseObserved.Store(true)
			released = true
			releaseCh = nil
			if err := flushPending(); err != nil {
				return err
			}
		}
	}
}
