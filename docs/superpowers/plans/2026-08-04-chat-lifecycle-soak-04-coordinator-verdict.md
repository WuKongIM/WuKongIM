# Chat Lifecycle Soak Phase 4: Coordinator and Verdict Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Coordinate three independent workers, prove repeated all-node Channel runtime transitions, and produce deterministic product/harness verdicts for fixed-pressure and aged-data capacity runs.

**Architecture:** A fourth-host coordinator owns no traffic sockets. It validates topology/configuration, prepares the fixed group catalog, assigns workers, grants global rate, polls workers and service/host observers, selects bounded lifecycle cohorts, and reduces evidence into checkpoints and final reports. Raw identities exist only in transient authenticated probe requests; persisted reports use hashes/sample indexes.

**Tech Stack:** Go coordinator state machine, existing target/metrics/bench APIs, Prometheus text parsing, protected debug endpoints, node_exporter filesystem metrics, worker HTTP clients, JSON and Markdown reports.

---

## Task 1: Implement formal preflight and target observation

**Files:**

- Create: `internal/bench/chatlifecycle/observer.go`
- Create: `internal/bench/chatlifecycle/observer_test.go`
- Create: `internal/bench/chatlifecycle/preflight.go`
- Create: `internal/bench/chatlifecycle/preflight_test.go`
- Modify: `internal/bench/target/client.go`
- Modify: `internal/bench/target/client_test.go`

- [ ] **Step 1: Write target-pool topology tests**

Formal configuration must name three service-node metrics/debug endpoints, a balanced API endpoint pool, a separately balanced TCP gateway pool, three worker endpoints, and three host-metrics endpoints. Reject reused worker/service addresses and an API pool that is merely copied as the gateway pool. Local shakeout may explicitly set `profile: local_shakeout` and use loopback ports.

- [ ] **Step 2: Write minimal debug/metrics decoding tests**

Extend the target client only as needed to read:

- health and readiness;
- protected `/debug/config` values for 12 initial Slot groups, 256 hash slots, replica counts 3/3, and `max_channels=50000` per node;
- protected `/debug/cluster` Slot leader/replica/ISR/lag facts;
- product Prometheus metrics, including Go/process, runtime queues, channel activation rejection, and channel metadata creation;
- protected forced-GC heap trigger used before a metrics scrape.

Use minimal response structs so unrelated debug response additions do not break the benchmark.

- [ ] **Step 3: Write node_exporter disk tests**

Parse `node_filesystem_avail_bytes` and `node_filesystem_size_bytes` for an explicitly configured data mount/device. Assert each service node starts with at least 1,000,000,000,000 bytes on that filesystem. Missing or duplicate matches are `harness_invalid`; below 5% free is `infrastructure_failure` and triggers a safe coordinated stop.

- [ ] **Step 4: Implement five-second observation polling**

Poll health, readiness, and cluster state every five seconds. A continuous 30-second failure yields `product_failure`. Assert all 12 Slot groups have one leader and three replicas; for hot groups, ISR or replication lag outside thresholds for 30 seconds fails. Leader imbalance above 20% for ten minutes fails.

- [ ] **Step 5: Write preflight outcome tests**

Cover wrong Slot count, wrong hash-slot count, replica mismatch, missing bench capability, unreachable worker, unauthorized debug/bench endpoint, host disk ambiguity, and valid formal/local profiles. No traffic assignment may occur before preflight passes.

- [ ] **Step 6: Run observer/preflight tests**

```bash
GOWORK=off go test ./internal/bench/target ./internal/bench/chatlifecycle -run 'Observer|Preflight|Disk|ClusterHealth' -count=1
```

Expected: PASS.

## Task 2: Prepare the fixed group catalog and assign workers

**Files:**

- Create: `internal/bench/chatlifecycle/coordinator.go`
- Create: `internal/bench/chatlifecycle/coordinator_test.go`
- Create: `internal/bench/chatlifecycle/setup.go`
- Create: `internal/bench/chatlifecycle/setup_test.go`

- [ ] **Step 1: Write setup idempotency tests**

Use existing benchmark preparation APIs to create the 2,000 deterministic group channels and fixed membership before traffic. Repeating setup for the same `run_id` must be safe; a shape mismatch for an existing group must fail. Person channels must never be pre-created through setup APIs.

- [ ] **Step 2: Write assignment/fencing tests**

Deterministically partition global user indexes and rate weights across exactly three workers. Assert no overlap/gap, one coordinator generation, and refusal to combine snapshots from another `run_id`, assignment, or generation.

- [ ] **Step 3: Implement coordinator startup order**

Use this strict state sequence:

```text
preflight -> group setup -> worker assign -> worker start -> observe -> checkpoint/finalize
```

