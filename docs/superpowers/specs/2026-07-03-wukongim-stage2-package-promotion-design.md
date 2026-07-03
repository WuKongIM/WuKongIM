# WuKongIM Stage 2 Package Promotion Design

Date: 2026-07-03
Status: Proposed for review
Scope: package-path promotion after `cmd/wukongim` has become the default v2 runtime.

## 1. Purpose

Stage 1 promoted the product entrypoint: `cmd/wukongim` starts the v2
composition root. Stage 2 should make the source tree match that product
reality by promoting v2 package paths to canonical names:

- `internalv2` becomes the canonical server kernel under `internal`.
- `pkg/controllerv2` becomes `pkg/controller`.
- `pkg/clusterv2` becomes `pkg/cluster`.
- `pkg/channelv2` becomes `pkg/channel`.
- v2 black-box tests and scripts lose `v2` in operator-facing names.

The migration must preserve single-node cluster semantics, avoid compatibility
facades as a long-term design, and keep high-QPS message paths unchanged except
for import paths and ownership cleanup.

## 2. Current Evidence

The current tree is not ready for a safe big-bang directory swap.

Direct production and test importers found with `GOWORK=off go list -json ./...`
include:

| Target path | Direct importers |
| --- | ---: |
| `internalv2` | 18 |
| `internal/bench` | 15 |
| `internal/runtime/channelid` | 17 |
| `internal/runtime/plugin` | 3 |
| `internal/usecase/plugin/pluginproto` | 13 |
| `pkg/clusterv2` | 14 |
| `pkg/controllerv2` | 14 |
| `pkg/channelv2` | 14 |
| `pkg/cluster` | 13 |
| `pkg/controller` | 8 |
| `pkg/channel` | 27 |

Important blockers:

- `internalv2` still imports reusable helpers from old `internal`, especially
  `internal/bench/model`, `internal/runtime/channelid`,
  `internal/runtime/plugin`, and `internal/usecase/plugin/pluginproto`.
- `internal/bench` is not old server-kernel code. It is wkbench black-box
  tooling used by `cmd/wkbench`, `cmd/wkcli`, and v2 bench APIs. It must be
  preserved when old `internal` server packages are retired.
- `pkg/channelv2/store` imports old `pkg/channel` DTOs because
  `pkg/db/message` still exposes storage APIs in terms of old channel types.
- Old `pkg/cluster` still owns utility paths used by tooling, including
  `pkg/cluster/hashslot`, `pkg/db/inspect`, `pkg/db/transfer`, and
  `pkg/slot/proxy`.
- Old `pkg/controller` is coupled to old `pkg/cluster`. Promoting
  ControllerV2 without moving or retiring the old cluster path would leave two
  control-plane packages fighting for canonical names.

## 3. Alternatives

### A. Big-Bang Rename

Move every v2 directory into the old canonical path in one branch and rewrite
all imports at once.

This is fast to type but unsafe. It mixes behavior-neutral import rewrites with
real ownership changes, storage DTO extraction, old test retirement, and docs
updates. A compile failure would not tell us which boundary is wrong.

### B. Phased Canonical Ownership

Promote packages by dependency order. First extract shared contracts out of
old `internal` and old `pkg/channel`; then promote Controller, Cluster,
Channel, and finally `internalv2`.

This is the recommended approach. Each phase has a small import boundary and a
focused verification gate. The final tree has canonical paths and no long-term
v2 suffix packages.

### C. Canonical Facade Packages

Create `pkg/cluster`, `pkg/controller`, `pkg/channel`, and `internal` facades
that import v2 packages while leaving v2 directories in place.

This reduces initial churn but creates the compatibility layer we are trying to
delete. It also makes import-boundary tests weaker because new code could still
choose either old or v2 paths. Do not use this as the final design.

## 4. Recommended Migration Order

### Phase 0: Freeze Ownership And Import Audits

Classify the tree before any rename:

- Canonical v2 future:
  `cmd/wukongim`, `internalv2`, `pkg/controllerv2`, `pkg/clusterv2`,
  `pkg/channelv2`, `test/e2ev2`, and canonical scripts.
- Legacy server runtime to retire or move aside:
  old `internal/app`, `internal/access`, `internal/usecase`,
  `internal/runtime` server-only packages, `pkg/controller`, `pkg/cluster`,
  and old `pkg/channel` ISR runtime.
- Shared or tooling code to preserve:
  `internal/bench`, `internal/runtime/channelid`,
  `internal/usecase/plugin/pluginproto`, plugin host runtime, hash-slot
  helpers, and message DB storage DTOs.

Add a small import-audit command to each phase plan:

```sh
GOWORK=off go list -json ./... >/tmp/wukongim-go-list.json
rg -n 'github.com/WuKongIM/WuKongIM/(internalv2|pkg/(clusterv2|controllerv2|channelv2))' --glob '*.go'
```

The second command is expected to find imports before final promotion and no
production Go imports after final promotion.

### Phase 1: Extract Shared Contracts

This phase removes v2 dependencies on old server-kernel paths while preserving
black-box tooling.

Recommended moves:

- Move channel ID helpers from `internal/runtime/channelid` to
  `pkg/protocol/channelid`.
  These helpers encode protocol-facing personal, command, agent, and request
  subscriber channel IDs, and currently only depend on `pkg/protocol/frame`.
- Move plugin wire contracts from `internal/usecase/plugin/pluginproto` to
  `pkg/plugin/pluginproto`.
  This is an external plugin protocol contract, not an old usecase.
- Move the plugin process host from `internal/runtime/plugin` into the v2-owned
  runtime before the `internalv2` rename, for example
  `internalv2/runtime/pluginhost`.
  After final internal promotion it becomes `internal/runtime/pluginhost`.
- Keep `internal/bench` in place for now and mark it as shared black-box
  tooling, not v1 server runtime. It can remain under `internal/bench` after
  `internalv2` is merged into `internal`.

Do not rename `internalv2` in this phase. The success condition is that v2
server packages no longer need old `internal` server-kernel imports except the
explicitly preserved `internal/bench` tooling boundary.

Verification:

```sh
GOWORK=off go test ./pkg/protocol/channelid ./pkg/plugin/pluginproto -count=1
GOWORK=off go test ./internalv2/... ./cmd/wukongim -count=1
GOWORK=off go test ./internal/bench/... ./cmd/wkbench ./cmd/wkcli/... -count=1
git diff --check
```

### Phase 2: Promote ControllerV2

Controller promotion should happen before cluster promotion because
`pkg/clusterv2/control` depends on ControllerV2 state and runtime contracts.

Steps:

1. Move old `pkg/controller` to a temporary legacy path or retire it together
   with the old cluster code if no remaining required package imports it.
2. If old `pkg/cluster` must still compile during this phase, rewrite it to
   import the moved legacy controller path.
3. Move `pkg/controllerv2` to `pkg/controller`.
4. Change package declarations and docs from `controllerv2` language to
   canonical controller language where it is public-facing.
5. Update `pkg/clusterv2/control`, `internalv2/usecase/management`, and tests
   to import `pkg/controller`.
6. Update `pkg/controller/FLOW.md` so it describes the promoted ControllerV2
   engine, not the old control plane.

No long-term `pkg/controllerv2` forwarding package should remain.

Verification:

```sh
GOWORK=off go test ./pkg/controller/... -count=1
GOWORK=off go test ./pkg/clusterv2/control ./pkg/clusterv2/tasks -count=1
GOWORK=off go test ./internalv2/usecase/management ./internalv2/access/manager -count=1
rg -n 'pkg/controllerv2' --glob '*.go'
git diff --check
```

### Phase 3: Promote ClusterV2

Cluster promotion is the main product-runtime rename. It should happen only
after Controller uses the canonical path.

