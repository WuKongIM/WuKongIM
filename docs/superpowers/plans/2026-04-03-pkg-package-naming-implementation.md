# Pkg Package Naming Implementation Plan

**Status:** Completed on branch `pkg-package-naming`

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename `pkg/` packages to the approved role-based names, split `wkstore` into `metastore` and `metafsm`, and update all imports/tests/docs without changing runtime behavior.

**Architecture:** This is a repo-wide structural refactor executed in dependency order. Move low-level packages first (`replication`, `transport`, `protocol`, storage primitives), then split `wkstore`, then rename cluster and channel-log facades, and finally clean repository vocabulary and verify the whole tree. Preserve behavior, exported APIs, and test coverage; only package boundaries, package identifiers, and import names should change.

**Tech Stack:** Go 1.23, standard library file moves, `go test`, `gofmt`, existing docs under `docs/superpowers/*`

---

## Scope Check

This refactor touches multiple package families, but it should stay as one implementation plan because the import rewrites overlap heavily in the same call sites:

- `internal/app/*`
- `internal/access/*`
- `internal/gateway/*`
- `internal/usecase/message/*`
- cross-package tests under `pkg/*`

Splitting this into independent implementation plans would create conflicting import-path migrations and duplicate verification work.

## File Structure

### New package directories

- Create: `pkg/replication/isr`
  Responsibility: single-group ISR replication runtime; package name stays `isr`.
- Create: `pkg/replication/isrnode`
  Responsibility: node-local runtime for many ISR groups; package name becomes `isrnode`.
- Create: `pkg/replication/multiraft`
  Responsibility: multi-group raft runtime; package name stays `multiraft`.
- Create: `pkg/transport/nodetransport`
  Responsibility: node-to-node TCP/RPC transport; package name becomes `nodetransport`.
- Create: `pkg/protocol/wkframe`
  Responsibility: WuKong frame/object model; package name becomes `wkframe`.
- Create: `pkg/protocol/wkcodec`
  Responsibility: WuKong binary frame codec; package name becomes `wkcodec`.
- Create: `pkg/protocol/wkjsonrpc`
  Responsibility: WuKong JSON-RPC schema and frame bridge; package name becomes `wkjsonrpc`.
- Create: `pkg/storage/metadb`
  Responsibility: user/channel metadata DB and slot snapshot storage; package name becomes `metadb`.
- Create: `pkg/storage/raftstorage`
  Responsibility: `multiraft.Storage` adapters over memory and Pebble; package name becomes `raftstorage`.
- Create: `pkg/storage/metastore`
  Responsibility: distributed metadata facade used by upper layers; package name becomes `metastore`.
- Create: `pkg/storage/metafsm`
  Responsibility: metadata raft state machine and command codec; package name becomes `metafsm`.
- Create: `pkg/storage/channellog`
  Responsibility: channel-oriented append/fetch/idempotency/status facade; package name becomes `channellog`.
- Create: `pkg/cluster/raftcluster`
  Responsibility: raft-backed cluster runtime, routing, forwarding, and discovery; package name becomes `raftcluster`.
- Create: `pkg/util/uuidutil`
  Responsibility: UUID helper wrapper if the package is retained; package name becomes `uuidutil`.

### Existing directories to remove after migration

- Remove: `pkg/consensus/isr`
- Remove: `pkg/consensus/multiisr`
- Remove: `pkg/consensus/multiraft`
- Remove: `pkg/wktransport`
- Remove: `pkg/proto/wkpacket`
- Remove: `pkg/proto/wkproto`
- Remove: `pkg/proto/jsonrpc`
- Remove: `pkg/controller/wkdb`
- Remove: `pkg/controller/raftstore`
- Remove: `pkg/controller/wkstore`
- Remove: `pkg/msgstore/channelcluster`
- Remove: `pkg/controller/wkcluster`
- Remove: `pkg/wkutil`
- Remove empty parent directories: `pkg/consensus`, `pkg/controller`, `pkg/proto`, `pkg/msgstore`

### Split boundary for `wkstore`

- Move to `pkg/storage/metastore`:
  - `pkg/controller/wkstore/store.go`
  - `pkg/controller/wkstore/integration_test.go`
- Move to `pkg/storage/metafsm`:
  - `pkg/controller/wkstore/command.go`
  - `pkg/controller/wkstore/statemachine.go`
  - `pkg/controller/wkstore/state_machine_test.go`
  - `pkg/controller/wkstore/benchmark_test.go`
  - `pkg/controller/wkstore/fsm_stress_test.go`
- Split test helpers:
  - create `pkg/storage/metastore/testutil_test.go` for runtime/store helpers
  - create `pkg/storage/metafsm/testutil_test.go` for state-machine helpers
  - retire `pkg/controller/wkstore/testutil_test.go`

## Implementation Notes

- Follow `@superpowers:test-driven-development` task-by-task even though this is structural work.
- Use `git mv` for package moves so history stays readable.
- For path-only renames, keep package identifiers stable where that preserves clarity:
  - `isr`
  - `multiraft`
- For leaf-package renames, update both directory name and `package` clause:
  - `multiisr` -> `isrnode`
  - `wktransport` -> `nodetransport`
  - `wkpacket` -> `wkframe`
  - `wkproto` -> `wkcodec`
  - `jsonrpc` -> `wkjsonrpc`
  - `wkdb` -> `metadb`
  - `raftstore` -> `raftstorage`
  - `wkcluster` -> `raftcluster`
  - `wkutil` -> `uuidutil`
- After each task:
  1. run `gofmt -w` on touched Go files
  2. run the targeted tests listed in that task
  3. make a focused commit before starting the next task
- Keep runtime behavior unchanged. Rename package comments and import aliases, but do not redesign APIs inside the same task unless the approved split requires it.

## Task 1: Move `pkg/consensus/isr` to `pkg/replication/isr`

