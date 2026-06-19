package manager

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/observability/diagnostics"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/gin-gonic/gin"
)

const (
	defaultDiagnosticsLimit = 100
	maxDiagnosticsLimit     = 500
)

// DiagnosticsResponse is the manager HTTP JSON envelope for diagnostics queries.
type DiagnosticsResponse struct {
	// Scope describes whether the query used cluster or local-node targeting.
	Scope string `json:"scope"`
	// Status is the aggregate diagnostics status.
	Status string `json:"status"`
	// GeneratedAt records when the aggregate response was generated.
	GeneratedAt time.Time `json:"generated_at"`
	// Query echoes the normalized query sent to the management usecase.
	Query DiagnosticsQueryDTO `json:"query"`
	// Summary contains compact aggregate hints for the result set.
	Summary DiagnosticsSummaryDTO `json:"summary"`
	// Nodes contains per-node query outcomes.
	Nodes []DiagnosticsNodeDTO `json:"nodes"`
	// Events contains merged diagnostics events after aggregate truncation.
	Events []DiagnosticsEventDTO `json:"events"`
	// Notes contains aggregate caveats such as partial controller snapshots.
	Notes []string `json:"notes"`
}

// DiagnosticsQueryDTO is the snake_case query echo returned to the web client.
type DiagnosticsQueryDTO struct {
	// TraceID filters events by diagnostics trace id.
	TraceID string `json:"trace_id,omitempty"`
	// ClientMsgNo filters events by client message number.
	ClientMsgNo string `json:"client_msg_no,omitempty"`
	// ChannelKey filters events by diagnostics channel key.
	ChannelKey string `json:"channel_key,omitempty"`
	// UID filters events by sender UID without exposing FromUID in event DTOs.
	UID string `json:"uid,omitempty"`
	// MessageSeq filters events by channel message sequence.
	MessageSeq uint64 `json:"message_seq,omitempty"`
	// Stage filters events by diagnostics stage.
	Stage string `json:"stage,omitempty"`
	// Result filters events by diagnostics result before node-local truncation.
	Result string `json:"result,omitempty"`
	// Limit is the normalized maximum number of aggregate events.
	Limit int `json:"limit,omitempty"`
}

// DiagnosticsSummaryDTO contains compact aggregate hints for diagnostics results.
type DiagnosticsSummaryDTO struct {
	FirstFailureStage     string   `json:"first_failure_stage"`
	FirstFailureResult    string   `json:"first_failure_result"`
	FirstFailureErrorCode string   `json:"first_failure_error_code"`
	SlowestStage          string   `json:"slowest_stage"`
	SlowestDurationMS     int64    `json:"slowest_duration_ms"`
	InvolvedNodes         []uint64 `json:"involved_nodes"`
	PeerNodes             []uint64 `json:"peer_nodes"`
	SlotID                uint32   `json:"slot_id"`
	ChannelKey            string   `json:"channel_key"`
	ClientMsgNo           string   `json:"client_msg_no"`
	MessageSeq            uint64   `json:"message_seq"`
	EventCount            int      `json:"event_count"`
}

// DiagnosticsNodeDTO reports one queried node's contribution to the aggregate.
type DiagnosticsNodeDTO struct {
	NodeID     uint64   `json:"node_id"`
	Status     string   `json:"status"`
	DurationMS int64    `json:"duration_ms"`
	EventCount int      `json:"event_count"`
	Notes      []string `json:"notes"`
}

// DiagnosticsEventDTO is the redacted manager HTTP view of one diagnostics event.
type DiagnosticsEventDTO struct {
	TraceID      string    `json:"trace_id"`
	SpanID       string    `json:"span_id"`
	ParentSpanID string    `json:"parent_span_id"`
	Stage        string    `json:"stage"`
	At           time.Time `json:"at"`
	DurationMS   int64     `json:"duration_ms"`
	NodeID       uint64    `json:"node_id"`
	PeerNodeID   uint64    `json:"peer_node_id"`
	SlotID       uint32    `json:"slot_id"`
	ChannelKey   string    `json:"channel_key"`
	ClientMsgNo  string    `json:"client_msg_no"`
	MessageSeq   uint64    `json:"message_seq"`
	RangeStart   uint64    `json:"range_start"`
	RangeEnd     uint64    `json:"range_end"`
	Service      string    `json:"service"`
	Result       string    `json:"result"`
	ErrorCode    string    `json:"error_code"`
	Error        string    `json:"error"`
	Attempt      int       `json:"attempt"`
	RequestCount int       `json:"request_count"`
	RecordCount  int       `json:"record_count"`
	ByteCount    int       `json:"byte_count"`
	QueueDepth   int       `json:"queue_depth"`
	ReplicaRole  string    `json:"replica_role"`
	SampleReason string    `json:"sample_reason"`
}

