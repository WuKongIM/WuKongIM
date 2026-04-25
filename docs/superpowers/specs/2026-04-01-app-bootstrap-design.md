# App Bootstrap Design

## Overview

Add `internal/app` as the process-level composition boundary for the repository.

The project already has clear module seams for transport (`internal/gateway`), business ingress (`internal/service`), distributed coordination (`pkg/wkcluster`), distributed storage APIs (`pkg/wkstore`), local metadata storage (`pkg/wkdb`), and raft persistence (`pkg/raftstore`). What is still missing is the runtime assembly layer that wires these modules together into a startable clustered node without collapsing their boundaries.

The first version should be cluster-first. A node should start its local storage, create the multiraft-backed cluster runtime, create the business service layer on top of the store and cluster-facing ports, then expose external listeners through the gateway. The composition logic should live in one place and should not leak into `cmd`, `gateway`, or `service`.

## Goals

- Introduce a single process composition module under `internal/app`
- Keep `cmd` thin: parse raw config, create app, start, wait for signal, stop
- Make startup order, shutdown order, and partial-failure rollback explicit
- Keep existing module boundaries intact while connecting them into a runnable clustered node
- Expose read-only accessors for key runtime components so later modules such as `api`, `metrics`, and background tasks can integrate without re-owning bootstrap logic

## Non-Goals

- Implementing a full public server API in this change
- Adding dependency injection containers, plugin registries, or event buses
- Moving business logic from `internal/service` into `internal/app`
- Teaching `cmd` to build runtime closures such as `NewStorage` or `NewStateMachine`
- Solving cross-node message delivery semantics beyond defining the correct assembly seam

## Existing Constraints

- There is currently no production `main` package or top-level process bootstrap in the repository.
- `internal/gateway` already exposes a clean `Handler` boundary and should remain unaware of cluster or storage details.
- `internal/service` already acts as the business-layer ingress above the gateway and currently owns local session registry and local person-send flow.
- `pkg/wkcluster` requires runtime-provided storage and state machine factories:
  - `NewStorage(groupID)` for raft persistence
  - `NewStateMachine(groupID)` for business apply path
- `pkg/wkstore` already provides the correct state machine factory and business storage wrapper once a `wkdb.DB` and `wkcluster.Cluster` exist.
- `pkg/wkdb` and `pkg/raftstore` are node-local resources and must be opened before cluster startup.

These constraints imply that the bootstrap layer must translate high-level node configuration into concrete runtime factories, but must do so internally rather than pushing low-level composition into callers.

## Approaches Considered

### 1. Recommended: Introduce `internal/app`

Create a focused composition module that owns config normalization, dependency construction, lifecycle orchestration, and runtime accessors.

Pros:

- creates one stable process-level boundary
- keeps `cmd` small and disposable
- avoids pushing assembly knowledge into business and transport modules
- provides a natural home for future `api`, `tasks`, and observability modules

Cons:

- adds one more package before there is a full binary
- requires a new config model above existing package configs

### 2. Introduce `internal/server`

Create a runtime-oriented container focused on daemon semantics.

Pros:

- reads naturally once the repository grows background jobs, management endpoints, and richer runtime hooks

Cons:

- encourages mixing lifecycle concerns with future business runtime concerns too early
- does not buy more clarity than `internal/app` at the current stage

### 3. Compose everything directly in `cmd/wukongim/main.go`

Build all dependencies in the entrypoint.

Pros:

- shortest path to a runnable binary

Cons:

- assembly logic quickly becomes the new coupling hotspot
- makes later reuse in tests and additional binaries awkward
- weakens the clean boundaries already established elsewhere in the repository

## Recommended Approach

Choose approach 1 and add `internal/app` as a thin but strict composition layer.

Dependency direction should be:

- `cmd -> internal/app`
- `internal/app -> gateway/service/wkstore/wkcluster/wkdb/raftstore`
- `internal/service -> business ports backed by wkstore/cluster`
- `internal/gateway -> service.Handler`

The reverse must remain forbidden:

- `gateway` must not know about `wkcluster`
- `wkcluster` must not know about `gateway`
- `cmd` must not directly construct business/runtime closures
- `service` must not open storage or start networking

## Architecture

`internal/app` should own four responsibilities.

### 1. Config Boundary

`cmd` parses raw configuration from flags, files, or environment and converts it into a structured `app.Config`.

`app.Config` should be the highest-level runtime config model used inside the process. It should be explicit and stable, and should not include low-level construction closures.

Suggested shape:

```go
type Config struct {
    Node    NodeConfig
    Storage StorageConfig
    Cluster ClusterConfig
    Gateway GatewayConfig
}
```

Responsibility split:

- `cmd`:
  - parse raw config
  - handle CLI/env/file concerns
  - call `app.New(cfg)`
- `internal/app`:
  - validate config relationships
  - derive default paths and package configs
  - translate config into concrete runtime dependencies

### 2. Runtime Construction

`internal/app` should build the runtime in a fixed sequence:

1. open `wkdb`
2. open `raftstore`
3. derive `wkcluster.Config`
4. create `wkcluster.Cluster`
5. create `wkstore.Store`
6. create `service.Service`
7. create `gateway.Gateway`

Important rule: `cmd` passes intent, `app` creates objects.

That means `cmd` should not supply:

- `wkcluster.Config.NewStorage`
- `wkcluster.Config.NewStateMachine`
- already-constructed `wkdb.DB`, `raftstore.DB`, `wkcluster.Cluster`, or `gateway.Gateway`

Instead, `app` should derive:

```go
clusterCfg := wkcluster.Config{
    NodeID:     cfg.Cluster.NodeID,
    ListenAddr: cfg.Cluster.ListenAddr,
    GroupCount: cfg.Cluster.GroupCount,
    NewStorage: func(groupID multiraft.GroupID) (multiraft.Storage, error) {
        return raftDB.ForGroup(uint64(groupID)), nil
    },
    NewStateMachine: wkstore.NewStateMachineFactory(db),
    Nodes:  ...,
    Groups: ...,
}
```

This keeps low-level runtime wiring in one place.

### 3. Lifecycle Orchestration

`internal/app` should make startup and shutdown order explicit.

Recommended startup order:

1. open local storage (`wkdb`, `raftstore`)
2. start cluster runtime (`wkcluster`)
3. construct service layer (`wkstore`, `service`)
4. start ingress (`gateway`)

Rationale:

- local persistence must exist before cluster starts
- cluster must be ready before business ingress accepts traffic
- gateway must start last because external traffic should only enter after the node can actually serve requests

Recommended shutdown order:

1. stop `gateway`
2. stop service-owned background work when it exists
3. stop `cluster`
4. close `raftstore`
5. close `wkdb`

Rationale:

- stop new traffic first
- then stop internal coordination
- then release local storage

Partial-failure rule:

- if startup fails at any step, `app` must roll back every previously-started resource in reverse order
- this rollback logic must live in `internal/app`, not in `cmd`

### 4. Runtime Accessors

`App` should expose read-only accessors for core components:

```go
type App struct {
    cfg     Config

    db      *wkdb.DB
    raftDB  *raftstore.DB
    cluster *wkcluster.Cluster
    store   *wkstore.Store
    service *service.Service
    gateway *gateway.Gateway
}

func (a *App) DB() *wkdb.DB
func (a *App) Cluster() *wkcluster.Cluster
func (a *App) Store() *wkstore.Store
func (a *App) Service() *service.Service
func (a *App) Gateway() *gateway.Gateway
```

These accessors exist for extension, not ownership transfer. External modules may consume them, but `App` remains the owner of startup and shutdown.

## Proposed Package Layout

```text
cmd/wukongim/
  main.go

internal/app/
  app.go
  config.go
  build.go
  lifecycle.go
  errors.go
```

Responsibilities:

- `cmd/wukongim/main.go`
  - parse raw config
  - call `app.New`
  - start app
  - wait for signal
  - stop app
