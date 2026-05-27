# ClusterV2 Controller Probe Readiness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a non-mutating ControllerV2 Raft proposal probe and clusterv2 test readiness helpers so startup-sensitive tests can wait on a real Controller write-path barrier.

**Architecture:** Keep the probe inside `pkg/controllerv2/raft.Service` as an empty normal Raft entry tracked by the existing proposal tracker. Expose the capability through a narrow `pkg/clusterv2/control.ProposeProbe` interface implemented by `control.Runtime`, then keep clusterv2 readiness aggregation in test-only helpers so `Node.Start` remains a local node lifecycle method.

**Tech Stack:** Go, etcd raft `RawNode`, ControllerV2 FSM/statefile/WAL, `pkg/clusterv2/control`, same-package Go test helpers, Go unit tests with temp dirs and in-memory/local transports.

---

## Spec Review Decision

Approved for implementation with two concrete plan corrections:

- The current `proposalTracker.bindAppended` deliberately ignores empty normal entries, so the implementation must add probe-aware tracking or `ProbePropose` will hang until context timeout.
- Completed `Service.Stop` currently makes proposal calls look not-started. This plan aligns the probe spec with the exported error comments by preserving `ErrNotStarted` before the first start and returning `ErrStopped` after a service has been stopped.

## Preconditions

- Do not modify unrelated current workspace changes:
  - `docs/superpowers/specs/2026-05-26-meta-table-codec-design.md`
  - `docs/superpowers/specs/2026-05-26-clusterv2-controller-probe-readiness-design.md`
- Read before editing these packages:
  - `pkg/controllerv2/FLOW.md`
  - `pkg/clusterv2/FLOW.md`
  - `docs/superpowers/specs/2026-05-26-clusterv2-controller-probe-readiness-design.md`
- In nested `.worktrees/...` worktrees under the repository parent `go.work`, run Go commands with `GOWORK=off` so tests use this module.

## File Structure

Modify:

- `pkg/controllerv2/raft/proposal_tracker.go` - bind empty normal Raft entries only to pending probe proposals while keeping leader no-op entries untracked.
- `pkg/controllerv2/raft/proposal_tracker_test.go` - cover probe binding, no-op skipping, and command/probe ordering.
- `pkg/controllerv2/raft/service.go` - add `ProbePropose`, probe requests, shared proposal submission, and stopped-after-start lifecycle tracking.
- `pkg/controllerv2/raft/service_test.go` - cover single-node probe success, no logical state mutation, follower rejection, leader discovery by trying voters, and lifecycle errors.
- `pkg/clusterv2/control/controller.go` - add the optional `ProposeProbe` capability interface.
- `pkg/clusterv2/control/runtime.go` - implement `ProbePropose` by delegating to the hosted ControllerV2 Raft service.
- `pkg/clusterv2/control/runtime_test.go` - cover runtime probe success and not-started behavior.
- `pkg/clusterv2/readiness_test.go` - add same-package test helpers `WaitNodeReady` and `WaitClusterReady` plus helper tests.
- `pkg/clusterv2/node_test.go` - replace the manual three-voter convergence polling with `WaitClusterReady`.
- `pkg/controllerv2/FLOW.md` - document empty-entry probe semantics.
- `pkg/clusterv2/FLOW.md` - document local `Start` readiness and test-level distributed readiness.

Do not create a persistent `command.KindProbe`, do not mutate `cluster-state.json` for probes, and do not make `Node.Start` wait for global distributed readiness.

---

### Task 1: Make Proposal Tracking Probe-Aware

**Files:**
- Modify: `pkg/controllerv2/raft/proposal_tracker_test.go`
- Modify: `pkg/controllerv2/raft/proposal_tracker.go`
- Modify: `pkg/controllerv2/raft/service.go`

- [ ] **Step 1: Write failing proposal tracker tests**

Append these tests to `pkg/controllerv2/raft/proposal_tracker_test.go`:

```go
func TestProposalTrackerBindsProbeToEmptyEntry(t *testing.T) {
	tracker := newProposalTracker()
	resp := make(chan error, 1)
	tracker.enqueue(trackedProposal{resp: resp, probe: true})

	tracker.bindAppended([]raftpb.Entry{{Index: 7, Type: raftpb.EntryNormal}})
	tracker.complete(7, nil)

	require.NoError(t, <-resp)
}

func TestProposalTrackerSkipsLeaderNoopForCommandProposal(t *testing.T) {
	tracker := newProposalTracker()
	resp := make(chan error, 1)
	tracker.enqueue(trackedProposal{resp: resp})

	tracker.bindAppended([]raftpb.Entry{{Index: 7, Type: raftpb.EntryNormal}})
	tracker.complete(7, nil)
	select {
	case err := <-resp:
		t.Fatalf("command proposal completed on empty leader no-op: %v", err)
	default:
	}

	tracker.bindAppended([]raftpb.Entry{{Index: 8, Type: raftpb.EntryNormal, Data: []byte("cmd")}})
	tracker.complete(8, nil)
	require.NoError(t, <-resp)
}

func TestProposalTrackerBindsCommandAndProbeInOrder(t *testing.T) {
	tracker := newProposalTracker()
	cmdResp := make(chan error, 1)
	probeResp := make(chan error, 1)
	tracker.enqueue(trackedProposal{resp: cmdResp})
	tracker.enqueue(trackedProposal{resp: probeResp, probe: true})

	tracker.bindAppended([]raftpb.Entry{
		{Index: 10, Type: raftpb.EntryNormal, Data: []byte("cmd")},
		{Index: 11, Type: raftpb.EntryNormal},
	})
	tracker.complete(10, nil)
	tracker.complete(11, nil)

	require.NoError(t, <-cmdResp)
	require.NoError(t, <-probeResp)
}
```

- [ ] **Step 2: Run tracker tests to verify they fail**

Run: `GOWORK=off go test ./pkg/controllerv2/raft -run TestProposalTracker`

Expected: FAIL with compile errors because `trackedProposal` does not have a `probe` field.

- [ ] **Step 3: Add the probe marker to tracked proposals**

In `pkg/controllerv2/raft/service.go`, replace `trackedProposal` with:

```go
type trackedProposal struct {
	resp  chan error
	probe bool
}
```

- [ ] **Step 4: Bind empty entries only for probe proposals**

In `pkg/controllerv2/raft/proposal_tracker.go`, replace `bindAppended` with:

```go
func (t *proposalTracker) bindAppended(entries []raftpb.Entry) {
	for _, entry := range entries {
		if entry.Type != raftpb.EntryNormal || len(t.queue) == 0 {
			continue
		}
		isProbeEntry := len(entry.Data) == 0
		if t.queue[0].probe != isProbeEntry {
			continue
		}
		tracked := t.queue[0]
		t.queue = t.queue[1:]
		t.byIndex[entry.Index] = tracked
	}
}
```

This keeps untracked Raft leader no-op entries from completing command proposals and lets explicit probe proposals bind to their empty entries.

- [ ] **Step 5: Run tracker tests to verify they pass**

Run: `GOWORK=off go test ./pkg/controllerv2/raft -run TestProposalTracker`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/controllerv2/raft/proposal_tracker.go pkg/controllerv2/raft/proposal_tracker_test.go pkg/controllerv2/raft/service.go
git commit -m "test: cover controller raft probe tracking"
```

---

### Task 2: Add ControllerV2 Raft ProbePropose

**Files:**
- Modify: `pkg/controllerv2/raft/service_test.go`
- Modify: `pkg/controllerv2/raft/service.go`

- [ ] **Step 1: Write failing raft service probe tests**

Append these tests to `pkg/controllerv2/raft/service_test.go`:

```go
func TestProbeProposeSingleNodeSucceedsWithoutStateMutation(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1})
	cluster.start(t)
	cluster.propose(t, testInitCommand("wk-raft-probe-single", cluster.peers))
	cluster.waitForRevision(t, 1)

	before := cluster.nodes[0].stateMachine.Snapshot(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, cluster.waitForLeader(t).service.ProbePropose(ctx))
	after := cluster.nodes[0].stateMachine.Snapshot(context.Background())

	require.Equal(t, before, after)
}

