package chatlifecycle

import (
	"container/heap"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const maxVerifierCapacity = 10_000_000

var (
	errVerifierConfig = errors.New("chat lifecycle verifier: capacities and deadline are invalid")
)

// VerifierConfig fixes every retained state bound. CorrelationCapacity should
// be sized from the offered rate multiplied by CorrelationDeadline.
type VerifierConfig struct {
	PendingCapacity     int
	SequenceCapacity    int
	CorrelationCapacity int
	CorrelationDeadline time.Duration
}

// TerminalSendCode is the fixed terminal outcome vocabulary used after the
// retry engine decides that no further attempt may run.
type TerminalSendCode uint8

const (
	TerminalSendRetryExhausted TerminalSendCode = iota + 1
	TerminalSendNonRetriable
	TerminalSendSessionClosed
)

// VerificationError is a redacted stable verifier failure. It never wraps a
// transport error because arbitrary error text is forbidden in evidence.
type VerificationError struct {
	classification SyncClassification
	code           FailureCode
}

func (e *VerificationError) Error() string {
	if e == nil {
		return "chat lifecycle verification failed"
	}
	return "chat lifecycle verification failed: " + failureCodeName(e.code)
}

// Classification reports product_failure or harness_invalid ownership.
func (e *VerificationError) Classification() SyncClassification {
	if e == nil {
		return ""
	}
	return e.classification
}

// Code returns the closed low-cardinality failure code.
func (e *VerificationError) Code() FailureCode {
	if e == nil {
		return 0
	}
	return e.code
}

// SendackRejectedError reports a retryable or terminal decision input without
// completing the logical send. The retry engine owns the eventual decision.
type SendackRejectedError struct {
	reason frame.ReasonCode
}

func (e *SendackRejectedError) Error() string {
	return "chat lifecycle SENDACK rejected"
}

// ReasonCode exposes only the protocol's fixed numeric reason vocabulary.
func (e *SendackRejectedError) ReasonCode() frame.ReasonCode {
	if e == nil {
		return frame.ReasonUnknown
	}
	return e.reason
}

// VerifierSnapshot contains only aggregate counts and bounded-state gauges.
type VerifierSnapshot struct {
	Classification         SyncClassification
	Sent                   uint64
	Attempts               uint64
	RetryAttempts          uint64
	Acknowledged           uint64
	SendackRejections      uint64
	Terminal               uint64
	DuplicateCompletions   uint64
	ConflictingCompletions uint64
	UnknownSendacks        uint64
	Received               uint64
	ReceiveFailures        uint64
	ReceiveAcknowledged    uint64
	ReceiveAckFailures     uint64
	DuplicateDeliveries    uint64
	SequenceRegressions    uint64
	Sampled                uint64
	SampledDelivered       uint64
	SampledExpired         uint64
	PendingCurrent         int
	PendingUnfinished      int
	PendingPeak            int
	SequenceCurrent        int
	SequencePeak           int
	CorrelationCurrent     int
	CorrelationPeak        int
	DeadlineCurrent        int
}

// VerificationDrain reports unfinished bounded state without enumerating any
// logical identity. P3.5 can poll it while performing process-level drain.
type VerificationDrain struct {
	PendingUnfinished       int
	CorrelationOutstanding  int
	NextCorrelationDeadline time.Time
}

type sendCompletion uint8

const (
	sendIncomplete sendCompletion = iota
	sendAcknowledged
	sendTerminal
)

type pendingSend struct {
	logical    LogicalSend
	completion sendCompletion
	messageID  int64
	messageSeq uint64
	terminal   TerminalSendCode
}

type recipientSequenceState struct {
	channels map[sequenceChannel]uint64
}

type sequenceChannel struct {
	id          string
	channelType uint8
}

type sampledCorrelation struct {
	logical    LogicalSend
	deadline   time.Time
	heapIndex  int
	ackSeen    bool
	recvSeen   bool
	messageID  int64
	messageSeq uint64
}

type correlationDeadlineHeap []*sampledCorrelation

func (h correlationDeadlineHeap) Len() int { return len(h) }
func (h correlationDeadlineHeap) Less(i, j int) bool {
	if h[i].deadline.Equal(h[j].deadline) {
		if h[i].logical.WorkerID == h[j].logical.WorkerID {
			return h[i].logical.LogicalSend < h[j].logical.LogicalSend
		}
		return h[i].logical.WorkerID < h[j].logical.WorkerID
	}
	return h[i].deadline.Before(h[j].deadline)
}
func (h correlationDeadlineHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}
func (h *correlationDeadlineHeap) Push(value any) {
	item := value.(*sampledCorrelation)
	item.heapIndex = len(*h)
	*h = append(*h, item)
}
func (h *correlationDeadlineHeap) Pop() any {
	old := *h
	last := len(old) - 1
	item := old[last]
	old[last] = nil
	item.heapIndex = -1
	*h = old[:last]
	return item
}

// Verifier owns all bounded SEND, receive-order, and exact-correlation state.
// Its methods are safe for concurrent asynchronous worker paths.
type Verifier struct {
	mu       sync.Mutex
	model    TrafficModel
	config   VerifierConfig
	evidence *EvidenceRecorder

	pending           map[string]*pendingSend
	pendingUnfinished int
	pendingPeak       int
	sequences         map[string]*recipientSequenceState
	sequenceCount     int
	sequencePeak      int
	correlations      map[string]*sampledCorrelation
	deadlines         correlationDeadlineHeap
	correlationPeak   int

	counters VerifierSnapshot
}

// NewVerifier constructs empty state without preallocating from untrusted bounds.
func NewVerifier(model TrafficModel, config VerifierConfig, evidence *EvidenceRecorder) (*Verifier, error) {
	if model.identity == nil || evidence == nil || !validVerifierCapacity(config.PendingCapacity) ||
		!validVerifierCapacity(config.SequenceCapacity) || !validVerifierCapacity(config.CorrelationCapacity) ||
		config.CorrelationDeadline <= 0 {
		return nil, errVerifierConfig
	}
	v := &Verifier{
		model:        model,
		config:       config,
		evidence:     evidence,
		pending:      make(map[string]*pendingSend),
		sequences:    make(map[string]*recipientSequenceState),
		correlations: make(map[string]*sampledCorrelation),
	}
	heap.Init(&v.deadlines)
	return v, nil
}

// RegisterSend installs one attempt-independent logical identity and, when its
// exact-cycle position is selected, one physically removable deadline entry.
func (v *Verifier) RegisterSend(logical LogicalSend, registeredAt time.Time) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.model.validateLogicalSend(logical); err != nil {
		return v.recordHarnessLocked(FailureCodeGeneratorInvariant, logical, EvidenceStageSend, 0)
	}
	if _, exists := v.pending[logical.ClientMsgNo]; exists {
		return v.recordHarnessLocked(FailureCodeGeneratorInvariant, logical, EvidenceStageSend, 0)
	}
	if len(v.pending) >= v.config.PendingCapacity {
		return v.recordHarnessLocked(FailureCodePendingCapacity, logical, EvidenceStageCapacity, uint64(v.config.PendingCapacity))
	}
	sampled, sampleErr := v.sampledLocked(logical)
	if sampleErr != nil {
		return v.recordHarnessLocked(FailureCodeGeneratorInvariant, logical, EvidenceStageCorrelation, 0)
	}
	if sampled && len(v.correlations) >= v.config.CorrelationCapacity {
		return v.recordHarnessLocked(FailureCodeCorrelationCapacity, logical, EvidenceStageCapacity, uint64(v.config.CorrelationCapacity))
	}
	v.pending[logical.ClientMsgNo] = &pendingSend{logical: logical}
	v.pendingUnfinished++
	v.counters.Sent++
	if len(v.pending) > v.pendingPeak {
		v.pendingPeak = len(v.pending)
	}
	if sampled {
		correlation := &sampledCorrelation{
			logical:   logical,
			deadline:  registeredAt.Add(v.config.CorrelationDeadline),
			heapIndex: -1,
		}
		v.correlations[logical.ClientMsgNo] = correlation
		heap.Push(&v.deadlines, correlation)
		v.counters.Sampled++
		if len(v.correlations) > v.correlationPeak {
			v.correlationPeak = len(v.correlations)
		}
	}
	return nil
}

