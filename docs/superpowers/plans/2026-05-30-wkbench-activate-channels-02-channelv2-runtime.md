# wkbench Activate Channels 02 ChannelV2 Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add ChannelV2 runtime snapshot, probe, and safe evict operations that can later power the bench HTTP API without exposing reactor internals.

**Architecture:** `pkg/channelv2` owns runtime-shaped DTOs and imports no `internal/bench` packages. `pkg/channelv2/reactor` collects state by sending runtime events to each owning reactor, preserving single-writer map ownership. `pkg/channelv2/service` exposes the same operations on the service facade so clusterv2 wiring can depend on a narrow public interface in the next phase.

**Tech Stack:** Go, `pkg/channelv2`, `pkg/channelv2/reactor`, `pkg/channelv2/service`, in-memory channelv2 stores, `GOWORK=off go test`.

---

## Files

- Create: `pkg/channelv2/bench_runtime.go`
- Create: `pkg/channelv2/reactor/bench_runtime.go`
- Create: `pkg/channelv2/reactor/bench_runtime_test.go`
- Create: `pkg/channelv2/service/bench_runtime.go`
- Create: `pkg/channelv2/service/bench_runtime_test.go`
- Modify: `pkg/channelv2/reactor/event.go`
- Modify: `pkg/channelv2/reactor/future.go`
- Modify: `pkg/channelv2/reactor/metrics.go`
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/FLOW.md`
- Modify: `pkg/channelv2/reactor/FLOW.md`

## Task 1: Runtime DTOs

**Files:**

- Create: `pkg/channelv2/bench_runtime.go`

- [ ] **Step 1: Add public runtime DTOs**

Create:

```go
package channelv2

import "context"

// RuntimeBench exposes benchmark-only runtime observation and cleanup.
type RuntimeBench interface {
	RuntimeSnapshot(context.Context) (RuntimeSnapshot, error)
	RuntimeProbe(context.Context, RuntimeSelector) (RuntimeProbeResult, error)
	RuntimeEvict(context.Context, RuntimeSelector) (RuntimeEvictResult, error)
}

// RuntimeSelector selects concrete channel identities. Callers expand benchmark ranges before entering pkg/channelv2.
type RuntimeSelector struct {
	// ChannelIDs are copied by callers before asynchronous use.
	ChannelIDs []ChannelID
}

// RuntimeSnapshot is one node's low-cardinality ChannelV2 runtime view.
type RuntimeSnapshot struct {
	NodeID                  NodeID
	ActiveTotal             int
	ActiveLeader            int
	ActiveFollower          int
	FollowerParked          int
	ActivationRejectedTotal uint64
	Reactors                []RuntimeReactorSnapshot
	WorkerQueues            []RuntimeWorkerQueue
}

// RuntimeReactorSnapshot summarizes one reactor partition.
type RuntimeReactorSnapshot struct {
	ReactorID    int
	Leader       int
	Follower     int
	Parked       int
	MailboxDepth int
}

// RuntimeWorkerQueue summarizes one bounded worker pool queue.
type RuntimeWorkerQueue struct {
	Pool  string
	Depth int
}

// RuntimeProbeResult reports local loaded runtime presence for selected channels.
type RuntimeProbeResult struct {
	Checked        int
	LoadedLeader   int
	LoadedFollower int
	Missing        []ChannelID
}

// RuntimeEvictResult reports local runtime eviction results.
type RuntimeEvictResult struct {
	Requested   int
	Evicted     int
	SkippedBusy int
	Missing     int
}
```

- [ ] **Step 2: Test compile**

Run:

```bash
GOWORK=off go test ./pkg/channelv2 -count=1
```

Expected:

```text
ok  	github.com/WuKongIM/WuKongIM/pkg/channelv2
```

## Task 2: Reactor Runtime Events

**Files:**

- Modify: `pkg/channelv2/reactor/event.go`
- Modify: `pkg/channelv2/reactor/future.go`
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/reactor/metrics.go`

- [ ] **Step 1: Add event and result fields**

In `event.go`, add event kinds after `EventCheckState`:

