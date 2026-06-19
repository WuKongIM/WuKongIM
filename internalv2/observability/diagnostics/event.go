package diagnostics

import "time"

// Stage identifies a stable diagnostics step in the message processing path.
type Stage string

// Result describes the outcome observed by a diagnostics event.
type Result string

// ErrorCode classifies common diagnostics failures with stable tokens.
type ErrorCode string

// Status summarizes the outcome of a diagnostics query.
type Status string

const (
	// StatusOK indicates the query found only successful events.
	StatusOK Status = "ok"
	// StatusError indicates the query found at least one failed event.
	StatusError Status = "error"
	// StatusPartial indicates the query found at least one partial event.
	StatusPartial Status = "partial"
	// StatusNotFound indicates the query matched no retained local events.
	StatusNotFound Status = "not_found"
)

const (
	// ResultOK indicates the observed stage completed successfully.
	ResultOK Result = "ok"
	// ResultError indicates the observed stage failed with an error.
	ResultError Result = "error"
	// ResultTimeout indicates the observed stage exceeded its deadline.
	ResultTimeout Result = "timeout"
	// ResultCanceled indicates the observed stage stopped because its context was canceled.
	ResultCanceled Result = "canceled"
	// ResultPartial indicates the observed stage returned an incomplete response.
	ResultPartial Result = "partial"
	// ResultDropped indicates the observed event or operation was dropped.
	ResultDropped Result = "dropped"
	// ResultSkipped indicates the observed stage was intentionally skipped.
	ResultSkipped Result = "skipped"
)

const (
	// ErrorCodeUnknown is the fallback classification for unrecognized failures.
	ErrorCodeUnknown ErrorCode = "unknown_error"
	// ErrorCodeInvalidFrame identifies malformed client or node frames.
	ErrorCodeInvalidFrame ErrorCode = "invalid_frame"
	// ErrorCodeUnauthenticated identifies senders that failed authentication.
	ErrorCodeUnauthenticated ErrorCode = "unauthenticated_sender"
	// ErrorCodeContextCanceled identifies failures caused by context cancellation.
	ErrorCodeContextCanceled ErrorCode = "context_canceled"
)

// Event describes one diagnostic fact observed on the local node.
type Event struct {
	// TraceID correlates events that belong to the same diagnostics trace.
	TraceID string `json:"trace_id,omitempty"`
	// SpanID identifies this event span within a trace when span data is available.
	SpanID string `json:"span_id,omitempty"`
	// ParentSpanID identifies the parent span when this event is part of a span tree.
	ParentSpanID string `json:"parent_span_id,omitempty"`
	// Stage identifies the stable processing step that emitted this event.
	Stage Stage `json:"stage"`
	// At is the local node timestamp when the event was recorded.
	At time.Time `json:"at"`
	// Duration is the elapsed time observed for this stage.
	Duration time.Duration `json:"duration,omitempty"`
	// NodeID is the local cluster node that observed the event.
	NodeID uint64 `json:"node_id,omitempty"`
	// PeerNodeID is the remote cluster node involved in this event, when any.
	PeerNodeID uint64 `json:"peer_node_id,omitempty"`
	// SlotID identifies the slot associated with the event, when known.
	SlotID uint32 `json:"slot_id,omitempty"`
	// ChannelKey is the diagnostics-safe channel identifier used for lookups.
	ChannelKey string `json:"channel_key,omitempty"`
	// ClientMsgNo is the client message number used for idempotency correlation.
	ClientMsgNo string `json:"client_msg_no,omitempty"`
	// MessageSeq is the single message sequence associated with this event.
	MessageSeq uint64 `json:"message_seq,omitempty"`
	// RangeStart is the first message sequence covered by a range event.
	RangeStart uint64 `json:"range_start,omitempty"`
	// RangeEnd is the last message sequence covered by a range event.
	RangeEnd uint64 `json:"range_end,omitempty"`
	// FromUID is the sender UID and must be redacted before response serialization.
	FromUID string `json:"from_uid,omitempty"`
	// Service identifies the node-local service or RPC path involved in this event.
	Service string `json:"service,omitempty"`
	// Result records the stable outcome token for the event.
	Result Result `json:"result"`
	// ErrorCode records the stable error classification when Result is not ok.
	ErrorCode ErrorCode `json:"error_code,omitempty"`
	// Error stores a bounded, non-payload error summary for diagnostics.
	Error string `json:"error,omitempty"`
	// Attempt records the retry or delivery attempt associated with the event.
	Attempt int `json:"attempt,omitempty"`
	// RequestCount records how many logical requests were included in a batch event.
	RequestCount int `json:"request_count,omitempty"`
	// RecordCount records how many durable log records were included in a batch event.
	RecordCount int `json:"record_count,omitempty"`
	// ByteCount records the approximate payload bytes included in a batch event.
	ByteCount int `json:"byte_count,omitempty"`
	// QueueDepth records queue pressure observed at the event point.
	QueueDepth int `json:"queue_depth,omitempty"`
	// ReplicaRole records the channel replica role observed for this event.
	ReplicaRole string `json:"replica_role,omitempty"`
	// SampleReason explains why this event was retained by diagnostics sampling.
	SampleReason string `json:"sample_reason,omitempty"`
}

