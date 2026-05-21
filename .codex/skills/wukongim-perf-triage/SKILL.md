---
name: wukongim-perf-triage
description: Use when investigating WuKongIM wk-sim, wkbench dev-sim, Docker Compose three-node cluster performance, throughput, timeout, sendack, recv, fanout, Raft, delivery, data-plane, or distributed benchmark regressions.
---

# WuKongIM Performance Triage

## Core Rule

Use evidence-first triage for WuKongIM three-node Docker Compose and `wk-sim` performance work. Do not tune configuration or change code before collecting evidence, classifying the failure, and proving one falsifiable hypothesis.

Before running a benchmark, read `docs/development/PERF_TRIAGE.md` for current project commands, scenario presets, and evidence layout.

## Required Flow

1. Define scenario, clean-vs-accumulated mode, duration, and success criteria.
2. Establish a healthy `smoke-default` baseline before high-rate runs.
3. Collect status timeline, Compose logs, split node logs, metrics, pprof, Docker stats, git revision, and Compose config.
4. Classify the issue before changing workload, config, or code.
5. Write one falsifiable hypothesis and one expected observation.
6. Run one minimal experiment that changes exactly one variable.
7. Change code only after evidence points to a code defect and a regression test can fail before the fix.
8. Verify with the target scenario plus `smoke-default` and `sampled-correctness`.
9. Record concise findings in `docs/development/WKSIM_STRESS_FINDINGS.md`.

## Hard Stops

- If `smoke-default` fails, stop high-rate testing and diagnose baseline health first.
- If `send_errors > 0`, `recv_errors > 0`, or `last_error` is non-empty, classify the failure before optimization.
- Do not compare clean-stack and accumulated-stack runs as the same evidence class.
- Do not blame the server when `wk-sim` is CPU-bound or concurrency-limited.
- Do not change more than one workload/config/code variable per experiment.
- Do not add local-only deployment branches; single-node means single-node cluster.

## Isolation Guide

Use isolated scenarios before mixed traffic:

| Scenario | Use For |
| --- | --- |
| `smoke-default` | baseline startup, readiness, and default workload health |
| `sampled-correctness` | recv correctness and cross-node delivery |
| `person-hotpath` | personal channel send, metadata refresh, sendack, routing |
| `group-fanout` | subscriber expansion, delivery tag, cross-node fanout |
| `mixed-highrate` | final contention across gateway, data-plane, Raft/store, delivery, GC |
| accumulated data | storage growth, replay, metadata refresh, history-sensitive issues |

## Classification Hints

- `send_errors`: send/append/forwarding path; inspect leader routing, channel runtime, Raft append/apply.
- `recv_errors` after sendack success: delivery path; inspect presence, delivery tag, fanout, recv matching.
- Early-window timeouts with `channelmeta.bootstrap`: cold start or warmup gap.
- Clean passes but accumulated fails: storage, replay, metadata refresh, or scan issue.
- Service idle while `wk-sim` is busy: benchmark client bottleneck.
- `connected_users` stable but `active_users` flaps: online churn or reconnect instability; inspect gateway/session repair and target logs before tuning throughput.
- All containers saturated: local Docker capacity boundary.

## Required Report Shape

```markdown
## Scenario
- workload:
- clean or accumulated:
- duration:
- success criteria:

## Evidence
- status:
- compose logs:
- app logs:
- error logs:
- warn logs:
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
