package workload

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	defaultPersonDevicePrefix  = "bench-device"
	defaultPersonClientPrefix  = "bench-msg"
	defaultWorkloadTimeout     = 5 * time.Second
	verifyRecvModeFull         = "full"
	defaultMatchingBufferLimit = 1024
)

// autoRecvAckYieldDelay briefly leaves the reader slot open when foreground matchers are queued.
var autoRecvAckYieldDelay = time.Millisecond

// PersonClient extends a WKProto connection client with message send and read capabilities.
type PersonClient interface {
	ConnectionClient
	// Send writes one send packet for the connected client.
	Send(ctx context.Context, pkt *frame.SendPacket) error
	// ReadFrame reads one frame from the connected client stream.
	ReadFrame(ctx context.Context) (frame.Frame, error)
	// RecvAck writes one receive acknowledgment for a delivered message.
	RecvAck(ctx context.Context, messageID int64, messageSeq uint64) error
}

// PersonPair identifies one deterministic person-channel assignment.
type PersonPair struct {
	// ChannelIndex is the stable deterministic channel index for this pair.
	ChannelIndex int
	// SenderUID is the connected sender UID for the pair.
	SenderUID string
	// RecipientUID is the connected recipient UID for the pair.
	RecipientUID string
}

// PersonConfig controls a deterministic person-channel workload.
type PersonConfig struct {
	// RunID identifies the benchmark run used in generated payloads.
	RunID string
	// ProfileName identifies the channel profile used in generated payloads.
	ProfileName string
	// TrafficName identifies the traffic stream used in generated payloads.
	TrafficName string
	// SenderUID is the default sender when Pairs is empty.
	SenderUID string
	// RecipientUID is the default recipient when Pairs is empty.
	RecipientUID string
	// Pairs contains the assigned sender and recipient pairs for this worker.
	Pairs []PersonPair
	// ClientMsgPrefix is prepended to generated client message numbers.
	ClientMsgPrefix string
	// DevicePrefix is prepended to generated device identifiers.
	DevicePrefix string
	// PayloadSizeBytes controls the generated payload size. Zero keeps the base payload as-is.
	PayloadSizeBytes int
	// Rate is the per-channel send rate used during Run.
	Rate model.Rate
	// MaxConcurrency bounds concurrent send+sendack operations. Zero preserves sequential sends.
	MaxConcurrency int
	// Phase identifies the traffic phase used in generated message identities.
	Phase string
	// RunDuration is the measured duration used during Run.
	RunDuration time.Duration
	// WarmupDuration is the duration used during Warmup.
	WarmupDuration time.Duration
	// CooldownDuration is the drain wait used during Cooldown.
	CooldownDuration time.Duration
	// AckTimeout bounds the wait for a sendack after one send request.
	AckTimeout time.Duration
	// RecvTimeout bounds the wait for a delivered recv frame when verification is enabled.
	RecvTimeout time.Duration
	// VerifyRecvMode enables receive verification when set to "full".
	VerifyRecvMode string
	// RecvAck controls whether the recipient sends a receive acknowledgment after verification.
	RecvAck bool
	// Metrics stores the counters, latencies, and error samples for this workload.
	Metrics *metrics.Registry

	sleep func(context.Context, time.Duration) error
}

// PersonRunConfig controls one timed person workload execution.
type PersonRunConfig struct {
	// Phase identifies the traffic phase used in generated message identities.
	Phase string
	// Duration is the traffic window length for this workload execution.
	Duration time.Duration
	// Rate is the per-channel send rate for this workload execution.
	Rate model.Rate
}

// PersonWorkload executes deterministic person-channel benchmark traffic.
type PersonWorkload struct {
	cfg                      PersonConfig
	metrics                  *metrics.Registry
	pairs                    []PersonPair
	clients                  map[string]PersonClient
	useMessageIndexAsChannel bool
	// warmupOperationDeadline caps the final in-flight warmup operation tail.
	warmupOperationDeadline time.Time

	mu        sync.Mutex
	connected map[string]struct{}
}

// NewPersonWorkload validates the config, normalizes the pair list, and binds clients.
func NewPersonWorkload(cfg PersonConfig, clients map[string]PersonClient) (*PersonWorkload, error) {
	cfg.RunID = strings.TrimSpace(cfg.RunID)
	cfg.ProfileName = strings.TrimSpace(cfg.ProfileName)
	cfg.TrafficName = strings.TrimSpace(cfg.TrafficName)
	cfg.SenderUID = strings.TrimSpace(cfg.SenderUID)
	cfg.RecipientUID = strings.TrimSpace(cfg.RecipientUID)
	cfg.ClientMsgPrefix = strings.TrimSpace(cfg.ClientMsgPrefix)
	cfg.DevicePrefix = strings.TrimSpace(cfg.DevicePrefix)
	cfg.VerifyRecvMode = strings.TrimSpace(cfg.VerifyRecvMode)
	if cfg.ClientMsgPrefix == "" {
		cfg.ClientMsgPrefix = defaultPersonClientPrefix
	}
	if cfg.DevicePrefix == "" {
		cfg.DevicePrefix = defaultPersonDevicePrefix
	}
	if cfg.Metrics == nil {
		cfg.Metrics = metrics.NewRegistry()
	}
	if cfg.sleep == nil {
		cfg.sleep = sleepContext
	}

	useMessageIndexAsChannel := len(cfg.Pairs) == 0 && cfg.SenderUID != "" && cfg.RecipientUID != ""
	pairs, err := normalizePersonPairs(cfg)
	if err != nil {
		return nil, err
	}
	if len(pairs) == 0 {
		return nil, fmt.Errorf("person workload: at least one pair is required")
	}
	boundClients := make(map[string]PersonClient, len(clients))
	for uid, client := range clients {
		uid = strings.TrimSpace(uid)
		if uid == "" || client == nil {
			continue
		}
		boundClients[uid] = client
	}
	for _, pair := range pairs {
		if _, ok := boundClients[pair.SenderUID]; !ok {
			return nil, fmt.Errorf("person workload: missing client for sender %q", pair.SenderUID)
		}
		if _, ok := boundClients[pair.RecipientUID]; !ok {
			return nil, fmt.Errorf("person workload: missing client for recipient %q", pair.RecipientUID)
		}
	}
	return &PersonWorkload{
		cfg:                      cfg,
		metrics:                  cfg.Metrics,
		pairs:                    pairs,
		clients:                  boundClients,
		useMessageIndexAsChannel: useMessageIndexAsChannel,
		connected:                make(map[string]struct{}),
	}, nil
}

