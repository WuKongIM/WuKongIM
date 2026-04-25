# Process-Level Black-Box E2E Test Architecture Design

## Summary

This design adds a first-class `test/e2e` layer for `WuKongIM v3.1`.

The first phase stays intentionally small and strict:

- tests live under `test/e2e`
- tests start the real `cmd/wukongim` binary as child processes
- tests interact only through external protocols, not internal app objects
- the first scenario covers one `单节点集群` and one real `WKProto` send flow

The goal is to create a stable black-box foundation that can later grow into
three-node process-level e2e, restart/failover coverage, and finally
compose-backed deployment verification without rewriting the test shape.

## Goals

- Add a dedicated process-level black-box e2e layer under `test/e2e`.
- Keep the e2e boundary strict: no direct `internal/app` orchestration and no
  direct database reads in assertions.
- Start the real `cmd/wukongim` binary with generated `wukongim.conf` files so
  config loading, listener binding, startup ordering, and shutdown behavior are
  part of the test surface.
- Build a reusable e2e harness that manages binary build, config generation,
  port reservation, process lifecycle, readiness checks, and diagnostics.
- Add one first end-to-end send test that proves a fresh `单节点集群` can accept
  two real `WKProto` clients, return `SendAck`, deliver `RecvPacket`, and accept
  `RecvAck`.
- Keep the first phase small enough to run deterministically on a developer
  machine without pulling in Docker or browser automation.

## Non-Goals

- No Docker Compose orchestration in phase 1.
- No browser or `wk-web` e2e in phase 1.
- No three-node process orchestration in phase 1.
- No direct import of `internal/app` from `test/e2e`.
- No direct DB, store, or runtime inspection in e2e assertions.
- No broad chaos framework, restart matrix, or fault injection in phase 1.
- No attempt to replace existing `integration` tests; the new e2e layer is a
  separate black-box tier.

## Existing Context

### Repository support that already exists

- `cmd/wukongim` already supports `-config` and loads the same
  `wukongim.conf` format used by normal deployments.
- The repository already treats one process as a `单节点集群`, so phase 1 can stay
  cluster-first without inventing a local bypass mode.
- `internal/app/integration_test.go` and
  `internal/app/multinode_integration_test.go` already prove real protocol flows
  and provide a reference for expected `WKProto` message semantics.
- Root-level `docker-compose.yml` already exists, which means future deployment
  e2e can grow on top of the same external-facing scenario shape.

### Gaps this design fills

- There is no dedicated `test/e2e` directory for black-box process testing.
- There is no reusable harness that starts the real `wukongim` binary with
  generated config files and temp directories.
- There is no stable readiness contract for process-level tests.
- There is no first black-box regression that validates the full
  `Connect -> Send -> SendAck -> Recv -> RecvAck` path through an actual child
  process.

## Approaches Considered

### 1. Recommended: process-level black-box e2e with real child processes

Create `test/e2e`, build `./cmd/wukongim` once in `TestMain`, then let each test
start real child processes with generated config files and interact only through
external protocols.

Pros:

- keeps the e2e boundary clean and durable
- exercises real startup, config parsing, port binding, and shutdown behavior
- makes later three-node and compose migration straightforward
- avoids coupling the e2e layer to internal wiring details

Cons:

- slower than process-internal integration tests
- requires explicit readiness, cleanup, and log diagnostics support

### 2. Black-box assertions on top of internal bootstrap helpers

Put tests under `test/e2e`, but start servers by calling internal bootstrap code
instead of launching the real binary.

Pros:

- easier to debug
- somewhat faster startup

Cons:

- weakens the black-box boundary immediately
- misses config and process lifecycle failures that only happen in the real
  binary path
- creates long-term coupling between `test/e2e` and internal composition

### 3. Compose-first e2e from the start

Drive phase 1 e2e through Docker Compose instead of local child processes.

Pros:

- closest to packaged deployment
- validates container and network packaging early

Cons:

- slower and noisier for the first iteration
- harder to debug harness issues
- too large a scope for the first black-box foundation

## Recommended Approach

Choose approach 1.

Phase 1 should optimize for a sharp boundary and a small stable surface:

- real binary
- real config files
- real temp data directories
- real network listeners
- real `WKProto` clients
- zero internal state assertions

This gives the repository a reliable black-box base without overcommitting to
three-node process control or deployment packaging in the first change.

## Design

### 1. Directory Layout

Add a dedicated e2e test package:

```text
test/e2e/
  README.md
  e2e_test.go
  suite/
    binary.go
    config.go
    ports.go
    process.go
    readiness.go
    runtime.go
    wkproto_client.go
```

Responsibilities:

- `test/e2e/README.md`
  - explains the purpose and rules of the e2e layer
  - documents how to run, debug, and inspect failures
- `test/e2e/e2e_test.go`
  - contains the first scenario test entry
  - owns `TestMain`