```go
// EventRuntimeSnapshot asks one reactor to summarize loaded runtime state.
EventRuntimeSnapshot
// EventRuntimeProbe asks one reactor to inspect selected loaded runtimes.
EventRuntimeProbe
// EventRuntimeEvict asks one reactor to evict selected safe runtimes.
EventRuntimeEvict
```

Add selected IDs to `Event`:

```go
RuntimeChannelIDs []ch.ChannelID
```

In `future.go`, extend `Result`:

```go
RuntimeSnapshot ch.RuntimeReactorSnapshot
RuntimeProbe    ch.RuntimeProbeResult
RuntimeEvict    ch.RuntimeEvictResult
```

- [ ] **Step 2: Track local activation rejections**

In `reactor.go`, add to `Reactor`:

```go
activationRejectedTotal uint64
```

In `metrics.go`, increment the local counter before notifying observers:

```go
func (r *Reactor) observeActivationRejected(reason string) {
	if r != nil {
		r.activationRejectedTotal++
	}
	if observer, ok := r.cfg.Observer.(RuntimeObserver); ok {
		observer.ObserveChannelActivationRejected(reason)
	}
}
```

- [ ] **Step 3: Route runtime events**

In `reactor.go`, extend `handle`:

```go
case EventRuntimeSnapshot:
	r.handleRuntimeSnapshot(event)
case EventRuntimeProbe:
	r.handleRuntimeProbe(event)
case EventRuntimeEvict:
	r.handleRuntimeEvict(event)
```

- [ ] **Step 4: Run failing tests**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/reactor -count=1
```

Expected:

```text
FAIL
... handleRuntimeSnapshot undefined ...
```

## Task 3: Reactor Snapshot, Probe, And Evict

**Files:**

- Create: `pkg/channelv2/reactor/bench_runtime.go`
- Create: `pkg/channelv2/reactor/bench_runtime_test.go`

- [ ] **Step 1: Add reactor tests**

Add these test cases:

```go
func TestRuntimeSnapshotCountsLeaderFollowerAndParked(t *testing.T)
func TestRuntimeProbeReportsLoadedAndMissingChannels(t *testing.T)
func TestRuntimeEvictEvictsSafeRuntimeAndSkipsBusyRuntime(t *testing.T)
```

Test setup rules:

- Use existing reactor package test helpers for in-memory stores and metadata.
- Apply one leader meta and one follower meta through `EventApplyMeta`.
- Mark one follower runtime parked by setting its replication parked state inside the reactor package test.
- For busy eviction, create an append waiter on a loaded runtime and assert `SkippedBusy == 1`.

- [ ] **Step 2: Implement group methods**

In `bench_runtime.go`, add:

```go
func (g *Group) RuntimeSnapshot(ctx context.Context) (ch.RuntimeSnapshot, error)
func (g *Group) RuntimeProbe(ctx context.Context, selector ch.RuntimeSelector) (ch.RuntimeProbeResult, error)
func (g *Group) RuntimeEvict(ctx context.Context, selector ch.RuntimeSelector) (ch.RuntimeEvictResult, error)
```

Implementation rules:

- Return `ch.ErrClosed` when the group is nil or closed.
- Snapshot submits `EventRuntimeSnapshot` to every reactor and aggregates totals.
- Probe and evict group selected IDs by `g.router.PickIndex(ch.ChannelKeyForID(id))`.
- Copy `selector.ChannelIDs` before submitting events.
- Worker queue snapshots come from `g.pools.StoreAppend`, `StoreRead`, `StoreApply`, and `RPC` names and queue depths.

- [ ] **Step 3: Implement reactor handlers**

Add handlers:

```go
func (r *Reactor) handleRuntimeSnapshot(event Event)
func (r *Reactor) handleRuntimeProbe(event Event)
func (r *Reactor) handleRuntimeEvict(event Event)
```

Handler rules:

- Snapshot counts `RoleLeader`, `RoleFollower`, parked followers, total mailbox depth, and `activationRejectedTotal`.
- Probe checks `r.channels[ch.ChannelKeyForID(id)]`; loaded leaders/followers are counted by role and absent channels are appended to `Missing`.
- Evict calls existing `r.evictRuntimeChannel(key, rc, "bench runtime evict")`; missing channels increment `Missing`; loaded but not safe channels increment `SkippedBusy`.

- [ ] **Step 4: Run reactor tests**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/reactor -count=1
```