func TestProbeProposeFollowerReturnsNotLeader(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1, 2, 3})
	cluster.start(t)
	leader := cluster.waitForLeader(t)
	var follower *testRaftNode
	for _, node := range cluster.nodes {
		if node.id != leader.id {
			follower = node
			break
		}
	}
	require.NotNil(t, follower)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.ErrorIs(t, follower.service.ProbePropose(ctx), ErrNotLeader)
}

func TestProbeProposeFindsLeaderInThreeVoters(t *testing.T) {
	cluster := newRaftTestCluster(t, []uint64{1, 2, 3})
	cluster.start(t)

	require.Eventually(t, func() bool {
		for _, node := range cluster.nodes {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			err := node.service.ProbePropose(ctx)
			cancel()
			if err == nil {
				return true
			}
			if !errors.Is(err, ErrNotLeader) {
				t.Logf("ProbePropose(node=%d) error: %v", node.id, err)
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond)
}

func TestProbeProposeLifecycleErrors(t *testing.T) {
	peers := []Peer{{NodeID: 1, Addr: "n1"}}
	service, err := NewService(Config{
		NodeID:         1,
		Peers:          peers,
		AllowBootstrap: true,
		RaftDir:        filepath.Join(t.TempDir(), "controller-raft"),
		StateMachine:   newTestStateMachine(t, filepath.Join(t.TempDir(), "cluster-state.json")),
		Transport:      newMemoryRaftTransport(),
		TickInterval:   testRaftTickInterval,
	})
	require.NoError(t, err)

	require.ErrorIs(t, service.ProbePropose(context.Background()), ErrNotStarted)
	require.NoError(t, service.Start(context.Background()))
	require.NoError(t, service.Stop())
	require.ErrorIs(t, service.ProbePropose(context.Background()), ErrStopped)
}
```

- [ ] **Step 2: Run raft probe tests to verify they fail**

Run: `GOWORK=off go test ./pkg/controllerv2/raft -run 'TestProbePropose'`

Expected: FAIL with compile errors because `Service.ProbePropose` does not exist.

- [ ] **Step 3: Extend the service lifecycle state**

In `pkg/controllerv2/raft/service.go`, add `stopped bool` beside the existing lifecycle booleans:

```go
	mu       sync.Mutex
	started  bool
	stopping bool
	stopped  bool
	stopCh   chan struct{}
```

In `Start`, after the `started` check and before opening the store, clear the stopped marker:

```go
	s.stopped = false
	s.err = nil
```

In `Stop`, after clearing channels and setting `started` and `stopping` false, set the stopped marker:

```go
	s.started = false
	s.stopping = false
	s.stopped = true
	s.stopCh = nil
	s.doneCh = nil
	s.stepCh = nil
	s.proposal = nil
```

- [ ] **Step 4: Add probe requests and shared proposal submission**

In `pkg/controllerv2/raft/service.go`, replace `proposalRequest` with:

```go
type proposalRequest struct {
	ctx   context.Context
	cmd   command.Command
	probe bool
	resp  chan error
}
```

Replace `Propose` with this method and add `ProbePropose` plus `submitProposal` immediately after it:

```go
// Propose appends a ControllerV2 command on the leader and waits until it is applied.
func (s *Service) Propose(ctx context.Context, cmd command.Command) error {
	return s.submitProposal(ctx, proposalRequest{cmd: cmd})
}

// ProbePropose appends an empty Raft entry and waits until it is applied.
// It verifies the Controller proposal write path without mutating ControllerV2 cluster state.
func (s *Service) ProbePropose(ctx context.Context) error {
	return s.submitProposal(ctx, proposalRequest{probe: true})
}

func (s *Service) submitProposal(ctx context.Context, req proposalRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if !s.started {
		stopped := s.stopped
		s.mu.Unlock()
		if stopped {
			return ErrStopped
		}
		return ErrNotStarted
	}
	if s.stopping {
		s.mu.Unlock()
		return ErrStopped
	}
	proposalCh := s.proposal
	stopCh := s.stopCh
	doneCh := s.doneCh
	s.mu.Unlock()

	req.ctx = ctx
	req.resp = make(chan error, 1)
	select {
	case proposalCh <- req:
	case <-ctx.Done():
		return ctx.Err()
	case <-doneCh:
		return s.currentError()
	case <-stopCh:
		return ErrStopped
	}

	select {
	case err := <-req.resp:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-doneCh:
		return s.currentError()
	case <-stopCh:
		return ErrStopped
	}
}
```

- [ ] **Step 5: Propose empty data for probes in the run loop**

In the `case req := <-proposalCh:` block in `pkg/controllerv2/raft/service.go`, replace the command encoding and tracker enqueue section with:

```go
			var data []byte
			if !req.probe {
				encoded, err := command.Encode(req.cmd)
				if err != nil {
					req.resp <- err
					continue
				}
				data = encoded
			}
			if err := rawNode.Propose(data); err != nil {
				req.resp <- err
				continue
			}
			trackerMu.Lock()
			tracker.enqueue(trackedProposal{resp: req.resp, probe: req.probe})
			trackerMu.Unlock()
```

- [ ] **Step 6: Run raft probe tests to verify they pass**

Run: `GOWORK=off go test ./pkg/controllerv2/raft -run 'TestProbePropose|TestProposalTracker'`

Expected: PASS.

- [ ] **Step 7: Run all ControllerV2 raft tests**

Run: `GOWORK=off go test ./pkg/controllerv2/raft`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/controllerv2/raft/service.go pkg/controllerv2/raft/service_test.go
git commit -m "feat: add controller raft proposal probe"
```

---

### Task 3: Expose The Probe Through clusterv2 Control Runtime

**Files:**
- Modify: `pkg/clusterv2/control/controller.go`
- Modify: `pkg/clusterv2/control/runtime.go`
- Modify: `pkg/clusterv2/control/runtime_test.go`

- [ ] **Step 1: Write failing control runtime tests**

Update imports in `pkg/clusterv2/control/runtime_test.go` to include `errors` and `cv2raft`:

```go
import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	cv2raft "github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft"
	cv2state "github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	cv2sync "github.com/WuKongIM/WuKongIM/pkg/controllerv2/sync"
)
```

Append these tests to `pkg/clusterv2/control/runtime_test.go`:

```go
func TestRuntimeProbeProposeSingleVoter(t *testing.T) {
	runtime, err := NewRuntime(RuntimeConfig{
		NodeID:           1,
		Addr:             "127.0.0.1:10001",
		StateDir:         t.TempDir(),
		ClusterID:        "cluster-probe-single",
		Role:             RuntimeRoleVoter,
		Voters:           []RuntimeVoter{{NodeID: 1, Addr: "127.0.0.1:10001"}},
		AllowBootstrap:   true,
		InitialSlotCount: 1,
		HashSlotCount:    4,
		ReplicaCount:     1,
		TickInterval:     5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := runtime.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Stop(context.Background()) })

	before, err := runtime.LocalSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LocalSnapshot(before) error = %v", err)
	}
	if err := runtime.ProbePropose(ctx); err != nil {
		t.Fatalf("ProbePropose() error = %v", err)
	}
	after, err := runtime.LocalSnapshot(context.Background())
	if err != nil {
		t.Fatalf("LocalSnapshot(after) error = %v", err)
	}
	if before.Revision != after.Revision || len(before.Slots) != len(after.Slots) {
		t.Fatalf("ProbePropose mutated local snapshot: before=%#v after=%#v", before, after)
	}
}

func TestRuntimeProbeProposeWithoutRaftReturnsNotStarted(t *testing.T) {
	var runtime Runtime
	if err := runtime.ProbePropose(context.Background()); !errors.Is(err, cv2raft.ErrNotStarted) {
		t.Fatalf("ProbePropose() error = %v, want ErrNotStarted", err)
	}
}
```

- [ ] **Step 2: Run control tests to verify they fail**

Run: `GOWORK=off go test ./pkg/clusterv2/control -run 'TestRuntimeProbePropose'`

Expected: FAIL with compile errors because `Runtime.ProbePropose` does not exist.

- [ ] **Step 3: Add the optional capability interface**

In `pkg/clusterv2/control/controller.go`, after `Controller`, add:

```go
// ProposeProbe verifies whether the local control runtime can commit a Controller proposal.
type ProposeProbe interface {
	// ProbePropose commits a non-mutating Controller proposal probe.
	ProbePropose(context.Context) error
}
```

- [ ] **Step 4: Implement Runtime.ProbePropose**

In `pkg/clusterv2/control/runtime.go`, add this method after `LeaderID`:

```go
// ProbePropose verifies the hosted ControllerV2 proposal path when this runtime is a voter.
func (r *Runtime) ProbePropose(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r == nil || r.raft == nil {
		return cv2raft.ErrNotStarted
	}
	return r.raft.ProbePropose(ctx)
}
```

- [ ] **Step 5: Run control runtime tests to verify they pass**

Run: `GOWORK=off go test ./pkg/clusterv2/control -run 'TestRuntimeProbePropose|TestRuntimeSingleVoterBootstrapsSnapshot'`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/clusterv2/control/controller.go pkg/clusterv2/control/runtime.go pkg/clusterv2/control/runtime_test.go
git commit -m "feat: expose controller proposal probe in clusterv2 control"
```

---

### Task 4: Add clusterv2 Readiness Test Helpers

**Files:**
- Create: `pkg/clusterv2/readiness_test.go`
- Modify: `pkg/clusterv2/node_test.go`

- [ ] **Step 1: Create failing readiness helper tests and helper skeleton**

Create `pkg/clusterv2/readiness_test.go` with this complete content:

```go
package clusterv2

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

type nodeReadinessState struct {
	NodeID         uint64
	Snapshot       Snapshot
	ProbeSupported bool
	ProbeError     string
}

// WaitNodeReady waits until one started node has consumed a valid local control snapshot and runtime gates.
func WaitNodeReady(ctx context.Context, node *Node) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	latest := []nodeReadinessState{{}}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if nodeLocalReady(node, &latest[0]) {
			return nil
		}
		select {
		case <-ctx.Done():
			return readinessTimeoutError(ctx.Err(), latest)
		case <-ticker.C:
		}
	}
}

// WaitClusterReady waits until all nodes are locally ready and one supported Controller voter probe succeeds.
func WaitClusterReady(ctx context.Context, nodes ...*Node) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if len(nodes) == 0 {
		return errors.New("clusterv2 readiness: no nodes")
	}
	latest := make([]nodeReadinessState, len(nodes))
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if clusterLocalReady(nodes, latest) && controllerProbeReady(ctx, nodes, latest) {
			return nil
		}
		select {
		case <-ctx.Done():
			return readinessTimeoutError(ctx.Err(), latest)
		case <-ticker.C:
		}
	}
}

