# InternalV2 Dynamic Node Stage 7 Gofail Fault Injection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add opt-in gofail-backed failure injection for internalv2 dynamic node join and onboarding so asynchronous fault windows are reproducible instead of flaky.

**Architecture:** Keep normal builds untouched by adding gofail marker comments only at narrow clusterv2 boundaries, then enabling them in a temporary source copy through the existing gofail build script. Run fault scenarios as opt-in e2ev2 black-box tests using `WK_E2EV2_BINARY` and per-node `GOFAIL_HTTP` loopback endpoints. Stage 7A builds the harness and one deterministic Slot replica move fault; Stage 7B adds targeted clusterv2 RPC faults and process restart coverage.

**Tech Stack:** Go 1.23, `go.etcd.io/gofail@v0.2.0`, existing `scripts/build-gofail-binary.sh`, existing `test/e2ev2/suite`, real `cmd/wukongimv2` multi-process e2e, manager HTTP APIs.

---

## Design Notes

- The upstream gofail workflow is comment-marker based: `gofail enable` rewrites marker comments into runtime code in a source tree, `GOFAIL_HTTP=127.0.0.1:port` exposes runtime controls, and `PUT /<failpoint>` activates expressions such as `return("hello")`.
- WuKongIM already has a safe pilot for legacy e2e transport failpoints. Reuse that pattern: build from a temporary source copy, never commit generated gofail files, and keep all fault tests opt-in.
- e2ev2 uses `cmd/wukongimv2`, so the existing build script must support a configurable command package while preserving its current default for `cmd/wukongim`.
- Fault injection must prove root causes. A failing e2e must dump manager task status, Slot runtime status where available, gofail endpoint lists/counts, and node logs.
- Default commands such as `go test ./pkg/...` and serial e2ev2 must still pass without gofail runtime imports.

## File Map

- Modify: `scripts/build-gofail-binary.sh` to add `--cmd ./cmd/wukongimv2`.
- Modify: `scripts/gofail_build_script_test.go` to lock script command rendering.
- Create: `test/e2ev2/suite/gofail.go` for HTTP failpoint controls.
- Create: `test/e2ev2/suite/gofail_test.go` for helper URL/env behavior.
- Modify: `pkg/clusterv2/tasks/slot_replica_move.go` with delay failpoints around async phase gates.
- Create: `pkg/clusterv2/tasks/gofail_markers_test.go` for marker preservation.
- Modify: `pkg/clusterv2/net/transport.go` with targeted typed RPC fault markers.
- Create: `pkg/clusterv2/net/gofail_markers_test.go` for marker preservation and alias coverage.
- Create: `test/e2ev2/cluster/dynamic_node_faults/AGENTS.md` for opt-in scenario rules.
- Create: `test/e2ev2/cluster/dynamic_node_faults/gofail_smoke_test.go` for binary/failpoint exposure.
- Create: `test/e2ev2/cluster/dynamic_node_faults/slot_replica_move_fault_test.go` for delayed replica-move convergence.
- Create: `test/e2ev2/cluster/dynamic_node_faults/node_lifecycle_rpc_fault_test.go` for seed-join/control-write retry faults.
- Create: `test/e2ev2/cluster/dynamic_node_faults/restart_during_onboarding_test.go` for process restart recovery.
- Modify: `test/e2ev2/AGENTS.md` and `test/e2ev2/cluster/AGENTS.md` to catalog the new opt-in package.

---

### Task 1: Make the gofail Build Script Work for `cmd/wukongimv2`

**Files:**
- Modify: `scripts/build-gofail-binary.sh`
- Modify: `scripts/gofail_build_script_test.go`

- [ ] **Step 1: Write the failing script test for command package override**

Add this test to `scripts/gofail_build_script_test.go`:

```go
func TestBuildGofailBinaryDryRunSupportsCommandPackageOverride(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "wukongimv2-gofail")
	workDir := filepath.Join(t.TempDir(), "src")
	cmd := exec.Command("bash", "scripts/build-gofail-binary.sh",
		"--dry-run",
		"--out", outPath,
		"--work-dir", workDir,
		"--cmd", "./cmd/wukongimv2",
		"--package", "pkg/clusterv2/tasks",
	)
	cmd.Dir = repoRootForScriptTests(t)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"command_package=./cmd/wukongimv2",
		"failpoint_packages=pkg/transport pkg/clusterv2/tasks",
		"build_cmd=GOWORK=off go build -o " + outPath + " ./cmd/wukongimv2",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry run output missing %q:\n%s", want, text)
		}
	}
}
```

- [ ] **Step 2: Run the test and verify it fails for missing `--cmd`**

Run:

```bash
GOWORK=off go test ./scripts -run TestBuildGofailBinaryDryRunSupportsCommandPackageOverride -count=1
```

Expected: FAIL because `scripts/build-gofail-binary.sh` currently rejects `--cmd`.

- [ ] **Step 3: Implement `--cmd` without changing the default**

In `scripts/build-gofail-binary.sh`:

```bash
CMD_PACKAGE="./cmd/wukongim"
```

Add usage text:

