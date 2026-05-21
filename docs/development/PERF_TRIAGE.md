# WuKongIM Performance Triage

This runbook is the project-local companion for `.codex/skills/wukongim-perf-triage/SKILL.md`. Use it when investigating `wk-sim`, `wkbench dev-sim`, or three-node Docker Compose performance, timeout, throughput, sendack, recv, delivery, Raft, or data-plane regressions.

## Core Rule

Collect evidence before tuning or changing code. Classify the failure, write one falsifiable hypothesis, then run one minimal experiment that changes exactly one variable.

## Required Flow

1. Define the scenario, stack mode, duration, and success criteria.
2. Establish a clean baseline before high-rate or accumulated-data runs.
3. Collect evidence: status timeline, Compose logs, split node logs, metrics, pprof, Docker stats, git revision, and Compose config.
4. Classify the issue before changing anything.
5. Form one falsifiable hypothesis with one expected observation.
6. Run one minimal experiment that changes exactly one variable.
7. Change code only after evidence points to a code defect and a regression test can be written.
8. Verify with the target scenario plus `smoke-default` and `sampled-correctness`.
9. Record concise findings in `docs/development/WKSIM_STRESS_FINDINGS.md`.

## Automatic Evidence Collection

Prefer the project script for first-pass evidence capture:

```bash
scripts/dev-sim-perf-triage.sh smoke-default --duration 120
scripts/dev-sim-perf-triage.sh sampled-correctness --duration 120 --no-build
scripts/dev-sim-perf-triage.sh group-fanout --duration 180 --profile-seconds 30
```

Useful options:

- `--clean`: stop Compose and remove `docker/dev-cluster` plus `docker/dev-sim` before running.
- `--no-build`: reuse the existing image.
- `--no-up`: collect from an already running stack.
- `--out-dir DIR`: override the evidence base directory.
- `--duration SECONDS`: control status and Docker stats sampling duration.
- `--profile-seconds SECONDS`: control each node CPU pprof duration.

The script writes evidence first, then exits non-zero if final `/status` is not healthy or reports send/recv errors.
The sampled `/status` timeline now includes both the configured steady-state online pool (`connected_users`) and the latest live online count (`active_users`), plus reconnect churn (`reconnected_users`) when the simulator repairs sessions.

Node logs are collected from `internal/log` output under `docker/dev-cluster/node*/logs`:

- `logs/app/`: copied from node `app.log`.
- `logs/error/`: copied from node `error.log`.
- `logs/debug/`: copied from node `debug.log` when present.
- `logs/warn/`: copied from node `warn.log`; if the file is missing on older runs, derived from `app.log` warn records.
- `logs/compose/`: raw `docker compose logs` output for nodes and `wk-sim`.
- `status.jsonl`: sampled `/status` timeline with `connected_users`, `active_users`, and `reconnected_users`.

When reviewing `status.jsonl`, look for `active_users` dipping below `connected_users` or `reconnected_users` increasing between samples; that usually indicates reconnect churn rather than a pure throughput issue.

## Scenario Matrix

| Scenario | Suggested Environment | Purpose |
| --- | --- | --- |
| `smoke-default` | Compose defaults | Prove the stack is healthy before stress runs. |
| `sampled-correctness` | Low users/channels, `WK_SIM_VERIFY_RECV=sampled` | Catch cross-node delivery and recv correctness issues. |
| `person-hotpath` | Person channels only | Isolate personal channel send, metadata refresh, sendack, and routing. |
| `group-fanout` | Group channels only | Isolate subscriber expansion, delivery tag, and cross-node fanout. |
| `mixed-highrate` | Person + group, higher rate/concurrency | Exercise contention between gateway, data-plane RPC, Raft/store, delivery, and GC. |
| accumulated-data | Do not clean `docker/dev-cluster` | Find history-sensitive storage, replay, and metadata refresh issues. |
| `custom` | Explicit `WK_SIM_*` overrides | Reproduce a user-provided workload exactly. |

