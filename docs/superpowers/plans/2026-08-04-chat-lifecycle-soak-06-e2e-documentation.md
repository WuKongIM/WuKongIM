# Chat Lifecycle Soak Phase 6: E2E and Repository Knowledge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove the complete lifecycle against real three-node processes and leave all changed architectural, operational, and benchmark contracts accurately documented.

**Architecture:** Add one process-level E2E scenario that creates a real person channel via WKProto, performs real full login sync, observes the Channel runtime on all three replicas, waits for natural idle eviction without a control API, reheats by real SEND, and proves sequence/metadata continuity. Then close wiring coverage, update every affected FLOW, and run the repository's scoped unit/integration/E2E gates.

**Tech Stack:** Existing `test/e2e/suite` three-node harness, real `wukongim` processes, HTTP and WKProto clients, restricted bench probes/metrics, integration/E2E Go build tags, Markdown FLOW/knowledge documentation.

---

## Task 1: Add subtree instructions and a real natural-lifecycle E2E

**Files:**

- Create: `test/e2e/message/chat_lifecycle/AGENTS.md`
- Create: `test/e2e/message/chat_lifecycle/chat_lifecycle_test.go`
- Modify: `test/e2e/message/AGENTS.md`

- [ ] **Step 1: Record exact E2E boundaries**

The subtree `AGENTS.md` must require:

- process-level black-box traffic and observation only;
- a three-node cluster with 12 logical Slot groups, 256 hash slots, and replicas 3/3;
- actual natural idle wait longer than five minutes;
- no runtime eviction endpoint, mocked clock, direct DB write, Docker, or product-internal runtime call;
- bounded polling and a nine-minute package timeout;
- raw IDs only in transient test logs when the test fails.

- [ ] **Step 2: Write the E2E test skeleton and observe failure**

Use the `e2e` build tag and `suite.New(t).StartThreeNodeCluster` with bench, metrics, and protected debug capability enabled. Configure `WK_CLUSTER_INITIAL_SLOT_COUNT=12`, 256 hash slots, Slot/Channel replicas 3, and `max_channels=50000`.

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/chat_lifecycle -run TestPersonChannelNaturalReheat -count=1 -timeout=9m -p=1
```

Expected: FAIL until the scenario and Phase 1 product evidence are wired.

- [ ] **Step 3: Implement real login, send, receive, and sync**

Connect two deterministic users through TCP, invoke full `/conversation/sync` with version zero after login, send the first person message, require SENDACK, parse/ACK RECV, and record the assigned message sequence. Derive the canonical internal person channel only for the probe.

- [ ] **Step 4: Prove initial authoritative state**

Poll explicit probes on all three nodes until exactly one leader and two followers report loaded with consistent monotonic LEO/HW. Scrape the create metric and assert exactly one `created` increment for the channel's Slot in this isolated test.

- [ ] **Step 5: Wait for natural absence**

Close both sessions and send no traffic/probe that refreshes activity. Sleep/poll beyond the real five-minute idle timeout plus bounded scheduler grace until all three nodes report absent. Fail with node-by-node runtime detail if the deadline expires.

- [ ] **Step 6: Reheat and prove continuity**

Reconnect, perform another version-zero full sync, send a real person message, and require SENDACK/RECV/RECVACK. Poll all replicas loaded again, assert the new sequence is greater than the first, and assert the authoritative `created` counter did not increase during reheat.

- [ ] **Step 7: Run the isolated E2E three times**

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/chat_lifecycle -run TestPersonChannelNaturalReheat -count=3 -timeout=27m -p=1
```

Expected: PASS three consecutive process-level runs.

## Task 2: Close application composition coverage

**Files:**

- Modify: `internal/app/observability_test.go`
- Create: `internal/app/chat_lifecycle_bench_wiring_test.go`
- Modify: `internal/app/FLOW.md`

- [ ] **Step 1: Write an app wiring test**

Construct the composition root with fake Slot proposer/channel runtime dependencies and assert:

- the channel metrics observer reaches the authoritative Slot proposer;
- the multi-observer forwards create outcomes once;
- the restricted bench handler reaches the infra explicit runtime probe;
- bench/debug auth middleware rejects a missing bearer token;
- normal public entrypoints do not expose the restricted probe.

- [ ] **Step 2: Run the wiring test before changes**

```bash
GOWORK=off go test ./internal/app -run 'TestChatLifecycleBenchWiring' -count=1
```

Expected: FAIL until the test and any missing Phase 1 wiring exist.

- [ ] **Step 3: Make the smallest wiring corrections**

Change only composition. Do not move creation or probe business rules into `internal/app`.

- [ ] **Step 4: Run app/access/infra tests**

```bash
GOWORK=off go test ./internal/app ./internal/access/api ./internal/infra/cluster -run 'ChatLifecycle|MetaCreate|RuntimeProbe|Observability' -count=1
```

Expected: PASS.

## Task 3: Update architectural flow and project knowledge

**Files:**

- Modify: `internal/bench/FLOW.md`
- Modify: `pkg/bench/model/FLOW.md`
- Modify: `pkg/cluster/FLOW.md`
- Modify: `pkg/slot/FLOW.md`
- Modify: `internal/access/api/FLOW.md`
- Modify: `internal/infra/cluster/FLOW.md`
- Modify: `internal/app/FLOW.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`
- Modify: `cmd/wkbench/README.md`
- Modify: `test/e2e/message/AGENTS.md`

