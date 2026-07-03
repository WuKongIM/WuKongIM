# WuKongIM V2 Promotion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Promote the current v2 runtime so the product entrypoint `cmd/wukongim` starts `internalv2/app` and `pkg/clusterv2` by default, while keeping core package renames (`internalv2 -> internal`, `clusterv2 -> cluster`, `controllerv2 -> controller`) for the next migration stage.

**Architecture:** Move the v2 command, config loader, examples, local scripts, e2e harness build target, Prometheus job identity, and active docs onto canonical `wukongim` names. Delete the legacy v1 command entrypoint. Keep `internalv2`, `pkg/clusterv2`, `pkg/controllerv2`, and `pkg/channelv2` package paths unchanged in this stage so runtime behavior and package ownership stay reviewable.

**Tech Stack:** Go, `internalv2/app`, `pkg/clusterv2`, strict `WK_` env-file config parsing, Bash local scripts, Go script tests, e2ev2 black-box harness.

---

## Scope Guard

- This plan is Stage 1 of the v2 promotion. It does not rename `internalv2`, `pkg/clusterv2`, `pkg/controllerv2`, or `pkg/channelv2`.
- Do not introduce a runtime switch that can boot legacy `internal/app` from `cmd/wukongim`.
- Do not bulk-edit historical plan/spec/perf evidence files under `docs/superpowers/plans`, `docs/superpowers/specs`, or old benchmark result notes. Only update active operational docs and AGENTS files.
- Keep script compatibility as thin delegation only. Runtime code must not gain compatibility branches for the old v1 architecture.
- Preserve the project rule that a one-node deployment is a single-node cluster.

## Files To Change

- Move into `cmd/wukongim/`:
  - `cmd/wukongimv2/main.go`
  - `cmd/wukongimv2/config.go`
  - `cmd/wukongimv2/main_test.go`
  - `cmd/wukongimv2/config_test.go`
  - `cmd/wukongimv2/wukongimv2.conf.example`
  - `cmd/wukongimv2/wukongimv2-node1.conf.example`
  - `cmd/wukongimv2/wukongimv2-node2.conf.example`
  - `cmd/wukongimv2/wukongimv2-node3.conf.example`
  - `cmd/wukongimv2/wukongimv2-join-node.conf.example`
- Delete or replace the old v1 command files in `cmd/wukongim/`.
- Remove `cmd/wukongimv2/` after the move.
- Update root config:
  - `wukongim.conf.example`
- Move and rename runnable script configs:
  - `scripts/wukongimv2/` -> `scripts/wukongim/`
  - `scripts/wukongimv2/wukongimv2.conf` -> `scripts/wukongim/wukongim.conf`
  - `scripts/wukongimv2/wukongimv2-node1.conf` -> `scripts/wukongim/wukongim-node1.conf`
  - `scripts/wukongimv2/wukongimv2-node2.conf` -> `scripts/wukongim/wukongim-node2.conf`
  - `scripts/wukongimv2/wukongimv2-node3.conf` -> `scripts/wukongim/wukongim-node3.conf`
- Move canonical scripts and leave old-name wrappers:
  - `scripts/start-wukongimv2-single-node.sh` -> `scripts/start-wukongim-single-node.sh`
  - `scripts/start-wukongimv2-three-nodes.sh` -> `scripts/start-wukongim-three-nodes.sh`
  - `scripts/smoke-wkcli-sim-wukongimv2.sh` -> `scripts/smoke-wkcli-sim-wukongim.sh`
  - `scripts/smoke-wkcli-sim-wukongimv2-three-nodes.sh` -> `scripts/smoke-wkcli-sim-wukongim-three-nodes.sh`
  - `scripts/bench-wukongimv2-delivery.sh` -> `scripts/bench-wukongim-delivery.sh`
  - `scripts/bench-wukongimv2-single-node-1000ch.sh` -> `scripts/bench-wukongim-single-node-1000ch.sh`
  - `scripts/bench-wukongimv2-single-node-real-qps.sh` -> `scripts/bench-wukongim-single-node-real-qps.sh`
  - `scripts/bench-wukongimv2-three-nodes-1000ch.sh` -> `scripts/bench-wukongim-three-nodes-1000ch.sh`
  - `scripts/bench-wukongimv2-three-nodes-10kch.sh` -> `scripts/bench-wukongim-three-nodes-10kch.sh`
  - `scripts/bench-wukongimv2-three-nodes-presence.sh` -> `scripts/bench-wukongim-three-nodes-presence.sh`
  - `scripts/bench-wukongimv2-three-nodes-real-qps.sh` -> `scripts/bench-wukongim-three-nodes-real-qps.sh`
