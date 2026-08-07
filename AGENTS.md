# AGENTS.md

## Project and Rule Levels

WuKongIM is a high-performance general-purpose messaging system maintained as
a Go monorepo.

The keywords `MUST`, `MUST NOT`, `SHOULD`, and `MAY` define requirement levels:

- `MUST` and `MUST NOT` are mandatory.
- `SHOULD` is the default unless there is a documented reason to deviate.
- `MAY` is optional.

## Core Runtime Semantics

- Every deployment is a cluster. A one-node deployment is a **single-node
  cluster**, not a standalone mode that bypasses cluster behavior.
- New code MUST NOT add business paths that bypass cluster semantics.
- Documentation, comments, and test names MUST use "single-node cluster" for
  deployment topology. Use "local" only for behavior within one node.
- Designs MUST account for high-scale workloads such as 100,000-member groups,
  high message rates, many channels, and many online users.
- Unless explicitly specified otherwise, designs MUST assume 256 hash slots.

## Before You Work

- Development SHOULD use a Git worktree with its working copy located in the .worktrees directory
- Before reading or changing a package, you MUST check that package for a
  `FLOW.md` and read it when present.
- If a change makes an applicable `FLOW.md` inaccurate, update it in the same
  change.
- More specific `AGENTS.md` files in a target subtree add to or override this
  file for that subtree.

## Design and Implementation

### Internal Architecture

| Path | Responsibility |
| --- | --- |
| `internal/access/*` | Entry-protocol adapters only; no reusable business rules. |
| `internal/usecase/*` | Entry-agnostic business orchestration. |
| `internal/runtime/*` | Reusable node-local runtime capabilities; no entry logic. |
| `internal/infra/*` | Adapters from internal ports to `pkg` runtimes or external infrastructure. |
| `internal/app/*` | The only composition root under `internal`; dependency wiring and lifecycle belong here. |
| `pkg/gateway/*` | Reusable gateway infrastructure; no product-specific use-case orchestration. |

Code MUST maintain these dependency directions:

```text
access -> usecase/runtime
usecase -> runtime/pkg
app -> access/usecase/runtime/infra/pkg
```

- Use cases MAY depend on narrow shared `pkg` contracts, but MUST NOT depend on
  entry-specific gateway/frame types or construct concrete infrastructure
  implementations.
- New HTTP, RPC, and task entrypoints SHOULD live in
  `internal/access/<entry>`.
- Reusable business capabilities SHOULD live in
  `internal/usecase/<domain>`.
- Node-local state, online routing, allocators, and similar capabilities SHOULD
  live in `internal/runtime/<capability>`.
- Infrastructure adapters MUST implement internal ports without moving
  business rules into `internal/infra`.
- New code MUST NOT recreate `internal/legacy` or `pkg/legacy/*`.
- New code MUST NOT introduce a broad service package or another global
  aggregate service object.
- Cross-layer changes SHOULD add an `internal/app` wiring test or an entry-level
  integration test.

### Code Quality

- Code MUST NOT be over-engineered. Abstractions SHOULD address current
  behavior or a clear extension point.
- Critical methods and important fields of primary structs MUST have English
  comments that explain their responsibility or constraints.
- Performance-sensitive designs MUST consider CPU, memory, allocations,
  contention, queue bounds, backpressure, and fanout at the expected scale.

### Configuration

- The primary configuration file is `wukongim.toml` in TOML format.
- TOML keys MUST use domain-grouped `snake_case`.
- Environment variables MUST use the `WK_` prefix and override file values.
- List values supplied through environment variables MUST replace the full list
  using JSON, for example:
  `WK_CLUSTER_NODES='[{"id":1,"addr":"wk-node1:7000"}]'`.
- Without `-config`, configuration is searched in this order:
  `./wukongim.toml`, `./conf/wukongim.toml`,
  `/etc/wukongim/wukongim.toml`.
- Configuration changes MUST update `wukongim.toml.example`.
- New or changed configuration fields MUST have detailed English comments.

## Testing and Validation

### Test Policy

- You MUST run at least the tests directly related to the change.
- Unit tests MUST remain fast. Tests that simulate realistic elapsed time or
  external integration MUST use the `integration` build tag.
- Tests under `scripts/` that build binaries, start or signal processes, use
  real sleeps/deadlines, open TCP listeners, or exercise retry/readiness loops
  MUST live in `*_integration_test.go` with the `integration` build tag.
  Static source/configuration contracts, parsers, AWK/JQ transforms, help
  output, and no-background dry runs SHOULD remain in the default unit tier.
- Development SHOULD default to unit tests. Integration and E2E suites SHOULD
  run only when the change affects those behaviors or the task explicitly
  requires them.
- Repository-wide Go gates MUST NOT use root `./...`. Go ignores `.gitignore`
  during package discovery and may include local packages under `tmp/` or
  `web/node_modules/`.