**Files:**
- Move: `pkg/consensus/isr/api_test.go`
- Move: `pkg/consensus/isr/append.go`
- Move: `pkg/consensus/isr/append_test.go`
- Move: `pkg/consensus/isr/benchmark_test.go`
- Move: `pkg/consensus/isr/doc.go`
- Move: `pkg/consensus/isr/errors.go`
- Move: `pkg/consensus/isr/fetch.go`
- Move: `pkg/consensus/isr/fetch_test.go`
- Move: `pkg/consensus/isr/meta.go`
- Move: `pkg/consensus/isr/meta_test.go`
- Move: `pkg/consensus/isr/progress.go`
- Move: `pkg/consensus/isr/recovery.go`
- Move: `pkg/consensus/isr/recovery_test.go`
- Move: `pkg/consensus/isr/replica.go`
- Move: `pkg/consensus/isr/replication.go`
- Move: `pkg/consensus/isr/replication_test.go`
- Move: `pkg/consensus/isr/snapshot_test.go`
- Move: `pkg/consensus/isr/testenv_test.go`
- Move: `pkg/consensus/isr/types.go`
- Modify: `pkg/consensus/multiisr/api_test.go`
- Modify: `pkg/consensus/multiisr/group.go`
- Modify: `pkg/consensus/multiisr/limits.go`
- Modify: `pkg/consensus/multiisr/limits_test.go`
- Modify: `pkg/consensus/multiisr/pressure_testutil_test.go`
- Modify: `pkg/consensus/multiisr/registry_test.go`
- Modify: `pkg/consensus/multiisr/runtime.go`
- Modify: `pkg/consensus/multiisr/runtime_test.go`
- Modify: `pkg/consensus/multiisr/scheduler_test.go`
- Modify: `pkg/consensus/multiisr/session.go`
- Modify: `pkg/consensus/multiisr/session_test.go`
- Modify: `pkg/consensus/multiisr/snapshot_test.go`
- Modify: `pkg/consensus/multiisr/testenv_test.go`
- Modify: `pkg/consensus/multiisr/transport.go`
- Modify: `pkg/consensus/multiisr/types.go`
- Modify: `pkg/msgstore/channelcluster/apply.go`
- Modify: `pkg/msgstore/channelcluster/apply_test.go`
- Modify: `pkg/msgstore/channelcluster/fetch_test.go`
- Modify: `pkg/msgstore/channelcluster/send.go`
- Modify: `pkg/msgstore/channelcluster/send_test.go`
- Modify: `pkg/msgstore/channelcluster/testenv_test.go`
- Modify: `pkg/msgstore/channelcluster/types.go`

- [ ] **Step 1: Switch one importer to the new `isr` path**

```go
import "github.com/WuKongIM/WuKongIM/pkg/replication/isr"
```

Use `pkg/consensus/multiisr/runtime_test.go` as the first importer change so the failure is isolated.

- [ ] **Step 2: Run the targeted test and confirm it fails because the new path does not exist yet**

Run: `go test ./pkg/consensus/multiisr -run TestNew -v`

Expected: FAIL with a compile/import error for `pkg/replication/isr`.

- [ ] **Step 3: Move the `isr` package files to `pkg/replication/isr`**

```bash
mkdir -p pkg/replication/isr
git mv pkg/consensus/isr/*.go pkg/replication/isr/
```

Keep `package isr` unchanged. Update `pkg/replication/isr/doc.go` to refer to the new path only in comments if needed.

- [ ] **Step 4: Rewrite remaining `pkg/consensus/isr` imports to `pkg/replication/isr`**

Update every file listed above in `pkg/consensus/multiisr` and `pkg/msgstore/channelcluster` to use the new path.

- [ ] **Step 5: Run focused tests**

Run: `go test ./pkg/replication/isr ./pkg/consensus/multiisr ./pkg/msgstore/channelcluster -run 'TestNew|TestReplica|TestSend|TestFetch' -v`

Expected: PASS for the targeted packages.

- [ ] **Step 6: Commit**

```bash
git add pkg/replication/isr pkg/consensus/multiisr pkg/msgstore/channelcluster
git commit -m "refactor: move isr package under replication"
```

## Task 2: Rename `pkg/consensus/multiisr` to `pkg/replication/isrnode`

**Files:**
- Move: `pkg/consensus/multiisr/api_test.go`
- Move: `pkg/consensus/multiisr/batching.go`
- Move: `pkg/consensus/multiisr/benchmark_test.go`
- Move: `pkg/consensus/multiisr/doc.go`
- Move: `pkg/consensus/multiisr/errors.go`
- Move: `pkg/consensus/multiisr/generation.go`
- Move: `pkg/consensus/multiisr/group.go`
- Move: `pkg/consensus/multiisr/limits.go`
- Move: `pkg/consensus/multiisr/limits_test.go`
- Move: `pkg/consensus/multiisr/pressure_testutil_test.go`
- Move: `pkg/consensus/multiisr/registry.go`
- Move: `pkg/consensus/multiisr/registry_test.go`
- Move: `pkg/consensus/multiisr/runtime.go`
- Move: `pkg/consensus/multiisr/runtime_test.go`
- Move: `pkg/consensus/multiisr/scheduler.go`
- Move: `pkg/consensus/multiisr/scheduler_test.go`
- Move: `pkg/consensus/multiisr/session.go`
- Move: `pkg/consensus/multiisr/session_test.go`
- Move: `pkg/consensus/multiisr/snapshot.go`
- Move: `pkg/consensus/multiisr/snapshot_test.go`
- Move: `pkg/consensus/multiisr/stress_test.go`
- Move: `pkg/consensus/multiisr/testenv_test.go`
- Move: `pkg/consensus/multiisr/transport.go`
- Move: `pkg/consensus/multiisr/types.go`

- [ ] **Step 1: Update one package-clause and one external test import to the target name**

```go
package isrnode
```

```go
import "github.com/WuKongIM/WuKongIM/pkg/replication/isrnode"
```

Use `pkg/consensus/multiisr/runtime.go` and `pkg/consensus/multiisr/runtime_test.go` first.

- [ ] **Step 2: Run the focused test and confirm it fails before the move**