func clusterLocalReady(nodes []*Node, latest []nodeReadinessState) bool {
	var revision uint64
	var slotCount uint32
	var hashSlotCount uint16
	var controllerLead uint64
	for i, node := range nodes {
		if !nodeLocalReady(node, &latest[i]) {
			return false
		}
		snap := latest[i].Snapshot
		if revision == 0 {
			revision = snap.StateRevision
			slotCount = snap.SlotCount
			hashSlotCount = snap.HashSlotCount
			controllerLead = snap.ControllerLead
			continue
		}
		if snap.StateRevision != revision || snap.SlotCount != slotCount || snap.HashSlotCount != hashSlotCount || snap.ControllerLead != controllerLead {
			return false
		}
	}
	return true
}

func nodeLocalReady(node *Node, latest *nodeReadinessState) bool {
	if node == nil {
		latest.NodeID = 0
		latest.Snapshot = Snapshot{}
		return false
	}
	snap := node.Snapshot()
	latest.NodeID = node.NodeID()
	latest.Snapshot = snap
	if !node.started.Load() || node.stopping.Load() {
		return false
	}
	if node.control != nil && (snap.StateRevision == 0 || !snap.RoutesReady) {
		return false
	}
	if !snap.SlotsReady {
		return false
	}
	if node.channels != nil && !snap.ChannelsReady {
		return false
	}
	return true
}

