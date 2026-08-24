---
scope: package
summary: Provides a tooling-grade WKProto TCP client with session crypto, bounded SEND/RECV queues, exact ACK matching, and pooling.
---

# WKProto Client Flow

## Responsibility

This package owns protocol connection behavior for wkbench, E2E tests, and Go
tools: CONNECT, optional crypto, SEND/ACK, RECV/ACK, ping, one writer pump, one
reader loop per connection, and optional identity pooling.
It does not provision users or channels, choose benchmark policy, or retry sends.

## Boundaries

- It does not create users, tokens, channels, or subscribers, and it owns no
  benchmark retry or identity-rebalance policy.
- SEND batching writes contiguous ordinary WKProto SEND frames; it introduces
  no SENDBATCH wire frame.
- Pooling assigns configured identities to gateway addresses round-robin.

## Main Flows

1. Connect dials, exchanges CONNECT/CONNACK under operation timeouts, derives
   optional session crypto, publishes the new session, starts writer/reader,
   then closes the replaced connection.
2. Send reserves bounded inflight state, records `(ClientSeq, ClientMsgNo)`,
   queues a writer request, and resolves the exact future when the reader sees
   its SENDACK.
3. The reader decrypts RECV into a bounded lossless queue and optionally sends
   RECVACK; send-only callers may discard inbound RECV before decryption/queueing.
4. A grant-bound terminal seal quiesces SEND/PING and reconnect admission,
   joins admitted SENDACKs, writes the reserved epoch/capability/nonce EVENT,
   and succeeds only after decoding the exact peer ACK with no trailing frame.

## Invariants and Failure Semantics

- `Close` is terminal. Reconnect before terminal close uses a fresh pending
  tracker and reader; streaming `Recv`/`ReadFrame` has no implicit operation timeout.
  Incomplete-frame EOF is unexpected, never a clean boundary.
- `TrySendAsync` leaves no pending entry when admission, writer queue, or
  inflight capacity is busy.
- Retries may reuse idempotent `ClientMsgNo`, but overlapping attempts require
  distinct nonzero `ClientSeq` so late ACKs cannot resolve another attempt.
- A full inbound queue backpressures the socket; close or replacement releases
  blocked publishers. Discard mode prevents RECV fanout from blocking ACK progress.
  Leased reads keep dequeued handoff ownership visible until the next stage
  accepts it.
- Ping and RECVACK share the writer so all frames remain serialized.
- Terminal capability and nonce are redacted and never metric labels. TCP
  half-close, local write completion, malformed/stale ACK, EOF, or post-ACK
  bytes fail closed. RECVACK remains permitted while the terminal ACK is pending.

## Read First

- [Client lifecycle](client.go)
- [Writer](writer.go)
- [Reader](reader.go)
- [Message API](message.go)
- [Identity pool](pool.go)

## Update Triggers

Update this file when connection replacement, timeouts, encryption, send
admission, ACK identity, inbound backpressure, control writes, or pooling changes.
