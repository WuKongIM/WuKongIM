package chatlifecycle

import (
	"errors"
	"sort"
	"sync"
)

const maxEvidenceExamplesPerSide = 64

var (
	errEvidenceBounds = errors.New("chat lifecycle evidence: first and last bounds must each be in 1..64")
	errEvidenceEvent  = errors.New("chat lifecycle evidence: event uses an invalid closed vocabulary")
)

// FailureClass is the fixed, low-cardinality evidence partition. It deliberately
// contains no identity-derived values.
type FailureClass uint8

const (
	FailureClassSend FailureClass = iota + 1
	FailureClassReceive
	FailureClassCorrelation
	FailureClassHarness
)

// EvidenceStage identifies the bounded verifier stage that emitted an example.
type EvidenceStage uint8

const (
	EvidenceStageSend EvidenceStage = iota + 1
	EvidenceStageSendack
	EvidenceStageReceive
	EvidenceStageRecvack
	EvidenceStageCorrelation
	EvidenceStageCapacity
)

// FailureCode is a closed reason vocabulary. Error strings and raw protocol
// identities never become evidence codes.
type FailureCode uint8

const (
	FailureCodeUnknownSendack FailureCode = iota + 1
	FailureCodeDuplicateCompletion
	FailureCodeConflictingCompletion
	FailureCodeInvalidSendack
	FailureCodeTerminalSend

	FailureCodeReceiveProtocol
	FailureCodeReceivePayload
	FailureCodeReceiveIdentity
	FailureCodeReceiveSequence
	FailureCodeRecvack

	FailureCodeCorrelationExpired
	FailureCodeCorrelationSequenceConflict

	FailureCodePendingCapacity
	FailureCodeCorrelationCapacity
	FailureCodeSequenceCapacity
	FailureCodeGeneratorInvariant
	FailureCodeRecvackCanceled
	FailureCodeRecvackDeadline
	FailureCodeRecvackUnclassified
	FailureCodeEngineQueueSaturated
	FailureCodeEngineCPUSaturated
	FailureCodeEngineInflightSaturated
	FailureCodeEngineRetrySaturated
	FailureCodeSessionReadFailed
	FailureCodeSessionLoginSaturated
)

// EvidenceEvent is the recorder input. Fingerprint is a stable redacted digest;
// Value is a numeric fact such as a sequence or capacity.
type EvidenceEvent struct {
	Class       FailureClass
	Stage       EvidenceStage
	Code        FailureCode
	SampleIndex uint64
	Fingerprint [16]byte
	Value       uint64
}

// EvidenceExample is the public, identity-free retained example shape.
type EvidenceExample struct {
	Stage       EvidenceStage `json:"stage"`
	Code        FailureCode   `json:"code"`
	SampleIndex uint64        `json:"sample_index"`
	Fingerprint [16]byte      `json:"fingerprint"`
	Value       uint64        `json:"value"`
}

// EvidenceClassSnapshot contains aggregate count plus bounded disjoint first
// and last examples for one failure class.
type EvidenceClassSnapshot struct {
	Class FailureClass      `json:"class"`
	Count uint64            `json:"count"`
	First []EvidenceExample `json:"first"`
	Last  []EvidenceExample `json:"last"`
}

// EvidenceSnapshot is a stable, deeply copied projection of recorder state.
type EvidenceSnapshot struct {
	Classification SyncClassification      `json:"classification,omitempty"`
	Classes        []EvidenceClassSnapshot `json:"classes"`
}

// EvidenceRecorder is safe for concurrent verifier paths. Each class retains
// exactly firstK+lastK example slots regardless of elapsed run duration.
type EvidenceRecorder struct {
	mu             sync.Mutex
	firstK         int
	lastK          int
	classification SyncClassification
	classes        map[FailureClass]*retainedEvidenceClass
}

type retainedEvidenceClass struct {
	count    uint64
	first    []EvidenceExample
	last     []EvidenceExample
	lastNext int
}

// NewEvidenceRecorder validates fixed retention bounds before allocating state.
func NewEvidenceRecorder(firstK, lastK int) (*EvidenceRecorder, error) {
	if firstK < 1 || firstK > maxEvidenceExamplesPerSide || lastK < 1 || lastK > maxEvidenceExamplesPerSide {
		return nil, errEvidenceBounds
	}
	return &EvidenceRecorder{
		firstK:  firstK,
		lastK:   lastK,
		classes: make(map[FailureClass]*retainedEvidenceClass, 4),
	}, nil
}

