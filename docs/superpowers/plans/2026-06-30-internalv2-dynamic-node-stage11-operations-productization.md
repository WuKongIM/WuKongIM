# InternalV2 Dynamic Node Stage 11 Operations Productization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the validated internalv2 dynamic-node lifecycle into an operator-safe CLI, runbook, and e2ev2 rehearsal gate that can be repeated without guessing.

**Architecture:** Keep ControllerV2 and manager HTTP as the authoritative mutation surface. Add a thin `wkcli node` command family that calls only public manager APIs, add a black-box e2ev2 scenario that drives the real lifecycle through that CLI while traffic is running, and document the exact operational loop and failure evidence. Stage 11 does not start processes, change membership semantics, bypass health gates, or add direct Slot/Controller writes.

**Tech Stack:** Go, Cobra, `net/http`, `encoding/json`, internalv2 manager HTTP, `cmd/wkcli`, e2ev2 real-process harness, Bash readiness gate script, `docs/superpowers` runbooks.

---

## Scope

Stage 11 is the operations-productization layer on top of completed Stages 1-10B.

In scope:

- Add `wkcli node` commands for dynamic-node read, activation, onboarding, scale-in, drain, and final removal after the node has joined through seed discovery.
- Keep the CLI manager-only: it must use `/manager/nodes*` public HTTP endpoints and must not import `internalv2`, `pkg/controllerv2`, `pkg/clusterv2`, or storage internals.
- Add operator-facing output that preserves root-cause evidence: HTTP status, manager error body, `blocked_reasons`, health freshness, control revision, gateway drain counters, task counts, and safety booleans.
- Add a real `test/e2ev2/cluster/dynamic_node_operations` scenario that starts real `cmd/wukongimv2` processes, starts a dynamic node by seed join, and then uses the compiled or `go run` `wkcli node` path to activate, onboard, scale in, drain, and remove while traffic continues.
- Extend `scripts/e2ev2/dynamic-node-readiness-gate.sh` with an operations profile that records CLI/e2ev2 evidence.
- Add a runbook under `docs/superpowers/runbooks/` with exact add-node and remove-node procedures, failure triage, rollback-safe stopping points, and verification commands.
- Update the master dynamic-node lifecycle plan so Stage 11 is visibly sequenced after Stage 10B.

Out of scope:

- Starting, supervising, or terminating `cmd/wukongimv2` server processes from `wkcli node`.
- A public `wkcli node join` command. Dynamic node process startup must use seed discovery; the CLI begins after manager inventory observes `join_state=joining`.
- Any ControllerV2 voter changes.
- Any automatic Slot rebalance beyond existing bounded onboarding and scale-in `slot_replica_move` intents.
- Any compatibility path that mutates cluster state outside manager HTTP.
- Random chaos, soak matrices, CI provider wiring, or Kubernetes/node-pool automation.

## Dependencies And Stage Entry Gate

Stage 11 starts only after Stage 10B is merged and the current readiness gate passes.

Run before starting implementation:

```bash
GOWORK=off go test ./scripts -run DynamicNodeReadinessGate -count=1
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile full
git diff --check
```

Expected:

- script tests pass;
- `--profile full` proves Stage10A gofail recovery and Stage9D real-traffic lifecycle;
- the worktree has no unrelated modifications.

If the full gate fails, use `docs/superpowers/plans/2026-06-30-internalv2-dynamic-node-stage10b-readiness-gate.md` for root-cause debugging before touching Stage 11.

## File Structure

- Create: `cmd/wkcli/internal/nodeops/client.go`
  - Owns manager HTTP request/response plumbing for dynamic-node operations.
  - Accepts one resolved manager server URL, optional bearer token, and per-request timeout.
  - Returns typed results for CLI formatting and typed errors that include HTTP status and response body.

- Create: `cmd/wkcli/internal/nodeops/client_test.go`
  - Uses `httptest.Server` to prove exact methods, paths, JSON bodies, bearer token propagation, status handling, and error-body preservation.

- Create: `cmd/wkcli/internal/nodeops/import_boundary_test.go`
  - Proves the `nodeops` package does not import `internalv2`, `pkg/controllerv2`, `pkg/clusterv2`, or storage internals.

- Create: `cmd/wkcli/internal/nodeops/command.go`
  - Builds the `wkcli node` Cobra command tree.
  - Resolves `--server`, `--context`, and selected current context the same way `top`, `bench`, and `sim` do.
  - Formats human output and JSON output without hiding manager safety evidence.

- Create: `cmd/wkcli/internal/nodeops/command_test.go`
  - Uses fake manager HTTP handlers and isolated context dirs.
  - Proves commands call the intended manager endpoint and print the decisive fields operators need.

- Modify: `cmd/wkcli/cli.go`
  - Registers `nodeopscmd.NewCommand` in `defaultCommandFactories`.

- Modify: `cmd/wkcli/main_test.go`
  - Updates root help expectations so `node` is listed with the existing `context`, `top`, `bench`, and `sim` commands.

- Modify: `cmd/wkcli/README.md`
  - Documents `wkcli node` examples, target resolution, safety model, and exact add/remove loops.

- Create: `test/e2ev2/cluster/dynamic_node_operations/AGENTS.md`
  - Declares black-box scenario rules and serial execution command.

- Create: `test/e2ev2/cluster/dynamic_node_operations/ops_cli_test.go`
  - Starts a real three-node cluster plus seed-join node 4.
  - Drives activation, onboarding, scale-in, drain, and remove through `wkcli node`.
  - Keeps real WKProto traffic moving throughout the operation.

- Modify: `test/e2ev2/cluster/AGENTS.md`
  - Adds `dynamic_node_operations` to the scenario catalog.

- Modify: `scripts/e2ev2/dynamic-node-readiness-gate.sh`
  - Adds an `ops` profile that runs the Stage11 CLI/unit checks and the `dynamic_node_operations` e2ev2 scenario with evidence logs.

- Modify: `scripts/dynamic_node_readiness_gate_script_test.go`
  - Adds dry-run expectations for the `ops` profile and verifies evidence filenames.

- Create: `docs/superpowers/runbooks/internalv2-dynamic-node-operations.md`
  - Operator runbook for add-node, activate/onboard, scale-in/remove, and root-cause triage.

- Modify: `docs/development/PROJECT_KNOWLEDGE.md`
  - Adds one short knowledge note: `wkcli node` is a manager-only operations client; process startup remains outside the CLI.

- Modify: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md`
  - Links Stage 11 and records its exit gate.

## Execution Chain

```text
operator
  -> wkcli node ...
  -> manager HTTP /manager/nodes*
  -> internalv2/access/manager route
  -> internalv2/usecase/management safety and lifecycle usecase
  -> narrow clusterv2/ControllerV2 writer or read port
  -> manager JSON result
  -> wkcli evidence-preserving output
```

The e2ev2 proof must follow this chain:

```text
real cmd/wukongimv2 three-node cluster
  -> real traffic worker
  -> seed-join node 4 process
  -> wkcli node activate/onboarding/scale-in/drain/remove
  -> public manager HTTP only
  -> traffic still progresses
  -> public manager status and metrics explain final state
```

## Cross-Stage Invariants

- Single-node deployments remain single-node clusters; `wkcli node` must not introduce any bypass around cluster semantics.
- Dynamic node startup remains seed-join driven. The CLI operates lifecycle state only after the node appears in manager inventory as `joining`; it must not claim to launch, seed, or register the process itself.
- `join_state=joining` is not schedulable. The CLI must surface the not-schedulable state instead of silently activating.
- Activation must keep using manager readiness gates. The CLI must not retry by calling lower-level control APIs.
- Onboarding and scale-in must stay bounded by `--max-slot-moves`; defaults must be small, with `1` as the command default.
- Remove must remain fail-closed. If `safe_to_remove=false`, the CLI must print blockers and exit non-zero for `remove`.
- Unknown runtime, stale health, stale control revision, active tasks, Slot leadership, Channel inventory uncertainty, and gateway drain counters must be visible in CLI output.
- No high-cardinality metric labels or unbounded scans are added in this stage.

## Task 1: Add The `nodeops` Manager HTTP Client

**Files:**

- Create: `cmd/wkcli/internal/nodeops/client.go`
- Create: `cmd/wkcli/internal/nodeops/client_test.go`
- Create: `cmd/wkcli/internal/nodeops/import_boundary_test.go`

- [ ] **Step 1: Write client route tests first**

Create `cmd/wkcli/internal/nodeops/client_test.go` with tests that prove request paths, JSON bodies, bearer tokens, and response decoding.

```go
package nodeops

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientActivateNodePostsManagerRoute(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		defer r.Body.Close()
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"changed":true,"node_id":4,"addr":"127.0.0.1:11114","join_state":"active","revision":20}`))
	}))
	defer server.Close()

	client := NewClient(Config{Server: server.URL, Token: "secret", Timeout: time.Second})
	out, err := client.ActivateNode(context.Background(), 4)
	if err != nil {
		t.Fatalf("activate node: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/manager/nodes/4/activate" {
		t.Fatalf("unexpected request %s %s", gotMethod, gotPath)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("authorization header = %q", gotAuth)
	}
	if !out.Changed || out.JoinState != "active" || out.Revision != 20 {
		t.Fatalf("unexpected response: %#v", out)
	}
}

func TestClientScaleInStatusPreservesBlockers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/manager/nodes/4/scale-in/status" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{
			"node_id":4,
			"join_state":"leaving",
			"state_revision":88,
			"safe_to_proceed":false,
			"safe_to_remove":false,
			"blocked_by_health":true,
			"blocked_by_runtime_drain":true,
			"blocked_reasons":["target_health_stale","gateway_sessions_present"],
			"health_fresh":false,
			"health_status":"alive",
			"health_freshness":"stale",
			"observed_control_revision":80,
			"required_control_revision":88,
			"gateway_draining":true,
			"accepting_new_sessions":false,
			"gateway_sessions":2,
			"active_online":2
		}`))
	}))
	defer server.Close()

	client := NewClient(Config{Server: server.URL, Timeout: time.Second})
	out, err := client.ScaleInStatus(context.Background(), 4)
	if err != nil {
		t.Fatalf("scale-in status: %v", err)
	}
	if out.SafeToRemove || !out.BlockedByHealth || !out.BlockedByRuntimeDrain {
		t.Fatalf("unexpected safety flags: %#v", out)
	}
	if strings.Join(out.BlockedReasons, ",") != "target_health_stale,gateway_sessions_present" {
		t.Fatalf("blocked reasons not preserved: %#v", out.BlockedReasons)
	}
	if out.ObservedControlRevision != 80 || out.RequiredControlRevision != 88 || out.GatewaySessions != 2 {
		t.Fatalf("missing root-cause fields: %#v", out)
	}
}

func TestClientErrorIncludesHTTPStatusAndBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"conflict","message":"safe_to_remove=false blocked_by_channels=true"}`))
	}))
	defer server.Close()

	client := NewClient(Config{Server: server.URL, Timeout: time.Second})
	_, err := client.RemoveScaleInNode(context.Background(), 4)
	if err == nil {
		t.Fatalf("expected conflict error")
	}
	var apiErr *APIError
	if !AsAPIError(err, &apiErr) {
		t.Fatalf("expected APIError, got %T %v", err, err)
	}
	if apiErr.StatusCode != http.StatusConflict || !strings.Contains(apiErr.Body, "blocked_by_channels") {
		t.Fatalf("missing status/body: %#v", apiErr)
	}
}
```

Create `cmd/wkcli/internal/nodeops/import_boundary_test.go`:

```go
package nodeops

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestNodeOpsDoesNotImportClusterInternals(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", ".")
	cmd.Env = append(cmd.Environ(), "GOWORK=off")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list: %v\n%s", err, stderr.String())
	}
	forbidden := []string{
		"github.com/WuKongIM/WuKongIM/internalv2/",
		"github.com/WuKongIM/WuKongIM/pkg/controllerv2",
		"github.com/WuKongIM/WuKongIM/pkg/clusterv2",
		"github.com/WuKongIM/WuKongIM/pkg/db/",
	}
	for _, line := range strings.Split(stdout.String(), "\n") {
		for _, prefix := range forbidden {
			if strings.HasPrefix(line, prefix) {
				t.Fatalf("nodeops must stay manager-HTTP only; forbidden dependency %s", line)
			}
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
GOWORK=off go test ./cmd/wkcli/internal/nodeops -count=1
```

Expected: package creation is incomplete and tests fail until `client.go` exists.

- [ ] **Step 3: Implement the minimal client**

Create `cmd/wkcli/internal/nodeops/client.go` with these public types and methods:

```go
package nodeops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Server string
	Token string
	Timeout time.Duration
}

type Client struct {
	baseURL string
	token string
	httpClient *http.Client
}

type APIError struct {
	StatusCode int
	Body string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("manager request failed: status=%d body=%s", e.StatusCode, strings.TrimSpace(e.Body))
}

func AsAPIError(err error, target **APIError) bool {
	return errors.As(err, target)
}

type LifecycleResponse struct {
	Changed bool `json:"changed"`
	NodeID uint64 `json:"node_id"`
	Addr string `json:"addr,omitempty"`
	JoinState string `json:"join_state"`
	Revision uint64 `json:"revision"`
}

type MoveRequest struct {
	MaxSlotMoves uint32 `json:"max_slot_moves"`
}

type DrainRequest struct {
	Draining bool `json:"draining"`
}

type NodeScaleInStatus struct {
	NodeID uint64 `json:"node_id"`
	JoinState string `json:"join_state"`
	StateRevision uint64 `json:"state_revision"`
	SafeToProceed bool `json:"safe_to_proceed"`
	SafeToRemove bool `json:"safe_to_remove"`
	BlockedByHealth bool `json:"blocked_by_health"`
	BlockedByStaleRevision bool `json:"blocked_by_stale_revision"`
	BlockedByControlRevision bool `json:"blocked_by_control_revision"`
	BlockedBySlots bool `json:"blocked_by_slots"`
	BlockedBySlotLeadership bool `json:"blocked_by_slot_leadership"`
	BlockedBySlotRuntime bool `json:"blocked_by_slot_runtime"`
	BlockedByTasks bool `json:"blocked_by_tasks"`
	BlockedByChannels bool `json:"blocked_by_channels"`
	BlockedByRuntimeDrain bool `json:"blocked_by_runtime_drain"`
	UnknownRuntime bool `json:"unknown_runtime"`
	RuntimeUnknown bool `json:"runtime_unknown"`
	UnknownControlRevision bool `json:"unknown_control_revision"`
	UnknownChannelInventory bool `json:"unknown_channel_inventory"`
	HealthFresh bool `json:"health_fresh"`
	HealthStatus string `json:"health_status"`
	HealthFreshness string `json:"health_freshness"`
	ObservedControlRevision uint64 `json:"observed_control_revision"`
	RequiredControlRevision uint64 `json:"required_control_revision"`
	BlockedReasons []string `json:"blocked_reasons"`
	SlotReplicaCount int `json:"slot_replica_count"`
	SlotLeaderCount int `json:"slot_leader_count"`
	ActiveTaskCount int `json:"active_task_count"`
	FailedTaskCount int `json:"failed_task_count"`
	ChannelLeaderCount int `json:"channel_leader_count"`
	ChannelReplicaCount int `json:"channel_replica_count"`
	ChannelISRCount int `json:"channel_isr_count"`
	GatewayDraining bool `json:"gateway_draining"`
	AcceptingNewSessions bool `json:"accepting_new_sessions"`
	GatewaySessions int `json:"gateway_sessions"`
	ActiveOnline int `json:"active_online"`
	ClosingOnline int `json:"closing_online"`
	TotalOnline int `json:"total_online"`
	PendingActivations int `json:"pending_activations"`
}
```

The implementation must include:

- `func NewClient(cfg Config) *Client`
- `func (c *Client) ListNodes(ctx context.Context, out any) error`
- `func (c *Client) ActivateNode(ctx context.Context, nodeID uint64) (LifecycleResponse, error)`
- `func (c *Client) OnboardingPlan(ctx context.Context, nodeID uint64, maxSlotMoves uint32, out any) error`
- `func (c *Client) OnboardingStart(ctx context.Context, nodeID uint64, maxSlotMoves uint32, out any) error`
- `func (c *Client) OnboardingAdvance(ctx context.Context, nodeID uint64, maxSlotMoves uint32, out any) error`
- `func (c *Client) OnboardingStatus(ctx context.Context, nodeID uint64, out any) error`
- `func (c *Client) ScaleInStart(ctx context.Context, nodeID uint64) (LifecycleResponse, error)`
- `func (c *Client) ScaleInPlan(ctx context.Context, nodeID uint64, maxSlotMoves uint32, out any) error`
- `func (c *Client) ScaleInAdvance(ctx context.Context, nodeID uint64, maxSlotMoves uint32, out any) error`
- `func (c *Client) SetScaleInDrain(ctx context.Context, nodeID uint64, draining bool, out any) error`
- `func (c *Client) ScaleInStatus(ctx context.Context, nodeID uint64) (NodeScaleInStatus, error)`
- `func (c *Client) RemoveScaleInNode(ctx context.Context, nodeID uint64) (LifecycleResponse, error)`

The shared request helper must:

- trim the configured server URL and reject an empty server before sending;
- preserve absolute `http://` and `https://` manager URLs;
- send `Authorization: Bearer <token>` only when the token is non-empty;
- read at most 1 MiB of response body for errors;
- return `APIError` for non-2xx status with status code and body;
- decode JSON only when the caller provides a non-nil output pointer.

Use this request/helper implementation pattern so every method preserves the manager error body:

```go
func NewClient(cfg Config) *Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(cfg.Server), "/"),
		token: strings.TrimSpace(cfg.Token),
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (c *Client) doJSON(ctx context.Context, method, path string, in any, out any) error {
	if strings.TrimSpace(c.baseURL) == "" {
		return fmt.Errorf("manager server is required")
	}
	base, err := url.Parse(c.baseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return fmt.Errorf("manager server must be an absolute URL: %s", c.baseURL)
	}
	endpoint := *base
	endpoint.Path = strings.TrimRight(base.Path, "/") + path
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return &APIError{StatusCode: resp.StatusCode, Body: string(data)}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func nodePath(nodeID uint64, suffix string) string {
	return "/manager/nodes/" + strconv.FormatUint(nodeID, 10) + suffix
}

func (c *Client) ActivateNode(ctx context.Context, nodeID uint64) (LifecycleResponse, error) {
	var out LifecycleResponse
	err := c.doJSON(ctx, http.MethodPost, nodePath(nodeID, "/activate"), nil, &out)
	return out, err
}

func (c *Client) ScaleInStatus(ctx context.Context, nodeID uint64) (NodeScaleInStatus, error) {
	var out NodeScaleInStatus
	err := c.doJSON(ctx, http.MethodGet, nodePath(nodeID, "/scale-in/status"), nil, &out)
	return out, err
}

func (c *Client) RemoveScaleInNode(ctx context.Context, nodeID uint64) (LifecycleResponse, error) {
	var out LifecycleResponse
	err := c.doJSON(ctx, http.MethodPost, nodePath(nodeID, "/scale-in/remove"), nil, &out)
	return out, err
}
```

Implement the remaining onboarding and scale-in methods with the same path helper:

```go
func (c *Client) ListNodes(ctx context.Context, out any) error {
	return c.doJSON(ctx, http.MethodGet, "/manager/nodes", nil, out)
}

func (c *Client) OnboardingPlan(ctx context.Context, nodeID uint64, maxSlotMoves uint32, out any) error {
	return c.doJSON(ctx, http.MethodPost, nodePath(nodeID, "/onboarding/plan"), MoveRequest{MaxSlotMoves: maxSlotMoves}, out)
}

func (c *Client) OnboardingStart(ctx context.Context, nodeID uint64, maxSlotMoves uint32, out any) error {
	return c.doJSON(ctx, http.MethodPost, nodePath(nodeID, "/onboarding/start"), MoveRequest{MaxSlotMoves: maxSlotMoves}, out)
}

func (c *Client) OnboardingAdvance(ctx context.Context, nodeID uint64, maxSlotMoves uint32, out any) error {
	return c.doJSON(ctx, http.MethodPost, nodePath(nodeID, "/onboarding/advance"), MoveRequest{MaxSlotMoves: maxSlotMoves}, out)
}

func (c *Client) OnboardingStatus(ctx context.Context, nodeID uint64, out any) error {
	return c.doJSON(ctx, http.MethodGet, nodePath(nodeID, "/onboarding/status"), nil, out)
}

func (c *Client) ScaleInStart(ctx context.Context, nodeID uint64) (LifecycleResponse, error) {
	var out LifecycleResponse
	err := c.doJSON(ctx, http.MethodPost, nodePath(nodeID, "/scale-in/start"), nil, &out)
	return out, err
}

func (c *Client) ScaleInPlan(ctx context.Context, nodeID uint64, maxSlotMoves uint32, out any) error {
	return c.doJSON(ctx, http.MethodPost, nodePath(nodeID, "/scale-in/plan"), MoveRequest{MaxSlotMoves: maxSlotMoves}, out)
}

func (c *Client) ScaleInAdvance(ctx context.Context, nodeID uint64, maxSlotMoves uint32, out any) error {
	return c.doJSON(ctx, http.MethodPost, nodePath(nodeID, "/scale-in/advance"), MoveRequest{MaxSlotMoves: maxSlotMoves}, out)
}

func (c *Client) SetScaleInDrain(ctx context.Context, nodeID uint64, draining bool, out any) error {
	return c.doJSON(ctx, http.MethodPost, nodePath(nodeID, "/scale-in/drain"), DrainRequest{Draining: draining}, out)
}
```

- [ ] **Step 4: Run client tests**

Run:

```bash
GOWORK=off go test ./cmd/wkcli/internal/nodeops -count=1
```

Expected: all client route tests pass.

- [ ] **Step 5: Commit Task 1**

```bash
git add cmd/wkcli/internal/nodeops/client.go cmd/wkcli/internal/nodeops/client_test.go cmd/wkcli/internal/nodeops/import_boundary_test.go
git commit -m "feat: add wkcli node manager client"
```

## Task 2: Add `wkcli node` Read-Only Commands

**Files:**

- Create: `cmd/wkcli/internal/nodeops/command.go`
- Create: `cmd/wkcli/internal/nodeops/command_test.go`
- Modify: `cmd/wkcli/cli.go`
- Modify: `cmd/wkcli/main_test.go`

- [ ] **Step 1: Write command tests for root registration and read-only output**

Add these tests:

