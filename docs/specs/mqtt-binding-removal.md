# MQTT source binding removal

`SourceRemoval.Reconcile` completes one already-Removing Channel binding through
the existing foreground Slot metadata port. It accepts only an exact key, never
caller-supplied completion or release evidence. It owns no worker or message
storage, and performs at most three point reads and one CAS per bounded turn.

An explicitly ended Session or a newer lifetime can discharge an old binding
whose termination proof was projected by `SourceProgress`. Normal removal needs
a sealed end and a current subscription read proving admission has closed,
followed by a pinned Session/cursor read. The cursor must account exactly through
that end, complete through it, and have no pending or inflight obligations. A
newer subscription generation also proves old admission closed; its own work is
unaffected. Missing state, offline time and lease expiry are not release proof.
[SourceDrain](mqtt-source-drain.md) captures the accounting end after admission
closes and releases unadmitted backlog. This completion operation remains separate.

The per-consumer protection is table 26 on the source Slot. After validating
fresh Session-side evidence, one replicated source-Slot CAS acknowledges release
by setting ProtectionRevision to its new binding Revision, retaining Removing
and its retention/recovery indexes. A subsequent turn revalidates the evidence
and commits Removed, retaining that exact acknowledgement and the primary
tombstone. Any intervening binding revision invalidates the acknowledgement and
requires another source-Slot acknowledgement. Lost responses resume from durable
state; no in-memory receipt is needed.

This specifies the separate replicated **per-binding** source release operation:
it is distinct from the Session-Slot decision and from the final index removal.
No per-consumer record exists in the native Channel log. Its aggregate System 12
protection is unchanged; a binding acknowledgement cannot advance copied-through,
retire an anchor, stop protection or delete shared content. Initial protection
still needs the independent native log activation. Replay retirement continues
to obtain fresh minimum-consumer and committed-anchor proofs. No table, column,
Slot command or RPC encoding changes are needed.

## Failure inventory before implementation

1. Offline/expired/missing/foreign Session or an unproven stored reason releases
   responsibility; replacement releases the new Session or subscription's work.
2. An ACK gap, unadmitted pending work, an unsealed end, or a still-active
   subscription is treated as drained. Cursor/accounting changes beyond the
   sealed end are silently ignored.
3. An incomplete or unrelated read collection, regressed revision, wrong UID,
   operation, topic, authorization, source or start boundary becomes proof.
4. The acknowledgement drops retention/recovery indexes, final removal invents
   an acknowledgement, or concurrent progress reuses an obsolete acknowledgement.
5. Cancellation, clock regression, overflow, CAS conflict, malformed receipt or
   lost response produces false success or bypasses the next authoritative read.
6. Tombstones resurrect, pending removals are lost on restart, or cross-Slot
   routing becomes a local shortcut. Aggregate source/copy proofs are changed.

Tests use the already approved usecase/metadata and real cluster seams. The
normal sealed-end fixture does not claim full product projection is composed.
SourceDrain tests now additionally provide real usecase sealing and cancellation.

## Frozen context

Source `703f493f3af27b2952b7ba3c221acaf43890da9b`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/usecase/mqttsession/FLOW.md`: `14cafe07d4dd88c8dd4e85fc8dc2329edc8b4dc12d694ca9e89c2c09fd07bcdd`
- `internal/app/FLOW.md`: `ff578d45689b648ebd8af8142ee35adea50b04c293d708c2821e655aa162e0e7`
- `pkg/db/FLOW.md`: `3560287fef837ef40dcf04754ac1037a28ec85d26dae7748f465ab9524c624fe`
- `pkg/db/meta/FLOW.md`: `1e820ab2a012ae730f25988f9ed603258799ea8601ab95e48e89bb766d8c2f87`
