// Package cloudanalysismcp exposes cloudanalysis use cases through a bounded,
// authenticated Streamable HTTP Model Context Protocol endpoint.
package cloudanalysismcp

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	analysis "github.com/WuKongIM/WuKongIM/internal/usecase/cloudanalysis"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const analysisScope = "wukongim:analysis"

// ErrInvalidConfig reports an unsafe MCP gateway configuration.
var ErrInvalidConfig = errors.New("internal/access/cloudanalysismcp: invalid config")

// Config binds one MCP endpoint to an exact Analysis Session.
type Config struct {
	// RunID is the exact Run Identity represented by the gateway.
	RunID string
	// Token is a random run-scoped bearer credential with at least 32 bytes.
	Token string
	// TokenExpiresAt is the non-renewable Analysis Token deadline.
	TokenExpiresAt time.Time
	// Service is the bounded entry-independent analysis usecase.
	Service *analysis.Service
}

// NewHandler registers the strict tool allowlist and returns authenticated HTTP.
func NewHandler(cfg Config) (http.Handler, error) {
	if strings.TrimSpace(cfg.RunID) == "" || len(cfg.Token) < 32 || !cfg.TokenExpiresAt.After(time.Now()) || cfg.Service == nil {
		return nil, ErrInvalidConfig
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "wukongim-cloud-analysis", Version: "v1"}, nil)
	registerTools(server, cfg.Service)
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, SessionTimeout: 10 * time.Minute,
	})
	verifier := func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		if len(token) != len(cfg.Token) || subtle.ConstantTimeCompare([]byte(token), []byte(cfg.Token)) != 1 {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{
			Scopes: []string{analysisScope}, Expiration: cfg.TokenExpiresAt,
			UserID: cfg.RunID, Extra: map[string]any{"run_id": cfg.RunID},
		}, nil
	}
	authenticated := auth.RequireBearerToken(verifier, &auth.RequireBearerTokenOptions{Scopes: []string{analysisScope}})(streamable)
	return http.NewCrossOriginProtection().Handler(authenticated), nil
}

type metricsQueryRangeInput struct {
	RunID       string `json:"run_id" jsonschema:"exact Simulation Run identity"`
	QueryID     string `json:"query_id" jsonschema:"server allowlisted metric query identifier"`
	Start       string `json:"start" jsonschema:"inclusive RFC3339 start time"`
	End         string `json:"end" jsonschema:"inclusive RFC3339 end time"`
	StepSeconds int    `json:"step_seconds" jsonschema:"sample resolution in seconds"`
}

type traceStartInput struct {
	RunID       string `json:"run_id" jsonschema:"exact Simulation Run identity"`
	NodeID      uint64 `json:"node_id" jsonschema:"one allowlisted cluster node"`
	Target      string `json:"target" jsonschema:"sender_uid or channel"`
	UID         string `json:"uid,omitempty" jsonschema:"sender UID when target is sender_uid"`
	ChannelID   string `json:"channel_id,omitempty" jsonschema:"channel ID when target is channel"`
	ChannelType uint8  `json:"channel_type,omitempty" jsonschema:"positive channel type when target is channel"`
	TTLSeconds  int    `json:"ttl_seconds" jsonschema:"tracking lifetime from 1 through 900 seconds"`
}