Run: `go test ./pkg/consensus/multiisr -run TestNew -v`

Expected: FAIL because `package isrnode` and/or the new import path is unresolved.

- [ ] **Step 3: Move the package to `pkg/replication/isrnode` and update all package clauses**

```bash
mkdir -p pkg/replication/isrnode
git mv pkg/consensus/multiisr/*.go pkg/replication/isrnode/
```

Update:

- `package multiisr` -> `package isrnode`
- `package multiisr_test` -> `package isrnode_test`

- [ ] **Step 4: Rewrite all remaining self-imports and package comments**

Every test file under `pkg/replication/isrnode` should import `pkg/replication/isrnode` instead of `pkg/consensus/multiisr`.

- [ ] **Step 5: Run focused tests**

Run: `go test ./pkg/replication/isrnode -run 'TestNew|TestRuntime|TestRegistry|TestScheduler|TestSession' -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/replication/isrnode
git commit -m "refactor: rename multiisr package to isrnode"
```

## Task 3: Move `pkg/consensus/multiraft` to `pkg/replication/multiraft`

**Files:**
- Move: `pkg/consensus/multiraft/api.go`
- Move: `pkg/consensus/multiraft/benchmark_test.go`
- Move: `pkg/consensus/multiraft/control_test.go`
- Move: `pkg/consensus/multiraft/doc.go`
- Move: `pkg/consensus/multiraft/e2e_realistic_test.go`
- Move: `pkg/consensus/multiraft/e2e_test.go`
- Move: `pkg/consensus/multiraft/errors.go`
- Move: `pkg/consensus/multiraft/example_test.go`
- Move: `pkg/consensus/multiraft/future.go`
- Move: `pkg/consensus/multiraft/group.go`
- Move: `pkg/consensus/multiraft/log_test.go`
- Move: `pkg/consensus/multiraft/proposal_test.go`
- Move: `pkg/consensus/multiraft/ready.go`
- Move: `pkg/consensus/multiraft/recovery_test.go`
- Move: `pkg/consensus/multiraft/runtime.go`
- Move: `pkg/consensus/multiraft/runtime_test.go`
- Move: `pkg/consensus/multiraft/scheduler.go`
- Move: `pkg/consensus/multiraft/scheduler_test.go`
- Move: `pkg/consensus/multiraft/step_test.go`
- Move: `pkg/consensus/multiraft/storage_adapter.go`
- Move: `pkg/consensus/multiraft/testenv_test.go`
- Move: `pkg/consensus/multiraft/types.go`
- Modify: `internal/app/build.go`
- Modify: `pkg/controller/raftstore/memory.go`
- Modify: `pkg/controller/raftstore/memory_test.go`
- Modify: `pkg/controller/raftstore/pebble.go`
- Modify: `pkg/controller/raftstore/pebble_benchmark_test.go`
- Modify: `pkg/controller/raftstore/pebble_stress_test.go`
- Modify: `pkg/controller/raftstore/pebble_test.go`
- Modify: `pkg/controller/raftstore/pebble_test_helpers_test.go`
- Modify: `pkg/controller/wkcluster/cluster.go`
- Modify: `pkg/controller/wkcluster/cluster_test.go`
- Modify: `pkg/controller/wkcluster/config.go`
- Modify: `pkg/controller/wkcluster/config_test.go`
- Modify: `pkg/controller/wkcluster/discovery.go`
- Modify: `pkg/controller/wkcluster/forward.go`
- Modify: `pkg/controller/wkcluster/router.go`
- Modify: `pkg/controller/wkcluster/transport.go`
- Modify: `pkg/controller/wkcluster/transport_test.go`
- Modify: `pkg/controller/wkstore/benchmark_test.go`
- Modify: `pkg/controller/wkstore/fsm_stress_test.go`
- Modify: `pkg/controller/wkstore/integration_test.go`
- Modify: `pkg/controller/wkstore/state_machine_test.go`
- Modify: `pkg/controller/wkstore/statemachine.go`
- Modify: `pkg/controller/wkstore/testutil_test.go`

- [ ] **Step 1: Change one importer to the new `multiraft` path**

```go
import "github.com/WuKongIM/WuKongIM/pkg/replication/multiraft"
```

Start with `pkg/controller/raftstore/memory_test.go`.

- [ ] **Step 2: Run the targeted test and confirm the import fails before the move**

Run: `go test ./pkg/controller/raftstore -run TestMemory -v`

Expected: FAIL with an unresolved import for `pkg/replication/multiraft`.

- [ ] **Step 3: Move the package files**

```bash
mkdir -p pkg/replication/multiraft
git mv pkg/consensus/multiraft/*.go pkg/replication/multiraft/
```

Keep `package multiraft` unchanged.

- [ ] **Step 4: Rewrite all remaining imports from `pkg/consensus/multiraft` to `pkg/replication/multiraft`**

Touch every consumer file listed above before running tests.

- [ ] **Step 5: Run focused tests**

Run: `go test ./pkg/replication/multiraft ./pkg/controller/raftstore ./pkg/controller/wkcluster ./pkg/controller/wkstore -run 'TestNew|TestMemory|TestCluster|TestStateMachine' -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/replication/multiraft internal/app/build.go pkg/controller/raftstore pkg/controller/wkcluster pkg/controller/wkstore
git commit -m "refactor: move multiraft package under replication"
```

## Task 4: Rename `pkg/wktransport` to `pkg/transport/nodetransport`

**Files:**
- Move: `pkg/wktransport/client.go`
- Move: `pkg/wktransport/client_test.go`
- Move: `pkg/wktransport/codec.go`
- Move: `pkg/wktransport/codec_test.go`
- Move: `pkg/wktransport/errors.go`
- Move: `pkg/wktransport/pool.go`
- Move: `pkg/wktransport/pool_test.go`
- Move: `pkg/wktransport/server.go`
- Move: `pkg/wktransport/server_test.go`
- Move: `pkg/wktransport/types.go`
- Modify: `pkg/controller/wkcluster/cluster.go`
- Modify: `pkg/controller/wkcluster/forward.go`
- Modify: `pkg/controller/wkcluster/static_discovery.go`
- Modify: `pkg/controller/wkcluster/static_discovery_test.go`
- Modify: `pkg/controller/wkcluster/transport.go`
- Modify: `pkg/controller/wkcluster/transport_test.go`
- Modify: `pkg/controller/wkcluster/forward_test.go`