```bash
  --cmd PACKAGE           Go command package to build. Defaults to ./cmd/wukongim.
```

Add argument parsing:

```bash
    --cmd)
      CMD_PACKAGE="${2:?missing value for --cmd}"
      shift 2
      ;;
```

Add dry-run output:

```bash
  echo "command_package=$CMD_PACKAGE"
  echo "build_cmd=GOWORK=off go build -o $OUT_PATH $CMD_PACKAGE"
```

Replace the build command:

```bash
GOWORK=off go build -o "$OUT_PATH" "$CMD_PACKAGE"
```

- [ ] **Step 4: Verify script tests**

Run:

```bash
GOWORK=off go test ./scripts -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 1**

```bash
git add scripts/build-gofail-binary.sh scripts/gofail_build_script_test.go
git commit -m "test: support gofail wukongimv2 builds"
```

---
### Task 2: Add e2ev2 gofail HTTP Control Helpers

**Files:**
- Create: `test/e2ev2/suite/gofail.go`
- Create: `test/e2ev2/suite/gofail_test.go`

- [ ] **Step 1: Write the helper tests first**

Create `test/e2ev2/suite/gofail_test.go`:

```go
//go:build e2e

package suite

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGofailEndpointEnvUsesLoopbackHTTP(t *testing.T) {
	endpoint := ReserveGofailEndpoint(t)
	require.True(t, strings.HasPrefix(endpoint.Env(), "GOFAIL_HTTP=127.0.0.1:"))
	require.Equal(t, "http://"+endpoint.Addr, endpoint.BaseURL())
}

func TestGofailEndpointRejectsEmptyFailpointName(t *testing.T) {
	endpoint := GofailEndpoint{Addr: "127.0.0.1:1"}
	err := endpoint.Enable(context.Background(), "", `return("boom")`)
	require.Error(t, err)
}

func TestReserveGofailEndpointReturnsFreePort(t *testing.T) {
	endpoint := ReserveGofailEndpoint(t)
	ln, err := net.Listen("tcp", endpoint.Addr)
	require.NoError(t, err)
	require.NoError(t, ln.Close())
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/suite -run 'TestGofailEndpoint|TestReserveGofailEndpoint' -count=1
```

Expected: FAIL because `GofailEndpoint` does not exist.

- [ ] **Step 3: Implement the helper**

Create `test/e2ev2/suite/gofail.go`:

```go
//go:build e2e

package suite

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// GofailEndpoint controls one node-local gofail HTTP server.
type GofailEndpoint struct {
	// Addr is the loopback host:port assigned to GOFAIL_HTTP.
	Addr string
}

// ReserveGofailEndpoint reserves and releases one loopback address for a gofail HTTP server.
func ReserveGofailEndpoint(t testing.TB) GofailEndpoint {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve gofail endpoint: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close reserved gofail listener: %v", err)
	}
	return GofailEndpoint{Addr: addr}
}

// Env returns the process environment entry that enables the gofail HTTP server.
func (e GofailEndpoint) Env() string {
	return "GOFAIL_HTTP=" + e.Addr
}

// BaseURL returns the HTTP base URL for the gofail endpoint.
func (e GofailEndpoint) BaseURL() string {
	return "http://" + e.Addr
}

// Enable activates one failpoint expression.
func (e GofailEndpoint) Enable(ctx context.Context, name string, expression string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("gofail failpoint name is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, e.BaseURL()+"/"+name, bytes.NewBufferString(expression))
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("enable gofail %s: status=%d body=%s", name, resp.StatusCode, body)
	}
	return nil
}

// Disable deactivates one failpoint.
func (e GofailEndpoint) Disable(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("gofail failpoint name is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, e.BaseURL()+"/"+name, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("disable gofail %s: status=%d body=%s", name, resp.StatusCode, body)
	}
	return nil
}

// List returns the failpoint list exposed by the node process.
func (e GofailEndpoint) List(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.BaseURL()+"/", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("list gofail: status=%d body=%s", resp.StatusCode, data)
	}
	return string(data), nil
}