// ObserveAttempt verifies that a retry did not mint a replacement identity.
func (v *Verifier) ObserveAttempt(logical LogicalSend, attempt RetryAttempt) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	pending := v.pending[logical.ClientMsgNo]
	if pending == nil || pending.logical != logical || pending.completion != sendIncomplete ||
		attempt.ClientMsgNo != logical.ClientMsgNo || attempt.Attempt > 3 {
		return v.recordHarnessLocked(FailureCodeGeneratorInvariant, logical, EvidenceStageSend, uint64(attempt.Attempt))
	}
	v.counters.Attempts++
	if attempt.Attempt > 0 {
		v.counters.RetryAttempts++
	}
	return nil
}

// HandleSendack completes a pending logical send only for an exact successful
// SENDACK carrying positive server identity fields.
func (v *Verifier) HandleSendack(ack *frame.SendackPacket) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if ack == nil || ack.ClientMsgNo == "" {
		return v.recordSendFailureLocked(FailureCodeInvalidSendack, 0, [16]byte{}, EvidenceStageSendack, 0)
	}
	pending := v.pending[ack.ClientMsgNo]
	if pending == nil {
		v.counters.UnknownSendacks++
		return v.recordSendFailureLocked(FailureCodeUnknownSendack, 0, messageFingerprint(ack.ClientMsgNo), EvidenceStageSendack, ack.MessageSeq)
	}
	if pending.completion != sendIncomplete {
		if pending.completion == sendAcknowledged && pending.messageID == ack.MessageID && pending.messageSeq == ack.MessageSeq {
			v.counters.DuplicateCompletions++
			return v.recordSendFailureLocked(FailureCodeDuplicateCompletion, pending.logical.LogicalSend, messageFingerprint(ack.ClientMsgNo), EvidenceStageSendack, ack.MessageSeq)
		}
		if pending.completion == sendTerminal && ack.MessageID == 0 && ack.MessageSeq == 0 {
			v.counters.DuplicateCompletions++
			return v.recordSendFailureLocked(FailureCodeDuplicateCompletion, pending.logical.LogicalSend, messageFingerprint(ack.ClientMsgNo), EvidenceStageSendack, 0)
		}
		v.counters.ConflictingCompletions++
		return v.recordSendFailureLocked(FailureCodeConflictingCompletion, pending.logical.LogicalSend, messageFingerprint(ack.ClientMsgNo), EvidenceStageSendack, ack.MessageSeq)
	}
	if ack.ReasonCode != frame.ReasonSuccess {
		if ack.MessageID != 0 || ack.MessageSeq != 0 {
			return v.recordSendFailureLocked(FailureCodeInvalidSendack, pending.logical.LogicalSend, messageFingerprint(ack.ClientMsgNo), EvidenceStageSendack, ack.MessageSeq)
		}
		v.counters.SendackRejections++
		return &SendackRejectedError{reason: ack.ReasonCode}
	}
	if ack.MessageID <= 0 || ack.MessageSeq == 0 {
		return v.recordSendFailureLocked(FailureCodeInvalidSendack, pending.logical.LogicalSend, messageFingerprint(ack.ClientMsgNo), EvidenceStageSendack, ack.MessageSeq)
	}
	pending.completion = sendAcknowledged
	pending.messageID = ack.MessageID
	pending.messageSeq = ack.MessageSeq
	v.pendingUnfinished--
	v.counters.Acknowledged++
	if correlation := v.correlations[ack.ClientMsgNo]; correlation != nil {
		correlation.ackSeen = true
		if correlation.recvSeen && (correlation.messageID != ack.MessageID || correlation.messageSeq != ack.MessageSeq) {
			return v.recordCorrelationConflictLocked(correlation, ack.MessageSeq)
		}
		correlation.messageID = ack.MessageID
		correlation.messageSeq = ack.MessageSeq
		if correlation.recvSeen {
			v.completeCorrelationLocked(correlation)
		}
	}
	return nil
}