- [ ] **Step 1: Update one importer and package name to the target vocabulary**

```go
package nodetransport
```

```go
import "github.com/WuKongIM/WuKongIM/pkg/transport/nodetransport"
```

Start with `pkg/controller/wkcluster/static_discovery.go`.

- [ ] **Step 2: Run the focused test and confirm it fails**

Run: `go test ./pkg/controller/wkcluster -run TestStaticDiscovery -v`

Expected: FAIL because the new package path does not exist yet.

- [ ] **Step 3: Move files and rename the package clause**

```bash
mkdir -p pkg/transport/nodetransport
git mv pkg/wktransport/*.go pkg/transport/nodetransport/
```

Update all `package wktransport` clauses to `package nodetransport`.

- [ ] **Step 4: Rewrite remaining imports and identifiers**

Replace imports of `pkg/wktransport` with `pkg/transport/nodetransport` and replace local identifiers like `wktransport.ErrStopped` with `nodetransport.ErrStopped`.

- [ ] **Step 5: Run focused tests**

Run: `go test ./pkg/transport/nodetransport ./pkg/controller/wkcluster -run 'TestClient|TestServer|TestStaticDiscovery|TestTransport' -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/transport/nodetransport pkg/controller/wkcluster
git commit -m "refactor: rename wktransport package to nodetransport"
```

## Task 5: Rename `pkg/proto/wkpacket` to `pkg/protocol/wkframe`

**Files:**
- Move: `pkg/proto/wkpacket/common.go`
- Move: `pkg/proto/wkpacket/common_test.go`
- Move: `pkg/proto/wkpacket/connack.go`
- Move: `pkg/proto/wkpacket/connect.go`
- Move: `pkg/proto/wkpacket/disconnect.go`
- Move: `pkg/proto/wkpacket/event.go`
- Move: `pkg/proto/wkpacket/packet_test.go`
- Move: `pkg/proto/wkpacket/ping.go`
- Move: `pkg/proto/wkpacket/pong.go`
- Move: `pkg/proto/wkpacket/recv.go`
- Move: `pkg/proto/wkpacket/recvack.go`
- Move: `pkg/proto/wkpacket/send.go`
- Move: `pkg/proto/wkpacket/sendack.go`
- Move: `pkg/proto/wkpacket/setting.go`
- Move: `pkg/proto/wkpacket/sub.go`
- Move: `pkg/proto/wkpacket/suback.go`
- Modify: `internal/access/api/integration_test.go`
- Modify: `internal/access/api/message_send.go`
- Modify: `internal/access/api/server_test.go`
- Modify: `internal/access/gateway/error_map.go`
- Modify: `internal/access/gateway/frame_router.go`
- Modify: `internal/access/gateway/handler_test.go`
- Modify: `internal/access/gateway/integration_test.go`
- Modify: `internal/access/gateway/lifecycle.go`
- Modify: `internal/access/gateway/mapper.go`
- Modify: `internal/access/gateway/router_test.go`
- Modify: `internal/app/integration_test.go`
- Modify: `internal/gateway/auth.go`
- Modify: `internal/gateway/auth_test.go`
- Modify: `internal/gateway/core/dispatcher.go`
- Modify: `internal/gateway/core/server.go`
- Modify: `internal/gateway/core/server_test.go`
- Modify: `internal/gateway/gateway_internal_test.go`
- Modify: `internal/gateway/gateway_test.go`
- Modify: `internal/gateway/options_test.go`
- Modify: `internal/gateway/protocol/jsonrpc/adapter.go`
- Modify: `internal/gateway/protocol/jsonrpc/adapter_test.go`
- Modify: `internal/gateway/protocol/protocol.go`
- Modify: `internal/gateway/protocol/wkproto/adapter.go`
- Modify: `internal/gateway/protocol/wkproto/adapter_test.go`
- Modify: `internal/gateway/session/manager_test.go`
- Modify: `internal/gateway/session/session.go`
- Modify: `internal/gateway/testkit/fake_handler.go`
- Modify: `internal/gateway/testkit/fake_protocol.go`
- Modify: `internal/gateway/types/auth.go`
- Modify: `internal/gateway/types/event.go`
- Modify: `internal/runtime/online/delivery.go`
- Modify: `internal/runtime/online/delivery_test.go`
- Modify: `internal/runtime/online/registry_test.go`
- Modify: `internal/runtime/online/types.go`
- Modify: `internal/usecase/message/command.go`
- Modify: `internal/usecase/message/result.go`
- Modify: `internal/usecase/message/send.go`
- Modify: `internal/usecase/message/send_test.go`
- Modify: `pkg/proto/jsonrpc/codec.go`
- Modify: `pkg/proto/jsonrpc/frame_bridge_test.go`
- Modify: `pkg/proto/jsonrpc/types.go`
- Modify: `pkg/proto/wkproto/common.go`
- Modify: `pkg/proto/wkproto/connack.go`
- Modify: `pkg/proto/wkproto/connack_test.go`
- Modify: `pkg/proto/wkproto/connect.go`
- Modify: `pkg/proto/wkproto/connect_test.go`
- Modify: `pkg/proto/wkproto/disconnect.go`
- Modify: `pkg/proto/wkproto/disconnect_test.go`
- Modify: `pkg/proto/wkproto/event.go`
- Modify: `pkg/proto/wkproto/event_test.go`
- Modify: `pkg/proto/wkproto/message_seq.go`
- Modify: `pkg/proto/wkproto/ping_test.go`
- Modify: `pkg/proto/wkproto/pong_test.go`
- Modify: `pkg/proto/wkproto/protocol.go`
- Modify: `pkg/proto/wkproto/recv.go`
- Modify: `pkg/proto/wkproto/recv_test.go`
- Modify: `pkg/proto/wkproto/recvack.go`
- Modify: `pkg/proto/wkproto/recvack_test.go`
- Modify: `pkg/proto/wkproto/send.go`
- Modify: `pkg/proto/wkproto/send_test.go`
- Modify: `pkg/proto/wkproto/sendack.go`
- Modify: `pkg/proto/wkproto/sendack_test.go`
- Modify: `pkg/proto/wkproto/sub.go`
- Modify: `pkg/proto/wkproto/sub_test.go`
- Modify: `pkg/proto/wkproto/suback.go`
- Modify: `pkg/proto/wkproto/suback_test.go`

