---
scope: package
summary: Owns one exact benchmark terminal drain, capability grant, and session-seal admission proof.
---

# Benchmark Terminal Usecase Flow

## Responsibility

`internal/usecase/benchterminal` owns the entry-agnostic, once-per-product-
process terminal cut for one exact benchmark run and assignment. It drains
accepted product work, mints one opaque capability, and counts exact owner-local
session-seal admissions.
It does not parse entry requests, look up sessions, encode markers, or write
transports.

## Boundaries

- HTTP and gateway adapters parse requests, bind the authenticated session, and
  encode markers. This package never looks up sessions or writes transports.
- Gateway SEND, Channel append, and Online Delivery expose narrow drain ports.
- The controller is one-shot process state, not a general drain/restart plane.
  A new reviewed QPS tier starts a new product process and controller.

## Main Flows

```text
Prepare(exact run, assignment, expected sessions)
  -> close Gateway SEND admission and drain accepted mailbox work
  -> stop Channel append admission and drain append/handoff work
  -> quiesce Online Delivery and wait pending RECVACK bindings = 0
  -> issue non-zero epoch plus opaque capability
  -> await exactly the expected unique SessionFence admissions

authenticated terminal EVENT
  -> adapter verifies current session identity and copies redacting proof values
  -> constant-time epoch and capability-digest validation
  -> reserve the exact unique session
  -> per-call sealer closes ordinary outbound admission and enqueues marker ACK
  -> publish the bounded session count or a permanent closed failure
```

## Invariants and Failure Semantics

- Drain ports run in the stated order against one detached context bounded by
  at most 90 seconds. Caller cancellation stops only that wait; an exact retry
  joins the existing generation and never cancels admitted work.
- Run and assignment strings are trimmed and bounded to 128 bytes. Session count
  is positive, bounded by configuration, and normally capped at 2,500.
- A drain, randomness, protocol, duplicate, overflow, stale-proof, or seal
  failure permanently fails the epoch and never issues or reopens a capability.
- `SessionSealer` is supplied per call, and the adapter must prove its usecase
  SessionID is the currently authenticated session before sealing.
- Marker admission, async-write completion, empty buffers, and TCP half-close
  are not remote receipt proof. Only the client's exact decoded ACK closes the
  benchmark receive proof.
- Capability, nonce, digest, run, assignment, session identity, and raw adapter
  errors never enter status, formatting, logs, metrics, or supervisor labels.

## Read First

- [Controller](controller.go)
- [Controller tests](controller_test.go)

## Update Triggers

Update this file when drain order, one-shot identity, capability validation,
session-seal counting, failure permanence, or secret exposure changes.