// Metrics returns the workload metrics registry.
func (w *PersonWorkload) Metrics() *metrics.Registry {
	if w == nil {
		return nil
	}
	return w.metrics
}

// WrapPersonClientsForConcurrentReads serializes ReadFrame access per UID and replays unmatched frames.
func WrapPersonClientsForConcurrentReads(clients map[string]PersonClient) map[string]PersonClient {
	wrapped := make(map[string]PersonClient, len(clients))
	for uid, client := range clients {
		if client == nil {
			continue
		}
		wrapped[uid] = &matchingPersonClient{client: client, bufferLimit: defaultMatchingBufferLimit}
	}
	return wrapped
}

// AutoRecvAckOptions controls how the background recv-ack drainer treats incoming frames.
type AutoRecvAckOptions struct {
	// BufferRecvFrames keeps drained recv frames available for explicit verification waiters.
	BufferRecvFrames bool
	// DisableRecvAck drains recv frames without acknowledging them.
	DisableRecvAck bool
}

// StartAutoRecvAck continuously drains recv frames and sends protocol receive acks.
func StartAutoRecvAck(clients map[string]PersonClient) context.CancelFunc {
	return StartAutoRecvAckWithOptions(clients, AutoRecvAckOptions{BufferRecvFrames: true})
}

// StartAutoRecvAckWithOptions continuously drains recv frames using explicit buffering policy.
func StartAutoRecvAckWithOptions(clients map[string]PersonClient, opts AutoRecvAckOptions) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	for _, client := range clients {
		starter, ok := client.(interface {
			startAutoRecvAckWithOptions(context.Context, AutoRecvAckOptions)
		})
		if !ok || starter == nil {
			continue
		}
		starter.startAutoRecvAckWithOptions(ctx, opts)
	}
	return cancel
}

// Connect opens the sender and recipient sessions for all assigned person pairs.
func (w *PersonWorkload) Connect(ctx context.Context) error {
	for _, uid := range w.uniqueUIDs() {
		if w.isConnected(uid) {
			continue
		}
		client, ok := w.clients[uid]
		if !ok {
			return fmt.Errorf("person workload: missing client for %q", uid)
		}
		if err := client.Connect(ctx, uid, w.deviceID(uid)); err != nil {
			w.recordError("person_connect_error", err)
			return err
		}
		w.mu.Lock()
		w.connected[uid] = struct{}{}
		w.mu.Unlock()
	}
	return nil
}

// Warmup runs low-rate traffic for the configured warmup duration.
func (w *PersonWorkload) Warmup(ctx context.Context) error {
	if w.cfg.WarmupDuration <= 0 {
		return nil
	}
	restore := w.useWarmupTimeouts()
	defer restore()
	return w.RunWindow(ctx, PersonRunConfig{Phase: "warmup", Duration: w.cfg.WarmupDuration, Rate: warmupRateForDuration(w.cfg.Rate, w.cfg.WarmupDuration)})
}

// Run sends rate-limited person traffic for the configured measured duration.
func (w *PersonWorkload) Run(ctx context.Context) error {
	return w.RunWindow(ctx, PersonRunConfig{Phase: "run", Duration: w.cfg.RunDuration, Rate: w.cfg.Rate})
}

// RunMeasuredWindow sends one uniquely named window at the configured measured rate.
func (w *PersonWorkload) RunMeasuredWindow(ctx context.Context, duration time.Duration, window int) error {
	return w.RunWindow(ctx, PersonRunConfig{Phase: fmt.Sprintf("run-window-%d", window), Duration: duration, Rate: w.cfg.Rate})
}

// Cooldown waits for the configured drain period without emitting new sends.
func (w *PersonWorkload) Cooldown(ctx context.Context) error {
	if w.cfg.CooldownDuration <= 0 {
		return nil
	}
	return w.cfg.sleep(ctx, w.cfg.CooldownDuration)
}

// RunWindow emits this workload's traffic within a caller-owned phase window.
func (w *PersonWorkload) RunWindow(ctx context.Context, cfg PersonRunConfig) error {
	return w.runFor(ctx, cfg)
}