// HandleRecv validates a real RECV against its Phase 2 marker, updates bounded
// sequence/correlation state, and acknowledges every packet with trustworthy
// positive server identity fields. Validation and RECVACK failures are both
// retained; neither error includes packet or transport text.
func (v *Verifier) HandleRecv(ctx context.Context, recipient string, recv *frame.RecvPacket, acker RecvAcker) error {
	v.mu.Lock()
	if recv == nil || recv.MessageID <= 0 || recv.MessageSeq == 0 {
		v.counters.ReceiveFailures++
		validationErr := v.recordReceiveFailureLocked(FailureCodeReceiveProtocol, 0, [16]byte{}, EvidenceStageReceive, 0)
		v.mu.Unlock()
		return validationErr
	}
	v.counters.Received++
	validationErr := v.validateRecvLocked(recipient, recv)
	if validationErr != nil {
		v.counters.ReceiveFailures++
	}
	v.mu.Unlock()

	ack := &frame.RecvackPacket{MessageID: recv.MessageID, MessageSeq: recv.MessageSeq}
	if acker == nil {
		v.mu.Lock()
		v.counters.ReceiveAckFailures++
		ackErr := v.recordFailureLocked(EvidenceEvent{
			Class:       FailureClassHarness,
			Stage:       EvidenceStageRecvack,
			Code:        FailureCodeGeneratorInvariant,
			Fingerprint: messageFingerprint(recv.ClientMsgNo),
			Value:       recv.MessageSeq,
		})
		v.mu.Unlock()
		return errors.Join(validationErr, ackErr)
	}
	if err := acker.AckRecv(ctx, ack); err != nil {
		v.mu.Lock()
		v.counters.ReceiveAckFailures++
		ackErr := v.recordReceiveFailureLocked(FailureCodeRecvack, 0, messageFingerprint(recv.ClientMsgNo), EvidenceStageRecvack, recv.MessageSeq)
		v.mu.Unlock()
		return errors.Join(validationErr, ackErr)
	}
	v.mu.Lock()
	v.counters.ReceiveAcknowledged++
	v.mu.Unlock()
	return validationErr
}