- [ ] **Step 1: Update one importer and one package clause to `wkframe`**

```go
package wkframe
```

```go
import "github.com/WuKongIM/WuKongIM/pkg/protocol/wkframe"
```

Start with `pkg/proto/wkproto/protocol.go` and `internal/gateway/protocol/wkproto/adapter.go`.

- [ ] **Step 2: Run a focused test and confirm the import fails**

Run: `go test ./pkg/proto/wkproto -run TestProtocol -v`

Expected: FAIL because `pkg/protocol/wkframe` does not exist yet.

- [ ] **Step 3: Move files and rename the package**

```bash
mkdir -p pkg/protocol/wkframe
git mv pkg/proto/wkpacket/*.go pkg/protocol/wkframe/
```

Update every `package wkpacket` clause to `package wkframe`.

- [ ] **Step 4: Rewrite all `wkpacket` imports and identifiers**

Replace:

```go
wkpacket.Frame
```

with:

```go
wkframe.Frame
```

across every file listed above.

- [ ] **Step 5: Run focused tests**

Run: `go test ./pkg/protocol/wkframe ./pkg/proto/wkproto ./pkg/proto/jsonrpc ./internal/gateway/... ./internal/runtime/online ./internal/usecase/message -run 'TestProtocol|TestAdapter|TestSend|TestDelivery' -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/protocol/wkframe pkg/proto/wkproto pkg/proto/jsonrpc internal/access internal/gateway internal/runtime internal/usecase/message internal/app/integration_test.go
git commit -m "refactor: rename wkpacket package to wkframe"
```

## Task 6: Rename `pkg/proto/wkproto` to `pkg/protocol/wkcodec`

**Files:**
- Move: `pkg/proto/wkproto/.gitignore`
- Move: `pkg/proto/wkproto/common.go`
- Move: `pkg/proto/wkproto/connack.go`
- Move: `pkg/proto/wkproto/connack_test.go`
- Move: `pkg/proto/wkproto/connect.go`
- Move: `pkg/proto/wkproto/connect_test.go`
- Move: `pkg/proto/wkproto/decoder.go`
- Move: `pkg/proto/wkproto/decoder_test.go`
- Move: `pkg/proto/wkproto/disconnect.go`
- Move: `pkg/proto/wkproto/disconnect_test.go`
- Move: `pkg/proto/wkproto/encoder.go`
- Move: `pkg/proto/wkproto/event.go`
- Move: `pkg/proto/wkproto/event_test.go`
- Move: `pkg/proto/wkproto/message_seq.go`
- Move: `pkg/proto/wkproto/ping_test.go`
- Move: `pkg/proto/wkproto/pong_test.go`
- Move: `pkg/proto/wkproto/protocol.go`
- Move: `pkg/proto/wkproto/protocol_test.go`
- Move: `pkg/proto/wkproto/recv.go`
- Move: `pkg/proto/wkproto/recv_test.go`
- Move: `pkg/proto/wkproto/recvack.go`
- Move: `pkg/proto/wkproto/recvack_test.go`
- Move: `pkg/proto/wkproto/send.go`
- Move: `pkg/proto/wkproto/send_test.go`
- Move: `pkg/proto/wkproto/sendack.go`
- Move: `pkg/proto/wkproto/sendack_test.go`
- Move: `pkg/proto/wkproto/sub.go`
- Move: `pkg/proto/wkproto/sub_test.go`
- Move: `pkg/proto/wkproto/suback.go`
- Move: `pkg/proto/wkproto/suback_test.go`
- Modify: `internal/app/integration_test.go`
- Modify: `internal/access/gateway/integration_test.go`
- Modify: `internal/gateway/gateway_test.go`
- Modify: `internal/gateway/protocol/wkproto/adapter.go`
- Modify: `internal/gateway/protocol/wkproto/adapter_test.go`

- [ ] **Step 1: Update one importer and one package clause to `wkcodec`**

```go
package wkcodec
```

```go
import codec "github.com/WuKongIM/WuKongIM/pkg/protocol/wkcodec"
```

Start with `internal/gateway/protocol/wkproto/adapter.go`.

- [ ] **Step 2: Run the focused adapter test and confirm it fails**

Run: `go test ./internal/gateway/protocol/wkproto -run TestAdapter -v`

Expected: FAIL because `pkg/protocol/wkcodec` does not exist yet.

- [ ] **Step 3: Move files and rename the package**

```bash
mkdir -p pkg/protocol/wkcodec
git mv pkg/proto/wkproto/.gitignore pkg/protocol/wkcodec/.gitignore
git mv pkg/proto/wkproto/*.go pkg/protocol/wkcodec/
```

Update `package wkproto` to `package wkcodec` for all non-hidden files.

- [ ] **Step 4: Rewrite imports and local identifiers**

Replace `codec.New()` import paths to point at `pkg/protocol/wkcodec`. Keep the internal gateway adapter directory `internal/gateway/protocol/wkproto` unchanged for now; only its dependency changes in this task.

- [ ] **Step 5: Run focused tests**

Run: `go test ./pkg/protocol/wkcodec ./internal/gateway/protocol/wkproto ./internal/app -run 'TestProtocol|TestAdapter|TestIntegration' -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/protocol/wkcodec internal/gateway/protocol/wkproto internal/app/integration_test.go internal/access/gateway/integration_test.go internal/gateway/gateway_test.go
git commit -m "refactor: rename wkproto package to wkcodec"
```