func (w *PersonWorkload) runFor(ctx context.Context, cfg PersonRunConfig) error {
	if cfg.Duration <= 0 || cfg.Rate.PerSecond <= 0 {
		for _, pair := range w.pairs {
			if err := w.sendPairInPhase(ctx, pair, w.cfg.phaseName(cfg.Phase), pair.ChannelIndex); err != nil {
				return err
			}
		}
		return nil
	}
	totalMessages := scheduledMessageCount(cfg.Duration, cfg.Rate.PerSecond, len(w.pairs))
	if totalMessages <= 0 {
		return nil
	}
	interval := scheduledMessageInterval(cfg.Duration, totalMessages)
	phase := w.cfg.phaseName(cfg.Phase)
	if w.cfg.MaxConcurrency > 1 {
		stats := &scheduledMessageStats{}
		err := runScheduledMessagesByKeyWithStats(ctx, totalMessages, interval, w.cfg.MaxConcurrency, func(messageOffset int) string {
			pair := w.pairs[messageOffset%len(w.pairs)]
			return pair.SenderUID
		}, func(ctx context.Context, messageOffset int) error {
			pair := w.pairs[messageOffset%len(w.pairs)]
			err := w.sendPairInPhase(ctx, pair, phase, messageOffset)
			if shouldContinueTrafficOperationError(ctx, cfg.Phase, err) {
				return nil
			}
			return err
		}, stats)
		recordSchedulerStats(w.metrics, w.sendMetricLabels(phase), stats)
		return err
	}
	for messageOffset := 0; messageOffset < totalMessages; messageOffset++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		pair := w.pairs[messageOffset%len(w.pairs)]
		if err := w.sendPairInPhase(ctx, pair, phase, messageOffset); err != nil {
			if !shouldContinueTrafficOperationError(ctx, cfg.Phase, err) {
				return err
			}
		}
		if interval > 0 {
			if err := w.cfg.sleep(ctx, interval); err != nil {
				return err
			}
		}
	}
	return nil
}

// SendOne sends one deterministic person-channel message for the assigned pair at messageIndex.
func (w *PersonWorkload) SendOne(ctx context.Context, messageIndex int) error {
	if messageIndex < 0 {
		err := fmt.Errorf("person workload: message index must not be negative")
		w.recordError("person_send_error", err)
		return err
	}
	pair, err := w.pairForMessageIndex(messageIndex)
	if err != nil {
		w.recordError("person_send_error", err)
		return err
	}
	return w.sendPairInPhase(ctx, pair, w.cfg.phaseName("run"), messageIndex)
}

func (w *PersonWorkload) sendPairInPhase(ctx context.Context, pair PersonPair, phase string, messageIndex int) error {
	sender := w.clients[pair.SenderUID]
	recipient := w.clients[pair.RecipientUID]
	channelIndex := pair.ChannelIndex
	if w.useMessageIndexAsChannel {
		channelIndex = messageIndex
	}
	payload := w.buildPayload(phase, channelIndex, messageIndex)
	clientSeq := nextSendClientSeq(sender, uint64(messageIndex))
	clientMsgNo := w.clientMsgNo(phase, channelIndex, messageIndex)
	pkt := &frame.SendPacket{
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		ChannelID:   encodeBenchPersonChannel(pair.SenderUID, pair.RecipientUID),
		ChannelType: frame.ChannelTypePerson,
		Payload:     payload,
	}

	sendLabels := w.sendMetricLabels(phase)
	sendStart := time.Now()
	unlockSendack, err := lockSendackOperation(ctx, sender)
	if err != nil {
		if shouldRecordPhaseOperationError(ctx, err) {
			w.recordError("person_send_error", err)
			w.metrics.IncCounter("person_send_error_total", sendLabels)
		}
		return sessionOperationError(pair.SenderUID, "person sendack lock", err)
	}
	defer unlockSendack()
	if err := sender.Send(ctx, pkt); err != nil {
		if shouldRecordPhaseOperationError(ctx, err) {
			w.recordError("person_send_error", err)
			w.metrics.IncCounter("person_send_error_total", sendLabels)
		}
		return sessionOperationError(pair.SenderUID, "person send", err)
	}
	ack, err := w.waitForSendack(ctx, sender, clientSeq, clientMsgNo)
	if err != nil {
		if shouldRecordPhaseOperationError(ctx, err) {
			w.recordError("person_send_error", err)
			w.metrics.IncCounter("person_send_error_total", sendLabels)
		}
		return sessionOperationError(pair.SenderUID, "person sendack", err)
	}
	if ack.ReasonCode != frame.ReasonSuccess {
		err := fmt.Errorf("person workload: sendack rejected message %q with reason %s", clientMsgNo, ack.ReasonCode)
		w.recordError("person_send_error", err)
		w.metrics.IncCounter("person_send_error_total", sendLabels)
		return sessionOperationError(pair.SenderUID, "person sendack", err)
	}
	w.metrics.IncCounter("person_send_success_total", sendLabels)
	w.metrics.ObserveLatency("person_send_latency_seconds", sendLabels, time.Since(sendStart))

	if !strings.EqualFold(w.cfg.VerifyRecvMode, verifyRecvModeFull) {
		return nil
	}
	recvStart := time.Now()
	expectedMessageID := uint64(0)
	if ack.MessageID > 0 {
		expectedMessageID = uint64(ack.MessageID)
	}
	recv, err := w.waitForRecv(ctx, recipient, clientMsgNo, pair.SenderUID, expectedMessageID, ack.MessageSeq)
	if err != nil {
		if shouldRecordPhaseOperationError(ctx, err) {
			w.recordError("person_recv_error", err)
			w.metrics.IncCounter("person_recv_error_total", sendLabels)
		}
		return sessionOperationError(pair.RecipientUID, "person recv", err)
	}
	if string(recv.Payload) != string(payload) {
		err := fmt.Errorf("person workload: recv payload mismatch for %q", clientMsgNo)
		w.recordError("person_recv_error", err)
		w.metrics.IncCounter("person_recv_error_total", sendLabels)
		return sessionOperationError(pair.RecipientUID, "person recv", err)
	}
	if w.cfg.RecvAck {
		if err := recipient.RecvAck(ctx, recv.MessageID, recv.MessageSeq); err != nil {
			if shouldRecordPhaseOperationError(ctx, err) {
				w.recordError("person_recv_error", err)
				w.metrics.IncCounter("person_recv_error_total", sendLabels)
			}
			return sessionOperationError(pair.RecipientUID, "person recvack", err)
		}
	}
	w.metrics.IncCounter("person_recv_success_total", sendLabels)
	w.metrics.ObserveLatency("person_recv_latency_seconds", sendLabels, time.Since(recvStart))
	return nil
}