func (v *Verifier) validateRecvLocked(recipient string, recv *frame.RecvPacket) error {
	marker, err := DecodePayloadMarker(recv.Payload)
	if err != nil {
		return v.recordReceiveFailureLocked(FailureCodeReceivePayload, 0, messageFingerprint(recv.ClientMsgNo), EvidenceStageReceive, uint64(len(recv.Payload)))
	}

	var target string
	switch marker.Kind {
	case TrafficPerson:
		if recipient == "" || recv.FromUID == "" || recipient == recv.FromUID || recv.ChannelType != frame.ChannelTypePerson || recv.ChannelID != recv.FromUID {
			return v.recordReceiveFailureLocked(FailureCodeReceiveIdentity, marker.LogicalSend, messageFingerprint(recv.ClientMsgNo), EvidenceStageReceive, recv.MessageSeq)
		}
		target = recipient
	case TrafficGroup:
		if recipient == "" || recv.FromUID == "" || recv.ChannelType != frame.ChannelTypeGroup || recv.ChannelID == "" {
			return v.recordReceiveFailureLocked(FailureCodeReceiveIdentity, marker.LogicalSend, messageFingerprint(recv.ClientMsgNo), EvidenceStageReceive, recv.MessageSeq)
		}
		target = recv.ChannelID
	default:
		return v.recordReceiveFailureLocked(FailureCodeReceivePayload, marker.LogicalSend, messageFingerprint(recv.ClientMsgNo), EvidenceStageReceive, recv.MessageSeq)
	}

	logical, err := v.model.NewLogicalSend(uint64(marker.WorkerID), marker.LogicalSend, marker.Kind, recv.FromUID, target)
	if err != nil || logical.ClientMsgNo != recv.ClientMsgNo {
		return v.recordReceiveFailureLocked(FailureCodeReceiveIdentity, marker.LogicalSend, messageFingerprint(recv.ClientMsgNo), EvidenceStageReceive, recv.MessageSeq)
	}
	if err := v.model.VerifyPayload(recv.Payload, logical); err != nil {
		return v.recordReceiveFailureLocked(FailureCodeReceivePayload, marker.LogicalSend, messageFingerprint(recv.ClientMsgNo), EvidenceStageReceive, uint64(len(recv.Payload)))
	}

	channel := sequenceChannel{id: recv.ChannelID, channelType: recv.ChannelType}
	recipientState := v.sequences[recipient]
	if recipientState != nil {
		if previous, exists := recipientState.channels[channel]; exists {
			if recv.MessageSeq <= previous {
				if recv.MessageSeq == previous {
					v.counters.DuplicateDeliveries++
				} else {
					v.counters.SequenceRegressions++
				}
				return v.recordReceiveFailureLocked(FailureCodeReceiveSequence, logical.LogicalSend, messageFingerprint(recv.ClientMsgNo), EvidenceStageReceive, recv.MessageSeq)
			}
			recipientState.channels[channel] = recv.MessageSeq
		} else {
			if v.sequenceCount >= v.config.SequenceCapacity {
				return v.recordSequenceCapacityLocked(logical)
			}
			recipientState.channels[channel] = recv.MessageSeq
			v.sequenceCount++
		}
	} else {
		if v.sequenceCount >= v.config.SequenceCapacity {
			return v.recordSequenceCapacityLocked(logical)
		}
		v.sequences[recipient] = &recipientSequenceState{channels: map[sequenceChannel]uint64{channel: recv.MessageSeq}}
		v.sequenceCount++
	}
	if v.sequenceCount > v.sequencePeak {
		v.sequencePeak = v.sequenceCount
	}

	if correlation := v.correlations[recv.ClientMsgNo]; correlation != nil {
		if correlation.recvSeen && (correlation.messageID != recv.MessageID || correlation.messageSeq != recv.MessageSeq) {
			return v.recordCorrelationConflictLocked(correlation, recv.MessageSeq)
		}
		if correlation.ackSeen && (correlation.messageID != recv.MessageID || correlation.messageSeq != recv.MessageSeq) {
			return v.recordCorrelationConflictLocked(correlation, recv.MessageSeq)
		}
		if !correlation.recvSeen {
			correlation.recvSeen = true
			correlation.messageID = recv.MessageID
			correlation.messageSeq = recv.MessageSeq
		}
		if correlation.ackSeen {
			v.completeCorrelationLocked(correlation)
		}
	}
	return nil
}

