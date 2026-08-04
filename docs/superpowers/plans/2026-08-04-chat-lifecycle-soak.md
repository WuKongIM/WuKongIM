# Chat Lifecycle Soak Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `wkbench soak chat-lifecycle`, a black-box native-host workload that proves a continuously growing person-channel catalog can cycle through natural hot, cold, and reheated runtime states for 24 and 72 hours while login synchronization, message correctness, cluster health, and bounded resources remain valid.

**Architecture:** Keep existing wkbench modes unchanged and add a dedicated `internal/bench/chatlifecycle` subsystem. Three independent worker processes generate deterministic users, sparse person relationships, sessions, groups, and messages without retaining history-sized state; a fourth-host coordinator performs preflight, observation, lifecycle sampling, checkpoints, final verdicts, and an optional capacity staircase. Add only the bounded product evidence required to prove authoritative metadata creation and exact runtime presence, then exercise the product through HTTP and TCP WKProto on a three-node cluster with 12 logical Slot Raft Groups over 256 hash slots.

**Tech Stack:** Go, WKProto TCP, existing WuKongIM HTTP/bench/debug/Prometheus interfaces, TOML/YAML configuration already used by wkbench, Bash native-process orchestration, `go test` unit/integration/E2E tiers.

---

## Source of Truth

Implementation must satisfy the approved design:

- [Chat Lifecycle Soak Design](../specs/2026-08-04-chat-lifecycle-soak-design.md)
- Formal service topology: three independent service hosts, three independent load hosts, and one coordinator host.
- Local shakeout topology: three native `wukongim` processes; Docker and Compose are prohibited.
- Cluster contract: `initial_slot_count = 12`, 256 hash slots, Slot replica count 3, Channel replica count 3.
- Run contract: two-hour shakeout, continuous 24-hour checkpoint, continuous 72-hour final verdict, then optional aged-data capacity search.

Before changing any package in a phase, re-read the nearest `AGENTS.md` and that package's `FLOW.md`. Update an applicable `FLOW.md` in the same commit when its described contract changes.

## Phase Plans

Execute these in order. Each phase ends with focused verification and a reviewable commit.

1. [Phase 1 — Product Evidence](2026-08-04-chat-lifecycle-soak-01-product-evidence.md)
   Add authoritative metadata-creation results, the one new bounded metric, and an explicit bounded runtime probe.

2. [Phase 2 — Deterministic Workload Model](2026-08-04-chat-lifecycle-soak-02-deterministic-model.md)
   Define configuration, deterministic identities/relationships, lifecycle schedules, rate shaping, group catalog, payloads, and retry decisions.

3. [Phase 3 — Worker Runtime](2026-08-04-chat-lifecycle-soak-03-worker-runtime.md)
   Make WKProto receive verification lossless, implement real login synchronization, run traffic/session lifecycles, and expose a fenced dedicated worker-control API.

4. [Phase 4 — Coordinator and Verdict](2026-08-04-chat-lifecycle-soak-04-coordinator-verdict.md)
   Add formal preflight, cluster/resource observation, 1,200-channel lifecycle proof, correctness/latency/resource verdicts, checkpoints, reports, and capacity search.

5. [Phase 5 — CLI and Native Operations](2026-08-04-chat-lifecycle-soak-05-cli-native-operations.md)
   Wire commands, checked-in examples, native three-process shakeout automation, security defaults, and the operator runbook.

6. [Phase 6 — E2E and Repository Knowledge](2026-08-04-chat-lifecycle-soak-06-e2e-documentation.md)
   Prove a real natural eviction/reheat cycle, close cross-layer wiring coverage, update repository knowledge, and run the complete verification matrix.

## Frozen Cross-Phase Contracts

The following contracts must not drift between phases:

- `internal/bench/chatlifecycle` may depend on benchmark/public protocol packages, but must not import product runtime internals.
- Person traffic uses real TCP `CONNECT`, `/conversation/sync` after every login, `SEND`, `SENDACK`, `RECV`, and receive acknowledgements. It does not synthesize delivery success from metrics.
- `/conversation/sync` always sends `version=0`, empty `last_msg_seqs`, `limit=500`, and `msg_count=20`; a response of exactly 500 conversations is a failed run because full synchronization was not proven.
- Historical UIDs and relationships are reconstructed from `run_id`, worker assignment, seed, and monotonic indexes. Memory may scale with online/hot/verifier windows, never total history.
- Retry uses the same `client_msg_no`, at most three retries, and base delays of 100 ms, 500 ms, and 2 s with deterministic jitter.
- All message sends require `SENDACK`; all received frames are parsed and acknowledged. Exact end-to-end correlation samples 1% of sends.
- Worker or coordinator loss makes the run `harness_invalid`; a service process exit makes it `product_failure`. Formal runs do not resume or auto-restart.
- Reports contain hashed/sample references, never raw UIDs, tokens, or payloads.

## Requirement-to-Phase Coverage

| Requirement | Owning phase |
| --- | --- |
| Authoritative create/already-existing/error metric by physical Slot | 1 |
| Exact bounded all-node runtime presence and sequence evidence | 1, 4 |
| 250k new UIDs/day, approximately 1M person channels/day, 10k online | 2, 3 |
| 60/25/10/5 lifecycle mix and natural five-minute cold transition | 2, 3, 6 |
| 90/10 person/group mix, group catalog including 100k group | 2, 3 |
| Real full sync on every login with no retained client cursor | 3 |
| 2,000 SEND/s, two-second credit capped at 4,000 | 2, 3 |
| Full RECV verification plus 1% exact correlation | 3, 4 |
| Every-ten-minute 1,200-channel lifecycle proof across 12 Slots | 1, 4 |
| 24/72-hour latency, resource, cluster, disk, and queue verdicts | 4 |
| Aged-data capacity staircase and recovery step | 4 |
| Native no-Docker launch/configuration/runbook | 5 |
| Real process-level natural eviction/reheat acceptance | 6 |

## Commit Sequence

Use these commit boundaries unless an earlier step exposes a smaller independently useful boundary:

```text
feat(slot): expose authoritative channel meta create result
feat(metrics): count authoritative channel meta creation
feat(bench): probe explicit channel runtimes
feat(wkbench): add deterministic chat lifecycle model
feat(wkbench): add chat lifecycle worker runtime
feat(wkbench): add chat lifecycle coordinator verdicts
feat(wkbench): add chat lifecycle capacity search
feat(wkbench): add chat lifecycle commands and native runbook
test(e2e): prove natural chat channel reheat lifecycle
docs: record chat lifecycle soak contracts
```

Do not combine product metric/probe changes with the workload implementation; those changes require separate review for production surface area.

## Final Verification

Run from the repository root after all phases:

```bash
GOWORK=off go test ./pkg/db/meta ./pkg/slot/fsm ./pkg/cluster/... ./pkg/metrics ./pkg/bench/model ./internal/access/api ./internal/infra/cluster ./internal/app ./internal/bench/chatlifecycle ./internal/bench/wkproto ./internal/bench/target ./cmd/wkbench ./scripts -count=1
GOWORK=off go test -tags=integration ./scripts/... -run ChatLifecycle -count=1 -timeout=9m -parallel=2
GOWORK=off go test -tags=e2e ./test/e2e/message/chat_lifecycle -count=1 -timeout=9m -p=1
git diff --check
```

Expected:

```text
all listed unit packages pass
native chat-lifecycle script integration tests pass
the E2E test observes loaded -> naturally absent -> reheated with continuous sequence
git diff --check prints no output
```

The 24-hour and 72-hour formal host runs are release evidence, not normal CI gates. Attach their redacted JSON and Markdown reports to the release or performance record.