// WaitListed waits until all failpoint names appear in the node-local failpoint list.
func (e GofailEndpoint) WaitListed(ctx context.Context, names ...string) (string, error) {
	var lastErr error
	for ctx.Err() == nil {
		body, err := e.List(ctx)
		if err == nil {
			ok := true
			for _, name := range names {
				if !strings.Contains(body, name+"=") {
					ok = false
					break
				}
			}
			if ok {
				return body, nil
			}
			lastErr = fmt.Errorf("missing failpoint in body: %s", body)
		} else {
			lastErr = err
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return "", fmt.Errorf("wait gofail list: %w; last=%v", ctx.Err(), lastErr)
}
```

- [ ] **Step 4: Verify suite helper tests**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/suite -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 2**

```bash
git add test/e2ev2/suite/gofail.go test/e2ev2/suite/gofail_test.go
git commit -m "test: add e2ev2 gofail controls"
```

---
### Task 3: Add Slot Replica Move Failpoints

**Files:**
- Modify: `pkg/clusterv2/tasks/slot_replica_move.go`
- Create: `pkg/clusterv2/tasks/gofail_markers_test.go`

- [ ] **Step 1: Write marker preservation tests**

Create `pkg/clusterv2/tasks/gofail_markers_test.go`:

```go
package tasks

import (
	"os"
	"strings"
	"testing"
)

func TestSlotReplicaMoveGofailMarkersStayInSource(t *testing.T) {
	source, err := os.ReadFile("slot_replica_move.go")
	if err != nil {
		t.Fatalf("ReadFile(slot_replica_move.go): %v", err)
	}
	text := string(source)
	required := []string{
		"// gofail: var wkSlotReplicaMovePromoteLearnerDelay string",
		"// if err := sleepSlotReplicaMoveFailpoint(ctx, wkSlotReplicaMovePromoteLearnerDelay); err != nil { return err }",
		"// gofail: var wkSlotReplicaMoveTransferLeaderDelay string",
		"// if err := sleepSlotReplicaMoveFailpoint(ctx, wkSlotReplicaMoveTransferLeaderDelay); err != nil { return err }",
		"// gofail: var wkSlotReplicaMoveRemoveVoterDelay string",
		"// if err := sleepSlotReplicaMoveFailpoint(ctx, wkSlotReplicaMoveRemoveVoterDelay); err != nil { return err }",
	}
	for _, marker := range required {
		if !strings.Contains(text, marker) {
			t.Fatalf("slot_replica_move.go missing gofail marker %q", marker)
		}
	}
}

func TestSleepSlotReplicaMoveFailpoint(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepSlotReplicaMoveFailpoint(ctx, "1s"); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleepSlotReplicaMoveFailpoint canceled error = %v, want context.Canceled", err)
	}
	if err := sleepSlotReplicaMoveFailpoint(context.Background(), "not-a-duration"); err != nil {
		t.Fatalf("invalid duration should be ignored, got %v", err)
	}
}
```

Add imports to this new test file:

```go
import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)
```

- [ ] **Step 2: Run marker tests and verify failure**

Run:

```bash
GOWORK=off go test ./pkg/clusterv2/tasks -run 'TestSlotReplicaMoveGofailMarkersStayInSource|TestSleepSlotReplicaMoveFailpoint' -count=1
```

Expected: FAIL because the markers and helper do not exist.

- [ ] **Step 3: Add the failpoint sleep helper**

Append this helper near the other small slot replica move helpers in `pkg/clusterv2/tasks/slot_replica_move.go`:

```go
func sleepSlotReplicaMoveFailpoint(ctx context.Context, raw string) error {
	if raw == "" {
		return nil
	}
	delay, err := time.ParseDuration(raw)
	if err != nil || delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
```

- [ ] **Step 4: Add marker comments at async phase gates**

In `promoteLearner`, insert before `e.changeConfig(... PromoteLearner ...)`:

```go
// gofail: var wkSlotReplicaMovePromoteLearnerDelay string
// if err := sleepSlotReplicaMoveFailpoint(ctx, wkSlotReplicaMovePromoteLearnerDelay); err != nil { return err }
```

In `removeVoter`, insert before `e.cfg.Runtime.TransferLeadership(...)`:

```go
// gofail: var wkSlotReplicaMoveTransferLeaderDelay string
// if err := sleepSlotReplicaMoveFailpoint(ctx, wkSlotReplicaMoveTransferLeaderDelay); err != nil { return err }
```

In `removeVoter`, insert before `e.changeConfig(... RemoveVoter ...)`:

```go
// gofail: var wkSlotReplicaMoveRemoveVoterDelay string
// if err := sleepSlotReplicaMoveFailpoint(ctx, wkSlotReplicaMoveRemoveVoterDelay); err != nil { return err }
```

- [ ] **Step 5: Verify normal tests stay clean**

Run:

```bash
GOWORK=off go test ./pkg/clusterv2/tasks -count=1
```

Expected: PASS without importing gofail runtime.

- [ ] **Step 6: Commit Task 3**

```bash
git add pkg/clusterv2/tasks/slot_replica_move.go pkg/clusterv2/tasks/gofail_markers_test.go
git commit -m "test: add slot replica move failpoints"
```

---
### Task 4: Add clusterv2 Typed RPC Failpoints

**Files:**
- Modify: `pkg/clusterv2/net/transport.go`
- Create: `pkg/clusterv2/net/gofail_markers_test.go`

- [ ] **Step 1: Write marker and parser tests**

Create `pkg/clusterv2/net/gofail_markers_test.go`:

```go
package clusternet

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestTransportGofailMarkersStayInSource(t *testing.T) {
	source, err := os.ReadFile("transport.go")
	if err != nil {
		t.Fatalf("ReadFile(transport.go): %v", err)
	}
	text := string(source)
	required := []string{
		"// gofail: var wkClusterNetCallShardFault string",
		"// if err := gofailClusterNetServiceFault(wkClusterNetCallShardFault, serviceID); err != nil { return nil, err }",
		"// gofail: var wkClusterNetCallShardOwnedFault string",
		"// if err := gofailClusterNetServiceFault(wkClusterNetCallShardOwnedFault, serviceID); err != nil { return nil, err }",
		"// gofail: var wkClusterNetSendFault string",
		"// if err := gofailClusterNetServiceFault(wkClusterNetSendFault, serviceID); err != nil { return err }",
		"// gofail: var wkClusterNetSendOwnedFault string",
		"// if err := gofailClusterNetServiceFault(wkClusterNetSendOwnedFault, serviceID); err != nil { return err }",
	}
	for _, marker := range required {
		if !strings.Contains(text, marker) {
			t.Fatalf("transport.go missing gofail marker %q", marker)
		}
	}
}

func TestGofailClusterNetServiceFaultMatchesAlias(t *testing.T) {
	err := gofailClusterNetServiceFault("node_lifecycle:seed join unavailable", RPCNodeLifecycle)
	if err == nil || !strings.Contains(err.Error(), "seed join unavailable") {
		t.Fatalf("matched error = %v, want seed join unavailable", err)
	}
	if err := gofailClusterNetServiceFault("node_lifecycle:seed join unavailable", RPCControlWrite); err != nil {
		t.Fatalf("non-matching service error = %v, want nil", err)
	}
	if err := gofailClusterNetServiceFault("all:boom", RPCControlWrite); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("all match error = %v, want boom", err)
	}
	if err := gofailClusterNetServiceFault("malformed", RPCControlWrite); !errors.Is(err, errGofailClusterNetFault) {
		t.Fatalf("malformed error = %v, want errGofailClusterNetFault", err)
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
GOWORK=off go test ./pkg/clusterv2/net -run 'TestTransportGofailMarkersStayInSource|TestGofailClusterNetServiceFaultMatchesAlias' -count=1
```

Expected: FAIL because the markers and parser do not exist.

- [ ] **Step 3: Add helper functions and imports**

Modify `pkg/clusterv2/net/transport.go` imports to include:

```go
import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2"
)
```

Add helpers near `servicePriority`:

```go
var errGofailClusterNetFault = errors.New("clusterv2/net: gofail injected service fault")

func gofailClusterNetServiceFault(raw string, serviceID uint8) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	alias, message, ok := strings.Cut(raw, ":")
	if !ok {
		return fmt.Errorf("%w: %s", errGofailClusterNetFault, raw)
	}
	alias = strings.TrimSpace(alias)
	message = strings.TrimSpace(message)
	if alias != "all" && alias != transportServiceAlias(serviceID) {
		return nil
	}
	if message == "" {
		message = "injected"
	}
	return fmt.Errorf("%w: %s", errGofailClusterNetFault, message)
}
```

- [ ] **Step 4: Add failpoint comments to typed RPC client methods**

At the start of `CallShard`, before `c.client.Call(...)`:

```go
// gofail: var wkClusterNetCallShardFault string
// if err := gofailClusterNetServiceFault(wkClusterNetCallShardFault, serviceID); err != nil { return nil, err }
```

At the start of `CallShardOwned`, before `c.client.CallOwned(...)`:

```go
// gofail: var wkClusterNetCallShardOwnedFault string
// if err := gofailClusterNetServiceFault(wkClusterNetCallShardOwnedFault, serviceID); err != nil { return nil, err }
```

At the start of `Send`, before notify calls:

```go
// gofail: var wkClusterNetSendFault string
// if err := gofailClusterNetServiceFault(wkClusterNetSendFault, serviceID); err != nil { return err }
```

At the start of `SendOwned`, before notify calls:

```go
// gofail: var wkClusterNetSendOwnedFault string
// if err := gofailClusterNetServiceFault(wkClusterNetSendOwnedFault, serviceID); err != nil { return err }
```

- [ ] **Step 5: Verify normal clusterv2 net tests**

Run:

```bash
GOWORK=off go test ./pkg/clusterv2/net -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit Task 4**

