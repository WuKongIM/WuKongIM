# Three-Node Process-Level E2E Cross-Node WKProto Design

## Summary

This design is phase 2 of the `test/e2e` black-box foundation.

It extends the existing real-binary e2e harness from one `单节点集群` to one
real three-node cluster and adds one new cross-node happy-path regression:

- start three real `cmd/wukongim` processes
- wait for cluster readiness without fixed sleeps
- discover the current slot leader through existing manager APIs
- connect the sender to one follower
- connect the recipient to another non-leader node
- assert the full `Connect -> Send -> SendAck -> Recv -> RecvAck` closure across
  nodes

The design stays strict about boundaries:

- no reuse of `internal/app` gray-box harnesses
- no fixed wait windows as correctness conditions
- no new product APIs just for e2e
- no leader crash, restart, or fault injection in this phase

The goal is to make the current process-level e2e layer strong enough that later
leader crash and rolling restart scenarios can reuse the same black-box cluster
harness instead of replacing it.

## Goals

- Extend `test/e2e` from one real process to one real three-node cluster.
- Add one stable black-box regression that proves cross-node WKProto delivery on
  the normal path.
- Use existing external health and manager endpoints instead of sleeps to decide
  readiness and cluster role discovery.
- Force follower ingress by connecting the sender to a follower and the
  recipient to a different non-leader node.
- Keep all process data, logs, config files, and diagnostics scoped to the test
  temp directory.
- Design the harness so later three-node scenarios can add failure injection on
  top of the same startup and discovery primitives.

## Non-Goals

- No leader crash coverage in this phase.
- No rolling restart coverage in this phase.
- No network partition, jitter, or fault injection framework.
- No new debug-only or product-only HTTP endpoints.
- No use of `internal/app` test harnesses or direct app object orchestration.
- No direct DB, raft, or runtime inspection in test assertions.
- No broad channel metadata or ISR correctness matrix in the first three-node
  test.
- No replacement of the existing one-node-cluster process e2e baseline.

## Existing Context

### Phase 1 foundation already in place

The repository already has a process-level black-box e2e layer under
`test/e2e` that:

- builds the real `./cmd/wukongim` binary in `TestMain`
- writes generated `WK_` config files
- starts real child processes
- uses real WKProto clients
- verifies one `单节点集群` send path through `Connect -> Send -> SendAck -> Recv
  -> RecvAck`

That means phase 2 does not need a new test framework. It only needs to extend
that harness to three nodes and add cluster-aware discovery and diagnostics.

### External APIs that already solve the hard parts

The repository already exposes two important black-box interfaces that are good
fits for process-level e2e:

- `GET /readyz`
  - returns `200` only when the node reports `ready`
  - already checks gateway availability, managed slots readiness, and hash slot
    table readiness
- `GET /manager/slots/:slot_id`
  - returns the observed runtime leader and peer set for a managed slot
  - gives the e2e harness a stable way to discover leader vs. followers without
    internal app access

The repository also already exposes `GET /manager/connections`, which can be
used as a black-box assertion that a test client is actually connected to the
expected node.

### Reference behavior already covered by gray-box tests

`internal/app/multinode_integration_test.go` already contains three-node
references for follower forwarding, durable send, leader crash, and rolling
restart behavior.

Those tests are useful as semantic references, but phase 2 must not reuse their
internal harness or import `internal/app` from `test/e2e`.

## Approaches Considered

### 1. Recommended: use existing manager slot APIs for role discovery

Enable a loopback-only manager port on each e2e node and discover the current
leader through `GET /manager/slots/1`.

Pros:

- stays fully black-box
- uses a stable external contract instead of internal knowledge
- directly answers the question the test cares about: which node currently leads
  the managed slot
- remains useful for future leader crash and restart scenarios

Cons:

- requires adding manager listener configuration to e2e nodes
- introduces one more external port per node

### 2. Use debug cluster endpoints for role discovery

Enable `GET /debug/cluster` and infer the leader from debug snapshots.

Pros:

- rich diagnostics
- can expose more cluster details than the manager slot endpoint

Cons:

- `debug/*` is a diagnostic surface, not the cleanest long-term e2e contract
- gives the harness more data than it needs for phase 2
- weaker boundary than using the manager APIs meant for external observation

### 3. Assume fixed node roles after startup

Hard-code a startup expectation such as node 1 leader, node 2 follower, node 3
follower.

Pros:

- simplest implementation
- smallest helper surface

Cons:

- brittle and timing-dependent
- breaks as soon as leader election differs across runs
- incompatible with later failure scenarios

## Recommended Approach

Choose approach 1.

The phase-2 harness should treat role discovery as an external observation step,
not as an internal assumption. The best contract for that is the existing
manager slot detail API.

