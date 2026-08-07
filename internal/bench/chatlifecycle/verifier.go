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

const maxSendAttemptIdentities = len(fixedRetryBases) + 1

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
	reason    frame.ReasonCode
	clientSeq uint64
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

// ClientSeq identifies the exact rejected wire attempt.
func (e *SendackRejectedError) ClientSeq() uint64 {
	if e == nil {
		return 0
	}
	return e.clientSeq
}

// ProductRecvAckFailureCode is the explicit closed vocabulary by which a
// target adapter may attribute a RECVACK failure to product behavior.
type ProductRecvAckFailureCode uint8

const (
	ProductRecvAckRejected ProductRecvAckFailureCode = iota + 1
	ProductRecvAckProtocol
)

// ProductRecvAckError is an attribution token, not an error transport. It
// deliberately drops the original cause so raw target text cannot escape.
type ProductRecvAckError struct {
	code ProductRecvAckFailureCode
}

// NewProductRecvAckError creates an explicitly classified product failure.
// Invalid codes remain unclassified and therefore default to harness-invalid.
func NewProductRecvAckError(code ProductRecvAckFailureCode, _ error) error {
	return &ProductRecvAckError{code: code}
}

func (e *ProductRecvAckError) Error() string { return "chat lifecycle RECVACK product failure" }

func (e *ProductRecvAckError) valid() bool {
	return e != nil && e.code >= ProductRecvAckRejected && e.code <= ProductRecvAckProtocol
}

// VerifierSnapshot contains only aggregate counts and bounded-state gauges.
type VerifierSnapshot struct {
	Classification            SyncClassification
	Sent                      uint64
	Attempts                  uint64
	FirstAttempts             uint64
	FirstAttemptFailures      uint64
	RetryAttempts             uint64
	Acknowledged              uint64
	SendackRejections         uint64
	Terminal                  uint64
	DuplicateCompletions      uint64
	ConflictingCompletions    uint64
	UnknownSendacks           uint64
	Received                  uint64
	ReceiveFailures           uint64
	ReceiveAcknowledged       uint64
	ReceiveAckFailures        uint64
	ReceiveAckProductFailures uint64
	ReceiveAckHarnessFailures uint64
	DuplicateDeliveries       uint64
	Corruptions               uint64
	SequenceRegressions       uint64
	ConflictingDeliveries     uint64
	Sampled                   uint64
	SampledDelivered          uint64
	SampledExpired            uint64
	Losses                    uint64
	Duplicates                uint64
	PendingCurrent            int
	PendingUnfinished         int
	PendingPeak               int
	SequenceCurrent           int
	SequencePeak              int
	CorrelationCurrent        int
	CorrelationPeak           int
	DeadlineCurrent           int
	ReleasedAttemptCurrent    int
	ReleasedAttemptPeak       int
	SendackLatency            WorkerHistogramSnapshot
	RecvackLatency            WorkerHistogramSnapshot
}

type verifierSendCounters struct {
	sent                   uint64
	attempts               uint64
	firstAttempts          uint64
	firstAttemptFailures   uint64
	retryAttempts          uint64
	acknowledged           uint64
	sendackRejections      uint64
	terminal               uint64
	duplicateCompletions   uint64
	conflictingCompletions uint64
	unknownSendacks        uint64
	sampled                uint64
	sampledDelivered       uint64
	sampledExpired         uint64
}

type verifierReceiveCounters struct {
	received                  uint64
	receiveFailures           uint64
	receiveAcknowledged       uint64
	receiveAckFailures        uint64
	receiveAckProductFailures uint64
	receiveAckHarnessFailures uint64
	duplicateDeliveries       uint64
	corruptions               uint64
	sequenceRegressions       uint64
	conflictingDeliveries     uint64
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
	logical             LogicalSend
	registeredAt        time.Time
	completion          sendCompletion
	completionClientSeq uint64
	messageID           int64
	messageSeq          uint64
	terminal            TerminalSendCode
	attempts            [maxSendAttemptIdentities]sendAttemptIdentity
	attemptCount        uint8
	firstAttemptFailed  bool
}

type sendAttemptIdentity struct {
	clientSeq   uint64
	attempt     uint8
	outstanding bool
}

func (s *pendingSend) attempt(clientSeq uint64) *sendAttemptIdentity {
	if s == nil || clientSeq == 0 {
		return nil
	}
	for index := uint8(0); index < s.attemptCount; index++ {
		if s.attempts[index].clientSeq == clientSeq {
			return &s.attempts[index]
		}
	}
	return nil
}

type releasedAttemptKey struct {
	clientSeq   uint64
	clientMsgNo string
}

type releasedAttempt struct {
	key       releasedAttemptKey
	deadline  time.Time
	heapIndex int
}

type releasedAttemptDeadlineHeap []*releasedAttempt