## Task 7: Rename `pkg/proto/jsonrpc` to `pkg/protocol/wkjsonrpc`

**Files:**
- Move: `pkg/proto/jsonrpc/README.md`
- Move: `pkg/proto/jsonrpc/README_zh.md`
- Move: `pkg/proto/jsonrpc/codec.go`
- Move: `pkg/proto/jsonrpc/codec_test.go`
- Move: `pkg/proto/jsonrpc/event_test.go`
- Move: `pkg/proto/jsonrpc/example_event.go`
- Move: `pkg/proto/jsonrpc/example_new_fields.go`
- Move: `pkg/proto/jsonrpc/frame_bridge_test.go`
- Move: `pkg/proto/jsonrpc/protocol.md`
- Move: `pkg/proto/jsonrpc/stream_test.go`
- Move: `pkg/proto/jsonrpc/types.go`
- Move: `pkg/proto/jsonrpc/wukongim_rpc_schema.json`
- Modify: `internal/gateway/gateway_test.go`
- Modify: `internal/gateway/protocol/jsonrpc/adapter.go`
- Modify: `internal/gateway/protocol/jsonrpc/adapter_test.go`

- [ ] **Step 1: Update one importer and one package clause**

```go
package wkjsonrpc
```

```go
import pkgjsonrpc "github.com/WuKongIM/WuKongIM/pkg/protocol/wkjsonrpc"
```

Start with `internal/gateway/protocol/jsonrpc/adapter.go` and `pkg/proto/jsonrpc/codec.go`.

- [ ] **Step 2: Run the focused adapter test and confirm it fails**

Run: `go test ./internal/gateway/protocol/jsonrpc -run TestAdapter -v`

Expected: FAIL because `pkg/protocol/wkjsonrpc` does not exist yet.

- [ ] **Step 3: Move files and rename the package**

```bash
mkdir -p pkg/protocol/wkjsonrpc
git mv pkg/proto/jsonrpc/* pkg/protocol/wkjsonrpc/
```

Rename `package jsonrpc` to `package wkjsonrpc` in Go files.

- [ ] **Step 4: Rewrite remaining imports**

Update every file listed above so they import `pkg/protocol/wkjsonrpc`.

- [ ] **Step 5: Run focused tests**

Run: `go test ./pkg/protocol/wkjsonrpc ./internal/gateway/protocol/jsonrpc -run 'TestDecode|TestFrameBridge|TestAdapter' -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/protocol/wkjsonrpc internal/gateway/protocol/jsonrpc internal/gateway/gateway_test.go
git commit -m "refactor: rename jsonrpc package to wkjsonrpc"
```

## Task 8: Rename `wkdb` to `metadb` and `raftstore` to `raftstorage`

**Files:**
- Move: `pkg/controller/wkdb/batch.go`
- Move: `pkg/controller/wkdb/benchmark_test.go`
- Move: `pkg/controller/wkdb/catalog.go`
- Move: `pkg/controller/wkdb/channel.go`
- Move: `pkg/controller/wkdb/channel_test.go`
- Move: `pkg/controller/wkdb/codec.go`
- Move: `pkg/controller/wkdb/codec_test.go`
- Move: `pkg/controller/wkdb/db.go`
- Move: `pkg/controller/wkdb/errors.go`
- Move: `pkg/controller/wkdb/shard.go`
- Move: `pkg/controller/wkdb/shard_spans.go`
- Move: `pkg/controller/wkdb/snapshot.go`
- Move: `pkg/controller/wkdb/snapshot_codec.go`
- Move: `pkg/controller/wkdb/snapshot_test.go`
- Move: `pkg/controller/wkdb/stress_test.go`
- Move: `pkg/controller/wkdb/testutil_test.go`
- Move: `pkg/controller/wkdb/user.go`
- Move: `pkg/controller/wkdb/user_test.go`
- Move: `pkg/controller/raftstore/helpers.go`
- Move: `pkg/controller/raftstore/memory.go`
- Move: `pkg/controller/raftstore/memory_test.go`
- Move: `pkg/controller/raftstore/pebble.go`
- Move: `pkg/controller/raftstore/pebble_benchmark_test.go`
- Move: `pkg/controller/raftstore/pebble_codec.go`
- Move: `pkg/controller/raftstore/pebble_stress_test.go`
- Move: `pkg/controller/raftstore/pebble_test.go`
- Move: `pkg/controller/raftstore/pebble_test_helpers_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/lifecycle_test.go`
- Modify: `internal/access/gateway/handler_test.go`
- Modify: `internal/usecase/message/app.go`
- Modify: `internal/usecase/message/send_test.go`
- Modify: `pkg/controller/wkcluster/cluster_test.go`
- Modify: `pkg/controller/wkstore/benchmark_test.go`
- Modify: `pkg/controller/wkstore/command.go`
- Modify: `pkg/controller/wkstore/fsm_stress_test.go`
- Modify: `pkg/controller/wkstore/integration_test.go`
- Modify: `pkg/controller/wkstore/state_machine_test.go`
- Modify: `pkg/controller/wkstore/statemachine.go`
- Modify: `pkg/controller/wkstore/store.go`
- Modify: `pkg/controller/wkstore/testutil_test.go`

- [ ] **Step 1: Update one importer to `metadb` and one importer to `raftstorage`**

```go
import "github.com/WuKongIM/WuKongIM/pkg/storage/metadb"
import "github.com/WuKongIM/WuKongIM/pkg/storage/raftstorage"
```

Start with `internal/app/build.go`.

- [ ] **Step 2: Run the focused app test and confirm it fails**

Run: `go test ./internal/app -run TestNewBuildsDBClusterStoreMessageAndGatewayAdapter -v`

Expected: FAIL because one or both new package paths do not exist yet.

- [ ] **Step 3: Move files and rename package clauses**