```go
package nodeops

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
	contextcmd "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/context"
)

func TestNodeListCommandPrintsLifecycleAndHealthEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/manager/nodes" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"items":[{"node_id":4,"name":"node-4","membership":{"join_state":"joining","schedulable":false},"health":{"fresh":true,"freshness":"fresh","status":"alive","runtime_ready":true,"observed_control_revision":7}}],"total":1}`))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{"ls", "--server", server.URL})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, stderr.String())
	}
	for _, want := range []string{"node-4", "join_state=joining", "schedulable=false", "health=fresh/alive", "runtime_ready=true", "control_rev=7"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestNodeScaleInStatusCommandPrintsRootCauseBlockers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/manager/nodes/4/scale-in/status" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"node_id":4,"join_state":"leaving","state_revision":88,"safe_to_proceed":false,"safe_to_remove":false,"blocked_by_health":true,"blocked_by_runtime_drain":true,"blocked_reasons":["target_health_stale","gateway_sessions_present"],"health_freshness":"stale","health_status":"alive","observed_control_revision":80,"required_control_revision":88,"gateway_draining":true,"accepting_new_sessions":false,"gateway_sessions":2,"active_online":2}`))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{"scale-in", "status", "4", "--server", server.URL})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, stderr.String())
	}
	for _, want := range []string{"node=4", "safe_to_remove=false", "target_health_stale", "gateway_sessions_present", "control_rev=80/88", "gateway_sessions=2"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestNodeCommandUsesFirstContextServerAndPrintsEvidence(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[],"total":0}`))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("second server should not be called")
	}))
	defer second.Close()

	contextDir := t.TempDir()
	store := contextcmd.NewStore(contextDir)
	if err := store.Save(contextcmd.Context{Name: "ops", Servers: []string{first.URL, second.URL}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Select("ops"); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr, ContextDir: &contextDir})
	cmd.SetArgs([]string{"ls"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "using first manager server from context ops: "+first.URL) {
		t.Fatalf("missing first-server evidence: %q", stderr.String())
	}
}
```

Modify `cmd/wkcli/main_test.go`:

```go
func TestRootCommandHelpListsPlannedSubcommands(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := runWithIO([]string{"--help"}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("expected help exit code %d, got %d stderr %q", exitOK, code, stderr.String())
	}
	for _, want := range []string{"wkcli", "context", "top", "bench", "sim", "node"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected help to contain %q, got %q", want, stdout.String())
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
GOWORK=off go test ./cmd/wkcli ./cmd/wkcli/internal/nodeops -count=1
```

Expected: tests fail because `nodeops.NewCommand` and root registration are not implemented.

- [ ] **Step 3: Implement target resolution and read-only command tree**

Create `cmd/wkcli/internal/nodeops/command.go` with:

```go
package nodeops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/command"
	contextcmd "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/context"
	"github.com/spf13/cobra"
)

type options struct {
	server string
	contextName string
	token string
	timeout time.Duration
	jsonOutput bool
}

func NewCommand(deps command.Deps) *cobra.Command {
	opts := &options{timeout: 10 * time.Second}
	cmd := &cobra.Command{
		Use: "node",
		Short: "Operate internalv2 dynamic cluster nodes through manager HTTP",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmd.Help(); err != nil {
				return command.Exit{Code: command.ExitInternal, Message: err.Error()}
			}
			return command.Exit{Code: command.ExitConfig}
		},
	}
	cmd.PersistentFlags().StringVar(&opts.server, "server", "", "Manager HTTP server URL")
	cmd.PersistentFlags().StringVar(&opts.contextName, "context", "", "wkcli context name")
	cmd.PersistentFlags().StringVar(&opts.token, "token", "", "Manager bearer token")
	cmd.PersistentFlags().DurationVar(&opts.timeout, "timeout", 10*time.Second, "HTTP request timeout")
	cmd.PersistentFlags().BoolVar(&opts.jsonOutput, "json", false, "Print raw JSON result")
	cmd.AddCommand(newListCommand(deps, opts))
	cmd.AddCommand(newActivateCommand(deps, opts))
	cmd.AddCommand(newOnboardingCommand(deps, opts))
	cmd.AddCommand(newScaleInCommand(deps, opts))
	return cmd
}
```

Implement these helpers in the same file:

- `func resolveServer(deps command.Deps, opts *options) (string, error)`
- `func clientFromOptions(deps command.Deps, opts *options) (*Client, error)`
- `func parseNodeIDArg(args []string) (uint64, error)`
- `func writeJSON(w io.Writer, value any) error`
- `func printNodeList(w io.Writer, resp nodeListResponse)`
- `func printScaleInStatus(w io.Writer, status NodeScaleInStatus)`
- `func writeAPIError(err error) error`

`resolveServer` must use this target order:

1. `--server` when present.
2. `--context NAME` from `cmd/wkcli/internal/context.Store`.
3. selected current context from the same store.

If the selected context contains multiple servers, use the first one for every Stage 11 command and print a stderr note when the command receives `command.Deps.Stderr`; read-only commands use the same first-server rule so the operational surface stays deterministic.

Use these helper bodies for target resolution and error mapping:

```go
func resolveServer(deps command.Deps, opts *options) (string, error) {
	if strings.TrimSpace(opts.server) != "" {
		return strings.TrimRight(strings.TrimSpace(opts.server), "/"), nil
	}
	storeDir := contextcmd.DefaultStoreDir()
	if deps.ContextDir != nil && strings.TrimSpace(*deps.ContextDir) != "" {
		storeDir = strings.TrimSpace(*deps.ContextDir)
	}
	store := contextcmd.NewStore(storeDir)
	name := strings.TrimSpace(opts.contextName)
	if name == "" {
		current, err := store.Current()
		if err != nil {
			return "", err
		}
		name = current
	}
	if name == "" {
		return "", fmt.Errorf("no manager server configured; pass --server, --context, or select a wkcli context")
	}
	saved, err := store.Load(name)
	if err != nil {
		return "", err
	}
	for _, server := range saved.Servers {
		if trimmed := strings.TrimRight(strings.TrimSpace(server), "/"); trimmed != "" {
			if len(saved.Servers) > 1 && deps.Stderr != nil {
				fmt.Fprintf(deps.Stderr, "using first manager server from context %s: %s\n", name, trimmed)
			}
			return trimmed, nil
		}
	}
	return "", fmt.Errorf("context %s has no manager servers", name)
}

func clientFromOptions(deps command.Deps, opts *options) (*Client, error) {
	server, err := resolveServer(deps, opts)
	if err != nil {
		return nil, command.Exit{Code: command.ExitConfig, Message: err.Error()}
	}
	return NewClient(Config{Server: server, Token: opts.token, Timeout: opts.timeout}), nil
}

func parseNodeIDArg(args []string) (uint64, error) {
	if len(args) != 1 {
		return 0, command.Exit{Code: command.ExitConfig, Message: "node id is required"}
	}
	nodeID, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil || nodeID == 0 {
		return 0, command.Exit{Code: command.ExitConfig, Message: "node id must be a positive integer"}
	}
	return nodeID, nil
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func writeAPIError(err error) error {
	var apiErr *APIError
	if AsAPIError(err, &apiErr) {
		return command.Exit{Code: command.ExitUnavailable, Message: apiErr.Error()}
	}
	return command.Exit{Code: command.ExitInternal, Message: err.Error()}
}
```

Add the concrete read-only list DTO, command, and renderer:

```go
type nodeListResponse struct {
	Total int `json:"total"`
	Items []nodeListItem `json:"items"`
}

type nodeListItem struct {
	NodeID uint64 `json:"node_id"`
	Name string `json:"name"`
	Membership struct {
		JoinState string `json:"join_state"`
		Schedulable bool `json:"schedulable"`
	} `json:"membership"`
	Health struct {
		Fresh bool `json:"fresh"`
		Freshness string `json:"freshness"`
		Status string `json:"status"`
		RuntimeReady bool `json:"runtime_ready"`
		ObservedControlRevision uint64 `json:"observed_control_revision"`
	} `json:"health"`
}

func newListCommand(deps command.Deps, opts *options) *cobra.Command {
	return &cobra.Command{
		Use: "ls",
		Aliases: []string{"list"},
		Short: "List manager-observed dynamic node lifecycle and health evidence",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := clientFromOptions(deps, opts)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), opts.timeout)
			defer cancel()
			var out nodeListResponse
			if err := client.ListNodes(ctx, &out); err != nil {
				return writeAPIError(err)
			}
			if opts.jsonOutput {
				return writeJSON(deps.Stdout, out)
			}
			printNodeList(deps.Stdout, out)
			return nil
		},
	}
}

func printNodeList(w io.Writer, resp nodeListResponse) {
	if len(resp.Items) == 0 {
		fmt.Fprintln(w, "no nodes")
		return
	}
	for _, node := range resp.Items {
		name := strings.TrimSpace(node.Name)
		if name == "" {
			name = "node-" + strconv.FormatUint(node.NodeID, 10)
		}
		fmt.Fprintf(w, "%s node=%d join_state=%s schedulable=%t health=%s/%s fresh=%t runtime_ready=%t control_rev=%d\n",
			name,
			node.NodeID,
			node.Membership.JoinState,
			node.Membership.Schedulable,
			node.Health.Freshness,
			node.Health.Status,
			node.Health.Fresh,
			node.Health.RuntimeReady,
			node.Health.ObservedControlRevision,
		)
	}
}
```

`printScaleInStatus` must include at least:

```text
node=4 join_state=leaving state_revision=88
safe_to_proceed=false safe_to_remove=false
blocked_reasons=target_health_stale,gateway_sessions_present
health=stale/alive fresh=false control_rev=80/88
slots replicas=0 leaders=0 tasks active=0 failed=0 channels leader=0 replica=0 isr=0
gateway draining=true accepting_new_sessions=false gateway_sessions=2 active_online=2 closing_online=0 pending_activations=0
```

Use this renderer so the status command exposes the blocker evidence from manager:

```go
func printScaleInStatus(w io.Writer, status NodeScaleInStatus) {
	reasons := strings.Join(status.BlockedReasons, ",")
	if reasons == "" {
		reasons = "-"
	}
	fmt.Fprintf(w, "node=%d join_state=%s state_revision=%d\n", status.NodeID, status.JoinState, status.StateRevision)
	fmt.Fprintf(w, "safe_to_proceed=%t safe_to_remove=%t\n", status.SafeToProceed, status.SafeToRemove)
	fmt.Fprintf(w, "blocked_reasons=%s\n", reasons)
	fmt.Fprintf(w, "health=%s/%s fresh=%t control_rev=%d/%d\n",
		status.HealthFreshness,
		status.HealthStatus,
		status.HealthFresh,
		status.ObservedControlRevision,
		status.RequiredControlRevision,
	)
	fmt.Fprintf(w, "slots replicas=%d leaders=%d tasks active=%d failed=%d channels leader=%d replica=%d isr=%d\n",
		status.SlotReplicaCount,
		status.SlotLeaderCount,
		status.ActiveTaskCount,
		status.FailedTaskCount,
		status.ChannelLeaderCount,
		status.ChannelReplicaCount,
		status.ChannelISRCount,
	)
	fmt.Fprintf(w, "gateway draining=%t accepting_new_sessions=%t gateway_sessions=%d active_online=%d closing_online=%d pending_activations=%d\n",
		status.GatewayDraining,
		status.AcceptingNewSessions,
		status.GatewaySessions,
		status.ActiveOnline,
		status.ClosingOnline,
		status.PendingActivations,
	)
}
```

The status command must call the typed client and renderer directly:

```go
func newScaleInStatusCommand(deps command.Deps, opts *options) *cobra.Command {
	return &cobra.Command{
		Use: "status NODE_ID",
		Short: "Read fail-closed scale-in safety evidence",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeID, err := parseNodeIDArg(args)
			if err != nil {
				return err
			}
			client, err := clientFromOptions(deps, opts)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), opts.timeout)
			defer cancel()
			out, err := client.ScaleInStatus(ctx, nodeID)
			if err != nil {
				return writeAPIError(err)
			}
			if opts.jsonOutput {
				return writeJSON(deps.Stdout, out)
			}
			printScaleInStatus(deps.Stdout, out)
			return nil
		},
	}
}
```

- [ ] **Step 4: Register the command**

Modify `cmd/wkcli/cli.go` imports:

```go
nodeopscmd "github.com/WuKongIM/WuKongIM/cmd/wkcli/internal/nodeops"
```

Modify `defaultCommandFactories`:

```go
func defaultCommandFactories() []command.Factory {
	return []command.Factory{
		contextcmd.NewCommand,
		topcmd.NewCommand,
		benchcmd.NewCommand,
		simcmd.NewCommand,
		nodeopscmd.NewCommand,
	}
}
```

- [ ] **Step 5: Run read-only command tests**

Run:

```bash
GOWORK=off go test ./cmd/wkcli ./cmd/wkcli/internal/nodeops -count=1
```

Expected: root help lists `node`; read-only commands print lifecycle, health, and blocker evidence.

- [ ] **Step 6: Commit Task 2**

```bash
git add cmd/wkcli/cli.go cmd/wkcli/main_test.go cmd/wkcli/internal/nodeops/command.go cmd/wkcli/internal/nodeops/command_test.go
git commit -m "feat: add wkcli node read commands"
```

## Task 3: Add `wkcli node` Mutation Commands

**Files:**

- Modify: `cmd/wkcli/internal/nodeops/command.go`
- Modify: `cmd/wkcli/internal/nodeops/command_test.go`

- [ ] **Step 1: Write mutation command tests**

Append tests that cover one command from each lifecycle phase:

```go
func TestNodeActivateCommandPostsManagerRoute(t *testing.T) {
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"changed":true,"node_id":4,"addr":"127.0.0.1:11114","join_state":"active","revision":20}`))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{"activate", "4", "--server", server.URL})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, stderr.String())
	}
	if gotMethod != http.MethodPost || gotPath != "/manager/nodes/4/activate" {
		t.Fatalf("unexpected request %s %s", gotMethod, gotPath)
	}
	for _, want := range []string{"node=4", "join_state=active", "changed=true", "revision=20"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestNodeScaleInRemoveReturnsConflictWithBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/manager/nodes/4/scale-in/remove" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"conflict","message":"safe_to_remove=false blocked_reasons=blocked_by_channels"}`))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	cmd := NewCommand(command.Deps{Stdout: &stdout, Stderr: &stderr})
	cmd.SetArgs([]string{"scale-in", "remove", "4", "--server", server.URL})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected conflict")
	}
	if !strings.Contains(err.Error(), "blocked_by_channels") {
		t.Fatalf("error should preserve manager body, got %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
GOWORK=off go test ./cmd/wkcli/internal/nodeops -count=1
```

Expected: tests fail because mutation commands are not present.

- [ ] **Step 3: Implement mutation commands**

Add these commands under `wkcli node`:

```text
wkcli node activate 4
wkcli node onboarding plan 4 --max-slot-moves 1
wkcli node onboarding start 4 --max-slot-moves 1
wkcli node onboarding advance 4 --max-slot-moves 1
wkcli node onboarding status 4
wkcli node scale-in start 4
wkcli node scale-in plan 4 --max-slot-moves 1
wkcli node scale-in advance 4 --max-slot-moves 1
wkcli node scale-in drain 4 --draining=true
wkcli node scale-in status 4
wkcli node scale-in remove 4
```

Rules:

- `--max-slot-moves` defaults to `1` and rejects values above `5` before sending.
- Stage 11 intentionally has no `wkcli node join`; the node process must join through seed discovery before these commands operate it.
- `remove` returns a non-zero exit when manager returns non-2xx and must include the manager response body in the error.
- `drain` prints `draining`, `accepting_new_sessions`, `gateway_sessions`, `active_online`, `closing_online`, and `pending_activations`.
- `activate`, `scale-in start`, and `scale-in remove` print `changed`, `join_state`, and `revision`.
- `plan`, `start`, and `advance` may print raw JSON in human mode if a compact stable renderer is not useful; JSON must not drop fields.

Use this command handler pattern for lifecycle mutations:

```go
func newActivateCommand(deps command.Deps, opts *options) *cobra.Command {
	return &cobra.Command{
		Use: "activate NODE_ID",
		Short: "Activate a seed-joined dynamic data node after readiness gates pass",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeID, err := parseNodeIDArg(args)
			if err != nil {
				return err
			}
			client, err := clientFromOptions(deps, opts)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), opts.timeout)
			defer cancel()
			out, err := client.ActivateNode(ctx, nodeID)
			if err != nil {
				return writeAPIError(err)
			}
			if opts.jsonOutput {
				return writeJSON(deps.Stdout, out)
			}
			fmt.Fprintf(deps.Stdout, "node=%d changed=%t join_state=%s revision=%d\n", out.NodeID, out.Changed, out.JoinState, out.Revision)
			return nil
		},
	}
}