```bash
git add pkg/clusterv2/net/transport.go pkg/clusterv2/net/gofail_markers_test.go
git commit -m "test: add clusterv2 rpc failpoints"
```

---
### Task 5: Add Opt-In e2ev2 Gofail Smoke for `cmd/wukongimv2`

**Files:**
- Create: `test/e2ev2/cluster/dynamic_node_faults/AGENTS.md`
- Create: `test/e2ev2/cluster/dynamic_node_faults/gofail_smoke_test.go`

- [ ] **Step 1: Create package AGENTS rules**

Create `test/e2ev2/cluster/dynamic_node_faults/AGENTS.md`:

```markdown
# dynamic_node_faults AGENTS

This package contains opt-in gofail-backed internalv2 dynamic-node fault tests.

## Scenario Contract

- Tests must use a gofail-enabled `cmd/wukongimv2` binary supplied through `WK_E2EV2_BINARY`.
- Tests must be skipped unless `WK_E2EV2_GOFAIL_DYNAMIC_NODE=1`.
- Each node gets its own loopback `GOFAIL_HTTP` endpoint through `suite.WithNodeEnv`.
- Faults are controlled through the gofail HTTP endpoint and disabled before test cleanup when possible.
- Tests remain black-box: use manager HTTP, WKProto, and process handles only.

## Running

```bash
scripts/build-gofail-binary.sh --cmd ./cmd/wukongimv2 --package pkg/clusterv2/tasks --package pkg/clusterv2/net --out /tmp/wukongimv2-gofail
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 10m -p=1
```
```