func (w *PersonWorkload) sendMetricLabels(phase string) metrics.Labels {
	return metrics.Labels{
		"phase":        stableMetricPhase(phase),
		"channel_type": model.ChannelTypePerson,
		"profile":      w.cfg.ProfileName,
		"traffic":      w.cfg.TrafficName,
	}
}

func stableMetricPhase(phase string) string {
	phase = strings.TrimSpace(phase)
	if strings.HasPrefix(phase, "run-window-") {
		return "run"
	}
	return phase
}

func (w *PersonWorkload) isConnected(uid string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.connected[uid]
	return ok
}

func (w *PersonWorkload) uniqueUIDs() []string {
	seen := make(map[string]struct{}, len(w.pairs)*2)
	uids := make([]string, 0, len(w.pairs)*2)
	for _, pair := range w.pairs {
		for _, uid := range []string{pair.SenderUID, pair.RecipientUID} {
			if _, ok := seen[uid]; ok {
				continue
			}
			seen[uid] = struct{}{}
			uids = append(uids, uid)
		}
	}
	return uids
}

func (w *PersonWorkload) pairForMessageIndex(messageIndex int) (PersonPair, error) {
	for _, pair := range w.pairs {
		if pair.ChannelIndex == messageIndex {
			return pair, nil
		}
	}
	if len(w.pairs) == 1 {
		return w.pairs[0], nil
	}
	return PersonPair{}, fmt.Errorf("person workload: no pair assigned to channel index %d", messageIndex)
}

func (w *PersonWorkload) waitForSendack(ctx context.Context, client PersonClient, clientSeq uint64, clientMsgNo string) (*frame.SendackPacket, error) {
	deadlineCtx, cancel := w.withTimeout(ctx, w.cfg.AckTimeout)
	defer cancel()
	f, err := readFrameMatching(deadlineCtx, client, func(f frame.Frame) bool {
		ack, ok := f.(*frame.SendackPacket)
		return ok && matchesExpectedSendack(ack, clientSeq, clientMsgNo)
	})
	if err != nil {
		return nil, fmt.Errorf("person workload: sendack not received for %q: %w", clientMsgNo, err)
	}
	return f.(*frame.SendackPacket), nil
}

func (w *PersonWorkload) waitForRecv(ctx context.Context, client PersonClient, clientMsgNo, senderUID string, expectedMessageID uint64, expectedMessageSeq uint64) (*frame.RecvPacket, error) {
	deadlineCtx, cancel := w.withTimeout(ctx, w.cfg.RecvTimeout)
	defer cancel()
	f, err := readFrameMatching(deadlineCtx, client, func(f frame.Frame) bool {
		recv, ok := f.(*frame.RecvPacket)
		if !ok {
			return false
		}
		if recv.FromUID != senderUID || recv.ChannelType != frame.ChannelTypePerson || recv.ClientMsgNo != clientMsgNo {
			return false
		}
		if expectedMessageID > 0 && recv.MessageID != int64(expectedMessageID) {
			return false
		}
		if expectedMessageSeq > 0 && recv.MessageSeq != expectedMessageSeq {
			return false
		}
		return true
	})
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("person workload: recv not received for %q: %w", clientMsgNo, err)
		}
		return nil, err
	}
	return f.(*frame.RecvPacket), nil
}

func (w *PersonWorkload) buildPayload(phase string, channelIndex, messageIndex int) []byte {
	base := w.payloadMarker(phase, channelIndex, messageIndex)
	if w.cfg.PayloadSizeBytes <= 0 {
		return []byte(base)
	}
	if len(base) >= w.cfg.PayloadSizeBytes {
		return []byte(base[:w.cfg.PayloadSizeBytes])
	}
	payload := make([]byte, 0, w.cfg.PayloadSizeBytes)
	payload = append(payload, base...)
	fill := fmt.Sprintf("|%s|%d|", w.cfg.ClientMsgPrefix, messageIndex)
	for len(payload) < w.cfg.PayloadSizeBytes {
		remaining := w.cfg.PayloadSizeBytes - len(payload)
		if remaining >= len(fill) {
			payload = append(payload, fill...)
			continue
		}
		payload = append(payload, fill[:remaining]...)
	}
	return payload
}

