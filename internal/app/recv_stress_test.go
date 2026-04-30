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

	deliveryruntime "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestRecvStressConfigDefaultsAndOverrides(t *testing.T) {
	clearRecvStressConfigEnv(t)
	defaultCfg := loadRecvStressConfig(t)
	require.False(t, defaultCfg.Enabled)
	require.Equal(t, recvStressModeLatency, defaultCfg.Mode)
	require.Equal(t, 1, defaultCfg.MaxInflightPerWorker)
	require.Equal(t, 5*time.Second, defaultCfg.Duration)
	require.Equal(t, max(4, runtime.GOMAXPROCS(0)), defaultCfg.Workers)
	require.Equal(t, max(8, defaultCfg.Workers), defaultCfg.Recipients)
	require.Equal(t, 50, defaultCfg.MessagesPerWorker)
	require.Equal(t, 3*time.Second, defaultCfg.DialTimeout)
	require.Equal(t, 5*time.Second, defaultCfg.RecvTimeout)
	require.EqualValues(t, 20260429, defaultCfg.Seed)

	t.Setenv(recvStressEnv, "1")
	t.Setenv(recvStressModeEnv, string(recvStressModeThroughput))
	t.Setenv(recvStressDurationEnv, "11s")
	t.Setenv(recvStressWorkersEnv, "3")
	t.Setenv(recvStressRecipientsEnv, "5")
	t.Setenv(recvStressMessagesPerWorkerEnv, "7")
	t.Setenv(recvStressMaxInflightEnv, "17")
	t.Setenv(recvStressDialTimeoutEnv, "2s")
	t.Setenv(recvStressRecvTimeoutEnv, "4s")
	t.Setenv(recvStressSeedEnv, "42")

	cfg := loadRecvStressConfig(t)
	require.True(t, cfg.Enabled)
	require.Equal(t, recvStressModeThroughput, cfg.Mode)
	require.Equal(t, 17, cfg.MaxInflightPerWorker)
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