func (v *Verifier) recordSequenceCapacityLocked(logical LogicalSend) error {
	return v.recordFailureLocked(EvidenceEvent{
		Class:       FailureClassHarness,
		Stage:       EvidenceStageCapacity,
		Code:        FailureCodeSequenceCapacity,
		SampleIndex: logical.LogicalSend,
		Fingerprint: messageFingerprint(logical.ClientMsgNo),
		Value:       uint64(v.config.SequenceCapacity),
	})
}

// ReleaseRecipient removes all per-channel monotonic state owned by a logged
// out recipient, keeping verifier memory proportional to online ownership.
func (v *Verifier) ReleaseRecipient(recipient string) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	state := v.sequences[recipient]
	if state == nil {
		return 0
	}
	released := len(state.channels)
	delete(v.sequences, recipient)
	v.sequenceCount -= released
	return released
}

// ExpireCorrelations pops only due heap roots. Every expired sampled message is
// confirmed loss evidence and is physically removed from both retained indexes.
func (v *Verifier) ExpireCorrelations(now time.Time) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	expired := 0
	for len(v.deadlines) > 0 && !v.deadlines[0].deadline.After(now) {
		correlation := heap.Pop(&v.deadlines).(*sampledCorrelation)
		delete(v.correlations, correlation.logical.ClientMsgNo)
		v.counters.SampledExpired++
		expired++
		_ = v.evidence.Record(EvidenceEvent{
			Class:       FailureClassCorrelation,
			Stage:       EvidenceStageCorrelation,
			Code:        FailureCodeCorrelationExpired,
			SampleIndex: correlation.logical.LogicalSend,
			Fingerprint: messageFingerprint(correlation.logical.ClientMsgNo),
			Value:       uint64(v.config.CorrelationDeadline),
		})
	}
	return expired
}