func (w *PersonWorkload) payloadMarker(phase string, channelIndex, messageIndex int) string {
	return fmt.Sprintf("run=%s profile=%s traffic=%s phase=%s channel=%d message=%d", w.cfg.RunID, w.cfg.ProfileName, w.cfg.TrafficName, phase, channelIndex, messageIndex)
}

func (w *PersonWorkload) clientMsgNo(phase string, channelIndex, messageIndex int) string {
	return fmt.Sprintf("%s-%s-%s-%s-%s-ch%d-msg%d", w.cfg.ClientMsgPrefix, w.cfg.RunID, w.cfg.ProfileName, w.cfg.TrafficName, phase, channelIndex, messageIndex)
}

func (cfg PersonConfig) phaseName(phase string) string {
	phase = strings.TrimSpace(phase)
	if phase != "" {
		return phase
	}
	phase = strings.TrimSpace(cfg.Phase)
	if phase != "" {
		return phase
	}
	return "run"
}

func encodeBenchPersonChannel(leftUID, rightUID string) string {
	leftHash := crc32.ChecksumIEEE([]byte(leftUID))
	rightHash := crc32.ChecksumIEEE([]byte(rightUID))
	if leftHash > rightHash {
		return leftUID + "@" + rightUID
	}
	if leftHash == rightHash && leftUID > rightUID {
		return leftUID + "@" + rightUID
	}
	return rightUID + "@" + leftUID
}

type matchingFrameReader interface {
	readFrameMatching(context.Context, func(frame.Frame) bool) (frame.Frame, error)
}

type sendackOperationLocker interface {
	lockSendackOperation(context.Context) (func(), error)
}

type sendClientSeqAllocator interface {
	nextSendClientSeq() uint64
}

type matchingPersonClient struct {
	client      PersonClient
	bufferLimit int
	mu          sync.Mutex
	clientSeq   atomic.Uint64
	buffer      []frame.Frame
	reading     bool
	notify      chan struct{}
	// foregroundMatchers tracks explicit matchers that should own socket reads until they return.
	foregroundMatchers int
	// foregroundWaiters tracks explicit matchers blocked behind the active read.
	foregroundWaiters     int
	autoRecvAck           bool
	autoRecvAckBufferRecv bool
	autoRecvAckDisableAck bool
	autoRecvAckReader     bool
	autoRecvAckReadErr    error
	debugReadFrames       uint64
	debugReadSendacks     uint64
	debugReadRecvs        uint64
	debugDroppedSendacks  uint64
	debugDroppedRecvs     uint64
	debugLastFrames       []string
}

func (c *matchingPersonClient) Connect(ctx context.Context, uid, deviceID string) error {
	return c.client.Connect(ctx, uid, deviceID)
}

func (c *matchingPersonClient) Send(ctx context.Context, pkt *frame.SendPacket) error {
	return c.client.Send(ctx, pkt)
}

func (c *matchingPersonClient) lockSendackOperation(ctx context.Context) (func(), error) {
	return func() {}, nil
}

func (c *matchingPersonClient) nextSendClientSeq() uint64 {
	return c.clientSeq.Add(1)
}

func (c *matchingPersonClient) ReadFrame(ctx context.Context) (frame.Frame, error) {
	return c.readFrameMatching(ctx, func(frame.Frame) bool { return true })
}

func (c *matchingPersonClient) RecvAck(ctx context.Context, messageID int64, messageSeq uint64) error {
	c.mu.Lock()
	autoAck := c.autoRecvAck
	disableAck := c.autoRecvAckDisableAck
	c.mu.Unlock()
	if autoAck && !disableAck {
		// The matching reader sends the ack when it returns or buffers a recv frame.
		return nil
	}
	return c.client.RecvAck(ctx, messageID, messageSeq)
}

func (c *matchingPersonClient) Close() error {
	return c.client.Close()
}

