//go:build e2e

package three_node

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

const (
	managerUsername = "ops-admin"
	managerPassword = "ops-secret"
)

type managerLogin struct {
	AccessToken string `json:"access_token"`
}

type mcpStatus struct {
	Revision       uint64 `json:"revision"`
	Enabled        bool   `json:"enabled"`
	ObservedStatus string `json:"observed_status"`
	OwnerNodeID    uint64 `json:"owner_node_id"`
}

type tokenCreate struct {
	CredentialID string `json:"credential_id"`
	Token        string `json:"token"`
	Revision     uint64 `json:"revision"`
}

func TestThreeNodeOperationsMCPIngressProfileAndOwnerRestart(t *testing.T) {
	cluster := suite.New(t).StartThreeNodeCluster(opsMCPOptions()...)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		require.NoError(t, suite.WaitTCPReady(ctx, cluster.MustNode(nodeID).ManagerAddr()), cluster.DumpDiagnostics())
	}

	managerJWT := loginManager(t, cluster.MustNode(1))
	status := readMCPStatus(t, cluster.MustNode(1), managerJWT)
	managerRequest(t, cluster.MustNode(1), managerJWT, http.MethodPut, "/manager/mcp/owner", map[string]any{
		"expected_revision": status.Revision,
		"owner_node_id":     1,
	}, http.StatusAccepted, nil)
	status = readMCPStatus(t, cluster.MustNode(1), managerJWT)
	require.Equal(t, uint64(1), status.OwnerNodeID)

	var created tokenCreate
	managerRequest(t, cluster.MustNode(1), managerJWT, http.MethodPost, "/manager/mcp/tokens", map[string]any{
		"expected_revision": status.Revision,
	}, http.StatusCreated, &created)
	require.NotEmpty(t, created.CredentialID)
	require.NotEmpty(t, created.Token)
	managerRequest(t, cluster.MustNode(1), managerJWT, http.MethodPost, "/manager/mcp/start", map[string]any{
		"expected_revision": created.Revision,
	}, http.StatusAccepted, nil)
	eventuallyMCPReady(t, cluster, managerJWT, created.Revision+1)

	session2 := connectMCP(t, cluster.MustNode(2), created.Token)
	defer session2.Close()
	listed, err := session2.ListTools(context.Background(), nil)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, expectedToolNames(), listedToolNames(listed))
	callTool(t, session2, "cluster_health", map[string]any{})
	callTool(t, session2, "pprof_analyze", map[string]any{
		"node_id": 3,
		"kind":    "goroutine",
		"rows":    10,
	})

	session3 := connectMCP(t, cluster.MustNode(3), created.Token)
	callTool(t, session3, "cluster_health", map[string]any{})
	require.NoError(t, session3.Close())
	requireMCPConnectFails(t, cluster.MustNode(3), "wko_wrong_secret")
	requireMCPConnectFails(t, cluster.MustNode(3), managerJWT)

	require.NoError(t, cluster.MustNode(1).Stop(), cluster.DumpDiagnostics())
	requireMCPOwnerUnavailable(t, cluster.MustNode(2), created.Token)
	require.NoError(t, cluster.StartStoppedNode(1), cluster.DumpDiagnostics())
	restartCtx, restartCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer restartCancel()
	require.NoError(t, cluster.WaitClusterReady(restartCtx), cluster.DumpDiagnostics())
	require.NoError(t, suite.WaitTCPReady(restartCtx, cluster.MustNode(1).ManagerAddr()), cluster.DumpDiagnostics())

	sessionAfterRestart := connectMCP(t, cluster.MustNode(3), created.Token)
	defer sessionAfterRestart.Close()
	callTool(t, sessionAfterRestart, "cluster_health", map[string]any{})
	logResult := callTool(t, sessionAfterRestart, "logs_search", map[string]any{
		"node_id": 2,
		"source":  "app",
		"limit":   10,
	})
	logPayload, err := json.Marshal(logResult.StructuredContent)
	require.NoError(t, err)
	require.Contains(t, string(logPayload), `"content_trust":"untrusted"`)

	status = readMCPStatus(t, cluster.MustNode(2), managerJWT)
	var rotated tokenCreate
	managerRequest(t, cluster.MustNode(2), managerJWT, http.MethodPost, "/manager/mcp/tokens", map[string]any{
		"expected_revision": status.Revision,
	}, http.StatusCreated, &rotated)
	require.NotEmpty(t, rotated.CredentialID)
	require.NotEmpty(t, rotated.Token)
	require.NotEqual(t, created.Token, rotated.Token)
	eventuallyMCPRevision(t, cluster, managerJWT, rotated.Revision)

	rotatedSession := connectMCP(t, cluster.MustNode(3), rotated.Token)
	defer rotatedSession.Close()
	callTool(t, rotatedSession, "cluster_health", map[string]any{})

	managerRequest(
		t,
		cluster.MustNode(3),
		managerJWT,
		http.MethodDelete,
		"/manager/mcp/tokens/"+created.CredentialID,
		map[string]any{"expected_revision": rotated.Revision},
		http.StatusAccepted,
		nil,
	)
	eventuallyMCPRevision(t, cluster, managerJWT, rotated.Revision+1)
	requireMCPConnectFails(t, cluster.MustNode(2), created.Token)
	callTool(t, rotatedSession, "cluster_health", map[string]any{})

	status = readMCPStatus(t, cluster.MustNode(2), managerJWT)
	managerRequest(t, cluster.MustNode(2), managerJWT, http.MethodPost, "/manager/mcp/stop", map[string]any{
		"expected_revision": status.Revision,
	}, http.StatusAccepted, nil)
	eventuallyMCPStopped(t, cluster, managerJWT)
	requireMCPUnavailableCode(t, cluster.MustNode(3), rotated.Token, "mcp_disabled")
}

