package diagnostics

import (
	"context"
	"sort"
	"sync"
	"time"
)

const (
	defaultCapacity      = 50000
	defaultMaxEventsKey  = 256
	defaultMaxKeysIndex  = 10000
	defaultMaxQueryLimit = 500
	defaultQueryLimit    = 100
	defaultMaxErrorBytes = 256
)

// StoreOptions controls the bounded in-memory diagnostics store.
type StoreOptions struct {
	// NodeID is the local cluster node id reported in query envelopes.
	NodeID uint64
	// Capacity is the maximum number of retained events in the ring buffer.
	Capacity int
	// MaxEventsPerKey bounds retained event ids per index key.
	MaxEventsPerKey int
	// MaxKeysPerIndex bounds high-cardinality keys per index.
	MaxKeysPerIndex int
	// MaxErrorBytes bounds stored error text to avoid payload-sized diagnostics.
	MaxErrorBytes int
	// Now supplies timestamps for records and query envelopes.
	Now func() time.Time
}

type storedEvent struct {
	id    uint64
	event Event
}

// Store keeps recent diagnostics events in a bounded ring with bounded indexes.
type Store struct {
	mu       sync.RWMutex
	events   []storedEvent
	byID     map[uint64]Event
	nextSlot int
	nextID   uint64
	index    *indexes
	nodeID   uint64
	now      func() time.Time
	maxError int
}

// NewStore creates a bounded node-local diagnostics store.
func NewStore(opts StoreOptions) *Store {
	if opts.Capacity <= 0 {
		opts.Capacity = defaultCapacity
	}
	if opts.MaxEventsPerKey <= 0 {
		opts.MaxEventsPerKey = defaultMaxEventsKey
	}
	if opts.MaxKeysPerIndex <= 0 {
		opts.MaxKeysPerIndex = defaultMaxKeysIndex
	}
	if opts.MaxErrorBytes <= 0 {
		opts.MaxErrorBytes = defaultMaxErrorBytes
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Store{
		events:   make([]storedEvent, opts.Capacity),
		byID:     make(map[uint64]Event, opts.Capacity),
		index:    newIndexes(opts.MaxEventsPerKey, opts.MaxKeysPerIndex),
		nodeID:   opts.NodeID,
		now:      opts.Now,
		maxError: opts.MaxErrorBytes,
	}
}

// Record appends an event to the ring and bounded indexes.
func (s *Store) Record(event Event) {
	if s == nil {
		return
	}
	event = normalizeEvent(event, s.now(), s.maxError)
	s.mu.Lock()
	if overwritten := s.events[s.nextSlot]; overwritten.id != 0 {
		delete(s.byID, overwritten.id)
	}
	s.nextID++
	id := s.nextID
	s.events[s.nextSlot] = storedEvent{id: id, event: event}
	s.byID[id] = event
	s.nextSlot = (s.nextSlot + 1) % len(s.events)
	s.index.add(id, event)
	s.mu.Unlock()
}

// UsageRatio returns the retained-event share of the ring buffer capacity.
func (s *Store) UsageRatio() float64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.events) == 0 {
		return 0
	}
	return float64(len(s.byID)) / float64(len(s.events))
}