func registerTools(server *mcp.Server, service *analysis.Service) {
	readOnly := toolAnnotations(true)
	active := toolAnnotations(false)

	mcp.AddTool(server, &mcp.Tool{Name: "run_inspect", Description: "Prove the exact run identity and current live or released inventory state.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input analysis.RunRequest) (*mcp.CallToolResult, analysis.Observation, error) {
			output, err := service.RunInspect(ctx, input)
			return nil, output, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "cluster_snapshot", Description: "Read a bounded aggregate cluster node and workqueue snapshot.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input analysis.RunRequest) (*mcp.CallToolResult, analysis.Observation, error) {
			output, err := service.ClusterSnapshot(ctx, input)
			return nil, output, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "metrics_query_range", Description: "Query one server-allowlisted Prometheus signal over a bounded time range.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input metricsQueryRangeInput) (*mcp.CallToolResult, analysis.Observation, error) {
			start, startErr := time.Parse(time.RFC3339, input.Start)
			end, endErr := time.Parse(time.RFC3339, input.End)
			if startErr != nil || endErr != nil || input.StepSeconds <= 0 {
				return nil, analysis.Observation{}, analysis.ErrInvalidToolInput
			}
			output, err := service.MetricsQueryRange(ctx, analysis.MetricsQueryRangeRequest{
				RunID: input.RunID, QueryID: input.QueryID, Start: start, End: end, Step: time.Duration(input.StepSeconds) * time.Second,
			})
			return nil, output, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "logs_search", Description: "Search bounded ordinary application logs on one allowlisted cluster node.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input analysis.LogsSearchRequest) (*mcp.CallToolResult, analysis.Observation, error) {
			output, err := service.LogsSearch(ctx, input)
			return nil, output, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "logs_context", Description: "Read one bounded page around an opaque application-log cursor.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input analysis.LogsContextRequest) (*mcp.CallToolResult, analysis.Observation, error) {
			output, err := service.LogsContext(ctx, input)
			return nil, output, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "diagnostics_query", Description: "Query bounded retained WuKongIM diagnostic events.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input analysis.DiagnosticsQueryRequest) (*mcp.CallToolResult, analysis.Observation, error) {
			output, err := service.DiagnosticsQuery(ctx, input)
			return nil, output, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "task_audits_query", Description: "Query bounded retained Controller task audit histories.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input analysis.TaskAuditsQueryRequest) (*mcp.CallToolResult, analysis.Observation, error) {
			output, err := service.TaskAuditsQuery(ctx, input)
			return nil, output, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "trace_start", Description: "Install one expiring, one-node diagnostics tracking rule within the session budget.", Annotations: active},
		func(ctx context.Context, _ *mcp.CallToolRequest, input traceStartInput) (*mcp.CallToolResult, analysis.Observation, error) {
			if input.TTLSeconds <= 0 {
				return nil, analysis.Observation{}, analysis.ErrInvalidToolInput
			}
			output, err := service.TraceStart(ctx, analysis.TraceStartRequest{
				RunID: input.RunID, NodeID: input.NodeID, Target: input.Target, UID: input.UID,
				ChannelID: input.ChannelID, ChannelType: input.ChannelType, TTL: time.Duration(input.TTLSeconds) * time.Second,
			})
			return nil, output, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "trace_query", Description: "Read bounded retained events for one exact diagnostics trace.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input analysis.TraceQueryRequest) (*mcp.CallToolResult, analysis.Observation, error) {
			output, err := service.TraceQuery(ctx, input)
			return nil, output, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "profile_capture", Description: "Capture one bounded CPU, heap, or goroutine profile on one allowlisted node.", Annotations: active},
		func(ctx context.Context, _ *mcp.CallToolRequest, input analysis.ProfileCaptureRequest) (*mcp.CallToolResult, analysis.Observation, error) {
			output, err := service.ProfileCapture(ctx, input)
			return nil, output, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "profile_top", Description: "Summarize one gateway-owned profile as bounded symbolized top rows.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input analysis.ProfileTopRequest) (*mcp.CallToolResult, analysis.Observation, error) {
			output, err := service.ProfileTop(ctx, input)
			return nil, output, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "profile_list", Description: "List bounded metadata for gateway-owned profile captures.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input analysis.ProfileListRequest) (*mcp.CallToolResult, analysis.Observation, error) {
			output, err := service.ProfileList(ctx, input)
			return nil, output, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "config_read_redacted", Description: "Read one node's allowlisted and already-redacted effective configuration.", Annotations: readOnly},
		func(ctx context.Context, _ *mcp.CallToolRequest, input analysis.ConfigReadRequest) (*mcp.CallToolResult, analysis.Observation, error) {
			output, err := service.ConfigReadRedacted(ctx, input)
			return nil, output, err
		})
}

func toolAnnotations(readOnly bool) *mcp.ToolAnnotations {
	closedWorld := false
	destructive := false
	return &mcp.ToolAnnotations{
		ReadOnlyHint: readOnly, DestructiveHint: &destructive, OpenWorldHint: &closedWorld,
	}
}
