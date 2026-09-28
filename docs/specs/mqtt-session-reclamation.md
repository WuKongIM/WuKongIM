# MQTT Session child reclamation

Status: the storage transaction, Slot command and Node facade are implemented.
Bounded indexed discovery and durable historical-row backfill are also implemented.
Exact-owner orchestration and shared background scheduling are now composed;
process acceptance and its scope are recorded below. Source-tombstone retirement
remains separate full-objective work. This is part of the
approved [MQTT design](mqtt-im-access.md), not completion of its cleanup contract.

## Authority and scope

`ReclaimMQTTSession` addresses one namespace/ClientID through its existing Session
Slot. A current parent revision must prove every requested lifetime ended: the
requested generation is below the current generation, or equals an explicitly
Ended generation. Active/offline current lifetimes, missing parents, future
lifetimes, stale revisions and regressed update times reject without writes.

The operation deletes old subscription intents and their recovery indexes,
delivery cursors, qualified accounting receipts, inflight rows and send-order
indexes. It retains the Session's UID binding, owner/generation fences, allocators,
end reason and Will reference. A newer live lifetime keeps its own counters and
children. Ending-lifetime counters become zero only with final cleanup of that
same lifetime. It never deletes source/UID bindings, detached Wills or shared
message content, and never advances remote completion/protection projections.

Persisted Ended state, including quota termination, does not establish owner
quiescence. Callers still need the existing exact-owner isolation contract before
claiming execution has stopped. This metadata result is not isolation or shared
content GC proof. In particular, source tombstones cannot be deleted merely
because these Session children disappeared: a durable source-side fence must
first reject the original revision-zero Preparing request.

## Bounded deterministic transaction

Each proposal fixes `through_generation`, expected Session revision and update
time. It selects at most 65 ordered primary candidates and removes at most 64
subscriptions per step. Decoding is strict; malformed keys/values abort the whole
batch. Recovery-index keys are deleted from each decoded row in that same commit.
No list of all subscriptions or message bodies is loaded.

Selection merges the atomic apply batch's point overlays. Deleted primary
prefixes are retained as range masks and skipped by seeks, so consecutive cleanup
commands produce the same results whether Raft applies them separately or in one
batch. Candidate memory stays at 65 keys; overlay/range checks are bounded by
operations already admitted to that apply batch. This is a structural work bound,
not a measured scale/latency qualification.

An incomplete page changes the Session revision but leaves the durable completion
marker unchanged. After a lost/partial reply, the caller reads current authority
and proposes another bounded page. Deletions themselves are durable progress; a
restart starts again at the first remaining key without storing a topic cursor.

When no requested intents remain, range tombstones remove the complete historical
cursor, accounting and inflight spans, including the inflight order index. They
use the encoded namespace/ClientID and inclusive generation prefix; MaxUint64 is
handled by prefix end, without generation arithmetic overflow. Point and range
overlays mask earlier writes and disk rows for later commands in the same batch.
Logical absence is atomic; physical disk reclamation follows engine compaction.

## Durable and wire identifiers

Table 22 gains optional uint64 column **30**, `reclaimed_through_generation`.
Missing/zero means no certified completion. Only reclamation advances the value;
ordinary CAS and lifecycle transitions preserve it, including Clean Start.
A marker equal to the current generation requires Ended state and zero delivery
counters. The existing envelope version, primary tuple and deadline index stay
unchanged. Zero is omitted from JSON so legacy lifecycle digests retain their
original serialization. Inspection exposes the marker; raw metadata snapshots
and restores preserve it together with the deleted ranges' resulting state.

Slot command **75** uses header version 1, body version 1 and a 16 KiB total
limit. Unknown fields/versions and trailing JSON reject. The Node facade retains
foreground/restore-maintenance admission; the proxy hashes the existing Session
routing tuple and requires committed proposal results, with no result-less
fallback. No new RPC service or metadata table is introduced. Indexed discovery below adds
one secondary index and one per-hash-Slot coverage record.

`Applied` binds revision to expected+1. `Done` binds the completion marker to the
requested boundary. `Unchanged` is a monotonic completion witness and may survive
later Session mutations; it is not an exact-request receipt and authorizes no new
effect. A conflicting receipt cannot claim removed rows or completion. The proxy
rejects contradictory, unknown and oversized response shapes.

