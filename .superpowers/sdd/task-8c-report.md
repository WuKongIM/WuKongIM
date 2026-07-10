# Task 8c Report: Channel data-plane lease renewal budget

## Result

Implemented the TTL-backed health-report timeout budget from
`.superpowers/sdd/task-8c-brief.md`. The lease TTL remains the fail-closed
boundary; only the bounded Controller report timeout changed.

Implementation commit: `d272e2c5c963a92209eee429e3dddfeca08fe0d5`
(`fix: budget channel lease health reports`)

## Files

- `pkg/cluster/node_loops.go`
  - reserves one scheduling interval and divides the remaining positive TTL
    across three renewal attempts;
  - clamps the result to the existing minimum and a positive TTL.
- `pkg/cluster/node_health_report_test.go`
  - adds the delayed Controller write regression test;
  - adds the required timeout-budget table, including defensive inputs.
- `pkg/cluster/FLOW.md`
  - documents the TTL-backed report budget and unchanged fail-closed lease
    semantics.

No Router, configuration, gateway/client timeout, e2e assertion, or lease
freshness code was changed.

## TDD Evidence

### RED

Command:

```bash
GOWORK=off go test ./pkg/cluster \
  -run 'Test(ReportNodeHealthAllowsLatencyBeyondIntervalWithinLeaseBudget|HealthReportTimeoutUsesTTLBackedRenewalBudget)$' \
  -count=1
```

Expected failures observed before production changes:

```text
TestReportNodeHealthAllowsLatencyBeyondIntervalWithinLeaseBudget:
  reportNodeHealth() error = context deadline exceeded, want nil
TestHealthReportTimeoutUsesTTLBackedRenewalBudget/dynamic_lifecycle:
  got 500ms, want 9.833333333s
TestHealthReportTimeoutUsesTTLBackedRenewalBudget/defaults:
  got 5s, want 8.333333333s
```

### GREEN

Focused health-report tests:

```bash
GOWORK=off go test ./pkg/cluster \
  -run 'Test(ReportNodeHealth|HealthReport|StopHealthReport)' -count=1
```

```text
ok  github.com/WuKongIM/WuKongIM/pkg/cluster  0.725s
```

Full package:

```bash
GOWORK=off go test ./pkg/cluster -count=1
```

```text
ok  github.com/WuKongIM/WuKongIM/pkg/cluster  81.400s
```

Formatting and whitespace:

```bash
gofmt -w pkg/cluster/node_loops.go pkg/cluster/node_health_report_test.go
git diff --check
```

Both completed successfully.

## Real Dynamic Scenarios

Built one fresh product binary:

```bash
E2E_BINARY="$(mktemp "${TMPDIR:-/tmp}/wukongim-e2e.XXXXXX")"
GOWORK=off go build -o "$E2E_BINARY" ./cmd/wukongim
```

Then ran the scenarios sequentially with that binary:

```bash
WK_E2E_BINARY="$E2E_BINARY" GOWORK=off \
  go test -tags=e2e ./test/e2e/cluster/dynamic_node_readiness \
  -count=1 -timeout=8m -p=1
```

```text
ok  github.com/WuKongIM/WuKongIM/test/e2e/cluster/dynamic_node_readiness  40.167s
```

```bash
WK_E2E_BINARY="$E2E_BINARY" GOWORK=off \
  go test -tags=e2e ./test/e2e/cluster/dynamic_node_operations \
  -count=1 -timeout=8m -p=1
```

```text
ok  github.com/WuKongIM/WuKongIM/test/e2e/cluster/dynamic_node_operations  35.108s
```

The temporary binary was removed after both runs.

## Scope Notes And Concerns

- The brief starting point was `f8b031514`. During this task, the root agent
  committed independent Task 8d e2e-suite work as `8fbd96d97`; it did not
  overlap the three Task 8c files.
- Router deadline retry amplification remains intentionally out of scope, as
  required by the brief.
- No blocking Task 8c concern remains after the focused, package, and two real
  scenario passes.
