# WuKongIM V2 Promotion Design

Date: 2026-07-03
Status: Proposed for review
Scope: default product entrypoint promotion, v2 configuration contract, local scripts, documentation, and migration roadmap.

## 1. Purpose

WuKongIM should make the current v2 architecture the default product runtime.
The first promotion slice changes what operators get when they run
`cmd/wukongim`: the binary should start the `internalv2/app` composition root,
use `pkg/clusterv2`, ControllerV2, Slot Multi-Raft, ChannelV2, and preserve the
project-wide rule that single-node deployment is a single-node cluster.

This design deliberately separates product-entry promotion from package rename
cleanup. Package paths such as `internalv2`, `pkg/clusterv2`,
`pkg/controllerv2`, and `pkg/channelv2` can be renamed after the default
runtime is proven through focused tests and black-box scripts.

## 2. Current State

The codebase already has most of the runtime needed for promotion:

- `cmd/wukongimv2` starts `internalv2/app` and parses the v2 `WK_` config keys.
- `internalv2/app` wires `pkg/clusterv2`, ControllerV2, Slot Multi-Raft,
  ChannelV2, gateway, API, manager, diagnostics, metrics, plugin hooks,
  webhook hooks, conversation, delivery, and node lifecycle surfaces.
- `internalv2/access/api` exposes legacy-compatible message, channel, user,
  conversation, CMD sync, route, health, ready, metrics, debug, top, and
  benchmark routes.
- `internalv2/access/manager` exposes the v2 manager node, slot, controller,
  log, channel, conversation, message, connection, plugin, DB inspect,
  diagnostics, user, and system-user routes.
- Local scripts and e2ev2 tests already exercise `cmd/wukongimv2` as the real
  v2 black-box target.

Important gaps before promotion:

- `cmd/wukongim` still starts `internal/app` and imports old `pkg/cluster`.
- `cmd/wukongimv2/README.md` still describes v2 as a migration-period
  verification entry rather than the production entrypoint.
- Root `wukongim.conf.example` and `cmd/wukongimv2` config parsing are close,
  but the promoted binary must have one documented config contract and one
  default lookup order.
- Many scripts, e2ev2 helpers, and reports carry `wukongimv2` names. Those names
  are useful during the migration but should not remain the only supported
  operator path after promotion.
- v2 code still depends on a few old-path shared contracts:
  `internal/runtime/channelid`, `internal/usecase/plugin/pluginproto`,
  `internal/runtime/plugin`, and `internal/bench/model`.
- Some tooling still depends on old `pkg/cluster`, especially `pkg/db/inspect`,
  `pkg/db/transfer`, and `pkg/slot/proxy`.

## 3. Goals

1. `go run ./cmd/wukongim -config ./wukongim.conf` starts the v2 runtime.
2. The promoted binary keeps the v2 cluster semantics for single-node clusters
   and static or seed-join multi-node clusters.
3. The config file contract remains `wukongim.conf` with `WK_` keys and the
   existing lookup order: `./wukongim.conf`, `./conf/wukongim.conf`,
   `/etc/wukongim/wukongim.conf`.
4. Root examples and scripts make v2 the normal path without requiring users to
   discover `cmd/wukongimv2`.
5. v1 runtime code is not deleted until the promoted entrypoint has a clean
   verification path.
6. Later package renames are planned, but not coupled to the first promotion
   implementation.

## 4. Non-Goals

- Do not rename `internalv2` to `internal` in the first implementation slice.
- Do not rename `pkg/clusterv2`, `pkg/controllerv2`, or `pkg/channelv2` in the
  first implementation slice.
- Do not add facade packages that hide v2 behind old names long term.
- Do not keep a hidden startup branch that bypasses cluster semantics for
  single-node deployments.
- Do not migrate unrelated legacy manager routes unless a promoted default
  endpoint proves they are required.
- Do not change durable storage formats as part of entrypoint promotion.

## 5. Recommended Approach

Use a staged promotion:

```text
Stage 1: Promote the default binary
  -> cmd/wukongim starts internalv2/app
  -> config and examples describe v2 as the default
  -> scripts and tests have canonical wukongim names
  -> cmd/wukongimv2 is either a short-lived alias or removed after callers move

Stage 2: Extract shared contracts away from legacy internal paths
  -> channel ID helpers move to a neutral package
  -> plugin protocol and plugin runtime move to v2-owned or neutral packages
  -> bench target models become explicitly shared

Stage 3: Rename core packages
  -> pkg/controllerv2 becomes pkg/controller
  -> pkg/clusterv2 becomes pkg/cluster
  -> pkg/channelv2 becomes pkg/channel after old DTO dependencies are gone
  -> internalv2 becomes internal after old internal is retired

Stage 4: Delete legacy code and stale docs
  -> remove old v1 app/runtime packages
  -> remove old scripts and examples
  -> update AGENTS.md directory structure
```

