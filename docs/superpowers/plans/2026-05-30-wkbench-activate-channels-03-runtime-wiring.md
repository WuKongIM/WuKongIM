# wkbench Activate Channels 03 Runtime Wiring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire ChannelV2 runtime bench controls from `pkg/channelv2` through `pkg/clusterv2`, `internalv2/infra/cluster`, and `internalv2/app` so the internalv2 bench HTTP API can use real runtime data.

**Architecture:** `pkg/clusterv2/channels.Service` delegates runtime snapshot/probe/evict to the hosted ChannelV2 runtime from Phase 02. `pkg/clusterv2.Node` exposes a public node-scoped facade that keeps lifecycle checks and local node identity in one place. `internalv2/infra/cluster` adapts clusterv2 runtime DTOs to `internal/bench/model` HTTP DTOs, including benchmark run/profile range expansion.

**Tech Stack:** Go, `pkg/channelv2`, `pkg/clusterv2`, `internalv2/infra/cluster`, `internalv2/app`, `internalv2/access/api`, `httptest`, `GOWORK=off go test`.

---

## Files

- Create: `pkg/clusterv2/channels/bench_runtime.go`
- Create: `pkg/clusterv2/channels/bench_runtime_test.go`
- Create: `pkg/clusterv2/node_channel_runtime.go`
- Create: `pkg/clusterv2/node_channel_runtime_test.go`
- Create: `internalv2/infra/cluster/bench_runtime.go`
- Create: `internalv2/infra/cluster/bench_runtime_test.go`
- Modify: `pkg/clusterv2/node.go`
- Modify: `pkg/clusterv2/FLOW.md`
- Modify: `internalv2/infra/cluster/FLOW.md`
- Modify: `internalv2/app/app.go`
- Modify: `internalv2/app/app_test.go`
- Modify: `internalv2/app/FLOW.md`

## Task 1: clusterv2 Channel Service Facade

**Files:**

- Create: `pkg/clusterv2/channels/bench_runtime.go`
- Create: `pkg/clusterv2/channels/bench_runtime_test.go`

- [ ] **Step 1: Add failing channel service tests**

Add tests:

```go
func TestServiceRuntimeSnapshotDelegatesToRuntimeBench(t *testing.T)
func TestServiceRuntimeProbeDelegatesToRuntimeBench(t *testing.T)
func TestServiceRuntimeEvictDelegatesToRuntimeBench(t *testing.T)
func TestServiceRuntimeBenchUnsupported(t *testing.T)
```

Use a fake runtime that implements `channelv2.Cluster`, `channelv2.RuntimeBench`, and `channelv2/transport.Server`. Assert the fake receives the selector and that unsupported runtimes return `channelv2.ErrInvalidConfig`.

- [ ] **Step 2: Run failing tests**

```bash
GOWORK=off go test ./pkg/clusterv2/channels -count=1
```

Expected:

```text
FAIL
... RuntimeSnapshot undefined ...
```

- [ ] **Step 3: Implement service methods**

Create:

```go
package channels

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

func (s *Service) RuntimeSnapshot(ctx context.Context) (ch.RuntimeSnapshot, error) {
	bench, ok := s.runtime.(ch.RuntimeBench)
	if !ok {
		return ch.RuntimeSnapshot{}, ch.ErrInvalidConfig
	}
	return bench.RuntimeSnapshot(ctx)
}

func (s *Service) RuntimeProbe(ctx context.Context, selector ch.RuntimeSelector) (ch.RuntimeProbeResult, error) {
	bench, ok := s.runtime.(ch.RuntimeBench)
	if !ok {
		return ch.RuntimeProbeResult{}, ch.ErrInvalidConfig
	}
	return bench.RuntimeProbe(ctx, selector)
}

func (s *Service) RuntimeEvict(ctx context.Context, selector ch.RuntimeSelector) (ch.RuntimeEvictResult, error) {
	bench, ok := s.runtime.(ch.RuntimeBench)
	if !ok {
		return ch.RuntimeEvictResult{}, ch.ErrInvalidConfig
	}
	return bench.RuntimeEvict(ctx, selector)
}
```

- [ ] **Step 4: Verify**

```bash
GOWORK=off go test ./pkg/clusterv2/channels -count=1
```

Expected:

```text
ok  	github.com/WuKongIM/WuKongIM/pkg/clusterv2/channels
```

## Task 2: clusterv2 Node Facade

**Files:**

- Create: `pkg/clusterv2/node_channel_runtime.go`
- Create: `pkg/clusterv2/node_channel_runtime_test.go`
- Modify: `pkg/clusterv2/node.go`

- [ ] **Step 1: Extend the private channel service interface**

In `node.go`, add these methods to `channelService`:

```go
RuntimeSnapshot(context.Context) (channelv2.RuntimeSnapshot, error)
RuntimeProbe(context.Context, channelv2.RuntimeSelector) (channelv2.RuntimeProbeResult, error)
RuntimeEvict(context.Context, channelv2.RuntimeSelector) (channelv2.RuntimeEvictResult, error)
```

- [ ] **Step 2: Add failing node facade tests**

Add:

```go
func TestNodeChannelRuntimeSnapshotDelegatesToChannels(t *testing.T)
func TestNodeChannelRuntimeProbeDelegatesToChannels(t *testing.T)
func TestNodeChannelRuntimeEvictDelegatesToChannels(t *testing.T)
func TestNodeChannelRuntimeRequiresStartedChannels(t *testing.T)
```

Use `channels.NewService(channels.Config{Runtime: fakeRuntime})`, `New(validNodeConfig(t), WithChannels(service))`, and `node.Start(context.Background())`. Extend the existing `nodeChannelRuntime` test fake with Phase 02 `RuntimeBench` methods.

- [ ] **Step 3: Implement node methods**

Create:

```go
package clusterv2

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

// ChannelRuntimeSnapshot returns local ChannelV2 runtime state for benchmark controllers.
func (n *Node) ChannelRuntimeSnapshot(ctx context.Context) (channelv2.RuntimeSnapshot, error) {
	if err := ctxErr(ctx); err != nil {
		return channelv2.RuntimeSnapshot{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelv2.RuntimeSnapshot{}, err
	}
	if n.channels == nil {
		return channelv2.RuntimeSnapshot{}, ErrNotStarted
	}
	out, err := n.channels.RuntimeSnapshot(ctx)
	if err != nil {
		return channelv2.RuntimeSnapshot{}, err
	}
	if out.NodeID == 0 {
		out.NodeID = channelv2.NodeID(n.cfg.NodeID)
	}
	return out, nil
}

// ChannelRuntimeProbe reports local loaded runtime presence for selected channels.
func (n *Node) ChannelRuntimeProbe(ctx context.Context, selector channelv2.RuntimeSelector) (channelv2.RuntimeProbeResult, error) {
	if err := ctxErr(ctx); err != nil {
		return channelv2.RuntimeProbeResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelv2.RuntimeProbeResult{}, err
	}
	if n.channels == nil {
		return channelv2.RuntimeProbeResult{}, ErrNotStarted
	}
	return n.channels.RuntimeProbe(ctx, selector)
}

// ChannelRuntimeEvict evicts selected safe local runtime state without deleting durable data.
func (n *Node) ChannelRuntimeEvict(ctx context.Context, selector channelv2.RuntimeSelector) (channelv2.RuntimeEvictResult, error) {
	if err := ctxErr(ctx); err != nil {
		return channelv2.RuntimeEvictResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return channelv2.RuntimeEvictResult{}, err
	}
	if n.channels == nil {
		return channelv2.RuntimeEvictResult{}, ErrNotStarted
	}
	return n.channels.RuntimeEvict(ctx, selector)
}
```

- [ ] **Step 4: Verify**

```bash
GOWORK=off go test ./pkg/clusterv2 -run 'TestNodeChannelRuntime|TestNodeAppendChannel|TestNodeStopClosesChannelService' -count=1
```

Expected:

```text
ok  	github.com/WuKongIM/WuKongIM/pkg/clusterv2
```

## Task 3: internalv2 infra Adapter

**Files:**

- Create: `internalv2/infra/cluster/bench_runtime.go`
- Create: `internalv2/infra/cluster/bench_runtime_test.go`

- [ ] **Step 1: Add adapter tests**

Add:

```go
func TestChannelRuntimeBenchControllerMapsSnapshot(t *testing.T)
func TestChannelRuntimeBenchControllerExpandsProbeRange(t *testing.T)
func TestChannelRuntimeBenchControllerMapsEvictResult(t *testing.T)
```

Probe range test expectation:

```go
[]channelv2.ChannelID{
	{ID: "run-a-activate-groups-2", Type: 2},
	{ID: "run-a-activate-groups-3", Type: 2},
	{ID: "run-a-activate-groups-4", Type: 2},
}
```

- [ ] **Step 2: Implement adapter**

Create:

```go
package cluster

import (
	"context"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

type ChannelRuntimeBenchNode interface {
	NodeID() uint64
	ChannelRuntimeSnapshot(context.Context) (channelv2.RuntimeSnapshot, error)
	ChannelRuntimeProbe(context.Context, channelv2.RuntimeSelector) (channelv2.RuntimeProbeResult, error)
	ChannelRuntimeEvict(context.Context, channelv2.RuntimeSelector) (channelv2.RuntimeEvictResult, error)
}

type ChannelRuntimeBenchController struct {
	node ChannelRuntimeBenchNode
}

func NewChannelRuntimeBenchController(node ChannelRuntimeBenchNode) *ChannelRuntimeBenchController {
	return &ChannelRuntimeBenchController{node: node}
}
```