- `test/e2e/suite/binary.go`
  - builds `./cmd/wukongim` once per `go test` process
- `test/e2e/suite/config.go`
  - writes temporary `wukongim.conf` files using the normal `WK_` format
- `test/e2e/suite/ports.go`
  - reserves loopback ports for cluster/API/gateway listeners
- `test/e2e/suite/process.go`
  - starts and stops child processes
  - captures stdout/stderr and exit diagnostics
- `test/e2e/suite/readiness.go`
  - waits for TCP and protocol-level readiness
- `test/e2e/suite/runtime.go`
  - assembles test-scoped suites and node handles
- `test/e2e/suite/wkproto_client.go`
  - provides a black-box `WKProto` client wrapper for connect/send/recv/ack

### 2. Layer Boundaries

The new e2e layer must stay stricter than existing integration tests.

Rules:

- `test/e2e` must not import `internal/app`.
- Test assertions must not read internal stores, runtime state, or DB files.
- Tests may use protocol-facing packages such as `pkg/protocol/frame`,
  `pkg/protocol/codec`, or the existing protocol testkit if they are used only
  as client-side helpers.
- The suite may know process addresses, config paths, log paths, and child
  process state, but not server internals.

This preserves a clear repository testing pyramid:

- `unit`: local logic and state machines
- `integration`: in-process composition and semantic assembly
- `e2e`: external black-box binary behavior

### 3. Core Objects

#### `BinaryCache`

`BinaryCache` is responsible for building the e2e binary once per `go test`
invocation.

It should:

- build `./cmd/wukongim` once in `TestMain`
- write the binary into a temporary location
- return the binary path to individual suites

It should not:

- know about ports
- know about config files
- own per-test cleanup

#### `NodeSpec`

`NodeSpec` is a pure input description for one node instance.

It should hold:

- node ID and name
- temporary data directory
- config file path
- stdout/stderr log file paths
- cluster/API/gateway listen addresses
- phase-specific toggles if needed later

It should not hold running process state.

#### `NodeProcess`

`NodeProcess` is one running `wukongim` child process.

It should hold:

- `exec.Cmd`
- PID
- exit state
- the `NodeSpec`
- handles to stdout/stderr files

It should expose only a small black-box lifecycle:

- `Start()`
- `WaitReady()`
- `Stop()`
- `DumpDiagnostics()`

#### `E2ESuite`

`E2ESuite` is the per-test orchestrator.

It should:

- create the temp directory tree
- allocate ports
- generate config files
- start one or more nodes
- register cleanup
- create black-box clients

Phase 1 only needs:

- `New(t *testing.T)`
- `StartSingleNodeCluster()`
- helpers to access the node's external addresses

Later phases can extend the same suite shape to three-node clusters without
rewriting test intent.

#### `ReadinessProbe`

The readiness component should separate process health from protocol health.

Phase 1 should use two readiness levels:

- `process-ready`: the child process is alive and the gateway listener accepts
  TCP connections
- `protocol-ready`: a real `WKProto` `ConnectPacket` receives a successful
  `ConnackPacket`

Only `protocol-ready` should count as fully ready for the first send scenario.

#### `WKProtoClient`

The e2e client wrapper should be a client-only helper around real `WKProto`
frames.

It should support:

- connect
- send one `SendPacket`
- read one `SendAckPacket`
- read one `RecvPacket`
- send one `RecvackPacket`

It should not grow into a general scenario engine in phase 1.

### 4. Process Lifecycle

#### Binary build

`TestMain` should build `./cmd/wukongim` once:

```bash
go build -o <tmp>/wukongim-e2e ./cmd/wukongim
```

This avoids repeated compilation and keeps each test focused on runtime
behavior.

#### Per-test runtime tree

Each suite should use its own `t.TempDir()` and create a node-specific tree such
as:

```text
<temp>/
  node-1/
    wukongim.conf
    data/
    stdout.log
    stderr.log
```

This keeps all process artifacts together and makes failure inspection easy.

#### Start

Each node should start with:

```bash
<binary> -config <path/to/wukongim.conf>
```

Stdout and stderr should be redirected into per-node files instead of being
mixed into the test runner output.

#### Ready

The suite should first wait for the process to stay alive and the gateway port
to accept TCP connections, then perform a real protocol handshake and require a
successful `ConnackPacket`.

This intentionally replaces blind sleeps with an external client observable
contract.

#### Stop

Shutdown should be standardized:

1. send `SIGTERM`
2. wait for a short timeout
3. if needed, force kill
4. include exit information and log paths in failure diagnostics

This same stop contract can later support restart and failover scenarios.

### 5. Port Allocation and Retry Strategy

The suite should reserve loopback addresses for:

- cluster listen address
- gateway `tcp-wkproto` listener
- optional API listen address if phase 1 enables it

The harness should not assume that a reserved port can never be stolen between
reservation and process start.