// Record increments aggregate evidence and retains only bounded redacted examples.
func (r *EvidenceRecorder) Record(event EvidenceEvent) error {
	if r == nil || !validEvidenceEvent(event) {
		return errEvidenceEvent
	}
	example := EvidenceExample{
		Stage:       event.Stage,
		Code:        event.Code,
		SampleIndex: event.SampleIndex,
		Fingerprint: event.Fingerprint,
		Value:       event.Value,
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	class := r.classes[event.Class]
	if class == nil {
		class = &retainedEvidenceClass{
			first: make([]EvidenceExample, 0, r.firstK),
			last:  make([]EvidenceExample, 0, r.lastK),
		}
		r.classes[event.Class] = class
	}
	class.count++
	if len(class.first) < r.firstK {
		class.first = append(class.first, example)
	} else if len(class.last) < r.lastK {
		class.last = append(class.last, example)
	} else {
		class.last[class.lastNext] = example
		class.lastNext = (class.lastNext + 1) % r.lastK
	}

	classification := classificationForFailureClass(event.Class)
	if r.classification != SyncClassificationProductFailure {
		if classification == SyncClassificationProductFailure || r.classification == "" {
			r.classification = classification
		}
	}
	return nil
}

// Snapshot returns classes sorted by their fixed enum and copies every slice.
func (r *EvidenceRecorder) Snapshot() EvidenceSnapshot {
	if r == nil {
		return EvidenceSnapshot{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	keys := make([]FailureClass, 0, len(r.classes))
	for class := range r.classes {
		keys = append(keys, class)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	snapshot := EvidenceSnapshot{
		Classification: r.classification,
		Classes:        make([]EvidenceClassSnapshot, 0, len(keys)),
	}
	for _, key := range keys {
		retained := r.classes[key]
		class := EvidenceClassSnapshot{
			Class: key,
			Count: retained.count,
			First: append([]EvidenceExample(nil), retained.first...),
			Last:  chronologicalLast(retained.last, retained.lastNext, r.lastK),
		}
		snapshot.Classes = append(snapshot.Classes, class)
	}
	return snapshot
}

// reset starts a fresh worker generation after all prior work has joined.
func (r *EvidenceRecorder) reset() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.classification = ""
	r.classes = make(map[FailureClass]*retainedEvidenceClass, 4)
	r.mu.Unlock()
}

func chronologicalLast(last []EvidenceExample, next, capacity int) []EvidenceExample {
	if len(last) < capacity || next == 0 {
		return append([]EvidenceExample(nil), last...)
	}
	result := make([]EvidenceExample, 0, len(last))
	result = append(result, last[next:]...)
	result = append(result, last[:next]...)
	return result
}

func classificationForFailureClass(class FailureClass) SyncClassification {
	if class == FailureClassHarness {
		return SyncClassificationHarnessInvalid
	}
	return SyncClassificationProductFailure
}

func validEvidenceEvent(event EvidenceEvent) bool {
	if event.Stage < EvidenceStageSend || event.Stage > EvidenceStageCapacity {
		return false
	}
	switch event.Class {
	case FailureClassSend:
		return (event.Stage == EvidenceStageSend || event.Stage == EvidenceStageSendack) &&
			event.Code >= FailureCodeUnknownSendack && event.Code <= FailureCodeTerminalSend
	case FailureClassReceive:
		return (event.Stage == EvidenceStageReceive || event.Stage == EvidenceStageRecvack) &&
			event.Code >= FailureCodeReceiveProtocol && event.Code <= FailureCodeRecvack
	case FailureClassCorrelation:
		return event.Stage == EvidenceStageCorrelation &&
			event.Code >= FailureCodeCorrelationExpired && event.Code <= FailureCodeCorrelationSequenceConflict
	case FailureClassHarness:
		switch event.Code {
		case FailureCodePendingCapacity, FailureCodeCorrelationCapacity, FailureCodeSequenceCapacity:
			return event.Stage == EvidenceStageCapacity
		case FailureCodeGeneratorInvariant:
			return event.Stage == EvidenceStageSend || event.Stage == EvidenceStageRecvack || event.Stage == EvidenceStageCorrelation
		case FailureCodeRecvackCanceled, FailureCodeRecvackDeadline, FailureCodeRecvackUnclassified:
			return event.Stage == EvidenceStageRecvack
		case FailureCodeEngineQueueSaturated, FailureCodeEngineCPUSaturated,
			FailureCodeEngineInflightSaturated, FailureCodeEngineRetrySaturated:
			return event.Stage == EvidenceStageCapacity
		case FailureCodeSessionReadFailed:
			return event.Stage == EvidenceStageReceive
		case FailureCodeSessionLoginSaturated:
			return event.Stage == EvidenceStageCapacity
		default:
			return false
		}
	default:
		return false
	}
}
