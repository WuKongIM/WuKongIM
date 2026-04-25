# `pkg/cluster/raftcluster` Split Design

**Date:** 2026-04-10
**Status:** Proposed

## Overview

Split `pkg/cluster/raftcluster` into multiple packages with single, explicit responsibilities and migrate callers to depend on the narrow capability they actually use.

This redesign is intentionally non-compatible. It optimizes for semantic accuracy, dependency clarity, and long-term maintainability rather than minimizing import churn.

## Goals

- Split the current `raftcluster` package into focused packages with one clear runtime responsibility each
- Use package names that describe concrete capability rather than historical aggregation
- Move `internal/app` to compose multiple cluster-side capabilities explicitly instead of relying on one large facade
- Update upper-layer callers to depend on accurate, minimal interfaces
- Preserve existing cluster behavior, including controller-driven managed-group semantics and single-node-cluster semantics

## Non-Goals

- Preserving the `pkg/cluster/raftcluster` import path
- Introducing a compatibility facade or temporary wrapper package
- Changing controller placement logic in `pkg/cluster/groupcontroller`
- Changing `multiraft` semantics or business slotting semantics
- Reworking unrelated `internal/*` package boundaries outside the dependency cleanup needed for this split

## Problem Statement

The current `pkg/cluster/raftcluster` package mixes several distinct responsibilities:

- node-local multiraft runtime startup and lifecycle
- node discovery and address resolution
- group routing and leader lookup
- follower-to-leader proposal forwarding
- controller RPC client behavior and operator commands
- managed-group reconciliation execution and readiness checks

That mixture shows up directly in the file layout:

- `cluster.go`, `config.go`, `transport.go` combine node runtime assembly, routing, controller startup, and observation loops
- `router.go`, `discovery.go`, and `static_discovery.go` expose reusable infrastructure that should not live inside a large cluster facade
- `controller_client.go` and `operator.go` implement controller-facing client behavior, not node runtime behavior
- `agent.go`, `managed_groups.go`, `assignment_cache.go`, and `readiness.go` implement local reconcile execution rather than generic cluster access

This causes four concrete problems:

1. package naming is unstable because the package surface is not single-purpose
2. upper layers import a broad cluster facade even when they only need routing or RPC transport
3. `internal/app` hides important lifecycle composition inside `Cluster.Start()` instead of wiring it explicitly
4. tests cluster around one package boundary even though they are verifying different subsystems

This package is now hard to name precisely for the same reason it is hard to reason about: it is carrying too many responsibilities.

## Design Principles

### 1. Package names describe concrete capability

Leaf package names should answer:

> What does this package provide?

Preferred names in this design are:

- `raftnode`
- `grouprouting`
- `nodediscovery`
- `controllerclient`
- `groupreconcile`

These names are more accurate than continuing to stretch `raftcluster` over unrelated concerns.

### 2. Responsibility wins over historical aggregation

If code is reusable outside one facade, it should live in its own package. Discovery and routing are both examples of reusable cluster infrastructure that should not remain coupled to node startup.

### 3. Mechanism words remain explicit when they improve precision

Words such as `raft`, `node`, `controller`, and `reconcile` are kept when they describe the actual mechanism or runtime role.

### 4. Composition belongs in `internal/app`

`internal/app` remains the single composition root. The split should make lifecycle and dependency wiring more explicit there, not recreate a new omnibus cluster facade somewhere else.

### 5. If a caller only needs one capability, expose one capability

Upper layers should depend on minimal interfaces such as routing, propose, RPC mux registration, or authoritative RPC, rather than importing a broad cluster type by default.

## Target Package Structure

```text
pkg/cluster/
  raftnode/            node-local raft runtime startup, lifecycle, proposal, RPC transport
  grouprouting/        group routing and leader/local/peer lookup
  nodediscovery/       node endpoint resolution and static discovery
  controllerclient/    controller leader RPC client and operator commands
  groupreconcile/      assignment cache, agent loop, managed-group execution, readiness
```

The old `pkg/cluster/raftcluster` package is removed entirely after the migration.

## Package Responsibilities

### `pkg/cluster/raftnode`

This package owns node-local multiraft runtime composition and the data-plane RPC path.

Responsibilities:

- node configuration validation and defaults
- transport server, pools, and clients needed by the node runtime
- `multiraft.Runtime` startup and shutdown
- proposal submission
- follower-to-leader forwarding
- RPC mux exposure for upper-layer authoritative RPC handlers
- controller raft service startup when the local node is part of the controller quorum

This package does not own:

- generic node discovery definitions
- reusable group routing policy as a standalone concern
- controller RPC client behavior
- managed-group reconcile execution loops

Recommended public API:

- `type Node`
- `type Config`
- `type SeedGroup`
- `func New(cfg Config) (*Node, error)`
- methods for `Start`, `Stop`, `Propose`, `RPCService`, `RPCMux`, and runtime access needed by reconciliation and upper-layer RPC serving

### `pkg/cluster/grouprouting`

This package owns group routing and leader/local/peer lookup.

Responsibilities:

- key-to-group slot routing
- leader lookup via runtime state
- local-node comparison helpers
- peer lookup for a group, including runtime-observed peer sets when available

This package should be usable independently by code that needs routing but should not know how node startup works.

Recommended public API:

- `type Router`
- `func New(groupCount uint32, localNode multiraft.NodeID, runtime RuntimeView) *Router`
- methods for `SlotForKey`, `LeaderOf`, `IsLocal`, and `PeersForGroup`

`RuntimeView` should be a narrow interface defined in `grouprouting`, not a hard dependency on the full node type.

### `pkg/cluster/nodediscovery`

This package owns node endpoint discovery and address resolution.

Responsibilities:

- discovery interface definition
- node endpoint metadata shape
- static endpoint table implementation

Recommended public API:

- `type Discovery interface`
- `type NodeEndpoint struct`
- `type Static struct`
- `func NewStatic(endpoints []NodeEndpoint) *Static`

This package must stay dependency-light so it can be used by both `raftnode` and `controllerclient`.

### `pkg/cluster/controllerclient`

This package owns RPC communication with the controller leader.

Responsibilities:

- controller RPC request and response encoding/decoding
- leader cache and redirect handling
- retry semantics for controller commands and controller reads
- assignment refresh, node listing, runtime view listing, task fetch, and task result reporting
- operator commands such as drain, resume, transfer, and force reconcile

This package does not own local managed-group execution.

Recommended public API:

- `type Client`
- `func New(...) *Client`
- methods for `Report`, `ListNodes`, `RefreshAssignments`, `ListRuntimeViews`, `GetTask`, `ReportTaskResult`, `MarkNodeDraining`, `ResumeNode`, `ForceReconcile`, `TransferGroupLeader`, and `RecoverGroup`

### `pkg/cluster/groupreconcile`

This package owns local execution of controller-issued desired state.

Responsibilities:

- assignment cache
- periodic heartbeat/report loop behavior for one node
- managed-group open/bootstrap/close behavior
- managed-group membership change execution
- leader transfer and catch-up waits needed by repair and rebalance execution
- readiness checks for managed groups
- local task execution and task result reporting

This package executes controller decisions but does not make placement decisions.

Recommended public API:

- `type Agent`
- `func New(node RuntimeNode, client ControllerClient, ...) *Agent`
- lifecycle methods such as `Start`, `Stop`, `HeartbeatOnce`, `SyncAssignments`, `ApplyAssignments`, and readiness helpers used by app lifecycle checks

## File Mapping

The first-pass file relocation should follow this mapping.

### Move to `pkg/cluster/raftnode`

- `pkg/cluster/raftcluster/cluster.go` -> `pkg/cluster/raftnode/node.go`
- `pkg/cluster/raftcluster/config.go` -> `pkg/cluster/raftnode/config.go`
- `pkg/cluster/raftcluster/transport.go` -> `pkg/cluster/raftnode/transport.go`
- `pkg/cluster/raftcluster/forward.go` -> `pkg/cluster/raftnode/forward.go`
- forwarding-related pieces of `pkg/cluster/raftcluster/codec.go` -> `pkg/cluster/raftnode/forward_codec.go`
- runtime/proposal-facing errors from `pkg/cluster/raftcluster/errors.go` -> `pkg/cluster/raftnode/errors.go`

### Move to `pkg/cluster/grouprouting`