Clusters and tooling must use matching implementations before command 75 or a
nonzero marker is written. Old writers do not preserve this new marker and are
not safe for downgrade. MQTT still requires its existing pre-feature backup and
matched rollout; JSONL MQTT state transfer and restored-owner activation remain
separate unfinished requirements.

## Evidence and remaining work

Failure inventory preceded implementation: unfinished/stale parents, partial
pages, live lifetime isolation, detached obligations, same-batch rows, transaction
rollback, snapshot preservation, delayed child writes, forged/regressed markers,
legacy encoding, maximum generation and corrupt input. Distributed tests precede
routing implementation and cover strict wire/result shapes, foreign hash Slots,
FSM replay and the Node maintenance gate. Review then found apply-grouping
sensitivity; a failing 130-subscription regression preceded the range-mask fix.

The real three-node integration test uses TCP, disk and 256 hash Slots, reclaims
65 old subscriptions in pages of 64 and 1, reconstructs the entire cluster between
pages, and verifies authority reads plus idempotent completion afterward. It uses
controlled lifetime decisions; it is not a process-level MQTT scenario, abrupt
crash/partition proof or automatic discovery test. Exact results and source hashes
are retained in [the report](../reports/mqtt-session-reclamation.json).

Indexed discovery includes historical rows through the coverage contract below.
The composed scheduler rotates 256 hash Slots under bounded work and preserves
unresolved owner duties. Source/Will recovery remains independently responsible. Source-tombstone retirement, full JSONL/restore
composition and scale qualification remain part of the full objective.

## Indexed discovery and historical coverage

Table 22 index **3** (`idx_mqtt_session_reclamation`) contains only identities
whose latest ended generation exceeds `reclaimed_through_generation`: the current
Ended generation or the predecessor of a live generation. Ordinary Session writes
maintain it atomically. The cursor is the complete namespace/ClientID tuple in
encoded string order (length, then bytes); it contains no revision or authority.

Table 22 **System 1** stores one key-bound, checksummed version-1 fixed coverage
record per logical hash Slot. It contains the last scanned primary identity and
Done. Missing/incomplete coverage is not empty work: read kind **23** rejects it
with Conflict. A corrupt record fails closed. Raw metadata snapshots include both
the checkpoint and all index entries in the same pinned state.

Slot command **76** accepts only a version-1 body, with a 128-byte total bound.
It resumes after the persisted primary cursor, reads at most 65 candidates and
indexes at most 64 in one atomic batch with the next checkpoint. It merges earlier
point/range overlays, does not rewrite Session values or revisions, and repeats a
completed build without a state change. Concurrent new or changed Sessions behind
the cursor are covered by the ordinary writer. Completion certifies coverage,
never absence of remaining cleanup or owner isolation.

Read kind 23 pins the coverage record and at most limit+1 index/primary witnesses.
It rejects missing, corrupt or stale witnesses, checks encoded order and returns
the exact last cursor on terminal pages too. Node and proxy use the existing
foreground/maintenance gates, current hash-Slot ownership, result-bearing proposals
and authoritative read barriers. Old request JSON omits the new zero cursor field.

All writers must match before backfill or discovery is enabled. An old writer
could omit eligibility after a completed build; this is not a mixed-writer rolling
upgrade guarantee. Rollback requires the existing pre-feature backup. No new table
or Session value column is added by discovery; MQTT JSONL remains unfinished.

Storage tests cover legacy rows with no new index, pinned reads, corrupt witnesses,
rollback, snapshot restore, encoded order, lifecycle changes and same-batch
backfill. A real three-node TCP/disk integration with 256 hash Slots builds 65 rows
in pages of 64 and 1 across graceful full-cluster reconstruction, checks reads from
all nodes and confirms reclamation withdraws a candidate. It is not process-level
MQTT or automatic scheduling acceptance; missing historical indexes are tested in
storage. See [discovery evidence](../reports/mqtt-reclamation-discovery.json).

## Autonomous Session-child cleanup

