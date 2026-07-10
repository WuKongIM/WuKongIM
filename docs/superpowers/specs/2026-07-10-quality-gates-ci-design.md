# WuKongIM Quality Gates and CI Design

**Date:** 2026-07-10

## Status

Approved for implementation planning.

## Problem

WuKongIM has broad unit, integration, e2e, benchmark, and web coverage, but no
checked-in CI workflow. Its local release gates are not currently green:

- `go test ./... -count=1` fails the Grafana exported-metric coverage test
  and can expose a timing-sensitive cold-channel test failure.
- `go vet ./...` reports a copied `atomic.Uint64` in `wkcli sim`.
- `go mod tidy -diff` is non-empty.
- recursive Go package discovery includes ignored Go sources below `tmp/` and
  `web/node_modules/`, making the package set machine-dependent.
- the web project uses `bun.lock` and Bun commands but declares Yarn.
- the existing ESLint rules currently report 41 errors and 6 warnings, so
  enabling a strict zero-finding lint command would turn this bounded CI task
  into an unrelated multi-page React behavior refactor.

## Goals

1. Restore deterministic local Go and web quality gates.
2. Add fast, fail-closed GitHub Actions checks for pull requests and pushes to
   `main`.
3. Run race, integration, e2e, and three-node smoke checks separately on a
   schedule and by manual dispatch.
4. Keep Go package discovery independent of ignored directories.
5. Retain safe diagnostics for expensive failures.

## Non-goals

- Fixing cold-channel first-write retry behavior.
- Optimizing three-node Slot/Raft/storage latency.
- Splitting `internal/app` or changing shutdown semantics.
- Changing GitHub branch protection in the repository commit.
- Running performance benchmarks on every pull request.
- Clearing all existing web lint findings. Historical findings are frozen by
  an exact baseline and are removed in a separate cleanup.

## Considered Approaches

### One all-inclusive pull-request workflow

This maximizes immediate coverage but makes routine feedback depend on
long-running race, e2e, and three-node tests.

### Two-level matrix gates

This is the selected approach. Pull requests receive fast parallel Go and web
feedback. Scheduled and manual workflows exercise expensive concurrency and
real-runtime paths.

### Path-filtered checks

This is fastest, but WuKongIM has cross-layer contracts and composition.
Path-only selection can miss regressions in consumers whose files did not
change, so it is not used initially.

## Pull-request Workflow

Create `.github/workflows/ci.yml` for pull requests, pushes to `main`, and
manual dispatch. A concurrency group cancels obsolete runs for the same branch
or pull request.

### Go quality

Use Go `1.25.11`, matching the `toolchain` directive in `go.mod`, cache by
`go.sum`, and run read-only checks:

```text
tracked Go formatting check
go mod tidy -diff
GOWORK=off go vet ./cmd/... ./internal/... ./pkg/... ./scripts/... ./docker/...
```

Formatting and tidy checks report differences without rewriting the checkout.
Vet must use explicit roots and must not recurse into `tmp`,
`web/node_modules`, or e2e packages.

### Go unit matrix

Run independent fail-closed jobs for:

```text
./cmd/...
./internal/...
./pkg/...
./scripts/... ./docker/...
```

Each group uses `GOWORK=off`, `-count=1`, and a 15-minute job timeout.
Matrix jobs continue independently so one failure does not hide other results.
The Go quality job has a 10-minute timeout. Grafana dashboard coverage belongs
to the `scripts+docker` group.

### Web

Use Bun `1.3.11` and `web/bun.lock`; the web job has a 10-minute timeout:

```text
bun install --frozen-lockfile
bun run lint
bun run test
bunx tsc -b
bun run build
```

`web/package.json` must declare Bun consistently and expose baseline-aware
`lint` and `lint:update-baseline` scripts. Existing ESLint rules remain
enabled.

## Scheduled and Manual Workflow

Create `.github/workflows/nightly.yml` with a daily `18:00 UTC` schedule
(`02:00 Asia/Shanghai`) and `workflow_dispatch`. Expensive checks are
independent jobs:

### Race matrix

Split concurrency-critical roots into:

- `internal/app` and `internal/runtime/...`;
- `pkg/gateway/...` and `pkg/transport/...`;
- `pkg/channel/...`, `pkg/cluster/...`, and `pkg/slot/...`.

Each race group has a 45-minute timeout. Changing the package split or removing
coverage requires an explicit design amendment.

### Integration

Run explicit `internal` and `pkg` roots with `-tags=integration` and a
30-minute timeout. Do not use repository-root `./...`.

### E2E