```bash
mkdir -p pkg/storage/metadb pkg/storage/raftstorage
git mv pkg/controller/wkdb/*.go pkg/storage/metadb/
git mv pkg/controller/raftstore/*.go pkg/storage/raftstorage/
```

Rename:

- `package wkdb` -> `package metadb`
- `package raftstore` -> `package raftstorage`

- [ ] **Step 4: Rewrite all remaining imports and identifiers**

Examples:

```go
db, err := metadb.Open(path)
raftDB, err := raftstorage.Open(path)
```

Update every file listed above before running tests.

- [ ] **Step 5: Run focused tests**

Run: `go test ./pkg/storage/metadb ./pkg/storage/raftstorage ./internal/app ./internal/usecase/message -run 'TestOpen|TestNew|TestSend' -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/storage/metadb pkg/storage/raftstorage internal/app internal/usecase/message internal/access/gateway/handler_test.go pkg/controller/wkcluster/cluster_test.go pkg/controller/wkstore
git commit -m "refactor: rename wkdb and raftstore packages"
```

## Task 9: Split `wkstore` into `metastore` and `metafsm`

**Files:**
- Create: `pkg/storage/metastore/store.go`
- Create: `pkg/storage/metastore/integration_test.go`
- Create: `pkg/storage/metastore/testutil_test.go`
- Create: `pkg/storage/metafsm/command.go`
- Create: `pkg/storage/metafsm/statemachine.go`
- Create: `pkg/storage/metafsm/state_machine_test.go`
- Create: `pkg/storage/metafsm/benchmark_test.go`
- Create: `pkg/storage/metafsm/fsm_stress_test.go`
- Create: `pkg/storage/metafsm/testutil_test.go`
- Delete: `pkg/controller/wkstore/store.go`
- Delete: `pkg/controller/wkstore/integration_test.go`
- Delete: `pkg/controller/wkstore/command.go`
- Delete: `pkg/controller/wkstore/statemachine.go`
- Delete: `pkg/controller/wkstore/state_machine_test.go`
- Delete: `pkg/controller/wkstore/benchmark_test.go`
- Delete: `pkg/controller/wkstore/fsm_stress_test.go`
- Delete: `pkg/controller/wkstore/testutil_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`
- Modify: `pkg/controller/wkcluster/cluster_test.go`

- [ ] **Step 1: Update one app import to the target split**

```go
import "github.com/WuKongIM/WuKongIM/pkg/storage/metastore"
import "github.com/WuKongIM/WuKongIM/pkg/storage/metafsm"
```

Use `internal/app/build.go` first so the compile failure is obvious.

- [ ] **Step 2: Run the focused app test and confirm it fails**

Run: `go test ./internal/app -run TestNewBuildsDBClusterStoreMessageAndGatewayAdapter -v`

Expected: FAIL because `metastore` and `metafsm` do not exist yet.

- [ ] **Step 3: Create `metastore` with only the facade code**

Move `store.go` into `pkg/storage/metastore/store.go` and rename:

```go
package metastore
```

Keep the dependency surface narrow:

- in this task, `Store` may continue to import the current cluster package path so the repository stays green
- in Task 11, switch that import to `pkg/cluster/raftcluster`
- `Store` should already switch to `metadb` in this task

- [ ] **Step 4: Create `metafsm` with state machine and command codec files**

Move `command.go`, `statemachine.go`, `state_machine_test.go`, `benchmark_test.go`, and `fsm_stress_test.go` into `pkg/storage/metafsm`, and rename:

```go
package metafsm
```

Split `testutil_test.go` so:

- `metafsm/testutil_test.go` keeps `mustNewStateMachine`, DB helpers, fake transport, and wait helpers needed by FSM tests
- `metastore/testutil_test.go` keeps only the helpers still required by metastore integration tests

- [ ] **Step 5: Update app wiring and tests**

Examples:

```go
app.store = metastore.New(app.cluster, app.db)
NewStateMachine: metafsm.NewStateMachineFactory(db)
```

Update:

- `internal/app/app.go`
- `internal/app/build.go`
- `pkg/controller/wkcluster/cluster_test.go`

- [ ] **Step 6: Run focused tests**