- `pkg/cluster/raftcluster/router.go` -> `pkg/cluster/grouprouting/router.go`
- runtime peer tracking helpers from `pkg/cluster/raftcluster/cluster.go` -> `pkg/cluster/grouprouting/runtime_peers.go`

### Move to `pkg/cluster/nodediscovery`

- `pkg/cluster/raftcluster/discovery.go` -> `pkg/cluster/nodediscovery/discovery.go`
- `pkg/cluster/raftcluster/static_discovery.go` -> `pkg/cluster/nodediscovery/static.go`

### Move to `pkg/cluster/controllerclient`

- `pkg/cluster/raftcluster/controller_client.go` -> `pkg/cluster/controllerclient/client.go`
- `pkg/cluster/raftcluster/operator.go` -> `pkg/cluster/controllerclient/operator.go`
- controller timeout helpers from `pkg/cluster/raftcluster/readiness.go` -> `pkg/cluster/controllerclient/timeouts.go`
- controller redirect/retry-related errors -> `pkg/cluster/controllerclient/errors.go`

### Move to `pkg/cluster/groupreconcile`

- `pkg/cluster/raftcluster/agent.go` -> `pkg/cluster/groupreconcile/agent.go`
- `pkg/cluster/raftcluster/assignment_cache.go` -> `pkg/cluster/groupreconcile/assignment_cache.go`
- `pkg/cluster/raftcluster/managed_groups.go` -> `pkg/cluster/groupreconcile/managed_groups.go`
- readiness and runtime-view helpers from `pkg/cluster/raftcluster/readiness.go` -> `pkg/cluster/groupreconcile/readiness.go`
- managed-group execution test hooks remain with this package because they are execution concerns rather than node concerns

## Type and Name Mapping

The public renames should be explicit and non-compatibility-preserving.

- `raftcluster.Cluster` -> `raftnode.Node`
- `raftcluster.Config` -> `raftnode.Config`
- `raftcluster.NewCluster` -> `raftnode.New`
- `raftcluster.NodeConfig` -> `nodediscovery.NodeEndpoint`
- `raftcluster.GroupConfig` -> `raftnode.SeedGroup`
- `raftcluster.Router` -> `grouprouting.Router`
- `raftcluster.StaticDiscovery` -> `nodediscovery.Static`
- `groupAgent` -> `groupreconcile.Agent`
- `controllerClient` -> `controllerclient.Client`

The naming rationale is consistent throughout:

- `raftnode` describes a node-scoped raft runtime, not an abstract whole cluster
- `controllerclient` describes a controller-facing RPC client, not generic cluster behavior
- `groupreconcile` describes execution responsibility more accurately than `agent`, which is a role but not a responsibility

## Dependency Direction

Dependencies should point in one direction and avoid recreating a broad cluster facade.

### Base packages

- `nodediscovery` depends only on low-level types needed for endpoint metadata
- `grouprouting` depends on `multiraft` and its own narrow runtime view interface

### Node runtime package

- `raftnode` depends on `nodediscovery`, `grouprouting`, `multiraft`, `nodetransport`, `controllerraft`, `groupcontroller`, `controllermeta`, and `raftstorage`
- `raftnode` must not depend on `controllerclient` or `groupreconcile`

### Controller-facing client package

- `controllerclient` depends on `nodediscovery`, `nodetransport`, `controllermeta`, and `groupcontroller` request/response types as needed
- `controllerclient` must not depend on `groupreconcile`
- `controllerclient` should depend on a narrow caller interface for RPC transport rather than the whole node type where possible

### Reconcile package

- `groupreconcile` depends on `raftnode` only through a narrow runtime/control interface needed to execute tasks locally
- `groupreconcile` depends on `controllerclient` for controller reads and commands
- `groupreconcile` must not depend on `internal/app`

### Composition root

- `internal/app` depends on all of the above and is the only layer that wires them together

## Lifecycle and Composition

The current `raftcluster.Cluster.Start()` hides three distinct startup concerns in one method:

- node runtime startup
- controller client readiness
- managed-group observation and reconciliation loop startup

After the split, startup should become explicit in `internal/app`.

Recommended order:

1. build `[]nodediscovery.NodeEndpoint`
2. create `nodediscovery.Static`
3. create `raftnode.Node`
4. start `raftnode.Node`
5. if controller is enabled, create `controllerclient.Client`
6. create `groupreconcile.Agent`
7. start agent observation and reconciliation loops
8. inject only the required capabilities into upper layers