// Query describes the supported node-local diagnostics lookup keys.
type Query struct {
	TraceID     string `json:"trace_id,omitempty"`
	ClientMsgNo string `json:"client_msg_no,omitempty"`
	ChannelKey  string `json:"channel_key,omitempty"`
	// UID filters events by sender UID and must not cause event FromUID exposure.
	UID        string `json:"uid,omitempty"`
	MessageSeq uint64 `json:"message_seq,omitempty"`
	Stage      Stage  `json:"stage,omitempty"`
	Result     Result `json:"result,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

// QuerySummary contains compact aggregate hints for matched diagnostics events.
type QuerySummary struct {
	SlowestStage      string `json:"slowest_stage,omitempty"`
	SlowestDurationMS int64  `json:"slowest_duration_ms,omitempty"`
	ErrorStage        string `json:"error_stage,omitempty"`
	ErrorCode         string `json:"error_code,omitempty"`
}

// QueryResult is the stable JSON response envelope for diagnostics queries.
type QueryResult struct {
	Scope       string       `json:"scope"`
	NodeID      uint64       `json:"node_id"`
	TraceID     string       `json:"trace_id,omitempty"`
	ClientMsgNo string       `json:"client_msg_no,omitempty"`
	ChannelKey  string       `json:"channel_key,omitempty"`
	UID         string       `json:"uid,omitempty"`
	MessageSeq  uint64       `json:"message_seq,omitempty"`
	Query       Query        `json:"query"`
	Status      Status       `json:"status"`
	StartedAt   time.Time    `json:"started_at,omitempty"`
	DurationMS  int64        `json:"duration_ms,omitempty"`
	Summary     QuerySummary `json:"summary,omitempty"`
	Events      []Event      `json:"events"`
	Notes       []string     `json:"notes,omitempty"`
}

// ContainsMessageSeq reports whether this event directly or range-wise references seq.
func (e Event) ContainsMessageSeq(seq uint64) bool {
	if e.MessageSeq == seq {
		return true
	}
	if e.RangeStart == 0 || e.RangeEnd == 0 {
		return false
	}
	return seq >= e.RangeStart && seq <= e.RangeEnd
}

func normalizeEvent(event Event, now time.Time, maxErrorBytes int) Event {
	if event.At.IsZero() {
		event.At = now
	}
	if event.Result == "" {
		event.Result = ResultOK
	}
	if maxErrorBytes > 0 && len(event.Error) > maxErrorBytes {
		event.Error = event.Error[:maxErrorBytes]
	}
	return event
}

func redactEvent(event Event) Event {
	event.FromUID = ""
	return event
}