func printLifecycle(w io.Writer, out LifecycleResponse) {
	fmt.Fprintf(w, "node=%d changed=%t join_state=%s revision=%d\n", out.NodeID, out.Changed, out.JoinState, out.Revision)
}
```

Use this `scale-in` command structure:

```go
func newScaleInCommand(deps command.Deps, opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use: "scale-in",
		Short: "Drain and remove an internalv2 dynamic data node through manager safety gates",
	}
	cmd.AddCommand(
		newScaleInStartCommand(deps, opts),
		newScaleInPlanCommand(deps, opts),
		newScaleInAdvanceCommand(deps, opts),
		newScaleInDrainCommand(deps, opts),
		newScaleInStatusCommand(deps, opts),
		newScaleInRemoveCommand(deps, opts),
	)
	return cmd
}

func newScaleInStartCommand(deps command.Deps, opts *options) *cobra.Command {
	return &cobra.Command{
		Use: "start NODE_ID",
		Short: "Mark a dynamic data node leaving",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeID, err := parseNodeIDArg(args)
			if err != nil {
				return err
			}
			client, err := clientFromOptions(deps, opts)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), opts.timeout)
			defer cancel()
			out, err := client.ScaleInStart(ctx, nodeID)
			if err != nil {
				return writeAPIError(err)
			}
			if opts.jsonOutput {
				return writeJSON(deps.Stdout, out)
			}
			printLifecycle(deps.Stdout, out)
			return nil
		},
	}
}

func newScaleInDrainCommand(deps command.Deps, opts *options) *cobra.Command {
	var draining bool
	cmd := &cobra.Command{
		Use: "drain NODE_ID",
		Short: "Toggle gateway drain mode for a leaving node",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeID, err := parseNodeIDArg(args)
			if err != nil {
				return err
			}
			client, err := clientFromOptions(deps, opts)
			if err != nil {
				return err
			}
			var out map[string]any
			ctx, cancel := context.WithTimeout(cmd.Context(), opts.timeout)
			defer cancel()
			if err := client.SetScaleInDrain(ctx, nodeID, draining, &out); err != nil {
				return writeAPIError(err)
			}
			if opts.jsonOutput {
				return writeJSON(deps.Stdout, out)
			}
			fmt.Fprintf(deps.Stdout, "node=%d draining=%v accepting_new_sessions=%v gateway_sessions=%v active_online=%v closing_online=%v pending_activations=%v\n",
				nodeID, out["draining"], out["accepting_new_sessions"], out["gateway_sessions"], out["active_online"], out["closing_online"], out["pending_activations"])
			return nil
		},
	}
	cmd.Flags().BoolVar(&draining, "draining", true, "Set gateway drain mode")
	return cmd
}

func newScaleInRemoveCommand(deps command.Deps, opts *options) *cobra.Command {
	return &cobra.Command{
		Use: "remove NODE_ID",
		Short: "Mark a fully drained node removed after safe_to_remove=true",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeID, err := parseNodeIDArg(args)
			if err != nil {
				return err
			}
			client, err := clientFromOptions(deps, opts)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), opts.timeout)
			defer cancel()
			out, err := client.RemoveScaleInNode(ctx, nodeID)
			if err != nil {
				return writeAPIError(err)
			}
			if opts.jsonOutput {
				return writeJSON(deps.Stdout, out)
			}
			printLifecycle(deps.Stdout, out)
			return nil
		},
	}
}
```

Add concrete onboarding command builders:

```go
func newOnboardingCommand(deps command.Deps, opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use: "onboarding",
		Short: "Move bounded Slot replicas onto an active dynamic data node",
	}
	cmd.AddCommand(
		newOnboardingMoveCommand(deps, opts, "plan"),
		newOnboardingMoveCommand(deps, opts, "start"),
		newOnboardingMoveCommand(deps, opts, "advance"),
		newOnboardingStatusCommand(deps, opts),
	)
	return cmd
}

func newOnboardingMoveCommand(deps command.Deps, opts *options, action string) *cobra.Command {
	var maxSlotMoves uint32 = 1
	cmd := &cobra.Command{
		Use: action + " NODE_ID",
		Short: action + " bounded Slot onboarding work",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateMaxSlotMoves(maxSlotMoves); err != nil {
				return err
			}
			nodeID, err := parseNodeIDArg(args)
			if err != nil {
				return err
			}
			client, err := clientFromOptions(deps, opts)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), opts.timeout)
			defer cancel()
			var out map[string]any
			switch action {
			case "plan":
				err = client.OnboardingPlan(ctx, nodeID, maxSlotMoves, &out)
			case "start":
				err = client.OnboardingStart(ctx, nodeID, maxSlotMoves, &out)
			case "advance":
				err = client.OnboardingAdvance(ctx, nodeID, maxSlotMoves, &out)
			default:
				return command.Exit{Code: command.ExitInternal, Message: "unknown onboarding action"}
			}
			if err != nil {
				return writeAPIError(err)
			}
			return writeJSON(deps.Stdout, out)
		},
	}
	cmd.Flags().Uint32Var(&maxSlotMoves, "max-slot-moves", 1, "Maximum Slot replica moves to plan or create")
	return cmd
}

func newOnboardingStatusCommand(deps command.Deps, opts *options) *cobra.Command {
	return &cobra.Command{
		Use: "status NODE_ID",
		Short: "Read active onboarding tasks for a dynamic node",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeID, err := parseNodeIDArg(args)
			if err != nil {
				return err
			}
			client, err := clientFromOptions(deps, opts)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), opts.timeout)
			defer cancel()
			var out map[string]any
			if err := client.OnboardingStatus(ctx, nodeID, &out); err != nil {
				return writeAPIError(err)
			}
			return writeJSON(deps.Stdout, out)
		},
	}
}
```

Add concrete scale-in plan and advance builders:

```go
func newScaleInPlanCommand(deps command.Deps, opts *options) *cobra.Command {
	return newScaleInMoveCommand(deps, opts, "plan")
}