If any worker fails after assignment, stop the other workers and return `harness_invalid`. If any server process becomes unavailable for 30 seconds, stop workers and return `product_failure`. Do not resume either run.

- [ ] **Step 4: Add global grant and snapshot aggregation**

Issue grants whose three-worker sum is the Phase 2 global token bucket. Aggregate histograms with compatible bucket schemas; reject a snapshot whose counters regress or whose worker clock/sequence is stale.

- [ ] **Step 5: Run coordinator setup tests**

```bash
GOWORK=off go test ./internal/bench/chatlifecycle -run 'Setup|Assignment|CoordinatorStart|Grant' -count=1
```

Expected: PASS.

## Task 3: Prove loaded, naturally absent, and reheated Channel runtimes

**Files:**

- Create: `internal/bench/chatlifecycle/lifecycle_proof.go`
- Create: `internal/bench/chatlifecycle/lifecycle_proof_test.go`
- Modify: `internal/bench/chatlifecycle/worker_protocol.go`
- Modify: `internal/bench/chatlifecycle/worker_server.go`
- Modify: `internal/bench/chatlifecycle/worker_server_test.go`

- [ ] **Step 1: Define a bounded lifecycle-candidate lease**

Workers may return up to the requested number of candidate records over the authenticated control link. Each record contains a concrete canonical person channel identity, physical Slot assignment, initial sequence, quiet-window bounds, and deterministic reheat deadline. Candidates are transient and never emitted verbatim into reports.

- [ ] **Step 2: Write balanced cohort-selection tests**

Every ten minutes select exactly 1,200 candidates: 100 per logical Slot group across all 12 groups. Prefer revisit schedules that have already been observed loaded, will receive no messages for more than the product's natural five-minute idle eviction interval, and will deterministically reheat afterward. Reject duplicate channels or an undersupplied Slot cohort as `harness_invalid`.

- [ ] **Step 3: Write all-node transition tests**

Using fake explicit-probe responses, require this state machine for every candidate:

```text
loaded on all three replicas with one leader and monotonic LEO/HW
-> naturally absent from all three nodes without eviction API calls
-> real SEND reheat
-> loaded on all three replicas with sequence continuing above initial sequence
```

Any `error`, `closing`, stuck loaded state past the bounded quiet deadline, partial replica reheat, or sequence reset fails the lifecycle cohort. Classify cold/reheat SENDACK latency only after the same candidate has been proven absent on all three nodes; unproven returning conversations stay out of the cold histogram.

- [ ] **Step 4: Implement asynchronous probe polling**

Batch explicit probes at no more than 1,200 identities per request and poll all three service nodes. Probe traffic must not count as Channel activity or extend eviction. Bound concurrent requests and record probe latency/transport errors separately from product lifecycle failure.

- [ ] **Step 5: Reconcile authoritative create counts**

At every checkpoint, compute expected unique channel creations from deterministic person edges plus prepared groups and compare it with the sum of `wukongim_channelv2_meta_created_total{result="created"}`. Reheated candidates add zero to expected unique creations; concurrent losers may increment `already_existing` but must not increment `created`. This aggregate accounting proves reheat did not recreate metadata without adding a channel-ID metric label.

- [ ] **Step 6: Run lifecycle proof tests**

```bash
GOWORK=off go test ./internal/bench/chatlifecycle -run 'LifecycleCandidate|LifecycleProof|MetaCreateAccounting' -count=1
```

Expected: PASS.

## Task 4: Evaluate correctness, latency, resource, and cluster thresholds

**Files:**

- Create: `internal/bench/chatlifecycle/verdict.go`
- Create: `internal/bench/chatlifecycle/verdict_test.go`
- Create: `internal/bench/chatlifecycle/window.go`
- Create: `internal/bench/chatlifecycle/window_test.go`

- [ ] **Step 1: Write correctness verdict tests**

Any verified message loss, duplicate, corruption, or sequence regression fails immediately. Terminal sends and Channel activation rejections must be zero. First-attempt failure over the whole run must stay below 0.01%, and every one-minute window must stay at or below 0.1%. Worker queue saturation or observer gaps classify as `harness_invalid`.

- [ ] **Step 2: Write latency-window tests**

After the first two warmup hours, enforce these five-minute windows:

| Operation | p99 | p99.9 |
| --- | ---: | ---: |
| Hot SENDACK | 200 ms | 1 s |
| Cold/reheat SENDACK | 2 s | 5 s |
| Full conversation sync | 1 s | 3 s |

Any individual operation over 10 seconds is an anomaly sample. A threshold breach sustained for five minutes fails; shorter breaches remain warnings/evidence.

- [ ] **Step 3: Write resource-slope tests**

From hourly forced-GC metrics samples, calculate rolling six-hour live-heap slope and fail above 5%. Calculate 24-hour goroutine slope and fail above 5%. Queue depth/inflight must return to their established warm baseline after bursts. Evaluate each node separately; do not hide one leaking node behind an average.

