# internalv2 Dynamic Node Stage 9 Production Readiness Design

Date: 2026-06-29
Status: Proposed for review
Scope: internalv2 dynamic node production readiness, ControllerV2 health
freshness, lifecycle observability, real traffic validation

## 1. Purpose

Stages 1 through 8 made dynamic data-node lifecycle functional and fault-tested:
seed join, activation, Slot onboarding, scale-in preparation, safe removal, real
e2ev2 coverage, and gofail-backed join/onboarding/scale-in fault recovery are
now covered. Stage 9 should close the remaining production-readiness gap before
the feature is treated as safe to operate under real load.

The core missing piece is a durable health freshness loop. The dynamic-node
design intentionally separated membership from health, but health reports are
still not strong enough to gate placement or removal. A node can be a durable
`active` member while its runtime state is stale, unknown, or unreachable. Stage
9 makes that distinction explicit and observable: fresh health can unlock
placement and safety checks, while stale or unknown health fails closed.

Stage 9 also proves the lifecycle under real client traffic. The result should
answer the operator question: "Can we add, onboard, drain, and remove a data
node while traffic continues, and can the system explain why it refuses unsafe
operations?"

## 2. Current State

Useful existing behavior:

- ControllerV2 is the durable membership authority for node lifecycle state.
- clusterv2 discovery already follows ControllerV2 snapshots after startup.
- Placement code excludes non-active lifecycle states.
- Scale-in status fails closed for unknown Channel inventory, runtime summary,
  Slot status, task status, and control-revision gaps.
- Stage 8 gofail tests prove important failure windows are reproducible and
  diagnose-able through manager status, task counters, and failpoint counts.
- e2ev2 already has real-process dynamic join, onboarding, scale-in, and removal
  scenarios under `test/e2ev2/cluster`.
- `wkcli sim` can generate real v2 traffic through public bench and WKProto
  surfaces without importing server internals.

Remaining gaps:

- `ReportNode` and `ReportSlots` are still best-effort and do not define a
  durable freshness contract.
- Placement cannot safely use health until reports have freshness, TTL, and
  fail-closed semantics.
- Manager and Prometheus do not yet expose a complete low-cardinality view of
  dynamic-node readiness, health freshness, lifecycle attempts, task blockers,
  and removal blocked reasons.
- There is no scripted real-traffic readiness run that combines dynamic join,
  onboarding, scale-in, and removal while clients continue to connect and send.

## 3. Goals

1. Add a durable, low-frequency node health report loop whose freshness can be
   evaluated by ControllerV2, clusterv2, manager, and placement logic.
2. Make health-gated placement fail closed: a node must be active, data-role,
   alive, and fresh before it can receive new Slot or Channel placement.
3. Make removal status explain stale or unknown health as explicit blockers.
4. Expose low-cardinality metrics and manager fields for dynamic-node lifecycle
   health, attempts, task state, and blocked reasons.
5. Add a real-traffic production smoke that exercises dynamic join, onboarding,
   scale-in, and removal while WKProto traffic continues.
6. Preserve the performance envelope for high-cardinality deployments: many
   nodes, 256 hash slots by default, large groups, high-frequency messages, many
   channels, and many online users.

## 4. Non-Goals

- Do not dynamically add or remove Controller Raft voters.
- Do not automatically rebalance all Slot or Channel ownership after join.
- Do not implement historical Channel physical migration.
- Do not physically delete `removed` node identities from ControllerV2 state.
- Do not add random chaos, clock-skew tests, storage corruption tests, or
  unbounded soak matrices.
- Do not put UID, channel ID, or task ID into high-cardinality Prometheus labels.
- Do not introduce any single-node-cluster shortcut that bypasses cluster
  semantics.
- Do not build new production code on `internal/bench/devsim`; real traffic
  validation should use public v2 surfaces and `wkcli sim` where useful.

## 5. Health Freshness Model

Membership and health remain separate.

Membership answers whether the node belongs to the cluster:

```text
joining -> active -> leaving -> removed
```

Health answers whether the node is currently usable and whether that evidence is
fresh:

```text
reported status: alive | suspect | down
freshness: fresh | stale | missing
effective health: alive | suspect | down | unknown
```

`unknown` is derived, not directly reported. A node is effectively `unknown`
when no report exists, the report is older than the configured TTL, the report
belongs to an older cluster epoch, or the report cannot be trusted for the
operation being evaluated.

ControllerV2 should store a compact health report per node:

```go
type NodeHealthReport struct {
    NodeID                  uint64
    Status                  NodeStatus
    RuntimeReady            bool
    ObservedControlRevision uint64
    ObservedSlotRevision    uint64
    ReportSeq               uint64
    ReportedAtUnixMilli     int64
    ErrorCode               string
}
```

This type is illustrative; the implementation plan may adjust names to match
local package conventions. The stored record must stay bounded and must not
include per-channel, per-UID, or per-session details.

### Freshness Rules

- A report is fresh when `now - reported_at <= health_report_ttl`.
- A report is stale when a previous report exists but the TTL expired.
- A report is missing when the node has no report in the current cluster state.
- `joining` nodes may report health, but they are still not schedulable until
  activation succeeds.
- `leaving` nodes may report health so drain and remove can be evaluated, but
  they are not schedulable.
- `removed` nodes are tombstones; stale or missing health does not resurrect
  them.
- Report timestamps should be evaluated on the Controller leader side to avoid
  trusting remote clocks for freshness.

### Reporting Cadence

Reports should be low frequency and bounded:

- Default interval: 5 seconds.
- Default TTL: 30 seconds.
- Report immediately on local readiness transitions when practical.
- Suppress unchanged periodic reports only if the suppression still preserves a
  fresh heartbeat before TTL expires.
- ControllerV2 writes should be idempotent and monotonic by `(node_id,
  report_seq)` or a local equivalent.

If the implementation adds config fields, they must use `WK_` names and update
`wukongim.conf.example` with detailed English comments.

## 6. Data Flow

The intended path is:

```text
local node runtime
  -> builds bounded health report
  -> clusterv2 control write / leader forwarding
  -> ControllerV2 stores report with leader-side timestamp
  -> ControllerV2 snapshot exposes membership + health freshness
  -> clusterv2 applies snapshot and updates placement candidate views
  -> internalv2 manager and Prometheus expose status and blocked reasons
```

The reporting path must not depend on manager HTTP. Manager reads the result; it
does not own the durable health truth.

## 7. Placement Semantics

Placement candidates must satisfy all of these:

- node has data role.
- `join_state == active`.
- effective health is fresh `alive`.
- runtime readiness is true.
- node is not `suspect`, `down`, `unknown`, `joining`, `leaving`, or `removed`.

This applies to:

- Slot onboarding target selection.
- ChannelV2 initial placement candidates.
- any manager planning API that proposes new placement.

Health-gated placement must not scan high-cardinality local state. It should use
the ControllerV2 snapshot or a bounded clusterv2 candidate view derived from the
snapshot.

If health freshness is unavailable because Stage 9 is only partially deployed,
the system should fail closed for new dynamic placement. Static bootstrap should
remain able to start a single-node cluster or static multi-node cluster through
the existing Controller voter path, but no new cluster-bypass branch should be
introduced.

## 8. Removal And Drain Semantics

Removal remains explicit and fail-closed. In addition to Stage 5 and Stage 8
blockers, Stage 9 adds health freshness blockers:

- target node health report is missing or stale.
- any alive/suspect data node has a missing or stale control-revision report
  needed to prove it observed the leaving revision.
- target runtime readiness or runtime summary cannot be trusted.
- report freshness cannot be evaluated because ControllerV2 state is stale or
  unavailable.

Manager scale-in status should expose these as explicit fields or blocked
reasons. Suggested manager-facing concepts:

```text
health_fresh
health_status
health_report_age_ms
health_report_ttl_ms
observed_control_revision
required_control_revision
blocked_by_health
blocked_by_stale_revision
blocked_reasons[]
```

The final remove route must continue to require `safe_to_remove=true`.
Stale or missing health must keep `safe_to_remove=false`.

## 9. Observability

Stage 9 should add low-cardinality Prometheus metrics and manager fields.

Recommended metrics:

- `wukongim_node_lifecycle_nodes{join_state,status}`
- `wukongim_node_health_freshness_nodes{freshness,status}`
- `wukongim_node_health_report_age_seconds`
- `wukongim_node_lifecycle_attempts_total{operation,result}`
- `wukongim_node_onboarding_tasks{state}`
- `wukongim_node_scale_in_blockers_total{reason}`
- `wukongim_slot_replica_move_duration_seconds`
- `wukongim_slot_replica_move_failures_total{reason}`
- `wukongim_discovery_membership_revision`

