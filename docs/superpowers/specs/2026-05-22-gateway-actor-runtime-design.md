# Gateway Actor Runtime Design

## Goal

Replace the gateway gnet connection runtime with a shard-owned actor pipeline, remove the stdnet transport and blocking writer fallback, and make gnet the only gateway transport.

## Non-goals

- Do not preserve stdnet compatibility.
- Do not keep a blocking writer fallback for gateway production transports.
- Do not keep unused session encoded queue APIs after the gnet-only path owns outbound writes.

## Current Problems

- gnet currently creates one goroutine per connection through `connState.start()` and `go s.run()`.
- gateway core can create per-session writer goroutines through `startWriter` for queued outbound writes.
- stdnet keeps blocking write semantics alive, forcing core to retain write deadlines, write queues, and fallback paths.
- session metadata and outbound queue ownership are mixed in `internal/gateway/session`.

## Target Architecture

The gnet engine owns network events and hands connection work to a fixed actor pool. Each connection is assigned to exactly one actor shard for its lifetime. The actor shard serializes open, data, outbound, and close events for that connection.

```text
gnet event loop
  -> connState.enqueueEvent
  -> actorPool.schedule(conn)
  -> actorShard.run
  -> ConnHandler.OnOpen / OnData / OnClose
  -> core protocol/auth/dispatch
  -> transport-owned ordered AsyncWrite / AsyncWritev
```

The actor pool removes per-connection goroutines. Core no longer starts writer goroutines for gnet. Outbound writes are asynchronous and ordered by the gnet connection state.

## Core Rules

- A `connState` has one owner actor shard.
- gnet callbacks never call gateway handlers directly; they enqueue connection events and return.
- Actor shards call the existing `transport.ConnHandler` boundary first, so core behavior can be preserved while transport internals change.
- Core writes directly to `transport.Conn.Write`, which is now expected to be transport-managed and non-blocking in production.
- stdnet is removed. Tests that need a transport use `internal/gateway/testkit` or gnet.
- Session no longer owns encoded outbound queues; it remains a session metadata and value holder.

## Outbound Semantics

For gnet TCP, `Conn.Write` uses `AsyncWrite`. For gnet WebSocket, small payloads use compact frame encoding and large payloads use `AsyncWritev` to avoid copying the payload. The connection state keeps outbound byte accounting until gnet callbacks release it.

All outbound writes for a connection must use the same transport path. There is no mixed direct/queued path.

## Removal Scope

- Remove `internal/gateway/transport/stdnet` package and tests.
- Remove stdnet registration from `internal/gateway/gateway.go`.
- Update tests and docs that expect stdnet to exist.
- Remove core `startWriter`, write timeout fallback, session encoded queue methods, and tests that only validate blocking writer behavior.
- Remove obsolete gateway session config keys for read buffer size, write queue size, and write timeout. Keep max inbound bytes, max outbound bytes, idle timeout, async SEND settings, and close-on-handler-error.

## Safety Requirements

- Same session open/close callbacks fire once.
- Inbound frame ordering is preserved per connection.
- Outbound write ordering is preserved per connection.
- WebSocket text/binary opcode behavior remains unchanged.
- Outbound overflow still maps to outbound overflow errors and close reasons.
- Idle timeout, auth, async SEND dispatch, batch SEND dispatch, and drain summary semantics remain unchanged.
- gnet listener grouping and restart behavior remain unchanged.

## Benchmark Targets

- Idle connection goroutine count should not scale linearly with connection count in gnet unit tests.
- SEND dispatch should keep zero allocations on queue-full rejection and avoid extra fallback allocation when no batch handler exists.
- gnet hot-path benchmarks should show no additional allocations for TCP and WebSocket traffic compared with the current implementation.
