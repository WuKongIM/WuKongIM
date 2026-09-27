# MQTT inbox removal

Status: storage, bounded removal usecase and app composition implemented;
product admission and automatic cleanup remain pending.
This extends the approved source-binding and authoritative Slot seams.

## Failure inventory before implementation

- Persist closed UID qualification before scanning Session cursors. Neither
  Preparing qualification nor an absent qualification may admit future sources
  after closed intent; late establishment CAS cannot reopen removal.
- Initial discovery and removal are separate progress. Reusing the completed
  initial cursor would skip old sources or destroy established receipt evidence.
  Removal must continue across bounded pages, lost replies and owner takeover.
- Drain one original source incarnation at a time through SourceDrain. Advance
  only after its fixed accounting end has released all unadmitted charges; retain
  inflight PacketIDs, order, immutable content, ACK gaps and source responsibility.
- Closed Session intent rejects ordinary late Init and Account. Cancellation-only
  Init may add an empty cursor behind removal progress, but creates no backlog or
  exchange. Cursorless bindings remain recovery debt; UID completion cannot prove
  their source release or grant shared-content GC.
- Metadata rejects partial markers, unsupported versions, non-UID use, open-stage
  drain fields, incomplete keys, invalid bounds, phase/cursor regressions, skipped
  initialization and changed completed progress. Normal UID tombstones need drain
  completion; explicit lifetime termination remains a separate discharge proof.
- Preserve legacy rows/JSON when drain fields are absent. Marked rows must survive
  exact command retries, Slot snapshots, replay and inspection. All-or-none fields
  and byte bounds are validated during decoding, not only before writing.
- Usecase completion requires fresh exact owner/intent and completed qualification.
  Denied receive permission cannot prevent safe cleanup. Panic, cancellation,
  conflict, corruption, clock regression and uncertain writes return no receipt.

## Persisted progress

Reuse `mqtt_source_binding` (table 26), with UID-only optional columns 29–32:
`drain_version`, `drain_after_source_id`, `drain_after_source_generation`,
`drain_done`. Version 1 scans the existing Session/subscription cursor prefix in
encoded SourceID/SourceGeneration order; kind is the fixed Channel source kind.
The identity prefix comes from the UID binding key. ProgressRevision witnesses
the current Session revision after completed drains. Initialization commits an
empty cursor before any scan; completed progress is immutable. Initial discovery
fields remain unchanged throughout removal.

These are optional appended columns in the existing version-1 envelope; no table,
index, new command or row-key change. Command 71 carries nonzero fields using
optional JSON fields. Matched readers/writers/tools and a pre-feature rollback
backup are required: old writers can erase optional progress. Product lifecycle,
offline cleanup, unknown-binding recovery and restored-owner fencing remain part
of the full MQTT acceptance contract.

## Verification

The tests first failed without the drain fields. Metadata tests cover independent
progress, initialization, encoded ordering, completion/lifetime-end transitions,
legacy JSON/rows and incomplete/malformed column tuples. The real Slot FSM test
applies command 71, restores a snapshot, replays an exact retry and completes the
same checkpoint. Usecase tests first failed without InboxRemoval and cover two
bounded pages, per-source continuation after lost replies/owner takeover, denied
receive permission, preserved PacketIDs and ACK after unsubscribe. Cursorless
preparation is retained as independent debt; malformed/pending evidence and late
owner effects never mint a receipt.

App integration first failed without its composition factory. The real single-node
cluster (256 hash slots) establishes qualification and existing/future sources,
accounts two messages while offline, admits one exchange, then unsubscribes. It
verifies that only the unadmitted charge is released, the exact exchange remains,
and its original PacketID/order still completes through the real ACK usecase.
This is app/cluster integration: no socket delivery or enabled product listener
is claimed. Evidence includes `unadmitted_released=1`, `inflight_preserved=1`,
`exact_ack_after_unsubscribe=true`, and `wire_transport=false`.

```sh
GOWORK=off go test -race -p 2 ./pkg/db/meta ./pkg/slot/fsm -count=1
GOWORK=off go test -race -p 2 ./internal/usecase/mqttsession -count=1
GOWORK=off go test -race -p 2 -tags=integration ./internal/usecase/mqttsession ./internal/app -run '^(TestMQTTInboxRemoval|TestMQTTInboxEstablishmentExisting|TestMQTTInboxAppenderOffline|TestMQTTInboxAdmissionOffline)' -count=1 -v
```