- Update script tests:
  - `scripts/wukongimv2_single_node_script_test.go`
  - `scripts/wukongimv2_three_nodes_script_test.go`
  - `scripts/wukongimv2_three_node_bench_script_test.go`
  - `scripts/wkcli_sim_smoke_script_test.go`
  - `scripts/wkcli_top_three_nodes_script_test.go`
- Update e2e harness and docs:
  - `test/e2ev2/suite/binary.go`
  - `test/e2ev2/suite/binary_test.go`
  - `test/e2ev2/suite/config.go`
  - `test/e2ev2/suite/config_test.go`
  - `test/e2ev2/suite/runtime.go`
  - `test/e2ev2/suite/runtime_test.go`
  - `test/e2ev2/**/AGENTS.md`
  - `scripts/e2ev2/dynamic-node-readiness-gate.sh`
- Update Prometheus/product identity:
  - `internalv2/app/prometheus.go`
  - `internalv2/app/prometheus_test.go`
  - `internalv2/app/manager_monitor_prometheus.go`
  - `internalv2/app/manager_monitor_prometheus_test.go`
  - `internalv2/app/manager_cluster_monitor_prometheus_test.go`
  - `internalv2/app/observability_test.go`
  - `internalv2/app/config.go`
  - `internalv2/app/prometheus_embedded/README.md`
- Update active docs:
  - `AGENTS.md`
  - `internalv2/FLOW.md`
  - `internalv2/app/FLOW.md`
  - `internalv2/access/manager/FLOW.md`
  - `cmd/wkbench/README.md`
  - `docs/development/PROJECT_KNOWLEDGE.md`
  - `docs/development/PERF_TRIAGE.md`

## Implementation Steps

- [ ] **Step 0: Confirm clean migration base**

  Run:

  ```bash
  git status --short
  git log --oneline -1
  ```

  Expected:

  - The latest commit is the approved v2 promotion design commit.
  - Any unrelated user changes are noted and preserved.

- [ ] **Step 1: Add the failing promoted-command expectation**

  Before moving implementation files, update the `cmd/wukongim` tests so they expect v2 behavior.

  Replace the old `cmd/wukongim/main_test.go` and `cmd/wukongim/config_test.go` contents with the current v2 tests from `cmd/wukongimv2`, then change expectations:

  - Package remains `main`.
  - Import boundary test package target changes from `./cmd/wukongimv2` to `./cmd/wukongim`.
  - Import boundary must require `github.com/WuKongIM/WuKongIM/internalv2/app`.
  - Import boundary must reject `github.com/WuKongIM/WuKongIM/internal/app`.
  - Error strings should say `cmd/wukongim production files`.
  - Example config file names change from `wukongimv2*.conf.example` to `wukongim*.conf.example`.
  - Script config paths change from `../../scripts/wukongimv2/wukongimv2.conf` to `../../scripts/wukongim/wukongim.conf`, and node config paths change to `../../scripts/wukongim/wukongim-nodeN.conf`.

  Run the focused red check:

  ```bash
  GOWORK=off go test ./cmd/wukongim -run 'TestRun|TestImportBoundary|TestLoadConfigExampleFile|TestLoadConfigMultiNodeExampleFiles' -count=1
  ```

  Expected red result:

  - `cmd/wukongim` still imports `internal/app` or has the old `run` signature.
  - Example config tests fail until files are moved/renamed in the next steps.

