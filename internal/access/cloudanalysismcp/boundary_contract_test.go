package cloudanalysismcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	analysis "github.com/WuKongIM/WuKongIM/internal/usecase/cloudanalysis"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRegisteredToolsDelegateEveryClosedWorldOperationInMemory(t *testing.T) {
	t.Parallel()

	service := newAnalysisServiceForAccessTest(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "v1"}, nil)
	registerTools(server, service)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect(): %v", err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v1"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect(): %v", err)
	}
	defer clientSession.Close()

	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		args map[string]any
	}{
		{name: "run_inspect", args: map[string]any{"run_id": "run-1"}},
		{name: "cluster_snapshot", args: map[string]any{"run_id": "run-1"}},
		{name: "workload_inspect", args: map[string]any{"run_id": "run-1"}},
		{name: "metrics_query_range", args: map[string]any{
			"run_id": "run-1", "query_id": "send_rate", "start": now.Add(-time.Minute).Format(time.RFC3339),
			"end": now.Format(time.RFC3339), "step_seconds": 10,
		}},
		{name: "logs_search", args: map[string]any{"run_id": "run-1", "node_id": 1, "source": "app", "limit": 1}},
		{name: "logs_context", args: map[string]any{"run_id": "run-1", "node_id": 1, "source": "app", "cursor": "opaque", "before": 1}},
		{name: "diagnostics_query", args: map[string]any{"run_id": "run-1", "node_id": 1, "limit": 1}},
		{name: "task_audits_query", args: map[string]any{"run_id": "run-1", "node_id": 1, "limit": 1}},
		{name: "trace_query", args: map[string]any{"run_id": "run-1", "trace_id": "trace-1", "limit": 1}},
		{name: "profile_capture", args: map[string]any{"run_id": "run-1", "node_id": 1, "kind": "heap"}},
		{name: "profile_top", args: map[string]any{"run_id": "run-1", "profile_id": "profile-1", "limit": 1}},
		{name: "profile_list", args: map[string]any{"run_id": "run-1", "node_id": 1, "limit": 1}},
		{name: "config_read_redacted", args: map[string]any{"run_id": "run-1", "node_id": 1}},
		{name: "trace_start", args: map[string]any{"run_id": "run-1", "node_id": 1, "target": "sender_uid", "uid": "u1", "ttl_seconds": 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: test.name, Arguments: test.args})
			if err != nil {
				t.Fatalf("CallTool(%s): %v", test.name, err)
			}
			if result.IsError {
				t.Fatalf("CallTool(%s) returned tool error: %#v", test.name, result.Content)
			}
		})
	}

	for _, test := range []struct {
		name string
		args map[string]any
	}{
		{name: "metrics_query_range", args: map[string]any{"run_id": "run-1", "query_id": "send_rate", "start": "not-time", "end": "not-time", "step_seconds": 0}},
		{name: "trace_start", args: map[string]any{"run_id": "run-1", "node_id": 1, "target": "sender_uid", "uid": "u1", "ttl_seconds": 0}},
	} {
		result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: test.name, Arguments: test.args})
		if err != nil {
			t.Fatalf("CallTool(%s invalid): %v", test.name, err)
		}
		if !result.IsError {
			t.Fatalf("CallTool(%s invalid) IsError = false", test.name)
		}
	}
}

func TestHandlerValidatesDynamicRunScopedTokensWithoutHTTPListener(t *testing.T) {
	t.Parallel()

	service := newAnalysisServiceForAccessTest(t)
	now := time.Now()
	for _, test := range []struct {
		name       string
		expiresAt  time.Time
		verifyErr  error
		wantStatus int
	}{
		{name: "valid", expiresAt: now.Add(time.Hour), wantStatus: http.StatusUnsupportedMediaType},
		{name: "expired", expiresAt: now.Add(-time.Minute), wantStatus: http.StatusUnauthorized},
		{name: "rejected", expiresAt: now.Add(time.Hour), verifyErr: errors.New("rejected"), wantStatus: http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, err := NewHandler(Config{
				RunID: "run-1", Service: service,
				VerifyToken: func(_ context.Context, token string, _ *http.Request) (time.Time, error) {
					if token != "dynamic-token" {
						t.Fatalf("token = %q", token)
					}
					return test.expiresAt, test.verifyErr
				},
			})
			if err != nil {
				t.Fatalf("NewHandler(): %v", err)
			}
			request := httptest.NewRequest(http.MethodPost, "http://analysis.invalid/mcp", nil)
			request.Header.Set("Authorization", "Bearer dynamic-token")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%q", response.Code, test.wantStatus, response.Body.String())
			}
		})
	}
}

func TestHandlerAndTokenExchangeRejectInvalidComposition(t *testing.T) {
	t.Parallel()

	service := newAnalysisServiceForAccessTest(t)
	for _, cfg := range []Config{
		{},
		{RunID: "run-1", Token: "short", TokenExpiresAt: time.Now().Add(time.Hour), Service: service},
		{RunID: "run-1", Token: "01234567890123456789012345678901", TokenExpiresAt: time.Now().Add(-time.Hour), Service: service},
	} {
		if _, err := NewHandler(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("NewHandler(%+v) error = %v, want ErrInvalidConfig", cfg, err)
		}
	}
	if _, err := NewTokenExchangeHandler(TokenExchangeConfig{}); !errors.Is(err, errInvalidTokenExchange) {
		t.Fatalf("NewTokenExchangeHandler() error = %v", err)
	}

	handler, err := NewTokenExchangeHandler(TokenExchangeConfig{
		Verify: func(context.Context, string) error { return nil },
		Issue:  func() (string, time.Time, error) { return "", time.Time{}, errors.New("not ready") },
	})
	if err != nil {
		t.Fatalf("NewTokenExchangeHandler(): %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/analysis/token", nil)
	request.Header.Set("Authorization", "bearer oidc")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("issue failure status = %d, want 409", response.Code)
	}
}

func TestTypedObservationRejectsCrossToolData(t *testing.T) {
	t.Parallel()

	if _, err := typedObservation[analysis.WorkloadInspection](analysis.Observation{Data: map[string]any{}}); err == nil {
		t.Fatal("typedObservation() error = nil, want cross-tool data rejection")
	}
}

func newAnalysisServiceForAccessTest(t *testing.T) *analysis.Service {
	t.Helper()
	effective := model.Scenario{Version: "wkbench/v1", Run: model.RunConfig{ID: "test", RandomSeed: 42}}
	digest, err := model.DigestScenario(effective)
	if err != nil {
		t.Fatalf("DigestScenario(): %v", err)
	}
	service, err := analysis.New(analysis.Config{
		RunID: "run-1", Nodes: []uint64{1, 2, 3}, MetricQueries: map[string]string{"send_rate": "up"},
	}, mcpSourcesStub{inspection: analysis.RunInspection{
		RunID: "run-1", State: "running", InventoryCount: 12,
		Scenario: analysis.ScenarioInspection{Digest: digest, RandomSeed: 42, HashSlotCount: 256, Effective: &effective},
	}})
	if err != nil {
		t.Fatalf("analysis.New(): %v", err)
	}
	return service
}
