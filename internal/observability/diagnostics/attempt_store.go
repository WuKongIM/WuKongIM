package diagnostics

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

const attemptSummaryCapacity = 65536
const attemptIndexCapacity = 2 * attemptSummaryCapacity
const attemptPhaseCount = 5
const attemptDurablePhaseIndex = attemptPhaseCount - 1

// AttemptStoreOptions opts into separate fixed diagnostic storage. Disabled
// construction returns before randomness, allocation, clocks or background work.
type AttemptStoreOptions struct {
	Enabled bool
	NodeID  uint64
	Epoch   [16]byte
}
type attemptRPCSlot struct {
	cookie  [16]byte
	request uint64
	state   uint8
	digits  uint8
}

// attemptSummaryRow stores only fixed values; indexes have separate budgets.
type attemptSummaryRow struct {
	key     sendtrace.AttemptKey
	ordinal uint64
	trace   [16]byte
	// at preserves the existing ingress monotonic clock without a new read.
	at        time.Time
	offsets   [attemptPhaseCount]int64
	durations [attemptPhaseCount]int64
	refs      [2]attemptRPCSlot
	keySlot   uint32
	traceSlot uint32
	results   [attemptPhaseCount]uint8
	known     uint8
	refCount  uint8
	flags     uint8
}
type attemptIndexEntry struct {
	key     sendtrace.AttemptKey
	ordinal uint64
	row     uint32
	used    uint8
}
type attemptTraceIndexEntry struct {
	trace   [16]byte
	ordinal uint64
	row     uint32
	used    uint8
}

const (
	rowTraceKnown uint8 = 1 << iota
	rowTraceAmbiguous
	rowRPCOverflow
	rowAtKnown
)

// AttemptStore is an opt-in bounded invocation summary, independent of the
// existing event ring. Every update is fenced by process epoch and ordinal.
type AttemptStore struct {
	mu          sync.RWMutex
	rows        []attemptSummaryRow
	index       []attemptIndexEntry
	traceIndex  []attemptTraceIndexEntry
	nextOrdinal uint64
	nodeID      uint64
	epoch       [16]byte
}

// NewAttemptStore allocates the fixed summary and indexes only when enabled.
func NewAttemptStore(opts AttemptStoreOptions) *AttemptStore {
	if !opts.Enabled {
		return nil
	}
	if opts.Epoch == [16]byte{} {
		if _, err := rand.Read(opts.Epoch[:]); err != nil {
			return nil
		}
	}
	return &AttemptStore{rows: make([]attemptSummaryRow, attemptSummaryCapacity), index: make([]attemptIndexEntry, attemptIndexCapacity), traceIndex: make([]attemptTraceIndexEntry, attemptIndexCapacity), nodeID: opts.NodeID, epoch: opts.Epoch}
}

// AttemptLinkEnabled reports whether this independently opted-in store exists.
func (s *AttemptStore) AttemptLinkEnabled() bool { return s != nil }
func attemptHash(key sendtrace.AttemptKey) uint64 {
	return binary.LittleEndian.Uint64(key.MessageFingerprint[:8]) ^ binary.LittleEndian.Uint64(key.ConnectFingerprint[8:]) ^ uint64(key.ClientSeq)*0x9e3779b97f4a7c15
}
func traceHash(key [16]byte) uint64 {
	return binary.LittleEndian.Uint64(key[:8]) ^ binary.LittleEndian.Uint64(key[8:])
}

// rowFor requires the store lock and rejects stale epochs or overwritten rows.
func (s *AttemptStore) rowFor(token sendtrace.AttemptToken) *attemptSummaryRow {
	if token.Epoch != s.epoch || token.Ordinal == 0 {
		return nil
	}
	row := &s.rows[(token.Ordinal-1)%attemptSummaryCapacity]
	if row.ordinal != token.Ordinal {
		return nil
	}
	return row
}

// Backshift deletion preserves an empty probe boundary instead of accumulating
// tombstones across a long run. Moved entries update their owning fixed row.
func (s *AttemptStore) eraseKeyIndex(position uint32) {
	hole := uint64(position)
	for scan := (hole + 1) % attemptIndexCapacity; s.index[scan].used == 1; scan = (scan + 1) % attemptIndexCapacity {
		entry := s.index[scan]
		home := attemptHash(entry.key) % attemptIndexCapacity
		if (scan+attemptIndexCapacity-home)%attemptIndexCapacity >= (hole+attemptIndexCapacity-home)%attemptIndexCapacity {
			s.index[hole] = entry
			if row := &s.rows[entry.row]; row.ordinal == entry.ordinal {
				row.keySlot = uint32(hole)
			}
			hole = scan
		}
	}
	s.index[hole] = attemptIndexEntry{}
}