- [ ] **Step 2: Move v2 command implementation into `cmd/wukongim`**

  Use `git mv` for moves so history is preserved. Remove the old v1 command files before moving v2 files into their names:

  ```bash
  git rm cmd/wukongim/main.go cmd/wukongim/config.go cmd/wukongim/main_test.go cmd/wukongim/config_test.go
  git mv cmd/wukongimv2/main.go cmd/wukongim/main.go
  git mv cmd/wukongimv2/config.go cmd/wukongim/config.go
  git mv cmd/wukongimv2/main_test.go cmd/wukongim/main_test.go
  git mv cmd/wukongimv2/config_test.go cmd/wukongim/config_test.go
  git mv cmd/wukongimv2/wukongimv2.conf.example cmd/wukongim/wukongim.conf.example
  git mv cmd/wukongimv2/wukongimv2-node1.conf.example cmd/wukongim/wukongim-node1.conf.example
  git mv cmd/wukongimv2/wukongimv2-node2.conf.example cmd/wukongim/wukongim-node2.conf.example
  git mv cmd/wukongimv2/wukongimv2-node3.conf.example cmd/wukongim/wukongim-node3.conf.example
  git mv cmd/wukongimv2/wukongimv2-join-node.conf.example cmd/wukongim/wukongim-join-node.conf.example
  git rm cmd/wukongimv2/README.md
  ```

  Edit the moved files:

  - In `cmd/wukongim/config.go`, change comments like "wukongimv2 skeleton" to "wukongim".
  - Keep `defaultConfigPaths` as `./wukongim.conf`, `./conf/wukongim.conf`, `/etc/wukongim/wukongim.conf`.
  - Keep `loadConfig(args []string)` strict `WK_` parsing from v2.
  - In `cmd/wukongim/main.go`, keep `run(ctx, os.Args[1:], newInternalV2App)` so tests can inject fake apps.
  - Remove references to `internal/app` and `pkg/cluster`.

  Run:

  ```bash
  GOWORK=off go test ./cmd/wukongim -run 'TestRun|TestImportBoundary|TestLoadConfigDefaultValues|TestLoadConfigExplicitConfigFile' -count=1
  GOWORK=off go test ./cmd/wukongim -count=1
  ```

  Expected:

  - `cmd/wukongim` tests pass.
  - `go list -f '{{join .Imports "\n"}}' ./cmd/wukongim` contains `internalv2/app` and does not contain `internal/app`.

- [ ] **Step 3: Canonicalize config examples and runnable script configs**

  Move the runnable script config directory:

  ```bash
  git mv scripts/wukongimv2 scripts/wukongim
  git mv scripts/wukongim/wukongimv2.conf scripts/wukongim/wukongim.conf
  git mv scripts/wukongim/wukongimv2-node1.conf scripts/wukongim/wukongim-node1.conf
  git mv scripts/wukongim/wukongimv2-node2.conf scripts/wukongim/wukongim-node2.conf
  git mv scripts/wukongim/wukongimv2-node3.conf scripts/wukongim/wukongim-node3.conf
  ```

  Edit `cmd/wukongim/*.conf.example`, `scripts/wukongim/*.conf`, and `wukongim.conf.example`:

  - Replace `cmd/wukongimv2` with `cmd/wukongim`.
  - Replace `wukongimv2-single` with `wukongim-single`.
  - Replace `wukongimv2-dev-three` with `wukongim-dev-three`.
  - Replace data/log/prometheus paths like `wukongimv2-single-node-data`, `wukongimv2-node-1`, and `wukongimv2-three-nodes` with `wukongim-single-node-data`, `wukongim-node-1`, and `wukongim-three-nodes`.
  - Ensure `wukongim.conf.example` is generated from the promoted v2 example, not from the old v1 config surface.
  - Keep every key in `wukongim.conf.example` within `supportedConfigKeys` from `cmd/wukongim/config.go`.

  Add or update config tests in `cmd/wukongim/config_test.go`:

  - `TestLoadConfigExampleFile` loads `wukongim.conf.example`.
  - `TestLoadConfigMultiNodeExampleFiles` loads `wukongim-node1.conf.example`, `wukongim-node2.conf.example`, and `wukongim-node3.conf.example`.
  - A root example test loads `../../wukongim.conf.example`.
  - The supported-key test scans `../../wukongim.conf.example`, `wukongim*.conf.example`, and `../../scripts/wukongim/*.conf`.

  Run:

  ```bash
  GOWORK=off go test ./cmd/wukongim -run 'Test.*Config|TestLoadConfig.*Example|TestSupportedConfigKeys' -count=1
  rg -n 'WK_CLUSTER_GROUP_COUNT|WK_CLUSTER_GROUP_REPLICA_N|WK_CLUSTER_CHANNEL_EXECUTION|WK_CLUSTER_APPEND_GROUP_COMMIT|WK_CHANNEL_PLANE_' wukongim.conf.example cmd/wukongim scripts/wukongim
  ```

  Expected:

  - Config tests pass.
  - The `rg` command returns no unsupported legacy v1 config keys.

