# MQTT qualified backlog range receipts

## Need and contract

Aggregate cursor counts cannot identify whether a later-expired or newly No-Local
publication was previously charged. Re-evaluating current options at window debit
can underflow or leak the Session quota. Preserve original charged positions and
byte amounts as bounded range receipts, separate from current send permission.
This implements the approved cursor's range-accounting responsibility, not another
message body table or per-message inflight creation.

Metadata table 24 (`mqtt_delivery_cursor`) System **1** holds an auxiliary receipt
keyed by its full cursor tuple and range From. A key-bound version-1 fixed envelope
stores From/Through, captured subscription revision, evaluation time, NextFrom,
and at most 256 sorted `(source position, charged bytes)` pairs. No payload,
credentials, PacketID or mutable message content is duplicated. Only ranges with
positive charged counts allocate receipts; empty coverage creates no auxiliary row.
Cursor optional columns **25/26/27** store accounting version/head/tail. The optional triple must be complete whenever present. Version 0
retains legacy semantics; version 1 requires receipts. Existing inflight work may
remain during the upgrade, but no unadmitted legacy backlog may be guessed.

Command 82 operation **4** (AccountQualified) installs receipts, counters and quota
termination in one existing Session-Slot commit. It checks exact subscription
revision as well as owner/parent fences. The caller proves protected committed
coverage and qualification. Receipt totals must exactly match count/byte additions;
coverage is at most 256 positions. Once opted in, legacy accounting cannot bypass
receipts. Same-batch writes observe the prior tail; exact retry never appends twice.

Window admission consumes the first outstanding charged position and its exact
bytes. Advance verifies debit against a bounded receipt; neither can skip charged
positions based on changed options/time. Consumed ranges unlink atomically, while
ACK still uses the frozen inflight bytes and preserves earlier gaps. Current send
QoS/No Local/expiry and permission remain business policy; these stored charges
cannot authorize sending. New read kind **18** returns the Session, exact cursor
and current head receipt from one pinned snapshot, rejecting a missing witness.
Normal cursor reads retain their response shape. SourceDrain releases one bounded head per call and reports explicit pending
until the fixed end is reached; Removing intent retains unfinished cleanup.

## Compatibility and bounds

No new logical table or RPC service is allocated. All participating writers/tools
must match before operation 4/read kind 18 are used; old peers reject them. New
cursor columns are optional for old rows, but old writers cannot maintain the new
System records. Rollback needs a pre-feature backup. Hash-Slot snapshots preserve
row/index/System spans together. Inspection exposes receipt format/head/tail, and
the bounded read exposes receipt content; offline MQTT JSONL transfer remains a
separate required deliverable. Receipt count is bounded by charged backlog plus
at most one partially consumed range per cursor, not arbitrary skipped history.

## Failure inventory before implementation

- Ambiguous upgrade with legacy unadmitted counts; legacy accounting bypass;
  changed options/parent/owner causes a receipt for another decision.
- Wrong range, duplicate/out-of-order/out-of-range positions, >256 items or covered
  positions, byte overflow, mismatched totals, or mutable caller slices change a commit.
- Partial version columns, headless byte debt, inconsistent successor count/bytes/
  revision/time, missing/corrupt/cross-key head/tail, a loop/backward link or malformed envelope
  becomes empty backlog; failed neighbors leave a partially linked receipt.
- Exact retries append twice, zero-charge pages allocate indefinitely, quota
  termination loses receipts, or snapshot/restart omits auxiliary state.
- Admission skips a charged item or changes bytes; advance releases more/less than
  its receipt, crosses unbounded ranges, or loses ACK gaps and current inflight.
- Point reads are not pinned with Session/cursor, return unrelated collections,
  or downgrade to old read semantics across RPC. Cleanup treats partial as complete.

Tests precede implementation at the approved metadata/FSM/routed cluster seams.
The [consumer accounting usecase](mqtt-consumer-accounting.md) now supplies source
qualification. Offline scheduling, product listener and full process acceptance
remain required after these prerequisites.

## Frozen context

Source `bbd46deb2db0a3b80307efbe6bef48dc76e8f259`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `3560287fef837ef40dcf04754ac1037a28ec85d26dae7748f465ab9524c624fe`
- `pkg/db/meta/FLOW.md`: `d2687858c0fa93634a91b3d9d24e0dbef5915e1b103b1d7126a62d738a9cf593`
- `pkg/slot/FLOW.md`: `b65fd9718120b50d00e0ea95280188dac52d8a5c8517d35c525119a0470ab01e`
- `pkg/cluster/FLOW.md`: `578ad5ec4db13eadf96b7c0ad34fc9d0ffbeb7f4dd92c7713067858a23d33677`
- `internal/usecase/mqttsession/FLOW.md`: `61311c61e2a227527e37437badf4e31d0f3a26f7c11107b84378d904c9da0fcd`