Do not start with `mixed-highrate` when the path-specific failure is unknown. Run isolated person/group scenarios first.

## Suggested Scenario Presets

`scripts/dev-sim-perf-triage.sh` applies these presets and generates a unique `WK_SIM_UID_PREFIX` by default. Use direct `docker compose` commands only when you need manual control.

```bash
# Baseline health, Compose defaults.
docker compose --profile dev-sim up -d --build wk-sim

# Correctness-oriented sampled run for smaller laptops.
WK_SIM_USERS=40 \
WK_SIM_PERSON_CHANNELS=10 \
WK_SIM_GROUP_CHANNELS=3 \
WK_SIM_GROUP_MEMBERS=12 \
WK_SIM_RATE=0.5/s \
WK_SIM_TRAFFIC_CONCURRENCY=16 \
WK_SIM_VERIFY_RECV=sampled \
WK_SIM_UID_PREFIX=sampled-$(date +%s) \
  docker compose --profile dev-sim up -d --build wk-sim

# Person send-path isolation.
WK_SIM_USERS=500 \
WK_SIM_PERSON_CHANNELS=250 \
WK_SIM_GROUP_CHANNELS=0 \
WK_SIM_RATE=1/s \
WK_SIM_TRAFFIC_CONCURRENCY=256 \
WK_SIM_VERIFY_RECV=none \
WK_SIM_UID_PREFIX=person-$(date +%s) \
  docker compose --profile dev-sim up -d --build wk-sim

# Group fanout isolation.
WK_SIM_USERS=500 \
WK_SIM_PERSON_CHANNELS=0 \
WK_SIM_GROUP_CHANNELS=250 \
WK_SIM_GROUP_MEMBERS=10 \
WK_SIM_RATE=1/s \
WK_SIM_TRAFFIC_CONCURRENCY=256 \
WK_SIM_VERIFY_RECV=none \
WK_SIM_UID_PREFIX=group-$(date +%s) \
  docker compose --profile dev-sim up -d --build wk-sim
```

## Evidence Directory

Store every triage run under `docs/development/perf-runs/<timestamp>-<scenario>/`.

```text
docs/development/perf-runs/20260521-153000-group-fanout/
  summary.md
  env.txt
  git.txt
  compose-config.yml
  status.jsonl
  docker-stats.jsonl
  logs/
    compose/
      wk-node1.log
      wk-node2.log
      wk-node3.log
      wk-sim.log
    app/
      wk-node1.log
      wk-node2.log
      wk-node3.log
    error/
      wk-node1.log
      wk-node2.log
      wk-node3.log
      wk-sim.log
    warn/
      wk-node1.log
      wk-node2.log
      wk-node3.log
      wk-sim.log
    debug/
      wk-node1.log
      wk-node2.log
      wk-node3.log
  metrics/
    node1.prom
    node2.prom
    node3.prom
  pprof/
    node1-cpu.pb.gz
    node1-heap.pb.gz
    node1-goroutine.txt
```

Minimum evidence:

```bash
git rev-parse HEAD > git.txt
git status --short >> git.txt
docker compose config > compose-config.yml
curl -fsS http://127.0.0.1:19091/status >> status.jsonl
docker stats --no-stream >> docker-stats.jsonl
docker compose logs --no-color wk-node1 > logs/compose/wk-node1.log
docker compose logs --no-color wk-node2 > logs/compose/wk-node2.log
docker compose logs --no-color wk-node3 > logs/compose/wk-node3.log
docker compose logs --no-color wk-sim > logs/compose/wk-sim.log
cp docker/dev-cluster/node1/logs/app.log logs/app/wk-node1.log
cp docker/dev-cluster/node1/logs/error.log logs/error/wk-node1.log
cp docker/dev-cluster/node1/logs/warn.log logs/warn/wk-node1.log
cp docker/dev-cluster/node1/logs/debug.log logs/debug/wk-node1.log
curl -fsS http://127.0.0.1:15001/metrics > metrics/node1.prom
curl -fsS http://127.0.0.1:15002/metrics > metrics/node2.prom
curl -fsS http://127.0.0.1:15003/metrics > metrics/node3.prom
curl -fsS http://127.0.0.1:15001/debug/pprof/goroutine?debug=2 > pprof/node1-goroutine.txt
curl -fsS http://127.0.0.1:15002/debug/pprof/goroutine?debug=2 > pprof/node2-goroutine.txt
curl -fsS http://127.0.0.1:15003/debug/pprof/goroutine?debug=2 > pprof/node3-goroutine.txt
```

