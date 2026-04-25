# E2E Scenario Directory Architecture Design

## Summary

This design refactors `test/e2e` from a single top-level test package with a
growing `e2e_test.go` into a domain-first, scenario-directory architecture.

The new structure optimizes for three things:

- adding a new black-box e2e scenario should be a copy-and-fill operation
- readers should be able to discover the full e2e inventory from the directory
  tree and `README` files
- shared process/WKProto/manager harness code should remain centralized in
  `test/e2e/suite`, not re-implemented inside every scenario

The recommended shape is:

- `test/e2e/suite/` for stable reusable black-box infrastructure
- `test/e2e/<domain>/README.md` for domain-level indexing
- `test/e2e/<domain>/<scenario>/README.md` +
  `test/e2e/<domain>/<scenario>/<scenario>_test.go` for each scenario

This keeps the strict process-level black-box boundary already established by
the current e2e layer while making the suite scalable as more scenarios are
added.

## Goals

- Remove the long-term pressure to place every e2e scenario in
  `test/e2e/e2e_test.go`.
- Make the repository layout itself answer "what e2e tests exist?".
- Make it easy to add one new scenario without editing unrelated test files.
- Preserve the current black-box contract:
  - real `cmd/wukongim` child processes
  - real config files
  - real external protocols and manager APIs
  - no `internal/app` gray-box orchestration from `test/e2e`
- Keep `test/e2e/suite` as the single home for common process-level harness
  code.
- Support focused execution:
  - all e2e tests
  - one domain
  - one scenario package
- Keep failure diagnostics bounded and discoverable.

## Non-Goals

- No change to the strict black-box boundary of `test/e2e`.
- No replacement of existing `suite` helpers with per-scenario copies.
- No introduction of `internal/app` orchestration into e2e.
- No attempt to solve every future performance problem in the first directory
  refactor.
- No broad redesign of scenario semantics; this design reorganizes how e2e
  tests are structured and discovered.

## Existing Context

### Current strengths

- `test/e2e/suite/` already contains reusable process-level helpers for:
  - building the real binary
  - generating config files
  - reserving ports
  - starting/stopping processes
  - readiness checks
  - manager queries
  - WKProto client behavior
- `test/e2e/README.md` already documents the black-box rules.
- The current top-level tests already prove both:
  - one `单节点集群` send path
  - one three-node cross-node path

### Current scaling problem

- Scenario tests currently accumulate in `test/e2e/e2e_test.go`.
- Adding more scenarios will make one file mix unrelated concerns such as:
  - single-node happy paths
  - cross-node delivery
  - failover
  - restart
  - auth and gateway cases
- The directory tree does not clearly show:
  - what domains are covered
  - which scenario directories exist
  - where to place a new scenario

### Go-specific constraint

When tests move into subdirectories, each scenario becomes its own Go test
package. A root `TestMain` in `test/e2e` does not automatically serve
`./test/e2e/...`. The architecture therefore must not depend on a single
top-level `TestMain` to make new scenario packages work.

## Approaches Considered

### 1. Split the top-level package into more `*_test.go` files

Example:

```text
test/e2e/
  message_test.go
  cluster_test.go
  gateway_test.go
```

Pros:

- smallest immediate diff
- no new package boundaries
- no harness change required

Cons:

- still trends toward large flat files
- scenario-local `README` files do not fit naturally
- weak directory-level discoverability
- new scenarios still require choosing among shared top-level files

### 2. Recommended: domain directories with scenario subdirectories

Example:

```text
test/e2e/
  suite/
  message/
    README.md
    cross_node_closure/
      README.md
      cross_node_closure_test.go
    slot_leader_failover/
      README.md
      slot_leader_failover_test.go
```

Pros:

- domain and scenario boundaries are explicit
- directory tree doubles as a test catalog
- each scenario can carry its own README and local helpers
- adding a scenario becomes a low-friction copy-and-fill workflow
- `suite/` remains stable and clean

Cons:

- requires moving away from a single root `TestMain`
- introduces more packages and more directories

### 3. Organize by runtime shape instead of domain