func (h releasedAttemptDeadlineHeap) Len() int { return len(h) }
func (h releasedAttemptDeadlineHeap) Less(i, j int) bool {
	if h[i].deadline.Equal(h[j].deadline) {
		if h[i].key.clientMsgNo == h[j].key.clientMsgNo {
			return h[i].key.clientSeq < h[j].key.clientSeq
		}
		return h[i].key.clientMsgNo < h[j].key.clientMsgNo
	}
	return h[i].deadline.Before(h[j].deadline)
}
func (h releasedAttemptDeadlineHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}
func (h *releasedAttemptDeadlineHeap) Push(value any) {
	item := value.(*releasedAttempt)
	item.heapIndex = len(*h)
	*h = append(*h, item)
}
func (h *releasedAttemptDeadlineHeap) Pop() any {
	old := *h
	last := len(old) - 1
	item := old[last]
	old[last] = nil
	item.heapIndex = -1
	*h = old[:last]
	return item
}

type recipientSequenceState struct {
	channels map[sequenceChannel]recvSequenceValue
}

type sequenceChannel struct {
	id          string
	channelType uint8
}

type recvSequenceValue struct {
	messageID          int64
	messageSeq         uint64
	messageFingerprint [16]byte
	ackConfirmed       bool
}

func (v recvSequenceValue) sameIdentity(other recvSequenceValue) bool {
	return v.messageID == other.messageID && v.messageSeq == other.messageSeq && v.messageFingerprint == other.messageFingerprint
}

type recvSequenceToken struct {
	recipient string
	channel   sequenceChannel
	value     recvSequenceValue
	admitted  bool
}

type preparedRecv struct {
	logical             LogicalSend
	marker              PayloadMarker
	evidenceFingerprint [16]byte
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
// Its methods are safe across asynchronous workers and recipient sessions.
// One recipient's RECV calls must still come from one session drain in wire
// order because sequence correctness is defined by that ordered stream.
type Verifier struct {
	// sendMu protects pending SENDs, sampled correlations, and deadlines.
	sendMu sync.Mutex
	// recvMu protects recipient sequence ownership and receive counters.
	recvMu   sync.Mutex
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
	releasedAttempts  map[releasedAttemptKey]*releasedAttempt
	releasedDeadlines releasedAttemptDeadlineHeap
	releasedPeak      int

	sendCounters   verifierSendCounters
	recvCounters   verifierReceiveCounters
	sendackLatency WorkerHistogramSnapshot
	recvackLatency WorkerHistogramSnapshot
}

// NewVerifier constructs empty state without preallocating from untrusted bounds.
func NewVerifier(model TrafficModel, config VerifierConfig, evidence *EvidenceRecorder) (*Verifier, error) {
	if model.identity == nil || evidence == nil || !validVerifierCapacity(config.PendingCapacity) ||
		!validVerifierCapacity(config.SequenceCapacity) || !validVerifierCapacity(config.CorrelationCapacity) ||
		config.CorrelationDeadline <= 0 {
		return nil, errVerifierConfig
	}
	v := &Verifier{
		model:            model,
		config:           config,
		evidence:         evidence,
		pending:          make(map[string]*pendingSend),
		sequences:        make(map[string]*recipientSequenceState),
		correlations:     make(map[string]*sampledCorrelation),
		releasedAttempts: make(map[releasedAttemptKey]*releasedAttempt),
		sendackLatency:   newWorkerHistogramSnapshot(),
		recvackLatency:   newWorkerHistogramSnapshot(),
	}
	heap.Init(&v.deadlines)
	heap.Init(&v.releasedDeadlines)
	return v, nil
}

// RegisterSend installs one attempt-independent logical identity and, when its
// exact-cycle position is selected, one physically removable deadline entry.
func (v *Verifier) RegisterSend(logical LogicalSend, registeredAt time.Time) error {
	v.sendMu.Lock()
	defer v.sendMu.Unlock()
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
	v.pending[logical.ClientMsgNo] = &pendingSend{logical: logical, registeredAt: registeredAt}
	v.pendingUnfinished++
	v.sendCounters.sent++
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
		v.sendCounters.sampled++
		if len(v.correlations) > v.correlationPeak {
			v.correlationPeak = len(v.correlations)
		}
	}
	return nil
}

// ShouldCorrelate reports the immutable exact one-percent sampling decision.
// Routing uses it before registration so sampled sends can require an online
// delivery recipient without changing the verifier's cycle or retained state.
func (v *Verifier) ShouldCorrelate(logical LogicalSend) (bool, error) {
	if v == nil || v.model.identity == nil || uint64(logical.WorkerID) >= v.model.identity.Workers() {
		return false, errVerifierConfig
	}
	return v.sampledLocked(logical)
}

// ObserveAttempt registers one explicit wire-attempt identity and verifies that
// a retry did not mint a replacement logical message identity.
func (v *Verifier) ObserveAttempt(logical LogicalSend, attempt RetryAttempt, clientSeq uint64) error {
	v.sendMu.Lock()
	defer v.sendMu.Unlock()
	pending := v.pending[logical.ClientMsgNo]
	if pending == nil || pending.logical != logical || pending.completion != sendIncomplete ||
		attempt.ClientMsgNo != logical.ClientMsgNo || attempt.Attempt > 3 || clientSeq == 0 ||
		pending.attemptCount >= uint8(maxSendAttemptIdentities) || pending.attempt(clientSeq) != nil {
		return v.recordHarnessLocked(FailureCodeGeneratorInvariant, logical, EvidenceStageSend, uint64(attempt.Attempt))
	}
	pending.attempts[pending.attemptCount] = sendAttemptIdentity{clientSeq: clientSeq, attempt: attempt.Attempt, outstanding: true}
	pending.attemptCount++
	v.sendCounters.attempts++
	if attempt.Attempt == 0 {
		v.sendCounters.firstAttempts++
	} else {
		v.sendCounters.retryAttempts++
	}
	return nil
}

