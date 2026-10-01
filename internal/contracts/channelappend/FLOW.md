---
scope: package
summary: Defines immutable entry-independent Channel append, authority, result, and committed-envelope contracts with stable error families.
---

# Channel Append Contracts Flow

## Responsibility

`internal/contracts/channelappend` contains the DTOs, sentinel errors, reasons,
and clone helpers shared by entries, message usecases, node RPC, cluster
adapters, and the Channel append authority runtime.
It does not perform permission checks, durable append, routing, or delivery.

## Boundaries

- Route resolution, permission, append execution, recipient discovery, Online
  Delivery, and gateway push live outside this contract package.
- Recipient plan/route/push DTOs belong to `internal/contracts/onlinedelivery`.
- Concrete entry, app, cluster, and Channel runtime types must not cross this
  boundary.

## Main Flows

1. Entry-neutral `Send`/`SendBatch` commands reach the authority router and
   aligned append contract.
2. Fresh success yields an immutable committed envelope for post-commit
   recipient planning.
3. Stable reason and error families return to entry adapters for SENDACK mapping.

## Invariants and Failure Semantics

- Hot-path payload and scoped-recipient slices may be borrowed only while every
  participant treats them as immutable; concrete durable/async owners copy.
- Publication metadata follows the same immutable ownership through commands,
  messages and committed envelopes. Retry queries borrow body and metadata;
  their comparison excludes only the server-assigned ingress clock.
- A trusted process-local item callback grants append submission only at the origin
  router, including retries. It is stripped before local handoff and never encoded
  in node RPC or stored as publication metadata. Cloning preserves the same callback.
- Authority target carries complete route generation and observed write-fence
  state. Route generation orders cache projection and the durable quorum authority.
- Append requests carry expected authority/leader epochs and an optional exact durable route.
  Nonzero routes require complete epochs and quorum commit; adapters cannot rewrite intent.
- Server-allocated message-ID proof applies to every item and skips only
  existing-ID reads; sender/client idempotency remains mandatory.

`ErrAppendNotSubmitted` proves this complete origin invocation dispatched no new original mutation. It grants no positive publication receipt and must be suppressed after any prior ambiguous submission; error text and deadlines cannot supply it.

## Read First

- [Contract types](types.go)
- [Stable errors](errors.go)
- [Ownership tests](types_test.go)

## Update Triggers

Update this file when DTO ownership, route fencing, append proof, clone behavior,
reason/error stability, or package boundaries change.