Example:

```text
test/e2e/
  single_node/
  three_node/
  failover/
```

Pros:

- deployment shape is obvious

Cons:

- business capabilities become scattered
- "what message tests exist?" becomes harder to answer
- long-term maintenance follows topology instead of product behavior

## Recommended Approach

Choose approach 2.

The repository should treat `test/e2e` as a catalog of black-box scenarios,
organized first by capability domain and then by scenario directory.

The design principles are:

- path names should explain the scenario before the file is opened
- each scenario directory should own its purpose and its local explanation
- `suite/` should be the only place for shared black-box infrastructure
- adding a scenario should not require touching a giant central test file

## Design

### 1. Target Directory Layout

```text
test/e2e/
  README.md
  AGENTS.md
  suite/
    binary.go
    config.go
    manager_client.go
    ports.go
    process.go
    readiness.go
    runtime.go
    wkproto_client.go
    ...

  message/
    README.md
    single_node_send_message/
      README.md
      single_node_send_message_test.go
    cross_node_closure/
      README.md
      cross_node_closure_test.go
    slot_leader_failover/
      README.md
      slot_leader_failover_test.go

  gateway/
    README.md
    ...

  cluster/
    README.md
    ...
```

Rules:

- root `test/e2e/` contains:
  - shared docs
  - `suite/`
  - domain directories
- domain directories contain:
  - one domain `README.md`
  - many scenario subdirectories
- scenario directories contain:
  - one scenario `README.md`
  - one primary `*_test.go`
  - optional scenario-local helper files only when truly needed

### 2. Package Boundary and Ownership

#### `test/e2e/suite`

`suite/` remains the stable shared infrastructure package.

It owns:

- binary build/cache behavior
- test workspace creation
- node and cluster startup helpers
- loopback port reservation
- process lifecycle and diagnostics
- readiness waiting
- manager-facing helper queries
- WKProto client behavior

It must not own:

- scenario-specific business assertions
- scenario-specific README content
- one-off helper logic that only one scenario needs

#### Scenario packages

Each scenario subdirectory is its own Go package.

It owns:

- the scenario orchestration itself
- scenario-local helper functions
- scenario-local naming and assertions
- its own scenario README

It must depend only on:

- `test/e2e/suite`
- protocol-facing packages already acceptable at the black-box boundary

Scenario packages must not import each other.

### 3. Binary Build and Test Entry Strategy

The previous root-level `TestMain` does not scale across scenario subpackages.
The shared harness therefore must provide a package-independent entry point.

Target usage:

```go
func TestScenarioName(t *testing.T) {
    s := suite.New(t)
    cluster := s.StartThreeNodeCluster()
    // scenario body
}
```

Design requirements:

- `suite.New(t)` should not require a package-local `TestMain`
- scenario authors should not manually wire binary paths
- the binary cache should be hidden behind the shared harness API

Implementation guidance:

- move binary-path resolution behind `suite.New(t)` or another internal
  `suite` helper
- keep the cache package-private so new scenario packages do not need to know
  how the binary is built
- optimize for correctness and ergonomics first; build reuse across scenario
  packages can evolve behind `suite/` without changing scenario code

### 4. Naming Conventions

#### Domain directories

- use concise capability names such as:
  - `message`
  - `gateway`
  - `cluster`

#### Scenario directories

- use `snake_case`
- directory name should state the behavior under test, for example:
  - `single_node_send_message`
  - `cross_node_closure`
  - `slot_leader_failover`

#### Test files

- primary test file name: `<scenario>_test.go`
- optional local support files:
  - `helpers_test.go`
  - `assertions_test.go`
  - `fixtures_test.go`

These support files remain inside the scenario directory and do not become
shared harness code by default.

### 5. Documentation and Discoverability

#### Root README

`test/e2e/README.md` should remain the entry point for the whole layer.

It should contain:

- the black-box rules
- how to run all e2e tests
- a domain table showing:
  - domain
  - scenario directory
  - purpose
  - single-package run command

#### Domain README

Each domain directory gets a lightweight `README.md` that lists:

- the domain purpose
- the scenarios in that domain
- the expected style of new scenarios in that domain

#### Scenario README

Every scenario directory gets its own `README.md`.

Required sections:

- scenario purpose
- cluster shape (`单节点集群`, three-node cluster, failover, etc.)
- core external steps
- expected observable outcome
- main diagnostics to inspect on failure
- exact single-package run command

This makes the directory tree itself a browsable e2e inventory.

### 6. Scenario Authoring Contract

Adding a new scenario should follow a repeatable template:

1. choose a domain
2. create `test/e2e/<domain>/<scenario>/`
3. add:
   - `README.md`
   - `<scenario>_test.go`
4. use `suite.New(t)` and existing `suite/` helpers
5. keep any one-off helper local unless a second scenario needs it
6. update:
   - domain `README.md`
   - root `test/e2e/README.md`

This turns "add one e2e test" into a predictable, low-friction workflow.

### 7. Failure Diagnostics

The current bounded diagnostics model should remain unchanged:

- config path
- stdout/stderr
- node app/error log tails
- manager slot bodies
- manager connection observations

The structural refactor must keep diagnostics inside `suite/` so every scenario
inherits the same failure reporting quality without reimplementing it.

### 8. Migration Plan

The migration should happen in small, low-risk steps.

#### Step 1: decouple scenarios from root `TestMain`

- move binary resolution behind `suite.New(t)` or equivalent
- allow scenario packages to work without local binary wiring

#### Step 2: create the first domain structure

- create `test/e2e/message/README.md`
- move existing message-related scenarios from `test/e2e/e2e_test.go` into:
  - `test/e2e/message/single_node_send_message/`
  - `test/e2e/message/cross_node_closure/`
  - `test/e2e/message/slot_leader_failover/`

#### Step 3: add documentation

- rewrite `test/e2e/README.md` as the global catalog
- add per-domain README files
- add per-scenario README files

#### Step 4: remove the central scenario file

- once the migrated scenarios are stable, remove `test/e2e/e2e_test.go`
- keep the root directory as an index and shared harness entry point only

### 9. Testing Strategy

The structural refactor should be verified at three levels:

#### Harness tests

- keep and extend `test/e2e/suite/*_test.go`
- cover any new binary-cache and suite-construction behavior

#### Scenario package execution

- run each migrated scenario package directly, for example:
  - `go test -tags=e2e ./test/e2e/message/single_node_send_message -count=1`
  - `go test -tags=e2e ./test/e2e/message/cross_node_closure -count=1`
  - `go test -tags=e2e ./test/e2e/message/slot_leader_failover -count=1`

#### Full inventory execution

- run:
  - `go test -tags=e2e ./test/e2e/... -count=1`

This confirms that:

- the tree is discoverable
- per-scenario packages are runnable
- the shared harness still works across the full e2e layer

## Risks and Mitigations

### Risk: scenario packages rebuild the binary too often

Mitigation:

- hide build/cache behavior inside `suite/`
- start with the simplest correct cache behavior
- optimize behind the same `suite` API if aggregate e2e runtime becomes a
  problem

### Risk: too much code migrates out of `suite/`

Mitigation:

- keep a strict rule that only multi-scenario reusable black-box helpers belong
  in `suite/`
- keep single-scenario helpers local

### Risk: documentation drifts from reality

Mitigation:

- require README updates whenever a scenario is added, removed, or renamed
- keep the inventory tables short and path-based

## Open Questions

- Whether a future optimization should add a repository-wide shared binary cache
  across scenario packages instead of only package-local caching behind
  `suite/`
- Whether certain larger domains should eventually gain a domain-local helper
  subpackage in addition to `suite/`; the default in this design is "no" until
  repeated duplication appears

## Recommendation

Refactor `test/e2e` to:

- keep `suite/` as stable shared infrastructure
- organize tests by domain then scenario directory
- make each scenario directory self-describing with a README
- remove the dependency on one central `e2e_test.go`

This gives the repository an e2e architecture that is easier to grow, easier to
read, and easier to maintain without weakening the current black-box boundary.
