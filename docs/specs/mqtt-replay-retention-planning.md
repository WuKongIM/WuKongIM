# MQTT coherent replay retention planning

Shared-content reclamation first captures an accepted Channel replay anchor,
then reads the lowest source-owned consumer floor through a fresh Slot barrier
and one pinned primary/index view. The result is a bounded retention plan, not
permission to physically delete: the chosen decision must still enter the
replicated Channel source state and preserve repair/readiness proof boundaries.

## Admission ordering

A new binding commits unknown-boundary Preparing before its second fresh source
protection confirmation fixes StartAfter. A retention attempt captures its anchor
before reading bindings. If the new binding commits before that read's snapshot,
it is either a zero floor or a protected known boundary. If it commits later,
its subsequent fresh Channel confirmation observes a committed tail at least as
high as the previously captured anchor. Existing binding floors only advance;
Removed tombstones never resurrect. Thus a delayed retention plan cannot acquire
a new consumer below its captured anchor through supported admission paths.
The order is mandatory: reading consumers before capturing a newer anchor would
permit a newly admitted consumer to be skipped. Historical-start/backfill
subscriptions would require a different admission protocol and are unsupported.

Preparing, Active and Removing all participate. Session-ended Removing remains
an obligation until separate source removal acknowledges it. Unknown floors
block reclamation; no age, timeout or replica-local absence substitutes for proof.
The minimum is capped by the accepted anchor, never by local speculative copy.
Full placement and write-fence identity are checked again before returning.

## Storage and boundedness

The existing retention index 4 and MQTTReadSourceRetention are reused; no new
schema, command, read kind or wire version is introduced. Retention pages pin a
snapshot even for direct storage callers. Each visited index entry must decode
and match one valid primary row from that same snapshot. Missing, stale or corrupt
witnesses fail instead of being skipped. At most limit+1 witnesses are checked;
a first-page limit of one is enough to prove the minimum (or explicit absence).
This does not audit arbitrary missing indexes or all rows in the database;
transactional index maintenance and intact storage remain required invariants.

Each usecase attempt makes two fresh placement reads, one accepted-prefix read
and one first-page consumer read, under one deadline. No subscriber scan, cache,
per-consumer goroutine, metadata write or shared-content deletion is added.
Without an accepted anchor it returns no reclaimable range and skips consumers.

## Failure inventory before code

1. Mixed primary/index snapshots, stale/missing/corrupt index witnesses, malformed
   keys, unbounded skipped rows or a resumed cursor claim a false minimum.
2. Unknown or Removing consumers disappear; an empty incomplete page, unrelated
   collections, foreign source/identity or inconsistent continuation prove absence.
3. Consumers are read before the anchor; a concurrent first subscription starts
   below a later deletion boundary. A stale pre-registration source tail is reused.
4. A future/foreign anchor, changed placement/fence, cancellation or authority
   failure is returned as a usable plan. A speculative local copy expands it.
5. A large subscriber population forces full fanout; result aliases mutable port
   buffers; no-anchor/invalid requests invoke unnecessary work.
6. Real cross-node Slot reads fail to preserve unknown preparation or ACK gaps;
   controlled test admission is confused with complete product delivery.

## Frozen context

Source `c9e4858f6e2f60ffe1a4e595c92b76953f56032f`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `8b723e6f1402c2361a872e3a6e4021ac4146782ebc9b416cb7b9cc9787bb9d75`
- `pkg/db/meta/FLOW.md`: `ea69b4c3c809ff1430eaa0d1637d06a9d5c6f52deb9a6a1d6278184607dc0e16`
- `pkg/channel/FLOW.md`: `7ae69d0c02737dee2ad08839ed154f9dcb46bd060f594a938fc37b9562833f19`
- `internal/usecase/mqttsession/FLOW.md`: `bb9c3f83ef22ee952eeb2a4c504bd581e8b34bd320710029240a6749ff1551d8`
- `internal/app/FLOW.md`: `e476f43da3e4452393b795384de71c3b362d4b1829e6a7c25f3348e641fbec4e`