Phase 1 should therefore allow one startup retry if the child process exits with
an obvious address-in-use failure:

- allocate a fresh port set
- rewrite config
- restart once

Anything more complex is unnecessary in the first version.

### 6. Config Generation

Phase 1 must use the normal `wukongim.conf` format with `WK_` keys.

At minimum, the generated config for the `单节点集群` should include:

- `WK_NODE_ID`
- `WK_NODE_NAME`
- `WK_NODE_DATA_DIR`
- `WK_CLUSTER_LISTEN_ADDR`
- `WK_CLUSTER_SLOT_COUNT=1`
- `WK_CLUSTER_NODES=[{"id":1,"addr":"..."}]`
- `WK_GATEWAY_LISTENERS=[...]`

Optional config values such as API listen address can be included only if they
support the readiness contract or debugging story.

The harness must not invent a test-only configuration format, because that
would weaken the exact startup path the e2e layer is meant to validate.

### 7. First Scenario: send one person message in a fresh single-node cluster

Add the first phase-1 scenario:

- `TestE2E_WKProtoSendMessage_SingleNodeCluster`

The scenario should:

1. start a fresh real `单节点集群`
2. connect a sender `WKProto` client as `u1`
3. connect a recipient `WKProto` client as `u2`
4. send one person message from `u1` to `u2`
5. assert the sender receives `SendAckPacket`
6. assert the recipient receives `RecvPacket`
7. send `RecvackPacket`

Important details:

- do not pre-seed runtime metadata through internal helpers
- do not read DB files afterward
- treat the scenario as a full external regression, not as a convenience smoke
  test

#### Expected assertions

For `SendAckPacket`:

- `ReasonCode == frame.ReasonSuccess`
- `MessageID != 0`
- `MessageSeq != 0`

For `RecvPacket`:

- `FromUID == "u1"`
- `Payload == []byte("hello from e2e")`
- `MessageID` equals the acked message ID
- `MessageSeq` equals the acked message sequence
- `ChannelType == frame.ChannelTypePerson`
- `ChannelID == "u1"` to match current person-channel delivery behavior

The recipient should then send:

- `RecvackPacket{MessageID: ..., MessageSeq: ...}`

Phase 1 does not need to assert internal ack cleanup state; that belongs in
integration tests.

### 8. Diagnostics

When startup or scenario execution fails, diagnostics should prioritize
developer usefulness over minimal output.

The suite should report:

- binary path
- config path
- node addresses
- process exit code or signal
- stdout/stderr log paths
- the tail of stdout/stderr when practical

This keeps process-level flake investigation manageable.

### 9. Build Tags and Execution Matrix

Introduce a dedicated build tag for the new layer:

- `//go:build e2e`

Recommended execution model:

- default local development:
  - `go test ./...`
  - does not run e2e
- focused e2e:
  - `go test -tags=e2e ./test/e2e/... -count=1`
- integration:
  - continues to use `//go:build integration`

Initial CI recommendation:

- PR: optional/manual e2e job while the harness matures
- nightly: run the focused `e2e` package
- release or risky refactor branch: run `integration + e2e`

This avoids making the repository slower for every normal edit before the new
layer is proven stable.

### 10. Evolution Plan

Once the phase-1 foundation is stable, extend the same harness in this order:

1. add three-node process-level suite startup
2. add three-node `WKProto` send coverage
3. add restart and leader change scenarios
4. add API and manager black-box scenarios
5. add deployment-level `docker compose` smoke e2e that reuses the same
   scenario contracts

This ordering protects the first step from turning into a large testing
framework rewrite.

## Risks and Mitigations

### Risk: flaky startup due to timing assumptions

Mitigation:

- use protocol-level readiness instead of sleeps
- keep one bounded retry for address conflicts

### Risk: accidental drift back toward gray-box tests

Mitigation:

- forbid `internal/app` imports in `test/e2e`
- document the rule in `test/e2e/README.md`

### Risk: first scenario grows too broad

Mitigation:

- keep phase 1 to one fresh `单节点集群` send flow only
- defer three-node and persistence assertions to later phases

### Risk: poor failure debugging experience

Mitigation:

- standardize stdout/stderr capture
- print config and log locations on failure

## Files Expected In The First Implementation Plan

The first implementation plan should assume the following files:

- Create: `test/e2e/README.md`
- Create: `test/e2e/e2e_test.go`
- Create: `test/e2e/suite/binary.go`
- Create: `test/e2e/suite/config.go`
- Create: `test/e2e/suite/ports.go`
- Create: `test/e2e/suite/process.go`
- Create: `test/e2e/suite/readiness.go`
- Create: `test/e2e/suite/runtime.go`
- Create: `test/e2e/suite/wkproto_client.go`

The first implementation plan should not require production runtime changes
unless the black-box test exposes a real startup or first-send defect.
