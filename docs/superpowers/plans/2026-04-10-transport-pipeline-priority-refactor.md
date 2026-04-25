# Transport Pipeline Priority Refactor Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor `pkg/transport` to use pooled frame buffers, per-connection priority queues, and muxed read/write loops so raft traffic is no longer blocked by business RPC traffic.

**Architecture:** Replace the lock-held connection pool with `Pool -> MuxConn -> priorityWriter` and keep the wire format unchanged. Server and client both talk through `MuxConn`, while upstream code switches to the new `PoolConfig` API and separate pools for raft vs RPC traffic.

**Tech Stack:** Go, `net.Conn`, `net.Buffers`, `sync/atomic`, `testing`, `stretchr/testify`

---

### Task 1: Frame and pending primitives

**Files:**
- Create: `pkg/transport/frame.go`
- Create: `pkg/transport/pending.go`
- Create: `pkg/transport/frame_test.go`
- Create: `pkg/transport/pending_test.go`
- Modify: `pkg/transport/errors.go`
- Delete: `pkg/transport/codec.go`
- Delete: `pkg/transport/codec_test.go`

- [ ] Step 1: add failing tests for frame read/write, max size, invalid type, and pending map concurrency.
- [ ] Step 2: run `go test ./pkg/transport -run 'Test(ReadFrame|Pending)'` and confirm failures are from missing implementation.
- [ ] Step 3: implement slab-backed frame helpers and sharded pending map.
- [ ] Step 4: rerun the same tests until green.

### Task 2: Priority writer and muxed connection

**Files:**
- Create: `pkg/transport/writer.go`
- Create: `pkg/transport/conn.go`
- Create: `pkg/transport/writer_test.go`
- Create: `pkg/transport/conn_test.go`

- [ ] Step 1: add failing tests for priority ordering, queue backpressure, RPC response matching, and pending failure on close.
- [ ] Step 2: run `go test ./pkg/transport -run 'TestPriorityWriter|TestMuxConn'` and confirm failures.
- [ ] Step 3: implement `priorityWriter` and `MuxConn` with `readFrame` / `writeFrame`.
- [ ] Step 4: rerun the focused tests until green.

### Task 3: Rewrite pool, client, and server

**Files:**
- Modify: `pkg/transport/pool.go`
- Modify: `pkg/transport/client.go`
- Modify: `pkg/transport/server.go`
- Modify: `pkg/transport/types.go`
- Modify: `pkg/transport/pool_test.go`
- Modify: `pkg/transport/client_test.go`
- Modify: `pkg/transport/server_test.go`

- [ ] Step 1: rewrite tests around `PoolConfig`, `Send`, `RPC`, and the new message handler signature.
- [ ] Step 2: run `go test ./pkg/transport` and confirm failures reflect the old API.
- [ ] Step 3: replace the old connection-lock pool and client/server code with the muxed model.
- [ ] Step 4: rerun `go test ./pkg/transport` until green.

### Task 4: Upstream compile migration

**Files:**
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/controller/raft/service_test.go`
- Modify: `pkg/cluster/transport_test.go`
- Modify: `pkg/cluster/forward_test.go`
- Modify: `pkg/cluster/controller_client_internal_test.go`
- Modify: `pkg/channel/transport/integration_test.go`
- Modify: `pkg/channel/transport/adapter_test.go`
- Modify: `pkg/channel/log/multinode_integration_test.go`
- Modify: `internal/app/lifecycle.go`
- Modify: other direct `transport.NewPool(...)` and `Server.Handle(func(net.Conn,...))` callsites flagged by compiler

- [ ] Step 1: update callsites to `transport.NewPool(transport.PoolConfig{...})` and remove `net.Conn` from transport message handlers.
- [ ] Step 2: run `go test ./...` and capture remaining compile/runtime regressions.
- [ ] Step 3: fix the minimal remaining callsites or tests.
- [ ] Step 4: rerun `go test ./...` until green.