func newScaleInAdvanceCommand(deps command.Deps, opts *options) *cobra.Command {
	return newScaleInMoveCommand(deps, opts, "advance")
}

func newScaleInMoveCommand(deps command.Deps, opts *options, action string) *cobra.Command {
	var maxSlotMoves uint32 = 1
	cmd := &cobra.Command{
		Use: action + " NODE_ID",
		Short: action + " bounded Slot scale-in drain work",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateMaxSlotMoves(maxSlotMoves); err != nil {
				return err
			}
			nodeID, err := parseNodeIDArg(args)
			if err != nil {
				return err
			}
			client, err := clientFromOptions(deps, opts)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), opts.timeout)
			defer cancel()
			var out map[string]any
			if action == "plan" {
				err = client.ScaleInPlan(ctx, nodeID, maxSlotMoves, &out)
			} else {
				err = client.ScaleInAdvance(ctx, nodeID, maxSlotMoves, &out)
			}
			if err != nil {
				return writeAPIError(err)
			}
			return writeJSON(deps.Stdout, out)
		},
	}
	cmd.Flags().Uint32Var(&maxSlotMoves, "max-slot-moves", 1, "Maximum Slot replica moves to plan or create")
	return cmd
}
```

The `validateMaxSlotMoves` helper must be:

```go
func validateMaxSlotMoves(value uint32) error {
	if value == 0 || value > 5 {
		return command.Exit{Code: command.ExitConfig, Message: "--max-slot-moves must be between 1 and 5"}
	}
	return nil
}
```

- [ ] **Step 4: Run mutation command tests**

Run:

```bash
GOWORK=off go test ./cmd/wkcli/internal/nodeops -count=1
```

Expected: mutation commands hit the expected manager routes and preserve failure bodies.

- [ ] **Step 5: Run full wkcli tests**

Run:

```bash
GOWORK=off go test ./cmd/wkcli ./cmd/wkcli/internal/... -count=1
```

Expected: all wkcli tests pass.

- [ ] **Step 6: Commit Task 3**

```bash
git add cmd/wkcli/internal/nodeops/command.go cmd/wkcli/internal/nodeops/command_test.go
git commit -m "feat: add wkcli node lifecycle commands"
```

## Task 4: Add Real E2EV2 `wkcli node` Operations Rehearsal

**Files:**

- Create: `test/e2ev2/cluster/dynamic_node_operations/AGENTS.md`
- Create: `test/e2ev2/cluster/dynamic_node_operations/ops_cli_test.go`
- Modify: `test/e2ev2/cluster/AGENTS.md`

- [ ] **Step 1: Add scenario rules**

Create `test/e2ev2/cluster/dynamic_node_operations/AGENTS.md`:

````markdown
# dynamic_node_operations AGENTS

This package proves Stage 11 operator workflows for internalv2 dynamic data
nodes through `wkcli node`.

## Scenario Contract

- Start a real three-node `cmd/wukongimv2` cluster with manager HTTP, metrics,
  bench API, gateway listeners, and short test-only health report intervals.
- Start node 4 as a real seed-join process; do not shortcut by writing manager
  state from the test.
- Use `go run ./cmd/wkcli node ...` or a test-built wkcli binary for lifecycle
  operations after node 4 appears in manager state.
- Keep real WKProto `SEND -> SENDACK` traffic running while the CLI performs
  activate, onboarding, scale-in, gateway drain, and remove.
- Assert CLI stdout includes root-cause evidence such as `safe_to_remove`,
  `blocked_reasons`, health freshness, control revision, and gateway counters.

## Rules

- Keep tests black-box: do not import `internalv2/app`, `internalv2/usecase`,
  storage internals, ControllerV2 internals, or clusterv2 internals.
- Use public manager HTTP, public `/metrics`, WKProto clients, and process
  handles from `test/e2ev2/suite`.
- Prefer polling public status over fixed sleeps.
- Keep task fanout bounded with `--max-slot-moves 1`.
- Run this package serially with `-p=1`.

## Running

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_operations -count=1 -timeout 12m -p=1
```
````

- [ ] **Step 2: Write the e2ev2 test first**

Create `test/e2ev2/cluster/dynamic_node_operations/ops_cli_test.go` with the scenario skeleton below. Reuse helper patterns from `test/e2ev2/cluster/dynamic_node_readiness` by copying only black-box polling logic into this package or moving shared public helpers into `test/e2ev2/suite` if the implementation needs reuse.

```go
//go:build e2e

package dynamic_node_operations

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestWKCLINodeOperationsLifecycleWithTraffic(t *testing.T) {
	s := suite.New(t)
	const joinToken = "e2ev2-stage11-ops-token"
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
		suite.WithNodeConfigOverrides(1, readinessOverrides()),
		suite.WithNodeConfigOverrides(2, readinessOverrides()),
		suite.WithNodeConfigOverrides(3, readinessOverrides()),
		suite.WithNodeConfigOverrides(4, readinessOverrides()),
	)

	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	manager := cluster.ManagerClient(t, 1)
	traffic := startTrafficWorker(t, cluster, cluster.MustNode(1), "stage11-ops")
	defer stopTrafficWorker(t, traffic)

	node4 := cluster.StartSeedJoinNode(t, suite.SeedJoinNodeConfig{
		NodeID: 4,
		Seeds: cluster.SeedAddrs(),
		JoinToken: joinToken,
	})
	require.NotNil(t, node4)
	manager.EventuallyNodeJoinState(t, 4, "joining", 30*time.Second)
	manager.EventuallyNodeReadiness(t, 4, true, 30*time.Second)

	contextDir := t.TempDir()
	server := "http://" + cluster.MustNode(1).ManagerAddr()
	runWKCLI(t, contextDir, "context", "add", "ops", "--server", server, "--select")

	out := runWKCLI(t, contextDir, "node", "ls", "--context", "ops")
	require.Contains(t, out, "join_state=joining")
	require.Contains(t, out, "schedulable=false")

	out = runWKCLI(t, contextDir, "node", "activate", "4", "--context", "ops")
	require.Contains(t, out, "join_state=active")
	manager.EventuallyNodeJoinState(t, 4, "active", 30*time.Second)
	requireTrafficProgress(t, cluster, traffic, 3, 10*time.Second)

	out = eventuallyWKCLIContains(t, contextDir, 45*time.Second,
		[]string{"node", "onboarding", "start", "4", "--context", "ops", "--max-slot-moves", "1", "--json"},
		[]string{`"created"`},
	)
	requireJSONUintField(t, out, "created", 1)
	requireJSONUintField(t, out, "target_node_id", 4)
	manager.EventuallyOnboardingSafe(t, 4, 150*time.Second)
	requireTrafficProgress(t, cluster, traffic, 3, 10*time.Second)

	out = eventuallyWKCLIContains(t, contextDir, 45*time.Second,
		[]string{"node", "scale-in", "start", "4", "--context", "ops"},
		[]string{"join_state=leaving"},
	)
	require.Contains(t, out, "changed=true")
	manager.EventuallyNodeJoinState(t, 4, "leaving", 30*time.Second)

	out = runWKCLI(t, contextDir, "node", "scale-in", "drain", "4", "--context", "ops", "--draining=true")
	require.Contains(t, out, "draining=true")

	out = eventuallyWKCLIContains(t, contextDir, 45*time.Second,
		[]string{"node", "scale-in", "advance", "4", "--context", "ops", "--max-slot-moves", "1", "--json"},
		[]string{`"node_id"`},
	)
	requireJSONUintField(t, out, "node_id", 4)
	requireJSONFieldPresent(t, out, "state_revision")
	manager.EventuallyScaleInSafeToRemove(t, 4, 150*time.Second)

	status := runWKCLI(t, contextDir, "node", "scale-in", "status", "4", "--context", "ops")
	require.Contains(t, status, "safe_to_remove=true")
	require.Contains(t, status, "control_rev=")
	require.Contains(t, status, "gateway_sessions=0")

	out = runWKCLI(t, contextDir, "node", "scale-in", "remove", "4", "--context", "ops")
	require.Contains(t, out, "join_state=removed")
	manager.EventuallyNodeJoinState(t, 4, "removed", 30*time.Second)
	requireTrafficProgress(t, cluster, traffic, 3, 10*time.Second)
}

func runWKCLI(t *testing.T, contextDir string, args ...string) string {
	t.Helper()
	result := runWKCLIResult(t, contextDir, args...)
	if result.Err != nil {
		t.Fatalf("wkcli %s failed: exit=%d err=%v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), result.ExitCode, result.Err, result.Stdout, result.Stderr)
	}
	return result.Stdout
}

func requireJSONUintField(t *testing.T, text string, field string, want uint64) {
	t.Helper()
	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(text), &payload), text)
	raw, ok := payload[field]
	require.True(t, ok, "json field %s missing from %s", field, text)
	var got uint64
	require.NoError(t, json.Unmarshal(raw, &got), text)
	require.Equal(t, want, got, "json field %s", field)
}

func requireJSONFieldPresent(t *testing.T, text string, field string) {
	t.Helper()
	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(text), &payload), text)
	_, ok := payload[field]
	require.True(t, ok, "json field %s missing from %s", field, text)
}

type wkcliResult struct {
	Stdout string
	Stderr string
	ExitCode int
	Err error
}

func runWKCLIResult(t *testing.T, contextDir string, args ...string) wkcliResult {
	t.Helper()
	cmdArgs := append([]string{"run", "./cmd/wkcli", "--context-dir", contextDir}, args...)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", cmdArgs...)
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "GOWORK=off")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	return wkcliResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode, Err: err}
}

func eventuallyWKCLIContains(t *testing.T, contextDir string, timeout time.Duration, args []string, wants []string) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last wkcliResult
	for time.Now().Before(deadline) {
		last = runWKCLIResult(t, contextDir, args...)
		combined := last.Stdout + "\n" + last.Stderr
		if last.Err != nil && !strings.Contains(combined, "status=409") && !strings.Contains(combined, "status=503") && !strings.Contains(combined, "conflict") && !strings.Contains(combined, "service_unavailable") {
			t.Fatalf("wkcli %s failed without retryable readiness status: exit=%d err=%v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), last.ExitCode, last.Err, last.Stdout, last.Stderr)
		}
		ok := true
		for _, want := range wants {
			if !strings.Contains(combined, want) {
				ok = false
				break
			}
		}
		if ok {
			return last.Stdout
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("wkcli %s did not contain %v before timeout; last exit=%d stdout:\n%s\nstderr:\n%s", strings.Join(args, " "), wants, last.ExitCode, last.Stdout, last.Stderr)
	return ""
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatalf("go.mod not found")
		}
		wd = parent
	}
}
```

