# Chat Lifecycle Soak Phase 2: Deterministic Workload Model Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a deterministic, history-independent model that reproduces the approved user, relationship, session, channel, group, payload, rate, and retry distributions from compact indexes.

**Architecture:** Introduce `internal/bench/chatlifecycle` as an entry-agnostic benchmark domain package. Pure functions derive every historical identity and decision from a stable seed, worker assignment, and monotonic index; only the later worker runtime may hold online sessions and bounded scheduling/verifier windows. Validate all formal invariants at config load so a malformed run never reaches the target.

**Tech Stack:** Go pure functions and heap-based scheduling primitives, existing benchmark model/config conventions, `pkg/protocol/channelid` canonical person-channel normalization, table-driven/property-style unit tests.

---

## Task 1: Define and validate the formal configuration

**Files:**

- Create: `internal/bench/chatlifecycle/doc.go`
- Create: `internal/bench/chatlifecycle/config.go`
- Create: `internal/bench/chatlifecycle/config_test.go`
- Create: `internal/bench/chatlifecycle/types.go`
- Create: `internal/bench/chatlifecycle/FLOW.md`

- [ ] **Step 1: Write valid-default and invalid-boundary tests**

The default formal profile must encode these exact values:

```text
workers=3                     online_users=10000
new_users_per_day=250000      send_rate=2000/s
person_share=90%              group_share=10%
logical_slot_groups=12        hash_slots=256
runtime_sample_every=10m      runtime_sample_size=1200
sync_limit=500                sync_msg_count=20
short_burst_credit=2s         max_burst=4000
max_channels_per_node=50000   disk_stop_free_percent=5
```

Reject non-three-worker formal profiles, sample sizes above 1,200, shares that do not total 100%, retry counts above three, sync limits other than 500, empty seeds/run IDs, and a capacity profile without a completed 72-hour aged checkpoint.

Run:

```bash
GOWORK=off go test ./internal/bench/chatlifecycle -run 'TestDefaultConfig|TestConfigValidate' -count=1
```

Expected: FAIL because the package does not exist.

- [ ] **Step 2: Implement typed durations, distributions, and validation**

Keep production-host addresses and secrets outside pure workload structs. Separate `WorkloadConfig`, `ObservationConfig`, `Thresholds`, and `CapacityConfig` so tests can construct the model without HTTP or sockets.

- [ ] **Step 3: Document the package flow**

`FLOW.md` must describe `config -> deterministic plan -> worker runtime -> bounded snapshots -> coordinator verdict`, identify the history-independent memory rule, and state that public APIs/WKProto are the only product mutation paths.

- [ ] **Step 4: Run the package tests**

```bash
GOWORK=off go test ./internal/bench/chatlifecycle -run 'Config' -count=1
```

Expected: PASS.

## Task 2: Derive users and a bounded sparse relationship graph

**Files:**

- Create: `internal/bench/chatlifecycle/identity.go`
- Create: `internal/bench/chatlifecycle/identity_test.go`
- Create: `internal/bench/chatlifecycle/relationship.go`
- Create: `internal/bench/chatlifecycle/relationship_test.go`

- [ ] **Step 1: Write identity determinism and partition tests**

For a fixed run ID/seed, assert that all three workers derive disjoint UIDs from their assigned global indexes, repeated derivation is byte-identical, and no generated UID or channel identifier contains bearer tokens or payload data.

- [ ] **Step 2: Write the one-million-channel/day graph test**

For each global user index, derive an outgoing degree of 3, 4, or 5 with a deterministic 25/50/25 split and connect to the next `degree` global user indexes. This gives every mature user 3–5 incoming and 3–5 outgoing edges, prevents celebrity nodes, and yields exactly four new undirected person relationships per user on average.

Test a 250,000-user virtual day without retaining edge objects and assert:

```text
average outgoing degree = 4
unique created person relationships = 1,000,000
mature per-user conversation degree is between 6 and 10
all edges point from lower to higher global index
```

Run:

```bash
GOWORK=off go test ./internal/bench/chatlifecycle -run 'Identity|RelationshipGraph' -count=1
```

Expected: FAIL before graph derivation exists.

- [ ] **Step 3: Implement index-only edge reconstruction**

Expose functions that reconstruct outgoing owners and incoming owners from at most the preceding five indexes. Do not build a global adjacency map. Use `channelid.NormalizePersonChannel` only for internal lifecycle evidence; a real person `SEND` still addresses the peer UID as required by the gateway protocol.

- [ ] **Step 4: Add returning-conversation selection tests**

Select one or two reconstructable edges per returning login. Across a deterministic large sample, 80% must select a last-24-hour index bucket and 20% an older bucket. Each revisit schedules 2–5 messages. A selected conversation is classified as cold only after all-node runtime evidence proves absence.

- [ ] **Step 5: Run identity/graph tests**

```bash
GOWORK=off go test ./internal/bench/chatlifecycle -run 'Identity|Relationship|Returning' -count=1
```

Expected: PASS with bounded allocations in the large virtual-day test.

## Task 3: Model sessions and channel lifecycles

**Files:**