This keeps `internal/app` as the single composition root and makes cluster-side lifecycle easier to reason about and test.

## Upper-Layer Dependency Cleanup

The split is only valuable if callers stop depending on a broad cluster facade.

### `internal/access/node`

`internal/access/node` already defines a local interface. Keep that approach, but update the implementation expectation from `*raftcluster.Cluster` to `*raftnode.Node`.

The access layer should continue to depend only on:

- `RPCMux`
- `LeaderOf`
- `IsLocal`
- `SlotForKey`
- `RPCService`
- `PeersForGroup`

### `pkg/storage/metastore`

`metastore` should stop importing the old cluster package directly and instead depend on a package-local minimal interface such as `ShardRuntime`.

That interface should contain only the methods `metastore` actually uses:

- `SlotForKey`
- `Propose`
- `RPCMux`
- `RPCService`
- `GroupIDs`
- `PeersForGroup`
- `IsLocal`

This keeps distributed metadata APIs bound to the capabilities they require instead of to a historical runtime package.

### `internal/app`

`internal/app` should own separate fields for:

- `*raftnode.Node`
- `*controllerclient.Client` when enabled
- `*groupreconcile.Agent` when enabled

These should no longer be hidden behind one type that tries to represent the whole cluster subsystem.

## Error Ownership

Errors should move with the responsibility that produces them.

- runtime/proposal/forwarding errors belong to `raftnode`
- controller redirect and controller retry classification belong to `controllerclient`
- managed-group task execution and readiness errors belong to `groupreconcile`

Do not recreate a shared omnibus error package just to preserve old names.

## Testing Strategy

Tests should move with the package boundary they validate.

### Unit and package tests

- `router_test.go` moves to `pkg/cluster/grouprouting`
- `static_discovery_test.go` moves to `pkg/cluster/nodediscovery`
- `controller_client_internal_test.go` moves to `pkg/cluster/controllerclient`
- `agent.go`, `managed_groups.go`, and readiness-related tests move to `pkg/cluster/groupreconcile`
- node runtime, forwarding, config, and transport tests move to `pkg/cluster/raftnode`

### Integration tests

Keep a smaller set of end-to-end cluster tests that verify the composed system still works across package boundaries:

- node startup and shutdown
- follower proposal forwarding to leader
- controller-driven assignment refresh and local application
- managed-group repair or rebalance flow

The package-level names in these tests should reflect the new structure rather than continuing to refer to `raftcluster`.

## Migration Strategy

This migration is one-shot rather than compatibility-preserving.

Recommended order:

1. extract `nodediscovery`
2. extract `grouprouting`
3. extract `raftnode`
4. extract `controllerclient`
5. extract `groupreconcile`
6. update `internal/app` wiring
7. update upper-layer callers and tests
8. remove the old `pkg/cluster/raftcluster` directory

Each stage should keep behavior stable, but there is no compatibility wrapper at the end.

## Risks and Mitigations

### Risk: recreating the old facade under a new name

Mitigation:

- keep `raftnode` focused on node runtime and proposal transport only
- do not hide controller client or reconcile agent inside `raftnode.Node`

### Risk: circular dependencies during extraction

Mitigation:

- define narrow interfaces in the consuming package
- keep `grouprouting` and `nodediscovery` dependency-light
- keep `groupreconcile` dependent on narrow node interfaces rather than full composition objects

### Risk: broad import churn in upper layers

Mitigation:

- use minimal local interfaces in callers such as `metastore` and `internal/access/node`
- migrate imports at the same time as package extraction rather than preserving old names temporarily

### Risk: lifecycle regressions after splitting startup logic

Mitigation:

- add or update `internal/app` lifecycle tests to verify node runtime startup, controller client wiring, and reconcile loop startup order explicitly

## Accepted Design Decisions

- The split is aggressive and non-compatible by design
- `raftcluster` is removed rather than retained as a facade
- package names prioritize responsibility over historical continuity
- `internal/app` remains the only composition root
- upper-layer packages are updated to depend on accurate capability interfaces

## Open Questions

None for the design itself. The next step is an implementation plan that sequences the extraction, wiring changes, and test migration.