// Query returns a redacted, sorted snapshot of retained events matching q.
func (s *Store) Query(ctx context.Context, q Query) QueryResult {
	if s == nil {
		return notFoundResult(q, 0, time.Time{}, 0)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return notFoundResult(q, 0, time.Time{}, 0)
	}
	started := s.now()
	limit := normalizeLimit(q.Limit)

	s.mu.RLock()
	ids := append([]uint64(nil), s.index.lookup(q)...)
	candidates := s.candidateEventsLocked(ids)
	if len(ids) == 0 && q.Stage != "" {
		candidates = s.stageCandidateEventsLocked(q.Stage)
	}
	if len(ids) == 0 && q.Stage == "" {
		candidates = s.retainedCandidateEventsLocked()
	}
	nodeID := s.nodeID
	s.mu.RUnlock()

	if ctx.Err() != nil {
		return notFoundResult(q, nodeID, started, s.now().Sub(started))
	}

	out := make([]Event, 0, minInt(limit, len(candidates)))
	for _, event := range candidates {
		if ctx.Err() != nil {
			return notFoundResult(q, nodeID, started, s.now().Sub(started))
		}
		if matchesQuery(event, q) {
			out = append(out, redactEvent(event))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	if len(out) == 0 {
		return notFoundResult(q, nodeID, started, s.now().Sub(started))
	}
	return buildQueryResult(nodeID, q, out, started, s.now().Sub(started))
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return defaultQueryLimit
	}
	if limit > defaultMaxQueryLimit {
		return defaultMaxQueryLimit
	}
	return limit
}

func (s *Store) candidateEventsLocked(ids []uint64) []Event {
	if len(ids) == 0 {
		return nil
	}
	out := make([]Event, 0, len(ids))
	for _, id := range ids {
		if event, ok := s.byID[id]; ok {
			out = append(out, event)
		}
	}
	return out
}

func (s *Store) stageCandidateEventsLocked(stage Stage) []Event {
	return s.candidateEventsLocked(s.index.stage.Get(string(stage)))
}

func (s *Store) retainedCandidateEventsLocked() []Event {
	out := make([]Event, 0, len(s.byID))
	for i := 0; i < len(s.events); i++ {
		slot := (s.nextSlot + i) % len(s.events)
		stored := s.events[slot]
		if stored.id == 0 {
			continue
		}
		out = append(out, stored.event)
	}
	return out
}

func matchesQuery(event Event, q Query) bool {
	if q.TraceID != "" && event.TraceID != q.TraceID {
		return false
	}
	if q.ClientMsgNo != "" && event.ClientMsgNo != q.ClientMsgNo {
		return false
	}
	if q.ChannelKey != "" && event.ChannelKey != q.ChannelKey {
		return false
	}
	if q.UID != "" && event.FromUID != q.UID {
		return false
	}
	if q.MessageSeq > 0 && !event.ContainsMessageSeq(q.MessageSeq) {
		return false
	}
	if q.Stage != "" && event.Stage != q.Stage {
		return false
	}
	if q.Result != "" && event.Result != q.Result {
		return false
	}
	return true
}

func notFoundResult(q Query, nodeID uint64, started time.Time, duration time.Duration) QueryResult {
	return QueryResult{
		Scope:       "local_node",
		NodeID:      nodeID,
		TraceID:     q.TraceID,
		ClientMsgNo: q.ClientMsgNo,
		ChannelKey:  q.ChannelKey,
		UID:         q.UID,
		MessageSeq:  q.MessageSeq,
		Query:       q,
		Status:      StatusNotFound,
		StartedAt:   started,
		DurationMS:  duration.Milliseconds(),
		Events:      []Event{},
		Notes:       []string{"no local diagnostics events matched; possible reasons include sampling, expiration, remote-node processing, or diagnostics disabled"},
	}
}

func buildQueryResult(nodeID uint64, q Query, events []Event, started time.Time, duration time.Duration) QueryResult {
	return QueryResult{
		Scope:       "local_node",
		NodeID:      nodeID,
		TraceID:     firstNonEmpty(q.TraceID, firstTraceID(events)),
		ClientMsgNo: firstNonEmpty(q.ClientMsgNo, firstClientMsgNo(events)),
		ChannelKey:  firstNonEmpty(q.ChannelKey, firstChannelKey(events)),
		UID:         q.UID,
		MessageSeq:  firstNonZero(q.MessageSeq, firstMessageSeq(events)),
		Query:       q,
		Status:      statusForEvents(events),
		StartedAt:   started,
		DurationMS:  duration.Milliseconds(),
		Summary:     summarizeEvents(events),
		Events:      events,
	}
}

func summarizeEvents(events []Event) QuerySummary {
	var summary QuerySummary
	var slowest time.Duration
	for _, event := range events {
		if event.Duration > slowest {
			slowest = event.Duration
			summary.SlowestStage = string(event.Stage)
			summary.SlowestDurationMS = event.Duration.Milliseconds()
		}
		if summary.ErrorStage == "" && isErrorResult(event.Result) {
			summary.ErrorStage = string(event.Stage)
			summary.ErrorCode = string(event.ErrorCode)
		}
	}
	return summary
}

func statusForEvents(events []Event) Status {
	status := StatusOK
	for _, event := range events {
		if isErrorResult(event.Result) {
			return StatusError
		}
		if event.Result == ResultPartial {
			status = StatusPartial
		}
	}
	return status
}

func isErrorResult(result Result) bool {
	return result == ResultError || result == ResultTimeout || result == ResultCanceled
}

func firstTraceID(events []Event) string {
	for _, event := range events {
		if event.TraceID != "" {
			return event.TraceID
		}
	}
	return ""
}

func firstClientMsgNo(events []Event) string {
	for _, event := range events {
		if event.ClientMsgNo != "" {
			return event.ClientMsgNo
		}
	}
	return ""
}

func firstChannelKey(events []Event) string {
	for _, event := range events {
		if event.ChannelKey != "" {
			return event.ChannelKey
		}
	}
	return ""
}

func firstMessageSeq(events []Event) uint64 {
	for _, event := range events {
		if event.MessageSeq > 0 {
			return event.MessageSeq
		}
	}
	return 0
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func firstNonZero(a, b uint64) uint64 {
	if a != 0 {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