Implement `Snapshot`, `Probe`, and `Evict` methods matching `internalv2/access/api.ChannelRuntimeBenchController`. Mapping rules:

- Set `Version` to `bench/v1`.
- Set `NodeID` from runtime snapshot; if zero, use `node.NodeID()`.
- Copy `RunID` and `Profile` from the query.
- Expand ids as `fmt.Sprintf("%s-%s-%d", runID, profile, index)`.
- Map missing `channelv2.ChannelID` values to their `ID` strings.

- [ ] **Step 3: Verify**

```bash
GOWORK=off go test ./internalv2/infra/cluster -count=1
```

Expected:

```text
ok  	github.com/WuKongIM/WuKongIM/internalv2/infra/cluster
```

## Task 4: internalv2 App Wiring

**Files:**

- Modify: `internalv2/app/app.go`
- Modify: `internalv2/app/app_test.go`

- [ ] **Step 1: Add app wiring test**

Add:

```go
func TestNewWiresBenchRuntimeControllerWhenClusterSupportsIt(t *testing.T)
```

Construct `New(Config{API: APIConfig{ListenAddr: "127.0.0.1:0"}, Bench: BenchConfig{APIEnabled: true}}, WithCluster(fakeRuntimeBenchCluster))`. Type assert `app.api.(*accessapi.Server)`, call its handler for `/bench/v1/capabilities`, and assert `channel_runtime_snapshot`, `channel_runtime_probe`, and `channel_runtime_evict` are true.

- [ ] **Step 2: Implement wiring helper**

In `app.go`, add:

```go
func (a *App) benchRuntimeController() accessapi.ChannelRuntimeBenchController {
	node, ok := a.cluster.(clusterinfra.ChannelRuntimeBenchNode)
	if !ok {
		return nil
	}
	return clusterinfra.NewChannelRuntimeBenchController(node)
}
```

Pass it into `accessapi.New`:

```go
BenchRuntime: app.benchRuntimeController(),
```

- [ ] **Step 3: Verify**

```bash
GOWORK=off go test ./internalv2/app -run 'TestNewWiresBenchRuntimeController|TestStartOrderIncludesAPI' -count=1
```

Expected:

```text
ok  	github.com/WuKongIM/WuKongIM/internalv2/app
```

## Task 5: FLOW Updates

**Files:**

- Modify: `pkg/clusterv2/FLOW.md`
- Modify: `internalv2/infra/cluster/FLOW.md`
- Modify: `internalv2/app/FLOW.md`

- [ ] **Step 1: Document wiring path**

Add one short paragraph to each file:

```markdown
Bench runtime controls flow from internalv2 HTTP through `internalv2/infra/cluster`, `pkg/clusterv2.Node`, `pkg/clusterv2/channels.Service`, and finally the hosted ChannelV2 runtime. These routes are benchmark-only observation/cleanup controls and do not replace the gateway SEND activation path.
```

- [ ] **Step 2: Verify docs**

```bash
rg -n "Bench runtime controls" pkg/clusterv2/FLOW.md internalv2/infra/cluster/FLOW.md internalv2/app/FLOW.md
```

Expected: all three files match.

## Phase 03 Verification And Commit

- [ ] **Step 1: Run all phase tests**

```bash
GOWORK=off go test ./pkg/clusterv2/channels ./pkg/clusterv2 ./internalv2/infra/cluster ./internalv2/app -count=1
```

Expected:

```text
ok  	github.com/WuKongIM/WuKongIM/pkg/clusterv2/channels
ok  	github.com/WuKongIM/WuKongIM/pkg/clusterv2
ok  	github.com/WuKongIM/WuKongIM/internalv2/infra/cluster
ok  	github.com/WuKongIM/WuKongIM/internalv2/app
```

- [ ] **Step 2: Commit**

```bash
git add pkg/clusterv2/channels/bench_runtime.go pkg/clusterv2/channels/bench_runtime_test.go pkg/clusterv2/node.go pkg/clusterv2/node_channel_runtime.go pkg/clusterv2/node_channel_runtime_test.go internalv2/infra/cluster/bench_runtime.go internalv2/infra/cluster/bench_runtime_test.go internalv2/app/app.go internalv2/app/app_test.go pkg/clusterv2/FLOW.md internalv2/infra/cluster/FLOW.md internalv2/app/FLOW.md
git commit -m "feat(wukongimv2): wire channel runtime bench controller"
```
