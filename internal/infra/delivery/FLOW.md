---
scope: package
summary: Adapts presence lookup and owner-local session writes for canonical online delivery.
---

# Delivery Infrastructure Flow

## Responsibility

This package adapts presence and the online-session registry to the delivery
runtime's narrow ports, and converts accepted owner-local deliveries into
gateway packets.
It does not own plan admission, retries, ACK tracking, or offline classification.

## Boundaries

- Retry, ACK tracking, plan admission, grouping, and offline classification
  belong to `internal/runtime/delivery`.
- Protocol and concrete session details stay inside the local writer adapter.
- Presence results preserve exact-target alignment; adapter availability is
  not interpreted as recipient offline state.

## Main Flows

1. `OnlinePresence` resolves exact authority-target groups and returns aligned
   routes or aligned group errors.
2. `LocalSessionWriter` rechecks active UID, session, and owner identity,
   builds the receive packet, and writes through the stored session handle.

## Invariants and Failure Semantics

- Packet projection removes the configured command suffix before deriving a
  person peer UID and preserves command and non-persistence flags on the receive
  packet. It also preserves the complete message setting bitset, topic, and
  expiration from the envelope. Sequence-zero envelopes are transient.
- The writer performs the final owner fence immediately before the write.
- Accepted writes return success; missing registry state is retryable, while
  stale identity, packet-build, closed-session, and overflow failures drop.
- This adapter never creates ACK tokens or changes retry policy.
- A failed presence dependency must not be converted into an offline result.

- Edit hints resolve bounded subscriber pages through presence and use exact session owner fences. Only opted-in sessions receive the body-free EVENT; no RECVACK or offline record is created, and slow/closed sessions can drop hints for foreground sync to repair.

## Read First

- [Presence adapter](online_presence.go)
- [Local session writer](local_session_writer.go)

## Update Triggers

Update this file when presence alignment, owner fencing, packet conversion,
session-write results, or delivery port contracts change.