Metric names can be adjusted to the existing metrics package conventions. The
labels must stay bounded. Do not label by UID, channel ID, client ID, message
ID, or arbitrary error string.

Manager detail pages and APIs may include per-node fields because manager reads
are operator-facing and bounded by request pagination. The default node list
should stay lightweight. Full-cardinality detail must stay behind explicit
detail/status APIs.

## 10. Real Traffic Production Smoke

Stage 9 must prove behavior with real client traffic. The smoke should run
against real `cmd/wukongimv2` processes and public HTTP/WKProto surfaces.

Minimum scenario:

1. Start a static three-node cluster.
2. Start `wkcli sim` or an e2ev2 equivalent that connects users and sends real
   WKProto traffic at a bounded rate.
3. Start a fourth data node through seed join.
4. Activate node 4 after readiness gates pass.
5. Run Slot onboarding for at least one Slot while traffic continues.
6. Mark node 4 leaving.
7. Enable gateway drain.
8. Drain Slot placement away from node 4 while traffic continues.
9. Wait for `safe_to_remove=true`.
10. Remove node 4 and verify tombstone state.

The smoke should collect:

- send success and error counts.
- manager lifecycle status snapshots.
- health freshness snapshots.
- Slot and task status snapshots.
- Prometheus samples for the Stage 9 metrics.
- stdout/stderr and app log tails on failure.

The first Stage 9 smoke should be bounded, not a long soak. A later release can
add a longer soak once the short smoke is stable and cheap enough to run.

## 11. Performance Constraints

Stage 9 is production-readiness work, so it must be careful with hot paths:

- No SEND, append, fetch, delivery, or presence hot path should synchronously
  wait for health reporting.
- Health reports are low-frequency control-plane writes.
- Reports are per-node, not per-channel or per-session.
- Manager default list APIs must remain paginated/lightweight.
- Scale-in inventory APIs may scan explicit inventories, but those scans must
  stay bounded or be exposed as detail/status operations.
- The design must remain valid for 256 hash slots, large groups, high message
  rates, many channels, and many online users.

## 12. Error Handling

Health report writes are best-effort at the sender but durable at ControllerV2
once accepted. If a report write fails:

- local runtime logs a bounded warning.
- metrics record a report failure count with a bounded reason.
- the next interval retries.
- placement and removal eventually observe stale/unknown if reports cannot be
  committed.

Controller leader changes must not create duplicate unsafe state. Reports should
be idempotent and should not regress `ReportSeq` or freshness for a newer
accepted report.

Manager APIs should return bounded conflict responses for unsafe operations and
include blocked reasons where possible. They should not expose raw internal
errors or produce unbounded error strings in metrics labels.

## 13. Testing Strategy

Stage 9 should be implemented in small stages:

### Stage 9A: Health Report Model

- ControllerV2 state/codec tests for health report persistence and snapshots.
- clusterv2 control mapping tests for membership + health freshness.
- config tests if interval/TTL knobs are added.

### Stage 9B: Health-Gated Placement And Remove

- unit tests proving stale, missing, suspect, down, joining, leaving, and
  removed nodes are excluded from placement.
- manager scale-in status tests proving stale health and stale control revision
  block removal with explicit reasons.

### Stage 9C: Metrics And Manager Evidence

- metrics registry tests for low-cardinality labels.
- manager API tests for health freshness fields.
- regression tests ensuring default node lists stay bounded.

### Stage 9D: Real Traffic Production Smoke

- e2ev2 scenario or script using real `cmd/wukongimv2` and public surfaces.
- optional `wkcli sim` integration for sustained but bounded real traffic.
- serial execution with `-p=1` where the suite is timing-sensitive.

## 14. Exit Criteria

Stage 9 is complete when:

- Health reports have durable freshness semantics in ControllerV2 snapshots.
- Placement candidate views require fresh usable health.
- Scale-in status and final remove fail closed on stale/missing health and stale
  control-revision evidence.
- Prometheus and manager expose lifecycle health and blocked reasons with
  bounded labels/fields.
- A real-traffic dynamic-node smoke passes against real `cmd/wukongimv2`
  processes.
- Focused package tests, e2ev2 tests, `git diff --check`, and any config example
  checks pass on the implementation branch and again after local merge to
  `main`.

## 15. Handoff To Planning

The implementation plan should split this design into Stage 9A through Stage 9D
and keep each stage independently reviewable. The first plan should start with
the health report model and tests before touching placement or manager removal
logic.