func (s *Server) handleDiagnosticsTrace(c *gin.Context) {
	traceID := strings.TrimSpace(c.Param("trace_id"))
	if traceID == "" {
		jsonError(c, http.StatusBadRequest, "bad_request", "trace_id is required")
		return
	}
	req, ok := s.baseDiagnosticsRequest(c)
	if !ok {
		return
	}
	req.Query.TraceID = traceID
	s.queryDiagnostics(c, req)
}

func (s *Server) handleDiagnosticsMessage(c *gin.Context) {
	req, ok := s.baseDiagnosticsRequest(c)
	if !ok {
		return
	}
	clientMsgNo := strings.TrimSpace(c.Query("client_msg_no"))
	channelKey := strings.TrimSpace(c.Query("channel_key"))
	messageSeqRaw := strings.TrimSpace(c.Query("message_seq"))
	hasClientSelector := clientMsgNo != ""
	hasChannelSelector := channelKey != "" || messageSeqRaw != ""
	if hasClientSelector && hasChannelSelector {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid message selector")
		return
	}
	if hasClientSelector {
		req.Query.ClientMsgNo = clientMsgNo
		s.queryDiagnostics(c, req)
		return
	}
	if channelKey == "" || messageSeqRaw == "" {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid message selector")
		return
	}
	messageSeq, err := parsePositiveUint64(messageSeqRaw)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid message_seq")
		return
	}
	req.Query.ChannelKey = channelKey
	req.Query.MessageSeq = messageSeq
	s.queryDiagnostics(c, req)
}

func (s *Server) handleDiagnosticsEvents(c *gin.Context) {
	req, ok := s.baseDiagnosticsRequest(c)
	if !ok {
		return
	}
	if stage := strings.TrimSpace(c.Query("stage")); stage != "" {
		req.Query.Stage = diagnostics.Stage(stage)
	}
	if uid := strings.TrimSpace(c.Query("uid")); uid != "" {
		req.Query.UID = uid
	}
	if channelKey := strings.TrimSpace(c.Query("channel_key")); channelKey != "" {
		req.Query.ChannelKey = channelKey
	}
	result, valid := parseDiagnosticsResult(c.Query("result"))
	if !valid {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid result")
		return
	}
	req.Query.Result = result
	s.queryDiagnostics(c, req)
}

