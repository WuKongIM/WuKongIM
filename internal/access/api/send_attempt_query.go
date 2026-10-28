package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
	"github.com/gin-gonic/gin"
)

// SendAttemptReader reads only this node's independently enabled summaries.
type SendAttemptReader interface {
	QuerySendAttempts(context.Context, []sendtrace.AttemptKey) diagnostics.AttemptQueryResult
}

// RPCSelector is an exact node-local diagnostic identity, never a remote call.
type RPCSelector struct {
	TraceID   string        `json:"trace_id"`
	Side      rpctrace.Side `json:"side"`
	ServiceID uint16        `json:"service_id"`
}

// RPCSelectorResult retains one detached typed sample or a closed unknown state.
type RPCSelectorResult struct {
	Selector RPCSelector      `json:"selector"`
	Status   string           `json:"status"`
	Sample   *rpctrace.Sample `json:"sample,omitempty"`
}

// SendAttemptQueryResponse keeps summary and lower retained sample evidence apart.
type SendAttemptQueryResponse struct {
	Version    uint64                         `json:"version"`
	NodeID     uint64                         `json:"node_id,omitempty"`
	Status     string                         `json:"status"`
	Results    []diagnostics.AttemptQueryItem `json:"results"`
	RPCResults []RPCSelectorResult            `json:"rpc_results,omitempty"`
}
type sendAttemptQueryRequest struct {
	Version      uint64                 `json:"version"`
	Keys         []sendtrace.AttemptKey `json:"keys"`
	RPCSelectors []RPCSelector          `json:"rpc_selectors"`
}

func validRPCSelector(selector RPCSelector) bool {
	return selector.ServiceID != 0 && (selector.Side == rpctrace.SideClient || selector.Side == rpctrace.SideServer) && (sendtrace.RPCReference{State: "bound", TraceID: selector.TraceID}).Valid()
}
func selectExactRPCSample(selector RPCSelector, result diagnostics.QueryResult) RPCSelectorResult {
	out := RPCSelectorResult{Selector: selector, Status: "not_retained"}
	if !validRPCSelector(selector) {
		out.Status = "unknown"
		return out
	}
	if len(result.Events) == 0 {
		return out
	}
	out.Status = "unknown"
	stage := diagnostics.StageRPCServer
	if selector.Side == rpctrace.SideClient {
		stage = diagnostics.StageRPCClient
	}
	matches := 0
	for _, event := range result.Events {
		sample := event.RPC
		if event.TraceID != selector.TraceID || event.Stage != stage || sample == nil || sample.TraceID != selector.TraceID || sample.Side != selector.Side || sample.ServiceID != selector.ServiceID || sample.Unbound || len(sample.Spans) > rpctrace.MaxSpans || (sample.CommitCohorts != nil && len(sample.CommitCohorts.Links) > rpctrace.MaxCommitCohortLinks) {
			continue
		}
		matches++
		if matches > 1 {
			out.Status = "ambiguous"
			out.Sample = nil
			return out
		}
		copy := sample.Clone()
		out.Sample = &copy
	}
	if matches == 1 {
		out.Status = "retained"
	}
	return out
}
func (s *Server) handleSendAttemptQuery(c *gin.Context) {
	// Unlike compatibility debug reads, this new surface needs a configured
	// bearer capability even in a controlled environment.
	if s == nil || s.benchToken == "" {
		writeDebugJSONError(c, http.StatusNotFound, "diagnostic query unavailable")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10))
	if err != nil {
		var oversized *http.MaxBytesError
		if errors.As(err, &oversized) {
			writeDebugJSONError(c, http.StatusRequestEntityTooLarge, "diagnostic query too large")
		} else {
			writeDebugJSONError(c, http.StatusBadRequest, "invalid diagnostic query")
		}
		return
	}
	var request sendAttemptQueryRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF || request.Version != 1 || len(request.Keys)+len(request.RPCSelectors) == 0 || len(request.Keys)+len(request.RPCSelectors) > 16 {
		writeDebugJSONError(c, http.StatusBadRequest, "invalid diagnostic query")
		return
	}
	for _, key := range request.Keys {
		if !key.Valid() {
			writeDebugJSONError(c, http.StatusBadRequest, "invalid anonymous attempt selector")
			return
		}
	}
	for _, selector := range request.RPCSelectors {
		if !validRPCSelector(selector) {
			writeDebugJSONError(c, http.StatusBadRequest, "invalid RPC selector")
			return
		}
	}
	response := SendAttemptQueryResponse{Version: 1, Status: "ok", Results: []diagnostics.AttemptQueryItem{}}
	if len(request.Keys) > 0 {
		if s.sendAttempts == nil {
			response.Status = "unsupported"
		} else {
			summary := s.sendAttempts.QuerySendAttempts(c.Request.Context(), request.Keys)
			response.NodeID = summary.NodeID
			response.Status = summary.Status
			response.Results = summary.Results
		}
	}
	for _, selector := range request.RPCSelectors {
		if s.diagnostics == nil {
			response.Status = "unsupported"
			response.RPCResults = append(response.RPCResults, RPCSelectorResult{Selector: selector, Status: "unsupported"})
			continue
		}
		stage := diagnostics.StageRPCServer
		if selector.Side == rpctrace.SideClient {
			stage = diagnostics.StageRPCClient
		}
		result := s.diagnostics.QueryDiagnostics(c.Request.Context(), diagnostics.Query{TraceID: selector.TraceID, Stage: stage, Limit: 500})
		response.RPCResults = append(response.RPCResults, selectExactRPCSample(selector, result))
	}
	maximum := 64 << 10
	if len(request.RPCSelectors) > 0 {
		maximum = 256 << 10
	}
	encoded, err := json.Marshal(response)
	if err != nil || len(encoded)+1 > maximum {
		response.Status = "unknown"
		for i := range response.Results {
			response.Results[i].Status = "unknown"
			response.Results[i].Candidates = []diagnostics.AttemptCandidate{}
		}
		for i := range response.RPCResults {
			response.RPCResults[i].Status = "unknown"
			response.RPCResults[i].Sample = nil
		}
		encoded, _ = json.Marshal(response)
	}
	if len(encoded)+1 > maximum {
		encoded = []byte(`{"version":1,"status":"unknown","results":[]}`)
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", append(encoded, '\n'))
}