- [ ] **Step 4: Move startup, smoke, and benchmark scripts to canonical names**

  Move scripts with `git mv`, then create old-name wrappers that delegate to the new canonical scripts:

  ```bash
  git mv scripts/start-wukongimv2-single-node.sh scripts/start-wukongim-single-node.sh
  git mv scripts/start-wukongimv2-three-nodes.sh scripts/start-wukongim-three-nodes.sh
  git mv scripts/smoke-wkcli-sim-wukongimv2.sh scripts/smoke-wkcli-sim-wukongim.sh
  git mv scripts/smoke-wkcli-sim-wukongimv2-three-nodes.sh scripts/smoke-wkcli-sim-wukongim-three-nodes.sh
  git mv scripts/bench-wukongimv2-delivery.sh scripts/bench-wukongim-delivery.sh
  git mv scripts/bench-wukongimv2-single-node-1000ch.sh scripts/bench-wukongim-single-node-1000ch.sh
  git mv scripts/bench-wukongimv2-single-node-real-qps.sh scripts/bench-wukongim-single-node-real-qps.sh
  git mv scripts/bench-wukongimv2-three-nodes-1000ch.sh scripts/bench-wukongim-three-nodes-1000ch.sh
  git mv scripts/bench-wukongimv2-three-nodes-10kch.sh scripts/bench-wukongim-three-nodes-10kch.sh
  git mv scripts/bench-wukongimv2-three-nodes-presence.sh scripts/bench-wukongim-three-nodes-presence.sh
  git mv scripts/bench-wukongimv2-three-nodes-real-qps.sh scripts/bench-wukongim-three-nodes-real-qps.sh
  ```

  Canonical script edits:

  - Build command: `go build -o "$BIN_PATH" ./cmd/wukongim`.
  - Dry-run output includes `build_cmd=go build -o $BIN_PATH ./cmd/wukongim`.
  - Default binary names: `wukongim`.
  - Config paths: `scripts/wukongim/wukongim.conf`, `scripts/wukongim/wukongim-node1.conf`, etc.
  - Default data/log paths: `data/wukongim-*`.
  - Log prefixes: `[wukongim-single]`, `[wukongim-three]`.
  - Smoke script node command: `go run ./cmd/wukongim`.
  - Auto-join generated config names: `wukongim-node4.conf`.
  - Benchmark summaries refer to "local wukongim single-node cluster" or "local wukongim three-node cluster".
  - New env var prefixes:
    - `WK_WUKONGIM_SINGLE_NODE_*`
    - `WK_WUKONGIM_THREE_NODES_*`
  - Keep old `WK_WUKONGIMV2_*` as a temporary fallback only in the canonical scripts, so old wrappers can delegate without surprising operators.

  Old-name wrapper example for `scripts/start-wukongimv2-single-node.sh`:

  ```bash
  #!/usr/bin/env bash
  set -euo pipefail
  ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  printf '[deprecated] %s moved to scripts/start-wukongim-single-node.sh\n' "$(basename "$0")" >&2
  exec "$ROOT_DIR/scripts/start-wukongim-single-node.sh" "$@"
  ```

  Old-name wrapper example for `scripts/bench-wukongimv2-three-nodes-10kch.sh`:

  ```bash
  #!/usr/bin/env bash
  set -euo pipefail
  ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  printf '[deprecated] %s moved to scripts/bench-wukongim-three-nodes-10kch.sh\n' "$(basename "$0")" >&2
  exec "$ROOT_DIR/scripts/bench-wukongim-three-nodes-10kch.sh" "$@"
  ```

  Update script tests:

  - Rename test functions from `WukongIMV2` to `WukongIM`.
  - Update fake binary helpers to emit `wukongim.calls` and `wukongim.env`.
  - Expect build target `./cmd/wukongim`.
  - Expect canonical script/config/data/log names.
  - Add a small wrapper test that running an old `*-wukongimv2-*.sh --dry-run` delegates and prints the deprecation line.

  Run:

  ```bash
  GOWORK=off go test ./scripts -run 'TestWukongIM|TestWkcliSim|Test.*Bench|TestWkcliTop' -count=1
  bash scripts/start-wukongim-single-node.sh --dry-run
  bash scripts/start-wukongim-three-nodes.sh --dry-run
  bash scripts/smoke-wkcli-sim-wukongim.sh --dry-run
  ```

  Expected:

  - Script tests pass.
  - Dry-run output has no `cmd/wukongimv2` and no `scripts/wukongimv2`.