// eraseTraceIndex preserves the fixed index probe boundary while rows wrap.
func (s *AttemptStore) eraseTraceIndex(position uint32) {
	hole := uint64(position)
	for scan := (hole + 1) % attemptIndexCapacity; s.traceIndex[scan].used == 1; scan = (scan + 1) % attemptIndexCapacity {
		entry := s.traceIndex[scan]
		home := traceHash(entry.trace) % attemptIndexCapacity
		if (scan+attemptIndexCapacity-home)%attemptIndexCapacity >= (hole+attemptIndexCapacity-home)%attemptIndexCapacity {
			s.traceIndex[hole] = entry
			if row := &s.rows[entry.row]; row.ordinal == entry.ordinal {
				row.traceSlot = uint32(hole)
			}
			hole = scan
		}
	}
	s.traceIndex[hole] = attemptTraceIndexEntry{}
}

// BeginSendAttempt uses the existing ingress timestamp; it never reads a clock.
func (s *AttemptStore) BeginSendAttempt(key sendtrace.AttemptKey, at time.Time) sendtrace.AttemptToken {
	if s == nil || !key.Valid() {
		return sendtrace.AttemptToken{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nextOrdinal == math.MaxUint64 {
		return sendtrace.AttemptToken{}
	}
	s.nextOrdinal++
	ordinal := s.nextOrdinal
	slot := uint32((ordinal - 1) % attemptSummaryCapacity)
	row := &s.rows[slot]
	if row.ordinal != 0 {
		entry := &s.index[row.keySlot]
		if entry.used == 1 && entry.ordinal == row.ordinal {
			s.eraseKeyIndex(row.keySlot)
		}
		if row.flags&rowTraceKnown != 0 {
			entry := &s.traceIndex[row.traceSlot]
			if entry.used == 1 && entry.ordinal == row.ordinal {
				s.eraseTraceIndex(row.traceSlot)
			}
		}
	}
	*row = attemptSummaryRow{key: key, ordinal: ordinal}
	if !at.IsZero() {
		row.at = at
		row.flags |= rowAtKnown
	}
	start := attemptHash(key) % attemptIndexCapacity
	for n := uint64(0); n < attemptIndexCapacity; n++ {
		i := (start + n) % attemptIndexCapacity
		if s.index[i].used != 1 {
			s.index[i] = attemptIndexEntry{key: key, ordinal: ordinal, row: slot, used: 1}
			row.keySlot = uint32(i)
			return sendtrace.AttemptToken{Epoch: s.epoch, Ordinal: ordinal}
		}
	}
	return sendtrace.AttemptToken{}
}
func attemptPhaseIndex(stage sendtrace.Stage) int {
	switch stage {
	case sendtrace.StageGatewayAsyncDispatchWait:
		return 0
	case sendtrace.StageGatewayMessagesSend:
		return 1
	case sendtrace.StageGatewayWriteSendack:
		return 2
	case sendtrace.StageGatewaySessionPublicationWait:
		return 3
	case sendtrace.StageMessageSendDurable:
		return attemptDurablePhaseIndex
	}
	return -1
}

var attemptStages = [attemptPhaseCount]sendtrace.Stage{sendtrace.StageGatewayAsyncDispatchWait, sendtrace.StageGatewayMessagesSend, sendtrace.StageGatewayWriteSendack, sendtrace.StageGatewaySessionPublicationWait, sendtrace.StageMessageSendDurable}
var attemptBoundaries = [attemptPhaseCount]string{"enqueue_to_dispatch", "message_call_to_result", "write_frame_callback", "ready_to_publication_attempt", "diagnostic_event_duration"}

func attemptResultCode(result sendtrace.Result) uint8 {
	switch result {
	case sendtrace.ResultOK:
		return 1
	case sendtrace.ResultError:
		return 2
	case sendtrace.ResultTimeout:
		return 3
	case sendtrace.ResultCanceled:
		return 4
	case sendtrace.ResultPartial:
		return 5
	case sendtrace.ResultDropped:
		return 6
	case sendtrace.ResultSkipped:
		return 7
	}
	return 0
}

var attemptResults = [8]sendtrace.Result{"", sendtrace.ResultOK, sendtrace.ResultError, sendtrace.ResultTimeout, sendtrace.ResultCanceled, sendtrace.ResultPartial, sendtrace.ResultDropped, sendtrace.ResultSkipped}

// RecordAttemptPhase retains the first valid fact for an exact live token.
// Gateway phases borrow existing start clocks; durable is duration-only because
// its retry-segment sum does not establish a continuous start or end.
func (s *AttemptStore) RecordAttemptPhase(token sendtrace.AttemptToken, phase sendtrace.AttemptPhase) {
	if s == nil || !token.Valid() {
		return
	}
	p := attemptPhaseIndex(phase.Stage)
	result := attemptResultCode(phase.Result)
	if p < 0 || phase.Duration < 0 || result == 0 || (p != attemptDurablePhaseIndex && phase.StartedAt.IsZero()) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.rowFor(token)
	if row == nil || row.known&(1<<p) != 0 {
		return
	}
	if p == attemptDurablePhaseIndex {
		row.durations[p] = int64(phase.Duration)
		row.results[p] = result
		row.known |= 1 << p
		return
	}
	if row.flags&rowAtKnown == 0 {
		row.at = phase.StartedAt
		row.flags |= rowAtKnown
	}
	// Reject arithmetic overflow rather than fabricate an interval.
	delta := phase.StartedAt.Sub(row.at)
	if delta < 0 || !row.at.Add(delta).Equal(phase.StartedAt) {
		return
	}
	if phase.Duration > 0 && delta > time.Duration(math.MaxInt64)-phase.Duration {
		return
	}
	row.offsets[p] = int64(delta)
	row.durations[p] = int64(phase.Duration)
	row.results[p] = result
	row.known |= 1 << p
}

// BindAttemptTrace accepts only fixed nonzero upper identities. Rebinding or
// reuse marks ambiguity; no later event may select the first/latest caller.
func (s *AttemptStore) BindAttemptTrace(token sendtrace.AttemptToken, trace string) {
	if s == nil {
		return
	}
	key, valid := sendtrace.DecodeFingerprint(trace)
	if !valid || key == [16]byte{} {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.rowFor(token)
	if row == nil {
		return
	}
	if row.flags&rowTraceKnown != 0 {
		if row.trace != key {
			row.flags |= rowTraceAmbiguous
		}
		return
	}
	start := traceHash(key) % attemptIndexCapacity
	free := -1
	for n := uint64(0); n < attemptIndexCapacity; n++ {
		i := (start + n) % attemptIndexCapacity
		entry := &s.traceIndex[i]
		if entry.used == 0 {
			if free < 0 {
				free = int(i)
			}
			break
		}
		if entry.used == 2 {
			if free < 0 {
				free = int(i)
			}
			continue
		}
		if entry.trace == key {
			other := &s.rows[entry.row]
			if other.ordinal == entry.ordinal {
				other.flags |= rowTraceAmbiguous
				row.flags |= rowTraceAmbiguous
			}
		}
	}
	if free < 0 {
		return
	}
	row.trace = key
	row.flags |= rowTraceKnown
	row.traceSlot = uint32(free)
	s.traceIndex[free] = attemptTraceIndexEntry{trace: key, ordinal: row.ordinal, row: uint32((row.ordinal - 1) % attemptSummaryCapacity), used: 1}
}
func compactAttemptRPC(ref sendtrace.RPCReference) (attemptRPCSlot, bool) {
	var out attemptRPCSlot
	if !ref.Valid() {
		return out, false
	}
	switch ref.State {
	case "bound":
		out.state = 1
	case "unbound":
		out.state = 2
	case "recorder_disabled":
		out.state = 3
	case "recorder_exhausted":
		out.state = 4
	case "local_untraced":
		out.state = 5
	case "remote_untraced":
		out.state = 6
	}
	if out.state <= 2 {
		raw := strings.TrimPrefix(ref.TraceID, "local/")
		cookie, request, _ := strings.Cut(raw, "/")
		out.cookie, _ = sendtrace.DecodeFingerprint(cookie)
		out.request, _ = strconv.ParseUint(request, 16, 64)
		out.digits = uint8(len(request))
	}
	return out, true
}
func expandAttemptRPC(ref attemptRPCSlot) sendtrace.RPCReference {
	states := [7]string{"", "bound", "unbound", "recorder_disabled", "recorder_exhausted", "local_untraced", "remote_untraced"}
	out := sendtrace.RPCReference{State: states[ref.state]}
	if ref.state <= 2 {
		out.TraceID = hex.EncodeToString(ref.cookie[:]) + "/" + strings.Repeat("0", int(ref.digits)-len(strconv.FormatUint(ref.request, 16))) + strconv.FormatUint(ref.request, 16)
		if ref.state == 2 {
			out.TraceID = "local/" + out.TraceID
		}
	}
	return out
}
func (s *AttemptStore) recordRPC(row *attemptSummaryRow, ref attemptRPCSlot) {
	for i := 0; i < int(row.refCount); i++ {
		if row.refs[i] == ref {
			return
		}
	}
	if row.refCount == 2 {
		row.flags |= rowRPCOverflow
		return
	}
	row.refs[row.refCount] = ref
	row.refCount++
}

// RecordAttemptRPC copies at most two exact opaque references for a live token.
func (s *AttemptStore) RecordAttemptRPC(token sendtrace.AttemptToken, reference sendtrace.RPCReference) {
	if s == nil {
		return
	}
	ref, ok := compactAttemptRPC(reference)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if row := s.rowFor(token); row != nil {
		s.recordRPC(row, ref)
	}
}

// RecordTraceRPC projects an already emitted original forward reference through
// the fixed upper index. A reused trace never selects its first/latest caller.
func (s *AttemptStore) RecordTraceRPC(trace string, reference *sendtrace.RPCReference) {
	if s == nil || reference == nil {
		return
	}
	key, ok := sendtrace.DecodeFingerprint(trace)
	if !ok {
		return
	}
	ref, ok := compactAttemptRPC(*reference)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if row := s.uniqueTraceRowLocked(key); row != nil {
		s.recordRPC(row, ref)
	}
}

// uniqueTraceRowLocked requires the store lock and returns only one retained,
// unambiguous upper binding. Missing, reused and overwritten bindings stay unknown.
func (s *AttemptStore) uniqueTraceRowLocked(key [16]byte) *attemptSummaryRow {
	start := traceHash(key) % attemptIndexCapacity
	var found *attemptSummaryRow
	count := 0
	for n := uint64(0); n < attemptIndexCapacity; n++ {
		entry := &s.traceIndex[(start+n)%attemptIndexCapacity]
		if entry.used == 0 {
			break
		}
		if entry.used != 1 || entry.trace != key {
			continue
		}
		row := &s.rows[entry.row]
		if row.ordinal != entry.ordinal {
			continue
		}
		count++
		found = row
	}
	if count == 1 && found.flags&rowTraceAmbiguous == 0 {
		return found
	}
	return nil
}

// RecordTraceDurable projects only the existing duration/result fact through a
// unique upper binding. Event publication time cannot supply a continuous start
// or end for retry-segment sums, so no clock or publication timestamp is retained.
func (s *AttemptStore) RecordTraceDurable(trace string, duration time.Duration, result sendtrace.Result) {
	if s == nil || duration < 0 {
		return
	}
	key, ok := sendtrace.DecodeFingerprint(trace)
	code := attemptResultCode(result)
	if !ok || code == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if row := s.uniqueTraceRowLocked(key); row != nil && row.known&(1<<attemptDurablePhaseIndex) == 0 {
		row.durations[attemptDurablePhaseIndex] = int64(duration)
		row.results[attemptDurablePhaseIndex] = code
		row.known |= 1 << attemptDurablePhaseIndex
	}
}

// AttemptCandidate is a detached observation, never a generic uniqueness claim.
type AttemptCandidate struct {
	Token          sendtrace.AttemptToken                    `json:"token"`
	UpperTraceID   string                                    `json:"upper_trace_id,omitempty"`
	TraceAmbiguous bool                                      `json:"trace_ambiguous,omitempty"`
	RPCOverflow    bool                                      `json:"rpc_overflow,omitempty"`
	Phases         [attemptPhaseCount]sendtrace.AttemptPhase `json:"phases"`
	RPCReferences  []sendtrace.RPCReference                  `json:"rpc_references,omitempty"`
}
type AttemptQueryItem struct {
	Key        sendtrace.AttemptKey `json:"key"`
	Status     string               `json:"status"`
	Candidates []AttemptCandidate   `json:"candidates"`
}
type AttemptQueryResult struct {
	Version uint64             `json:"version"`
	NodeID  uint64             `json:"node_id,omitempty"`
	Status  string             `json:"status"`
	Results []AttemptQueryItem `json:"results"`
}

// candidate projects Gateway intervals from the retained ingress monotonic
// base, never one-way network time. Durable always remains a duration-only fact.
func (s *AttemptStore) candidate(row attemptSummaryRow) AttemptCandidate {
	out := AttemptCandidate{Token: sendtrace.AttemptToken{Epoch: s.epoch, Ordinal: row.ordinal}, TraceAmbiguous: row.flags&rowTraceAmbiguous != 0, RPCOverflow: row.flags&rowRPCOverflow != 0}
	if row.flags&rowTraceKnown != 0 {
		out.UpperTraceID = hex.EncodeToString(row.trace[:])
	}
	for i, stage := range attemptStages {
		phase := sendtrace.AttemptPhase{Stage: stage, Boundary: attemptBoundaries[i], EndDerived: i != attemptDurablePhaseIndex}
		if row.known&(1<<i) != 0 {
			if i != attemptDurablePhaseIndex {
				phase.StartedAt = row.at.Add(time.Duration(row.offsets[i])).UTC()
			}
			phase.Duration = time.Duration(row.durations[i])
			phase.Result = attemptResults[row.results[i]]
			phase.Observed = true
		}
		out.Phases[i] = phase
	}
	for i := 0; i < int(row.refCount); i++ {
		out.RPCReferences = append(out.RPCReferences, expandAttemptRPC(row.refs[i]))
	}
	return out
}

// QuerySendAttempts reads only retained fixed summaries. Missing coverage and
// duplicate candidates remain explicit; a sole candidate is never called exact.
func (s *AttemptStore) QuerySendAttempts(ctx context.Context, keys []sendtrace.AttemptKey) AttemptQueryResult {
	out := AttemptQueryResult{Version: 1, Status: "unsupported", Results: []AttemptQueryItem{}}
	if s == nil {
		return out
	}
	out.NodeID = s.nodeID
	out.Status = "ok"
	if len(keys) > 16 {
		out.Status = "unknown"
		return out
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, key := range keys {
		item := AttemptQueryItem{Key: key, Status: "not_retained", Candidates: []AttemptCandidate{}}
		if !key.Valid() || (ctx != nil && ctx.Err() != nil) {
			item.Status = "unknown"
			out.Results = append(out.Results, item)
			continue
		}
		start := attemptHash(key) % attemptIndexCapacity
		var selected [2]attemptSummaryRow
		count := 0
		for n := uint64(0); n < attemptIndexCapacity; n++ {
			entry := &s.index[(start+n)%attemptIndexCapacity]
			if entry.used == 0 {
				break
			}
			if entry.used != 1 || entry.key != key {
				continue
			}
			row := s.rows[entry.row]
			if row.ordinal != entry.ordinal {
				continue
			}
			count++
			if selected[0].ordinal == 0 || row.ordinal < selected[0].ordinal {
				selected[1] = selected[0]
				selected[0] = row
			} else if selected[1].ordinal == 0 || row.ordinal < selected[1].ordinal {
				selected[1] = row
			}
		}
		if count == 1 {
			item.Status = "candidate"
		} else if count > 1 {
			item.Status = "ambiguous"
		}
		for _, row := range selected {
			if row.ordinal != 0 {
				item.Candidates = append(item.Candidates, s.candidate(row))
			}
		}
		out.Results = append(out.Results, item)
	}
	return out
}

// DiscardSendAttempt removes only a descriptor that never entered its mailbox.
func (s *AttemptStore) DiscardSendAttempt(token sendtrace.AttemptToken) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.rowFor(token)
	if row == nil {
		return
	}
	s.eraseKeyIndex(row.keySlot)
	if row.flags&rowTraceKnown != 0 {
		s.eraseTraceIndex(row.traceSlot)
	}
	*row = attemptSummaryRow{}
}
