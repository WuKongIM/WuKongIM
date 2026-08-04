# Chat Lifecycle Soak Phase 5: CLI and Native Operations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose the soak and aged-capacity modes through stable wkbench commands and provide reproducible native-host configuration, launch, monitoring, and shutdown procedures with no Docker dependency.

**Architecture:** Thin CLI adapters load validated chat-lifecycle configuration and call the Phase 4 coordinator. Checked-in examples contain addresses and public thresholds but no secrets. A native local script builds/starts three independent service processes, three worker processes, and one coordinator process for shakeout; the formal runbook maps the same roles to seven hosts.

**Tech Stack:** Go `flag` command routing and existing wkbench config loaders, YAML/TOML examples, Bash native-process orchestration, process signals, bearer tokens supplied by protected environment or files.

---

## Task 1: Add soak and capacity CLI commands

**Files:**

- Create: `cmd/wkbench/soak_command.go`
- Create: `cmd/wkbench/chat_lifecycle_command.go`
- Modify: `cmd/wkbench/capacity_command.go`
- Modify: `cmd/wkbench/cli.go`
- Modify: `cmd/wkbench/main_test.go`
- Modify: `cmd/wkbench/README.md`

- [ ] **Step 1: Write help and parsing tests**

Prove these commands parse without affecting existing command output:

```text
wkbench soak chat-lifecycle --config <file> --output-dir <dir>
wkbench capacity chat-lifecycle --config <file> --checkpoint <72h.json> --output-dir <dir>
wkbench worker --mode chat-lifecycle --listen <private-address>
```

Require explicit config and output paths. Capacity requires a readable completed 72-hour checkpoint. Unknown chat-lifecycle flags must fail before any network call.

Run:

```bash
GOWORK=off go test ./cmd/wkbench -run 'Test.*ChatLifecycle|Test.*Soak' -count=1
```

Expected: FAIL because the commands do not exist.

- [ ] **Step 2: Add cancellation and exit-code tests**

SIGINT/SIGTERM must request coordinated stop, wait for bounded final snapshot/report output, and exit with a distinct non-zero code for product, harness, or infrastructure failure. A completed pass exits zero. A second signal exits promptly without corrupting the last atomic checkpoint.

- [ ] **Step 3: Implement thin adapters**

Parse/load/validate, construct target and worker clients, call coordinator, render the concise terminal summary, and map the closed outcome to an exit code. Keep scheduling and verdict rules out of `cmd/wkbench`.

- [ ] **Step 4: Update CLI documentation**

Document the fixed-pressure and capacity commands, checkpoint continuity, exit classifications, output files, worker mode, and the fact that existing `run`, `dev-sim`, and capacity subcommands are unchanged.

- [ ] **Step 5: Run all wkbench command tests**

```bash
GOWORK=off go test ./cmd/wkbench -count=1
```

Expected: PASS.

## Task 2: Add checked-in configuration examples

**Files:**

- Create: `configs/wkbench/chat-lifecycle/formal.yaml`
- Create: `configs/wkbench/chat-lifecycle/local-shakeout.yaml`
- Create: `configs/wkbench/chat-lifecycle/README.md`
- Create: `internal/bench/chatlifecycle/config_examples_test.go`

- [ ] **Step 1: Write example-loading tests**

Load both checked-in files through the real config loader. Assert the formal file has:

```text
3 service nodes, 3 workers, 3 host-metrics endpoints, 1 coordinator identity
12 logical Slot groups, 256 hash slots, replicas 3/3
10,000 online, 250,000 new UIDs/day, 2,000 SEND/s
2h warmup/shakeout thresholds, 24h checkpoint, 72h final
1TB minimum data filesystem per node, 5% safe-stop threshold
private/authenticated bench, metrics, debug, and pprof observation
```

No checked-in value may resemble a real bearer token.

- [ ] **Step 2: Write config-redaction tests**

Load a token from the supported environment/file source and assert `%v`, JSON report, validation errors, and terminal summaries replace it with `[REDACTED]`.

- [ ] **Step 3: Add formal and local profiles**

The formal example uses seven distinct placeholder hosts. The local profile uses loopback with unique ports and shorter scale but preserves 12 logical Slot groups, 256 hash slots, replicas 3/3, real sync, TCP, and natural eviction. It must be labeled non-formal evidence.

- [ ] **Step 4: Run config example tests**

```bash
GOWORK=off go test ./internal/bench/chatlifecycle -run 'ConfigExample|ConfigRedaction' -count=1
```

Expected: PASS.

## Task 3: Add a native three-node shakeout runner

**Files:**

- Create: `scripts/run-wukongim-three-node-chat-lifecycle-shakeout.sh`
- Create: `scripts/wukongim_three_node_chat_lifecycle_script_test.go`
- Create: `scripts/wukongim_three_node_chat_lifecycle_script_integration_test.go`

- [ ] **Step 1: Write the static script contract test**

Without starting processes, assert the script:

- has `--help` and `--dry-run`;
- builds one `wukongim` and one `wkbench` binary;
- creates an explicit temporary run directory;
- launches three service processes with separate config/data/log/port paths;
- launches three chat-lifecycle workers and one coordinator;
- sets `initial_slot_count=12`, 256 hash slots, and replicas 3/3;
- records PIDs and uses TERM plus bounded wait for cleanup;
- contains no `docker`, `docker compose`, or Compose file reference.

