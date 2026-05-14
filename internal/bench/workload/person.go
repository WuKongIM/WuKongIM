package workload

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	defaultPersonDevicePrefix = "bench-device"
	defaultPersonClientPrefix = "bench-msg"
	verifyRecvModeFull        = "full"
)

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
}

// PersonWorkload executes deterministic person-channel benchmark traffic.
type PersonWorkload struct {
	cfg                      PersonConfig
	metrics                  *metrics.Registry
	pairs                    []PersonPair
	clients                  map[string]PersonClient
	useMessageIndexAsChannel bool

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

// Warmup is reserved for future warmup traffic and currently does not emit load in v1.
func (w *PersonWorkload) Warmup(ctx context.Context) error {
	return nil
}

// Run sends one deterministic person message for every assigned pair.
func (w *PersonWorkload) Run(ctx context.Context) error {
	for _, pair := range w.pairs {
		if err := w.SendOne(ctx, pair.ChannelIndex); err != nil {
			return err
		}
	}
	return nil
}

// Cooldown is reserved for future draining behavior and currently performs no additional work.
func (w *PersonWorkload) Cooldown(ctx context.Context) error {
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
	sender := w.clients[pair.SenderUID]
	recipient := w.clients[pair.RecipientUID]
	channelIndex := pair.ChannelIndex
	if w.useMessageIndexAsChannel {
		channelIndex = messageIndex
	}
	payload := w.buildPayload(channelIndex, messageIndex)
	clientSeq := uint64(messageIndex)
	clientMsgNo := w.clientMsgNo(channelIndex, messageIndex)
	pkt := &frame.SendPacket{
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		ChannelID:   pair.RecipientUID,
		ChannelType: frame.ChannelTypePerson,
		Payload:     payload,
	}

	sendStart := time.Now()
	if err := sender.Send(ctx, pkt); err != nil {
		w.recordError("person_send_error", err)
		w.metrics.IncCounter("person_send_error_total", nil)
		return err
	}
	ack, err := w.waitForSendack(ctx, sender, clientSeq, clientMsgNo)
	if err != nil {
		w.recordError("person_send_error", err)
		w.metrics.IncCounter("person_send_error_total", nil)
		return err
	}
	if ack.ReasonCode != frame.ReasonSuccess {
		err := fmt.Errorf("person workload: sendack rejected message %q with reason %s", clientMsgNo, ack.ReasonCode)
		w.recordError("person_send_error", err)
		w.metrics.IncCounter("person_send_error_total", nil)
		return err
	}
	w.metrics.IncCounter("person_send_success_total", nil)
	w.metrics.ObserveLatency("person_send_latency_seconds", nil, time.Since(sendStart))

	if !strings.EqualFold(w.cfg.VerifyRecvMode, verifyRecvModeFull) {
		return nil
	}
	recvStart := time.Now()
	expectedMessageID := uint64(0)
	if ack.MessageID > 0 {
		expectedMessageID = uint64(ack.MessageID)
	}
	recv, err := w.waitForRecv(ctx, recipient, clientMsgNo, pair.SenderUID, expectedMessageID, ack.MessageSeq, w.payloadMarker(channelIndex, messageIndex))
	if err != nil {
		w.recordError("person_recv_error", err)
		w.metrics.IncCounter("person_recv_error_total", nil)
		return err
	}
	if string(recv.Payload) != string(payload) {
		err := fmt.Errorf("person workload: recv payload mismatch for %q", clientMsgNo)
		w.recordError("person_recv_error", err)
		w.metrics.IncCounter("person_recv_error_total", nil)
		return err
	}
	if w.cfg.RecvAck {
		if err := recipient.RecvAck(ctx, recv.MessageID, recv.MessageSeq); err != nil {
			w.recordError("person_recv_error", err)
			w.metrics.IncCounter("person_recv_error_total", nil)
			return err
		}
	}
	w.metrics.IncCounter("person_recv_success_total", nil)
	w.metrics.ObserveLatency("person_recv_latency_seconds", nil, time.Since(recvStart))
	return nil
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
	for {
		f, err := client.ReadFrame(deadlineCtx)
		if err != nil {
			if err == io.EOF {
				return nil, fmt.Errorf("person workload: sendack not received for %q: %w", clientMsgNo, err)
			}
			return nil, err
		}
		ack, ok := f.(*frame.SendackPacket)
		if !ok {
			continue
		}
		if ack.ClientSeq != clientSeq || ack.ClientMsgNo != clientMsgNo {
			continue
		}
		return ack, nil
	}
}

func (w *PersonWorkload) waitForRecv(ctx context.Context, client PersonClient, clientMsgNo, senderUID string, expectedMessageID uint64, expectedMessageSeq uint64, payloadMarker string) (*frame.RecvPacket, error) {
	deadlineCtx, cancel := w.withTimeout(ctx, w.cfg.RecvTimeout)
	defer cancel()
	for {
		f, err := client.ReadFrame(deadlineCtx)
		if err != nil {
			if err == io.EOF {
				return nil, fmt.Errorf("person workload: recv not received for %q: %w", clientMsgNo, err)
			}
			return nil, err
		}
		recv, ok := f.(*frame.RecvPacket)
		if !ok {
			continue
		}
		if recv.FromUID != senderUID || recv.ChannelID != senderUID || recv.ChannelType != frame.ChannelTypePerson || recv.ClientMsgNo != clientMsgNo {
			continue
		}
		if expectedMessageID > 0 && recv.MessageID != int64(expectedMessageID) {
			continue
		}
		if expectedMessageSeq > 0 && recv.MessageSeq != expectedMessageSeq {
			continue
		}
		if payloadMarker != "" && !strings.Contains(string(recv.Payload), payloadMarker) {
			continue
		}
		return recv, nil
	}
}

func (w *PersonWorkload) buildPayload(channelIndex, messageIndex int) []byte {
	base := w.payloadMarker(channelIndex, messageIndex)
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

func (w *PersonWorkload) payloadMarker(channelIndex, messageIndex int) string {
	return fmt.Sprintf("run=%s profile=%s traffic=%s channel=%d message=%d", w.cfg.RunID, w.cfg.ProfileName, w.cfg.TrafficName, channelIndex, messageIndex)
}

func (w *PersonWorkload) clientMsgNo(channelIndex, messageIndex int) string {
	return fmt.Sprintf("%s-%s-%s-%s-ch%d-msg%d", w.cfg.ClientMsgPrefix, w.cfg.RunID, w.cfg.ProfileName, w.cfg.TrafficName, channelIndex, messageIndex)
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
	if timeout <= 0 {
		return context.WithTimeout(ctx, 5*time.Second)
	}
	return context.WithTimeout(ctx, timeout)
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
