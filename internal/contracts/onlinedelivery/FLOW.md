# Online Delivery Contracts

`internal/contracts/onlinedelivery` owns the canonical values that cross the
Online Delivery seam.

- A `RecipientDeliveryPlan` explicitly identifies Durable or Transient delivery
  and carries a bounded set of recipients grouped by exact authority target.
- Successful plan admission transfers shared immutable ownership. Callers must
  not mutate the event, targets, or recipients after admission succeeds.
- An `OwnerPush` groups exact online routes owned by one node. Its result keeps
  accepted, retryable, and dropped routes distinct.
- Clone methods exist for adapters and tests that retain or serialize values;
  the Online Delivery admission hot path does not clone plans.

Subscriber discovery, authority grouping, conversation projection, ACK tokens,
worker queues, and retry state are not part of this contract package.
