# Repository Guidelines

## Project Structure & Module Organization
- `main.go` is the server entrypoint; CLI wiring lives in `cmd/` (`run`, `stop`, root command).
- Core backend implementation is split between `internal/` (service internals like `api/`, `server/`, `channel/`, `user/`) and reusable libraries in `pkg/` (cluster, raft, `wkdb`, network, protocol packages).
- Integration and end-to-end tests are in `test/e2e/`; package-level tests are colocated as `*_test.go` files.
- Runtime/sample configs are under `config/` and `exampleconfig/`.
- Web admin frontend is in `web/` (Vue 3 + TypeScript + Vite).
- Deployment assets live in `docker/`, root `docker-compose.yaml`, and `Dockerfile*`.

## Build, Test, and Development Commands
- `go run main.go` runs a standalone node.
- `go run main.go --config ./exampleconfig/cluster1.yaml` starts a cluster node with explicit config.
- `go build -o wukongim main.go` builds the server binary.
- `go test ./...` runs all Go tests.
- `go test -bench=. ./pkg/wkdb` runs database benchmarks.
- `cd docker/cluster && docker compose up -d` launches a local multi-node environment.
- `cd web && yarn dev` starts frontend dev server; `yarn build` performs type-check + production build.

## Coding Style & Naming Conventions
- Follow standard Go conventions and always run `gofmt` on changed Go files.
- Keep package names short and lowercase (see `pkg/*`, `internal/*`); exported identifiers use `PascalCase`, internal helpers use `camelCase`.
- Use descriptive, focused files by domain (`channel`, `user`, `webhook`, `raft`, etc.) rather than generic utility buckets.

## Testing Guidelines
- Name tests with Go defaults: files `*_test.go`, functions `TestXxx`, benchmarks `BenchmarkXxx`.
- Add/adjust tests for every behavior change, especially in storage (`pkg/wkdb`), protocol, and cluster paths.
- Run `go test ./...` before pushing; run targeted package tests while iterating for faster feedback.

## Commit & Pull Request Guidelines
- Prefer Conventional Commit prefixes seen in history: `fix:`, `feat:`, `refactor:`, `docs:`.
- Keep each commit scoped to one logical change; reference issue IDs when relevant (for example `fix: #509 ...`).
- PRs should include: purpose/scope, key config or API impacts, test evidence (`go test` output summary), and screenshots for `web/` UI changes.