func (c *matchingPersonClient) readFrameMatching(ctx context.Context, match func(frame.Frame) bool) (frame.Frame, error) {
	c.mu.Lock()
	c.foregroundMatchers++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.foregroundMatchers--
		c.signalLocked()
		c.mu.Unlock()
	}()
	for {
		c.mu.Lock()
		for idx, f := range c.buffer {
			if !match(f) {
				continue
			}
			c.buffer = append(c.buffer[:idx], c.buffer[idx+1:]...)
			c.mu.Unlock()
			return f, nil
		}
		if c.autoRecvAck && (c.autoRecvAckReader || c.autoRecvAckReadErr != nil) {
			if c.autoRecvAckReadErr != nil {
				err := c.autoRecvAckReadErr
				c.mu.Unlock()
				return nil, err
			}
			notify := c.notifyLocked()
			c.foregroundWaiters++
			c.mu.Unlock()
			var waitErr error
			select {
			case <-notify:
			case <-ctx.Done():
				waitErr = ctx.Err()
			}
			c.mu.Lock()
			c.foregroundWaiters--
			if waitErr != nil {
				waitErr = c.debugReadErrorLocked(waitErr)
			}
			c.mu.Unlock()
			if waitErr != nil {
				return nil, waitErr
			}
			continue
		}
		if c.reading {
			notify := c.notifyLocked()
			c.foregroundWaiters++
			c.mu.Unlock()
			var waitErr error
			select {
			case <-notify:
			case <-ctx.Done():
				waitErr = ctx.Err()
			}
			c.mu.Lock()
			c.foregroundWaiters--
			if waitErr != nil {
				waitErr = c.debugReadErrorLocked(waitErr)
			}
			c.mu.Unlock()
			if waitErr != nil {
				return nil, waitErr
			}
			continue
		}
		c.reading = true
		c.mu.Unlock()

		f, err := c.client.ReadFrame(ctx)
		c.mu.Lock()
		c.reading = false
		if err != nil {
			err = c.debugReadErrorLocked(err)
			c.signalLocked()
			c.mu.Unlock()
			return nil, err
		}
		c.observeReadFrameLocked(f)
		autoAck := c.autoRecvAck
		ackRecv := autoAck && !c.autoRecvAckDisableAck
		if match(f) {
			c.signalLocked()
			c.mu.Unlock()
			c.autoAckRecvFrame(ctx, f, ackRecv)
			return f, nil
		}
		if _, ok := f.(*frame.PongPacket); ok {
			c.signalLocked()
			c.mu.Unlock()
			continue
		}
		dropUnverifiedRecv := false
		if _, ok := f.(*frame.RecvPacket); ok && autoAck && !c.autoRecvAckBufferRecv {
			dropUnverifiedRecv = true
		}
		if !dropUnverifiedRecv {
			c.bufferFrameLocked(f)
		}
		c.signalLocked()
		c.mu.Unlock()
		c.autoAckRecvFrame(ctx, f, ackRecv)
	}
}

func (c *matchingPersonClient) startAutoRecvAck(ctx context.Context) {
	c.startAutoRecvAckWithOptions(ctx, AutoRecvAckOptions{BufferRecvFrames: true})
}

func (c *matchingPersonClient) startAutoRecvAckWithOptions(ctx context.Context, opts AutoRecvAckOptions) {
	c.mu.Lock()
	if c.autoRecvAck {
		c.mu.Unlock()
		return
	}
	c.autoRecvAck = true
	c.autoRecvAckBufferRecv = opts.BufferRecvFrames
	c.autoRecvAckDisableAck = opts.DisableRecvAck
	c.autoRecvAckReader = true
	c.autoRecvAckReadErr = nil
	c.mu.Unlock()
	go c.autoRecvAckLoop(ctx)
}

func (c *matchingPersonClient) autoRecvAckLoop(ctx context.Context) {
	for {
		if err := c.prefetchFrame(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			if isAutoRecvAckIdleTimeout(err) {
				continue
			}
			c.mu.Lock()
			c.autoRecvAckReadErr = err
			c.signalLocked()
			c.mu.Unlock()
			return
		}
	}
}