func (s *Server) baseDiagnosticsRequest(c *gin.Context) (managementusecase.DiagnosticsQueryRequest, bool) {
	limit, err := parseDiagnosticsLimit(c.Query("limit"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid limit")
		return managementusecase.DiagnosticsQueryRequest{}, false
	}
	nodeID, err := parseOptionalPositiveUint64(c.Query("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return managementusecase.DiagnosticsQueryRequest{}, false
	}
	return managementusecase.DiagnosticsQueryRequest{
		NodeID: nodeID,
		Query:  diagnostics.Query{Limit: limit},
	}, true
}

func (s *Server) queryDiagnostics(c *gin.Context, req managementusecase.DiagnosticsQueryRequest) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	resp, err := s.management.QueryDiagnostics(c.Request.Context(), req)
	if err != nil {
		jsonError(c, http.StatusInternalServerError, "internal_error", "diagnostics query failed")
		return
	}
	c.JSON(http.StatusOK, diagnosticsResponseDTO(resp))
}

func parseDiagnosticsLimit(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return defaultDiagnosticsLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > maxDiagnosticsLimit {
		return 0, strconv.ErrSyntax
	}
	return limit, nil
}

func parseOptionalPositiveUint64(raw string) (uint64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	return parsePositiveUint64(raw)
}

func parsePositiveUint64(raw string) (uint64, error) {
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func parseDiagnosticsResult(raw string) (diagnostics.Result, bool) {
	switch diagnostics.Result(strings.TrimSpace(raw)) {
	case "":
		return "", true
	case diagnostics.ResultOK:
		return diagnostics.ResultOK, true
	case diagnostics.ResultError:
		return diagnostics.ResultError, true
	case diagnostics.ResultTimeout:
		return diagnostics.ResultTimeout, true
	case diagnostics.ResultCanceled:
		return diagnostics.ResultCanceled, true
	case diagnostics.ResultPartial:
		return diagnostics.ResultPartial, true
	case diagnostics.ResultDropped:
		return diagnostics.ResultDropped, true
	case diagnostics.ResultSkipped:
		return diagnostics.ResultSkipped, true
	default:
		return "", false
	}
}

func diagnosticsResponseDTO(resp managementusecase.DiagnosticsQueryResponse) DiagnosticsResponse {
	return DiagnosticsResponse{
		Scope:       resp.Scope,
		Status:      string(resp.Status),
		GeneratedAt: resp.GeneratedAt,
		Query:       diagnosticsQueryDTO(resp.Query),
		Summary:     diagnosticsSummaryDTO(resp.Summary),
		Nodes:       diagnosticsNodesDTO(resp.Nodes),
		Events:      diagnosticsEventsDTO(resp.Events),
		Notes:       diagnosticsStringSliceDTO(resp.Notes),
	}
}

func diagnosticsQueryDTO(query diagnostics.Query) DiagnosticsQueryDTO {
	return DiagnosticsQueryDTO{
		TraceID:     query.TraceID,
		ClientMsgNo: query.ClientMsgNo,
		ChannelKey:  query.ChannelKey,
		UID:         query.UID,
		MessageSeq:  query.MessageSeq,
		Stage:       string(query.Stage),
		Result:      string(query.Result),
		Limit:       query.Limit,
	}
}

func diagnosticsSummaryDTO(summary managementusecase.DiagnosticsSummary) DiagnosticsSummaryDTO {
	return DiagnosticsSummaryDTO{
		FirstFailureStage:     summary.FirstFailureStage,
		FirstFailureResult:    summary.FirstFailureResult,
		FirstFailureErrorCode: summary.FirstFailureErrorCode,
		SlowestStage:          summary.SlowestStage,
		SlowestDurationMS:     summary.SlowestDurationMS,
		InvolvedNodes:         diagnosticsUint64SliceDTO(summary.InvolvedNodes),
		PeerNodes:             diagnosticsUint64SliceDTO(summary.PeerNodes),
		SlotID:                summary.SlotID,
		ChannelKey:            summary.ChannelKey,
		ClientMsgNo:           summary.ClientMsgNo,
		MessageSeq:            summary.MessageSeq,
		EventCount:            summary.EventCount,
	}
}

func diagnosticsNodesDTO(nodes []managementusecase.DiagnosticsNodeResult) []DiagnosticsNodeDTO {
	out := make([]DiagnosticsNodeDTO, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, DiagnosticsNodeDTO{
			NodeID:     node.NodeID,
			Status:     node.Status,
			DurationMS: node.DurationMS,
			EventCount: node.EventCount,
			Notes:      diagnosticsStringSliceDTO(node.Notes),
		})
	}
	return out
}

func diagnosticsEventsDTO(events []managementusecase.DiagnosticsEvent) []DiagnosticsEventDTO {
	out := make([]DiagnosticsEventDTO, 0, len(events))
	for _, event := range events {
		out = append(out, DiagnosticsEventDTO{
			TraceID:      event.TraceID,
			SpanID:       event.SpanID,
			ParentSpanID: event.ParentSpanID,
			Stage:        event.Stage,
			At:           event.At,
			DurationMS:   event.DurationMS,
			NodeID:       event.NodeID,
			PeerNodeID:   event.PeerNodeID,
			SlotID:       event.SlotID,
			ChannelKey:   event.ChannelKey,
			ClientMsgNo:  event.ClientMsgNo,
			MessageSeq:   event.MessageSeq,
			RangeStart:   event.RangeStart,
			RangeEnd:     event.RangeEnd,
			Service:      event.Service,
			Result:       event.Result,
			ErrorCode:    event.ErrorCode,
			Error:        event.Error,
			Attempt:      event.Attempt,
			RequestCount: event.RequestCount,
			RecordCount:  event.RecordCount,
			ByteCount:    event.ByteCount,
			QueueDepth:   event.QueueDepth,
			ReplicaRole:  event.ReplicaRole,
			SampleReason: event.SampleReason,
		})
	}
	return out
}

func diagnosticsStringSliceDTO(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return append([]string(nil), values...)
}

func diagnosticsUint64SliceDTO(values []uint64) []uint64 {
	if len(values) == 0 {
		return []uint64{}
	}
	return append([]uint64(nil), values...)
}