Run: `go test ./pkg/storage/metastore ./pkg/storage/metafsm ./internal/app -run 'TestMemoryBackedGroup|TestStateMachine|TestNewBuildsDBClusterStoreMessageAndGatewayAdapter' -v`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/storage/metastore pkg/storage/metafsm internal/app pkg/controller/wkcluster/cluster_test.go
git commit -m "refactor: split wkstore into metastore and metafsm"
```

## Task 10: Rename `channelcluster` to `channellog`

**Files:**
- Move: `pkg/msgstore/channelcluster/api_test.go`
- Move: `pkg/msgstore/channelcluster/apply.go`
- Move: `pkg/msgstore/channelcluster/apply_test.go`
- Move: `pkg/msgstore/channelcluster/cluster.go`
- Move: `pkg/msgstore/channelcluster/codec.go`
- Move: `pkg/msgstore/channelcluster/deleting_test.go`
- Move: `pkg/msgstore/channelcluster/doc.go`
- Move: `pkg/msgstore/channelcluster/errors.go`
- Move: `pkg/msgstore/channelcluster/fetch.go`
- Move: `pkg/msgstore/channelcluster/fetch_test.go`
- Move: `pkg/msgstore/channelcluster/meta.go`
- Move: `pkg/msgstore/channelcluster/meta_test.go`
- Move: `pkg/msgstore/channelcluster/send.go`
- Move: `pkg/msgstore/channelcluster/send_test.go`
- Move: `pkg/msgstore/channelcluster/testenv_test.go`
- Move: `pkg/msgstore/channelcluster/types.go`
- Modify: `internal/access/api/error_map.go`
- Modify: `internal/access/api/server_test.go`
- Modify: `internal/access/gateway/error_map.go`
- Modify: `internal/access/gateway/handler_test.go`
- Modify: `internal/access/gateway/integration_test.go`
- Modify: `internal/usecase/message/deps.go`
- Modify: `internal/usecase/message/retry.go`
- Modify: `internal/usecase/message/send.go`
- Modify: `internal/usecase/message/send_test.go`

- [ ] **Step 1: Update one importer and one package clause**

```go
package channellog
```

```go
import "github.com/WuKongIM/WuKongIM/pkg/storage/channellog"
```

Start with `internal/usecase/message/deps.go`.

- [ ] **Step 2: Run the focused usecase test and confirm it fails**

Run: `go test ./internal/usecase/message -run TestSend -v`

Expected: FAIL because `pkg/storage/channellog` does not exist yet.

- [ ] **Step 3: Move files and rename the package**

```bash
mkdir -p pkg/storage/channellog
git mv pkg/msgstore/channelcluster/*.go pkg/storage/channellog/
```

Rename `package channelcluster` to `package channellog`.

- [ ] **Step 4: Rewrite imports and type references**

Examples:

```go
type ChannelCluster interface {
    ApplyMeta(meta channellog.ChannelMeta) error
    Send(ctx context.Context, req channellog.SendRequest) (channellog.SendResult, error)
}
```

- [ ] **Step 5: Run focused tests**

Run: `go test ./pkg/storage/channellog ./internal/usecase/message ./internal/access/gateway ./internal/access/api -run 'TestSend|TestFetch|TestErrorMap' -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/storage/channellog internal/usecase/message internal/access/gateway internal/access/api
git commit -m "refactor: rename channelcluster package to channellog"
```

## Task 11: Rename `wkcluster` to `raftcluster`

**Files:**
- Move: `pkg/controller/wkcluster/cluster.go`
- Move: `pkg/controller/wkcluster/cluster_test.go`
- Move: `pkg/controller/wkcluster/codec.go`
- Move: `pkg/controller/wkcluster/codec_test.go`
- Move: `pkg/controller/wkcluster/config.go`
- Move: `pkg/controller/wkcluster/config_test.go`
- Move: `pkg/controller/wkcluster/discovery.go`
- Move: `pkg/controller/wkcluster/errors.go`
- Move: `pkg/controller/wkcluster/forward.go`
- Move: `pkg/controller/wkcluster/forward_test.go`
- Move: `pkg/controller/wkcluster/router.go`
- Move: `pkg/controller/wkcluster/router_test.go`
- Move: `pkg/controller/wkcluster/static_discovery.go`
- Move: `pkg/controller/wkcluster/static_discovery_test.go`
- Move: `pkg/controller/wkcluster/stress_test.go`
- Move: `pkg/controller/wkcluster/transport.go`
- Move: `pkg/controller/wkcluster/transport_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/lifecycle_test.go`
- Modify: `pkg/storage/metastore/store.go`

- [ ] **Step 1: Update one importer and one package clause**

```go
package raftcluster
```

```go
import "github.com/WuKongIM/WuKongIM/pkg/cluster/raftcluster"
```

Start with `internal/app/app.go`.

- [ ] **Step 2: Run the focused app test and confirm it fails**

Run: `go test ./internal/app -run TestStartStartsClusterBeforeGateway -v`

Expected: FAIL because `pkg/cluster/raftcluster` does not exist yet.

- [ ] **Step 3: Move files and rename the package**

```bash
mkdir -p pkg/cluster/raftcluster
git mv pkg/controller/wkcluster/*.go pkg/cluster/raftcluster/
```

Rename `package wkcluster` to `package raftcluster`.

- [ ] **Step 4: Rewrite imports and public identifiers**

Examples:

```go
cluster        *raftcluster.Cluster
func (c ClusterConfig) runtimeConfig(db *metadb.DB, raftDB *raftstorage.DB, nodeID uint64) raftcluster.Config
```

Update every file listed above.

- [ ] **Step 5: Run focused tests**

Run: `go test ./pkg/cluster/raftcluster ./internal/app ./pkg/storage/metastore -run 'TestCluster|TestStart|TestMemoryBackedGroup' -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/cluster/raftcluster internal/app pkg/storage/metastore
git commit -m "refactor: rename wkcluster package to raftcluster"
```

## Task 12: Final vocabulary cleanup, `uuidutil`, and full verification

**Files:**
- Delete: `pkg/wkutil/uuid.go`
- Modify: `docs/superpowers/specs/2026-04-03-pkg-package-naming-design.md`
- Modify: `docs/superpowers/plans/2026-04-03-pkg-package-naming-implementation.md`
- Modify any remaining Go files still importing:
  - `pkg/consensus/*`
  - `pkg/controller/*`
  - `pkg/proto/*`
  - `pkg/msgstore/channelcluster`
  - `pkg/wktransport`
  - `pkg/wkutil`

- [ ] **Step 1: Remove `wkutil` unless a new caller appears during implementation**

Current repository search shows no Go call sites importing `pkg/wkutil`.

Default action:

- delete `pkg/wkutil/uuid.go`
- do not create `pkg/util/uuidutil` unless a real caller appears during the rename work

If a caller does appear, add the replacement file in this task and keep the wrapper tiny.

- [ ] **Step 2: Search for stale imports and fail the build on purpose if any remain**

Run: `rg -n 'pkg/(consensus|controller|proto|msgstore/channelcluster|wktransport|wkutil)' cmd internal pkg docs -g '*.go' -g '*.md'`

Expected: either no matches, or only known historical references in already-approved docs that you will update immediately after.

- [ ] **Step 3: Rewrite remaining docs/import aliases and remove empty directories**

Run:

```bash
rmdir pkg/consensus pkg/controller pkg/proto pkg/msgstore 2>/dev/null || true
```

Also update any stale aliases like `wkdb`, `wkcluster`, `wkpacket`, and `wkproto` in tests and production files where the alias is now misleading.

- [ ] **Step 4: Run the full verification suite**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg internal docs
git commit -m "refactor: finish pkg naming redesign"
```

## Verification Summary

At minimum, keep this verification cadence:

1. targeted tests after every task
2. `gofmt -w` after every edit batch
3. `go test ./...` at the end

If any task triggers unexpected behavior changes, stop and debug before continuing. Do not stack multiple broken rename tasks.
