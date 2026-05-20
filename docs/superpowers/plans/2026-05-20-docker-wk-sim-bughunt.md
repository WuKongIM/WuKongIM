# Docker WK-Sim Bughunt Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans in this session because subagent delegation was not requested by the user. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Exercise the Docker Compose development cluster with `wk-sim`, find reproducible bugs or performance regressions, fix each confirmed code issue, and commit fixes incrementally.

**Architecture:** Treat the Compose stack as a black-box three-node cluster and drive simulator traffic through public bench APIs and WKProto gateways. For every confirmed issue, record evidence in the bughunt report, reproduce it with the narrowest automated test possible, apply the minimal fix, run focused verification, commit, and continue the next search pass.

**Tech Stack:** Go, Docker Compose, `cmd/wkbench dev-sim`, `scripts/dev-sim-compose-smoke.sh`, `test/e2e/bench/devsim_smoke`.

---

### Task 1: Baseline and Harness

**Files:**
- Create: `docs/superpowers/reports/2026-05-20-docker-wk-sim-bughunt.md`
- Read: `docker-compose.yml`
- Read: `docker/sim/dev-sim.yaml`
- Read: `scripts/dev-sim-compose-smoke.sh`

- [x] Set up an isolated worktree on branch `qa/wk-sim-bughunt-20260520`.
- [x] Run `GOWORK=off go test ./...` to establish the unit-test baseline.
- [x] Run `docker compose --profile dev-sim config --quiet`.
- [ ] Keep the bughunt report updated after every finding and verification run.

### Task 2: Compose Smoke Search Pass

**Files:**
- Use: `scripts/dev-sim-compose-smoke.sh`
- Use: `docker-compose.yml`
- Use: `docker/sim/dev-sim.yaml`

- [x] Run a small Compose dev-sim smoke with laptop-safe overrides.
- [x] Run the default Compose dev-sim smoke with extended timeout to characterize startup.
- [x] Reproduce the default smoke timeout with the script's default 90-second timeout.
- [ ] If the smoke fails, use systematic debugging before proposing any fix.

### Task 3: Stress and Performance Search Pass

**Files:**
- Use: `docker-compose.yml`
- Use: `cmd/wkbench`
- Use: `internal/bench/**`

- [ ] Raise simulator traffic in bounded steps and watch status counters, send errors, recv errors, and node logs.
- [ ] Compare intended ingress rate to observed `messages_sent` deltas.
- [ ] Record suspected bottlenecks only when backed by observed metrics or profiles.

### Task 4: Fix Loop

**Files:**
- Modify only files implicated by the confirmed root cause.
- Update package `FLOW.md` when changed behavior makes existing flow docs stale.

- [ ] For each confirmed bug, write a failing regression test first.
- [ ] Verify the test fails for the expected reason.
- [ ] Implement the smallest root-cause fix.
- [ ] Run focused tests and relevant Compose verification.
- [ ] Update the bughunt report with root cause, fix, and verification evidence.
- [ ] Commit only the files for that fix and continue the next search pass.