- [ ] **Step 2: Write the opt-in smoke test**

Create `test/e2ev2/cluster/dynamic_node_faults/gofail_smoke_test.go`:

```go
//go:build e2e

package dynamic_node_faults

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

const gofailDynamicNodeEnv = "WK_E2EV2_GOFAIL_DYNAMIC_NODE"

func requireGofailDynamicNodeEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv(gofailDynamicNodeEnv) != "1" {
		t.Skipf("set %s=1 and WK_E2EV2_BINARY to a gofail-enabled cmd/wukongimv2 binary", gofailDynamicNodeEnv)
	}
	if strings.TrimSpace(os.Getenv("WK_E2EV2_BINARY")) == "" {
		t.Skip("WK_E2EV2_BINARY must point at a gofail-enabled cmd/wukongimv2 binary")
	}
}

func TestGofailDynamicNodeBinaryExposesFailpoints(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	node1Fail := suite.ReserveGofailEndpoint(t)
	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken("e2ev2-gofail-smoke-token"),
		suite.WithNodeEnv(1, node1Fail.Env()),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	body, err := node1Fail.WaitListed(ctx,
		"wkSlotReplicaMovePromoteLearnerDelay",
		"wkSlotReplicaMoveTransferLeaderDelay",
		"wkSlotReplicaMoveRemoveVoterDelay",
		"wkClusterNetCallShardFault",
		"wkClusterNetCallShardOwnedFault",
		"wkClusterNetSendFault",
		"wkClusterNetSendOwnedFault",
	)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Contains(t, body, "wkClusterNetSendOwnedFault=")
}
```

- [ ] **Step 3: Verify normal skip behavior**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 2m -p=1
```

Expected: PASS with the package skipped because `WK_E2EV2_GOFAIL_DYNAMIC_NODE` is unset.

- [ ] **Step 4: Build a gofail-enabled wukongimv2 binary**

Run:

```bash
scripts/build-gofail-binary.sh --cmd ./cmd/wukongimv2 --package pkg/clusterv2/tasks --package pkg/clusterv2/net --out /tmp/wukongimv2-gofail
```

Expected: binary created at `/tmp/wukongimv2-gofail`.

- [ ] **Step 5: Verify failpoint exposure**

Run:

```bash
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run TestGofailDynamicNodeBinaryExposesFailpoints -count=1 -timeout 4m -p=1
```

Expected: PASS and failpoints listed through `GOFAIL_HTTP`.

- [ ] **Step 6: Commit Task 5**

```bash
git add test/e2ev2/cluster/dynamic_node_faults/AGENTS.md test/e2ev2/cluster/dynamic_node_faults/gofail_smoke_test.go
git commit -m "test: smoke gofail dynamic node binary"
```

---
### Task 6: Reproduce Slow Slot Replica Move Convergence with gofail

**Files:**
- Create: `test/e2ev2/cluster/dynamic_node_faults/slot_replica_move_fault_test.go`

- [ ] **Step 1: Write the failing e2e scenario**

Create `test/e2ev2/cluster/dynamic_node_faults/slot_replica_move_fault_test.go`:

```go
//go:build e2e

package dynamic_node_faults

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestSlotReplicaMoveSurvivesDelayedLeaderTransfer(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2ev2-gofail-slot-move-token"
	node1Fail := suite.ReserveGofailEndpoint(t)
	node2Fail := suite.ReserveGofailEndpoint(t)
	node3Fail := suite.ReserveGofailEndpoint(t)
	node4Fail := suite.ReserveGofailEndpoint(t)

	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
		suite.WithNodeEnv(1, node1Fail.Env()),
		suite.WithNodeEnv(2, node2Fail.Env()),
		suite.WithNodeEnv(3, node3Fail.Env()),
		suite.WithNodeEnv(4, node4Fail.Env()),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	manager := cluster.ManagerClient(t, 1)
	node4 := cluster.StartSeedJoinNode(t, suite.SeedJoinNodeConfig{
		NodeID:    4,
		Seeds:     cluster.SeedAddrs(),
		JoinToken: joinToken,
	})
	manager.EventuallyNodeJoinState(t, 4, "joining", 20*time.Second)
	manager.EventuallyNodeReadiness(t, 4, true, 20*time.Second)
	manager.MustActivateNode(t, 4)
	manager.EventuallyNodeJoinState(t, 4, "active", 20*time.Second)

	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	failCtx, cancelFail := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelFail()
	for _, endpoint := range []suite.GofailEndpoint{node1Fail, node2Fail, node3Fail} {
		require.NoError(t, endpoint.Enable(failCtx, "wkSlotReplicaMoveTransferLeaderDelay", `return("700ms")`), cluster.DumpDiagnostics())
		defer func(endpoint suite.GofailEndpoint) {
			disableCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = endpoint.Disable(disableCtx, "wkSlotReplicaMoveTransferLeaderDelay")
		}(endpoint)
	}

	plan := manager.MustPlanOnboarding(t, 4, 1)
	require.Len(t, plan.Candidates, 1, cluster.DumpDiagnostics())
	start := manager.MustStartOnboarding(t, 4, 1)
	require.Equal(t, uint32(1), start.Created, cluster.DumpDiagnostics())

	manager.EventuallyOnboardingSafe(t, 4, 90*time.Second)
	sender, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = sender.Close() }()
	require.NoError(t, sender.Connect(node4.GatewayAddr(), "gofail-slot-move-sender", "gofail-slot-move-device"), cluster.DumpDiagnostics())
}
```

- [ ] **Step 2: Run with current failpoints and verify the scenario is meaningful**

Run:

```bash
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run TestSlotReplicaMoveSurvivesDelayedLeaderTransfer -count=1 -timeout 6m -p=1
```

Expected before the Stage 6 follow-up fix: FAIL with `slot replica move leader transfer observation timed out`. Expected after the Stage 6 follow-up fix: PASS because the task remains active and later reconcile observes convergence.

- [ ] **Step 3: Strengthen assertions with task status diagnostics**

After `manager.EventuallyOnboardingSafe`, add:

```go
statusCtx, cancelStatus := context.WithTimeout(context.Background(), 5*time.Second)
defer cancelStatus()
status, err := manager.NodeOnboardingStatus(statusCtx, 4)
require.NoError(t, err, cluster.DumpDiagnostics())
require.Equal(t, uint32(0), status.Summary.Failed, "status=%#v\n%s", status, cluster.DumpDiagnostics())
```

- [ ] **Step 4: Verify the focused scenario**

Run:

```bash
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run TestSlotReplicaMoveSurvivesDelayedLeaderTransfer -count=1 -timeout 6m -p=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 6**

