# WK Sim Traffic Concurrency Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `wk-sim` generate bounded concurrent traffic so a 1000-channel Docker Compose run can reach the intended ingress rate before server-side CPU pprof optimization.

**Architecture:** Add an optional per-traffic concurrency knob that defaults to current sequential behavior for normal wkbench scenarios. The dev simulator reads `WK_SIM_TRAFFIC_CONCURRENCY`, writes it into derived traffic streams, and the worker passes it into person/group workloads. Person/group workloads share a small bounded scheduler that preserves rate pacing while allowing multiple in-flight send+sendack operations.

**Tech Stack:** Go, internal wkbench model/devsim/worker/workload packages, Docker Compose dev-sim profile.

---

### Task 1: Bounded Scheduler Tests

**Files:**
- Create: `internal/bench/workload/scheduler_test.go`
- Create: `internal/bench/workload/scheduler.go`

- [ ] Write a failing test that calls `runScheduledMessages` with `total=6`, `concurrency=3`, and a blocking send function; assert only 3 sends start before release and the 4th starts after one finishes.
- [ ] Run: `go test ./internal/bench/workload -run TestRunScheduledMessages -count=1`; expected compile failure because `runScheduledMessages` does not exist.
- [ ] Implement `runScheduledMessages(ctx, total, interval, concurrency, send)` with a sequential compatibility path for `concurrency <= 1` and a bounded goroutine path for higher concurrency.
- [ ] Run the same test and confirm it passes.

### Task 2: Workload Wiring

**Files:**
- Modify: `internal/bench/workload/person.go`
- Modify: `internal/bench/workload/group.go`
- Modify: `internal/bench/model/config.go`
- Modify: `internal/bench/worker/person_runner.go`
- Modify: `internal/bench/worker/group_runner.go`

- [ ] Add `Concurrency int` to `model.TrafficConfig` with an English comment.
- [ ] Add `MaxConcurrency int` to `PersonConfig` and `GroupConfig` with English comments.
- [ ] Pass traffic concurrency from worker builders into workload configs.
- [ ] Change person/group `runFor` to use `runScheduledMessages`; keep existing sleeps when concurrency is unset.
- [ ] Run: `go test ./internal/bench/workload ./internal/bench/worker -count=1`.

### Task 3: Dev-Sim Config and Compose Defaults

**Files:**
- Modify: `internal/bench/devsim/config.go`
- Modify: `internal/bench/devsim/config_test.go`
- Modify: `docker-compose.yml`
- Modify: `docker/sim/README.md`

- [ ] Add `Traffic.Concurrency` and `WK_SIM_TRAFFIC_CONCURRENCY` env override with validation.
- [ ] Derive person/group traffic streams with the configured concurrency.
- [ ] Test defaults/env/build-input propagation in devsim config tests.
- [ ] Update Compose defaults to `500` person channels + `500` group channels, `1/s`, and a bounded concurrency default.
- [ ] Update README to document 1000-channel profile and concurrency override.
- [ ] Run: `go test ./internal/bench/devsim -count=1`.

### Task 4: Verification and Profiling

**Files:**
- No source files expected beyond Tasks 1-3.

- [ ] Run: `go test ./internal/bench/workload ./internal/bench/worker ./internal/bench/devsim -count=1`.
- [ ] Rebuild/recreate `wk-sim` using Docker Compose profile.
- [ ] Confirm `/status` sends close to the configured target rate over a short window.
- [ ] Capture fresh node CPU pprof samples and inspect top cumulative functions.
