# wkbench Activate Channels Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a first-class `wkbench capacity activate-channels` scenario that proves N ChannelV2 group channels can be prepared, activated through real WKProto SEND, held live, probed, reported, and optionally evicted on an already-running cluster.

**Architecture:** Split the implementation into small phase plans. The default activation path stays black-box: `wkbench` prepares data through `/bench/v1/*`, sends exactly one group SEND per generated channel through the gateway, then reads bench-only runtime evidence from target nodes. Runtime observation/control is exposed through a narrow `pkg/channelv2 -> pkg/clusterv2 -> internalv2/app -> internalv2/access/api` boundary, while wkbench reuses the existing capacity runner, coordinator, temporary local worker, target client, and report patterns.

**Tech Stack:** Go stdlib HTTP/JSON/flag, existing `internal/bench` wkbench packages, `pkg/channelv2`, `pkg/clusterv2`, `internalv2/access/api`, Bash script wrappers, `go test` with `GOWORK=off`.

---

## Phase Plans

Execute these in order. Each phase is intentionally small enough to review and commit on its own.

1. `docs/superpowers/plans/2026-05-30-wkbench-activate-channels-01-api-contract.md`
   Shared JSON DTOs, target client methods, and internalv2 bench HTTP handlers with fake runtime controllers.

2. `docs/superpowers/plans/2026-05-30-wkbench-activate-channels-02-channelv2-runtime.md`
   ChannelV2 root DTOs plus reactor/service snapshot, probe, and evict support.

3. `docs/superpowers/plans/2026-05-30-wkbench-activate-channels-03-runtime-wiring.md`
   clusterv2 public facade, internalv2 infra adapter, and app API wiring.

4. `docs/superpowers/plans/2026-05-30-wkbench-activate-channels-04-runner.md`
   `internal/bench/capacity` activation config, scenario builder, runner, runtime evaluation, JSON/Markdown reports.

5. `docs/superpowers/plans/2026-05-30-wkbench-activate-channels-05-cli-scripts.md`
   `cmd/wkbench capacity activate-channels`, local non-Compose three-node wrapper, docs, and final verification.

## Scope

Implemented across the four phase plans:

- Capabilities, snapshot, probe, and evict bench API routes for internalv2 targets.
- ChannelV2 runtime snapshot/probe/evict support exposed through clusterv2 and app wiring.
- `wkbench capacity activate-channels` using real WKProto SEND activation.
- Hard pass/fail evaluation for activation count, send errors, sendack p99, runtime leader count, activation rejection deltas, and probe misses.
- Local non-Compose script wrapper that follows `scripts/start-wukongimv2-three-nodes.sh`.

Deferred:

- Server-side runtime activation route.
- Fault injection routes for slowdown or pause.
- Automatic maximum-channel search.
- ChannelV2 performance tuning.

## Cross-Phase Contracts

- `internal/bench/model` owns JSON-shaped bench API DTOs.
- `pkg/channelv2` owns runtime-shaped DTOs and must not import `internal/bench`.
- `internalv2/infra/cluster` maps runtime DTOs to HTTP/wkbench DTOs.
- HTTP handlers validate protocol input only; runtime facts come from the controller.
- The activation benchmark succeeds only when real SEND/SENDACK activates the channels. Snapshot/probe/evict APIs provide evidence and cleanup, not a bypass success path.

## Final Verification

Run after all phase plans are implemented:

```bash
GOWORK=off go test ./internal/bench/model ./internal/bench/target ./internalv2/access/api ./internalv2/infra/cluster ./internalv2/app ./pkg/channelv2/... ./pkg/clusterv2/... ./internal/bench/capacity ./cmd/wkbench ./scripts -count=1
bash -n scripts/activate-wukongimv2-three-nodes-10kch.sh scripts/bench-wukongimv2-three-nodes-10kch.sh
```

Expected:

```text
all listed go test packages pass
bash -n exits 0
```