func controllerProbeReady(ctx context.Context, nodes []*Node, latest []nodeReadinessState) bool {
	supported := false
	for i, node := range nodes {
		if node == nil || node.control == nil {
			continue
		}
		probe, ok := node.control.(control.ProposeProbe)
		latest[i].ProbeSupported = ok
		if !ok {
			continue
		}
		supported = true
		probeCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		err := probe.ProbePropose(probeCtx)
		cancel()
		if err == nil {
			latest[i].ProbeError = ""
			return true
		}
		latest[i].ProbeError = err.Error()
	}
	return !supported
}

func readinessTimeoutError(cause error, latest []nodeReadinessState) error {
	var b strings.Builder
	fmt.Fprintf(&b, "clusterv2 readiness: %v", cause)
	for _, item := range latest {
		fmt.Fprintf(&b, "\nnode=%d snapshot=%+v probe_supported=%t", item.NodeID, item.Snapshot, item.ProbeSupported)
		if item.ProbeError != "" {
			fmt.Fprintf(&b, " probe_error=%q", item.ProbeError)
		}
	}
	return errors.New(b.String())
}

func TestWaitNodeReadySucceedsForStartedSingleNodeCluster(t *testing.T) {
	cfg := validNodeConfig(t)
	cfg.Channel.TickInterval = time.Millisecond
	cfg.Control.ClusterID = "readiness-single"
	cfg.Slots.InitialSlotCount = 1
	cfg.Slots.HashSlotCount = 4
	cfg.Slots.ReplicaCount = 1
	node, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := node.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })
	if err := WaitNodeReady(ctx, node); err != nil {
		t.Fatalf("WaitNodeReady() error = %v", err)
	}
}

