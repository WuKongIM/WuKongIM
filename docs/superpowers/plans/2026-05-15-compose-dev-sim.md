# Compose Dev Simulator Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an optional Docker Compose `wk-sim` service that runs `wkbench dev-sim` to keep simulated users online and emit low-rate person/group messages for local debugging.

**Architecture:** Extend `cmd/wkbench` with a long-running `dev-sim` command backed by focused `internal/bench/devsim` components. The simulator derives a normal bench target/scenario from a compact dev config, waits for target readiness, runs prepare/connect, loops low-rate traffic windows, and exposes a small status HTTP API. `docker-compose.yml` starts it only under the `dev-sim` profile.

**Tech Stack:** Go, stdlib HTTP/signal/context, existing `internal/bench` config/planner/worker/workload/wkproto packages, Docker Compose profiles, YAML config, Go unit tests with existing `testify/require` style.

---

## Pre-Execution Notes

- Execute from repository root: `/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM`.
- Current unrelated dirty files may exist. Do not modify or commit unrelated `web/*` or monitor files.
- Read `AGENTS.md`, `internal/bench/FLOW.md`, and `cmd/wkbench/README.md` before editing.
- Use @superpowers:test-driven-development for each behavior change: write failing tests, verify failure, implement, verify pass.
- Keep `cmd/wkbench` thin. Most logic belongs in `internal/bench/devsim`.
- Preserve black-box boundaries: `internal/bench/*` must not import server internals.
- Use “单节点集群” terminology if documentation discusses single-node deployment semantics.

## File Structure

- Create: `internal/bench/devsim/config.go`
  - Dev simulator config structs, defaults, validation, environment overrides, and conversion helpers.
- Create: `internal/bench/devsim/config_test.go`
  - YAML/default/env override tests.
- Create: `internal/bench/devsim/status.go`
  - Thread-safe simulator status model and snapshot DTO.
- Create: `internal/bench/devsim/status_test.go`
  - Status transition and counter tests.
- Create: `internal/bench/devsim/server.go`
  - `GET /healthz` and `GET /status` HTTP server.
- Create: `internal/bench/devsim/server_test.go`
  - HTTP route tests.
- Create: `internal/bench/devsim/runner.go`
  - Supervisor loop, readiness wait, prepare/connect/run-window orchestration, graceful shutdown.
- Create: `internal/bench/devsim/runner_test.go`
  - Fake runner/target tests for readiness, retry, and shutdown behavior.
- Modify: `cmd/wkbench/main.go`
  - Register `dev-sim` command and delegate to `internal/bench/devsim`.
- Modify: `cmd/wkbench/main_test.go`
  - CLI parsing/error tests for `dev-sim`.
- Modify: `Dockerfile`
  - Build/copy `wkbench` into the dev image alongside `wukongim`, or add a multi-binary build step compatible with existing services.
- Modify: `docker-compose.yml`
  - Add `wk-sim` service under `profiles: ["dev-sim"]`.
- Create: `docker/sim/dev-sim.yaml`
  - Default simulator config targeting Compose service names.
- Create: `docker/sim/README.md`
  - Usage and tuning guide.
- Modify: `cmd/wkbench/README.md`
  - Document `dev-sim` and Compose workflow.
- Modify: `internal/bench/FLOW.md`
  - Add `devsim` package role and flow.

## Task 1: Add Dev-Sim Config

**Files:**
- Create: `internal/bench/devsim/config.go`
- Create: `internal/bench/devsim/config_test.go`

- [ ] **Step 1: Write failing config load/default test**

Create `internal/bench/devsim/config_test.go`:

```go
func TestLoadConfigAppliesDefaults(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "dev-sim.yaml")
    require.NoError(t, os.WriteFile(path, []byte(`version: wkbench/dev-sim/v1
status:
  listen: 127.0.0.1:19091
