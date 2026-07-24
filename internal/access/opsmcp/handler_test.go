package opsmcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	opscontract "github.com/WuKongIM/WuKongIM/internal/contracts/opsmcp"
	observe "github.com/WuKongIM/WuKongIM/internal/usecase/opsobserve"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestHandlerRequiresMCPBearerAndRejectsCrossOrigin(t *testing.T) {
	handler := mustHandler(t, verifierStub{token: "wko_test_secret"})
	for _, tt := range []struct {
		name   string
		auth   string
		origin string
		status int
	}{
		{name: "missing", status: http.StatusUnauthorized},
		{name: "manager jwt", auth: "Bearer manager.jwt.value", status: http.StatusUnauthorized},
		{name: "cross origin", auth: "Bearer wko_test_secret", origin: "https://manager.example", status: http.StatusForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			req.Header.Set("Authorization", tt.auth)
			req.Header.Set("Origin", tt.origin)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.status {
				t.Fatalf("status = %d body=%s, want %d", rec.Code, rec.Body.String(), tt.status)
			}
			if rec.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatalf("MCP emitted permissive CORS header: %#v", rec.Header())
			}
		})
	}
}

func TestHandlerForwardsTokenFreeRequestAndOwnerRevalidates(t *testing.T) {
	const token = "wko_test_secret"
	ownerVerifier := &forwardVerifierStub{verifierStub: verifierStub{token: token}}
	owner, err := NewEndpoint(Config{
		Verifier:    ownerVerifier,
		Service:     mustObservationService(t),
		LocalNodeID: 2,
	})
	if err != nil {
		t.Fatalf("NewEndpoint(owner) error = %v", err)
	}
	forwarder := &forwarderStub{owner: owner}
	ingress, err := NewEndpoint(Config{
		Verifier: verifierStub{
			token:     token,
			principal: Principal{CredentialID: "test", Revision: 8, OwnerNodeID: 2, DigestSHA256: "digest"},
		},
		Service: mustObservationService(t), LocalNodeID: 1, Forwarder: forwarder,
	})
	if err != nil {
		t.Fatalf("NewEndpoint(ingress) error = %v", err)
	}
	server := httptest.NewServer(ingress)
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             server.URL,
		HTTPClient:           &http.Client{Transport: bearerTransport{token: token, base: http.DefaultTransport}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer session.Close()
	if _, err := session.ListTools(context.Background(), nil); err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if forwarder.calls == 0 || ownerVerifier.forwardCalls == 0 {
		t.Fatalf("forward calls=%d verify calls=%d", forwarder.calls, ownerVerifier.forwardCalls)
	}
	if forwarder.last.CredentialID != "test" || forwarder.last.ExpectedRevision != 8 || forwarder.last.DigestSHA256 != "digest" {
		t.Fatalf("forward request = %#v", forwarder.last)
	}
	if string(forwarder.last.Payload) == token {
		t.Fatal("raw bearer token crossed owner RPC")
	}
}

func TestThreeManagerIngressesShareTokenAndResumeAfterOwnerRestart(t *testing.T) {
	const token = "wko_test_secret"
	newOwner := func() *Endpoint {
		endpoint, err := NewEndpoint(Config{
			Verifier: &forwardVerifierStub{verifierStub: verifierStub{token: token}, ownerNodeID: 1},
			Service:  mustObservationService(t), LocalNodeID: 1,
		})
		if err != nil {
			t.Fatalf("NewEndpoint(owner) error = %v", err)
		}
		return endpoint
	}
	forwarder := &forwarderStub{owner: newOwner(), ownerNodeID: 1}
	for _, ingressNodeID := range []uint64{2, 3} {
		ingress, err := NewEndpoint(Config{
			Verifier: verifierStub{
				token: token,
				principal: Principal{
					CredentialID: "test", Revision: 8, OwnerNodeID: 1, DigestSHA256: "digest",
				},
			},
			Service: mustObservationService(t), LocalNodeID: ingressNodeID, Forwarder: forwarder,
		})
		if err != nil {
			t.Fatalf("NewEndpoint(ingress %d) error = %v", ingressNodeID, err)
		}
		callClusterHealthThroughHTTP(t, ingress, token)
	}

	forwarder.owner = newOwner()
	ingress, err := NewEndpoint(Config{
		Verifier: verifierStub{
			token: token,
			principal: Principal{
				CredentialID: "test", Revision: 8, OwnerNodeID: 1, DigestSHA256: "digest",
			},
		},
		Service: mustObservationService(t), LocalNodeID: 3, Forwarder: forwarder,
	})
	if err != nil {
		t.Fatalf("NewEndpoint(restart ingress) error = %v", err)
	}
	callClusterHealthThroughHTTP(t, ingress, token)
}

func callClusterHealthThroughHTTP(t *testing.T, handler http.Handler, token string) {
	t.Helper()
	server := httptest.NewServer(handler)
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: server.URL,
		HTTPClient: &http.Client{Transport: bearerTransport{
			token: token, base: http.DefaultTransport,
		}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer session.Close()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "cluster_health", Arguments: map[string]any{},
	})
	if err != nil || result.IsError {
		t.Fatalf("CallTool() result=%#v error=%v", result, err)
	}
}