- [ ] **Step 1: Re-read each FLOW before editing its package documentation**

Confirm the final implementation names and boundaries before documenting them. Documentation must say `single-node cluster` wherever topology is discussed and distinguish node-local runtime absence from deleted replicated metadata.

- [ ] **Step 2: Update benchmark flow**

Document the dedicated chat-lifecycle coordinator/worker path, deterministic history reconstruction, real login synchronization, bounded verifier/evidence ownership, separate API/gateway balancing, continuous 24/72-hour checkpoints, and aged capacity continuation.

- [ ] **Step 3: Update product evidence flow**

Document the create-only Slot FSM result, Slot-leader observer point, closed metric result labels, explicit-probe 1,200 cap, detailed per-node runtime fields, restricted/authenticated route, and non-activity semantics of probes.

- [ ] **Step 4: Record durable repository knowledge**

Add concise entries covering:

- 12 logical Slot Raft Groups still distribute 256 hash slots;
- initial Channel runtime metadata creation is authoritative in the Slot FSM;
- Channel runtime eviction is node-local and must not delete replicated metadata;
- benchmark lifecycle evidence must use real SEND/reheat and full version-zero sync;
- formal soak uses native independent hosts, 1 TB per service node, and no resume.

- [ ] **Step 5: Check terminology and links**

```bash
rg -n 'standalone|stand-alone|Docker|Compose|initial_slot_count|256|1 TB|conversation/sync' docs/development/PROJECT_KNOWLEDGE.md internal/bench/FLOW.md pkg/cluster/FLOW.md pkg/slot/FLOW.md cmd/wkbench/README.md docs/superpowers/runbooks/2026-08-04-chat-lifecycle-soak.md
```

Expected: no standalone topology wording; Docker/Compose appears only as an explicit prohibition; Slot/hash/sync/storage contracts are consistent.

## Task 4: Run the complete scoped verification matrix

- [ ] **Step 1: Format changed Go files**

```bash
gofmt -w $(git diff --name-only -- '*.go')
```

Expected: exits zero. Inspect the resolved file list before running if unrelated Go files are already modified.

- [ ] **Step 2: Run all directly affected unit packages**

```bash
GOWORK=off go test ./pkg/db/meta ./pkg/slot/fsm ./pkg/cluster/... ./pkg/metrics ./pkg/bench/model ./internal/access/api ./internal/infra/cluster ./internal/app ./internal/bench/chatlifecycle ./internal/bench/wkproto ./internal/bench/target ./cmd/wkbench ./scripts -count=1
```

Expected: PASS.

- [ ] **Step 3: Run the repository unit gate**

```bash
GOWORK=off go test ./cmd/... ./internal/... ./pkg/... ./scripts/... ./docker/... -count=1
```

Expected: PASS. Do not replace this with root `./...`.

- [ ] **Step 4: Run the relevant integration script**

```bash
GOWORK=off go test -tags=integration ./scripts/... -run ChatLifecycle -count=1 -timeout=9m -parallel=1
```

Expected: PASS and no leaked child process.

- [ ] **Step 5: Run the real natural-lifecycle E2E**

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/chat_lifecycle -count=1 -timeout=9m -p=1
```

Expected: PASS with observed `loaded -> absent on all nodes -> reheated`, continuous sequence, and unchanged create count on reheat.

- [ ] **Step 6: Run race tests for the concurrent harness packages**

```bash
GOWORK=off go test -race ./internal/bench/chatlifecycle ./internal/bench/wkproto ./internal/bench/target -count=1
```

Expected: PASS.

- [ ] **Step 7: Scan for placeholders and unsafe output**

```bash
rg -n 'TBD|TODO|implement later|fill in|appropriate error handling|similar to' internal/bench/chatlifecycle cmd/wkbench configs/wkbench/chat-lifecycle docs/superpowers/runbooks/2026-08-04-chat-lifecycle-soak.md test/e2e/message/chat_lifecycle
rg -n 'Authorization: Bearer|bearer_token: [^<$]' configs/wkbench/chat-lifecycle docs/superpowers/runbooks/2026-08-04-chat-lifecycle-soak.md
git diff --check
```

Expected: all scans print no unsafe placeholder/secret match; diff check prints no output.

## Task 5: Commit and hand off formal evidence collection

- [ ] **Step 1: Commit E2E and wiring**

```bash
git add test/e2e/message/chat_lifecycle test/e2e/message/AGENTS.md internal/app
git commit -m "test(e2e): prove natural chat channel reheat lifecycle"
```

- [ ] **Step 2: Commit documentation**

```bash
git add internal/bench/FLOW.md pkg/bench/model/FLOW.md pkg/cluster/FLOW.md pkg/slot/FLOW.md internal/access/api/FLOW.md internal/infra/cluster/FLOW.md internal/app/FLOW.md docs/development/PROJECT_KNOWLEDGE.md cmd/wkbench/README.md
git commit -m "docs: record chat lifecycle soak contracts"
```

- [ ] **Step 3: Inspect final status**

```bash
git status --short
git log --oneline -10
```

Expected: only pre-existing unrelated user changes remain; the phase commit sequence is visible.

- [ ] **Step 4: Run formal evidence outside CI**

Follow the native-host runbook to execute the separate two-hour shakeout and the fresh continuous 24-to-72-hour run. Do not claim the product passed the soak until the redacted reports show a terminal `pass`. Capacity evidence is collected only after the 72-hour aged checkpoint succeeds.