target:
  api_addrs: ["http://wk-node1:5001"]
  gateway_tcp_addrs: ["wk-node1:5100"]
`), 0o644))

    cfg, err := LoadConfig(path, nil)

    require.NoError(t, err)
    require.Equal(t, "wkbench/dev-sim/v1", cfg.Version)
    require.Equal(t, 20, cfg.Online.TotalUsers)
    require.Equal(t, 5, cfg.Profiles.PersonChannels)
    require.Equal(t, "sim-u", cfg.Identity.UIDPrefix)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOWORK=off go test ./internal/bench/devsim -run TestLoadConfigAppliesDefaults -count=1`

Expected: FAIL because package/functions do not exist.

- [ ] **Step 3: Implement minimal config loader**

Implement `Config`, `LoadConfig(path string, env map[string]string)`, defaults, validation, and YAML parsing by following existing `internal/bench/config` patterns. Include English comments for exported structs/fields.

- [ ] **Step 4: Add env override tests**

Add test for `WK_SIM_USERS`, `WK_SIM_PERSON_CHANNELS`, `WK_SIM_GROUP_CHANNELS`, `WK_SIM_GROUP_MEMBERS`, `WK_SIM_RATE`, and `WK_SIM_UID_PREFIX`.

- [ ] **Step 5: Run config tests**

Run: `GOWORK=off go test ./internal/bench/devsim -run 'TestLoadConfig|TestConfigEnv' -count=1`

Expected: PASS.

## Task 2: Add Status Model And HTTP Server

**Files:**
- Create: `internal/bench/devsim/status.go`
- Create: `internal/bench/devsim/status_test.go`
- Create: `internal/bench/devsim/server.go`
- Create: `internal/bench/devsim/server_test.go`

- [ ] **Step 1: Write failing status transition test**

Test that a new status starts as `starting`, can transition to `running`, increments message/error counters, and returns immutable snapshots.

- [ ] **Step 2: Run status test to verify failure**

Run: `GOWORK=off go test ./internal/bench/devsim -run TestStatus -count=1`

Expected: FAIL until status type exists.

- [ ] **Step 3: Implement status model**

Use a small mutex-protected struct with fields: `State`, `RunID`, `ConnectedUsers`, `PersonChannels`, `GroupChannels`, `MessagesSent`, `SendErrors`, `RecvErrors`, `LastError`, `LastTransitionAt`.

- [ ] **Step 4: Write failing HTTP server test**

Test `GET /healthz` returns 200 and `GET /status` returns the current snapshot JSON.

- [ ] **Step 5: Implement server**

Use stdlib `http.Server`; provide `Start(ctx)` and `Close(ctx)` helpers or a simple `Handler()` for testability.

- [ ] **Step 6: Run tests**

Run: `GOWORK=off go test ./internal/bench/devsim -run 'TestStatus|TestServer' -count=1`

Expected: PASS.

## Task 3: Derive Bench Scenario From Dev-Sim Config

**Files:**
- Modify: `internal/bench/devsim/config.go`
- Create/modify: `internal/bench/devsim/config_test.go`

- [ ] **Step 1: Write failing derivation test**

Test that a compact dev-sim config converts into `model.TargetConfig`, `model.WorkerSet`, and `model.ScenarioConfig` with one in-process worker, person/group profiles, low-rate traffic, and deterministic prefixes.

- [ ] **Step 2: Run test to verify failure**

Run: `GOWORK=off go test ./internal/bench/devsim -run TestConfigDerivesBenchInputs -count=1`

Expected: FAIL because derivation is missing.

- [ ] **Step 3: Implement derivation helpers**

Add methods such as `BenchTarget()`, `BenchScenario(runID string)`, and `WorkerConfig(listen string)` or one `BuildBenchInputs(runID string)` helper.

- [ ] **Step 4: Run derivation tests**

Run: `GOWORK=off go test ./internal/bench/devsim -run TestConfigDerivesBenchInputs -count=1`