`SessionReclamation` accepts only a namespace/ClientID hint, reads fresh complete
Session authority and captures one ended-generation boundary. If the boundary is
the current Ended generation, it calls the existing exact-owner `End` with the
trusted Explicit cleanup trigger. That port still quiesces an already-ended row
and preserves its original reason and detached Wills. A second read must retain
the same Owner, UID and Ended state. If the boundary belongs to a replaced
lifetime, it never quiesces or follows the live successor.

One turn submits at most one command-75 page under fresh revision/time, bounded
by five seconds. Changed authority, invalid evidence, regressing clocks, panics,
cancellation and uncertain writes stop without inline retry or completion.
Applied results require the next revision and exact marker; partial progress must
remove 64 intents without changing the marker. Unchanged is only a monotonic
completion witness. All errors clear returned outcome flags.

The existing `ConsumerWorker` cohort now rotates a third stream per led hash
Slot. It retains at most three cursors and one coverage hint per Slot (768 cursors
and 256 hints at the default topology), independent of Session count. Before first
discovery it proposes one bounded backfill page; incomplete pages yield, and a
completed build permits a strict kind-23 read. Failed reads discard the coverage
hint; authoritative storage still checks coverage on every read. Lost leadership
and joined restart clear process hints, not durable progress.

Reclamation keys contain no generation/revision/time or body. They share the
existing `mqtt.workers` admission bound across queued and executing binding,
subscription and reclamation work. Whole-page validation precedes admission;
terminal cursors remain exact for kind 23. Pressure retains the last admitted
identity; unfinished rows remain indexed and retry after wrap. Stop joins the
same scanner and cohort before dependencies close. Callback panic and late success
produce a failed result rather than leaking admission or counting confirmation.

Product composition supplies the Node for discovery/backfill/cleanup and the
existing exact-owner Ender for isolation. Fixed metrics add `reclamation_confirmed`
and `reclamation_index_rows` to the existing 15-event consumer family. They count
observations, including possible retries, not unique Sessions or physical disk
reclamation. All existing stream bounds and configuration remain unchanged.

Failure-first checks cover isolation/authority changes, clocks, malformed reads
and receipts, ambiguous writes, 256-Slot rotation, incomplete/failed backfill,
pressure, lost leadership, late callbacks, panic and joined cohort shutdown. Real
three-node App composition autonomously removes old intents for both Ended and
replaced lifetimes while the successor can still begin execution. Process-level
scenarios use public completion/qualification metrics and fresh Paho/WKProto
traffic for zero-expiry disconnect, offline expiry and Clean Start in both cluster
topologies. They do not inspect tables or prove abrupt-crash isolation, physical
compaction, source-tombstone retirement or scale capacity. The final six process cases passed in 163.137 seconds; both Will regressions
passed in 53.384 seconds. Full usecase/access/App race checks passed after the
acquisition fixes; runtime/metrics and joined-cohort race checks passed on their
unchanged implementation. Results and bounded artifacts are retained in [worker evidence](../reports/mqtt-reclamation-worker.json).

### Reconnect regression found by process acceptance

The initial process run reached public cleanup confirmation, then received EOF
when reconnecting zero-expiry and expired Sessions. A focused acquisition test
using real lifecycle/storage calls reproduced four fresh-lifetime triggers: Ended,
Clean Start after older-generation cleanup, expired Offline and zero-expiry Active.
CONNECT constructed a fresh Session with a zero reclamation marker; the existing
lifecycle guard correctly rejected that regression. The fix preserves the durable
ClientID marker when resetting delivery counters and allocators. The failing
regression preceded the fix; no lifecycle validation was weakened.

Clean Start also intermittently received EOF before cleanup confirmation. A
bounded temporary probe captured a definite commit rejection from revision 3 to
4 under the same generation (the observed row was still Active); a real lifecycle
regression then reproduced a normal DISCONNECT completing between CONNECT's read
and proposal. Both resume and Clean Start failed at that seam before repair.
CONNECT now permits at most three definite-rejection proposals after strict
same-Owner/UID/revision/decision revalidation, rechecking authorization without
reserving another candidate or extending the initial lease. Unknown writes,
successors, changed lifetime decisions, cancellation and clock/lease failure do
not retry. The probe is absent from the repository and final candidate binary.

These repairs concern the captured acquisition failures. They do not establish a
cause for the older initial-SUBSCRIBE or post-PUBACK connection failures.
