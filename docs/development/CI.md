# Agent-Directed Validation

WuKongIM exposes test workflows as fixed, Agent-callable tools. The
authoritative workflow catalog and invocation protocol live beside the
workflows in [`.github/workflows/README.md`](../../.github/workflows/README.md).
All Go commands use explicit repository roots and `GOWORK=off`;
repository-root `./...` is not a valid gate because Go package discovery
ignores `.gitignore` and can include local packages below `tmp/` or
`web/node_modules/`.

## Agent-directed PR Validation

`.github/workflows/agent-pr-validation-control.yml` is the trusted control
plane. An authorized Agent publishes a versioned, commit-bound validation plan,
sets the fixed suite labels, and applies the one-shot `agent-ci/run` label. The
control plane never checks out pull-request code; it dispatches
`.github/workflows/agent-pr-validation.yml` with the pull request number, exact
head SHA, exact test-merge SHA, merge-gate generation run ID, actor, and
request-run identity.

The validation workflow revalidates the plan and minimum path-to-suite mapping,
checks out that immutable test-merge SHA with read-only credentials, and runs
only the selected fixed suites. It publishes PR/gate-generation-bound request
and evidence commit statuses. `.github/workflows/agent-pr-merge-gate.yml`
independently verifies the exact PR number, head SHA, test-merge SHA from
`github.sha`, gate run ID, trusted request run, and successful worker run, then
publishes the stable GitHub Actions check `Agent Validation Gate`. Its first
PR-event attempt fails closed; the isolated terminal status job reruns that
exact gate run after publishing generation-bound evidence, while the gate waits
within a fixed bound for the worker to finish. This check, not the worker's
commit status evidence, is the
branch-protection target. Editing, opening, reopening, or adding a commit
invalidates the old plan and gate. A failed validation attempt may be retried
once within the same gate generation only when the new plan cites the prior
Actions run ID. The next PR edit, open, reopen, or synchronize event creates a
new generation, so old evidence cannot block or satisfy fresh validation.
Gate generations use separate concurrency groups, and both the worker handoff
and the gate's final verdict reject a run ID that is no longer the newest
generation for the exact head and test-merge.

The bootstrap PR that first adds the merge-gate Workflow is the sole initial
attempt exception. It passes only after a complete, non-truncated base Git tree
proves that `agent-pr-merge-gate.yml` is not yet present; after merge, all first
attempts fail closed.

Test jobs cannot accept arbitrary commands or package paths. Status writes are
isolated from jobs that execute pull-request code, action caches are disabled,
renamed paths are classified by both their old and new names, and fork pull
requests use the same untrusted-code boundary. See the workflow catalog for
labels, plan schema, retry rules, and fork approval policy.

## Fixed Fast Validation

There is no automatic pull-request or `main` push test Workflow. After
inspecting the exact diff, an Agent selects `agent-ci/go-fast`,
`agent-ci/web`, and `agent-ci/demo` when the path-to-suite rules or assessed
risk require them. Branch protection waits for the stable
`Agent Validation Gate`, so a PR cannot merge until the selected tools publish
valid evidence for its exact head and test-merge commit.

| Check | Timeout | Contract |
| --- | ---: | --- |
| `Go quality` | 10m | tracked-file `gofmt`, `go mod tidy -diff`, explicit-root `go vet` |
| `Go unit (cmd)` | 15m | `./cmd/...` |
| `Go unit (internal)` | 15m | `./internal/...` |
| `Go unit (pkg)` | 15m | `./pkg/...` |
| `Go unit (scripts-docker)` | 15m | `./scripts/... ./docker/...` |
| `Web` | 10m | frozen Bun install, lint baseline, Vitest, TypeScript, build, tracked-output diff |
| `Demo` | 10m | pinned Node/Yarn, frozen install, avatar unit tests, Vue type check/build, tracked-output diff |

The default scripts package tier contains static source/configuration
contracts, parsers, AWK/JQ transforms, help output, and no-background dry runs.
Subprocess-heavy black-box, build, lifecycle, retry/readiness, TCP, and
fault-injection tests live in `*_integration_test.go` behind the `integration`
build tag. The integration tier shares a two-slot parallel pool; scenarios
whose wall-clock assertions are sensitive to CPU starvation run exclusively.
Their outer Go watchdogs intentionally include scheduler and process-reaping
slack. Production behavior is proved by the shorter timeout passed to the
script plus exit status, evidence, and descendant-cleanup assertions, so do
not tighten an outer watchdog merely to make the test appear faster.

The local equivalent uses Go 1.25.11, Bun 1.3.11, Node 22.12.0, and Yarn
1.22.22, matching the fixed Agent tools:

```bash
export GOWORK=off
test "$(go env GOVERSION)" = "go1.25.11"
unformatted="$(git ls-files -z '*.go' | xargs -0 gofmt -l)"
test -z "$unformatted"
GOWORK=off go vet ./cmd/... ./internal/... ./pkg/... ./scripts/... ./docker/...
GOWORK=off go test ./cmd/... -count=1
GOWORK=off go test ./internal/... -count=1
GOWORK=off go test ./pkg/... -count=1
GOWORK=off go test ./scripts/... ./docker/... -count=1
GOWORK=off go mod tidy -diff

cd web
test "$(bun --version)" = "1.3.11"
bun install --frozen-lockfile
bun run lint
bun run test
bunx tsc -b
bun run build
changes="$(git status --porcelain -- ../internal/access/manager/webui/dist)"
test -z "$changes"

cd ../demo/chatdemo
test "$(node --version)" = "v22.12.0"
test "$(corepack yarn --version)" = "1.22.22"
corepack yarn install --frozen-lockfile
corepack yarn test
corepack yarn build
changes="$(git status --porcelain -- ../../internal/access/api/demoui/dist)"
test -z "$changes"
```

`bun run lint` compares current ESLint results with
`web/eslint-baseline.json`. A new, changed, or removed finding fails. After a
reviewed lint cleanup, run `bun run lint:update-baseline` and commit the smaller
deterministic baseline in the same change. Agent validation never updates the
baseline.
The complete manager Web production bundle under
`internal/access/manager/webui/dist` is also tracked and rebuilt by the selected
Web tool because ordinary Go compilation embeds it without invoking Bun.
The complete chat Demo production bundle under
`internal/access/api/demoui/dist` follows the same tracked-artifact contract;
ordinary Go compilation embeds it without invoking Node or Yarn.

## Fixed Heavy Validation

There is no scheduled test suite. An Agent selects `agent-ci/go-race`,
`agent-ci/go-integration`, `agent-ci/go-e2e`, or
`agent-ci/three-node-smoke` when the diff and risk assessment require heavier
evidence.

| Check | Timeout | Contract |
| --- | ---: | --- |
| `Go race (internal-runtime)` | 45m | `internal/app` and `internal/runtime/...` |
| `Go race (gateway-transport)` | 45m | `pkg/gateway/...` and `pkg/transport/...` |
| `Go race (channel-cluster-slot)` | 45m | `pkg/channel/...`, `pkg/cluster/...`, and `pkg/slot/...` |
| `Go integration` | 40m | `-tags=integration` across explicit `internal/...`, `pkg/...`, and `scripts/...` roots; scripts have a 10m hard step limit |
| `Go e2e` | 60m | one prebuilt real `cmd/wukongim` binary and `test/e2e/...` |
| `Three-node smoke` | 30m | base three-node cluster plus real `wkcli sim` traffic |

Selected heavy-suite failures fail Agent validation and keep the merge gate
closed. Gofail dynamic-node faults and the 100K-subscriber scenario remain
explicit opt-in stress paths rather than part of routine validation.

The local integration equivalents are separate commands so the core package
gate keeps its existing package serialization while scripts use bounded
test-level concurrency:

```bash
GOWORK=off go test -tags=integration ./internal/... ./pkg/... -count=1 -p=1
GOWORK=off go test -tags=integration ./scripts/... -count=1 -timeout=9m -parallel=2
```

## Failure Evidence

Heavy validation usually uploads evidence only on failure and retains it for 7
days. Race, integration, and e2e jobs upload their bounded `go test` log.
The Cloud Medium recipient acceptance log is the exception: it is always
uploaded for 14 days so a passing performance gate remains auditable.
Three-node smoke uploads only `summary.md`, `cluster.log`, `sim.jsonl`, and
`node-logs/*.log` from `${RUNNER_TEMP}`.

Never upload the whole smoke directory. It can contain a compiled binary, node
databases, PID files, generated configurations, and—if promotion is enabled—an
authentication response and manager token.

## Workflow Maintenance

- Keep root workflow permissions empty. Jobs that execute pull-request code get
  only `contents: read`; only isolated control/status jobs receive write access.
- Keep `persist-credentials: false` for every pull-request checkout and disable
  writable caches on untrusted code paths.
- Pin actions by full commit SHA and keep the reviewed release in the comment.
- Update `scripts/github_workflows_test.go` when intentionally changing action
  pins, package groups, permissions, trigger contracts, or artifact paths.
- Update `scripts/agent-pr-validation-plan.sh` and its tests together when the
  plan schema, suite labels, or minimum path mapping changes.
- Parse all workflow files and run the contract tests before pushing:

```bash
ruby -e 'require "yaml"; ARGV.each { |f| YAML.load_file(f) }' .github/workflows/*.yml
bash -n scripts/agent-pr-validation-plan.sh
GOWORK=off go test ./scripts -run \
  '^(TestAgentPRValidation.*|TestAgentWorkflow.*|TestLegacyAutomaticTestWorkflowsAreAbsent)$' \
  -count=1
```

The migration completed on 2026-07-28 after same-repository PR #654 and Fork PR
#655 passed the full protocol. `main` branch protection requires the single
stable `Agent Validation Gate` check from the GitHub Actions app; the worker's
PR/gate-generation evidence commit statuses are not required. The former
automatic `ci.yml` and scheduled `nightly.yml` Workflows must not be restored.