func TestWaitClusterReadyReportsControllerProbeTimeout(t *testing.T) {
	probeErr := errors.New("probe boom")
	controller := &failingProbeController{StaticController: control.NewStaticController(nodeControlSnapshot()), err: probeErr}
	node, err := New(validNodeConfig(t), withController(controller))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := node.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = node.Stop(context.Background()) })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err = WaitClusterReady(ctx, node)
	if err == nil {
		t.Fatal("WaitClusterReady() error = nil, want timeout")
	}
	if !strings.Contains(err.Error(), "probe boom") || !strings.Contains(err.Error(), "snapshot=") {
		t.Fatalf("WaitClusterReady() error = %v, want probe and snapshot details", err)
	}
}

type failingProbeController struct {
	*control.StaticController
	err error
}

func (c *failingProbeController) ProbePropose(context.Context) error { return c.err }
```

- [ ] **Step 2: Run readiness tests to verify the helper compiles and current behavior passes locally**

Run: `GOWORK=off go test ./pkg/clusterv2 -run 'TestWaitNodeReady|TestWaitClusterReadyReports'`

Expected: PASS after Task 3 is complete. If it fails, fix the helper before replacing existing waits.

- [ ] **Step 3: Replace manual three-voter convergence polling**

In `pkg/clusterv2/node_test.go`, inside `TestNodeDefaultControllerV2ThreeVotersConvergeOverTransport`, replace the final `waitUntil` block with:

```go
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readyCancel()
	if err := WaitClusterReady(readyCtx, nodes...); err != nil {
		t.Fatalf("WaitClusterReady() error = %v", err)
	}
