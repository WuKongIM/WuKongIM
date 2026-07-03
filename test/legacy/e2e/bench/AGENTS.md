# bench AGENTS

This file is for agents working inside `test/legacy/e2e/bench`.

## Domain Purpose

This domain covers black-box wkbench CLI scenarios. Tests must exercise real
`cmd/wukongim` target processes, real wkbench coordinator/worker control paths,
the target bench API, and WKProto traffic.

## Scenarios

| Scenario | Purpose | Run |
| --- | --- | --- |
| `wkbench_smoke` | Prove wkbench can prepare tiny benchmark data through the target bench API, drive person and group WKProto traffic through one worker, write a successful report with non-zero sendack success, and fail preflight when the server bench API is disabled. | `GOWORK=off go test -tags=e2e,legacy_e2e ./test/legacy/e2e/bench/wkbench_smoke -count=1` |
| `devsim_smoke` | Prove `wkbench dev-sim` can run against a real three-node cluster, expose status, connect simulated users, and emit non-zero simulator traffic. | `GOWORK=off go test -tags=e2e,legacy_e2e ./test/legacy/e2e/bench/devsim_smoke -count=1` |

## Maintenance Rules

- Keep wkbench e2e scenarios under `test/legacy/e2e/bench/<scenario>/`.
- Give each scenario its own `AGENTS.md` and one primary `<scenario>_test.go`.
- Do not import `internal/app`, inspect stores, or use Manager APIs for bench
  setup; benchmark data must go through `/bench/v1/*` and traffic must go
  through WKProto.
- Use server bench mode explicitly with `WK_BENCH_API_ENABLE=true` for positive
  scenarios, and cover disabled-mode preflight when relevant.
- If a scenario is added, removed, renamed, or its run command, steps, or
  diagnostics change, update this file and the scenario's `AGENTS.md` in the
  same change.
