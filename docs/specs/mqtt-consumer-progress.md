# MQTT consumer completion projection

Shared replay reclamation needs authoritative consumer progress before selecting
a source floor. `SourceProgress.Reconcile` reads one exact source binding, then
uses the existing pinned Session/exact-cursor read through current Slot authority.
It projects only the cursor's contiguous completed prefix with a newer cursor
revision, preserving source/subscription incarnations and immutable boundaries.
Window admission, accounting and an out-of-order ACK cannot skip the earliest
outstanding gap. An unchanged floor causes no source-Slot write; several cursor
advances may coalesce into one binding CAS. A sealed removal end caps progress.

Explicit Session-ended state or a newer Session lifetime may move an old binding
to Removing with a Session decision revision. Lease expiry, elapsed message age,
offline time, missing Session/cursor or unavailable authority cannot discharge
the obligation. Existing Removed tombstones remain immutable. This operation
never marks Removed or manufactures the separate source-release acknowledgement;
normal removal/drain and final source-side removal remain subsequent lifecycle
work. In particular it is not a shared-content deletion API or a GC certificate.

Each turn has a bounded deadline, two point reads and at most one conditional
write, with no per-consumer worker or retained cache. Reads, cancellation and CAS
results are checked at effect boundaries. A lost reply is recovered by rereading
the durable binding, never by resetting or blindly retrying the previous value.
No metadata schema, Slot command or RPC format changes are required.

## Failure inventory before implementation

1. Accounted/window progress or a later ACK skips an earlier unacknowledged
   exchange; repeated visits generate writes without advancing the safe floor.
2. Foreign UID, Session/subscription/source generation, topic, authorization,
   cursor revision or start boundary is accepted; missing/extra/incoherent read
   results are mistaken for proof.
3. An unknown preparing boundary, offline Session or expired lease becomes an
   empty consumer; absent metadata, an RPC error or a stale view authorizes GC.
4. Session replacement/end loses a pending removal obligation, creates a source
   release acknowledgement, removes the binding index early or resurrects a
   tombstone. Normal removal progresses past its immutable end.
5. Concurrent binding writes, changed CAS receipts, cancellation, clock regression,
   revision overflow or uncertain responses report uncommitted progress.
6. Restart/retry loses a committed projection; cross-node authority is bypassed
   or a Session ACK and source projection accidentally become local-only work.

## Frozen context

Source `c7469eadf3df727b6e780a69f549249cfa0182cf`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `8b723e6f1402c2361a872e3a6e4021ac4146782ebc9b416cb7b9cc9787bb9d75`
- `pkg/db/meta/FLOW.md`: `ea69b4c3c809ff1430eaa0d1637d06a9d5c6f52deb9a6a1d6278184607dc0e16`
- `internal/usecase/mqttsession/FLOW.md`: `31ab148f5a38f46cef8f7f128de7b901792254f90fd9c0b72a3449a748015f9b`
- `internal/app/FLOW.md`: `33997eea283c8d11091604ef1658b1e70fcd15e88e6452a75f762565df3ba412`
