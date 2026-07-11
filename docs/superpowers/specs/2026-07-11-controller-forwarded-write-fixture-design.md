# Controller Forwarded Write Fixture Stabilization

Status: approved on 2026-07-11

## Context

`TestNodeDefaultControllerForwardsControlWriteOverTransport` verifies that a
Controller follower forwards `JoinNode` to the current leader over the real
node transport. Under constrained scheduling, the test can return
`expected_revision_mismatch` after `WaitControllerWriteReady` succeeds.

The readiness helper proves that all nodes have converged snapshots and that a
Controller voter can commit a non-mutating probe. It does not promise that
bootstrap reconciliation tasks have finished. Those tasks can advance the
logical Controller revision between `JoinNode` reading state and proposing its
internally fenced command. The low-level lifecycle API intentionally exposes
that mismatch as a retryable conflict; production seed-join orchestration owns
the retry policy.

## Scope

This change stabilizes only the transport-forwarding test fixture. It does not:

- change Controller lifecycle or revision-fence semantics;
- add retries around the `JoinNode` operation under test;
- weaken the existing Controller write-readiness probe;
- change production timeouts or background task scheduling.

## Design

Add a test-only condition helper in `pkg/cluster/node_defaults_test.go` that
waits for every node to expose a valid local control snapshot with:

- no active reconciliation tasks;
- the same non-zero logical revision;
- the same non-empty cluster identity.

The helper uses the test's existing bounded context and condition polling. On
timeout it reports the latest node snapshots so a future failure remains
diagnosable.

The helper does not interpret the snapshot `ControllerID` as a live-leader
proof. The second `WaitControllerWriteReady` call owns that responsibility by
probing the current Controller proposal path after task drain.

The forwarding test will:

1. start the three Controller voters;
2. call `WaitControllerWriteReady` for initial route and quorum readiness;
3. wait for bootstrap reconciliation tasks to drain across all voters;
4. call `WaitControllerWriteReady` again to prove the post-drain leader still
   has a writable quorum;
5. select a follower and call `JoinNode` exactly once;
6. assert the forwarded joining-node result.

This separates two different conditions: cluster write readiness and fixture
quiescence. It preserves production behavior while keeping the test focused on
transport forwarding rather than concurrent lifecycle conflict handling.

## Verification

The existing stress command is the red regression evidence:

```bash
GOWORK=off GOMAXPROCS=2 go test ./pkg/cluster \
  -run '^TestNodeDefaultControllerForwardsControlWriteOverTransport$' \
  -count=20
```

After the fixture change, verification must include:

- the same stress command;
- the focused test under `-race`;
- `GOWORK=off go test ./pkg/cluster -count=1`;
- the repository unit-test gate from `AGENTS.md` on the feature branch and
  again after fast-forwarding to local `main`.