- [ ] **Step 4: Write classification precedence tests**

Use closed outcomes:

```text
pass
product_failure
harness_invalid
infrastructure_failure
operator_stop
```

Disk below 5% is infrastructure failure; server crash or product threshold is product failure; worker/coordinator/observer saturation is harness invalid. Preserve the first terminal cause and attach later cleanup errors separately.

- [ ] **Step 5: Implement rolling reducers**

Use bounded ring windows for one-minute, five-minute, six-hour, and 24-hour calculations. Long runs must not retain raw metric samples indefinitely.

- [ ] **Step 6: Run verdict tests**

```bash
GOWORK=off go test ./internal/bench/chatlifecycle -run 'Verdict|LatencyWindow|ResourceSlope|Classification' -count=1
```

Expected: PASS.

## Task 5: Produce continuous checkpoints and redacted reports

**Files:**

- Create: `internal/bench/chatlifecycle/checkpoint.go`
- Create: `internal/bench/chatlifecycle/checkpoint_test.go`
- Create: `internal/bench/chatlifecycle/report.go`
- Create: `internal/bench/chatlifecycle/report_test.go`

- [ ] **Step 1: Write continuous-run checkpoint tests**

The two-hour shakeout is a separate run profile. In the formal run, the 24-hour checkpoint must write atomically without stopping/reassigning workers; the same process and run generation continue to the 72-hour final verdict.

- [ ] **Step 2: Define versioned JSON and Markdown output**

Include configuration digest, topology proof, worker generations, time windows, message/sync/lifecycle/meta-create accounting, latency/resource/cluster evidence, terminal classification, and capacity results. Use stable sample hashes/indexes only.

- [ ] **Step 3: Write redaction tests**

Seed fixtures with bearer tokens, raw UIDs, concrete channel IDs, and marker payloads. Serialize both formats and assert none appears. Atomic output uses a temporary sibling file followed by rename.

- [ ] **Step 4: Implement checkpoint/final report generation**

The report writer must not turn warning-only evidence into pass/fail changes. Include the exact threshold version and design profile used so results remain interpretable after defaults evolve.

- [ ] **Step 5: Run report tests**

```bash
GOWORK=off go test ./internal/bench/chatlifecycle -run 'Checkpoint|Report|Redact' -count=1
```

Expected: PASS.

## Task 6: Add the aged-data capacity staircase

**Files:**

- Create: `internal/bench/chatlifecycle/capacity.go`
- Create: `internal/bench/chatlifecycle/capacity_test.go`
- Modify: `internal/bench/chatlifecycle/coordinator.go`
- Modify: `internal/bench/chatlifecycle/coordinator_test.go`

- [ ] **Step 1: Write staircase-state tests**

Capacity mode requires the completed 72-hour checkpoint and the same running service dataset. Start at 2,000 SEND/s. Each step increases by 25% and lasts 30 minutes: ten minutes stabilization, then twenty minutes measurement. On first failed step, refine the interval in 10% increments.

- [ ] **Step 2: Write recovery tests**

After the failed/refined boundary, reduce to 2,000 SEND/s for 30 minutes without restarting service nodes. Pass recovery only when queues/inflight, latency, error rate, cluster lag, and resource pressure return to their accepted baseline ranges.

- [ ] **Step 3: Implement rate-control and terminal rules**

Product correctness failures still terminate immediately. A capacity threshold failure records the boundary and proceeds only to the bounded refinement/recovery sequence. Clean-dataset capacity remains an optional separate invocation, never substituted for aged-data evidence.

- [ ] **Step 4: Run capacity tests**

```bash
GOWORK=off go test ./internal/bench/chatlifecycle -run 'Capacity|Staircase|Recovery' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit coordinator/verdict work**

```bash
git add internal/bench/chatlifecycle internal/bench/target
git commit -m "feat(wkbench): add chat lifecycle coordinator verdicts"
```

```bash
git add internal/bench/chatlifecycle
git commit -m "feat(wkbench): add chat lifecycle capacity search"
```

## Task 7: Phase verification

- [ ] **Step 1: Run the package under race detection**

```bash
GOWORK=off go test -race ./internal/bench/chatlifecycle ./internal/bench/target -count=1
```

Expected: PASS.

- [ ] **Step 2: Run repeated deterministic tests**

```bash
GOWORK=off go test ./internal/bench/chatlifecycle -run 'RelationshipGraph|Assignment|LifecycleProof|Verdict|Capacity' -count=20
```

Expected: PASS with byte-identical deterministic fixtures.

- [ ] **Step 3: Inspect the phase diff**

```bash
git diff --check
git status --short
```

Expected: no whitespace errors and no unrelated files staged.
