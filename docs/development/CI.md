# Continuous Integration

WuKongIM uses two fail-closed GitHub Actions workflows. All Go commands use
explicit repository roots and `GOWORK=off`; repository-root `./...` is not a
valid gate because Go package discovery ignores `.gitignore` and can include
local packages below `tmp/` or `web/node_modules/`.

## Fast CI

`.github/workflows/ci.yml` runs for pull requests, pushes to `main`, and manual
dispatches. Obsolete runs for the same pull request/ref are cancelled.

| Check | Timeout | Contract |
| --- | ---: | --- |
| `Go quality` | 10m | tracked-file `gofmt`, `go mod tidy -diff`, explicit-root `go vet` |
| `Go unit (cmd)` | 15m | `./cmd/...` |
| `Go unit (internal)` | 15m | `./internal/...` |
| `Go unit (pkg)` | 15m | `./pkg/...` |
| `Go unit (scripts-docker)` | 15m | `./scripts/... ./docker/...` |
| `Web` | 10m | frozen Bun install, lint baseline, Vitest, TypeScript, build, tracked-output diff |
| `Demo` | 10m | pinned Node/Yarn, frozen install, Vue type check/build, tracked-output diff |

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

## Nightly and Manual Coverage

`.github/workflows/nightly.yml` starts daily at `18:00 UTC` (`02:00` in
Asia/Shanghai) and supports manual dispatch.

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
explicit opt-in stress paths rather than part of the daily workflow.

## Failure Evidence

Nightly uploads evidence only on failure and retains it for 7 days. Race,
integration, and e2e jobs upload their bounded `go test` log. Three-node smoke
uploads only `summary.md`, `cluster.log`, `sim.jsonl`, and `node-logs/*.log`
from `${RUNNER_TEMP}`.

Never upload the whole smoke directory. It can contain a compiled binary, node
databases, PID files, generated configurations, and—if promotion is enabled—an
authentication response and manager token.

## Workflow Maintenance

- Keep `permissions: contents: read` and `persist-credentials: false`.
- Pin actions by full commit SHA and keep the reviewed release in the comment.
- Update `scripts/github_workflows_test.go` when intentionally changing action
  pins, package groups, timeouts, or artifact paths.
- Parse both files and run the contract tests before pushing:

```bash
ruby -e 'require "yaml"; ARGV.each { |f| YAML.load_file(f) }' .github/workflows/*.yml
GOWORK=off go test ./scripts \
  -run '^(TestCIWorkflowContract|TestNightlyWorkflowContract)$' -count=1
```

Repository administrators may mark the fast CI checks as required on `main`
after observing a successful remote run. Branch-protection changes and workflow
dispatches are external operations and are not performed by repository code.