func opsMCPOptions() []suite.Option {
	overrides := map[string]string{
		"WK_MANAGER_AUTH_ON":    "true",
		"WK_MANAGER_JWT_SECRET": "e2e-ops-mcp-jwt-secret",
		"WK_MANAGER_JWT_ISSUER": "wukongim-e2e",
		"WK_MANAGER_JWT_EXPIRE": "1h",
		"WK_MANAGER_USERS": `[{"username":"ops-admin","password":"ops-secret","permissions":[` +
			`{"resource":"cluster.mcp","actions":["r","w"]}]}]`,
	}
	options := []suite.Option{suite.WithManagerHTTP()}
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		options = append(options, suite.WithNodeConfigOverrides(nodeID, overrides))
	}
	return options
}

func loginManager(t *testing.T, node *suite.StartedNode) string {
	t.Helper()
	var login managerLogin
	managerRequest(t, node, "", http.MethodPost, "/manager/login", map[string]any{
		"username": managerUsername,
		"password": managerPassword,
	}, http.StatusOK, &login)
	require.NotEmpty(t, login.AccessToken)
	return login.AccessToken
}

func readMCPStatus(t *testing.T, node *suite.StartedNode, token string) mcpStatus {
	t.Helper()
	var status mcpStatus
	managerRequest(t, node, token, http.MethodGet, "/manager/mcp", nil, http.StatusOK, &status)
	return status
}