```bash
git add test/e2ev2/cluster/dynamic_node_faults/slot_replica_move_fault_test.go
git commit -m "test: cover delayed replica move transfer"
```

---
### Task 7: Add Node Lifecycle and Control Write RPC Fault Scenarios

**Files:**
- Create: `test/e2ev2/cluster/dynamic_node_faults/node_lifecycle_rpc_fault_test.go`

- [ ] **Step 1: Write seed-join retry scenario**

Create `test/e2ev2/cluster/dynamic_node_faults/node_lifecycle_rpc_fault_test.go`:

```go
//go:build e2e

package dynamic_node_faults

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestSeedJoinRetriesThroughInjectedNodeLifecycleRPCFault(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2ev2-gofail-node-lifecycle-token"
	node1Fail := suite.ReserveGofailEndpoint(t)
	node2Fail := suite.ReserveGofailEndpoint(t)
	node3Fail := suite.ReserveGofailEndpoint(t)

	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
		suite.WithNodeEnv(1, node1Fail.Env()),
		suite.WithNodeEnv(2, node2Fail.Env()),
		suite.WithNodeEnv(3, node3Fail.Env()),
	)
	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	failCtx, cancelFail := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFail()
	require.NoError(t, node1Fail.Enable(failCtx, "wkClusterNetCallShardFault", `return("node_lifecycle:temporary join rpc fault")`), cluster.DumpDiagnostics())

	manager := cluster.ManagerClient(t, 1)
	node4 := cluster.StartSeedJoinNodeNoWait(t, suite.SeedJoinNodeConfig{
		NodeID:    4,
		Seeds:     []string{cluster.NodeAddr(1)},
		JoinToken: joinToken,
	})

	time.Sleep(700 * time.Millisecond)
	disableCtx, cancelDisable := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelDisable()
	require.NoError(t, node1Fail.Disable(disableCtx, "wkClusterNetCallShardFault"), cluster.DumpDiagnostics())

	httpCtx, cancelHTTP := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelHTTP()
	require.NoError(t, suite.WaitHTTPReady(httpCtx, node4.APIAddr(), "/readyz"), cluster.DumpDiagnostics())
	manager.EventuallyNodeJoinState(t, 4, "joining", 30*time.Second)
	manager.EventuallyNodeReadiness(t, 4, true, 20*time.Second)
}

func TestOnboardingControlWriteRetriesThroughInjectedRPCFault(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2ev2-gofail-control-write-token"
	node1Fail := suite.ReserveGofailEndpoint(t)
	node2Fail := suite.ReserveGofailEndpoint(t)
	node3Fail := suite.ReserveGofailEndpoint(t)

	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
		suite.WithNodeEnv(1, node1Fail.Env()),
		suite.WithNodeEnv(2, node2Fail.Env()),
		suite.WithNodeEnv(3, node3Fail.Env()),
	)
	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	manager := cluster.ManagerClient(t, 1)
	cluster.StartSeedJoinNode(t, suite.SeedJoinNodeConfig{
		NodeID:    4,
		Seeds:     cluster.SeedAddrs(),
		JoinToken: joinToken,
	})
	manager.EventuallyNodeJoinState(t, 4, "joining", 20*time.Second)
	manager.EventuallyNodeReadiness(t, 4, true, 20*time.Second)
	manager.MustActivateNode(t, 4)
	manager.EventuallyNodeJoinState(t, 4, "active", 20*time.Second)

	failCtx, cancelFail := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFail()
	for _, endpoint := range []suite.GofailEndpoint{node1Fail, node2Fail, node3Fail} {
		require.NoError(t, endpoint.Enable(failCtx, "wkClusterNetCallShardOwnedFault", `return("control_write:temporary control write fault")`), cluster.DumpDiagnostics())
	}

	start := manager.MustStartOnboarding(t, 4, 1)
	require.Equal(t, uint32(0), start.Created, "control write fault should prevent immediate task creation: %#v", start)

	disableCtx, cancelDisable := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelDisable()
	for _, endpoint := range []suite.GofailEndpoint{node1Fail, node2Fail, node3Fail} {
		require.NoError(t, endpoint.Disable(disableCtx, "wkClusterNetCallShardOwnedFault"), cluster.DumpDiagnostics())
	}

	start = manager.MustStartOnboarding(t, 4, 1)
	require.Equal(t, uint32(1), start.Created, cluster.DumpDiagnostics())
	manager.EventuallyOnboardingSafe(t, 4, 90*time.Second)
}
```

