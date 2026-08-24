---
scope: package
summary: Performs bounded node-local best-effort delivery of message, offline-recipient, and online-status webhooks.
---

# Webhook Runtime Flow

## Responsibility

This package wraps `pkg/workqueue` behind typed APIs for node-local best-effort
webhook admission, batching, retry, JSON mapping, and HTTP delivery.
It does not own message durability, presence, Channel ordering, or crash replay.

## Boundaries

- It receives already-decided events from app adapters and does not own message
  durability, subscriber scans, presence, plugin hooks, Channel ordering, or
  crash replay.
- Endpoint and JSON compatibility live here; product success semantics do not.

## Main Flows

1. A durable Channel append event enters a bounded notify pool and sends a JSON
   array to the `msg.notify` endpoint.
2. Delivery-classified offline recipients enter a sharded mailbox in bounded
   UID chunks and send `msg.offline` objects.
3. Successful presence activation or deactivation enters the bounded status
   pool and sends legacy `user.onlinestatus` strings.

## Invariants and Failure Semantics

- Admission is bounded and best effort; full, closed, canceled, and exhausted
  events are observed and dropped.
- Webhook failure never changes SENDACK, durable append, membership, or owner
  delivery results.
- Large offline fanout remains batched; never enqueue one item per recipient.
- This runtime provides no durability or crash-replay guarantee.

## Read First

- [Runtime](runtime.go)
- [Event types](types.go)
- [JSON mapper](mapper.go)
- [HTTP sender](sender.go)

## Update Triggers

Update this file when event sources, batching, queue pressure, retry, JSON
compatibility, endpoints, or best-effort failure semantics change.
