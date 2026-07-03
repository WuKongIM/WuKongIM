# devsim_smoke AGENTS

This file is for agents working on `test/legacy/e2e/bench/devsim_smoke`.

## Purpose

Prove the `wkbench dev-sim` CLI can run against a real three-node cluster,
connect simulated users through public WKProto gateways, and emit non-zero
person/group simulator traffic while exposing the development status API.

## Cluster Shape

Three real cluster nodes started from the production `cmd/wukongim` binary. The
positive smoke enables `WK_BENCH_API_ENABLE=true` on all nodes because simulator
preparation must go through `/bench/v1/*`.

## External Steps

1. Start a real three-node cluster through `test/legacy/e2e/suite`.
2. Wait for all nodes to satisfy HTTP readiness and WKProto readiness.
3. Build and run the real `cmd/wkbench` CLI as `wkbench dev-sim` with a tiny
   generated config targeting all three nodes.
4. Poll the simulator `/status` endpoint until it reports `running`, eight
   connected users, and non-zero sent messages.
5. Stop `wkbench dev-sim` with `SIGTERM` and require a clean exit.

## Observable Outcome

The simulator reaches `running`, reports the configured person/group channel
shape, and records at least one sent message through its status API.

## Failure Diagnostics

- generated simulator YAML path in temp dirs
- simulator stdout/stderr log
- generated node config
- node stdout/stderr
- node-scoped logs under `logs/`

## Run

`GOWORK=off go test -tags=e2e,legacy_e2e ./test/legacy/e2e/bench/devsim_smoke -count=1`

## Maintenance Rules

- Keep the workload tiny and deterministic; do not add fixed sleeps.
- Do not use Manager APIs or direct store reads for benchmark setup.
- If assertions, steps, diagnostics, or the run command change, update this file
  in the same change.