func TestLoadRecvStressConfigRejectsInvalidThroughputInflight(t *testing.T) {
	env := map[string]string{
		recvStressEnv:            "1",
		recvStressModeEnv:        string(recvStressModeThroughput),
		recvStressMaxInflightEnv: "0",
	}

	_, err := loadRecvStressConfigFromEnv(func(name string) (string, bool) {
		value, ok := env[name]
		return value, ok
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), recvStressMaxInflightEnv)
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
	submit := func(ctx context.Context, env deliveryruntime.CommittedEnvelope) error {
		require.Equal(t, "sender-a@recipient-a", env.ChannelID)
		require.Equal(t, frame.ChannelTypePerson, env.ChannelType)
		require.Equal(t, uint64(99000000002), env.MessageID)
		require.Equal(t, uint64(2), env.MessageSeq)
		require.Equal(t, "sender-a", env.FromUID)
		require.Equal(t, "recv-stress-0-recipient-a-00-99", env.ClientMsgNo)
		require.Equal(t, []byte("payload-99"), env.Payload)

		go func() {
			recv := &frame.RecvPacket{
				MessageID:   int64(env.MessageID),
				MessageSeq:  env.MessageSeq,
				ClientMsgNo: env.ClientMsgNo,
				ChannelID:   env.FromUID,
				ChannelType: env.ChannelType,
				FromUID:     env.FromUID,
				Payload:     append([]byte(nil), env.Payload...),
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

		return nil
	}

	record, failure, ok := executeRecvStressAttempt(context.Background(), client, submit, 0, "measure", 0, 99, []byte("payload-99"), time.Second)
	require.True(t, ok, failure)
	require.Empty(t, failure)
	require.Equal(t, "sender-a", record.SenderUID)
	require.Equal(t, "recipient-a", record.RecipientUID)
	require.Equal(t, "recv-stress-0-recipient-a-00-99", record.ClientMsgNo)
	require.EqualValues(t, 99000000002, record.MessageID)
	require.EqualValues(t, 2, record.MessageSeq)
	require.Equal(t, []byte("payload-99"), record.Payload)
	require.Positive(t, record.RecvLatency)

	select {
	case ack := <-ackCh:
		require.EqualValues(t, 99000000002, ack.MessageID)
		require.EqualValues(t, 2, ack.MessageSeq)
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
	submit := func(ctx context.Context, env deliveryruntime.CommittedEnvelope) error {
		go func() {
			_ = writeSendStressFrame(serverConn, &frame.RecvPacket{
				MessageID:   int64(env.MessageID),
				MessageSeq:  env.MessageSeq,
				ClientMsgNo: env.ClientMsgNo,
				ChannelID:   env.FromUID,
				ChannelType: env.ChannelType,
				FromUID:     env.FromUID,
				Payload:     []byte("other-payload"),
			}, time.Second)
		}()
		return nil
	}

	_, failure, ok := executeRecvStressAttempt(context.Background(), client, submit, 0, "measure", 0, 99, []byte("payload-99"), time.Second)
	require.False(t, ok)
	require.Contains(t, failure, "recv_mismatch")
}

func TestExecuteRecvStressAttemptTimesOutBlockedSubmit(t *testing.T) {
	client := recvStressWorkerClient{
		target: recvStressTarget{
			SenderUID:     "sender-a",
			RecipientUID:  "recipient-a",
			ChannelID:     "sender-a@recipient-a",
			ChannelType:   frame.ChannelTypePerson,
			ConnectNodeID: 2,
		},
	}
	submit := func(ctx context.Context, env deliveryruntime.CommittedEnvelope) error {
		<-ctx.Done()
		return ctx.Err()
	}

	_, failure, ok := executeRecvStressAttempt(context.Background(), client, submit, 0, "measure", 0, 99, []byte("payload-99"), 10*time.Millisecond)
	require.False(t, ok)
	require.Contains(t, failure, "submit error=context deadline exceeded")
}

func TestExecuteRecvStressAttemptAcksAndSkipsStaleRecvPackets(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	client := recvStressWorkerClient{
		target: recvStressTarget{
			SenderUID:    "sender-a",
			RecipientUID: "recipient-a",
			ChannelID:    "sender-a@recipient-a",
			ChannelType:  frame.ChannelTypePerson,
		},
		conn:   clientConn,
		reader: newSendStressFrameReader(clientConn),
	}
	ackCh := make(chan *frame.RecvackPacket, 2)
	submit := func(ctx context.Context, env deliveryruntime.CommittedEnvelope) error {
		go func() {
			for _, recv := range []*frame.RecvPacket{
				{
					MessageID:   9001,
					MessageSeq:  1,
					ClientMsgNo: recvStressClientMsgNo(0, "recipient-a", -1, 99),
					ChannelID:   env.FromUID,
					ChannelType: env.ChannelType,
					FromUID:     env.FromUID,
					Payload:     []byte("stale"),
				},
				{
					MessageID:   int64(env.MessageID),
					MessageSeq:  env.MessageSeq,
					ClientMsgNo: env.ClientMsgNo,
					ChannelID:   env.FromUID,
					ChannelType: env.ChannelType,
					FromUID:     env.FromUID,
					Payload:     append([]byte(nil), env.Payload...),
				},
			} {
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
			}
		}()
		return nil
	}

	record, failure, ok := executeRecvStressAttempt(context.Background(), client, submit, 0, "measure", 0, 99, []byte("payload-99"), time.Second)
	require.True(t, ok, failure)
	require.Empty(t, failure)
	require.EqualValues(t, 99000000002, record.MessageID)
	require.EqualValues(t, 2, record.MessageSeq)

	var firstAck *frame.RecvackPacket
	var secondAck *frame.RecvackPacket
	select {
	case firstAck = <-ackCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for stale recvack")
	}
	select {
	case secondAck = <-ackCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for current recvack")
	}
	require.EqualValues(t, 9001, firstAck.MessageID)
	require.EqualValues(t, 1, firstAck.MessageSeq)
	require.EqualValues(t, 99000000002, secondAck.MessageID)
	require.EqualValues(t, 2, secondAck.MessageSeq)
}

func TestRunRecvStressWorkerThroughputUsesConfiguredInflight(t *testing.T) {
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
			ConnectNodeID: 1,
		},
		conn:    clientConn,
		reader:  newSendStressFrameReader(clientConn),
		writeMu: &sync.Mutex{},
	}

	submitted := make(chan deliveryruntime.CommittedEnvelope, 4)
	submit := func(ctx context.Context, env deliveryruntime.CommittedEnvelope) error {
		select {
		case submitted <- env:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	serverErrCh := make(chan error, 1)
	go func() {
		for batch := 0; batch < 2; batch++ {
			first := <-submitted
			second := <-submitted
			select {
			case extra := <-submitted:
				serverErrCh <- fmt.Errorf("worker submitted %s before earlier throughput attempts completed", extra.ClientMsgNo)
				return
			default:
			}
			for _, env := range []deliveryruntime.CommittedEnvelope{first, second} {
				recv := &frame.RecvPacket{
					MessageID:   int64(env.MessageID),
					MessageSeq:  env.MessageSeq,
					ClientMsgNo: env.ClientMsgNo,
					ChannelID:   env.FromUID,
					ChannelType: env.ChannelType,
					FromUID:     env.FromUID,
					Payload:     append([]byte(nil), env.Payload...),
				}
				if err := writeSendStressFrame(serverConn, recv, time.Second); err != nil {
					serverErrCh <- err
					return
				}
				f, err := readSendStressFrameWithin(serverConn, time.Second)
				if err != nil {
					serverErrCh <- err
					return
				}
				if _, ok := f.(*frame.RecvackPacket); !ok {
					serverErrCh <- fmt.Errorf("expected *frame.RecvackPacket, got %T", f)
					return
				}
			}
		}
		serverErrCh <- nil
	}()

	cfg := recvStressConfig{
		Mode:                 recvStressModeThroughput,
		MaxInflightPerWorker: 2,
		MessagesPerWorker:    4,
		RecvTimeout:          time.Second,
		Seed:                 99,
	}
	outcome, records, failures, err := runRecvStressWorkerThroughput(client, submit, 0, cfg, time.Now().Add(time.Second))
	require.NoError(t, err)
	require.NoError(t, <-serverErrCh)
	require.EqualValues(t, 4, outcome.Total)
	require.EqualValues(t, 4, outcome.Success)
	require.Zero(t, outcome.Failed)
	require.Empty(t, failures)
	require.Len(t, records, 4)
}

func clearRecvStressConfigEnv(t *testing.T) {
	t.Helper()

	for _, name := range []string{
		recvStressEnv,
		recvStressModeEnv,
		recvStressDurationEnv,
		recvStressWorkersEnv,
		recvStressRecipientsEnv,
		recvStressMessagesPerWorkerEnv,
		recvStressMaxInflightEnv,
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
	recvStressModeEnv              = "WK_RECV_STRESS_MODE"
	recvStressDurationEnv          = "WK_RECV_STRESS_DURATION"
	recvStressWorkersEnv           = "WK_RECV_STRESS_WORKERS"
	recvStressRecipientsEnv        = "WK_RECV_STRESS_RECIPIENTS"
	recvStressMessagesPerWorkerEnv = "WK_RECV_STRESS_MESSAGES_PER_WORKER"
	recvStressMaxInflightEnv       = "WK_RECV_STRESS_MAX_INFLIGHT_PER_WORKER"
	recvStressDialTimeoutEnv       = "WK_RECV_STRESS_DIAL_TIMEOUT"
	recvStressRecvTimeoutEnv       = "WK_RECV_STRESS_RECV_TIMEOUT"
	recvStressSeedEnv              = "WK_RECV_STRESS_SEED"
	recvStressWarmupRecvTimeout    = 12 * time.Second
	recvStressThroughputInflight   = 32
)

type recvStressMode string

const (
	recvStressModeLatency    recvStressMode = "latency"
	recvStressModeThroughput recvStressMode = "throughput"
)

type recvStressConfig struct {
	// Enabled gates the expensive receive path stress test behind an explicit opt-in.
	Enabled bool
	// Mode selects latency-style request/response or throughput-style multi-inflight receive measurement.
	Mode recvStressMode
	// MaxInflightPerWorker caps outstanding synthetic deliveries per worker in throughput mode.
	MaxInflightPerWorker int
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
	// RecvTimeout bounds the delivery-submit-to-recv wait for each measured message.
	RecvTimeout time.Duration
	// Seed keeps generated message identifiers and payloads reproducible.
	Seed int64
}

// recvStressTarget describes one sender-to-online-recipient route under load.
type recvStressTarget struct {
	// SenderUID is the offline logical sender used to create person messages.
	SenderUID string
	// RecipientUID is the online user connected through WKProto.
	RecipientUID string
	// ChannelID is the normalized channel ID carried by the synthetic committed envelope.
	ChannelID string
	// ChannelType is the WuKong channel type for the target.
	ChannelType uint8
	// OwnerNodeID is the node whose delivery runtime accepts the synthetic envelope.
	OwnerNodeID uint64
	// ConnectNodeID is the gateway node where the recipient stays online.
	ConnectNodeID uint64
}

// recvStressRecord captures one successful delivery submit-to-receive measurement.
type recvStressRecord struct {
	// Worker is the zero-based worker index that produced the record.
	Worker int
	// Iteration is the per-worker message index.
	Iteration int
	// SenderUID is the logical sender written into the synthetic committed envelope.
	SenderUID string
	// RecipientUID is the online route that received the message.
	RecipientUID string
	// ChannelID is the normalized channel ID carried by the synthetic committed envelope.
	ChannelID string
	// ChannelType is the WuKong channel type for the received message.
	ChannelType uint8
	// ClientSeq is the generated client sequence for the synthetic envelope.
	ClientSeq uint64
	// ClientMsgNo is the generated idempotency key used to correlate recv packets.
	ClientMsgNo string
	// Payload is the expected message body observed by the recipient.
	Payload []byte
	// MessageID is the generated committed message ID received by the client.
	MessageID int64
	// MessageSeq is the generated committed channel sequence received by the client.
	MessageSeq uint64
	// SubmitLatency is the time spent enqueueing the synthetic envelope into delivery runtime.
	SubmitLatency time.Duration
	// RecvLatency is the elapsed time from delivery submit start to recipient recv.
	RecvLatency time.Duration
	// OwnerNodeID is the node whose delivery runtime accepted the synthetic envelope.
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
	// Failed is the number of attempts that stopped with submit, receive, or ack errors.
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
	// target identifies the sender, recipient, and delivery route under test.
	target recvStressTarget
	// conn is the recipient-side WKProto TCP connection.
	conn net.Conn
	// reader preserves partial WKProto frames across short read deadlines.
	reader *sendStressFrameReader
	// writeMu serializes recvack writes if a future reader loop shares the connection.
	writeMu *sync.Mutex
}

type recvStressSubmitFunc func(context.Context, deliveryruntime.CommittedEnvelope) error

// recvStressAttemptResult is the terminal state for one throughput-mode delivery.
type recvStressAttemptResult struct {
	record  recvStressRecord
	failure string
	ok      bool
}

// recvStressPendingAttempt keeps enough state to validate and record an inflight receive.
type recvStressPendingAttempt struct {
	client        recvStressWorkerClient
	worker        int
	phase         string
	iteration     int
	clientSeq     uint64
	clientMsgNo   string
	payload       []byte
	messageID     uint64
	messageSeq    uint64
	startedAt     time.Time
	submitLatency time.Duration
	onComplete    func(recvStressAttemptResult)
}

// recvStressInflightTracker correlates asynchronous Recv packets by ClientMsgNo.
type recvStressInflightTracker struct {
	mu      sync.Mutex
	pending map[string]*recvStressPendingAttempt
}

type recvStressThreeNodeRunSelection struct {
	cfg recvStressConfig
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
	mode, ok, err := parseRecvStressMode(lookupEnvValue(lookup, recvStressModeEnv))
	if err != nil {
		return recvStressConfig{}, fmt.Errorf("parse %s: %w", recvStressModeEnv, err)
	}
	if !ok {
		mode = recvStressModeLatency
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
		Enabled:              enabled,
		Mode:                 mode,
		Duration:             duration,
		Workers:              workers,
		MessagesPerWorker:    messagesPerWorker,
		DialTimeout:          dialTimeout,
		RecvTimeout:          recvTimeout,
		Seed:                 seed,
		MaxInflightPerWorker: 1,
	}
	if cfg.Mode == recvStressModeThroughput {
		maxInflight, err := sendStressEnvInt(lookup, recvStressMaxInflightEnv, recvStressThroughputInflight)
		if err != nil {
			return recvStressConfig{}, err
		}
		cfg.MaxInflightPerWorker = maxInflight
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
	switch cfg.Mode {
	case "", recvStressModeLatency, recvStressModeThroughput:
	default:
		return fmt.Errorf("%s must be one of %q or %q, got %q", recvStressModeEnv, recvStressModeLatency, recvStressModeThroughput, cfg.Mode)
	}
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
	if cfg.Mode == recvStressModeThroughput && cfg.MaxInflightPerWorker <= 0 {
		return fmt.Errorf("%s must be > 0, got %d", recvStressMaxInflightEnv, cfg.MaxInflightPerWorker)
	}
	return nil
}

func parseRecvStressMode(value string) (recvStressMode, bool, error) {
	if strings.TrimSpace(value) == "" {
		return "", false, nil
	}
	switch recvStressMode(strings.ToLower(strings.TrimSpace(value))) {
	case recvStressModeLatency:
		return recvStressModeLatency, true, nil
	case recvStressModeThroughput:
		return recvStressModeThroughput, true, nil
	default:
		return "", true, strconv.ErrSyntax
	}
}

func newRecvStressInflightTracker() *recvStressInflightTracker {
	return &recvStressInflightTracker{
		pending: make(map[string]*recvStressPendingAttempt),
	}
}

func (t *recvStressInflightTracker) Start(client recvStressWorkerClient, worker int, phase string, iteration int, env deliveryruntime.CommittedEnvelope, payload []byte, startedAt time.Time, onComplete func(recvStressAttemptResult)) {
	t.mu.Lock()
	t.pending[env.ClientMsgNo] = &recvStressPendingAttempt{
		client:      client,
		worker:      worker,
		phase:       phase,
		iteration:   iteration,
		clientSeq:   env.ClientSeq,
		clientMsgNo: env.ClientMsgNo,
		payload:     append([]byte(nil), payload...),
		messageID:   env.MessageID,
		messageSeq:  env.MessageSeq,
		startedAt:   startedAt,
		onComplete:  onComplete,
	}
	t.mu.Unlock()
}

func (t *recvStressInflightTracker) MarkSubmitted(clientMsgNo string, submitLatency time.Duration) {
	t.mu.Lock()
	if attempt, ok := t.pending[clientMsgNo]; ok {
		attempt.submitLatency = submitLatency
	}
	t.mu.Unlock()
}

func (t *recvStressInflightTracker) Complete(recv *frame.RecvPacket) (bool, error) {
	if recv == nil {
		return false, fmt.Errorf("recv stress inflight tracker: nil recv")
	}

	t.mu.Lock()
	attempt, ok := t.pending[recv.ClientMsgNo]
	if ok {
		delete(t.pending, recv.ClientMsgNo)
	}
	t.mu.Unlock()
	if !ok {
		return false, nil
	}

	if failure := validateRecvStressPacket(attempt.client.target, attempt.messageID, attempt.messageSeq, attempt.clientMsgNo, attempt.payload, recv); failure != "" {
		t.finish(attempt, recvStressAttemptResult{
			failure: fmt.Sprintf("worker=%d recipient=%s connect_node=%d phase=%s iteration=%d recv_mismatch %s", attempt.worker, attempt.client.target.RecipientUID, attempt.client.target.ConnectNodeID, attempt.phase, attempt.iteration, failure),
		})
		return true, nil
	}

	t.finish(attempt, recvStressAttemptResult{
		ok: true,
		record: recvStressRecord{
			Worker:        attempt.worker,
			Iteration:     attempt.iteration,
			SenderUID:     attempt.client.target.SenderUID,
			RecipientUID:  attempt.client.target.RecipientUID,
			ChannelID:     attempt.client.target.ChannelID,
			ChannelType:   attempt.client.target.ChannelType,
			ClientSeq:     attempt.clientSeq,
			ClientMsgNo:   attempt.clientMsgNo,
			Payload:       append([]byte(nil), attempt.payload...),
			MessageID:     recv.MessageID,
			MessageSeq:    recv.MessageSeq,
			SubmitLatency: attempt.submitLatency,
			RecvLatency:   time.Since(attempt.startedAt),
			OwnerNodeID:   attempt.client.target.OwnerNodeID,
			ConnectNodeID: attempt.client.target.ConnectNodeID,
		},
	})
	return true, nil
}

func (t *recvStressInflightTracker) Fail(clientMsgNo string, failure string) bool {
	t.mu.Lock()
	attempt, ok := t.pending[clientMsgNo]
	if ok {
		delete(t.pending, clientMsgNo)
	}
	t.mu.Unlock()
	if !ok {
		return false
	}
	t.finish(attempt, recvStressAttemptResult{failure: failure})
	return true
}

func (t *recvStressInflightTracker) FailAll(format string, args ...any) {
	t.mu.Lock()
	pending := make([]*recvStressPendingAttempt, 0, len(t.pending))
	for clientMsgNo, attempt := range t.pending {
		delete(t.pending, clientMsgNo)
		pending = append(pending, attempt)
	}
	t.mu.Unlock()

	failure := fmt.Sprintf(format, args...)
	for _, attempt := range pending {
		t.finish(attempt, recvStressAttemptResult{failure: failure})
	}
}

func (t *recvStressInflightTracker) FailExpired(timeout time.Duration) int {
	if timeout <= 0 {
		return 0
	}
	now := time.Now()
	t.mu.Lock()
	expired := make([]*recvStressPendingAttempt, 0)
	for clientMsgNo, attempt := range t.pending {
		if now.Sub(attempt.startedAt) < timeout {
			continue
		}
		delete(t.pending, clientMsgNo)
		expired = append(expired, attempt)
	}
	t.mu.Unlock()

	for _, attempt := range expired {
		t.finish(attempt, recvStressAttemptResult{
			failure: fmt.Sprintf("worker=%d recipient=%s connect_node=%d phase=%s iteration=%d recv timeout after %s", attempt.worker, attempt.client.target.RecipientUID, attempt.client.target.ConnectNodeID, attempt.phase, attempt.iteration, timeout),
		})
	}
	return len(expired)
}

func (t *recvStressInflightTracker) Pending() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.pending)
}

func (t *recvStressInflightTracker) finish(attempt *recvStressPendingAttempt, result recvStressAttemptResult) {
	if attempt.onComplete != nil {
		attempt.onComplete(result)
	}
}

func recvStressAcceptancePreset() recvStressConfig {
	return recvStressConfig{
		Mode:                 recvStressModeLatency,
		MaxInflightPerWorker: 1,
		Duration:             15 * time.Second,
		Workers:              16,
		Recipients:           32,
		MessagesPerWorker:    50,
		DialTimeout:          3 * time.Second,
		RecvTimeout:          20 * time.Second,
		Seed:                 20260429,
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

	expectedAcceptance := preset
	expectedAcceptance.Enabled = true

	if !recvStressThreeNodeHasExplicitConfigEnv() || cfg == expectedAcceptance {
		return recvStressThreeNodeRunSelection{cfg: expectedAcceptance}
	}

	return recvStressThreeNodeRunSelection{cfg: cfg}
}

func recvStressThreeNodeHasExplicitConfigEnv() bool {
	for _, name := range []string{
		recvStressModeEnv,
		recvStressDurationEnv,
		recvStressWorkersEnv,
		recvStressRecipientsEnv,
		recvStressMessagesPerWorkerEnv,
		recvStressMaxInflightEnv,
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
	t.Setenv(recvStressModeEnv, string(preset.Mode))
	t.Setenv(recvStressDurationEnv, preset.Duration.String())
	t.Setenv(recvStressWorkersEnv, strconv.Itoa(preset.Workers))
	t.Setenv(recvStressRecipientsEnv, strconv.Itoa(preset.Recipients))
	t.Setenv(recvStressMessagesPerWorkerEnv, strconv.Itoa(preset.MessagesPerWorker))
	t.Setenv(recvStressMaxInflightEnv, strconv.Itoa(preset.MaxInflightPerWorker))
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

func recvStressMessageID(worker int, iteration int, seed int64) uint64 {
	if seed < 0 {
		seed = -seed
	}
	return uint64(seed)*1_000_000_000 + uint64(worker)*1_000_000 + recvStressMessageSeq(iteration)
}

func recvStressMessageSeq(iteration int) uint64 {
	if iteration < -1 {
		iteration = -1
	}
	return uint64(iteration + 2)
}

func buildRecvStressEnvelope(client recvStressWorkerClient, worker int, iteration int, seed int64, payload []byte) deliveryruntime.CommittedEnvelope {
	clientSeq := recvStressMessageSeq(iteration)
	clientMsgNo := recvStressClientMsgNo(worker, client.target.RecipientUID, iteration, seed)
	return deliveryruntime.CommittedEnvelope{
		Message: channel.Message{
			MessageID:   recvStressMessageID(worker, iteration, seed),
			MessageSeq:  recvStressMessageSeq(iteration),
			ClientSeq:   clientSeq,
			ClientMsgNo: clientMsgNo,
			Timestamp:   int32(time.Now().Unix()),
			ChannelID:   client.target.ChannelID,
			ChannelType: client.target.ChannelType,
			FromUID:     client.target.SenderUID,
			Payload:     append([]byte(nil), payload...),
		},
	}
}

func executeRecvStressAttempt(ctx context.Context, client recvStressWorkerClient, submit recvStressSubmitFunc, worker int, phase string, iteration int, seed int64, payload []byte, recvTimeout time.Duration) (recvStressRecord, string, bool) {
	if submit == nil {
		return recvStressRecord{}, fmt.Sprintf("worker=%d recipient=%s connect_node=%d phase=%s iteration=%d submit function is nil", worker, client.target.RecipientUID, client.target.ConnectNodeID, phase, iteration), false
	}

	env := buildRecvStressEnvelope(client, worker, iteration, seed, payload)
	startedAt := time.Now()
	submitCtx := ctx
	var cancel context.CancelFunc
	waitTimeout := recvTimeout
	var attemptDeadline time.Time
	if recvTimeout > 0 {
		attemptDeadline = startedAt.Add(recvTimeout)
		submitCtx, cancel = context.WithDeadline(ctx, attemptDeadline)
		defer cancel()
	}
	if err := submit(submitCtx, env); err != nil {
		return recvStressRecord{}, fmt.Sprintf("worker=%d recipient=%s connect_node=%d phase=%s iteration=%d submit error=%v", worker, client.target.RecipientUID, client.target.ConnectNodeID, phase, iteration, err), false
	}
	submitLatency := time.Since(startedAt)
	if recvTimeout > 0 {
		waitTimeout = time.Until(attemptDeadline)
	}

	recv, err := waitForRecvStressExpectedPacket(client, client.target, env.MessageID, env.MessageSeq, env.ClientMsgNo, payload, waitTimeout)
	recvLatency := time.Since(startedAt)
	if err != nil {
		return recvStressRecord{}, fmt.Sprintf("worker=%d recipient=%s connect_node=%d phase=%s iteration=%d recv error=%v", worker, client.target.RecipientUID, client.target.ConnectNodeID, phase, iteration, err), false
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
		ClientSeq:     env.ClientSeq,
		ClientMsgNo:   env.ClientMsgNo,
		Payload:       append([]byte(nil), payload...),
		MessageID:     recv.MessageID,
		MessageSeq:    recv.MessageSeq,
		SubmitLatency: submitLatency,
		RecvLatency:   recvLatency,
		OwnerNodeID:   client.target.OwnerNodeID,
		ConnectNodeID: client.target.ConnectNodeID,
	}, "", true
}

func waitForRecvStressExpectedPacket(client recvStressWorkerClient, target recvStressTarget, messageID uint64, messageSeq uint64, clientMsgNo string, payload []byte, timeout time.Duration) (*frame.RecvPacket, error) {
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, fmt.Errorf("timed out waiting for recv packet client_msg_no=%s", clientMsgNo)
		}

		f, err := readRecvStressClientFrame(client, remaining)
		if err != nil {
			return nil, err
		}
		recv, ok := f.(*frame.RecvPacket)
		if !ok {
			return nil, fmt.Errorf("expected *frame.RecvPacket, got %T", f)
		}
		if recv.ClientMsgNo != clientMsgNo {
			// A prior delivery retry can arrive after warmup; ack it and keep waiting
			// so the stress run measures current message delivery instead of queue order.
			if err := writeRecvStressClientFrame(client, &frame.RecvackPacket{
				MessageID:  recv.MessageID,
				MessageSeq: recv.MessageSeq,
			}, remaining); err != nil {
				return nil, err
			}
			continue
		}
		if failure := validateRecvStressPacket(target, messageID, messageSeq, clientMsgNo, payload, recv); failure != "" {
			return nil, fmt.Errorf("recv_mismatch %s", failure)
		}
		return recv, nil
	}
}

func validateRecvStressPacket(target recvStressTarget, messageID uint64, messageSeq uint64, clientMsgNo string, payload []byte, recv *frame.RecvPacket) string {
	if recv == nil {
		return "nil recv packet"
	}
	if recv.ClientMsgNo != clientMsgNo {
		return fmt.Sprintf("client_msg_no=%s/%s", recv.ClientMsgNo, clientMsgNo)
	}
	if uint64(recv.MessageID) != messageID {
		return fmt.Sprintf("message_id=%d/%d", recv.MessageID, messageID)
	}
	if recv.MessageSeq != messageSeq {
		return fmt.Sprintf("message_seq=%d/%d", recv.MessageSeq, messageSeq)
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

func runRecvStressWorker(client recvStressWorkerClient, submit recvStressSubmitFunc, worker int, cfg recvStressConfig, deadline time.Time) (recvStressOutcome, []recvStressRecord, []string) {
	records := make([]recvStressRecord, 0, cfg.MessagesPerWorker)
	failures := make([]string, 0, 1)
	outcome := recvStressOutcome{}

	for iteration := 0; iteration < cfg.MessagesPerWorker; iteration++ {
		if time.Now().After(deadline) {
			break
		}

		outcome.Total++
		payload := recvStressPayload(worker, client.target, iteration, cfg.Seed)
		record, failure, ok := executeRecvStressAttempt(context.Background(), client, submit, worker, "measure", iteration, cfg.Seed, payload, cfg.RecvTimeout)
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

func runRecvStressWorkerThroughput(client recvStressWorkerClient, submit recvStressSubmitFunc, worker int, cfg recvStressConfig, deadline time.Time) (recvStressOutcome, []recvStressRecord, []string, error) {
	if submit == nil {
		return recvStressOutcome{}, nil, nil, fmt.Errorf("recv stress submit function is nil")
	}
	if cfg.MaxInflightPerWorker <= 0 {
		return recvStressOutcome{}, nil, nil, fmt.Errorf("%s must be > 0, got %d", recvStressMaxInflightEnv, cfg.MaxInflightPerWorker)
	}

	var (
		outcome  recvStressOutcome
		records  = make([]recvStressRecord, 0, cfg.MessagesPerWorker)
		failures = make([]string, 0, 1)
		mu       sync.Mutex
	)
	appendResult := func(result recvStressAttemptResult) {
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

	tracker := newRecvStressInflightTracker()
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
		readerErrCh <- readRecvStressThroughputPackets(client, cfg.RecvTimeout, tracker, writerDone, stop)
	}()

writerLoop:
	for iteration := 0; iteration < cfg.MessagesPerWorker; iteration++ {
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-stopWriter:
			break writerLoop
		default:
		}
		select {
		case slots <- struct{}{}:
		case <-stopWriter:
			break writerLoop
		}

		payload := recvStressPayload(worker, client.target, iteration, cfg.Seed)
		env := buildRecvStressEnvelope(client, worker, iteration, cfg.Seed, payload)
		startedAt := time.Now()
		tracker.Start(client, worker, "measure", iteration, env, payload, startedAt, func(result recvStressAttemptResult) {
			appendResult(result)
			<-slots
		})
		mu.Lock()
		outcome.Total++
		mu.Unlock()

		submitCtx, cancel := context.WithDeadline(context.Background(), minRecvStressDeadline(deadline, startedAt.Add(cfg.RecvTimeout)))
		err := submit(submitCtx, env)
		cancel()
		if err != nil {
			tracker.Fail(env.ClientMsgNo, fmt.Sprintf("worker=%d recipient=%s connect_node=%d phase=%s iteration=%d submit error=%v", worker, client.target.RecipientUID, client.target.ConnectNodeID, "measure", iteration, err))
			stop()
			break
		}
		tracker.MarkSubmitted(env.ClientMsgNo, time.Since(startedAt))
	}

	close(writerDone)
	readerErr := <-readerErrCh
	if readerErr != nil {
		return outcome, records, failures, readerErr
	}
	return outcome, records, failures, nil
}

func readRecvStressThroughputPackets(client recvStressWorkerClient, recvTimeout time.Duration, tracker *recvStressInflightTracker, writerDone <-chan struct{}, stopWriter func()) error {
	for {
		writerClosed := false
		select {
		case <-writerDone:
			writerClosed = true
		default:
		}
		if writerClosed && tracker.Pending() == 0 {
			return nil
		}
		if expired := tracker.FailExpired(recvTimeout); expired > 0 {
			stopWriter()
			continue
		}

		f, err := readRecvStressClientFrame(client, minSendStressDuration(recvTimeout, 200*time.Millisecond))
		if err != nil {
			if isSendStressTimeout(err) {
				continue
			}
			tracker.FailAll("throughput recv reader error: %v", err)
			stopWriter()
			return err
		}

		recv, ok := f.(*frame.RecvPacket)
		if !ok {
			err := fmt.Errorf("unexpected frame while waiting for throughput recv: %T", f)
			tracker.FailAll("%v", err)
			stopWriter()
			return err
		}
		if err := writeRecvStressClientFrame(client, &frame.RecvackPacket{
			MessageID:  recv.MessageID,
			MessageSeq: recv.MessageSeq,
		}, recvTimeout); err != nil {
			tracker.FailAll("throughput recvack write error: %v", err)
			stopWriter()
			return err
		}
		if _, err := tracker.Complete(recv); err != nil {
			tracker.FailAll("throughput recv reader mismatch: %v", err)
			stopWriter()
			return err
		}
	}
}

func minRecvStressDeadline(a, b time.Time) time.Time {
	if a.IsZero() {
		return b
	}
	if b.IsZero() || a.Before(b) {
		return a
	}
	return b
}

func warmupRecvStressClients(t *testing.T, owner *App, clients []recvStressWorkerClient, cfg recvStressConfig) []recvStressRecord {
	t.Helper()
	require.NotNil(t, owner)
	require.NotEmpty(t, clients)

	recvTimeout := cfg.RecvTimeout
	if recvTimeout < recvStressWarmupRecvTimeout {
		recvTimeout = recvStressWarmupRecvTimeout
	}
	submit := func(ctx context.Context, env deliveryruntime.CommittedEnvelope) error {
		return owner.deliveryRuntime.Submit(ctx, env)
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
			record, failure, ok := executeRecvStressAttempt(context.Background(), client, submit, worker, "warmup", -1, cfg.Seed, payload, recvTimeout)

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