Role selection should be:

- read `GET /manager/slots/1`
- require `state.quorum == "ready"`
- require `state.sync == "matched"`
- require `runtime.leader_id != 0`
- require `runtime.current_peers` to contain exactly the three cluster nodes
- define:
  - `leader = runtime.leader_id`
  - `followerA`, `followerB` = the remaining two nodes

Then the cross-node scenario becomes deterministic without relying on fixed
sleep windows or internal app knowledge.

## Design

### 1. Test Scenario

Add one new e2e test:

- `TestE2E_WKProtoSendMessage_ThreeNodeCluster_CrossNodeClosure`

Scenario flow:

1. Start one real three-node cluster through the `test/e2e/suite` harness.
2. Wait until every node satisfies the node-ready contract.
3. Resolve slot topology for slot `1` through `GET /manager/slots/1`.
4. Choose the sender node as one follower.
5. Choose the recipient node as the other non-leader node.
6. Connect a real WKProto sender client to `followerA.gateway`.
7. Connect a real WKProto recipient client to `followerB.gateway`.
8. Optionally verify local connection placement through
   `GET /manager/connections` on the two chosen follower nodes.
9. Send one real `SendPacket` from `u1` to `u2`.
10. Assert:
    - sender receives `SendAck(success)`
    - `message_id` and `message_seq` are non-zero
    - recipient receives `Recv`
    - `from_uid`, `channel_id`, `channel_type`, payload, `message_id`, and
      `message_seq` all match expectations
11. Send `RecvAck` from the recipient.

What this test proves:

- follower ingress works
- follower-to-leader forwarding works
- durable send acknowledgement is returned to the sender
- cross-node realtime delivery to an online recipient works

### 2. Readiness Contract

Phase 2 must not use fixed startup sleeps as a correctness condition.

#### Node-ready contract

Each node is considered ready only when both checks succeed:

1. `GET /readyz` returns `200`
2. a real `WKProto Connect -> Connack` handshake succeeds against that node's
   gateway listener

`/readyz` is the primary readiness signal because it already reflects gateway,
managed slot, and hash slot table readiness. The extra WKProto handshake keeps
this contract grounded in the actual client-facing protocol.

#### Cluster-ready contract

The cluster is ready when all three nodes satisfy the node-ready contract.

The test does not need a separate fixed waiting period after cluster-ready.
Role discovery should happen immediately after readiness passes, and topology
validation should be based on manager slot state rather than elapsed time.

### 3. Leader and Follower Discovery

Role discovery must stay black-box and must not depend on startup order.

Add a helper such as:

- `ResolveSlotTopology(ctx, slotID)`

It should:

- query `GET /manager/slots/:slot_id` from one or more started nodes
- parse the returned slot detail DTO
- require:
  - `state.quorum == "ready"`
  - `state.sync == "matched"`
  - `runtime.leader_id != 0`
  - `runtime.current_peers` contains exactly the three known node IDs
- return a topology structure like:
  - `SlotID`
  - `LeaderNodeID`
  - `FollowerNodeIDs []uint64`
  - optional raw response body for diagnostics

The manager surface should run only on loopback addresses in e2e config and
should disable auth for this test-only harness:

- `WK_MANAGER_LISTEN_ADDR=127.0.0.1:<port>`
- `WK_MANAGER_AUTH_ON=false`

This keeps the e2e role discovery contract explicit and small.

### 4. Harness Extensions

Keep extending `test/e2e/suite` natively instead of reusing gray-box helpers.

#### `StartedNode`

Extend the existing started node descriptor to include:

- `ManagerAddr`
- existing config path and process handles
- existing gateway/API/cluster addresses

Each started node should support black-box helper calls for:

- `/readyz`
- `/manager/slots/:slot_id`
- `/manager/connections`
- WKProto connection tests

#### `StartedCluster`

Add a cluster-scoped handle with a small public surface:

- `Nodes []StartedNode`
- `Node(id)` / `MustNode(id)`
- `WaitClusterReady(ctx)`
- `ResolveSlotTopology(ctx, slotID)`
- `DumpDiagnostics()`

`StartedCluster` should be responsible for startup, discovery, and diagnostics,
but not for test-specific business assertions.

#### `StartThreeNodeCluster`

Add a suite entrypoint that:

- allocates three sets of cluster, gateway, API, and manager ports
- renders three real `WK_` config files
- starts three real child processes
- registers cleanup for all processes
- returns `StartedCluster`

It should not hard-code leader or follower roles during startup.
All role decisions must be made later through manager-based topology discovery.

### 5. E2E Config Shape

Three-node e2e config should stay close to a normal deployment config but remain
fully test-scoped.

Per node, the harness should set at least:

- `WK_NODE_ID`
- `WK_NODE_NAME`
- `WK_NODE_DATA_DIR`
- `WK_CLUSTER_LISTEN_ADDR`
- `WK_CLUSTER_SLOT_COUNT=1`
- `WK_CLUSTER_INITIAL_SLOT_COUNT=1`
- `WK_CLUSTER_CONTROLLER_REPLICA_N=3`
- `WK_CLUSTER_SLOT_REPLICA_N=3`
- `WK_CLUSTER_NODES=[...]` with all three nodes
- `WK_GATEWAY_LISTENERS=[...]`
- `WK_API_LISTEN_ADDR=127.0.0.1:<port>`
- `WK_MANAGER_LISTEN_ADDR=127.0.0.1:<port>`
- `WK_MANAGER_AUTH_ON=false`
- `WK_LOG_DIR=<node-root>/logs`

The exact timeout and observation settings may reuse defaults unless phase-2
implementation proves a smaller e2e-oriented override is necessary.

### 6. Connection Placement Assertion

The main correctness path is the send/receive closure. Connection placement is a
secondary but useful black-box assertion.

Recommended behavior:

- after connecting `u1` and `u2`, query `GET /manager/connections` on the two
  target follower nodes
- assert that the expected local session appears on the expected node
- do not make `/manager/connections` part of cluster readiness

This keeps readiness minimal while still proving that the sender and recipient
really entered through the intended followers.

### 7. Diagnostics and Failure Reporting

Three-node process e2e failures must produce structured diagnostics without
flooding the terminal.

`DumpDiagnostics()` should collect, at minimum:

- each node's config file path
- each node's `stdout.log` and `stderr.log` path
- each node's last observed `/readyz` status code and response body
- the last observed `/manager/slots/1` response body
- relevant `/manager/connections` response bodies when connection placement was
  checked
- a short tail of stdout/stderr and app logs for each node

The harness should report log tails rather than whole log files. The files stay
on disk inside the test temp directory for deeper inspection.

### 8. Log and Workspace Isolation

All e2e artifacts must stay under `t.TempDir()`.

Per node layout should look like:

```text
<temp-root>/node-1/
  wukongim.conf
  stdout.log
  stderr.log
  logs/
  data/
```

And similarly for `node-2` and `node-3`.

The harness must explicitly set `WK_LOG_DIR` so the application never falls back
to repository-relative `./logs` paths during e2e runs.

This avoids polluting the repository with `test/e2e/logs/app.log`-style
artifacts and keeps every run self-contained.

### 9. Scope Boundary for Phase 2

Phase 2 should ship only these user-visible changes:

- one new three-node cluster harness path
- one new three-node cross-node WKProto e2e test
- the minimal helper coverage needed to keep the new harness maintainable

It should explicitly not include:

- leader crash scenarios
- rolling restart scenarios
- channel runtime metadata assertions in the main e2e flow
- product code changes to expose new e2e-only probes
- coupling to `internal/app` integration helpers

This keeps the first three-node black-box scenario narrow enough to stabilize
before adding failure-path coverage.

## Test Plan

### New e2e coverage

- keep the existing one-node-cluster send regression unchanged
- add `TestE2E_WKProtoSendMessage_ThreeNodeCluster_CrossNodeClosure`

### Helper-level coverage

Add or extend fast tests for the e2e suite helpers where practical:

- three-node config rendering
- manager slot topology parsing and validation
- cluster discovery error formatting
- temp log directory rendering

Heavy process tests remain behind `-tags=e2e`.

## Risks and Mitigations

### Risk: cluster observation is briefly behind readiness

A node may pass `/readyz` while the manager slot view is still converging.

Mitigation:

- keep readiness and topology discovery as separate steps
- let `ResolveSlotTopology` retry until slot `1` reports quorum `ready`, sync
  `matched`, and one non-zero leader

### Risk: tests become flaky through fixed timing assumptions

Mitigation:

- use `/readyz` and manager slot state instead of fixed sleeps
- use protocol-level handshake checks before starting assertions

### Risk: repository log pollution from process defaults

Mitigation:

- set `WK_LOG_DIR` explicitly per node inside `t.TempDir()`
- keep stdout/stderr and app logs under the same node-scoped temp tree

## Success Criteria

Phase 2 is successful when:

- `test/e2e` can start a real three-node cluster locally
- the harness determines readiness without fixed sleeps
- the harness discovers the current slot leader through manager APIs
- one sender on follower A can send to one recipient on follower B and complete
  `Connect -> Send -> SendAck -> Recv -> RecvAck`
- diagnostics are strong enough that a failed CI or local run tells the
developer which node was not ready or which cluster view was inconsistent
- later leader crash and rolling restart scenarios can be added on top of the
  same `StartedCluster` foundation