- `internal/app/config.go`
  - high-level config structs
  - validation and defaulting
  - translation into package configs
- `internal/app/build.go`
  - create runtime components
  - connect package dependencies
- `internal/app/lifecycle.go`
  - startup sequence
  - rollback
  - shutdown sequencing and idempotence
- `internal/app/app.go`
  - `App` type
  - constructor
  - accessors

## Public Interface

The first version should stay small:

```go
func New(cfg Config) (*App, error)
func (a *App) Start() error
func (a *App) Stop() error
```

Guidelines:

- `New` validates and constructs, but does not open listeners
- `Start` performs runtime activation
- `Stop` is safe to call multiple times

This gives tests and callers precise control over construction and lifecycle without collapsing them into a single `Run(ctx)` convenience method too early.

## Config Design

The high-level config model should be split by ownership rather than package internals.

### `NodeConfig`

Holds process identity and directory layout inputs such as:

- node ID
- node name, if needed
- base data directory

### `StorageConfig`

Holds local persistence settings:

- `wkdb` path
- `raftstore` path

`app` may derive these from a common data directory if explicit paths are omitted.

### `ClusterConfig`

Holds distributed runtime settings:

- listen address
- node list
- group list
- raft timing and worker counts
- forward and dial timeouts

This config should map cleanly into `wkcluster.Config` while keeping runtime closures internal to `app`.

### `GatewayConfig`

Holds ingress settings:

- listener definitions
- session defaults
- auth options

This config should map into `gateway.Options` after `service` has already been created, because the gateway handler must be the service instance.

## Assembly Boundaries

The bootstrap design should preserve ownership boundaries.

### `wkdb`

- node-local metadata persistence
- opened and closed by `app`
- never opened by `service`

### `raftstore`

- node-local raft persistence
- opened and closed by `app`
- only adapted into `wkcluster.NewStorage`

### `wkcluster`

- owns raft runtime, transport, and slot routing
- started and stopped by `app`
- exposed to upper layers only through the cluster instance or narrower ports

### `wkstore`

- business-level distributed storage facade
- created after cluster exists
- injected into service-owned ports as needed

### `service`

- business ingress layer above gateway
- remains the `gateway.Handler`
- should consume stores and cluster-facing business ports, not raw bootstrap config

### `gateway`

- ingress boundary
- created last
- started last
- stopped first

## Error Handling

The bootstrap layer should define clear error classes:

- invalid config
- build failure
- start failure
- stop failure

Guidelines:

- config errors should be returned from `New`
- startup errors should be returned from `Start`
- rollback errors should be joined or wrapped so the original startup failure remains visible
- shutdown should attempt best-effort cleanup even if one component returns an error

## Testing Strategy

Testing should focus on composition correctness rather than re-testing lower layers.

### Unit Tests

Add focused tests around:

- config validation and defaults
- derived storage paths
- startup order
- rollback order on intermediate failure
- stop idempotence

### Integration Tests

Add a first app-level integration test that:

- creates temp dirs
- starts an `App`
- verifies cluster starts successfully
- verifies gateway listener is available
- shuts down cleanly

This test should not replace existing gateway or cluster tests; it should only prove that the modules are correctly assembled into one process.

## Future Extensions

This design intentionally leaves room for additional process modules to attach to `App` without reworking startup:

- `internal/api`
- `internal/tasks`
- `internal/metrics`
- `internal/admin`

These modules should become app-owned collaborators, not independent bootstrap owners.

When those modules are introduced, they should follow the same rule:

- constructed by `app`
- started after their dependencies are ready
- stopped before their dependencies are torn down

## Migration Notes

The first implementation should stay conservative:

- add `internal/app`
- add `cmd/wukongim/main.go`
- keep `service` as the gateway handler
- inject `wkstore` and cluster-facing collaborators through `service.Options`
- avoid introducing a generic runtime framework

If later work needs a single-node cluster bootstrap path, it should be modeled as an additional configuration pathway inside `app.Config`, not as a separate bootstrap architecture.