Expected: PASS.

## Task 4: Add Supervisor Runner

**Files:**
- Create: `internal/bench/devsim/runner.go`
- Create: `internal/bench/devsim/runner_test.go`

- [ ] **Step 1: Define small interfaces and write failing readiness test**

Introduce test fakes for target readiness and traffic execution. Test that runner waits for readiness before calling prepare/connect/run.

- [ ] **Step 2: Run test to verify failure**

Run: `GOWORK=off go test ./internal/bench/devsim -run TestRunnerWaitsForReadiness -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement minimal runner loop**

Implement readiness polling, status updates, one prepare/connect step, and repeated run-window calls until context cancellation.

- [ ] **Step 4: Add retry test**

Test that a runtime target error updates `LastError`, backs off, and retries readiness/connect rather than exiting immediately.

- [ ] **Step 5: Add graceful shutdown test**

Test context cancellation stops the loop and marks status stopped.

- [ ] **Step 6: Run runner tests**

Run: `GOWORK=off go test ./internal/bench/devsim -run TestRunner -count=1`

Expected: PASS.

## Task 5: Wire Real Bench Components

**Files:**
- Modify: `internal/bench/devsim/runner.go`
- Add tests as needed in `internal/bench/devsim/runner_test.go`

- [ ] **Step 1: Inspect existing worker runner APIs**

Run: `rg -n "newDefaultWorkloadRunner|PhasePrepare|PhaseConnect|RunWindow|Worker" internal/bench/worker internal/bench/workload internal/bench/coordinator`

Expected: identify the smallest reusable public/internal functions needed.

- [ ] **Step 2: Add a thin adapter around existing worker runner**

Create an adapter that builds the plan, prepares data, connects clients, and executes warmup/run windows using existing `internal/bench` code. If existing functions are private and too coupled, extract small package-level helpers with tests rather than duplicating logic.

- [ ] **Step 3: Run bench package tests**

Run: `GOWORK=off go test ./internal/bench/... -count=1`

Expected: PASS.

## Task 6: Add `wkbench dev-sim` CLI

**Files:**
- Modify: `cmd/wkbench/main.go`
- Modify: `cmd/wkbench/main_test.go`

- [ ] **Step 1: Write failing CLI test**

Add a test that `wkbench dev-sim --config missing.yaml` returns config exit code and that `dev-sim --help` includes the command.

- [ ] **Step 2: Run CLI test to verify failure**

Run: `GOWORK=off go test ./cmd/wkbench -run TestDevSim -count=1`

Expected: FAIL.

- [ ] **Step 3: Implement command**

Register `dev-sim` with flags:

```text
--config string          required path to dev-sim yaml
--status-listen string   optional override for status.listen
```

Handle SIGINT/SIGTERM through context cancellation.

- [ ] **Step 4: Run CLI tests**

Run: `GOWORK=off go test ./cmd/wkbench -run TestDevSim -count=1`

Expected: PASS.

## Task 7: Build wkbench Into Docker Image

**Files:**
- Modify: `Dockerfile`

- [ ] **Step 1: Inspect current Dockerfile**

Run: `cat Dockerfile`

Expected: know current single-binary build path.

- [ ] **Step 2: Modify Dockerfile to build `wukongim` and `wkbench`**

Add a second build command:

```dockerfile
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/wukongim ./cmd/wukongim \
 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/wkbench ./cmd/wkbench
