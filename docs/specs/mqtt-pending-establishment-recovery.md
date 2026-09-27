# MQTT pending establishment recovery

The approved MQTT design requires durable Preparing subscriptions to finish after
their initiating connection disappears. Existing metadata commands already allow
cursor initialization and Preparing-to-Active for a current Offline Session; no
new table, encoding or RPC is needed. Live foreground projection keeps its Owner
execution gate. Offline preparation uses a separate bounded scope, fresh routed
reads, the captured Owner tuple and the complete original subscription intent.
It grants no network execution, SUBACK or isolation proof.

## Failure inventory, before implementation

1. Group interruption before registration, after unknown registration, after the
   known boundary, after cursor Init, or after binding activation must recover
   without a local Owner. A known start never moves to the newer Channel tail.
2. Inbox interruption before UID qualification, after qualification, during one
   directory page, after per-source replay, or after discovery completion must
   reuse durable checkpoints. Future-source qualification precedes discovery;
   each source still needs all-replica confirmation before progress advances.
3. Projection recovery must retain generation, operation, authorization version
   and every option. A changed child, Owner, UID, Session lifetime, source
   generation or authorization incarnation must fail without adopting a successor.
4. Offline preparation must reject Active, Ended, expired or absent Sessions,
   mismatched requests, cancelled calls and regressed clocks. Foreground APIs
   must retain their existing live-Owner requirement. An Offline row alone never
   grants message delivery or permits another connection to be isolated.
5. A lost write reply, callback panic, malformed/partial read or failed replay
   confirmation cannot produce a projection receipt. Exact recovery remains
   discoverable and idempotent; no generic conflict/unknown-error retry is added.
6. Initial directory scanning remains one bounded page per turn. Cancellation
   after a dependency returns must prevent the next effect. Recovery never
   allocates per-session workers or unbounded queues.
7. Final activation must recheck the captured Offline parent, exact Preparing
   child and fresh receive permission, then use one parent-revision CAS. A
   failure after successful projection must leave the subscription index usable.
8. Shared worker discovery must dispatch Preparing with bounded, body-free keys
   while preserving Removing recovery. Completion observations must distinguish
   activation from removal and suppress late/failed receipts.
9. Process acceptance must interrupt a real Paho SUBSCRIBE after durable intent
   and before final completion on single-node/three-node 256-Slot clusters. It
   must observe background completion before reconnect and then receive through
   the original subscription without another SUBSCRIBE. Controlled interruption
   is not evidence of abrupt crash or partition recovery.

## Implementation boundaries

Offline projection ports are the first dependency. Their failure tests use the
real metadata implementation with controlled source/authorization/reply faults;
this isolates exact identity and write-window failures before process acceptance.
Pending-intent orchestration, shared scheduling and process acceptance follow.
Revocation must use the existing exact-Owner Session termination policy where
required, rather than silently reviving a previous membership incarnation.

The full MQTT implementation goal remains active until the approved fault and
scale requirements, cleanup, isolation, restore and rollout work are complete.

## Verified projection milestone

GroupProjection and InboxEstablishment now expose EstablishOffline. Shared
preparation checks the captured, unexpired Offline Session and complete child;
foreground methods retain local Owner admission. InboxSources.PrepareIntent
pins the same Owner/child through nested operations. Ordinary future-source
admission keeps its existing separate ownership behavior. The new preparation
ports leave subscription intent in Preparing and do not send SUBACK.

The [evidence report](../reports/mqtt-offline-establishment-preparation.json)
records saved-source compile-time RED, a failing negative control without nested
fences, passing focused/full usecase race tests, deadline integration and existing
real single-node cluster composition. It includes commands and source/log digests.
The negative control was run after implementation and is labelled accordingly.
Dependency transfer failures are excluded from product failure evidence.

Pending-establishment orchestration, Preparing dispatch in the shared cohort,
final activation, revocation ending and new product-process fault acceptance
remain outstanding. Their requirements above remain part of the full goal.