- Create: `internal/bench/chatlifecycle/schedule.go`
- Create: `internal/bench/chatlifecycle/schedule_test.go`

- [ ] **Step 1: Write distribution tests with fixed seeds**

Over a large deterministic sample, assert exact bucket assignment counts within one item of their target percentages:

```text
login identity: 80% new, 20% returning
session: 25% 5–15m, 50% 15–45m, 20% 45–120m, 5% 2–6h
channel lifecycle: 60% one-shot, 25% revisit, 10% rotating 20–40m, 5% long 2–4h
```

Also assert the session mean is approximately 46 minutes and new user arrival averages approximately 3.6/s for 250,000/day.

- [ ] **Step 2: Implement schedule derivation**

Use keyed deterministic draws, not a shared mutable PRNG whose output changes when a caller adds a draw. The key must include semantic purpose, such as `session-duration`, `lifecycle-class`, and `return-age`, so replay remains stable across worker restarts used in unit tests.

- [ ] **Step 3: Test first-burst and revisit timing**

Every new relationship must schedule 2–8 initial messages across 5–30 seconds while both endpoints are online. Revisit, rotating, and long schedules must remain within their approved duration windows and never keep a Channel runtime alive by polling it.

- [ ] **Step 4: Run scheduler tests**

```bash
GOWORK=off go test ./internal/bench/chatlifecycle -run 'Schedule|Distribution|InitialBurst' -count=1
```

Expected: PASS.

## Task 4: Model rate, payload, direction, retry, and groups

**Files:**

- Create: `internal/bench/chatlifecycle/rate.go`
- Create: `internal/bench/chatlifecycle/rate_test.go`
- Create: `internal/bench/chatlifecycle/payload.go`
- Create: `internal/bench/chatlifecycle/payload_test.go`
- Create: `internal/bench/chatlifecycle/retry.go`
- Create: `internal/bench/chatlifecycle/retry_test.go`
- Create: `internal/bench/chatlifecycle/groups.go`
- Create: `internal/bench/chatlifecycle/groups_test.go`

- [ ] **Step 1: Write global-rate apportionment tests**

Given three worker weights, prove integer per-tick grants sum to the coordinator's global 2,000 SEND/s target, unused credit expires after two seconds, and no release exceeds the 4,000-message global burst cap. Rate updates used by capacity mode must take effect on the next tick without retroactive debt.

- [ ] **Step 2: Implement deterministic grant/apportionment math**

Use fixed-point arithmetic or cumulative integer remainders so long runs do not drift from the global target. Worker-local token buckets must not each claim the global burst allowance.

- [ ] **Step 3: Write content and direction distribution tests**

Across a stable sample, prove:

```text
payload sizes: 70% 256B, 25% 1KiB, 4% 4KiB, 1% 16KiB
traffic: 90% person, 10% group
person direction: 70% alternating bidirectional, 30% one-way
```

Payload bytes must carry a compact versioned marker sufficient to verify run, logical send, sender, receiver/channel, attempt-independent message identity, and checksum. The marker must fit inside the 256-byte class.

- [ ] **Step 4: Write retry identity tests**

Assert attempts zero through three reuse exactly the same `client_msg_no`; delay bases are 100 ms, 500 ms, and 2 s; jitter is deterministic and bounded; and no fourth retry is scheduled.

- [ ] **Step 5: Write the fixed group catalog test**

Derive exactly 2,000 groups with fixed membership:

```text
1,600 groups with 5–20 members
300 groups with 100–500 members
99 groups with 1,000–10,000 members
1 group with 100,000 members and a 1/min send schedule
```

Assert the derived hot-set target is approximately 8,000 person channels plus 2,000 group channels and that group IDs/members reconstruct without a history-sized client map.

- [ ] **Step 6: Implement payload, retry, and group derivation**

Use fixed membership throughout a run. Group catalog growth and membership churn remain out of scope.

- [ ] **Step 7: Run all model tests**

```bash
GOWORK=off go test ./internal/bench/chatlifecycle -run 'Rate|Payload|Direction|Retry|Group' -count=1
```

Expected: PASS.

## Task 5: Bound allocations and finalize the model phase

**Files:**

- Modify: `internal/bench/chatlifecycle/identity_test.go`
- Modify: `internal/bench/chatlifecycle/relationship_test.go`
- Modify: `internal/bench/chatlifecycle/FLOW.md`

- [ ] **Step 1: Add allocation regression coverage**

Use `testing.AllocsPerRun` around identity, edge, payload-choice, and schedule derivation. Add a test that scans one virtual day of 250,000 users while retaining only counters and asserts heap growth is independent of the number of scanned historical users.

- [ ] **Step 2: Run the full new package**

```bash
GOWORK=off go test ./internal/bench/chatlifecycle -count=1
```

Expected: PASS in the normal fast unit tier; no sleeps, sockets, or background processes are allowed in these tests.

- [ ] **Step 3: Check package boundaries**

```bash
go list -deps ./internal/bench/chatlifecycle | rg '^github.com/WuKongIM/WuKongIM/internal/(access|app|infra|runtime)/'
```

Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add internal/bench/chatlifecycle
git commit -m "feat(wkbench): add deterministic chat lifecycle model"
```