func eventuallyMCPReady(t *testing.T, cluster *suite.StartedCluster, token string, minimumRevision uint64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	last := make(map[uint64]mcpStatus, 3)
	for time.Now().Before(deadline) {
		ready := true
		for nodeID := uint64(1); nodeID <= 3; nodeID++ {
			status := readMCPStatus(t, cluster.MustNode(nodeID), token)
			last[nodeID] = status
			if !status.Enabled || status.ObservedStatus != "ready" || status.OwnerNodeID != 1 ||
				status.Revision < minimumRevision {
				ready = false
			}
		}
		if ready {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("MCP did not become ready: %#v\n%s", last, cluster.DumpDiagnostics())
}

func eventuallyMCPRevision(t *testing.T, cluster *suite.StartedCluster, token string, minimumRevision uint64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	last := make(map[uint64]mcpStatus, 3)
	for time.Now().Before(deadline) {
		converged := true
		for nodeID := uint64(1); nodeID <= 3; nodeID++ {
			status := readMCPStatus(t, cluster.MustNode(nodeID), token)
			last[nodeID] = status
			if status.Revision < minimumRevision {
				converged = false
			}
		}
		if converged {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("MCP revision did not converge to %d: %#v\n%s", minimumRevision, last, cluster.DumpDiagnostics())
}

func eventuallyMCPStopped(t *testing.T, cluster *suite.StartedCluster, token string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	last := make(map[uint64]mcpStatus, 3)
	for time.Now().Before(deadline) {
		stopped := true
		for nodeID := uint64(1); nodeID <= 3; nodeID++ {
			status := readMCPStatus(t, cluster.MustNode(nodeID), token)
			last[nodeID] = status
			if status.Enabled || status.ObservedStatus != "stopped" {
				stopped = false
			}
		}
		if stopped {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("MCP did not stop: %#v\n%s", last, cluster.DumpDiagnostics())
}

func managerRequest(
	t *testing.T,
	node *suite.StartedNode,
	token string,
	method string,
	path string,
	body any,
	wantStatus int,
	out any,
) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(payload)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, "http://"+node.ManagerAddr()+path, reader)
	require.NoError(t, err)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if method != http.MethodGet {
		request.Header.Set("Idempotency-Key", fmt.Sprintf("e2e-%s-%d", path, time.Now().UnixNano()))
	}
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err, node.DumpDiagnostics())
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, wantStatus, response.StatusCode, "body=%s\n%s", payload, node.DumpDiagnostics())
	if out != nil {
		require.NoError(t, json.Unmarshal(payload, out), "body=%s", payload)
	}
}

func connectMCP(t *testing.T, node *suite.StartedNode, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "wukongim-e2e", Version: "v1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: "http://" + node.ManagerAddr() + "/mcp",
		HTTPClient: &http.Client{Transport: bearerRoundTripper{
			token: token,
		}},
		DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err, node.DumpDiagnostics())
	return session
}

func requireMCPConnectFails(t *testing.T, node *suite.StartedNode, token string) {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "wukongim-e2e", Version: "v1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: "http://" + node.ManagerAddr() + "/mcp",
		HTTPClient: &http.Client{Transport: bearerRoundTripper{
			token: token,
		}},
		DisableStandaloneSSE: true,
	}, nil)
	if session != nil {
		_ = session.Close()
	}
	require.Error(t, err, node.DumpDiagnostics())
}

func requireMCPOwnerUnavailable(t *testing.T, node *suite.StartedNode, token string) {
	t.Helper()
	requireMCPUnavailableCode(t, node, token, "mcp_owner_unavailable")
}

func requireMCPUnavailableCode(t *testing.T, node *suite.StartedNode, token, wantCode string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"http://"+node.ManagerAddr()+"/mcp",
		bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"wukongim-e2e","version":"v1"}}}`),
	)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err, node.DumpDiagnostics())
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode, "body=%s", payload)
	var unavailable struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(payload, &unavailable), "body=%s", payload)
	require.Equal(t, wantCode, unavailable.Code, "body=%s", payload)
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	require.NoError(t, err)
	require.False(t, result.IsError, "tool=%s content=%#v", name, result.Content)
	require.NotNil(t, result.StructuredContent)
	return result
}

func listedToolNames(result *mcp.ListToolsResult) []string {
	names := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

func expectedToolNames() []string {
	names := []string{
		"backup_inspect",
		"channel_runtime_inspect",
		"cluster_health",
		"config_read_redacted",
		"controller_tasks_query",
		"diagnostics_query",
		"logs_context",
		"logs_search",
		"metrics_query_range",
		"node_inspect",
		"pprof_analyze",
		"slot_inspect",
	}
	sort.Strings(names)
	return names
}

type bearerRoundTripper struct {
	token string
}

func (t bearerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(clone)
}