Run canonical `test/e2e/...` packages with `-tags=e2e`, `GOWORK=off`,
bounded package parallelism, and a workflow timeout. Tests use the existing
real `cmd/wukongim` subprocess harness. The job timeout is 60 minutes.

### Three-node smoke

Run `scripts/smoke-wkcli-sim-wukongim-three-nodes.sh`. On failure, upload its
compact summary and node/application logs. Do not upload databases,
credentials, raw configuration, or environment dumps. The job timeout is
30 minutes and failure artifacts are retained for 7 days.

Nightly failures fail their job and workflow but do not retroactively block an
already merged pull request.

## Existing Gate Repairs

### Grafana metrics

Add real panels for every newly exported message-event metric:

- append and proposal rate/result;
- append/proposal and stage latency;
- proposal batch size;
- stream-cache sessions, lanes, payload bytes, and configured capacity.

Panels use low-cardinality aggregations and actionable titles. Hidden or dummy
references added only to satisfy coverage are not acceptable.

### `wkcli sim` atomic copy

Iterate group slices by index or pointer when building target requests so a
`Group` containing `atomic.Uint64` is never copied. Payload semantics stay
unchanged. `go vet` is the regression gate.

### Module metadata

Apply the exact `go mod tidy -diff` result. Promote dependencies now imported
directly and remove dependencies no longer referenced after the TOML
migration. Do not intentionally upgrade dependency versions.

### Web package manager

Declare the exact Bun version used by CI, retain `bun.lock` as the only
lockfile, and add lint scripts without changing lint rules.

### ESLint migration baseline

Add `web/scripts/eslint-baseline.mjs` and
`web/eslint-baseline.json`. The baseline stores a deterministic multiset
keyed by relative file, severity, rule ID, ESLint message ID, and message text;
line and column are display data, not identity, so unrelated line movement does
not invalidate the baseline.

`bun run lint` runs ESLint through its Node API and compares current findings
with the checked-in baseline. It fails when a finding is added, replaced, or
removed. A removed finding is a failure because the stale baseline must be
shrunk in the same change. `bun run lint:update-baseline` rewrites the
baseline deterministically and is a deliberate developer command, never a CI
step. This freezes current debt without disabling rules or allowing new debt.

## Failure Handling

- Required pull-request jobs are fail-closed; no `continue-on-error`.
- Every job has an explicit timeout.
- Expensive jobs run independently and upload compact evidence on failure.
- Artifacts exclude manager passwords, JWT secrets, join tokens, unredacted
  environment dumps, raw startup configuration, and database contents.
- CI checks do not mutate tracked files; generated differences fail with a
  readable diff.

## Version and Supply-chain Policy

- Pin Go to `1.25.11` and Bun to `1.3.11`.
- Pin every third-party GitHub Action to a reviewed immutable commit SHA and
  add a comment naming the represented upstream release.
- Use lockfile-backed dependency installation.
- Do not add a new test runner or package manager solely for CI formatting.

## Local Verification

```text
GOWORK=off go vet ./cmd/... ./internal/... ./pkg/... ./scripts/... ./docker/...
GOWORK=off go test ./cmd/... -count=1
GOWORK=off go test ./internal/... -count=1
GOWORK=off go test ./pkg/... -count=1
GOWORK=off go test ./scripts/... ./docker/... -count=1
go mod tidy -diff
cd web && bun run lint
cd web && bun run test
cd web && bunx tsc -b
cd web && bun run build
git diff --check
```

Workflow YAML must also parse locally with Ruby's bundled YAML parser before
the first GitHub run:

```text
ruby -e 'require "yaml"; ARGV.each { |f| YAML.load_file(f) }' .github/workflows/*.yml
```

## Acceptance Criteria

1. All pull-request workflow jobs pass from a clean checkout.
2. Grafana coverage passes with useful message-event panels.
3. `go vet` no longer reports an atomic/noCopy value copy.
4. `go mod tidy -diff` produces no output.
5. Go CI does not include ignored `tmp` or `web/node_modules` packages.
6. Baseline-aware web lint reports no new or stale findings, and web tests,
   type checking, and production build pass under pinned Bun.
7. Nightly exposes distinct race, integration, e2e, and three-node smoke
   results with bounded execution and safe artifacts.
8. Changes remain limited to gate repairs, CI, and operating documentation.

## Rollout

Land deterministic gate repairs and workflows together so the first remote run
is interpretable. Observe the first manual or pull-request run and correct
workflow-only environment issues without weakening gates. Repository
administrators may then mark the fast CI jobs as required checks on `main`;
that branch-protection change remains a separate external operation.