- [ ] **Step 2: Run focused RPC fault scenarios**

Run:

```bash
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run 'TestSeedJoinRetriesThroughInjectedNodeLifecycleRPCFault|TestOnboardingControlWriteRetriesThroughInjectedRPCFault' -count=1 -timeout 8m -p=1
```

Expected: PASS. If `TestOnboardingControlWriteRetriesThroughInjectedRPCFault` returns an HTTP error instead of `Created=0`, adjust the assertion to require the manager's bounded error response and then retry after disabling the failpoint.

- [ ] **Step 3: Commit Task 7**

```bash
git add test/e2ev2/cluster/dynamic_node_faults/node_lifecycle_rpc_fault_test.go
git commit -m "test: cover dynamic node rpc faults"
```

---
### Task 8: Add Restart During Onboarding Recovery

**Files:**
- Create: `test/e2ev2/cluster/dynamic_node_faults/restart_during_onboarding_test.go`
- Modify: `test/e2ev2/suite/runtime.go`

- [ ] **Step 1: Add restart helper tests or reuse process handles directly**

Prefer the smallest helper in `test/e2ev2/suite/runtime.go`:

```go
// Restart stops and starts the same process spec with the cluster binary path.
func (n *StartedNode) Restart(binaryPath string) error {
	if n == nil || n.Process == nil {
		return fmt.Errorf("started node is not running")
	}
	spec := n.Spec
	if err := n.Process.Stop(); err != nil {
		return err
	}
	process := &NodeProcess{Spec: spec, BinaryPath: binaryPath}
	if err := process.Start(); err != nil {
		return err
	}
	n.Process = process
	return nil
}
```

Add this method on `StartedCluster` so tests do not reach into private fields:

```go
// RestartNode restarts one node with its existing spec and the cluster binary.
func (c *StartedCluster) RestartNode(nodeID uint64) error {
	node, ok := c.Node(nodeID)
	if !ok {
		return fmt.Errorf("node %d not found", nodeID)
	}
	return node.Restart(c.binaryPath)
}
```

- [ ] **Step 2: Write the restart e2e**

Create `test/e2ev2/cluster/dynamic_node_faults/restart_during_onboarding_test.go`:

```go
//go:build e2e

package dynamic_node_faults

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestJoiningNodeRestartDuringOnboardingRecovers(t *testing.T) {
	requireGofailDynamicNodeEnabled(t)

	const joinToken = "e2ev2-gofail-restart-token"
	node1Fail := suite.ReserveGofailEndpoint(t)
	node2Fail := suite.ReserveGofailEndpoint(t)
	node3Fail := suite.ReserveGofailEndpoint(t)
	node4Fail := suite.ReserveGofailEndpoint(t)

	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithDynamicJoinToken(joinToken),
		suite.WithNodeEnv(1, node1Fail.Env()),
		suite.WithNodeEnv(2, node2Fail.Env()),
		suite.WithNodeEnv(3, node3Fail.Env()),
		suite.WithNodeEnv(4, node4Fail.Env()),
	)
	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	manager := cluster.ManagerClient(t, 1)
	cluster.StartSeedJoinNode(t, suite.SeedJoinNodeConfig{
		NodeID:    4,
		Seeds:     cluster.SeedAddrs(),
		JoinToken: joinToken,
	})
	manager.EventuallyNodeJoinState(t, 4, "joining", 20*time.Second)
	manager.EventuallyNodeReadiness(t, 4, true, 20*time.Second)
	manager.MustActivateNode(t, 4)
	manager.EventuallyNodeJoinState(t, 4, "active", 20*time.Second)

	failCtx, cancelFail := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFail()
	require.NoError(t, node4Fail.Enable(failCtx, "wkSlotReplicaMovePromoteLearnerDelay", `return("2s")`), cluster.DumpDiagnostics())
	start := manager.MustStartOnboarding(t, 4, 1)
	require.Equal(t, uint32(1), start.Created, cluster.DumpDiagnostics())

	time.Sleep(500 * time.Millisecond)
	require.NoError(t, cluster.RestartNode(4), cluster.DumpDiagnostics())
	disableCtx, cancelDisable := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelDisable()
	_ = node4Fail.Disable(disableCtx, "wkSlotReplicaMovePromoteLearnerDelay")

	readyAfterRestart, cancelAfterRestart := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelAfterRestart()
	require.NoError(t, cluster.WaitClusterReady(readyAfterRestart), cluster.DumpDiagnostics())
	manager.EventuallyOnboardingSafe(t, 4, 90*time.Second)
}
```

