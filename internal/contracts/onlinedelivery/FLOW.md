---
scope: package
summary: Defines immutable bounded recipient plans, exact authority targets, owner pushes, and classified delivery results.
---

# Online Delivery Contracts Flow

## Responsibility

`internal/contracts/onlinedelivery` owns the canonical values transferred across
Online Delivery planning, admission, presence resolution, and owner push seams.
It does not select recipients, execute plans, persist ACKs, or write sessions.

## Boundaries

- Subscriber discovery, target grouping, queues, ACK tokens, retry state, and
  concrete push execution stay outside.
- Clone helpers support retaining/serializing adapters and tests; the admitted
  hot path does not clone by default.
- The package contains data contracts only.

## Main Flows

1. A bounded plan classifies Durable or Transient delivery and groups recipients
   by exact authority target.
2. Successful admission transfers shared immutable plan ownership.
3. Owner push returns accepted, retryable, and dropped routes as distinct sets.

## Invariants and Failure Semantics

- Callers never mutate events, targets, or recipients after successful
  admission.
- Exact authority grouping survives queue and push boundaries.
- Intentionally skipped routes are classified as dropped rather than omitted.

## Read First

- [Delivery types](types.go)
- [Contract tests](types_test.go)

## Update Triggers

Update this file when plan classification, authority grouping, ownership
transfer, route result classes, or clone semantics change.