- [ ] **Step 5: Promote Prometheus and product telemetry identity**

  Update product-level observability names that identify the promoted binary:

  - In `internalv2/app/prometheus.go`, change generated Prometheus `JobName` from `wukongimv2` to `wukongim`.
  - In `internalv2/app/manager_monitor_prometheus.go`, change `managerMonitorPrometheusJobName` to `wukongim`.
  - In tests under `internalv2/app`, replace expected `job="wukongimv2"` and `job_name: wukongimv2` with `wukongim`.
  - In `internalv2/app/config.go`, change Prometheus comments from "wukongimv2 metrics" to "wukongim metrics".
  - Update `internalv2/app/observability_test.go` assertions for any product metric families that still use the `wukongimv2_` prefix. Keep `channelv2`, `controllerv2`, and `clusterv2` terms where they refer to current package/runtime names.
  - Update `internalv2/app/FLOW.md` and `internalv2/access/manager/FLOW.md` so they no longer describe `wukongimv2` as the Prometheus job.
  - Update `internalv2/app/prometheus_embedded/README.md` to mention `scripts/start-wukongim-single-node.sh` and `cmd/wukongim`.

  Run:

  ```bash
  GOWORK=off go test ./internalv2/app ./internalv2/access/manager -run 'TestPrometheus|TestManager.*Monitor|Test.*Observability' -count=1
  rg -n 'job="wukongimv2"|job_name: wukongimv2|managerMonitorPrometheusJobName\\s*=\\s*"wukongimv2"|wukongimv2_' internalv2/app internalv2/access/manager
  ```

  Expected:

  - Prometheus and manager monitor tests pass.
  - The `rg` command returns no product-level `wukongimv2` job or metric-family names in `internalv2/app` and `internalv2/access/manager`.

- [ ] **Step 6: Update e2ev2 harness to build the promoted binary**

  In `test/e2ev2/suite/binary.go`:

  - Rename the primary binary override env to `WK_E2E_BINARY`.
  - Keep `WK_E2EV2_BINARY` as a fallback for one transition.
  - Build `./cmd/wukongim`.
  - Cache the binary as `wukongim-e2e`.
  - Update build error text to `go build ./cmd/wukongim`.

  In `test/e2ev2/suite/config.go` and tests:

  - Change `staticThreeNodeClusterID` from `wukongimv2-e2ev2-three` to `wukongim-e2ev2-three`.
  - Change test temp paths from `/tmp/wukongimv2/node-1/data` to `/tmp/wukongim/node-1/data`, and from `/tmp/wukongimv2/node-1/logs` to `/tmp/wukongim/node-1/logs`.
  - Update comments in `runtime.go` from "wukongimv2 process" to "wukongim process".

  In `scripts/e2ev2/dynamic-node-readiness-gate.sh`:

  - Default gofail binary path: `$OUT_DIR/wukongim-gofail`.
  - Gofail build command: `--cmd ./cmd/wukongim`.
  - Test env: `WK_E2E_BINARY="$BINARY"`.
  - Keep `WK_E2EV2_BINARY` fallback only where the script intentionally supports old invocations.

  Update `test/e2ev2/**/AGENTS.md`:

  - Replace `cmd/wukongimv2` with `cmd/wukongim`.
  - Replace `WK_E2EV2_BINARY` with `WK_E2E_BINARY`, mentioning the old name only as a temporary fallback in the top-level harness doc.
  - Update gofail examples to `--cmd ./cmd/wukongim` and `/tmp/wukongim-gofail`.

  Run:

  ```bash
  GOWORK=off go test ./test/e2ev2/suite -run 'Test.*Binary|Test.*Config|Test.*Runtime' -count=1
  bash scripts/e2ev2/dynamic-node-readiness-gate.sh --dry-run --profile quick
  ```

  Expected:

  - Suite tests pass.
  - Dry-run commands build `./cmd/wukongim` and use `WK_E2E_BINARY`.