- [ ] **Step 3: Run the focused restart scenario**

Run:

```bash
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run TestJoiningNodeRestartDuringOnboardingRecovers -count=1 -timeout 8m -p=1
```

Expected: PASS. If restart exposes stale `GOFAIL_HTTP` endpoint state, update `RestartNode` diagnostics to include the node's env and re-reserve only when the process spec changes.

- [ ] **Step 4: Commit Task 8**

```bash
git add test/e2ev2/suite/runtime.go test/e2ev2/cluster/dynamic_node_faults/restart_during_onboarding_test.go
git commit -m "test: cover restart during onboarding"
```

---
### Task 9: Catalog Docs and Final Verification

**Files:**
- Modify: `test/e2ev2/AGENTS.md`
- Modify: `test/e2ev2/cluster/AGENTS.md`
- Modify: `test/e2ev2/cluster/dynamic_node_faults/AGENTS.md`

- [ ] **Step 1: Update e2ev2 catalogs**

Add `dynamic_node_faults` to `test/e2ev2/cluster/AGENTS.md` with this command:

```bash
scripts/build-gofail-binary.sh --cmd ./cmd/wukongimv2 --package pkg/clusterv2/tasks --package pkg/clusterv2/net --out /tmp/wukongimv2-gofail
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 10m -p=1
```

Add the same package row to `test/e2ev2/AGENTS.md`.

- [ ] **Step 2: Run default non-gofail checks**

Run:

```bash
GOWORK=off go test ./scripts ./pkg/clusterv2/tasks ./pkg/clusterv2/net -count=1
GOWORK=off go test -tags=e2e ./test/e2ev2/suite ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 2m -p=1
```

Expected: PASS. The `dynamic_node_faults` package should skip fault tests when opt-in env is absent.

- [ ] **Step 3: Build the gofail-enabled internalv2 binary**

Run:

```bash
scripts/build-gofail-binary.sh --cmd ./cmd/wukongimv2 --package pkg/clusterv2/tasks --package pkg/clusterv2/net --out /tmp/wukongimv2-gofail
```

Expected: PASS and `/tmp/wukongimv2-gofail` exists.

- [ ] **Step 4: Run opt-in gofail e2ev2**

Run:

```bash
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 10m -p=1
```

Expected: PASS.

- [ ] **Step 5: Run existing dynamic-node regression package**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_join -count=1 -timeout 10m -p=1
```

Expected: PASS.

- [ ] **Step 6: Run full serial e2ev2**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/... -count=1 -timeout 10m -p=1
```

Expected: PASS. Opt-in gofail tests should skip unless the env is set.

- [ ] **Step 7: Finish with repository checks**

Run:

```bash
git diff --check
git status --short --branch
```

Expected: no whitespace errors and only intentional files before the final commit.

- [ ] **Step 8: Commit docs and final verification notes**

```bash
git add test/e2ev2/AGENTS.md test/e2ev2/cluster/AGENTS.md test/e2ev2/cluster/dynamic_node_faults/AGENTS.md
git commit -m "docs: catalog dynamic node fault tests"
```

---

## Root Cause Targets

Stage 7 should make these failure modes deterministic:

1. Slot replica move async phase convergence:
   - `promote_learner` delay
   - leader-transfer delay before `remove_voter`
   - `remove_voter` config-change delay

2. Dynamic node lifecycle transport failures:
   - seed join `node_lifecycle` RPC temporary failure
   - Controller generic `control_write` RPC temporary failure

3. Process recovery:
   - node 4 restart while onboarding task remains active
   - no duplicate task creation after restart
   - manager status eventually reports `safe` and `failed=0`

## Execution Order

1. Implement Tasks 1-2 first; they are shared infrastructure and should not touch production runtime code.
2. Implement Task 3 and run the slot replica move gofail smoke before adding RPC failpoints.
3. Implement Task 4 only after Task 3 is stable; RPC failpoints affect more paths.
4. Implement Tasks 5-6 as Stage 7A and run the opt-in package.
5. Implement Tasks 7-8 as Stage 7B after Stage 7A passes at least twice.
6. Run Task 9 final verification before claiming the stage complete.

## Open Risk Controls

- Keep all gofail e2e tests opt-in so default CI and normal developer runs remain stable.
- Use loopback-only `GOFAIL_HTTP` addresses allocated by the test process.
- Disable failpoints in `defer` where a node remains alive after the assertion.
- Avoid broad random sleeps in tests; failpoint delays should create the window, and public manager status polling should observe recovery.
- Do not add production `WK_*` configuration for failpoints.
