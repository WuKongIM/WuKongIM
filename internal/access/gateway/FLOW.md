---
scope: package
summary: Maps gateway sessions and frames to message, presence, and delivery usecases without owning authority or durable-send policy.
---

# Gateway Access Flow

## Responsibility

`internal/access/gateway` adapts `pkg/gateway` authentication/session events and
WKProto frames to entry-independent presence, message, delivery, and benchmark
terminal-fence commands,
then maps results back to protocol frames and stable reason codes.
It does not own reusable message, presence, delivery, or storage policy.

## Boundaries

- Presence authority/conflict policy, durable SEND rules, recipient fanout, ACK
  tracking, and push behavior remain in usecases and runtimes.
- This package may import gateway/frame contracts but not concrete cluster or
  Channel runtimes.
- Gateway core already owns asynchronous bounded SEND admission; this adapter
  remains synchronous and creates no second queue.

## Main Flows

1. Activation maps authenticated Session values to presence, classifies known
   failures, and rolls back accepted activation if successful CONNACK cannot be
   published; close deactivates and independently reports delivery closure.
2. Single or batched SEND maps immutable packet/session fields, calls one
   aligned message batch, records optional trace stages, and writes one SENDACK
   for every input item.
3. PING best-effort touches presence before PONG; RECVACK validates identity and
   positive message ID before delegating best-effort delivery feedback.
4. The reserved terminal EVENT is strictly parsed into fixed-size redacting
   proof values, validated by the terminal controller, and sealed with its ACK
   under the exact session write lock; every later inbound frame is rejected
   before reaching any usecase.

## Invariants and Failure Semantics

- Missing activation UID is an authentication error; the adapter never writes
  CONNACK directly.
- Route movement and send-deadline failures map to retryable node-not-match
  SENDACK while observations keep timeout distinct.
- Unauthenticated SEND becomes a SENDACK result rather than a raw protocol
  error. Malformed/stale RECVACK is ignored without protocol noise.
- Batched result cardinality and order must match inputs.
- Send hooks run after permission inside the message usecase, uniformly across
  all entries. Payload ownership remains immutable until lower durable/async
  boundaries copy it.
- Terminal capability, nonce, digest, request, and ACK payloads are never
  exposed or logged. The usecase session identity must equal the authenticated
  gateway session before sealing.
- Deadline failures may add bounded permission, pre-append, submitter, and
  pre-submit budget timings to the existing warning. Planned shutdown fences
  suppress only identity-bearing cancellation warnings, not observations.

## Read First

- [Gateway handler](handler.go)
- [Presence mapping](presence.go)
- [SEND batching](batch.go)
- [DTO mapper](mapper.go)

## Update Triggers

Update this file when session mapping, activation rollback, frame support,
SENDACK error mapping, batching/alignment, trace stages, or import boundaries
change.