The final implementation must add local helper functions equivalent to:

- `readinessOverrides`
- `startTrafficWorker`
- `stopTrafficWorker`
- `requireTrafficProgress`

These helpers can be copied from `test/e2ev2/cluster/dynamic_node_readiness` when they use only public e2ev2 harness, public manager HTTP, and WKProto clients. If a helper would import non-public internals, rewrite it in this package using public surfaces.

- [ ] **Step 3: Run the e2ev2 test to verify it fails**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_operations -run TestWKCLINodeOperationsLifecycleWithTraffic -count=1 -timeout 12m -p=1
```

Expected: fails until the `wkcli node` command and scenario helpers are complete.

- [ ] **Step 4: Implement helpers and stabilize CLI polling**

Rules for stabilization:

- No fixed sleeps except a 100ms polling tick.
- When a CLI mutation returns `409 conflict` or `503 service_unavailable` during a known readiness window, poll the public status endpoint and retry until timeout.
- For any other non-2xx result, fail immediately with CLI stdout/stderr and `cluster.DumpDiagnostics()`.
- Keep `--max-slot-moves 1` in the scenario.

- [ ] **Step 5: Update scenario catalog**

Modify `test/e2ev2/cluster/AGENTS.md` scenario catalog:

```markdown
- `dynamic_node_operations`: Stage 11 operator rehearsal that drives dynamic
  activation, onboarding, scale-in, drain, and remove through `wkcli node`
  while real WKProto traffic continues.
```

- [ ] **Step 6: Run the e2ev2 scenario**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_operations -count=1 -timeout 12m -p=1
```

Expected: the scenario passes and proves the CLI path can operate a real dynamic node lifecycle without internal shortcuts.

- [ ] **Step 7: Commit Task 4**

```bash
git add test/e2ev2/cluster/AGENTS.md test/e2ev2/cluster/dynamic_node_operations/AGENTS.md test/e2ev2/cluster/dynamic_node_operations/ops_cli_test.go
git commit -m "test: add wkcli dynamic node operations e2e"
```

## Task 5: Add Operations Readiness Gate Profile

**Files:**

- Modify: `scripts/e2ev2/dynamic-node-readiness-gate.sh`
- Modify: `scripts/dynamic_node_readiness_gate_script_test.go`

- [ ] **Step 1: Add dry-run test for the operations profile**

Append to `scripts/dynamic_node_readiness_gate_script_test.go`:

```go
func TestDynamicNodeReadinessGateDryRunOpsProfileIncludesStage11(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	binary := filepath.Join(outDir, "wukongimv2-gofail")

	cmd := exec.Command("bash", "scripts/e2ev2/dynamic-node-readiness-gate.sh",
		"--dry-run",
		"--profile", "ops",
		"--out-dir", outDir,
		"--binary", binary,
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"profile=ops",
		"wkcli_cmd=GOWORK=off go test ./cmd/wkcli ./cmd/wkcli/internal/... -count=1",
		"stage11_ops_cmd=GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_operations -count=1 -timeout 12m -p=1",
		"stage9d_cmd=GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_readiness -run TestDynamicNodeLifecycleWithContinuousTraffic -count=1 -timeout 12m -p=1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
}
```

- [ ] **Step 2: Run script test to verify it fails**

Run:

```bash
GOWORK=off go test ./scripts -run 'TestDynamicNodeReadinessGateDryRunOpsProfile' -count=1
```

Expected: fails because `ops` is not a valid profile yet.

- [ ] **Step 3: Implement `ops` profile**

Modify `scripts/e2ev2/dynamic-node-readiness-gate.sh`:

- `validate_profile` accepts `quick`, `full`, and `ops`.
- `print_plan` prints `wkcli_cmd=` and `stage11_ops_cmd=` when profile is `ops`.
- runtime execution for `ops` runs everything in `full`, then:

```bash
WKCLI_CMD=(env GOWORK=off "$GO_BIN" test ./cmd/wkcli ./cmd/wkcli/internal/... -count=1)
STAGE11_OPS_CMD=(env GOWORK=off "$GO_BIN" test -tags=e2e ./test/e2ev2/cluster/dynamic_node_operations -count=1 -timeout 12m -p=1)
```

Use per-command logs:

- `wkcli.log`
- `stage11-ops.log`

The summary must include:

```text
- wkcli: PASS
- stage11-ops: PASS
```

on success.

- [ ] **Step 4: Run script tests**

Run:

```bash
GOWORK=off go test ./scripts -run DynamicNodeReadinessGate -count=1
```

Expected: all readiness gate script tests pass.

- [ ] **Step 5: Run the operations profile dry-run**

Run:

```bash
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile ops --dry-run
```

Expected: dry-run output includes Stage10A, Stage9D, wkcli unit checks, Stage11 operations e2e, and `git diff --check`.

- [ ] **Step 6: Commit Task 5**

```bash
git add scripts/e2ev2/dynamic-node-readiness-gate.sh scripts/dynamic_node_readiness_gate_script_test.go
git commit -m "test: add dynamic node operations gate"
```

## Task 6: Add Runbook, README, Project Knowledge, And Lifecycle Links

**Files:**

- Create: `docs/superpowers/runbooks/internalv2-dynamic-node-operations.md`
- Modify: `cmd/wkcli/README.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`
- Modify: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md`
- Modify: `docs/superpowers/plans/2026-06-30-internalv2-dynamic-node-stage11-operations-productization.md`

- [x] **Step 1: Write the operations runbook**

Create `docs/superpowers/runbooks/internalv2-dynamic-node-operations.md`:

````markdown
# InternalV2 Dynamic Node Operations Runbook

## Safety Model

- Dynamic node process startup is outside `wkcli node`.
- `wkcli node` calls only public manager HTTP endpoints under `/manager/nodes*`.
- Joining nodes are not schedulable until activation succeeds.
- Onboarding and scale-in create bounded `slot_replica_move` work with
  `--max-slot-moves`; use `1` unless there is a measured reason to increase it.
- Final removal is fail-closed. If `safe_to_remove=false`, do not retry blindly;
  inspect the printed blockers first.

## Target Setup

```bash
go run ./cmd/wkcli context add prod-a \
  --server http://127.0.0.1:5001 \
  --description "primary manager endpoint" \
  --select
