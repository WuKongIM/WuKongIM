# MQTT unsubscribe sealing and backlog release

`SourceDrain.Seal` runs under an admitted current owner for one exact Channel
binding. It reads the binding, then a current closed subscription (or newer
subscription generation), then a pinned Session/cursor. Closed intent prevents
new accounting and window admission through the existing Slot FSM. Seal the
binding's end at that cursor's AccountedThrough, preserving its start and progress;
this is the complete durable obligation, not a new read of the moving source tail.
Unaccounted future source positions are no longer matched after unsubscribe.

Commit Removing and the fixed end before releasing unadmitted backlog. Legacy cursors use one
MQTTWindowAdvance to move WindowThrough to the seal and subtract Pending minus
Inflight counts/bytes. Qualified cursors read one pinned accounting head and debit
only its exact original charges, stopping before the next range. Each turn uses
one Slot commit and returns ErrSourceDrainPending while the fixed end remains;
Removing intent retains the remaining work for a later reconcile. It leaves every inflight exchange, PacketID, delivery order, immutable
content reference and ACK gap untouched. Empty nonqualifying ranges still advance
the window. Already sealed retries preserve the first end; current Session CAS
rejects concurrent ACK/accounting/owner changes and a later turn rereads evidence.
The method returns without waiting for outstanding ACKs. SourceProgress and
SourceRemoval independently project completion and discharge responsibility.

Interrupted preparation must also be cancellable. Unknown source boundaries
need fresh replicated protection confirmation before fixing one empty start;
missing cursors require an explicit cancellation-only initialization after
closed intent, not acceptance of absence as general completion evidence. That
storage operation must reject active admission and keep ordinary Init unchanged.
The cancellation cursor can never admit new messages for the closed generation.

No message-body scan, worker, cache or new table is needed; qualified debit
checks at most 256 charge pairs and storage verifies its next head witness. App owns composition;
proof comes from current Node ports, not local storage or projection callbacks.
Normal removal does not require current receive permission and cannot terminate
the Session or discard already-started QoS exchanges. Product projection/inbox
discovery, automatic reconciliation and wire acknowledgements remain part of
the complete MQTT implementation.

There are at most six point reads, two binding CAS writes, one cancellation
initialization and one window mutation; known cursors need fewer operations.
Each call shares one deadline and joins its owner scope before returning. A
binding already removed as Drained can complete a retry whose earlier unsubscribe
reply was lost. When a newer subscription proves closure, its exact revision may
become the binding's IntentRevision; equality on reread is valid, regression is not.

Slot command 69 gains explicit operation 3 (CancelInit), preserving the version-1
envelope and row format. Older nodes reject it; participants must run matching
binaries. The concrete protector owns native source-generation encoding; the
usecase validates the returned exact incarnation through its existing contract.

## Failure inventory before implementation

1. Active/preparing subscription, wrong UID/topic/source/operation/authorization,
   stale owner or another lifetime can close or release a current obligation.
2. The captured end moves on retry, a concurrent admission is skipped, or a
   source-Slot failure still releases Session backlog before the seal commits.
3. Unsubscribe erases inflight content/order/PacketIDs, advances through an ACK
   gap, leaks Session quota, or affects a new same-topic subscription generation.
4. Accounting and cursor/session revisions are incoherent; incomplete/missing/
   extra read collections or absent authority are accepted as safe completion.
5. Lost seal/window replies cause double decrement, recreate a cursor, reset its
   start, release a successor, or prevent a resumed owner from continuing.
6. Parent cancellation, lease expiry, takeover, callback panic, clock regression,
   overflow, CAS conflict or malformed receipts leave operations admitted or
   report success without durable proof. Calls must be bounded with no retry loop.
7. Unknown-boundary or cursorless preparation can never be cancelled; cancellation
   initialization opens admission, resets an existing cursor or permits ordinary
   late Init/Account on a closed subscription.

Tests use the already approved usecase, storage/Slot and real app cluster seams.

## Frozen context

Source `ac2b1fbaf087a09796ff210f9e64cd38e6d6dd04`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/usecase/mqttsession/FLOW.md`: `d8536800f9e884352aafcfdf634abbd1cfa647bba00089f8f13c9d426f6d24a2`
- `internal/app/FLOW.md`: `dad6bb21eebd4d47d7c9338af99252caccf385a8dea0797f355c6aaf2df2b7a0`
- `pkg/db/FLOW.md`: `3560287fef837ef40dcf04754ac1037a28ec85d26dae7748f465ab9524c624fe`
- `pkg/db/meta/FLOW.md`: `1e820ab2a012ae730f25988f9ed603258799ea8601ab95e48e89bb766d8c2f87`

`SealGroup` now supplies bounded discovery for current closed group intent,
including preparation interrupted before its first binding. See
[group removal discovery](mqtt-group-removal-discovery.md); exact-source `Seal`
continues to reject a missing binding rather than infer empty work.

The [offline drain metadata contract](mqtt-offline-drain.md) now permits closed
intent cleanup while Offline, retaining exact owner/revision fences and inflight
work. `SourceDrain.Seal` still requires local live execution;
[ReconcileClosed](mqtt-closed-source-maintenance.md) now captures authoritative
closed intent independently of local Owners and runs in consumer maintenance.
Final subscription/UID completion and pre-binding discovery remain required.
