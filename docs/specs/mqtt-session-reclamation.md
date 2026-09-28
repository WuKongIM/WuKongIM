# MQTT Session child reclamation

Status: the storage transaction, Slot command and Node facade are implemented.
Automatic candidate discovery, owner-quiescence orchestration, bounded background
scheduling and product process acceptance remain required. This is part of the
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
fallback. No new RPC service, metadata table or secondary index is introduced.

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

Next, discovery must include old Session rows without relying on an unbackfilled
new index. Scheduling must rotate 256 hash Slots under bounded work and preserve
unresolved owner duties. Product acceptance must demonstrate autonomous cleanup
and continued source/Will recovery. Source-tombstone retirement, full JSONL/restore
composition and scale qualification remain part of the full objective.
