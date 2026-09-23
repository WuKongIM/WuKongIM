# MQTT group source preparation

This step connects the routed source-protection primitive to durable source
bindings and Session consumption cursors. It is a required component of the
complete subscription projection, not a `SubscriptionProjection` implementation
or permission to return SUBACK. Shared-content recovery, removal/drain, inbox
discovery/future-person-source admission and permission-incarnation ordering
remain mandatory before the product can compose that final projection.

`GroupSources.Prepare` derives UID and intent from the current admitted owner.
It requires a current Preparing/Active group subscription and rechecks receive
authorization. It never creates a subscription or accepts caller-chosen UID,
generation, source identity or consumption boundary.

Resolve/protect the group source to discover its immutable generation, then
commit an unknown-boundary Preparing binding. Only after that recoverable GC
obligation exists may another fresh protection confirmation select the current
committed boundary. Persist that boundary once, initialize the cursor using an
exact current Session owner/revision CAS, then activate the binding with the
committed cursor revision. A retry preserves the first stored boundary, even
after new messages, owner resume or an ambiguous mutation reply. Parent renewal
may advance the Session revision without changing the subscription intent.

Every binding/cursor write checks the live operation and current intent; final
completion also reauthorizes. Source protection grants no delivery authority.
Cross-Slot authority can change after a read: any resulting
late source row is only a conservative recovery/retention obligation. It never
authorizes delivery or activates the subscription. Removing/Removed bindings
cannot be revived. This step does not release protection or advance accounting,
copy, ACK, completion or inflight progress. Group subscriptions have one source
cursor; a bounded two-row query rejects changed incarnations or inconsistent
extra sources instead of silently resetting progress.

The infrastructure adapter maps a narrow source DTO to fresh runtime metadata,
allocates a server message ID, and calls the routed Channel facade with exact
epochs and route generation. It supplies no retry loop, policy or local-storage
fallback. All work shares a bounded usecase context (five seconds by default)
and owner scope. Cancellation is checked synchronously after clock callbacks
before mutation; delayed context callback propagation cannot admit another commit.

## Failure inventory before implementation

1. A forged UID/intent, own-inbox treated as a group, denied or changed membership,
   expired/replaced owner or canceled operation starts durable projection.
2. The cursor is initialized before recoverable source responsibility exists,
   or a missing/failed protection confirmation becomes a known boundary.
3. Crash or lost replies after unknown binding, boundary, cursor or activation
   commit resets the first boundary, creates another generation or loses work.
4. Renewal is confused with takeover; a stale cursor, Session, subscription,
   malformed receipt, foreign source or changed protection generation is accepted.
5. Concurrent removal resurrects a binding; missing bindings with existing
   cursors, extra group sources or changed incarnations silently recreate state.
6. Dependency errors/panics, invalid clocks, overflow or cancellation leak an
   owner operation, successful partial result or unbounded work.
7. A test-controlled source/permission port or prepared binding is mistaken for
   complete shared replay, full subscription projection or product acceptance.

Tests precede implementation: deterministic fault injection with real metadata
commits, adapter contract failures, and real three-node TCP/disk integration.

## Frozen context

Source `c4586d078`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/usecase/mqttsession/FLOW.md`: `ffe4891a137f68f5c691178edf5247a1c1e2c476d1c34b7834e58040b3e9cd87`
- `internal/infra/cluster/FLOW.md`: `280bbf0f0d5c87e1875e727cb33468c9f41b21490f85c80e4b92c12ace43fa76`
- `internal/app/FLOW.md`: `0d8a01b8041db4a3109bdea5e61c324006e2d6f65bd5638790d6e39acef19e92`