Stage 1 is the only implementation scope for the first plan.

## 6. Stage 1 Architecture

`cmd/wukongim` becomes a thin entrypoint over the v2 app:

```text
cmd/wukongim
  -> loadConfig(args or default paths)
  -> internalv2/app.New
  -> App.Start(ctx)
  -> wait for signal
  -> App.Stop(ctx with timeout)
```

The promoted entrypoint should reuse the proven `cmd/wukongimv2` lifecycle
shape:

- `run(ctx, args, newApp)` for testable startup.
- `runtimeApp` interface with `Start(context.Context)` and
  `Stop(context.Context)`.
- bounded stop timeout from v2 cluster config with a default fallback.
- config parser based on the v2 `supportedConfigKeys` list and `WK_` env
  overlay.

The old `cmd/wukongim` config parser should not be kept as a compatibility
layer inside the new binary. Instead, the promoted parser should explicitly
document unsupported old keys and fail with clear errors where the v2 runtime
has no equivalent.

## 7. Entrypoint Layout

First slice file ownership:

- `cmd/wukongim/main.go`: replace the v1 entrypoint with the v2 lifecycle.
- `cmd/wukongim/config.go`: replace the v1 config builder with the v2 config
  builder, keeping the binary name and default config lookup order.
- `cmd/wukongim/*_test.go`: port v2 entrypoint and config tests to the promoted
  command path.
- `cmd/wukongimv2`: keep only if needed as a short-lived wrapper or remove it
  when all scripts and tests use `cmd/wukongim`.
- `cmd/wukongimv2/README.md`: remove or replace with a migration note if the
  alias remains.

Preferred default: remove `cmd/wukongimv2` after scripts and e2ev2 tests move
to `cmd/wukongim`. If a short grace period is required, keep it as a wrapper
that calls the same code as `cmd/wukongim` and mark it deprecated in comments
and docs.

## 8. Configuration Contract

The promoted binary keeps the project config convention:

- main file name: `wukongim.conf`;
- key format: `WK_...`;
- env overrides file values;
- JSON list fields are replaced as a whole by env values;
- `wukongim.conf.example` is the canonical example.

The promoted config must include the v2 keys already parsed by
`cmd/wukongimv2`, including:

- node and cluster identity;
- static `WK_CLUSTER_NODES`;
- seed-join `WK_CLUSTER_SEEDS`, `WK_CLUSTER_ADVERTISE_ADDR`, and
  `WK_CLUSTER_JOIN_TOKEN`;
- Slot and ChannelV2 runtime sizing;
- ControllerV2 and Slot Raft compaction;
- gateway listeners and async runtime;
- manager auth and users;
- metrics, Prometheus, top, diagnostics, webhook, plugin, delivery, and
  conversation authority controls.

Config validation should fail closed for ambiguous or dangerous combinations:

- seed-join config must not be combined with static `WK_CLUSTER_NODES`;
- seed-join requires advertise address and join token;
- invalid JSON list values fail startup;
- invalid hash-slot counts fail startup;
- single-node cluster defaults must still go through ControllerV2 and Slot
  runtime startup.

## 9. Scripts And Local Operations

Scripts should expose canonical `wukongim` names:

- `scripts/start-wukongim-single-node.sh`
- `scripts/start-wukongim-three-nodes.sh`
- `scripts/smoke-wkcli-sim-wukongim.sh`
- benchmark scripts with `wukongim` in the operator-facing name

During Stage 1, old `wukongimv2` script names may remain as compatibility
wrappers that call the canonical scripts. The wrappers should be thin and
should not maintain separate config templates.

The canonical scripts should build `./cmd/wukongim`, use configs under
`scripts/wukongim/`, and write logs under promoted paths such as
`data/wukongim-single-node-logs/`. Existing evidence-preservation behavior in
smoke and bench scripts must be kept.

## 10. Documentation

Update documentation so v2 is no longer described as a separate skeleton:

- `cmd/wukongimv2/README.md` is removed or rewritten as an alias note.
- New or updated `cmd/wukongim/README.md` documents single-node cluster,
  static three-node cluster, seed-join nodes, metrics, Prometheus, and debug
  routes.
- `internalv2/FLOW.md` removes the Phase-1 non-goal that says not to wire v2
  into `cmd/wukongim`.
- `AGENTS.md` directory structure is updated after entrypoint or script
  directories change.
- `docs/development/PROJECT_KNOWLEDGE.md` gets a short note that the default
  runtime is v2 and single-node deployment remains a single-node cluster.

## 11. Error Handling

Promotion should make startup failures easier to diagnose:

- Missing default config paths should show the attempted paths and the missing
  required v2 keys.
- Deprecated v1-only keys should fail with a clear replacement or removal
  message when the promoted binary can detect them.
- Mixed seed and static cluster config should fail before any runtime starts.
- Stop errors should be logged, but stop should still use a bounded context.
- Script wrappers should exit with the underlying command status and preserve
  logs on failure.

## 12. Testing Strategy

Stage 1 verification should focus on promoted surfaces:

```sh
GOWORK=off go test ./cmd/wukongim -count=1
GOWORK=off go test ./internalv2/app ./internalv2/access/api ./internalv2/access/manager -count=1
GOWORK=off go test ./pkg/clusterv2/... ./pkg/controllerv2/... ./pkg/channelv2/... -count=1
GOWORK=off go test ./scripts -run 'WukongIM|WKCLI|Smoke|Bench' -count=1
GOWORK=off go test ./test/e2ev2/... -count=1
git diff --check
```

When full e2ev2 is too expensive for a narrow iteration, the implementation
plan should start with targeted package tests, then run at least one real
single-node `SEND -> SENDACK` smoke and one three-node readiness or wkcli sim
smoke before claiming the promoted binary is ready.

## 13. Performance And Operational Constraints

The entrypoint promotion must not add work to foreground SEND, append, route,
delivery, presence, or manager read paths. It should only change which
composition root starts by default.

Performance-sensitive defaults should preserve the current v2 tuning:

- hash slot count defaults to 256 unless config overrides it;
- ChannelV2 append batching and store worker defaults stay v2-owned;
- gateway async send and auth worker settings remain configurable;
- diagnostics and debug routes remain opt-in;
- Prometheus and top collectors preserve low-cardinality labels;
- scripts keep evidence collection for pprof and metrics triage.

## 14. Later Package Rename Roadmap

After Stage 1 is green on the default binary, later specs can handle package
renames in smaller batches:

1. Move shared helpers out of old internal paths:
   - `internal/runtime/channelid` to a neutral package;
   - `internal/usecase/plugin/pluginproto` to a plugin contract package;
   - `internal/runtime/plugin` to a v2-owned or shared plugin runtime package;
   - `internal/bench/model` to an explicit shared bench model package if it is
     no longer considered part of legacy internal.
2. Rename ControllerV2:
   - move old `pkg/controller` aside or delete it;
   - promote `pkg/controllerv2` to `pkg/controller`;
   - update `pkg/clusterv2/control` and manager imports.
3. Rename ClusterV2:
   - move old `pkg/cluster` aside or delete it;
   - promote `pkg/clusterv2` to `pkg/cluster`;
   - update `cmd/wukongim`, `internalv2`, scripts, and tooling imports.
4. Rename ChannelV2:
   - remove remaining old `pkg/channel` DTO dependencies from v2 store and
     manager helpers;
   - promote `pkg/channelv2` to `pkg/channel`;
   - update message DB adapter boundaries.
5. Rename `internalv2`:
   - remove or archive old `internal`;
   - promote `internalv2` to `internal`;
   - update FLOW docs and import boundary tests.

Each rename batch should have its own implementation plan and verification
gate. Renames should not introduce compatibility facade packages unless they
are temporary test-only shims removed in the same batch.

## 15. Acceptance Criteria

Stage 1 is complete when:

- `cmd/wukongim` starts the v2 app and no production file in that command
  imports `internal/app` or old `pkg/cluster`.
- Root `wukongim.conf.example` matches the promoted v2 config parser.
- Canonical local scripts build and run `./cmd/wukongim`.
- Old `wukongimv2` script or command names are removed or clearly delegated to
  canonical `wukongim` names.
- Internalv2 FLOW docs no longer call v2 a non-production skeleton.
- Focused Go tests and at least one real black-box v2 smoke pass through the
  promoted binary.
- No new single-node local bypass branch exists.