// DrainSnapshot is a constant-time projection of unfinished verification state.
func (v *Verifier) DrainSnapshot() VerificationDrain {
	v.mu.Lock()
	defer v.mu.Unlock()
	drain := VerificationDrain{
		PendingUnfinished:      v.pendingUnfinished,
		CorrelationOutstanding: len(v.correlations),
	}
	if len(v.deadlines) > 0 {
		drain.NextCorrelationDeadline = v.deadlines[0].deadline
	}
	return drain
}

// CompleteTerminal marks the explicit final result chosen by the retry engine.
func (v *Verifier) CompleteTerminal(logical LogicalSend, code TerminalSendCode) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	pending := v.pending[logical.ClientMsgNo]
	if pending == nil || pending.logical != logical || code < TerminalSendRetryExhausted || code > TerminalSendSessionClosed {
		return v.recordHarnessLocked(FailureCodeGeneratorInvariant, logical, EvidenceStageSend, uint64(code))
	}
	if pending.completion != sendIncomplete {
		v.counters.DuplicateCompletions++
		return v.recordSendFailureLocked(FailureCodeDuplicateCompletion, logical.LogicalSend, messageFingerprint(logical.ClientMsgNo), EvidenceStageSend, uint64(code))
	}
	pending.completion = sendTerminal
	pending.terminal = code
	v.pendingUnfinished--
	v.counters.Terminal++
	return v.recordSendFailureLocked(FailureCodeTerminalSend, logical.LogicalSend, messageFingerprint(logical.ClientMsgNo), EvidenceStageSend, uint64(code))
}

// ReleaseSend physically removes a completed pending identity. The worker calls
// this after it no longer needs duplicate-completion discrimination.
func (v *Verifier) ReleaseSend(logical LogicalSend) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	pending := v.pending[logical.ClientMsgNo]
	if pending == nil || pending.logical != logical || pending.completion == sendIncomplete {
		return v.recordHarnessLocked(FailureCodeGeneratorInvariant, logical, EvidenceStageSend, 0)
	}
	delete(v.pending, logical.ClientMsgNo)
	return nil
}

// Snapshot returns aggregate counters and gauges; it never enumerates identities.
func (v *Verifier) Snapshot() VerifierSnapshot {
	v.mu.Lock()
	defer v.mu.Unlock()
	snapshot := v.counters
	snapshot.PendingCurrent = len(v.pending)
	snapshot.PendingUnfinished = v.pendingUnfinished
	snapshot.PendingPeak = v.pendingPeak
	snapshot.SequenceCurrent = v.sequenceCount
	snapshot.SequencePeak = v.sequencePeak
	snapshot.CorrelationCurrent = len(v.correlations)
	snapshot.CorrelationPeak = v.correlationPeak
	snapshot.DeadlineCurrent = len(v.deadlines)
	snapshot.Classification = v.evidence.Snapshot().Classification
	return snapshot
}

// EvidenceSnapshot returns the recorder's stable, deeply copied redacted view.
func (v *Verifier) EvidenceSnapshot() EvidenceSnapshot {
	return v.evidence.Snapshot()
}

func (v *Verifier) sampledLocked(logical LogicalSend) (bool, error) {
	phase, err := v.model.identity.decisionBelow("verification-sample-phase/v1", 100, uint64(logical.WorkerID))
	if err != nil {
		return false, err
	}
	return (logical.LogicalSend%100+phase)%100 == 0, nil
}