```

Use `--token` on every command when manager auth is enabled:

```bash
go run ./cmd/wkcli node ls --context prod-a --token "$WK_MANAGER_TOKEN"
```

## Add A Data Node

1. Start the new `cmd/wukongimv2` process with seed discovery and join token.

   The new process must use the static cluster seeds, not its own
   `WK_CLUSTER_NODES` single-node bootstrap. The process must publish a stable
   `WK_CLUSTER_ADVERTISE_ADDR`.

2. Wait for manager inventory to show `joining` and fresh health:

   ```bash
   go run ./cmd/wkcli node ls --context prod-a
   ```

   Required evidence:

   ```text
   join_state=joining
   schedulable=false
   health=fresh/alive
   runtime_ready=true
   ```

3. Activate the node:

   ```bash
   go run ./cmd/wkcli node activate 4 --context prod-a
   ```

4. Confirm it is active and schedulable:

   ```bash
   go run ./cmd/wkcli node ls --context prod-a
   ```

5. Move one bounded Slot replica onto the node:

   ```bash
   go run ./cmd/wkcli node onboarding plan 4 --context prod-a --max-slot-moves 1
   go run ./cmd/wkcli node onboarding start 4 --context prod-a --max-slot-moves 1
   go run ./cmd/wkcli node onboarding status 4 --context prod-a
   ```

   Repeat `advance` only after status shows no active onboarding task:

   ```bash
   go run ./cmd/wkcli node onboarding advance 4 --context prod-a --max-slot-moves 1
   ```

## Remove A Data Node

1. Mark the node leaving:

   ```bash
   go run ./cmd/wkcli node scale-in start 4 --context prod-a
   ```

2. Enable gateway drain:

   ```bash
   go run ./cmd/wkcli node scale-in drain 4 --context prod-a --draining=true
   ```

3. Drain Slot replicas with bounded moves:

   ```bash
   go run ./cmd/wkcli node scale-in plan 4 --context prod-a --max-slot-moves 1
   go run ./cmd/wkcli node scale-in advance 4 --context prod-a --max-slot-moves 1
   ```

4. Check final safety:

   ```bash
   go run ./cmd/wkcli node scale-in status 4 --context prod-a
   ```

   Required evidence before final removal:

   ```text
   safe_to_remove=true
   gateway draining=true
   accepting_new_sessions=false
   gateway_sessions=0
   active_online=0
   closing_online=0
   total_online=0
   pending_activations=0
   ```

5. Remove the node:

   ```bash
   go run ./cmd/wkcli node scale-in remove 4 --context prod-a
   ```

## Root-Cause Triage

- `target_health_stale`: check node process liveness, `/readyz`, manager
  health freshness, and control revision catch-up.
- `eligible_node_health_stale`: replacement nodes are not fresh enough for safe
  placement; inspect `wkcli node ls`.
- `blocked_by_slots`: run `scale-in plan` and `scale-in advance` with
  `--max-slot-moves 1`; wait for tasks to clear.
- `blocked_by_slot_leadership`: wait for Slot leadership to move or inspect
  manager Slot status.
- `blocked_by_tasks`: inspect `/manager/controller/tasks` before submitting
  more work.
- `blocked_by_channels` or `unknown_channel_inventory`: do not remove; channel
  runtime inventory could not prove the target is empty.
- `blocked_by_runtime_drain`: keep drain enabled and wait until gateway and
  online counters reach zero.
- `unknown_runtime` or `unknown_control_revision`: treat as unsafe; collect
  node logs, manager status, and readiness gate evidence.

## Local Release Gate

```bash
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile ops
```

Evidence is written under:

```text
data/dynamic-node-readiness-gate/<timestamp>/
```
````

- [x] **Step 2: Update `cmd/wkcli/README.md`**

Add a `Node Operations` section after `Sim`:

````markdown
## Node Operations

`node` operates internalv2 dynamic data nodes through manager HTTP. It does not
start or stop server processes and does not write ControllerV2 or Slot state
directly.

```bash
go run ./cmd/wkcli node ls --context dev
go run ./cmd/wkcli node activate 4 --context dev
go run ./cmd/wkcli node onboarding start 4 --context dev --max-slot-moves 1
go run ./cmd/wkcli node scale-in start 4 --context dev
go run ./cmd/wkcli node scale-in drain 4 --context dev --draining=true
go run ./cmd/wkcli node scale-in status 4 --context dev
go run ./cmd/wkcli node scale-in remove 4 --context dev
```

The command preserves manager safety evidence in output, including health
freshness, control revision, `blocked_reasons`, `safe_to_remove`, and gateway
drain counters. Use
`docs/superpowers/runbooks/internalv2-dynamic-node-operations.md` for the full
operator procedure.
````

Also update the command table to include:

```markdown
| `node` | Operates internalv2 dynamic nodes through manager HTTP. |
```

- [x] **Step 3: Update project knowledge**

Add one concise bullet under `## Cluster Membership` in `docs/development/PROJECT_KNOWLEDGE.md`:

```markdown
- `wkcli node` is an operations client for public manager HTTP only; dynamic
  node process startup remains seed-join driven and outside the CLI.
```

- [x] **Step 4: Verify and maintain Stage 11 links in the master lifecycle plan**

Modify `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md`.

Confirm the `Goal` line no longer says "five ordered stages"; it should describe ordered, reviewable stages through operator-ready workflows.

Confirm the `Source Spec` list contains:

```markdown
- Stage 11 plan: `docs/superpowers/plans/2026-06-30-internalv2-dynamic-node-stage11-operations-productization.md`
```

Confirm the `Execution Order` table contains this row after Stage 10B:

```markdown
| 11 | Operations Productization | `2026-06-30-internalv2-dynamic-node-stage11-operations-productization.md` | Operator-safe `wkcli node`, runbook, and real CLI rehearsal gate |
```

Confirm a Gate 11 block exists after `Stage 10B Completion`:

````markdown
## Gate 11: Stage 11 starts only after Stage 10B readiness gate passes

Run:

```bash
GOWORK=off go test ./scripts -run DynamicNodeReadinessGate -count=1
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile full
git diff --check
```

Expected: Stage10B gate remains green before adding the operations layer.
````

Confirm a Stage 11 completion block exists for later use:

````markdown
## Stage 11 Completion

- [ ] **Stage 11 operations productization merged locally**

Run on merged local `main`:

```bash
GOWORK=off go test ./cmd/wkcli ./cmd/wkcli/internal/... -count=1
GOWORK=off go test ./scripts -run DynamicNodeReadinessGate -count=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_operations -count=1 -timeout 12m -p=1
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile ops
git diff --check
```

Expected: CLI unit tests, script tests, real CLI operations e2e, full operations
gate, and whitespace checks pass on merged local `main`.
````

- [x] **Step 5: Run docs and focused tests**

Evidence note: Task 6 adds the operations runbook, `wkcli node` README section,
project knowledge link, and confirms the master lifecycle plan already links
Stage 11 after Stage 10B with the required completion gate commands.

Verification evidence: `GOWORK=off go test ./cmd/wkcli ./cmd/wkcli/internal/... -count=1`,
`GOWORK=off go test ./scripts -run DynamicNodeReadinessGate -count=1`, and
`git diff --check` passed for Task 6.

Run:

```bash
GOWORK=off go test ./cmd/wkcli ./cmd/wkcli/internal/... -count=1
GOWORK=off go test ./scripts -run DynamicNodeReadinessGate -count=1
git diff --check
```

Expected: focused tests pass and docs have no whitespace errors.

- [x] **Step 6: Commit Task 6**

```bash
git add cmd/wkcli/README.md docs/development/PROJECT_KNOWLEDGE.md docs/superpowers/runbooks/internalv2-dynamic-node-operations.md docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md docs/superpowers/plans/2026-06-30-internalv2-dynamic-node-stage11-operations-productization.md
git commit -m "docs: add dynamic node operations runbook"
```

## Final Verification

Run before declaring Stage 11 complete:

```bash
GOWORK=off go test ./cmd/wkcli ./cmd/wkcli/internal/... -count=1
GOWORK=off go test ./scripts -run DynamicNodeReadinessGate -count=1
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_operations -count=1 -timeout 12m -p=1
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile ops
git diff --check
```

Expected:

- `wkcli node` unit tests pass;
- readiness gate script tests pass;
- Stage11 real CLI operations e2e passes serially;
- `--profile ops` writes evidence and passes;
- no whitespace errors remain.

## Rollback And Failure Triage

- If CLI unit tests fail, inspect `cmd/wkcli/internal/nodeops/client_test.go` first; most failures should map to wrong HTTP path, missing bearer token, or dropped error body.
- If the e2ev2 CLI rehearsal fails before activation, inspect manager node list output, `/readyz`, and seed-join process logs; the expected root cause is usually missing `joining` state or stale readiness evidence.
- If onboarding or scale-in CLI commands return `409`, inspect `blocked_reasons`, active Controller tasks, and Slot runtime status before retrying.
- If remove fails, do not change the CLI to ignore manager errors. The root cause must be proven from `safe_to_remove=false` blockers.
- If the gate script fails only in `ops`, use the evidence directory logs in this order: `wkcli.log`, `stage11-ops.log`, `stage9d-real-traffic.log`, then `stage10a-gofail.log`.

## Self-Review Checklist

- [ ] Every manager mutation in Stage 11 goes through `/manager/nodes*`.
- [ ] CLI commands preserve manager response fields needed for root-cause diagnosis.
- [ ] E2EV2 scenario starts node 4 by seed join and never calls manager join as a shortcut.
- [ ] Real traffic is running across activate, onboarding, scale-in, drain, and remove.
- [ ] All e2ev2 commands run with `-p=1`.
- [ ] No new configuration keys are added.
- [ ] No `internalv2`, `pkg/controllerv2`, `pkg/clusterv2`, or storage internals are imported by `cmd/wkcli/internal/nodeops`.
- [ ] Master lifecycle plan links Stage 11 after Stage 10B.
