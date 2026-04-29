package app

import (
	"context"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestRecvStressConfigDefaultsAndOverrides(t *testing.T) {
	clearRecvStressConfigEnv(t)
	defaultCfg := loadRecvStressConfig(t)
	require.False(t, defaultCfg.Enabled)
	require.Equal(t, 5*time.Second, defaultCfg.Duration)
	require.Equal(t, max(4, runtime.GOMAXPROCS(0)), defaultCfg.Workers)
	require.Equal(t, max(8, defaultCfg.Workers), defaultCfg.Recipients)
	require.Equal(t, 50, defaultCfg.MessagesPerWorker)
	require.Equal(t, 3*time.Second, defaultCfg.DialTimeout)
	require.Equal(t, 5*time.Second, defaultCfg.RecvTimeout)
	require.EqualValues(t, 20260429, defaultCfg.Seed)

	t.Setenv(recvStressEnv, "1")
	t.Setenv(recvStressDurationEnv, "11s")
	t.Setenv(recvStressWorkersEnv, "3")
	t.Setenv(recvStressRecipientsEnv, "5")
	t.Setenv(recvStressMessagesPerWorkerEnv, "7")
	t.Setenv(recvStressDialTimeoutEnv, "2s")
	t.Setenv(recvStressRecvTimeoutEnv, "4s")
	t.Setenv(recvStressSeedEnv, "42")

	cfg := loadRecvStressConfig(t)
	require.True(t, cfg.Enabled)
	require.Equal(t, 11*time.Second, cfg.Duration)
	require.Equal(t, 3, cfg.Workers)
	require.Equal(t, 5, cfg.Recipients)
	require.Equal(t, 7, cfg.MessagesPerWorker)
	require.Equal(t, 2*time.Second, cfg.DialTimeout)
	require.Equal(t, 4*time.Second, cfg.RecvTimeout)
	require.EqualValues(t, 42, cfg.Seed)
}

func TestLoadRecvStressConfigRejectsInvalidEnvWithoutSubprocess(t *testing.T) {
	env := map[string]string{
		recvStressEnv:        "1",
		recvStressWorkersEnv: "0",
	}

	_, err := loadRecvStressConfigFromEnv(func(name string) (string, bool) {
		value, ok := env[name]
		return value, ok
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), recvStressWorkersEnv)
}

func TestRecvStressActiveTargetCountCapsLatencyWorkers(t *testing.T) {
	require.Equal(t, 0, recvStressActiveTargetCount(recvStressConfig{Workers: 3}, 0))
	require.Equal(t, 2, recvStressActiveTargetCount(recvStressConfig{Workers: 3}, 2))
	require.Equal(t, 3, recvStressActiveTargetCount(recvStressConfig{Workers: 3}, 10))
}

func TestExecuteRecvStressAttemptAcksMatchingRecvPacket(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	client := recvStressWorkerClient{
		target: recvStressTarget{
			SenderUID:     "sender-a",
			RecipientUID:  "recipient-a",
			ChannelID:     "sender-a@recipient-a",
			ChannelType:   frame.ChannelTypePerson,
			OwnerNodeID:   1,
			ConnectNodeID: 2,
		},
		conn:    clientConn,
		reader:  newSendStressFrameReader(clientConn),
		writeMu: nil,
	}

	ackCh := make(chan *frame.RecvackPacket, 1)
	send := func(ctx context.Context, cmd messageusecase.SendCommand) (messageusecase.SendResult, error) {
		require.Equal(t, "sender-a", cmd.FromUID)
		require.Equal(t, "recipient-a", cmd.ChannelID)
		require.Equal(t, frame.ChannelTypePerson, cmd.ChannelType)
		require.Equal(t, "recv-stress-0-recipient-a-00-99", cmd.ClientMsgNo)
		require.Equal(t, []byte("payload-99"), cmd.Payload)

		go func() {
			recv := &frame.RecvPacket{
				MessageID:   1001,
				MessageSeq:  7,
				ClientMsgNo: cmd.ClientMsgNo,
				ChannelID:   cmd.FromUID,
				ChannelType: cmd.ChannelType,
				FromUID:     cmd.FromUID,
				Payload:     append([]byte(nil), cmd.Payload...),
			}
			if err := writeSendStressFrame(serverConn, recv, time.Second); err != nil {
				return
			}
			f, err := readSendStressFrameWithin(serverConn, time.Second)
			if err != nil {
				return
			}
			ack, ok := f.(*frame.RecvackPacket)
			if ok {
				ackCh <- ack
			}
		}()

		return messageusecase.SendResult{Reason: frame.ReasonSuccess, MessageID: 1001, MessageSeq: 7}, nil
	}

	record, failure, ok := executeRecvStressAttempt(context.Background(), client, send, 0, "measure", 0, 99, []byte("payload-99"), time.Second)
	require.True(t, ok, failure)
	require.Empty(t, failure)
	require.Equal(t, "sender-a", record.SenderUID)
	require.Equal(t, "recipient-a", record.RecipientUID)
	require.Equal(t, "recv-stress-0-recipient-a-00-99", record.ClientMsgNo)
	require.EqualValues(t, 1001, record.MessageID)
	require.EqualValues(t, 7, record.MessageSeq)
	require.Equal(t, []byte("payload-99"), record.Payload)
	require.Positive(t, record.RecvLatency)

	select {
	case ack := <-ackCh:
		require.EqualValues(t, 1001, ack.MessageID)
		require.EqualValues(t, 7, ack.MessageSeq)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for recvack")
	}
}

func TestExecuteRecvStressAttemptRejectsMismatchedRecvPacket(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	client := recvStressWorkerClient{
		target: recvStressTarget{
			SenderUID:    "sender-a",
			RecipientUID: "recipient-a",
			ChannelType:  frame.ChannelTypePerson,
		},
		conn:   clientConn,
		reader: newSendStressFrameReader(clientConn),
	}
	send := func(ctx context.Context, cmd messageusecase.SendCommand) (messageusecase.SendResult, error) {
		go func() {
			_ = writeSendStressFrame(serverConn, &frame.RecvPacket{
				MessageID:   1001,
				MessageSeq:  7,
				ClientMsgNo: "other-msg",
				ChannelID:   cmd.FromUID,
				ChannelType: cmd.ChannelType,
				FromUID:     cmd.FromUID,
				Payload:     cmd.Payload,
			}, time.Second)
		}()
		return messageusecase.SendResult{Reason: frame.ReasonSuccess, MessageID: 1001, MessageSeq: 7}, nil
	}

	_, failure, ok := executeRecvStressAttempt(context.Background(), client, send, 0, "measure", 0, 99, []byte("payload-99"), time.Second)
	require.False(t, ok)
	require.Contains(t, failure, "recv_mismatch")
}

func clearRecvStressConfigEnv(t *testing.T) {
	t.Helper()

	for _, name := range []string{
		recvStressEnv,
		recvStressDurationEnv,
		recvStressWorkersEnv,
		recvStressRecipientsEnv,
		recvStressMessagesPerWorkerEnv,
		recvStressDialTimeoutEnv,
		recvStressRecvTimeoutEnv,
		recvStressSeedEnv,
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

const (
	recvStressEnv                  = "WK_RECV_STRESS"
	recvStressDurationEnv          = "WK_RECV_STRESS_DURATION"
	recvStressWorkersEnv           = "WK_RECV_STRESS_WORKERS"
	recvStressRecipientsEnv        = "WK_RECV_STRESS_RECIPIENTS"
	recvStressMessagesPerWorkerEnv = "WK_RECV_STRESS_MESSAGES_PER_WORKER"
	recvStressDialTimeoutEnv       = "WK_RECV_STRESS_DIAL_TIMEOUT"
	recvStressRecvTimeoutEnv       = "WK_RECV_STRESS_RECV_TIMEOUT"
	recvStressSeedEnv              = "WK_RECV_STRESS_SEED"
	recvStressWarmupRecvTimeout    = 12 * time.Second
)

type recvStressConfig struct {
	// Enabled gates the expensive receive path stress test behind an explicit opt-in.
	Enabled bool
	// Duration caps the measurement window for each stress run.
	Duration time.Duration
	// Workers is the number of concurrent recipient connections used for measurement.
	Workers int
	// Recipients is the number of preloaded recipient targets available to workers.
	Recipients int
	// MessagesPerWorker caps the per-worker message budget in the measurement window.
	MessagesPerWorker int
	// DialTimeout bounds WKProto recipient connection establishment.
	DialTimeout time.Duration
	// RecvTimeout bounds the send-to-recv wait for each measured message.
	RecvTimeout time.Duration
	// Seed keeps generated message identifiers and payloads reproducible.
	Seed int64
}

// recvStressAcceptanceSpec groups the default benchmark shape with runtime tuning.
type recvStressAcceptanceSpec struct {
	// Benchmark is used when the test is enabled without explicit tuning envs.
	Benchmark recvStressConfig
	// Tuning carries cluster and gateway knobs shared with the send stress preset.
	Tuning sendStressAcceptanceSpec
	// MinISR is applied to preloaded channel runtime metadata.
	MinISR int
}

// recvStressTarget describes one sender-to-online-recipient route under load.
type recvStressTarget struct {
	// SenderUID is the offline logical sender used to create person messages.
	SenderUID string
	// RecipientUID is the online user connected through WKProto.
	RecipientUID string
	// ChannelID is the durable normalized channel ID.
	ChannelID string
	// ChannelType is the WuKong channel type for the target.
	ChannelType uint8
	// OwnerNodeID is the channel leader used to submit messages.
	OwnerNodeID uint64
	// ConnectNodeID is the gateway node where the recipient stays online.
	ConnectNodeID uint64
}

// recvStressRecord captures one successful send-to-receive measurement.
type recvStressRecord struct {
	// Worker is the zero-based worker index that produced the record.
	Worker int
	// Iteration is the per-worker message index.
	Iteration int
	// SenderUID is the logical sender written into the durable message.
	SenderUID string
	// RecipientUID is the online route that received the message.
	RecipientUID string
	// ChannelID is the durable normalized channel ID.
	ChannelID string
	// ChannelType is the WuKong channel type for the received message.
	ChannelType uint8
	// ClientSeq is the generated client sequence for the send command.
	ClientSeq uint64
	// ClientMsgNo is the generated idempotency key used to correlate recv packets.
	ClientMsgNo string
	// Payload is the expected message body observed by the recipient.
	Payload []byte
	// MessageID is the durable server message ID returned and received.
	MessageID int64
	// MessageSeq is the durable channel sequence returned and received.
	MessageSeq uint64
	// SendLatency is the time spent in the message send use case.
	SendLatency time.Duration
	// RecvLatency is the end-to-end time from send start to recipient recv.
	RecvLatency time.Duration
	// OwnerNodeID is the node that accepted the message send.
	OwnerNodeID uint64
	// ConnectNodeID is the gateway node that hosted the recipient session.
	ConnectNodeID uint64
}

// recvStressOutcome counts worker attempts and their terminal state.
type recvStressOutcome struct {
	// Total is the number of attempted receive measurements.
	Total uint64
	// Success is the number of attempts that received and acked the expected packet.
	Success uint64
	// Failed is the number of attempts that stopped with send, receive, or ack errors.
	Failed uint64
}

// recvStressObservedMetrics summarizes the receive stress run for logs and comparisons.
type recvStressObservedMetrics struct {
	// QPS is successful receive measurements per elapsed second.
	QPS float64
	// P50 is the median end-to-end receive latency.
	P50 time.Duration
	// P95 is the 95th percentile end-to-end receive latency.
	P95 time.Duration
	// P99 is the 99th percentile end-to-end receive latency.
	P99 time.Duration
	// VerificationCount is the number of successful records checked after the run.
	VerificationCount int
	// VerificationFailures is the number of worker failures recorded during the run.
	VerificationFailures int
}

// recvStressWorkerClient binds one target to its WKProto recipient connection.
type recvStressWorkerClient struct {
	// target identifies the sender, recipient, and cluster route under test.
	target recvStressTarget
	// conn is the recipient-side WKProto TCP connection.
	conn net.Conn
	// reader preserves partial WKProto frames across short read deadlines.
	reader *sendStressFrameReader
	// writeMu serializes recvack writes if a future reader loop shares the connection.
	writeMu *sync.Mutex
}

type recvStressSendFunc func(context.Context, messageusecase.SendCommand) (messageusecase.SendResult, error)

type recvStressThreeNodeRunSelection struct {
	cfg                 recvStressConfig
	preset              recvStressAcceptanceSpec
	minISR              int
	useAcceptancePreset bool
}

func (o recvStressOutcome) ErrorRate() float64 {
	if o.Total == 0 {
		return 0
	}
	return float64(o.Failed) * 100 / float64(o.Total)
}

func loadRecvStressConfig(t *testing.T) recvStressConfig {
	t.Helper()

	cfg, err := loadRecvStressConfigFromEnv(os.LookupEnv)
	require.NoError(t, err)
	return cfg
}

func loadRecvStressConfigFromEnv(lookup func(string) (string, bool)) (recvStressConfig, error) {
	enabled, ok, err := parseSendStressEnabled(lookupEnvValue(lookup, recvStressEnv))
	if err != nil {
		return recvStressConfig{}, fmt.Errorf("parse %s: %w", recvStressEnv, err)
	}
	if !ok {
		enabled = false
	}

	duration, err := sendStressEnvDuration(lookup, recvStressDurationEnv, 5*time.Second)
	if err != nil {
		return recvStressConfig{}, err
	}
	workers, err := sendStressEnvInt(lookup, recvStressWorkersEnv, max(4, runtime.GOMAXPROCS(0)))
	if err != nil {
		return recvStressConfig{}, err
	}
	messagesPerWorker, err := sendStressEnvInt(lookup, recvStressMessagesPerWorkerEnv, 50)
	if err != nil {
		return recvStressConfig{}, err
	}
	dialTimeout, err := sendStressEnvDuration(lookup, recvStressDialTimeoutEnv, 3*time.Second)
	if err != nil {
		return recvStressConfig{}, err
	}
	recvTimeout, err := sendStressEnvDuration(lookup, recvStressRecvTimeoutEnv, 5*time.Second)
	if err != nil {
		return recvStressConfig{}, err
	}
	seed, err := sendStressEnvInt64(lookup, recvStressSeedEnv, 20260429)
	if err != nil {
		return recvStressConfig{}, err
	}

	cfg := recvStressConfig{
		Enabled:           enabled,
		Duration:          duration,
		Workers:           workers,
		MessagesPerWorker: messagesPerWorker,
		DialTimeout:       dialTimeout,
		RecvTimeout:       recvTimeout,
		Seed:              seed,
	}
	if value, ok := lookup(recvStressRecipientsEnv); !ok || strings.TrimSpace(value) == "" {
		cfg.Recipients = max(8, cfg.Workers)
	} else {
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return recvStressConfig{}, fmt.Errorf("parse %s: %w", recvStressRecipientsEnv, err)
		}
		cfg.Recipients = parsed
	}
	if err := validateRecvStressConfig(cfg); err != nil {
		return recvStressConfig{}, err
	}
	return cfg, nil
}

func validateRecvStressConfig(cfg recvStressConfig) error {
	if cfg.Workers <= 0 {
		return fmt.Errorf("%s must be > 0, got %d", recvStressWorkersEnv, cfg.Workers)
	}
	if cfg.Recipients <= 0 {
		return fmt.Errorf("%s must be > 0, got %d", recvStressRecipientsEnv, cfg.Recipients)
	}
	if cfg.Recipients < cfg.Workers {
		return fmt.Errorf("%s must be >= %s, got %d < %d", recvStressRecipientsEnv, recvStressWorkersEnv, cfg.Recipients, cfg.Workers)
	}
	if cfg.MessagesPerWorker <= 0 {
		return fmt.Errorf("%s must be > 0, got %d", recvStressMessagesPerWorkerEnv, cfg.MessagesPerWorker)
	}
	if cfg.Duration <= 0 {
		return fmt.Errorf("%s must be > 0, got %s", recvStressDurationEnv, cfg.Duration)
	}
	if cfg.DialTimeout <= 0 {
		return fmt.Errorf("%s must be > 0, got %s", recvStressDialTimeoutEnv, cfg.DialTimeout)
	}
	if cfg.RecvTimeout <= 0 {
		return fmt.Errorf("%s must be > 0, got %s", recvStressRecvTimeoutEnv, cfg.RecvTimeout)
	}
	return nil
}

func recvStressAcceptancePreset() recvStressAcceptanceSpec {
	tuning := sendStressAcceptancePreset()
	return recvStressAcceptanceSpec{
		Benchmark: recvStressConfig{
			Duration:          15 * time.Second,
			Workers:           16,
			Recipients:        32,
			MessagesPerWorker: 50,
			DialTimeout:       3 * time.Second,
			RecvTimeout:       20 * time.Second,
			Seed:              20260429,
		},
		Tuning: tuning,
		MinISR: tuning.MinISR,
	}
}

func requireRecvStressEnabled(t *testing.T, cfg recvStressConfig) {
	t.Helper()
	if !cfg.Enabled {
		t.Skip("set WK_RECV_STRESS=1 to enable recv stress test")
	}
}

func recvStressActiveTargetCount(cfg recvStressConfig, totalTargets int) int {
	if totalTargets <= 0 || cfg.Workers <= 0 {
		return 0
	}
	return min(cfg.Workers, totalTargets)
}

func selectRecvStressThreeNodeRun(t *testing.T) recvStressThreeNodeRunSelection {
	t.Helper()

	preset := recvStressAcceptancePreset()
	cfg := loadRecvStressConfig(t)
	requireRecvStressEnabled(t, cfg)

	expectedAcceptance := preset.Benchmark
	expectedAcceptance.Enabled = true

	if !recvStressThreeNodeHasExplicitTuningEnv() || cfg == expectedAcceptance {
		cfg = expectedAcceptance
		return recvStressThreeNodeRunSelection{
			cfg:                 cfg,
			preset:              preset,
			minISR:              preset.MinISR,
			useAcceptancePreset: true,
		}
	}

	return recvStressThreeNodeRunSelection{
		cfg:                 cfg,
		preset:              preset,
		minISR:              3,
		useAcceptancePreset: false,
	}
}

func recvStressThreeNodeHasExplicitTuningEnv() bool {
	for _, name := range []string{
		recvStressDurationEnv,
		recvStressWorkersEnv,
		recvStressRecipientsEnv,
		recvStressMessagesPerWorkerEnv,
		recvStressDialTimeoutEnv,
		recvStressRecvTimeoutEnv,
		recvStressSeedEnv,
	} {
		if value, ok := os.LookupEnv(name); ok && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func applyRecvStressAcceptanceConfigEnv(t *testing.T, preset recvStressConfig) {
	t.Helper()

	clearRecvStressConfigEnv(t)
	t.Setenv(recvStressEnv, "1")
	t.Setenv(recvStressDurationEnv, preset.Duration.String())
	t.Setenv(recvStressWorkersEnv, strconv.Itoa(preset.Workers))
	t.Setenv(recvStressRecipientsEnv, strconv.Itoa(preset.Recipients))
	t.Setenv(recvStressMessagesPerWorkerEnv, strconv.Itoa(preset.MessagesPerWorker))
	t.Setenv(recvStressDialTimeoutEnv, preset.DialTimeout.String())
	t.Setenv(recvStressRecvTimeoutEnv, preset.RecvTimeout.String())
	t.Setenv(recvStressSeedEnv, strconv.FormatInt(preset.Seed, 10))
}

func recvStressClientMsgNo(worker int, recipientUID string, iteration int, seed int64) string {
	return fmt.Sprintf("recv-stress-%d-%s-%02d-%d", worker, recipientUID, iteration, seed)
}

func recvStressPayload(worker int, target recvStressTarget, iteration int, seed int64) []byte {
	return []byte(fmt.Sprintf("recv-stress payload worker=%d sender=%s recipient=%s iteration=%d seed=%d", worker, target.SenderUID, target.RecipientUID, iteration, seed))
}

func executeRecvStressAttempt(ctx context.Context, client recvStressWorkerClient, send recvStressSendFunc, worker int, phase string, iteration int, seed int64, payload []byte, recvTimeout time.Duration) (recvStressRecord, string, bool) {
	if send == nil {
		return recvStressRecord{}, fmt.Sprintf("worker=%d recipient=%s connect_node=%d phase=%s iteration=%d send function is nil", worker, client.target.RecipientUID, client.target.ConnectNodeID, phase, iteration), false
	}

	clientSeq := uint64(iteration + 1)
	clientMsgNo := recvStressClientMsgNo(worker, client.target.RecipientUID, iteration, seed)
	startedAt := time.Now()
	result, err := send(ctx, messageusecase.SendCommand{
		FromUID:     client.target.SenderUID,
		ChannelID:   client.target.RecipientUID,
		ChannelType: client.target.ChannelType,
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		Payload:     append([]byte(nil), payload...),
	})
	sendLatency := time.Since(startedAt)
	if err != nil {
		return recvStressRecord{}, fmt.Sprintf("worker=%d recipient=%s connect_node=%d phase=%s iteration=%d send error=%v", worker, client.target.RecipientUID, client.target.ConnectNodeID, phase, iteration, err), false
	}
	if result.Reason != frame.ReasonSuccess || result.MessageID == 0 || result.MessageSeq == 0 {
		return recvStressRecord{}, fmt.Sprintf("worker=%d recipient=%s connect_node=%d phase=%s iteration=%d reason=%s message_id=%d message_seq=%d", worker, client.target.RecipientUID, client.target.ConnectNodeID, phase, iteration, result.Reason, result.MessageID, result.MessageSeq), false
	}

	recv, err := waitForRecvStressPacket(client, recvTimeout)
	recvLatency := time.Since(startedAt)
	if err != nil {
		return recvStressRecord{}, fmt.Sprintf("worker=%d recipient=%s connect_node=%d phase=%s iteration=%d recv error=%v", worker, client.target.RecipientUID, client.target.ConnectNodeID, phase, iteration, err), false
	}
	if failure := validateRecvStressPacket(client.target, result, clientMsgNo, payload, recv); failure != "" {
		return recvStressRecord{}, fmt.Sprintf("worker=%d recipient=%s connect_node=%d phase=%s iteration=%d recv_mismatch %s", worker, client.target.RecipientUID, client.target.ConnectNodeID, phase, iteration, failure), false
	}
	if err := writeRecvStressClientFrame(client, &frame.RecvackPacket{
		MessageID:  recv.MessageID,
		MessageSeq: recv.MessageSeq,
	}, recvTimeout); err != nil {
		return recvStressRecord{}, fmt.Sprintf("worker=%d recipient=%s connect_node=%d phase=%s iteration=%d recvack error=%v", worker, client.target.RecipientUID, client.target.ConnectNodeID, phase, iteration, err), false
	}

	return recvStressRecord{
		Worker:        worker,
		Iteration:     iteration,
		SenderUID:     client.target.SenderUID,
		RecipientUID:  client.target.RecipientUID,
		ChannelID:     client.target.ChannelID,
		ChannelType:   client.target.ChannelType,
		ClientSeq:     clientSeq,
		ClientMsgNo:   clientMsgNo,
		Payload:       append([]byte(nil), payload...),
		MessageID:     recv.MessageID,
		MessageSeq:    recv.MessageSeq,
		SendLatency:   sendLatency,
		RecvLatency:   recvLatency,
		OwnerNodeID:   client.target.OwnerNodeID,
		ConnectNodeID: client.target.ConnectNodeID,
	}, "", true
}

func waitForRecvStressPacket(client recvStressWorkerClient, timeout time.Duration) (*frame.RecvPacket, error) {
	f, err := readRecvStressClientFrame(client, timeout)
	if err != nil {
		return nil, err
	}
	recv, ok := f.(*frame.RecvPacket)
	if !ok {
		return nil, fmt.Errorf("expected *frame.RecvPacket, got %T", f)
	}
	return recv, nil
}

func validateRecvStressPacket(target recvStressTarget, result messageusecase.SendResult, clientMsgNo string, payload []byte, recv *frame.RecvPacket) string {
	if recv == nil {
		return "nil recv packet"
	}
	if recv.ClientMsgNo != clientMsgNo {
		return fmt.Sprintf("client_msg_no=%s/%s", recv.ClientMsgNo, clientMsgNo)
	}
	if recv.MessageID != result.MessageID {
		return fmt.Sprintf("message_id=%d/%d", recv.MessageID, result.MessageID)
	}
	if recv.MessageSeq != result.MessageSeq {
		return fmt.Sprintf("message_seq=%d/%d", recv.MessageSeq, result.MessageSeq)
	}
	if recv.FromUID != target.SenderUID {
		return fmt.Sprintf("from_uid=%s/%s", recv.FromUID, target.SenderUID)
	}
	if recv.ChannelType != target.ChannelType {
		return fmt.Sprintf("channel_type=%d/%d", recv.ChannelType, target.ChannelType)
	}
	expectedChannelID := target.ChannelID
	if target.ChannelType == frame.ChannelTypePerson {
		expectedChannelID = target.SenderUID
	}
	if recv.ChannelID != expectedChannelID {
		return fmt.Sprintf("channel_id=%s/%s", recv.ChannelID, expectedChannelID)
	}
	if string(recv.Payload) != string(payload) {
		return fmt.Sprintf("payload=%q/%q", string(recv.Payload), string(payload))
	}
	return ""
}

func readRecvStressClientFrame(client recvStressWorkerClient, timeout time.Duration) (frame.Frame, error) {
	if client.reader != nil {
		return client.reader.ReadWithin(timeout)
	}
	return readSendStressFrameWithin(client.conn, timeout)
}

func writeRecvStressClientFrame(client recvStressWorkerClient, f frame.Frame, timeout time.Duration) error {
	if client.writeMu == nil {
		return writeSendStressFrame(client.conn, f, timeout)
	}
	client.writeMu.Lock()
	defer client.writeMu.Unlock()
	return writeSendStressFrame(client.conn, f, timeout)
}

func runRecvStressWorker(client recvStressWorkerClient, send recvStressSendFunc, worker int, cfg recvStressConfig, deadline time.Time) (recvStressOutcome, []recvStressRecord, []string) {
	records := make([]recvStressRecord, 0, cfg.MessagesPerWorker)
	failures := make([]string, 0, 1)
	outcome := recvStressOutcome{}

	for iteration := 0; iteration < cfg.MessagesPerWorker; iteration++ {
		if time.Now().After(deadline) {
			break
		}

		outcome.Total++
		payload := recvStressPayload(worker, client.target, iteration, cfg.Seed)
		record, failure, ok := executeRecvStressAttempt(context.Background(), client, send, worker, "measure", iteration, cfg.Seed, payload, cfg.RecvTimeout)
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

func warmupRecvStressClients(t *testing.T, owner *App, clients []recvStressWorkerClient, cfg recvStressConfig) []recvStressRecord {
	t.Helper()
	require.NotNil(t, owner)
	require.NotEmpty(t, clients)

	recvTimeout := cfg.RecvTimeout
	if recvTimeout < recvStressWarmupRecvTimeout {
		recvTimeout = recvStressWarmupRecvTimeout
	}
	send := func(ctx context.Context, cmd messageusecase.SendCommand) (messageusecase.SendResult, error) {
		return owner.Message().Send(ctx, cmd)
	}

	startedAt := time.Now()
	var wg sync.WaitGroup
	var mu sync.Mutex
	records := make([]recvStressRecord, 0, len(clients))
	failures := make([]string, 0, len(clients))
	for worker, client := range clients {
		worker := worker
		client := client
		wg.Add(1)
		go func() {
			defer wg.Done()

			payload := recvStressPayload(worker, client.target, -1, cfg.Seed)
			record, failure, ok := executeRecvStressAttempt(context.Background(), client, send, worker, "warmup", -1, cfg.Seed, payload, recvTimeout)

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

	t.Logf("recv stress warmup: workers=%d success=%d failed=%d timeout=%s duration=%s", len(clients), len(records), len(failures), recvTimeout, time.Since(startedAt))
	if len(failures) > 0 {
		t.Fatalf("recv stress warmup failures: %s", strings.Join(failures, " | "))
	}
	return records
}

func runRecvStressClient(t *testing.T, app *App, recipientUID string, cfg recvStressConfig) net.Conn {
	t.Helper()
	require.NotNil(t, app)
	require.NotEmpty(t, recipientUID)
	require.NotZero(t, cfg.DialTimeout)

	conn, err := dialRecvStressClient(app, recipientUID, cfg)
	require.NoError(t, err)
	return conn
}

func dialRecvStressClient(app *App, recipientUID string, cfg recvStressConfig) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", app.Gateway().ListenerAddr("tcp-wkproto"), cfg.DialTimeout)
	if err != nil {
		return nil, err
	}

	if err := writeSendStressFrame(conn, &frame.ConnectPacket{
		Version:         frame.LatestVersion,
		UID:             recipientUID,
		DeviceID:        recipientUID + "-recv-stress-device",
		DeviceFlag:      frame.APP,
		ClientTimestamp: time.Now().UnixMilli(),
	}, cfg.DialTimeout); err != nil {
		_ = conn.Close()
		return nil, err
	}

	f, err := readSendStressFrameWithin(conn, cfg.RecvTimeout)
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

func waitForRecvStressRoute(t *testing.T, app *App, uid string) {
	t.Helper()
	require.Eventually(t, func() bool {
		routes, err := app.presenceApp.EndpointsByUID(context.Background(), uid)
		return err == nil && len(routes) > 0
	}, 3*time.Second, 20*time.Millisecond, "recipient route %s did not become visible", uid)
}
