# wkbench_smoke AGENTS

This file is for agents working on `test/legacy/e2e/bench/wkbench_smoke`.

## Purpose

Prove the wkbench black-box CLI can run a tiny workload against a real
single-node cluster, and prove preflight rejects a target whose config requests
bench API usage while the server has bench mode disabled.

## Cluster Shape

One single-node cluster started from the real `cmd/wukongim` binary. The positive
smoke enables `WK_BENCH_API_ENABLE=true`; the negative preflight leaves it
disabled.

## External Steps

1. Start one real single-node cluster through `test/legacy/e2e/suite`.
2. Start one in-process wkbench worker control server on an ephemeral loopback
   address.
3. Run the real `cmd/wkbench` CLI binary with target, worker, and scenario YAML.
4. For the positive run, prepare two person pairs and one group with three
   members through the target bench API, connect clients through WKProto, and
   send the tiny person/group workload.
5. For the disabled-mode run, execute `wkbench doctor` and require preflight to
   fail before assignment or traffic.

## Observable Outcome

The positive run exits successfully, writes standard report artifacts, reports
`passed`, has non-zero person and group send success counters, and records at
least one receive/verification success counter. The disabled-mode run exits with
wkbench preflight code `2` and mentions bench API capabilities.

## Failure Diagnostics

- wkbench stdout/stderr captured by the test
- generated target, worker, and scenario YAML paths in temp dirs
- generated node config
- node stdout/stderr
- node-scoped logs under `logs/`
- wkbench report artifacts under the scenario `report_dir`

## Run

`GOWORK=off go test -tags=e2e,legacy_e2e ./test/legacy/e2e/bench/wkbench_smoke -count=1`

## Maintenance Rules

- Keep the workload tiny and deterministic; do not add fixed sleeps.
- Do not use Manager APIs or direct store reads for benchmark setup.
- If assertions, steps, diagnostics, or the run command change, update this file
  in the same change.
