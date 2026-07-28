# Continuous Integration

WuKongIM is migrating test workflows into fixed, Agent-callable tools. The
authoritative workflow catalog and invocation protocol live beside the
workflows in [`.github/workflows/README.md`](../../.github/workflows/README.md).
All Go commands use explicit repository roots and `GOWORK=off`;
repository-root `./...` is not a valid gate because Go package discovery
ignores `.gitignore` and can include local packages below `tmp/` or
`web/node_modules/`.

## Agent-directed PR Validation (Migration Phase 1)

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
commit status evidence, is the future
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

## Transitional Fast CI

`.github/workflows/ci.yml` runs for pull requests, pushes to `main`, and manual
dispatches. Obsolete runs for the same pull request/ref are cancelled. It
remains active during migration phase 1 so existing branch protection does not
lose required checks. Remove it only after a remote Agent-validation pilot is
green and branch protection requires the `Agent Validation Gate` check from
the GitHub Actions app.

| Check | Timeout | Contract |
| --- | ---: | --- |
| `Go quality` | 10m | tracked-file `gofmt`, `go mod tidy -diff`, explicit-root `go vet` |
| `Go unit (cmd)` | 15m | `./cmd/...` |
| `Go unit (internal)` | 15m | `./internal/...` |
| `Go unit (pkg)` | 15m | `./pkg/...` |
| `Go unit (scripts-docker)` | 15m | `./scripts/... ./docker/...` |
| `Web` | 10m | frozen Bun install, lint baseline, Vitest, TypeScript, build, tracked-output diff |
| `Demo` | 10m | pinned Node/Yarn, frozen install, avatar unit tests, Vue type check/build, tracked-output diff |

The scripts package contains subprocess-heavy black-box and fault-injection
tests. Isolated top-level cases share a two-slot parallel pool; scenarios whose
wall-clock assertions are sensitive to CPU starvation run exclusively. Their
outer Go watchdogs intentionally include scheduler and process-reaping slack.
Production behavior is proved by the shorter timeout passed to the script plus
exit status, evidence, and descendant-cleanup assertions, so do not tighten an
outer watchdog merely to make the test appear faster.

The local equivalent uses Go 1.25.11, Bun 1.3.11, Node 22.12.0, and Yarn 1.22.22, matching CI:

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
deterministic baseline in the same change. CI never updates the baseline.
The complete manager Web production bundle under
`internal/access/manager/webui/dist` is also tracked and rebuilt in CI because
ordinary Go compilation embeds it without invoking Bun.
The complete chat Demo production bundle under
`internal/access/api/demoui/dist` follows the same tracked-artifact contract;
ordinary Go compilation embeds it without invoking Node or Yarn.

## Transitional Nightly and Manual Coverage

`.github/workflows/nightly.yml` starts daily at `18:00 UTC` (`02:00` in
Asia/Shanghai) and supports manual dispatch. Its schedule remains active during
migration phase 1 and is removed only after equivalent Agent-invoked heavy
validation has been proven remotely.

| Check | Timeout | Contract |
| --- | ---: | --- |
| `Go race (internal-runtime)` | 45m | `internal/app` and `internal/runtime/...` |
| `Go race (gateway-transport)` | 45m | `pkg/gateway/...` and `pkg/transport/...` |
| `Go race (channel-cluster-slot)` | 45m | `pkg/channel/...`, `pkg/cluster/...`, and `pkg/slot/...` |
| `Go integration` | 30m | `-tags=integration` across explicit `internal/...` and `pkg/...` roots |
| `Go e2e` | 60m | one prebuilt real `cmd/wukongim` binary and `test/e2e/...` |
| `Three-node smoke` | 30m | base three-node cluster plus real `wkcli sim` traffic |

Nightly failures remain failures; they do not retroactively block a merged pull
request. Gofail dynamic-node faults and the 100K-subscriber scenario remain
explicit opt-in stress paths rather than part of routine validation.

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
  '^(TestAgentPRValidation.*|TestAgentWorkflowCatalogContract|TestCIWorkflowContract|TestNightlyWorkflowContract)$' \
  -count=1
```

Do not change branch protection or delete `ci.yml`/`nightly.yml` as part of
phase 1. Repository administrators first observe a successful remote pilot,
then replace the existing required checks with the single stable
`Agent Validation Gate` check from the GitHub Actions app; the worker's
PR/gate-generation evidence commit statuses must not be required. Legacy
workflow removal is a separate final migration.