Expected:

```text
ok  	github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor
```

## Task 4: Service Facade

**Files:**

- Create: `pkg/channelv2/service/bench_runtime.go`
- Create: `pkg/channelv2/service/bench_runtime_test.go`

- [ ] **Step 1: Add service tests**

Add:

```go
func TestServiceRuntimeSnapshotDelegatesToGroup(t *testing.T)
func TestServiceRuntimeProbeDelegatesToGroup(t *testing.T)
func TestServiceRuntimeEvictDelegatesToGroup(t *testing.T)
```

Each test should construct `service.New` with an in-memory store, apply metadata through `ApplyMeta`, then call the runtime method on the returned cluster after type asserting `ch.RuntimeBench`.

- [ ] **Step 2: Implement service methods**

Create `bench_runtime.go`:

```go
package service

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

func (c *cluster) RuntimeSnapshot(ctx context.Context) (ch.RuntimeSnapshot, error) {
	return c.group.RuntimeSnapshot(ctx)
}

func (c *cluster) RuntimeProbe(ctx context.Context, selector ch.RuntimeSelector) (ch.RuntimeProbeResult, error) {
	return c.group.RuntimeProbe(ctx, selector)
}

func (c *cluster) RuntimeEvict(ctx context.Context, selector ch.RuntimeSelector) (ch.RuntimeEvictResult, error) {
	return c.group.RuntimeEvict(ctx, selector)
}

var _ ch.RuntimeBench = (*cluster)(nil)
```

- [ ] **Step 3: Run service tests**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/service -count=1
```

Expected:

```text
ok  	github.com/WuKongIM/WuKongIM/pkg/channelv2/service
```

## Task 5: FLOW Documentation

**Files:**

- Modify: `pkg/channelv2/FLOW.md`
- Modify: `pkg/channelv2/reactor/FLOW.md`

- [ ] **Step 1: Document runtime bench path**

Add a short section to `pkg/channelv2/FLOW.md`:

```markdown
## Bench Runtime Observation

`RuntimeBench` exposes snapshot, probe, and safe eviction for controlled benchmark runs. Callers pass concrete `ChannelID` values; benchmark run/profile range expansion happens above `pkg/channelv2` so the runtime package does not depend on wkbench naming rules.
```

Add to `pkg/channelv2/reactor/FLOW.md`:

```markdown
## Bench Runtime Events

Runtime snapshot, probe, and evict requests enter reactors as mailbox events. The owning reactor reads or evicts its local `channels` map, preserving the single-writer rule. Evict only removes loaded runtime state when `safeToEvictRuntime()` is true; it never deletes durable channel metadata or messages.
```

- [ ] **Step 2: Verify docs are readable**

Run:

```bash
rg -n "Bench Runtime" pkg/channelv2/FLOW.md pkg/channelv2/reactor/FLOW.md
```

Expected:

```text
pkg/channelv2/FLOW.md:...
pkg/channelv2/reactor/FLOW.md:...
```

## Phase 02 Verification And Commit

- [ ] **Step 1: Run all phase tests**

```bash
GOWORK=off go test ./pkg/channelv2 ./pkg/channelv2/reactor ./pkg/channelv2/service -count=1
```

Expected:

```text
ok  	github.com/WuKongIM/WuKongIM/pkg/channelv2
ok  	github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor
ok  	github.com/WuKongIM/WuKongIM/pkg/channelv2/service
```

- [ ] **Step 2: Commit**

```bash
git add pkg/channelv2/bench_runtime.go pkg/channelv2/reactor/bench_runtime.go pkg/channelv2/reactor/bench_runtime_test.go pkg/channelv2/reactor/event.go pkg/channelv2/reactor/future.go pkg/channelv2/reactor/metrics.go pkg/channelv2/reactor/reactor.go pkg/channelv2/service/bench_runtime.go pkg/channelv2/service/bench_runtime_test.go pkg/channelv2/FLOW.md pkg/channelv2/reactor/FLOW.md
git commit -m "feat(channelv2): add bench runtime controls"
```