```

- [ ] **Step 4: Run clusterv2 readiness and default ControllerV2 tests**

Run: `GOWORK=off go test ./pkg/clusterv2 -run 'TestWaitNodeReady|TestWaitClusterReadyReports|TestNodeDefaultControllerV2ThreeVotersConvergeOverTransport'`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/clusterv2/readiness_test.go pkg/clusterv2/node_test.go
git commit -m "test: add clusterv2 controller readiness helpers"
```

---

### Task 5: Update Flow Documentation

**Files:**
- Modify: `pkg/controllerv2/FLOW.md`
- Modify: `pkg/clusterv2/FLOW.md`

- [ ] **Step 1: Update ControllerV2 flow documentation**

In `pkg/controllerv2/FLOW.md`, after the paragraph that starts with ``Revision` is the logical cluster-state version`, add:

```markdown
Empty normal Raft entries are also used by `raft.Service.ProbePropose` as non-mutating readiness probes. A probe entry advances Controller Raft applied metadata after it is committed and scheduled, but it is not decoded as a Controller command, does not call the FSM, does not increment logical `Revision`, and does not rewrite `cluster-state.json` business state.
```

- [ ] **Step 2: Update clusterv2 flow documentation**

In `pkg/clusterv2/FLOW.md`, after the paragraph that starts with ``Start` requires cluster semantics even for one node`, add:

```markdown
`Node.Start` only establishes local-node readiness: the node has a valid local control snapshot, installed routes, reconciled local Slot runtime state, and started local ChannelV2 resources. Tests that require distributed Controller write readiness should wait for all nodes to be locally ready and then require one ControllerV2 voter to pass the `control.ProposeProbe` capability. Slot and Channel write tests should add their own Slot leader or Channel metadata gates when those paths are part of the assertion.
```

- [ ] **Step 3: Verify docs mention the probe and local readiness split**

Run: `rg -n "ProbePropose|local-node readiness|non-mutating readiness probes" pkg/controllerv2/FLOW.md pkg/clusterv2/FLOW.md`

Expected: output contains the new ControllerV2 probe paragraph and the new clusterv2 readiness paragraph.

- [ ] **Step 4: Commit**

```bash
git add pkg/controllerv2/FLOW.md pkg/clusterv2/FLOW.md
git commit -m "docs: document controller probe readiness"
```

---

### Task 6: Final Verification

**Files:**
- Verify only.

- [ ] **Step 1: Run focused package tests**

Run: `GOWORK=off go test ./pkg/controllerv2/raft ./pkg/clusterv2/control ./pkg/clusterv2`

Expected: PASS.

- [ ] **Step 2: Run broader related tests**

Run: `GOWORK=off go test ./pkg/controllerv2/... ./pkg/clusterv2/...`

Expected: PASS.

- [ ] **Step 3: Inspect git diff for scope**

Run: `git diff --stat`

Expected: changed files are limited to the files listed in this plan plus existing unrelated workspace changes that were present before work started.

- [ ] **Step 4: Commit final fixes if any verification changes were needed**

```bash
git add pkg/controllerv2/raft pkg/clusterv2/control pkg/clusterv2 pkg/controllerv2/FLOW.md pkg/clusterv2/FLOW.md
git commit -m "test: stabilize clusterv2 controller readiness"
```

Skip this commit if Step 1 and Step 2 passed without additional file changes after Task 5.

---

## Self-Review Notes

- Spec coverage: ControllerV2 probe, control runtime capability, local and distributed clusterv2 readiness helpers, helper error details, and FLOW documentation are each mapped to a task.
- Boundary check: no ControllerV2 command kind is added, no single-node bypass branch is introduced, and `Node.Start` remains local lifecycle only.
- Tracker check: empty entries are only bound to tracked probe proposals, so existing leader no-op entries still cannot complete command proposals.
- Test check: focused tests cover successful probe, non-mutating state, follower rejection, all-voter probing, lifecycle errors, readiness success, and readiness timeout diagnostics.