// HandleSendack completes a pending logical send only for an exact successful
// SENDACK carrying positive server identity fields.
func (v *Verifier) HandleSendack(ack *frame.SendackPacket) error {
	return v.handleSendackAt(ack, time.Time{})
}

// HandleSendackAt additionally records registered-at to successful SENDACK
// latency from an explicit clock instant. A zero instant preserves the legacy
// verification-only seam without reading wall time.
func (v *Verifier) HandleSendackAt(ack *frame.SendackPacket, completedAt time.Time) error {
	return v.handleSendackAt(ack, completedAt)
}

func (v *Verifier) handleSendackAt(ack *frame.SendackPacket, completedAt time.Time) error {
	v.sendMu.Lock()
	defer v.sendMu.Unlock()
	if ack == nil || ack.ClientMsgNo == "" {
		return v.recordSendFailureLocked(FailureCodeInvalidSendack, 0, [16]byte{}, EvidenceStageSendack, 0)
	}
	positiveSuccess := ack.ReasonCode == frame.ReasonSuccess && ack.MessageID > 0 && ack.MessageSeq > 0
	if ack.ClientSeq != 0 {
		key := releasedAttemptKey{clientSeq: ack.ClientSeq, clientMsgNo: ack.ClientMsgNo}
		if v.releasedAttempts[key] != nil {
			var correlationErr error
			if positiveSuccess {
				correlationErr = v.observeSendackCorrelationLocked(ack)
			}
			v.consumeReleasedAttemptLocked(key)
			return correlationErr
		}
	}
	pending := v.pending[ack.ClientMsgNo]
	if pending == nil {
		var correlationErr error
		if positiveSuccess {
			correlationErr = v.observeSendackCorrelationLocked(ack)
		}
		return errors.Join(correlationErr, v.recordUnknownSendackLocked(0, ack))
	}
	var attempt *sendAttemptIdentity
	if pending.attemptCount > 0 {
		attempt = pending.attempt(ack.ClientSeq)
		if attempt == nil {
			return v.recordUnknownSendackLocked(pending.logical.LogicalSend, ack)
		}
	}
	var correlationErr error
	if positiveSuccess {
		correlationErr = v.observeSendackCorrelationLocked(ack)
	}
	if pending.attemptCount > 0 {
		if pending.completion != sendIncomplete && ack.ClientSeq != pending.completionClientSeq {
			attempt.outstanding = false
			return correlationErr
		}
		attempt.outstanding = false
	}
	if pending.completion != sendIncomplete {
		if pending.completion == sendAcknowledged && pending.messageID == ack.MessageID && pending.messageSeq == ack.MessageSeq {
			v.sendCounters.duplicateCompletions++
			completionErr := v.recordSendFailureLocked(FailureCodeDuplicateCompletion, pending.logical.LogicalSend, messageFingerprint(ack.ClientMsgNo), EvidenceStageSendack, ack.MessageSeq)
			return errors.Join(correlationErr, completionErr)
		}
		if pending.completion == sendTerminal && ack.MessageID == 0 && ack.MessageSeq == 0 {
			v.sendCounters.duplicateCompletions++
			completionErr := v.recordSendFailureLocked(FailureCodeDuplicateCompletion, pending.logical.LogicalSend, messageFingerprint(ack.ClientMsgNo), EvidenceStageSendack, 0)
			return errors.Join(correlationErr, completionErr)
		}
		v.sendCounters.conflictingCompletions++
		completionErr := v.recordSendFailureLocked(FailureCodeConflictingCompletion, pending.logical.LogicalSend, messageFingerprint(ack.ClientMsgNo), EvidenceStageSendack, ack.MessageSeq)
		return errors.Join(correlationErr, completionErr)
	}
	if ack.ReasonCode != frame.ReasonSuccess {
		if ack.MessageID != 0 || ack.MessageSeq != 0 {
			v.recordFirstAttemptFailureLocked(pending, attempt)
			return v.recordSendFailureLocked(FailureCodeInvalidSendack, pending.logical.LogicalSend, messageFingerprint(ack.ClientMsgNo), EvidenceStageSendack, ack.MessageSeq)
		}
		v.recordFirstAttemptFailureLocked(pending, attempt)
		v.sendCounters.sendackRejections++
		return &SendackRejectedError{reason: ack.ReasonCode, clientSeq: ack.ClientSeq}
	}
	if ack.MessageID <= 0 || ack.MessageSeq == 0 {
		v.recordFirstAttemptFailureLocked(pending, attempt)
		return v.recordSendFailureLocked(FailureCodeInvalidSendack, pending.logical.LogicalSend, messageFingerprint(ack.ClientMsgNo), EvidenceStageSendack, ack.MessageSeq)
	}
	pending.completion = sendAcknowledged
	pending.completionClientSeq = ack.ClientSeq
	pending.messageID = ack.MessageID
	pending.messageSeq = ack.MessageSeq
	v.pendingUnfinished--
	v.sendCounters.acknowledged++
	if !completedAt.IsZero() {
		recordWorkerLatency(&v.sendackLatency, completedAt.Sub(pending.registeredAt))
	}
	return correlationErr
}