func TestHandlerUsesServerGeneratedAuditRequestIDAndBoundsForwardHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("X-Request-Id", "wko_secret_must_not_be_audited")
	got := requestID(req)
	if got == req.Header.Get("X-Request-Id") || len(got) != 32 {
		t.Fatalf("requestID() = %q, want server-generated 128-bit hex", got)
	}
	if validForwardHeaderValues("application/json", strings.Repeat("a", maxForwardAcceptBytes+1), "") {
		t.Fatal("oversized forwarded Accept header was accepted")
	}
}

func TestHandlerMapsDisabledAndOwnerUnavailable(t *testing.T) {
	for _, tt := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "disabled", err: ErrDisabled, status: http.StatusServiceUnavailable, code: "mcp_disabled"},
		{name: "owner unavailable", err: ErrOwnerUnavailable, status: http.StatusServiceUnavailable, code: "mcp_owner_unavailable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handler := mustHandler(t, verifierStub{token: "wko_test_secret", err: tt.err})
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			req.Header.Set("Authorization", "Bearer wko_test_secret")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			var body httpError
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error = %v body=%s", err, rec.Body.String())
			}
			if rec.Code != tt.status || body.Code != tt.code {
				t.Fatalf("status=%d body=%#v", rec.Code, body)
			}
		})
	}
}

func TestHandlerListsExactlyFrozenV1ToolsAndCallsClusterHealth(t *testing.T) {
	token := "wko_test_secret"
	handler := mustHandler(t, verifierStub{token: token})
	server := httptest.NewServer(handler)
	defer server.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: server.URL,
		HTTPClient: &http.Client{Transport: bearerTransport{
			token: token, base: http.DefaultTransport,
		}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer session.Close()
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	got := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		got = append(got, tool.Name)
		if tool.Name != "pprof_analyze" && (tool.Annotations == nil || !tool.Annotations.ReadOnlyHint) {
			t.Fatalf("tool %s is not annotated read-only", tool.Name)
		}
		if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
			t.Fatalf("tool %s is not annotated non-destructive", tool.Name)
		}
		if tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Fatalf("tool %s is not closed-world", tool.Name)
		}
	}
	sort.Strings(got)
	want := ToolNames()
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("tools = %v, want %v", got, want)
		}
	}
	called, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "cluster_health", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if called.IsError {
		t.Fatalf("CallTool() content = %#v", called.Content)
	}
	content, ok := called.StructuredContent.(map[string]any)
	if !ok || content["schema"] != observe.ObservationSchema || content["cluster_id"] != "cluster-a" {
		t.Fatalf("structured content = %#v", called.StructuredContent)
	}
}

func TestHandlerReturnsBusinessValidationAsInvalidParams(t *testing.T) {
	token := "wko_test_secret"
	server := httptest.NewServer(mustHandler(t, verifierStub{token: token}))
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: server.URL,
		HTTPClient: &http.Client{Transport: bearerTransport{
			token: token, base: http.DefaultTransport,
		}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer session.Close()
	_, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "node_inspect", Arguments: map[string]any{"node_id": 0},
	})
	var rpcError *jsonrpc.Error
	if !errors.As(err, &rpcError) || rpcError.Code != jsonrpc.CodeInvalidParams {
		t.Fatalf("CallTool() error = %#v, want invalid params", err)
	}
	var envelope toolErrorEnvelope
	if unmarshalErr := json.Unmarshal(rpcError.Data, &envelope); unmarshalErr != nil ||
		envelope.Code != "invalid_tool_input" || envelope.Retryable {
		t.Fatalf("error data = %s unmarshal error = %v", rpcError.Data, unmarshalErr)
	}
}

func TestHandlerRejectsUnknownToolArguments(t *testing.T) {
	token := "wko_test_secret"
	server := httptest.NewServer(mustHandler(t, verifierStub{token: token}))
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: server.URL,
		HTTPClient: &http.Client{Transport: bearerTransport{
			token: token, base: http.DefaultTransport,
		}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer session.Close()

	_, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "node_inspect", Arguments: map[string]any{"node_id": 1, "command": "restart"},
	})
	var rpcError *jsonrpc.Error
	if !errors.As(err, &rpcError) || rpcError.Code != jsonrpc.CodeInvalidParams {
		t.Fatalf("CallTool() error = %#v, want closed-world invalid params", err)
	}
}