- E2E tests MUST remain process-level black-box tests and MUST follow
  `test/e2e/AGENTS.md` for their structure and execution.
- `pkg/metrics` instrumentation, `internal/bench` plans, and scripts SHOULD be
  added to an E2E workflow only when the scenario requires them.

### Common Commands

- Full unit suite:
  `GOWORK=off go test ./cmd/... ./internal/... ./pkg/... ./scripts/... ./docker/... -count=1`
- Focused unit tests:
  `GOWORK=off go test ./internal/... ./pkg/...`
- Integration tests:
  `GOWORK=off go test -tags=integration ./internal/... ./pkg/... -count=1`
- Scripts integration tests:
  `GOWORK=off go test -tags=integration ./scripts/... -count=1 -timeout=9m -parallel=2`
- E2E tests:
  `GOWORK=off go test -tags=e2e ./test/e2e/... -count=1`
- Run the server:
  `go run ./cmd/wukongim`
- Run with an explicit configuration:
  `go run ./cmd/wukongim -config ./wukongim.toml`

## Bug Diagnosis

1. MUST gather evidence; do not diagnose by guessing.
2. SHOULD inspect metrics first. If the necessary signal is missing, add
   bounded observability.
3. SHOULD use `pprof` for performance problems.

## Documentation and Knowledge

- Important repository rules and business knowledge MUST be recorded concisely
  in `docs/development/PROJECT_KNOWLEDGE.md`.
- Unrelated code-quality findings MAY be recorded in
  `docs/development/CODE_QUALITY.md`; then continue the current task.
- When the stable repository structure changes, update the directory guide
  below.

## Agent Workflows

- GitHub Issues are the repository issue tracker. Follow
  `docs/agents/issue-tracker.md`.
- Use the canonical triage labels defined in
  `docs/agents/triage-labels.md`.
- Use the single-context domain documentation layout defined in
  `docs/agents/domain.md`.
- The serverless GitHub Issue Agent follows `docs/agents/issue-agent.md`.
  Its control code, Workflows, policy, schemas, prompts, and instruction files
  are protected from automated changes. Every Codex task freezes applicable
  `AGENTS.md` and `FLOW.md` digests from its exact source revision.

## GitHub Actions tools

GitHub Actions are Agent-callable tools or explicit safety automations. Before
invoking or changing one, read `.github/workflows/README.md` and follow its
authorization, Review Agent evidence, retry, and monitoring contracts.

## Directory Guide

| Path | Purpose |
| --- | --- |
| `.agents/` | Repository-local agent skills and support files. |
| `.github/` | CI workflows and cloud-simulation support. |
| `cmd/wukongim/` | Product entrypoint that loads configuration and starts `internal/app`. |
| `cmd/wkbench/` | Black-box benchmark CLI. |
| `cmd/wkchatlifecycle/` | Fixed Run Plan materialization, Lease selector, and rehearsal-report validation. |
| `cmd/wkcli/`, `cmd/wkdb/` | Operations and local read-only storage diagnostics. |
| `cmd/wkcloud*/`, `cmd/wkanalysis/` | Cloud lease identity/lifecycle, simulation, deployment, validation, viewing, and analysis tools. |
| `cmd/wkissueagent/` | JSON-only GitHub Actions entrypoint for the stateless Issue Agent. |
| `cmd/wkreviewcheck/` | Frozen selector-only helper for composite Review Agent checks. |
| `internal/access/` | HTTP, gateway, node RPC, manager, plugin, and cloud-analysis entry adapters. |
| `internal/usecase/` | Reusable business use cases. |
| `internal/runtime/` | Reusable node-local runtimes. |
| `internal/infra/` | Cluster, delivery, backup, and cloud infrastructure adapters. |
| `internal/app/` | Product composition root and lifecycle. |
| `internal/config/`, `internal/contracts/` | Configuration loading and cross-layer contracts. |
| `internal/**/*agent*` | Issue Agent and Review Agent contracts, orchestration, clean verification, GitHub adapters, CLI boundaries, and composition. |
| `internal/bench/` | Benchmark planning, coordination, workers, workloads, and reporting. |
| `pkg/` | Reusable storage, protocol, gateway, cluster, channel, controller, slot, transport, metrics, plugin, and work-queue libraries. |
| `test/e2e/` | Real-process black-box E2E suites and shared harness. |
| `docs/` | Architecture, development, ADR, specification, plan, report, and runbook documentation. |
| `docs-site/` | Standalone Fumadocs application for the bilingual public v3 documentation. |
| `scripts/` | Repository automation, E2E gates, and cloud-simulation helpers. |
| `docker/` | Development clusters, simulation, and observability configurations. |
| `web/` | Manager React/Vite source. |
| `demo/chatdemo/` | Embedded chat demo Vue/Vite source. |
| `resources/` | Repository resources used by product and tooling. |