Run:

```bash
GOWORK=off go test ./scripts -run 'TestChatLifecycleShakeoutScript' -count=1
```

Expected: FAIL because the script does not exist.

- [ ] **Step 2: Implement help and dry-run first**

Dry-run must print resolved explicit commands/paths with tokens redacted and create no background process. Reject a run directory equal to the repository root, home directory, or `/`.

- [ ] **Step 3: Implement native launch/readiness/cleanup**

Reuse the readiness behavior from `scripts/start-wukongim-three-nodes.sh` without invoking it as an opaque background owner. Build once, write per-node configs under the run directory, wait for cluster readiness, start workers, then start the coordinator. On any early exit, signal only recorded PIDs and preserve logs/reports.

- [ ] **Step 4: Write the integration-tag process test**

The integration test builds binaries, launches a reduced native profile on allocated loopback ports, proves all three service nodes and workers become ready, sends a bounded real workload through the coordinator, requests graceful stop, and asserts every PID exits. Put the build tag at the top and keep real waits/listeners out of the default test tier.

- [ ] **Step 5: Run script checks**

```bash
bash -n scripts/run-wukongim-three-node-chat-lifecycle-shakeout.sh
GOWORK=off go test ./scripts -run 'TestChatLifecycleShakeoutScript' -count=1
GOWORK=off go test -tags=integration ./scripts -run 'TestChatLifecycleShakeoutScriptIntegration' -count=1 -timeout=9m -parallel=1
```

Expected: all PASS; process integration leaves no recorded child alive.

## Task 4: Write the formal native-host runbook

**Files:**

- Create: `docs/superpowers/runbooks/2026-08-04-chat-lifecycle-soak.md`
- Modify: `configs/wkbench/chat-lifecycle/README.md`

- [ ] **Step 1: Document provisioning and security**

Specify seven independent hosts for formal evidence. Each of the three service hosts needs a dedicated data filesystem of at least 1,000,000,000,000 bytes; the coordinator safe-stops at 5% free. Explain that 1 TB is the initial allocation and operators expand it only for a later run. Bind bench, metrics, debug, pprof, worker control, and node_exporter to private interfaces and require bearer authentication where supported.

- [ ] **Step 2: Document preflight and balanced ingress**

Give exact commands to validate configuration, health/readiness, all 12 Slot leaders and three replicas, 256 hash slots, disk selection, independent API load balancing, independent gateway load balancing, and worker reachability before starting the clock.

- [ ] **Step 3: Document the run sequence**

Cover:

```text
local native shakeout -> formal 2h shakeout -> fresh continuous 24h/72h soak
-> optional aged 72h capacity staircase -> 2,000 SEND/s recovery
```

State that the 24-hour checkpoint does not restart or pause the run and that fault injection is a separate scenario.

- [ ] **Step 4: Document monitoring and safe stop**

List five-second cluster checks, worker/harness utilization, disk, forced-GC/resource samples, report locations, token redaction, expected exit classifications, and the no-resume/no-auto-restart rule. Include how to collect pprof only after a bounded anomaly trigger.

- [ ] **Step 5: Verify every command in dry-run/help mode**

```bash
go run ./cmd/wkbench soak chat-lifecycle --help
go run ./cmd/wkbench capacity chat-lifecycle --help
go run ./cmd/wkbench worker --help
bash scripts/run-wukongim-three-node-chat-lifecycle-shakeout.sh --dry-run
```

Expected: all exit zero without starting a formal run or printing a secret.

## Task 5: Phase verification and commit

- [ ] **Step 1: Run directly affected unit tests**

```bash
GOWORK=off go test ./internal/bench/chatlifecycle ./cmd/wkbench ./scripts -count=1
bash -n scripts/run-wukongim-three-node-chat-lifecycle-shakeout.sh
```

Expected: PASS.

- [ ] **Step 2: Prove the new path has no Docker dependency**

```bash
rg -n -i 'docker|compose' cmd/wkbench internal/bench/chatlifecycle configs/wkbench/chat-lifecycle scripts/run-wukongim-three-node-chat-lifecycle-shakeout.sh docs/superpowers/runbooks/2026-08-04-chat-lifecycle-soak.md
```

Expected: only explanatory statements saying Docker/Compose is prohibited; no command, image, network, or Compose dependency.

- [ ] **Step 3: Commit**

```bash
git add cmd/wkbench internal/bench/chatlifecycle configs/wkbench/chat-lifecycle scripts/run-wukongim-three-node-chat-lifecycle-shakeout.sh scripts/wukongim_three_node_chat_lifecycle_script_test.go scripts/wukongim_three_node_chat_lifecycle_script_integration_test.go docs/superpowers/runbooks/2026-08-04-chat-lifecycle-soak.md
git commit -m "feat(wkbench): add chat lifecycle commands and native runbook"
```

- [ ] **Step 4: Inspect status**

```bash
git diff --check
git status --short
```

Expected: no whitespace errors and no unrelated files staged.