CPU profiles are useful when the run is actively reproducing the issue:

```bash
curl -fsS 'http://127.0.0.1:15001/debug/pprof/profile?seconds=30' > pprof/node1-cpu.pb.gz
curl -fsS 'http://127.0.0.1:15002/debug/pprof/profile?seconds=30' > pprof/node2-cpu.pb.gz
curl -fsS 'http://127.0.0.1:15003/debug/pprof/profile?seconds=30' > pprof/node3-cpu.pb.gz
curl -fsS http://127.0.0.1:15001/debug/pprof/heap > pprof/node1-heap.pb.gz
curl -fsS http://127.0.0.1:15002/debug/pprof/heap > pprof/node2-heap.pb.gz
curl -fsS http://127.0.0.1:15003/debug/pprof/heap > pprof/node3-heap.pb.gz
```

## Classification Guide

| Evidence | Likely Class | First Check |
| --- | --- | --- |
| `smoke-default` fails | baseline health failure | readiness, config, recent diff, Compose logs |
| early-window timeout with `channelmeta.bootstrap` | cold start or warmup gap | warmup duration and channel coverage |
| `send_errors > 0` | send/append/forwarding path | leader routing, channel runtime, Raft append/apply |
| `recv_errors > 0` with sendack success | delivery path | presence, delivery tag, cross-node fanout, recv matcher |
| clean passes, accumulated fails | data/history-sensitive issue | storage, replay, metadata refresh, scans |
| service idle, `wk-sim` busy | benchmark client bottleneck | simulator CPU, concurrency, RTT |
| `connected_users` is stable but `active_users` dips or spikes | online churn or reconnect flapping | reconnect path, gateway stability, session repair, target logs |
| all containers saturated | local Docker capacity | host CPU, concurrent tests, container limits |
| pprof hot in send/store/delivery | server hot path | targeted unit test or package benchmark |
| goroutines blocked on RPC/apply/fetch | distributed runtime lag | data-plane pending/inflight and Raft/fetch logs |

## Minimal Experiment Rules

Change one variable per run.

- Workload variables: `WK_SIM_RATE`, `WK_SIM_TRAFFIC_CONCURRENCY`, `WK_SIM_WARMUP`, `WK_SIM_VERIFY_RECV`, person/group channel counts, group members.
- Environment variables: clean vs accumulated data, build freshness, concurrent host load, metrics/diagnostics enabled.
- Service config: data-plane pool, gateway event loops, append batching, delivery ack batching, cache TTLs.

Prefer workload experiments before configuration experiments. Prefer configuration experiments before code changes.

## Fix Eligibility

Only change code when all are true:

- Evidence points to a specific subsystem and failure mode.
- Configuration/workload changes cannot explain the issue.
- A focused regression test can fail before the fix.
- The fix preserves cluster semantics; do not add local-only deployment branches.
- The target scenario plus `smoke-default` and `sampled-correctness` can verify the result.

## Report Template

```markdown
## Scenario
- workload:
- clean or accumulated:
- duration:
- success criteria:

## Evidence
- status:
- logs:
- metrics:
- pprof:
- docker stats:

## Classification
- category:
- confidence:
- reason:

## Hypothesis
- hypothesis:
- falsification test:

## Next Experiment
- one variable to change:
- expected result:
- stop condition:

## Fix Eligibility
- code change needed: yes/no
- reason:
- required regression test:
```