Steps:

1. Remove or move old `pkg/cluster` to a temporary legacy path.
2. Extract old cluster utilities that are not cluster runtime ownership:
   - hash-slot helpers should move to a neutral package such as
     `pkg/slot/hashslot` or `pkg/hashslot`;
   - DB inspect and transfer code should not import a full cluster runtime just
     to derive metadata shapes.
3. Move `pkg/clusterv2` to `pkg/cluster`.
4. Update imports in `cmd/wukongim`, `internalv2`, manager routes, tests,
   scripts, and FLOW docs.
5. Update public text from `clusterv2` to `cluster` unless it is historical
   evidence.

Success criteria:

- `cmd/wukongim` imports `pkg/cluster`, not `pkg/clusterv2`.
- The promoted cluster still wires Controller, Slot Multi-Raft, Channel
  runtime, typed node RPC, and single-node cluster startup.
- Old cluster runtime is not reachable from product code.

Verification:

```sh
GOWORK=off go test ./pkg/cluster/... -count=1
GOWORK=off go test ./cmd/wukongim ./internalv2/... -count=1
GOWORK=off go test ./pkg/db/inspect ./pkg/db/transfer ./pkg/slot/proxy -count=1
rg -n 'pkg/clusterv2' --glob '*.go'
git diff --check
```

### Phase 4: Promote ChannelV2

Channel promotion should be last among `pkg` renames. It has the highest risk
because old `pkg/channel` is both a runtime and a shared DTO source for
message DB storage.

Required pre-work:

- Break `pkg/db/message -> pkg/channel` coupling before the rename.
- Move storage-only DTOs and errors out of old `pkg/channel` into the message
  DB boundary, for example `pkg/db/message` or a small subpackage owned by it.
- Update old channel runtime, if still compiling, to use the extracted storage
  DTOs through the legacy path.
- Update `pkg/channelv2/store` so it adapts to message DB without importing the
  old channel runtime.

Then:

1. Remove or move old `pkg/channel` to a temporary legacy path.
2. Move `pkg/channelv2` to `pkg/channel`.
3. Update `pkg/cluster`, `internalv2/infra/cluster`, manager DTO adapters, and
   tests.
4. Update `pkg/channel/FLOW.md` to describe the promoted multi-reactor channel
   log runtime and its performance constraints.

Success criteria:

- `pkg/channel/store` can import message DB without import cycles.
- `pkg/db/message` does not import the promoted channel root.
- SEND append and follower apply semantics are unchanged.
- No product code imports `pkg/channelv2`.

Verification:

```sh
GOWORK=off go test ./pkg/channel/... ./pkg/db/message/... -count=1
GOWORK=off go test ./pkg/cluster/... ./internalv2/infra/cluster -count=1
GOWORK=off go test ./cmd/wukongim ./internalv2/... -count=1
rg -n 'pkg/channelv2' --glob '*.go'
git diff --check
```

### Phase 5: Promote InternalV2

Only promote the server kernel after the `pkg` runtime paths are canonical.

Steps:

1. Preserve `internal/bench` as canonical black-box tooling.
2. Remove or move old server-kernel subtrees under `internal`:
   `access`, `app`, `contracts`, `log`, `observability`, `runtime`, and
   `usecase`, except for preserved shared tooling.
3. Move `internalv2` server-kernel subtrees into `internal`.
4. Update imports in `cmd/wukongim`, tests, scripts, and docs.
5. Rename `test/e2ev2` into `test/e2e` or merge it into the canonical e2e
   tree after old e2e scenarios are retired or moved aside.
6. Remove `cmd/wukongimv2` and `scripts/wukongimv2` aliases if any remain.
7. Update `AGENTS.md`, `internal/FLOW.md`, and
   `docs/development/PROJECT_KNOWLEDGE.md`.

Success criteria:

- `cmd/wukongim` imports `internal/app`.
- Production Go code does not import `internalv2`.
- New code rules say `internal/access -> internal/usecase/runtime`, not
  `internalv2/access -> internalv2/usecase/runtime`.
- `internal/bench` remains available for wkbench and black-box target code.

Verification:

```sh
GOWORK=off go test ./cmd/wukongim ./internal/... -count=1
GOWORK=off go test ./pkg/... -count=1
GOWORK=off go test ./test/e2e/... -p=1 -count=1
rg -n 'internalv2|cmd/wukongimv2|test/e2ev2' --glob '*.go' --glob '*.md' --glob '*.sh'
git diff --check
```

For expensive black-box coverage, run the serial v2-derived e2e subset first,
then the broader suite when the import tree is stable.

## 5. Final Tree Shape

Target structure after Stage 2:

```text
cmd/
  wukongim/              product entrypoint over internal/app
  wkcli/
  wkbench/
  wkdb/

internal/
  app/                   canonical v2 composition root
  access/
  contracts/
  infra/
  log/
  observability/
  runtime/
  usecase/
  bench/                 preserved black-box wkbench tooling

pkg/
  channel/               promoted ChannelV2 multi-reactor runtime
  cluster/               promoted ClusterV2 runtime
  controller/            promoted ControllerV2 engine
  db/
  gateway/
  plugin/
  protocol/
  slot/
  transport/
  transportv2/
  workqueue/

test/
  e2e/                   canonical real-binary black-box tests
```

Old v1 runtime packages should either be deleted or moved to clearly named
temporary legacy paths during the branch. The final merge should not leave both
canonical and v2-suffixed product paths.

## 6. Documentation Updates

Each phase that changes paths must update the closest FLOW file in the same
commit:

- Controller phase: `pkg/controller/FLOW.md`.
- Cluster phase: `pkg/cluster/FLOW.md`.
- Channel phase: `pkg/channel/FLOW.md`.
- Internal phase: `internal/FLOW.md` and `AGENTS.md`.

Project notes should stay short:

- `docs/development/PROJECT_KNOWLEDGE.md` gets one concise note that source
  package paths are now canonical and `cmd/wukongim` remains the v2 runtime.
- Historical design docs under `docs/superpowers` do not need mass rewrites.
  They can keep old names as historical evidence.

## 7. Performance And Safety Constraints

- Do not add runtime branches that bypass cluster semantics for single-node
  clusters.
- Do not change foreground SEND, append, delivery, presence, or manager query
  behavior as part of path promotion unless a boundary extraction requires a
  mechanical type move.
- Keep hash slot defaults at 256 unless config explicitly overrides them.
- Keep Channel runtime batching, worker, and commit coordinator defaults
  behaviorally unchanged.
- Keep diagnostics, top, pprof, and Prometheus labels low-cardinality.
- Prefer compile-time import-boundary tests and real black-box smoke over mock
  reassurance.

## 8. Acceptance Criteria

Stage 2 is complete when:

- `cmd/wukongim` imports `internal/app` and `pkg/cluster`.
- No production Go file imports `internalv2`, `pkg/clusterv2`,
  `pkg/controllerv2`, or `pkg/channelv2`.
- Canonical `pkg/controller`, `pkg/cluster`, and `pkg/channel` FLOW docs
  describe the promoted v2 implementations.
- `internal/bench` still builds and supports wkbench and wkcli workflows.
- `test/e2e` contains the real-binary v2-derived black-box coverage.
- `AGENTS.md` directory structure matches the promoted tree.
- Focused package tests, serial black-box e2e, and at least one real
  `SEND -> SENDACK` smoke pass through the promoted binary.

## 9. Recommended Next Plan

If this design is approved, write the implementation plan as five separate
checklists:

1. Shared contract extraction plan.
2. Controller package promotion plan.
3. Cluster package promotion plan.
4. Channel package promotion plan.
5. Internal package promotion and docs plan.

Each checklist should be executable independently and should stop after its
verification gate before starting the next rename.
