# Controller Forwarded Write Fixture Stabilization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the real-transport Controller forwarding test wait for bootstrap reconciliation to finish before issuing its single lifecycle write.

**Architecture:** Keep production Controller lifecycle and revision-fence semantics unchanged. Add one test-only condition helper that observes converged, task-free control snapshots, then re-run the existing write-readiness probe before the test calls `JoinNode` exactly once.

**Tech Stack:** Go, `testing`, `context`, WuKongIM Controller/control snapshots, Go race detector.

---

### Task 1: Stabilize the forwarded-write test fixture

**Files:**
- Modify: `pkg/cluster/node_defaults_test.go:867-939`
- Reference: `pkg/cluster/readiness_test.go:68-89`
- Reference: `docs/superpowers/specs/2026-07-11-controller-forwarded-write-fixture-design.md`

- [ ] **Step 1: Verify the existing test is RED under constrained scheduling**

Run:

```bash
GOWORK=off GOMAXPROCS=2 go test ./pkg/cluster \
  -run '^TestNodeDefaultControllerForwardsControlWriteOverTransport$' \
  -count=20
```

Expected: FAIL in at least one iteration with `JoinNode() error` containing
`expected_revision_mismatch`. This proves the existing transport test exposes
the bootstrap-task revision race.

- [ ] **Step 2: Add a condition helper for converged task-free snapshots**

Add this test-only helper near the forwarding test in
`pkg/cluster/node_defaults_test.go`:

```go
func waitForControllerTasksDrained(t *testing.T, ctx context.Context, nodes ...*Node) {
	t.Helper()
	latest := make([]control.Snapshot, len(nodes))
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		converged := len(nodes) > 0
		var revision uint64
		var clusterID string
		for i, node := range nodes {
			if node == nil {
				converged = false
				break
			}
			snapshot, err := node.LocalControlSnapshot(ctx)
			if err != nil {
				converged = false
				break
			}
			latest[i] = snapshot
			if snapshot.ClusterID == "" || snapshot.Revision == 0 || len(snapshot.Tasks) != 0 {
				converged = false
				break
			}
			if revision == 0 {
				revision = snapshot.Revision
				clusterID = snapshot.ClusterID
				continue
			}
			if snapshot.Revision != revision || snapshot.ClusterID != clusterID {
				converged = false
				break
			}
		}
		if converged {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("wait for Controller tasks to drain: %v; latest=%+v", ctx.Err(), latest)
		case <-ticker.C:
		}
	}
}
```

This helper must not inspect `Snapshot.ControllerID` as a live-leader signal;
the post-drain proposal probe owns that check.

- [ ] **Step 3: Gate the single forwarded write on task drain and a fresh probe**

Replace the current one-phase readiness block in
`TestNodeDefaultControllerForwardsControlWriteOverTransport` with:

```go
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := WaitControllerWriteReady(readyCtx, nodes...)
	readyCancel()
	if err != nil {
		t.Fatalf("WaitControllerWriteReady(initial) error = %v", err)
	}

	drainCtx, drainCancel := context.WithTimeout(context.Background(), 5*time.Second)
	waitForControllerTasksDrained(t, drainCtx, nodes...)
	drainCancel()

	probeCtx, probeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = WaitControllerWriteReady(probeCtx, nodes...)
	probeCancel()
	if err != nil {
		t.Fatalf("WaitControllerWriteReady(post-drain) error = %v", err)
	}
```

Keep the existing follower selection, single `JoinNode` call, and result
assertions unchanged. Do not retry `JoinNode` in the test.

- [ ] **Step 4: Format and verify the focused stress test is GREEN**

Run:

```bash
gofmt -w pkg/cluster/node_defaults_test.go
git diff --check
GOWORK=off GOMAXPROCS=2 go test ./pkg/cluster \
  -run '^TestNodeDefaultControllerForwardsControlWriteOverTransport$' \
  -count=20
```

Expected: PASS for all 20 iterations and no diff whitespace errors.

- [ ] **Step 5: Verify race safety and the full cluster package**

Run:

```bash
GOWORK=off go test -race ./pkg/cluster \
  -run '^TestNodeDefaultControllerForwardsControlWriteOverTransport$' \
  -count=1
GOWORK=off go test ./pkg/cluster -count=1
```

Expected: both commands PASS.

- [ ] **Step 6: Commit the fixture fix**

```bash
git add pkg/cluster/node_defaults_test.go
git diff --cached --check
git commit -m "test(cluster): wait for bootstrap tasks before forwarded write"
```

Expected: one test-only commit; no production files staged.

### Task 2: Verify the feature branch and integrate local main

**Files:**
- Verify only: repository packages selected by `AGENTS.md`
- Cleanup: `.worktrees/runtime-scalability-fixes`

- [ ] **Step 1: Run the repository unit-test gate on the feature branch**

Run:

```bash
GOWORK=off go test ./cmd/... ./internal/... ./pkg/... ./scripts/... ./docker/... -count=1
```

Expected: PASS for every selected package.

- [ ] **Step 2: Confirm a clean fast-forward path to local main**

Run from the main repository root:

```bash
git status --short --branch
git merge-base --is-ancestor main codex/runtime-scalability-fixes
git rev-list --left-right --count main...codex/runtime-scalability-fixes
```

Expected: clean `main`, ancestry exit status 0, and `main` has zero commits not
contained in the feature branch.

- [ ] **Step 3: Fast-forward local main**

```bash
git merge --ff-only codex/runtime-scalability-fixes
```

Expected: fast-forward to the feature branch tip without a merge commit.

- [ ] **Step 4: Re-run the repository unit-test gate on merged main**

```bash
GOWORK=off go test ./cmd/... ./internal/... ./pkg/... ./scripts/... ./docker/... -count=1
git status --short --branch
```

Expected: all packages PASS and `main` remains clean.

- [ ] **Step 5: Remove the merged worktree and branch**

Run from the main repository root only after Step 4 passes:

```bash
git worktree remove /Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/runtime-scalability-fixes
git worktree prune
git branch -d codex/runtime-scalability-fixes
```

Expected: the worktree is no longer registered and the merged local feature
branch is deleted. Do not push `main`.