func (c *matchingPersonClient) prefetchFrame(ctx context.Context) error {
	c.mu.Lock()
	if c.shouldYieldToForegroundLocked() {
		c.autoRecvAckReader = false
		c.signalLocked()
		c.mu.Unlock()
		return c.finishAutoRecvAckYield(ctx)
	}
	if c.reading {
		notify := c.notifyLocked()
		c.mu.Unlock()
		select {
		case <-notify:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.reading = true
	c.mu.Unlock()

	f, err := c.client.ReadFrame(ctx)
	c.mu.Lock()
	c.reading = false
	if err != nil {
		c.signalLocked()
		c.mu.Unlock()
		return err
	}
	c.observeReadFrameLocked(f)
	if _, ok := f.(*frame.PongPacket); ok {
		yield := c.shouldYieldToForegroundLocked()
		if yield {
			c.autoRecvAckReader = false
		}
		c.signalLocked()
		c.mu.Unlock()
		if yield {
			return c.finishAutoRecvAckYield(ctx)
		}
		return nil
	}
	ackRecv := c.autoRecvAck && !c.autoRecvAckDisableAck
	if _, ok := f.(*frame.RecvPacket); ok && !c.autoRecvAckBufferRecv {
		yield := c.shouldYieldToForegroundLocked()
		if yield {
			c.autoRecvAckReader = false
		}
		c.signalLocked()
		c.mu.Unlock()
		c.autoAckRecvFrame(ctx, f, ackRecv)
		if yield {
			return c.finishAutoRecvAckYield(ctx)
		}
		return nil
	}
	c.bufferFrameLocked(f)
	yield := c.shouldYieldToForegroundLocked()
	if yield {
		c.autoRecvAckReader = false
	}
	c.signalLocked()
	c.mu.Unlock()
	c.autoAckRecvFrame(ctx, f, ackRecv)
	if yield {
		return c.finishAutoRecvAckYield(ctx)
	}
	return nil
}

func (c *matchingPersonClient) shouldYieldToForegroundLocked() bool {
	return c.foregroundMatchers > 0
}

func (c *matchingPersonClient) finishAutoRecvAckYield(ctx context.Context) error {
	if err := waitAutoRecvAckYield(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	for c.autoRecvAck && c.autoRecvAckReadErr == nil && c.foregroundMatchers > 0 {
		notify := c.notifyLocked()
		c.mu.Unlock()
		select {
		case <-notify:
		case <-ctx.Done():
			return ctx.Err()
		}
		c.mu.Lock()
	}
	if c.autoRecvAck && c.autoRecvAckReadErr == nil {
		c.autoRecvAckReader = true
		c.signalLocked()
	}
	c.mu.Unlock()
	return nil
}

func waitAutoRecvAckYield(ctx context.Context) error {
	if autoRecvAckYieldDelay <= 0 {
		return nil
	}
	timer := time.NewTimer(autoRecvAckYieldDelay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *matchingPersonClient) autoAckRecvFrame(ctx context.Context, f frame.Frame, enabled bool) {
	if !enabled {
		return
	}
	recv, ok := f.(*frame.RecvPacket)
	if !ok {
		return
	}
	_ = c.client.RecvAck(ctx, recv.MessageID, recv.MessageSeq)
}

func (c *matchingPersonClient) bufferFrameLocked(f frame.Frame) {
	limit := c.bufferLimit
	if limit <= 0 {
		limit = defaultMatchingBufferLimit
	}
	for len(c.buffer) >= limit {
		c.dropBufferedFrameLocked()
	}
	c.buffer = append(c.buffer, f)
}

func (c *matchingPersonClient) dropBufferedFrameLocked() {
	if len(c.buffer) == 0 {
		return
	}
	for idx, f := range c.buffer {
		if _, ok := f.(*frame.RecvPacket); ok {
			c.debugDroppedRecvs++
			c.buffer = append(c.buffer[:idx], c.buffer[idx+1:]...)
			return
		}
	}
	if _, ok := c.buffer[0].(*frame.SendackPacket); ok {
		c.debugDroppedSendacks++
	}
	c.buffer = c.buffer[1:]
}

func (c *matchingPersonClient) observeReadFrameLocked(f frame.Frame) {
	c.debugReadFrames++
	switch f.(type) {
	case *frame.SendackPacket:
		c.debugReadSendacks++
	case *frame.RecvPacket:
		c.debugReadRecvs++
	}
	summary := debugFrameSummary(f)
	if summary == "" {
		return
	}
	const limit = 8
	if len(c.debugLastFrames) >= limit {
		copy(c.debugLastFrames, c.debugLastFrames[1:])
		c.debugLastFrames[len(c.debugLastFrames)-1] = summary
		return
	}
	c.debugLastFrames = append(c.debugLastFrames, summary)
}

func (c *matchingPersonClient) debugReadErrorLocked(err error) error {
	if err == nil {
		return nil
	}
	sendacksBuffered := 0
	recvsBuffered := 0
	for _, f := range c.buffer {
		switch f.(type) {
		case *frame.SendackPacket:
			sendacksBuffered++
		case *frame.RecvPacket:
			recvsBuffered++
		}
	}
	return fmt.Errorf(
		"%w; matching_client frames=%d sendacks=%d recvs=%d buffered=%d buffered_sendacks=%d buffered_recvs=%d dropped_sendacks=%d dropped_recvs=%d auto_reader=%t foreground=%d waiters=%d last=%s",
		err,
		c.debugReadFrames,
		c.debugReadSendacks,
		c.debugReadRecvs,
		len(c.buffer),
		sendacksBuffered,
		recvsBuffered,
		c.debugDroppedSendacks,
		c.debugDroppedRecvs,
		c.autoRecvAckReader,
		c.foregroundMatchers,
		c.foregroundWaiters,
		strings.Join(c.debugLastFrames, ","),
	)
}

func debugFrameSummary(f frame.Frame) string {
	switch pkt := f.(type) {
	case *frame.SendackPacket:
		return fmt.Sprintf("sendack(seq=%d,msg=%s,reason=%s)", pkt.ClientSeq, pkt.ClientMsgNo, pkt.ReasonCode)
	case *frame.RecvPacket:
		return fmt.Sprintf("recv(seq=%d,msg=%s,ch=%s)", pkt.MessageSeq, pkt.ClientMsgNo, pkt.ChannelID)
	case *frame.PongPacket:
		return "pong"
	default:
		if f == nil {
			return "nil"
		}
		return fmt.Sprintf("%T", f)
	}
}

func isAutoRecvAckIdleTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func (c *matchingPersonClient) notifyLocked() <-chan struct{} {
	if c.notify == nil {
		c.notify = make(chan struct{})
	}
	return c.notify
}

func (c *matchingPersonClient) signalLocked() {
	if c.notify == nil {
		return
	}
	close(c.notify)
	c.notify = make(chan struct{})
}

func readFrameMatching(ctx context.Context, client PersonClient, match func(frame.Frame) bool) (frame.Frame, error) {
	if reader, ok := client.(matchingFrameReader); ok {
		return reader.readFrameMatching(ctx, match)
	}
	for {
		f, err := client.ReadFrame(ctx)
		if err != nil {
			return nil, err
		}
		if match(f) {
			return f, nil
		}
	}
}

func lockSendackOperation(ctx context.Context, client PersonClient) (func(), error) {
	if locker, ok := client.(sendackOperationLocker); ok && locker != nil {
		return locker.lockSendackOperation(ctx)
	}
	return func() {}, nil
}

func nextSendClientSeq(client PersonClient, fallback uint64) uint64 {
	if allocator, ok := client.(sendClientSeqAllocator); ok && allocator != nil {
		return allocator.nextSendClientSeq()
	}
	return fallback
}

func matchesExpectedSendack(ack *frame.SendackPacket, clientSeq uint64, clientMsgNo string) bool {
	if ack == nil || ack.ClientSeq != clientSeq {
		return false
	}
	if clientMsgNo == "" {
		return true
	}
	return ack.ClientMsgNo == clientMsgNo
}

func (w *PersonWorkload) deviceID(uid string) string {
	return w.cfg.DevicePrefix + "-" + uid
}

func (w *PersonWorkload) recordError(name string, err error) {
	if w == nil || err == nil {
		return
	}
	w.metrics.RecordErrorSample(name, err)
}

func (w *PersonWorkload) withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	timeout = boundedWarmupOperationTimeout(timeout, w.warmupOperationDeadline, time.Now())
	return context.WithTimeout(ctx, timeout)
}

func (w *PersonWorkload) useWarmupTimeouts() func() {
	ackTimeout := w.cfg.AckTimeout
	recvTimeout := w.cfg.RecvTimeout
	deadline := w.warmupOperationDeadline
	w.warmupOperationDeadline = time.Now().Add(w.cfg.WarmupDuration + warmupOperationTailTimeout(ackTimeout, recvTimeout))
	w.cfg.AckTimeout = warmupOperationTimeout(w.cfg.AckTimeout, w.cfg.WarmupDuration)
	w.cfg.RecvTimeout = warmupOperationTimeout(w.cfg.RecvTimeout, w.cfg.WarmupDuration)
	return func() {
		w.cfg.AckTimeout = ackTimeout
		w.cfg.RecvTimeout = recvTimeout
		w.warmupOperationDeadline = deadline
	}
}

func warmupOperationTailTimeout(ackTimeout, recvTimeout time.Duration) time.Duration {
	maximum := defaultWorkloadTimeout
	for _, timeout := range []time.Duration{ackTimeout, recvTimeout} {
		if timeout <= 0 {
			timeout = defaultWorkloadTimeout
		}
		if timeout > maximum {
			maximum = timeout
		}
	}
	return maximum
}

func boundedWarmupOperationTimeout(timeout time.Duration, deadline, now time.Time) time.Duration {
	if timeout <= 0 {
		timeout = defaultWorkloadTimeout
	}
	if deadline.IsZero() {
		return timeout
	}
	remaining := deadline.Sub(now)
	if remaining <= 0 {
		return time.Nanosecond
	}
	if remaining < timeout {
		return remaining
	}
	return timeout
}

func warmupOperationTimeout(timeout, duration time.Duration) time.Duration {
	if duration <= 0 {
		return timeout
	}
	base := timeout
	if base <= 0 {
		base = defaultWorkloadTimeout
	}
	if base < duration {
		return duration
	}
	return base
}

func scheduledMessageCount(duration time.Duration, perSecond float64, channelCount int) int {
	if duration <= 0 || perSecond <= 0 || channelCount <= 0 {
		return 0
	}
	count := int(duration.Seconds() * perSecond * float64(channelCount))
	if count < 1 {
		return 1
	}
	return count
}

func scheduledMessageInterval(duration time.Duration, totalMessages int) time.Duration {
	if duration <= 0 || totalMessages <= 0 {
		return 0
	}
	return time.Duration(int64(duration) / int64(totalMessages))
}

func warmupRateForDuration(rate model.Rate, duration time.Duration) model.Rate {
	if rate.PerSecond <= 0 {
		return rate
	}
	reduced := rate.PerSecond * 0.1
	if duration > 0 {
		// Warmup should activate each assigned channel at least once before the
		// measured run, even for low per-channel traffic rates.
		minPerChannel := 1 / duration.Seconds()
		if reduced < minPerChannel {
			reduced = minPerChannel
		}
	}
	return model.Rate{PerSecond: reduced}
}

func normalizePersonPairs(cfg PersonConfig) ([]PersonPair, error) {
	if len(cfg.Pairs) == 0 {
		if cfg.SenderUID == "" || cfg.RecipientUID == "" {
			return nil, nil
		}
		cfg.Pairs = []PersonPair{{ChannelIndex: 0, SenderUID: cfg.SenderUID, RecipientUID: cfg.RecipientUID}}
	}
	pairs := make([]PersonPair, 0, len(cfg.Pairs))
	for idx, pair := range cfg.Pairs {
		pair.SenderUID = strings.TrimSpace(pair.SenderUID)
		pair.RecipientUID = strings.TrimSpace(pair.RecipientUID)
		if pair.SenderUID == "" || pair.RecipientUID == "" {
			return nil, fmt.Errorf("person workload: pair %d requires sender_uid and recipient_uid", idx)
		}
		if pair.ChannelIndex < 0 {
			return nil, fmt.Errorf("person workload: pair %d channel_index must not be negative", idx)
		}
		pairs = append(pairs, pair)
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		if pairs[i].ChannelIndex != pairs[j].ChannelIndex {
			return pairs[i].ChannelIndex < pairs[j].ChannelIndex
		}
		if pairs[i].SenderUID != pairs[j].SenderUID {
			return pairs[i].SenderUID < pairs[j].SenderUID
		}
		return pairs[i].RecipientUID < pairs[j].RecipientUID
	})
	return pairs, nil
}
