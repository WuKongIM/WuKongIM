# Pull-request checks and CI

The authoritative Workflow catalog lives in
[`.github/workflows/README.md`](../../.github/workflows/README.md).

## Pull-request gate

`.github/workflows/ci.yml` is the only pull-request pipeline. Branch
protection requires one check, `ci-ok`, which aggregates every CI job: a
failed or cancelled job fails it, a job skipped because its area did not
change counts as passing. `scripts/ci/changes.sh` decides which areas a pull
request touched; `lint` always runs.

Human review remains the merge authority. There is no automated review agent.

## Named checks

`cmd/wkcicheck` is the single catalog of named checks. CI and local
development invoke the same selector:

```bash
go run ./cmd/wkcicheck go-format
go run ./cmd/wkcicheck go-vet
go run ./cmd/wkcicheck go-mod-tidy
go run ./cmd/wkcicheck flow-doc-contracts
go run ./cmd/wkcicheck agent-artifact-contracts
go run ./cmd/wkcicheck skill-focused-contracts
go run ./cmd/wkcicheck web               # Bun 1.3.11
go run ./cmd/wkcicheck demo              # Node.js 22.12.0, Yarn 1.22.22
go run ./cmd/wkcicheck docs              # Bun 1.3.11
go run ./cmd/wkcicheck docs-integration  # Bun 1.3.11, Node.js 22.12.0, Chromium
```

Workflows reference these names instead of copying their commands.

All repository-wide Go commands use `GOWORK=off` and explicit roots. Root
`./...` is forbidden because Go package discovery ignores `.gitignore`.

## Performance and compatibility

Performance is not a pull-request gate because GitHub-hosted runners differ
between allocations (see the Performance section of the Workflow README).
`perf.yml` runs nightly, on demand, and as a same-runner base/head A/B when a
pull request carries the `perf` label. `compat.yml` runs the native package
distribution matrix and released EasySDK acceptance nightly. Nightly `ci.yml`
also runs `wkcicheck gofail-e2e` (fault-injection E2E outside MQTT) and splits
full E2E into `wkcicheck e2e-shard-1` to `e2e-shard-4` so it fits its job. Nightly
failures open or update one `[nightly] ... failing` Issue per suite.

## Local verification of CI changes

```bash
GOWORK=off go test ./cmd/wkcicheck ./scripts/... -run 'Workflow|Selector|CICheck' -count=1
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.9 -shellcheck=
bash scripts/ci/changes.sh origin/main HEAD
```