COPY --from=builder /out/wukongim /usr/local/bin/wukongim
COPY --from=builder /out/wkbench /usr/local/bin/wkbench
```

- [ ] **Step 3: Verify Docker build**

Run: `docker compose build wk-node1 wk-node2 wk-node3`

Expected: build succeeds and image contains `/usr/local/bin/wkbench`.

## Task 8: Add Compose Service And Default Config

**Files:**
- Modify: `docker-compose.yml`
- Create: `docker/sim/dev-sim.yaml`
- Create: `docker/sim/README.md`

- [ ] **Step 1: Add default simulator config**

Create `docker/sim/dev-sim.yaml` matching the design defaults and Compose service hostnames.

- [ ] **Step 2: Add `wk-sim` service under profile**

Add `profiles: ["dev-sim"]`, use image/build matching node services, mount config, mount `./docker/dev-sim`, expose `19091:19091`, and command `/usr/local/bin/wkbench dev-sim --config /etc/wkbench/dev-sim.yaml`.

- [ ] **Step 3: Add README**

Document:

```bash
docker compose --profile dev-sim up -d --build
curl http://127.0.0.1:19091/status
docker compose logs -f wk-sim
```

- [ ] **Step 4: Validate compose config**

Run: `docker compose config --profiles dev-sim >/tmp/wk-compose-dev-sim.yml`

Expected: command exits 0 and rendered config includes `wk-sim` only with the profile.

## Task 9: Update wkbench Docs And FLOW

**Files:**
- Modify: `cmd/wkbench/README.md`
- Modify: `internal/bench/FLOW.md`

- [ ] **Step 1: Update README**

Add a `dev-sim` command section with Compose usage, status endpoint, and tuning env vars.

- [ ] **Step 2: Update FLOW**

Add `devsim` to package roles and explain supervisor flow.

- [ ] **Step 3: Run docs diff check**

Run: `git diff --check -- cmd/wkbench/README.md internal/bench/FLOW.md docker/sim/README.md`

Expected: no whitespace errors.

## Task 10: End-To-End Local Verification

**Files:**
- No new source files unless fixing discovered bugs.

- [ ] **Step 1: Run Go tests**

Run: `GOWORK=off go test ./cmd/wkbench ./internal/bench/... -count=1`

Expected: PASS.

- [ ] **Step 2: Build and start cluster with simulator**

Run:

```bash
docker compose --profile dev-sim up -d --build wk-node1 wk-node2 wk-node3 wk-sim
```

Expected: containers start.

- [ ] **Step 3: Check simulator status**

Run:

```bash
for i in $(seq 1 60); do
  curl -fsS http://127.0.0.1:19091/status && break
  sleep 1
done
```

Expected: status reaches `running`, connected users > 0, errors do not continually increase.

- [ ] **Step 4: Confirm target traffic**

Run:

```bash
docker compose logs --since=2m wk-node1 wk-node2 wk-node3 | rg 'sim-u|sim-msg|committed message routed|delivery.diag'
```

Expected: simulator messages appear in normal node logs.

- [ ] **Step 5: Restart one node and verify recovery**

Run:

```bash
docker compose restart wk-node2
sleep 20
curl -fsS http://127.0.0.1:19091/status
```

Expected: simulator remains alive and returns to `running` after retry/reconnect.

- [ ] **Step 6: Final diff check**

Run:

```bash
git diff --check -- \
  cmd/wkbench/README.md \
  docker-compose.yml \
  docker/sim/dev-sim.yaml \
  docker/sim/README.md \
  Dockerfile \
  internal/bench/FLOW.md \
  internal/bench/devsim \
  cmd/wkbench/main.go \
  cmd/wkbench/main_test.go
```

Expected: no whitespace errors.

## Task 11: Commit Implementation

**Files:** all simulator-related files only.

- [ ] **Step 1: Review status**

Run: `git status --short`

Expected: only simulator-related files are staged for this commit; unrelated user changes stay unstaged/uncommitted.

- [ ] **Step 2: Commit**

Run:

```bash
git add \
  Dockerfile \
  docker-compose.yml \
  docker/sim \
  cmd/wkbench/README.md \
  cmd/wkbench/main.go \
  cmd/wkbench/main_test.go \
  internal/bench/FLOW.md \
  internal/bench/devsim

git commit -m "feat: add compose development simulator"
```

Expected: commit succeeds.
