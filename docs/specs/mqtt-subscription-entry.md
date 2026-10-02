# MQTT subscription request completion and packet mapping

Source `9fbadb449`; applicable instruction/FLOW digests frozen before edits in
`/tmp/mqtt-subscription-entry-source.json`. Product admission remains unavailable.

## Failure inventory before implementation

- A normal first group subscription needs more than one replay-confirmation
  turn. A request-bounded usecase facade must resume explicit pending work with
  a fixed retry interval, attempt cap and total deadline. It may repeat only
  ErrReplayPending for Subscribe and ErrSourceDrainPending for Unsubscribe.
  Ambiguous errors, conflicts, denial, cancellation and panic must never retry.
- Each attempt uses the original exact owner and input, reusing existing durable
  intent and authorization checks; no attempt-owned operation or detached worker survives
  the attempt. Waiting owns only a bounded request timer. Runtime maintenance
  retains responsibility for long scans, fairness and disconnected recovery.
- Completion cannot mean success after a late result, malformed receipt, owner
  fence, changed child or failed reply enqueue. Preparing/Removing retain their
  durable responsibilities after timeout; no false SUBACK/UNSUBACK is sent.
- Entry maps canonical exact group/self-inbox topics and all subscription options,
  grants at most QoS 1, and preserves reason order for mixed success/denial/limit
  and unsupported filters. Wildcards/shared filters never reach business ports.
- Invalid packet IDs, empty/oversized batches, invalid options or unsupported,
  repeated/zero identifier properties fail before any subscription effects.
- A bounded exact-owner scope covers the entire control packet through its reply.
  Entry delegates pending convergence, permissions and storage to usecases.
  Unknown outcomes close with no partial packet reply; a preceding committed
  filter remains durable for recovery. Definite per-filter failure stays explicit.
- ACK success wakes delivery after reply enqueue; close wakes cleanup. Failed
  hints cannot roll back success. Unsubscribe does not require receive permission,
  remove membership, erase exchanges or free their PacketIDs.
- CONNACK enables subscription identifiers only when subscription entry and
  delivery/ACK ports are composed. Nil capability remains explicit.
- Independent Paho over real TCP and a 256-Slot single-node cluster must exercise
  actual SUBSCRIBE/UNSUBSCRIBE, source preparation before first business send,
  identifier/options mapping, automatic native-message delivery and durable ACK.
  Process-level acceptance, inbox future sources and offline maintenance remain
  separate required implementation work.

## Implementation and validation

`SubscriptionRequests` defaults to 64 attempts spaced by 25ms, with a total
five-second deadline; maximums are 256 attempts, one-second spacing and one minute.
It retries only the direction's explicit pending error, through the original
Subscriptions API. Every attempt rechecks exact ownership and current intent.
The gateway retains one owner scope through the whole batch and response, then
releases it before its delivery wake. Partial internal success followed by any
unknown result closes the connection with no batch ACK.

`ErrSubscriptionUnconfirmed` is joined to the original cause after observing a
Preparing/Removing intent or before attempting a mutation. The original pending
cause remains usable for convergence, but a subsequent denial/limit cannot be
mistaken for a definitive pre-write rejection. No database/RPC format changes.

- Request-facade RED: missing constructor/options,
  `/tmp/mqtt-subscription-requests-red.log`.
- Unconfirmed-intent RED: missing failure classification,
  `/tmp/mqtt-subscription-unconfirmed-red.log`.
- Entry RED: missing subscription capability,
  `/tmp/mqtt-subscription-entry-red.log`.
- App RED: missing real request composition,
  `/tmp/mqtt-subscription-entry-app-red.log`.
- Whole access race suite passed (2.048s):
  `GOWORK=off go test -race -p 2 ./internal/access/mqtt -count=1`,
  `/tmp/mqtt-subscription-entry-access-race.log`.
- Focused request-completion integration race tests passed (3.213s):
  `GOWORK=off go test -race -tags=integration -p 2 ./internal/usecase/mqttsession
  -run '^TestSubscription(Requests|FailureDistinguishes)' -count=1`,
  `/tmp/mqtt-subscription-control-race.log`. They cover stable pending intent,
  bounded exhaustion, cancellation/deadline, unknown/conflict/denied/panic results,
  wrong-direction pending errors and option validation.
- Whole Session usecase race suite passed (51.849s):
  `GOWORK=off go test -race -p 2 ./internal/usecase/mqttsession -count=1`,
  `/tmp/mqtt-subscription-entry-usecase-race.log`.
- Real Paho / TCP / 256-hash-Slot race integration passed (8.063s):
  `GOWORK=off go test -race -tags=integration -p 2 ./internal/app
  -run '^TestMQTTSubscriptionEntryPahoSingleNodeCluster$' -count=1 -timeout=2m -v`,
  `/tmp/mqtt-subscription-entry-paho-race.log`. Rerun to regenerate
  `mqtt_subscription_entry_evidence`. The first group's takeover setup uses the
  existing direct usecase fixture; the subsequent fresh empty group's complete
  SUB/UNSUB and delivery run through Paho. Mixed SUBACK reasons, granted QoS 1,
  persisted options/identifier, denial without runtime creation, native-message
  delivery, PUBACK after unsubscribe and no subsequent delivery are verified.

This composition supports the real group projection. The entry maps self-inbox
requests, but full inbox projection/future-source admission is still required
before product admission. No default-on listener or process acceptance is claimed.

FLOW render/check passed (86 compliant, zero invalid, nine existing length
warnings); frozen source/digests and `git diff --check` passed.