func (v *Verifier) completeCorrelationLocked(correlation *sampledCorrelation) {
	v.removeCorrelationLocked(correlation)
	v.counters.SampledDelivered++
}

func (v *Verifier) removeCorrelationLocked(correlation *sampledCorrelation) {
	delete(v.correlations, correlation.logical.ClientMsgNo)
	if correlation.heapIndex >= 0 {
		heap.Remove(&v.deadlines, correlation.heapIndex)
	}
}

func (v *Verifier) recordCorrelationConflictLocked(correlation *sampledCorrelation, value uint64) error {
	v.removeCorrelationLocked(correlation)
	v.counters.ConflictingCompletions++
	return v.recordFailureLocked(EvidenceEvent{
		Class:       FailureClassCorrelation,
		Stage:       EvidenceStageCorrelation,
		Code:        FailureCodeCorrelationSequenceConflict,
		SampleIndex: correlation.logical.LogicalSend,
		Fingerprint: messageFingerprint(correlation.logical.ClientMsgNo),
		Value:       value,
	})
}

func (v *Verifier) recordHarnessLocked(code FailureCode, logical LogicalSend, stage EvidenceStage, value uint64) error {
	return v.recordFailureLocked(EvidenceEvent{
		Class:       FailureClassHarness,
		Stage:       stage,
		Code:        code,
		SampleIndex: logical.LogicalSend,
		Fingerprint: messageFingerprint(logical.ClientMsgNo),
		Value:       value,
	})
}

func (v *Verifier) recordSendFailureLocked(code FailureCode, sample uint64, fingerprint [16]byte, stage EvidenceStage, value uint64) error {
	return v.recordFailureLocked(EvidenceEvent{
		Class:       FailureClassSend,
		Stage:       stage,
		Code:        code,
		SampleIndex: sample,
		Fingerprint: fingerprint,
		Value:       value,
	})
}

func (v *Verifier) recordReceiveFailureLocked(code FailureCode, sample uint64, fingerprint [16]byte, stage EvidenceStage, value uint64) error {
	return v.recordFailureLocked(EvidenceEvent{
		Class:       FailureClassReceive,
		Stage:       stage,
		Code:        code,
		SampleIndex: sample,
		Fingerprint: fingerprint,
		Value:       value,
	})
}

func (v *Verifier) recordFailureLocked(event EvidenceEvent) error {
	_ = v.evidence.Record(event)
	return &VerificationError{classification: classificationForFailureClass(event.Class), code: event.Code}
}

func validVerifierCapacity(capacity int) bool {
	return capacity > 0 && capacity <= maxVerifierCapacity
}

func failureCodeName(code FailureCode) string {
	switch code {
	case FailureCodeUnknownSendack:
		return "unknown_sendack"
	case FailureCodeDuplicateCompletion:
		return "duplicate_completion"
	case FailureCodeConflictingCompletion:
		return "conflicting_completion"
	case FailureCodeInvalidSendack:
		return "invalid_sendack"
	case FailureCodeTerminalSend:
		return "terminal_send"
	case FailureCodeReceiveProtocol:
		return "receive_protocol"
	case FailureCodeReceivePayload:
		return "receive_payload"
	case FailureCodeReceiveIdentity:
		return "receive_identity"
	case FailureCodeReceiveSequence:
		return "receive_sequence"
	case FailureCodeRecvack:
		return "recvack"
	case FailureCodeCorrelationExpired:
		return "correlation_expired"
	case FailureCodeCorrelationSequenceConflict:
		return "correlation_sequence_conflict"
	case FailureCodePendingCapacity:
		return "pending_capacity"
	case FailureCodeCorrelationCapacity:
		return "correlation_capacity"
	case FailureCodeSequenceCapacity:
		return "sequence_capacity"
	case FailureCodeGeneratorInvariant:
		return "generator_invariant"
	default:
		return "unknown"
	}
}

// RecvAcker is the narrow client-owned RECVACK transport seam.
type RecvAcker interface {
	AckRecv(context.Context, *frame.RecvackPacket) error
}