func mustHandler(t *testing.T, verifier Verifier) http.Handler {
	t.Helper()
	service := mustObservationService(t)
	handler, err := NewHandler(Config{Verifier: verifier, Service: service})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler
}

func mustObservationService(t *testing.T) *observe.Service {
	t.Helper()
	service, err := observe.New(observe.Config{ClusterID: "cluster-a", Source: observationSourceStub{}})
	if err != nil {
		t.Fatalf("observe.New() error = %v", err)
	}
	return service
}

type verifierStub struct {
	token     string
	err       error
	principal Principal
}

func (v verifierStub) VerifyBearer(_ context.Context, token string) (Principal, error) {
	if v.err != nil {
		return Principal{}, v.err
	}
	if token != v.token {
		return Principal{}, ErrUnauthorized
	}
	if v.principal.CredentialID != "" {
		return v.principal, nil
	}
	return Principal{CredentialID: "test", Revision: 7}, nil
}

type forwardVerifierStub struct {
	verifierStub
	forwardCalls int
	ownerNodeID  uint64
}

func (v *forwardVerifierStub) VerifyForward(_ context.Context, credentialID, digest string, revision uint64) (Principal, error) {
	v.forwardCalls++
	if credentialID != "test" || digest != "digest" || revision != 8 {
		return Principal{}, ErrUnauthorized
	}
	ownerNodeID := v.ownerNodeID
	if ownerNodeID == 0 {
		ownerNodeID = 2
	}
	return Principal{CredentialID: credentialID, Revision: revision, OwnerNodeID: ownerNodeID, DigestSHA256: digest}, nil
}

type forwarderStub struct {
	owner       *Endpoint
	ownerNodeID uint64
	calls       int
	last        opscontract.ForwardRequest
}

func (f *forwarderStub) ForwardOpsMCP(ctx context.Context, ownerNodeID uint64, request opscontract.ForwardRequest) (opscontract.ForwardResponse, error) {
	f.calls++
	f.last = request
	expectedOwnerNodeID := f.ownerNodeID
	if expectedOwnerNodeID == 0 {
		expectedOwnerNodeID = 2
	}
	if ownerNodeID != expectedOwnerNodeID {
		return opscontract.ForwardResponse{}, ErrOwnerUnavailable
	}
	return f.owner.ExecuteForward(ctx, request)
}

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
}

type observationSourceStub struct{}

func (observationSourceStub) ClusterHealth(context.Context, observe.ClusterHealthRequest) (observe.SourceResult, error) {
	return observe.SourceResult{Freshness: observe.FreshnessFresh, Completeness: observe.CompletenessComplete, Status: observe.StatusHealthy, Data: map[string]any{"nodes": 3}}, nil
}
func (observationSourceStub) NodeInspect(context.Context, observe.NodeInspectRequest) (observe.SourceResult, error) {
	return observe.SourceResult{}, nil
}
func (observationSourceStub) SlotInspect(context.Context, observe.SlotInspectRequest) (observe.SourceResult, error) {
	return observe.SourceResult{}, nil
}
func (observationSourceStub) ChannelRuntimeInspect(context.Context, observe.ChannelRuntimeInspectRequest) (observe.SourceResult, error) {
	return observe.SourceResult{}, nil
}
func (observationSourceStub) ControllerTasksQuery(context.Context, observe.ControllerTasksQueryRequest) (observe.SourceResult, error) {
	return observe.SourceResult{}, nil
}
func (observationSourceStub) MetricsQueryRange(context.Context, observe.MetricsQueryRangeRequest) (observe.SourceResult, error) {
	return observe.SourceResult{}, nil
}
func (observationSourceStub) LogsSearch(context.Context, observe.LogsSearchRequest) (observe.SourceResult, error) {
	return observe.SourceResult{}, nil
}
func (observationSourceStub) LogsContext(context.Context, observe.LogsContextRequest) (observe.SourceResult, error) {
	return observe.SourceResult{}, nil
}
func (observationSourceStub) DiagnosticsQuery(context.Context, observe.DiagnosticsQueryRequest) (observe.SourceResult, error) {
	return observe.SourceResult{}, nil
}
func (observationSourceStub) ConfigReadRedacted(context.Context, observe.ConfigReadRedactedRequest) (observe.SourceResult, error) {
	return observe.SourceResult{}, nil
}
func (observationSourceStub) BackupInspect(context.Context, observe.BackupInspectRequest) (observe.SourceResult, error) {
	return observe.SourceResult{}, nil
}
func (observationSourceStub) PprofAnalyze(context.Context, observe.PprofAnalyzeRequest) (observe.SourceResult, error) {
	return observe.SourceResult{}, errors.New("not wired in handler test")
}

var _ observe.Source = observationSourceStub{}
