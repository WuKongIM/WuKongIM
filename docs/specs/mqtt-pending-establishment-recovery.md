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

The subsequent orchestration milestone below adds final activation and shared
dispatch. Projection evidence above retains its original, narrower scope.

## Orchestration failure inventory

Before implementing final activation and dispatch, cover both real projections
followed by failed/rejected/lost final writes. Recovery preserves every original
option and identity and clears the pending index only after a definite Active
commit. A fresh Active row can confirm a lost successful reply without another
write. An unrelated parent update can be reread; changed child/Owner/lifetime,
expired Offline state, malformed evidence and cancelled callbacks cannot activate.

Definite receive denial or incarnation change before/after projection uses the
existing exact-Owner End path with reason Revoked. Authority failures are not
denial. End errors and late results produce no confirmation; a stored Revoked
ending may retry exact isolation/ending after a lost reply. Recovery cannot
follow a successor, reinterpret quota/expiry ending as revocation, or emit SUBACK.

## Background activation and composition

SubscriptionEstablishment rereads the current Session and complete child from
one authoritative point read. It uses only an unexpired Offline captured Owner,
confirms real inbox/group preparation, then rechecks intent and permission before
one final Preparing-to-Active CAS. Existing Active evidence can confirm a lost
reply without another write. Definite denial or permission-incarnation change
uses exact-Owner End with reason Revoked; unknown errors retain work. Ended
Revoked rows can retry the exact ending port, never infer isolation from storage.

The existing consumer scanner dispatches Preparing and Removing body-free keys
through the same bounded cohort and per-Slot cursors. App composes both offline
projection ports before worker construction. No queue, per-Session goroutine,
table, column, encoding or RPC is added. The thirteenth fixed consumer event is
`subscription_establishment_confirmed`; it counts successful observations,
including retries, not unique subscriptions. Failed or late turns clear outcomes.

The three-node composition case proves activation of both disconnected intents
before the first binding, followed by existing removal and permission tests.
Its earlier projection assertion required correction: a replay-pending result
may precede anchor creation while native replica checkpoints catch up. A bounded
probe found AnchorPosition=0, and removing the new recovery scenario reproduced
the same failure. The test now advances preparation until the leader reports an
anchor before requiring missing learner content and eventual all-replica coverage.
No product confirmation requirement was weakened.

The process scenario lives in `test/e2e/mqtt/subscribe_recovery`. Its two inert
comment failpoints interrupt committed intent and final background activation.
The final-write case sends a native message while activation is held, proving
that retry retains the original start rather than moving to the later tail.
The ordinary binary contains neither a failpoint runtime nor an added endpoint.
See the [orchestration evidence](../reports/mqtt-pending-establishment-recovery.json)
for exact verification scope, commands, fixed source context and limitations.

### Process acceptance investigation (open)

The first eight-case process run passed seven cases. The single-node cluster
inbox/final-write case activated offline and delivered its first original message,
then observed EOF while awaiting a fresh native message after PUBACK. A focused
three-run loop reproduced EOF twice. Fixed public MQTT counters confirmed
activation without quota/revocation ending; they did not identify the close path.
ACK/version contention, replay readiness and connection lifecycle are hypotheses,
not a diagnosed cause. Full process acceptance remains unverified until the
failure is explained and the unchanged business assertions pass.

A conditional ACK-CAS probe passed two short runs without reproducing EOF. A
later close-call-site/ACK-error probe passed three short runs; these successes
are not a fix or proof of the cause. An eight-publication diagnostic instead
hit the 100-second receive deadline twice, with no ACK conflict captured. Its
third repetition was intentionally stopped and its owned node group terminated;
that aborted repetition is not product-failure evidence. The original short
acceptance scenario and all its assertions remain intact. Temporary server and
test probes were removed; evidence logs retain their bounded observations.

After probe removal, the original product binary passed one unchanged short
repetition. This does not erase the earlier EOF failures or establish a fix.


### Subsequent candidate verification

The [bounded ACK concurrency change](mqtt-outbound-acknowledgements.md) fixes a
separately reproduced renewal/ACK rejection. Its candidate passed all eight
subscription process cases, preserving offline activation, original source start
and message identity with no foreground SUBSCRIBE retry. The original seven-of-eight
report remains historical evidence. This successful matrix does not identify the
cause of the intermittent EOF; that connection investigation and the separate
latency/scale requirements remain open. See [candidate evidence](../reports/mqtt-ack-concurrency.json).