func (v *Verifier) recordUnknownSendackLocked(logicalSend uint64, ack *frame.SendackPacket) error {
	v.sendCounters.unknownSendacks++
	return v.recordSendFailureLocked(
		FailureCodeUnknownSendack, logicalSend, messageFingerprint(ack.ClientMsgNo),
		EvidenceStageSendack, ack.MessageSeq,
	)
}

func (v *Verifier) observeSendackCorrelationLocked(ack *frame.SendackPacket) error {
	if correlation := v.correlations[ack.ClientMsgNo]; correlation != nil {
		if (correlation.ackSeen || correlation.recvSeen) &&
			(correlation.messageID != ack.MessageID || correlation.messageSeq != ack.MessageSeq) {
			return v.recordCorrelationConflictLocked(correlation, ack.MessageSeq)
		}
		correlation.ackSeen = true
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
// retained; neither error includes packet or transport text. Calls for one
// recipient must be made by one session drain in wire order. Logout must cancel
// and join that drain before calling ReleaseRecipient.
func (v *Verifier) HandleRecv(ctx context.Context, recipient string, recv *frame.RecvPacket, acker RecvAcker) error {
	return v.handleRecvAt(ctx, recipient, recv, acker, nil)
}

// HandleRecvAt additionally records each attempted RECVACK transport call
// duration using the supplied deterministic clock.
func (v *Verifier) HandleRecvAt(ctx context.Context, recipient string, recv *frame.RecvPacket, acker RecvAcker, now func() time.Time) error {
	return v.handleRecvAt(ctx, recipient, recv, acker, now)
}

func (v *Verifier) handleRecvAt(ctx context.Context, recipient string, recv *frame.RecvPacket, acker RecvAcker, now func() time.Time) error {
	if recv != nil && !v.model.hasRunMarkerPrefix(recv.Payload) {
		ackExternalRecv(ctx, recv, acker)
		return nil
	}
	v.recvMu.Lock()
	if recv == nil || recv.MessageID <= 0 || recv.MessageSeq == 0 {
		v.recvCounters.receiveFailures++
		v.recvMu.Unlock()
		validationErr := v.recordReceiveFailureLocked(FailureCodeReceiveProtocol, 0, [16]byte{}, EvidenceStageReceive, 0)
		return validationErr
	}
	v.recvCounters.received++
	v.recvMu.Unlock()

	prepared, prepareErr := v.prepareRecv(recipient, recv)
	var correlationErr error
	var sequenceErr error
	var sequenceToken recvSequenceToken
	if prepareErr == nil {
		v.sendMu.Lock()
		correlationErr = v.observeRecvCorrelationLocked(recv)
		v.sendMu.Unlock()

		v.recvMu.Lock()
		sequenceToken, sequenceErr = v.admitRecvSequenceLocked(recipient, recv, prepared)
		v.recvMu.Unlock()
	}
	validationErr := errors.Join(prepareErr, correlationErr, sequenceErr)
	if validationErr != nil {
		v.recvMu.Lock()
		v.recvCounters.receiveFailures++
		v.recvMu.Unlock()
	}

	ack := &frame.RecvackPacket{MessageID: recv.MessageID, MessageSeq: recv.MessageSeq}
	if acker == nil {
		v.recvMu.Lock()
		v.recvCounters.receiveAckFailures++
		v.recvCounters.receiveAckHarnessFailures++
		v.recvMu.Unlock()
		ackErr := v.recordFailureLocked(EvidenceEvent{
			Class:       FailureClassHarness,
			Stage:       EvidenceStageRecvack,
			Code:        FailureCodeGeneratorInvariant,
			Fingerprint: messageFingerprint(recv.ClientMsgNo),
			Value:       recv.MessageSeq,
		})
		return errors.Join(validationErr, ackErr)
	}
	var ackStarted time.Time
	if now != nil {
		ackStarted = now()
	}
	ackErr := acker.AckRecv(ctx, ack)
	var ackLatency time.Duration
	if now != nil {
		ackLatency = now().Sub(ackStarted)
	}
	if ackErr != nil {
		v.recvMu.Lock()
		if now != nil {
			recordWorkerLatency(&v.recvackLatency, ackLatency)
		}
		v.recvCounters.receiveAckFailures++
		classifiedAckErr := v.recordRecvAckFailureLocked(ackErr, recv)
		v.recvMu.Unlock()
		return errors.Join(validationErr, classifiedAckErr)
	}
	v.recvMu.Lock()
	v.confirmRecvSequenceLocked(sequenceToken)
	v.recvCounters.receiveAcknowledged++
	if now != nil {
		recordWorkerLatency(&v.recvackLatency, ackLatency)
	}
	v.recvMu.Unlock()
	return validationErr
}

// ackExternalRecv prevents unrelated Demo delivery from being replayed to a
// workload session. External traffic never changes correctness or ACK metrics,
// and its ACK result cannot classify the marked workload.
func ackExternalRecv(ctx context.Context, recv *frame.RecvPacket, acker RecvAcker) {
	if recv == nil || recv.MessageID <= 0 || recv.MessageSeq == 0 || acker == nil {
		return
	}
	_ = acker.AckRecv(ctx, &frame.RecvackPacket{MessageID: recv.MessageID, MessageSeq: recv.MessageSeq})
}

func (v *Verifier) prepareRecv(recipient string, recv *frame.RecvPacket) (preparedRecv, error) {
	evidenceFingerprint := messageFingerprint(recv.ClientMsgNo)
	marker, err := DecodePayloadMarker(recv.Payload)
	if err != nil {
		return preparedRecv{}, v.recordPayloadCorruption(0, evidenceFingerprint, uint64(len(recv.Payload)))
	}

	var target string
	switch marker.Kind {
	case TrafficPerson:
		if recipient == "" || recv.FromUID == "" || recipient == recv.FromUID || recv.ChannelType != frame.ChannelTypePerson || recv.ChannelID != recv.FromUID {
			return preparedRecv{}, v.recordReceiveFailureLocked(FailureCodeReceiveIdentity, marker.LogicalSend, evidenceFingerprint, EvidenceStageReceive, recv.MessageSeq)
		}
		target = recipient
	case TrafficGroup:
		if recipient == "" || recv.FromUID == "" || recv.ChannelType != frame.ChannelTypeGroup || recv.ChannelID == "" {
			return preparedRecv{}, v.recordReceiveFailureLocked(FailureCodeReceiveIdentity, marker.LogicalSend, evidenceFingerprint, EvidenceStageReceive, recv.MessageSeq)
		}
		target = recv.ChannelID
	default:
		return preparedRecv{}, v.recordPayloadCorruption(marker.LogicalSend, evidenceFingerprint, recv.MessageSeq)
	}

	logical, err := v.model.NewLogicalSend(uint64(marker.WorkerID), marker.LogicalSend, marker.Kind, recv.FromUID, target)
	if err != nil || logical.ClientMsgNo != recv.ClientMsgNo {
		return preparedRecv{}, v.recordReceiveFailureLocked(FailureCodeReceiveIdentity, marker.LogicalSend, evidenceFingerprint, EvidenceStageReceive, recv.MessageSeq)
	}
	if err := v.model.verifyDecodedPayloadMarker(marker, logical); err != nil {
		return preparedRecv{}, v.recordPayloadCorruption(marker.LogicalSend, evidenceFingerprint, uint64(len(recv.Payload)))
	}
	return preparedRecv{logical: logical, marker: marker, evidenceFingerprint: evidenceFingerprint}, nil
}

func (v *Verifier) admitRecvSequenceLocked(recipient string, recv *frame.RecvPacket, prepared preparedRecv) (recvSequenceToken, error) {
	channel := sequenceChannel{id: recv.ChannelID, channelType: recv.ChannelType}
	value := recvSequenceValue{
		messageID:          recv.MessageID,
		messageSeq:         recv.MessageSeq,
		messageFingerprint: prepared.marker.MessageIdentity,
	}
	token := recvSequenceToken{recipient: recipient, channel: channel, value: value, admitted: true}
	recipientState := v.sequences[recipient]
	if recipientState != nil {
		if previous, exists := recipientState.channels[channel]; exists {
			if recv.MessageSeq <= previous.messageSeq {
				if recv.MessageSeq == previous.messageSeq && previous.sameIdentity(value) {
					if !previous.ackConfirmed {
						return token, nil
					}
					v.recvCounters.duplicateDeliveries++
				} else if recv.MessageSeq == previous.messageSeq {
					v.recvCounters.conflictingDeliveries++
				} else {
					v.recvCounters.sequenceRegressions++
				}
				sequenceErr := v.recordReceiveFailureLocked(FailureCodeReceiveSequence, prepared.logical.LogicalSend, prepared.evidenceFingerprint, EvidenceStageReceive, recv.MessageSeq)
				return recvSequenceToken{}, sequenceErr
			}
			recipientState.channels[channel] = value
		} else {
			if v.sequenceCount >= v.config.SequenceCapacity {
				return recvSequenceToken{}, v.recordSequenceCapacityLocked(prepared.logical)
			}
			recipientState.channels[channel] = value
			v.sequenceCount++
		}
	} else {
		if v.sequenceCount >= v.config.SequenceCapacity {
			return recvSequenceToken{}, v.recordSequenceCapacityLocked(prepared.logical)
		}
		v.sequences[recipient] = &recipientSequenceState{channels: map[sequenceChannel]recvSequenceValue{channel: value}}
		v.sequenceCount++
	}
	if v.sequenceCount > v.sequencePeak {
		v.sequencePeak = v.sequenceCount
	}

	return token, nil
}

func (v *Verifier) observeRecvCorrelationLocked(recv *frame.RecvPacket) error {
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

func (v *Verifier) confirmRecvSequenceLocked(token recvSequenceToken) {
	if !token.admitted {
		return
	}
	state := v.sequences[token.recipient]
	if state == nil {
		return
	}
	current, exists := state.channels[token.channel]
	if !exists || !current.sameIdentity(token.value) {
		return
	}
	current.ackConfirmed = true
	state.channels[token.channel] = current
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
// The caller must first cancel and join that recipient's sole session drain so
// no earlier HandleRecv call can recreate or mutate released state.
func (v *Verifier) ReleaseRecipient(recipient string) int {
	v.recvMu.Lock()
	defer v.recvMu.Unlock()
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
	v.sendMu.Lock()
	defer v.sendMu.Unlock()
	expired := 0
	for len(v.deadlines) > 0 && !v.deadlines[0].deadline.After(now) {
		correlation := heap.Pop(&v.deadlines).(*sampledCorrelation)
		delete(v.correlations, correlation.logical.ClientMsgNo)
		v.sendCounters.sampledExpired++
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
	for len(v.releasedDeadlines) > 0 && !v.releasedDeadlines[0].deadline.After(now) {
		attempt := heap.Pop(&v.releasedDeadlines).(*releasedAttempt)
		delete(v.releasedAttempts, attempt.key)
	}
	return expired
}

// DrainSnapshot is a constant-time projection of unfinished verification state.
func (v *Verifier) DrainSnapshot() VerificationDrain {
	v.sendMu.Lock()
	defer v.sendMu.Unlock()
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
	v.sendMu.Lock()
	defer v.sendMu.Unlock()
	pending := v.pending[logical.ClientMsgNo]
	if pending == nil || pending.logical != logical || code < TerminalSendRetryExhausted || code > TerminalSendSessionClosed {
		return v.recordHarnessLocked(FailureCodeGeneratorInvariant, logical, EvidenceStageSend, uint64(code))
	}
	if pending.completion != sendIncomplete {
		v.sendCounters.duplicateCompletions++
		return v.recordSendFailureLocked(FailureCodeDuplicateCompletion, logical.LogicalSend, messageFingerprint(logical.ClientMsgNo), EvidenceStageSend, uint64(code))
	}
	for index := uint8(0); index < pending.attemptCount; index++ {
		if pending.attempts[index].attempt == 0 {
			v.recordFirstAttemptFailureLocked(pending, &pending.attempts[index])
			break
		}
	}
	pending.completion = sendTerminal
	pending.terminal = code
	v.pendingUnfinished--
	v.sendCounters.terminal++
	return v.recordSendFailureLocked(FailureCodeTerminalSend, logical.LogicalSend, messageFingerprint(logical.ClientMsgNo), EvidenceStageSend, uint64(code))
}

// ReleaseSend physically removes a completed pending identity while retaining
// only its unresolved sibling wire attempts until the existing verifier
// deadline. The retained set is bounded by pending capacity times MaxAttempts.
func (v *Verifier) ReleaseSend(logical LogicalSend) error {
	v.sendMu.Lock()
	defer v.sendMu.Unlock()
	pending := v.pending[logical.ClientMsgNo]
	if pending == nil || pending.logical != logical || pending.completion == sendIncomplete {
		return v.recordHarnessLocked(FailureCodeGeneratorInvariant, logical, EvidenceStageSend, 0)
	}
	unresolved := 0
	for index := uint8(0); index < pending.attemptCount; index++ {
		attempt := pending.attempts[index]
		if attempt.outstanding && attempt.clientSeq != pending.completionClientSeq {
			unresolved++
		}
	}
	capacity := v.config.PendingCapacity * maxSendAttemptIdentities
	if unresolved > capacity-len(v.releasedAttempts) {
		return v.recordHarnessLocked(FailureCodePendingCapacity, logical, EvidenceStageCapacity, uint64(capacity))
	}
	deadline := pending.registeredAt.Add(v.config.CorrelationDeadline)
	for index := uint8(0); index < pending.attemptCount; index++ {
		attempt := pending.attempts[index]
		if !attempt.outstanding || attempt.clientSeq == pending.completionClientSeq {
			continue
		}
		key := releasedAttemptKey{clientSeq: attempt.clientSeq, clientMsgNo: logical.ClientMsgNo}
		if _, exists := v.releasedAttempts[key]; exists {
			return v.recordHarnessLocked(FailureCodeGeneratorInvariant, logical, EvidenceStageSend, attempt.clientSeq)
		}
		released := &releasedAttempt{key: key, deadline: deadline, heapIndex: -1}
		v.releasedAttempts[key] = released
		heap.Push(&v.releasedDeadlines, released)
	}
	if len(v.releasedAttempts) > v.releasedPeak {
		v.releasedPeak = len(v.releasedAttempts)
	}
	delete(v.pending, logical.ClientMsgNo)
	return nil
}

// ResolveAttemptError removes one registered wire attempt after an async or
// immediate transport failure. A released sibling is consumed without making
// the already completed logical send unknown.
func (v *Verifier) ResolveAttemptError(clientMsgNo string, clientSeq uint64) error {
	v.sendMu.Lock()
	defer v.sendMu.Unlock()
	key := releasedAttemptKey{clientSeq: clientSeq, clientMsgNo: clientMsgNo}
	if clientSeq != 0 && clientMsgNo != "" && v.consumeReleasedAttemptLocked(key) {
		return nil
	}
	pending := v.pending[clientMsgNo]
	if pending == nil || pending.attemptCount == 0 {
		return v.recordUnknownSendackLocked(0, &frame.SendackPacket{ClientSeq: clientSeq, ClientMsgNo: clientMsgNo})
	}
	attempt := pending.attempt(clientSeq)
	if attempt == nil {
		return v.recordUnknownSendackLocked(pending.logical.LogicalSend, &frame.SendackPacket{ClientSeq: clientSeq, ClientMsgNo: clientMsgNo})
	}
	if pending.completion == sendIncomplete {
		v.recordFirstAttemptFailureLocked(pending, attempt)
	}
	attempt.outstanding = false
	return nil
}

func (v *Verifier) consumeReleasedAttemptLocked(key releasedAttemptKey) bool {
	attempt := v.releasedAttempts[key]
	if attempt == nil {
		return false
	}
	if attempt.heapIndex >= 0 {
		heap.Remove(&v.releasedDeadlines, attempt.heapIndex)
	}
	delete(v.releasedAttempts, key)
	return true
}

// abortSendHarness removes an incomplete SEND after the worker has already
// proven that local bounded-resource saturation invalidated the run. It avoids
// manufacturing a product terminal failure for work the harness could not own.
func (v *Verifier) abortSendHarness(logical LogicalSend) error {
	v.sendMu.Lock()
	defer v.sendMu.Unlock()
	pending := v.pending[logical.ClientMsgNo]
	if pending == nil || pending.logical != logical || pending.completion != sendIncomplete {
		return v.recordHarnessLocked(FailureCodeGeneratorInvariant, logical, EvidenceStageSend, 0)
	}
	delete(v.pending, logical.ClientMsgNo)
	v.pendingUnfinished--
	if correlation := v.correlations[logical.ClientMsgNo]; correlation != nil {
		v.removeCorrelationLocked(correlation)
	}
	return nil
}

// Snapshot returns aggregate counters and gauges; it never enumerates identities.
func (v *Verifier) Snapshot() VerifierSnapshot {
	// Snapshot takes the send domain before the receive domain and never holds
	// both locks simultaneously, so no reverse lock order can form.
	v.sendMu.Lock()
	send := v.sendCounters
	snapshot := VerifierSnapshot{
		Sent:                   send.sent,
		Attempts:               send.attempts,
		FirstAttempts:          send.firstAttempts,
		FirstAttemptFailures:   send.firstAttemptFailures,
		RetryAttempts:          send.retryAttempts,
		Acknowledged:           send.acknowledged,
		SendackRejections:      send.sendackRejections,
		Terminal:               send.terminal,
		DuplicateCompletions:   send.duplicateCompletions,
		ConflictingCompletions: send.conflictingCompletions,
		UnknownSendacks:        send.unknownSendacks,
		Sampled:                send.sampled,
		SampledDelivered:       send.sampledDelivered,
		SampledExpired:         send.sampledExpired,
		Losses:                 send.sampledExpired,
	}
	snapshot.PendingCurrent = len(v.pending)
	snapshot.PendingUnfinished = v.pendingUnfinished
	snapshot.PendingPeak = v.pendingPeak
	snapshot.CorrelationCurrent = len(v.correlations)
	snapshot.CorrelationPeak = v.correlationPeak
	snapshot.DeadlineCurrent = len(v.deadlines)
	snapshot.ReleasedAttemptCurrent = len(v.releasedAttempts)
	snapshot.ReleasedAttemptPeak = v.releasedPeak
	snapshot.SendackLatency = v.sendackLatency
	v.sendMu.Unlock()

	v.recvMu.Lock()
	recv := v.recvCounters
	snapshot.Received = recv.received
	snapshot.ReceiveFailures = recv.receiveFailures
	snapshot.ReceiveAcknowledged = recv.receiveAcknowledged
	snapshot.ReceiveAckFailures = recv.receiveAckFailures
	snapshot.ReceiveAckProductFailures = recv.receiveAckProductFailures
	snapshot.ReceiveAckHarnessFailures = recv.receiveAckHarnessFailures
	snapshot.DuplicateDeliveries = recv.duplicateDeliveries
	snapshot.Duplicates = recv.duplicateDeliveries
	snapshot.Corruptions = recv.corruptions
	snapshot.SequenceRegressions = recv.sequenceRegressions
	snapshot.ConflictingDeliveries = recv.conflictingDeliveries
	snapshot.SequenceCurrent = v.sequenceCount
	snapshot.SequencePeak = v.sequencePeak
	snapshot.RecvackLatency = v.recvackLatency
	v.recvMu.Unlock()

	snapshot.Classification = v.evidence.Snapshot().Classification
	return snapshot
}

// EvidenceSnapshot returns the recorder's stable, deeply copied redacted view.
func (v *Verifier) EvidenceSnapshot() EvidenceSnapshot {
	return v.evidence.Snapshot()
}

// resetRuntime discards a fully joined worker generation's bounded identity
// indexes and counters. Callers must first stop every sender and recipient drain.
func (v *Verifier) resetRuntime() {
	if v == nil {
		return
	}
	v.sendMu.Lock()
	v.pending = make(map[string]*pendingSend)
	v.pendingUnfinished = 0
	v.pendingPeak = 0
	v.correlations = make(map[string]*sampledCorrelation)
	v.deadlines = nil
	heap.Init(&v.deadlines)
	v.correlationPeak = 0
	v.releasedAttempts = make(map[releasedAttemptKey]*releasedAttempt)
	v.releasedDeadlines = nil
	heap.Init(&v.releasedDeadlines)
	v.releasedPeak = 0
	v.sendCounters = verifierSendCounters{}
	v.sendackLatency = newWorkerHistogramSnapshot()
	v.sendMu.Unlock()

	v.recvMu.Lock()
	v.sequences = make(map[string]*recipientSequenceState)
	v.sequenceCount = 0
	v.sequencePeak = 0
	v.recvCounters = verifierReceiveCounters{}
	v.recvackLatency = newWorkerHistogramSnapshot()
	v.recvMu.Unlock()
}

func (v *Verifier) sampledLocked(logical LogicalSend) (bool, error) {
	var phase uint64
	var err error
	if logical.Kind == TrafficGroup {
		// Group-domain ordinals form one global sequence across workers, so a
		// shared phase preserves exact one-percent group sampling.
		phase, err = v.model.identity.decisionBelow("verification-group-sample-phase/v1", 100)
	} else {
		phase, err = v.model.identity.decisionBelow("verification-sample-phase/v1", 100, uint64(logical.WorkerID))
	}
	if err != nil {
		return false, err
	}
	return (logical.LogicalSend%100+phase)%100 == 0, nil
}

func (v *Verifier) completeCorrelationLocked(correlation *sampledCorrelation) {
	v.removeCorrelationLocked(correlation)
	v.sendCounters.sampledDelivered++
}

func (v *Verifier) removeCorrelationLocked(correlation *sampledCorrelation) {
	delete(v.correlations, correlation.logical.ClientMsgNo)
	if correlation.heapIndex >= 0 {
		heap.Remove(&v.deadlines, correlation.heapIndex)
	}
}

func (v *Verifier) recordCorrelationConflictLocked(correlation *sampledCorrelation, value uint64) error {
	v.removeCorrelationLocked(correlation)
	v.sendCounters.conflictingCompletions++
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

func (v *Verifier) recordPayloadCorruption(sample uint64, fingerprint [16]byte, value uint64) error {
	v.recvMu.Lock()
	v.recvCounters.corruptions++
	v.recvMu.Unlock()
	return v.recordReceiveFailureLocked(FailureCodeReceivePayload, sample, fingerprint, EvidenceStageReceive, value)
}

func (v *Verifier) recordFirstAttemptFailureLocked(pending *pendingSend, attempt *sendAttemptIdentity) {
	if pending == nil || attempt == nil || attempt.attempt != 0 || pending.firstAttemptFailed {
		return
	}
	pending.firstAttemptFailed = true
	v.sendCounters.firstAttemptFailures++
}

func (v *Verifier) recordRecvAckFailureLocked(ackErr error, recv *frame.RecvPacket) error {
	var product *ProductRecvAckError
	if errors.As(ackErr, &product) && product.valid() {
		v.recvCounters.receiveAckProductFailures++
		return v.recordReceiveFailureLocked(FailureCodeRecvack, 0, messageFingerprint(recv.ClientMsgNo), EvidenceStageRecvack, uint64(product.code))
	}
	v.recvCounters.receiveAckHarnessFailures++
	code := FailureCodeRecvackUnclassified
	switch {
	case errors.Is(ackErr, context.Canceled):
		code = FailureCodeRecvackCanceled
	case errors.Is(ackErr, context.DeadlineExceeded):
		code = FailureCodeRecvackDeadline
	}
	return v.recordFailureLocked(EvidenceEvent{
		Class:       FailureClassHarness,
		Stage:       EvidenceStageRecvack,
		Code:        code,
		Fingerprint: messageFingerprint(recv.ClientMsgNo),
		Value:       recv.MessageSeq,
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
	case FailureCodeRecvackCanceled:
		return "recvack_canceled"
	case FailureCodeRecvackDeadline:
		return "recvack_deadline"
	case FailureCodeRecvackUnclassified:
		return "recvack_unclassified"
	case FailureCodeEngineQueueSaturated:
		return "engine_queue_saturated"
	case FailureCodeEngineCPUSaturated:
		return "engine_cpu_saturated"
	case FailureCodeEngineInflightSaturated:
		return "engine_inflight_saturated"
	case FailureCodeEngineRetrySaturated:
		return "engine_retry_saturated"
	case FailureCodeSessionReadFailed:
		return "session_read_failed"
	case FailureCodeSessionLoginSaturated:
		return "session_login_saturated"
	case FailureCodeSessionRemoteTerminal:
		return "session_remote_terminal"
	case FailureCodeOfferedLoadUnderDelivery:
		return "offered_load_under_delivery"
	case FailureCodeSessionSchedulerCPUSaturated:
		return "session_scheduler_cpu_saturated"
	case FailureCodeSessionFactoryFailed:
		return "session_factory_failed"
	case FailureCodeSessionConnectFailed:
		return "session_connect_failed"
	case FailureCodeSessionSyncFailed:
		return "session_sync_failed"
	case FailureCodeSessionSyncValidation:
		return "session_sync_validation"
	case FailureCodeLifecycleFenceExhausted:
		return "lifecycle_fence_exhausted"
	case FailureCodeLifecycleLeaseInvalidated:
		return "lifecycle_lease_invalidated"
	case FailureCodeLifecycleReplaySaturated:
		return "lifecycle_replay_saturated"
	default:
		return "unknown"
	}
}

// RecvAcker is the narrow client-owned RECVACK transport seam.
type RecvAcker interface {
	AckRecv(context.Context, *frame.RecvackPacket) error
}