- [ ] **Step 7: Update active docs and project knowledge**

  Update `AGENTS.md` directory structure:

  - `cmd/wukongim/` becomes the official program entrypoint that reads config and starts `internalv2/app`.
  - Remove `cmd/wukongimv2/`.
  - `scripts/wukongim/` becomes the runnable local config directory.
  - `test/e2ev2/` should say it starts real `cmd/wukongim` processes while still covering internalv2 behavior.

  Update active docs:

  - `docs/development/PROJECT_KNOWLEDGE.md`
    - Replace the note saying new architecture traffic should enter through standalone `cmd/wukongimv2`.
    - New note: `cmd/wukongim` is the promoted v2 product entrypoint; v2 package paths remain until the package-rename stage.
    - Replace runnable config directory note with `scripts/wukongim/`.
  - `docs/development/PERF_TRIAGE.md`
    - Replace active commands with canonical script names.
    - Leave historical findings in `docs/development/WKSIM_STRESS_FINDINGS.md` unchanged.
  - `cmd/wkbench/README.md`
    - Replace recommended local run scripts with `bench-wukongim-*` canonical names.
  - `internalv2/FLOW.md`, `internalv2/app/FLOW.md`, and `internalv2/access/manager/FLOW.md`
    - Keep package names as internalv2.
    - Update entrypoint, Prometheus job, and delivery-default descriptions from `wukongimv2` executable to `cmd/wukongim`.

  Run:

  ```bash
  rg -n 'cmd/wukongimv2|go run ./cmd/wukongimv2|scripts/wukongimv2|start-wukongimv2|bench-wukongimv2|WK_WUKONGIMV2|WK_E2EV2_BINARY|wukongimv2-e2e|job="wukongimv2"|job_name: wukongimv2' \
    AGENTS.md cmd scripts test/e2ev2 internalv2 docs/development/PROJECT_KNOWLEDGE.md docs/development/PERF_TRIAGE.md cmd/wkbench/README.md
  ```

  Expected:

  - No active docs or code point users at `cmd/wukongimv2`.
  - Allowed residuals only:
    - Old wrapper script filenames and wrapper deprecation text.
    - `internalv2`, `pkg/clusterv2`, `pkg/controllerv2`, and `pkg/channelv2` package/runtime names.
    - Historical evidence docs intentionally excluded from the command.

- [ ] **Step 8: Final verification**

  Run focused unit and script verification:

  ```bash
  GOWORK=off go test ./cmd/wukongim ./internalv2/app ./internalv2/access/manager ./scripts ./test/e2ev2/suite -count=1
  GOWORK=off go test ./internalv2/access/api ./internalv2/access/gateway ./internalv2/infra/cluster ./internalv2/usecase/message -count=1
  git diff --check
  ```

  Run one real promoted-binary smoke if ports are available:

  ```bash
  GOWORK=off go test -tags=e2e ./test/e2ev2/message/single_node_send -count=1 -timeout 2m
  ```

  If the e2e smoke cannot run because ports are occupied or the machine is resource constrained, record the exact blocker and keep the unit/script verification output.

## Commit Checkpoints

- Commit 1: promoted `cmd/wukongim` command and config examples.
- Commit 2: canonical scripts, wrappers, and script tests.
- Commit 3: Prometheus/product identity and e2ev2 harness/docs.

Each commit must pass `git diff --check` and the focused tests listed in its step before committing.

## Self-Review Checklist

- [ ] `cmd/wukongim` imports `internalv2/app`, not `internal/app`.
- [ ] `cmd/wukongimv2` no longer exists as a Go command package.
- [ ] `wukongim.conf.example` contains only keys accepted by `cmd/wukongim/config.go`.
- [ ] Local scripts build or run `./cmd/wukongim`, not `./cmd/wukongimv2`.
- [ ] Prometheus generated job and manager monitor query job are `wukongim`.
- [ ] e2ev2 builds `./cmd/wukongim` by default.
- [ ] Active docs and AGENTS files point users to `cmd/wukongim`.
- [ ] Historical docs were not rewritten just to rename old evidence.
- [ ] All verification commands from Step 8 were run or explicitly blocked with evidence.
