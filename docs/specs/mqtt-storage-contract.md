# MQTT storage implementation contract

Message source protection reserves System ID 12; its codec, CAS, protected-read
and retention boundary are specified in [the source contract](mqtt-source-protection.md).
System ID **13** now stores the first exact format-4 activation manifest; its
pending trim fence and committed materialization are specified in
[log activation](mqtt-source-log-activation.md). Local state is not a quorum
receipt or proof of shared-content durability. Product admission and complete
distributed replay remain required before MQTT access can open.

The seventh logical table is message-domain table 2, `mqtt_replay_message`, not a
Slot payload table. Its row, metering index, local frontier and backup-v2 contract
are frozen in [shared replay storage](mqtt-shared-replay.md). All seven tables now
have storage groundwork; the distributed runtime remains incomplete.

The approved [design](mqtt-im-access.md) remains the product contract. This file
freezes durable identifiers and failure cases before implementing each storage
slice. No storage primitive by itself authorizes a cluster session or proves an
old connection fenced. Product activation remains disabled until the complete
replication, recovery and source-protection chain passes acceptance.

## Session row, first slice

`mqtt_session` uses new metadata table ID **22**, primary index **1**, family
**0**, and deadline index **2**. Its primary tuple is `(broker_namespace,
client_id)`. UID is a durable binding in the value, never part of that primary
key. Expiring/ending a session retains the binding and monotonic generations.
Normal writes cannot delete the binding or rebind the ClientID to another UID.

Values use the existing key-bound checksum envelope, version 1, column codec.
Columns 1/2 are the key; value columns 3–25 are UID, session generation, revision,
owner generation/node/boot/connection, lease deadline, state, negotiated expiry,
offline expiry, device flag, receive maximum, maximum packet bytes, next packet
identifier, next delivery order, pending count/bytes, count/byte quotas, Will
generation, termination reason and updated time. Column 26 describes the derived
deadline index: active lease deadline or offline expiry; ended rows have no entry.
Every v1 value column is emitted and required; optional future columns may be read
with defaults. Unknown envelope versions/codecs/flags and invalid values fail.

Session, owner, row revision and delivery order are distinct counters. Row CAS
compares the expected revision, rejects UID rebinding and counter regression, and
supports an exact retry only while that same full result remains current. An
owner identity change requires a new owner generation. Resuming an ended session
requires a new session generation. Higher-level use cases must establish the
takeover proof before proposing ownership changes.

The deadline index includes the complete primary tuple after its timestamp, so
equal deadlines can be paged without omission. Page sizes are bounded; index
maintenance, row replacement and the Slot applied index share one batch. Read
operations here are node-storage operations, not authoritative cluster reads.

Before implementation, verify through the metadata boundary:

- Tuple isolation across namespaces; no token or protocol packet in the row.
- Exact retry, stale/changed retry, UID rebinding, owner/generation regression,
  ended-session resurrection, and same-batch read-your-writes.
- Failure of a neighboring mutation rolls back the row and its indexes.
- Round trip of all fields, strict corrupt/truncated/version/type rejection,
  physical-key checksum binding and optional future-column handling.
- Equal-deadline pagination, changed-deadline index removal, bounded page limits,
  and ended rows leaving the deadline index.
- Registered inspection, hash-slot snapshot/backup inclusion and pinned snapshot
  restoration. Restored live-owner invalidation and offline transfer wiring are
  separate required rollout work; exact snapshot preservation is not permission
  to resume a restored connection.

The other five metadata tables, shared replay keyspace, publication column and
source System identifiers will be frozen in their implementing slices. Existing
and retired IDs remain reserved. This first slice does not claim all seven tables
or cross-node persistence are implemented.

## Slot command for the session CAS

New Slot command ID **67** carries one expected revision and one bounded session
row. The existing command header remains version 1; this command's JSON body has
its own explicit version 1, rejects unknown fields/versions/trailing values, and
is bounded to 32 KiB including the header. No MQTT command is proposed by product
wiring yet. Mixed-version activation must be gated before that changes.

Verify through the public FSM boundary: ordered same-batch CAS results, stale
owner rejection without failing unrelated commands, owned hash-Slot enforcement,
atomic applied watermark, snapshot/replay preservation, bounded malformed-command
rejection and an inspection catalog entry. A successful FSM CAS still requires
the session use case's owner-isolation proof before a socket may become active.

## Subscription row and owner-fenced mutation

`mqtt_subscription` uses table **23**, primary index **1**, family **0** and
recovery index **2**. Its primary tuple is `(namespace, client_id,
session_generation, exact_topic)`. Value columns 5–17 are subscription generation,
target kind, target ID, granted QoS, No Local, Retain As Published, Retain Handling,
Subscription Identifier, authorization version, stage, operation ID, recovery time
and update time. Column 18 is the resulting session revision of this exact child
mutation; it prevents an unrelated session CAS from falsely acknowledging a retry.
Columns 1–4 are the primary tuple. The checksum column envelope
is version 1; all initial value fields are required. Target kinds are user inbox
(1) and group (2); the use case validates canonical topic/target correspondence.

Slot command **68** has a version-1 JSON body, rejects unknown fields and trailing
values, and is bounded to 32 KiB including its existing version-1 command header.
It atomically checks the session revision and full
owner identity, mutates the subscription and increments session revision. It
cannot create a session, change its owner or reset its backlog counters. Exact
retries succeed only while that owner, resulting revision and complete child row
remain current. New subscription generations use the resulting session revision,
which never repeats within a ClientID binding. Creating one intent per command
keeps generation allocation and per-filter outcomes deterministic.

Stages are Preparing (1), Active (2), Removing (3), Removed (4). Preparing and
Removing have a positive recovery timestamp; other stages have none. Creating an
intent requires the active current session generation. Preparing becomes Active
only after the use case establishes source protection and recoverable bindings;
this storage primitive is not proof of either. Active option replacement retains
the subscription generation, target, authorization version and operation ID, so
existing delivery cursors are not reset. Cancellation traverses Removing before
Removed. Removed is a tombstone until bounded cleanup; a later SUBSCRIBE allocates
a fresh generation. Existing work may finish while offline. Ended or older
session generations may only advance cleanup, never activate or create intents.

Failure inventory before implementation at the already approved metadata/FSM
seams: missing session; stale revision or any stale owner identity component;
future/ended/old session activation; session counter loss; changed retry; invalid
stage skips/regressions; generation/target/operation or authorization rebinding;
option replacement dropping existing progress; same-batch visibility; neighboring
failure leaving half a session/subscription/index update; namespace/generation
isolation; equal-time complete-key recovery pagination; stale index removal;
unbounded topic, identifier, page or command; corrupt/missing/type/version/checksum
row values; snapshot/inspection omission; wrong Slot ownership; command replay
and durable applied watermark. Codec tests cover the storage format contract;
product behavior continues to require the process-level acceptance suite.

Recovery pages are bounded to 256 and include the entire primary tuple after the
timestamp. Session-generation scans are bounded to 256 with an exact-topic cursor.
These are node-storage reads. Distributed authority, coherent quota reads,
source-side projection, cleanup and mixed-version activation remain required.

## Delivery cursor and backlog accounting

`mqtt_delivery_cursor` is metadata table **24**, primary index **1**, family **0**.
The primary tuple is `(namespace, client_id, session_generation,
subscription_generation, source_kind, source_id, source_generation)`. Source kind
1 identifies an IM Channel log; source ID must encode its complete Channel
identity (at most 4096 UTF-8 bytes), and source generation is a durable incarnation (at most 128 UTF-8
bytes), not a routing/leader epoch. The infrastructure adapter supplies canonical
identity. UID inbox subscriptions have one cursor for each concrete source.

Columns 8–18 are topic, authorization version, subscription start-after position,
accounted-through, window-through, completed-through, pending messages/bytes,
last mutation revision, last mutation digest and update time. Initial values use
the same version-1 checksum column envelope as the other new MQTT tables. All
initial value fields are required. Optional later fields must append new IDs.

The three progress positions must not be conflated:
`start_after <= completed_through <= window_through <= accounted_through`.
A source position identifies at most one application publication (a logical
message sequence, not a possibly batched Raft entry index). Accounting records
all qualifying reliable backlog, including messages that have
not entered the bounded inflight window. It does not authorize content GC or
confirm network delivery. Future window/ACK commands must preserve gaps and
atomically maintain the cursor, exchange records and session counters.

Slot command **69** initially supports cursor initialization and forward backlog
accounting. It checks the complete session owner and expected revision, current
session/subscription generation and captured authorization version. Cursor,
session revision/counters and applied watermark share one commit. New cursors
start with zero backlog and all progress at one protected source start. Accounting
requires strictly increasing source coverage and adds exact qualified count/bytes;
zero messages cannot add bytes. The caller must prove source coverage and the
counts from committed protected records; a timestamp or callback is not proof.
An increment cannot claim more messages than newly covered source positions.
Quota overflow persists the cursor and explicitly ends the session with the quota
reason in the same commit, preserving the reason and revision for exact retry.
No wall clock, access check or cross-Slot source proof is inferred by storage.

The command's version-1 JSON body is limited to 32 KiB including the existing
header. The cursor stores the canonical request digest and resulting session
revision so an unrelated write or changed retry cannot impersonate completion.
Generation/subscription source scans use complete cursors and pages of at most
256. Snapshot/inspection preserve all progress and accounting values.

Failure inventory before implementation: source/namespace/session/subscription
isolation; missing or stale session/owner/subscription; ended/old-generation
resurrection; source start rebinding; missing source mistaken for empty backlog;
changed/unrelated retry; quota count and byte overflow; arithmetic wrap; backward
or duplicate coverage; more qualified messages than covered positions; account
progress incorrectly releasing history; same-batch overlay and neighbor rollback;
corrupt, truncated or wrong-key values; unsupported body/version/field and bounds;
complete-key pagination, snapshot/replay and inspection; wrong Slot ownership.
Product window execution, gap-preserving PUBACK, source completeness proof and
quota scheduling remain required before the listener can be enabled.

## Bounded outbound exchange window

`mqtt_inflight` uses table **25**, primary index **1**, family **0**, and send-order
index **2**. Primary tuple: `(namespace, client_id, session_generation, direction,
packet_id)`. Direction 1 is server-to-client; direction 2 is reserved for possible
client-to-server state and is not accepted by this slice. Only admitted QoS 1
exchanges have rows. Values 6–23 are subscription generation, source kind/ID/
generation, delivery order, source position, MessageID, MessageSeq, content
version/hash, accounted bytes, QoS, stage, topic, Subscription Identifier,
previous/next PacketID in that cursor's outstanding list, and update time.
Awaiting PUBACK is stage 1. The immutable content reference is not replaced after
admission, including after edits or option changes. Recovery scans use send order
and the complete PacketID tie-breaker, never wrapping PacketID order.

Session optional columns 27/28 add outbound inflight count and configured window
limit. Missing count is zero; limit zero means the initial default 64. The hard
storage bound is 1024; admission also respects peer Receive Maximum. A resumed
connection may negotiate a lower Receive Maximum while retaining more old
exchanges; the runtime must separately throttle retransmissions. Ordinary
lifecycle CAS preserves delivery counters/allocators in the same generation;
new generations start with zero counters, and only delivery mutations change
same-generation accounting/window state.

Cursor optional columns 19–24 add inflight count/bytes, head/tail PacketID and the
last window result's PacketID/order. Missing values are zero. The outstanding
list is ordered by source position. Removing a middle entry frees its window
credit but does not skip the head. Completion is at most one position before the
first outstanding entry, or window-through if none remain. Pending messages
outside the window must fit in the not-yet-admitted source range. No per-message
ACK tombstone or per-offline-client body copy is needed.

Slot command **70** has a bounded 32-KiB version-1 body and supports admission,
PUBACK completion and covered-range advancement. Every command carries session/
owner/revision and exact source identity. An ACK also carries delivery order,
so a delayed internal ACK cannot release a reused PacketID's different exchange.
Admission validates the active current subscription; ordinary unsubscribe does
not prevent completing an already admitted exchange. Cursor/list/session/index
updates and Slot applied progress are one atomic batch. Exact retry uses a
versioned, domain-separated request digest and the cursor's last-result receipt.
Range advancement releases only unadmitted count/bytes; source qualification,
permission, immutable content durability and complete source coverage remain
caller proofs. No operation can infer those proofs from a local callback.

Before implementation, test: persist-before-visible window admission; immutable
content/options and send-order recovery; full window/Receive Maximum; ID wrapping
and occupied-ID skipping; out-of-order ACK and holes; duplicate/changed/unrelated
retry; stale owner/session/order; normal unsubscribe versus generation change;
same-batch admission/ACK and rollback; linked-neighbor corruption failing closed;
count/byte accounting and pending-range invariants; old rows without optional
columns; codec corruption/key checksum/bounds; snapshot and index preservation;
Slot ownership, bounded malformed commands and complete inspection catalog.

## Source-owned subscription projections

`mqtt_source_binding` uses table **26**, family **0**, primary index **1**. The
primary tuple is `(owner_kind, owner_id, owner_generation, namespace, client_id,
session_generation, subscription_generation)`. Kind 1 is a concrete Channel
source: its complete source ID and durable source generation are required. Kind
2 is UID inbox qualification: owner ID is the UID and logical owner generation
is empty. The key component encodes that absence as one NUL byte, because table
key strings cannot be empty; that reserved value is invalid for Channel generations.
Routing uses the source/UID owner, not the Session key, and never depends on a
Channel leader epoch. No Session row is expected on this binding's logical Slot.

Value columns 8–27 are UID, topic, binding revision, originating intent revision,
progress-proof revision, authorization version, operation ID, stage, boundary
known, start-after, completed-through, end-known, end-through, release reason,
discovery-after Channel ID/type, discovery complete, recovery time, update time
and acknowledged source-protection revision. All are required in the initial
version-1 checksum column envelope. Column 28 is a derived retention floor.
Values are bounded to 16 KiB; commands to 32 KiB including their existing header.

Binding revision is a source-owned CAS sequence. Intent revision fences stale
subscription lifecycle projections. Progress revision witnesses a committed
Session cursor or explicit Session termination; it never decreases. Source
protection revision acknowledges a separate replicated source operation, is
monotonic and cannot exceed the binding revision. Metadata cannot establish
these remote proofs by itself: the use case must obtain them through current
authorities before proposing a transition. Source-system operations must use the
binding's monotonic revision to fence delayed create/remove requests. For
per-consumer removal, the separate source operation is a replicated source-Slot
acknowledgement that retains Removing at ProtectionRevision=Revision; a later
validated CAS writes Removed. It changes no native aggregate source protection
or copy frontier. See [binding removal](mqtt-binding-removal.md).

Preparing -> Active -> Removing -> Removed is monotonic; Preparing may also
cancel into Removing. A new missing-key Removed tombstone may precede a delayed
prepare only with explicit Session-ended proof. Existing rows always traverse
Removing to retain cleanup work. Removed never resurrects, even at a larger
intent revision; a fresh subscription has a different generation/key. UID,
topic, operation ID and authorization incarnation cannot change inside one key.

For a Channel source, Preparing may initially have an unknown boundary. Source
protection installs it once; activation requires that boundary, an acknowledged
protection revision and a nonzero Session cursor proof. Completed position never
regresses or changes without a newer progress proof. A normal remove captures a
fixed end, drains or releases obligations through it, and acknowledges the source
release before Removed. Explicit Session termination can discharge all remaining
obligations using its newer authority proof. Removed tombstones remain until a
separate proven cleanup contract permits deletion; TTL alone is insufficient.

UID qualification has no message sequence/protection fields. Its stable Channel
primary-key discovery cursor is monotonic (encoded string length, bytes, type),
and activation requires initial discovery complete. Preparing qualification is
already discoverable by the future person-Channel creation path. That path must
commit UID directory registration before reading qualifications through a fresh
UID authority barrier; a current-conversation list or callback cannot substitute
for this handshake. The source-protection orchestration is still required.

Index **2** contains Preparing/Active rows for bounded candidate discovery,
index **3** contains all non-Removed rows by recovery time plus the complete key,
and index **4** orders live Channel-source obligations by conservative completed
position plus the complete Session key. An unknown boundary is a zero floor and
must block reclamation until resolved. UID qualification is absent from the
retention index. Removed rows leave all three indexes but remain addressable.
Pages are bounded to 256. Stored/projection progress is only a conservative
candidate for GC; missing authority or stale proofs never mean no consumers.
Generic table scans are insufficient for GC. The retention method now pins its
own snapshot (or reuses the enclosing read snapshot), validates at most limit+1
index/primary witnesses and rejects inconsistent entries rather than skipping
them. A fresh first-page read with limit one yields the minimum or explicit
absence. The [retention planner](mqtt-replay-retention-planning.md) captures an
accepted Channel anchor BEFORE that read and caps its floor by the anchor, so
later registrations cannot select an earlier source boundary. This plan still
requires a replicated reclamation decision and recovery-aware physical cleanup.
Progress projections may coalesce multiple committed cursor advances; high-scale
fanout must not require one source-Slot write per recipient per publication.

Slot command **71** carries one expected binding revision and row in a strictly
versioned JSON body. Failure inventory before code: source/UID routing isolation
without a local Session; exact/changed retry; stale intent/progress/revision;
identity or boundary rebinding; phase regression/resurrection; remove-before-
prepare tombstones; progress without proof; UID discovery regression and mixed
owner fields; missing protection/cleanup acknowledgment; same-batch visibility
and rollback; complete-key candidate/recovery/retention pages and index removal;
corrupt/bounded codec, inspection, snapshot, replay and Slot ownership. Product
proof validation, source-system replication and first-message handshake remain
required, not capabilities conferred by this storage primitive.

## Durable Will records and execution receipts

`mqtt_will` uses table **27**, family **0**, primary index **1** and recovery
index **2**. The key is `(namespace, client_id, original_session_generation,
will_generation)` and routes to the ClientID's Slot. Old obligations survive
replacement of the current Session. Will generation is allocated by the Session
lifecycle, not by a client. The originating owner tuple is immutable evidence,
not authority for an old connection to modify the current Session.

Value columns 5–33 are UID, origin owner generation/node/boot/connection,
row revision, Session decision revision, exact topic, target ID/type, payload,
publication metadata, Will Delay seconds, QoS, client message number, server
idempotency identity, stage, disconnect time, due time, execution generation,
executor node/boot/lease, cancellation reason, rejection reason, resulting
MessageID/MessageSeq, publication time and update time. Column 34 is the derived
recovery deadline. Required version-1 checksum column values are bounded to
128 KiB. Payload is at most 65,535 bytes (CONNECT Binary Data); optional opaque
publication metadata is at most 32 KiB and starts with format version 1. The
shared publication contract must validate its contents before use. Will Delay
is scheduling state, not a forwarded PUBLISH property. Message Expiry starts
with publication, not configuration, disconnection or the delay period.

Stages are Armed (1), Waiting (2), Ready (3), Executing (4), Published (5),
Cancelled (6) and Rejected (7). Creation starts Armed. An authoritative Session
decision moves Armed to Waiting/Ready/Cancelled. Waiting becomes Ready at its
deadline, or earlier after a newer Session-end decision; a same-Session resume
may cancel it strictly before its due time. Waiting cannot change its captured
disconnect time or extend its deadline. Ready is a durable publication obligation
and cannot be cancelled by a later connection. Normal DISCONNECT cancellation
is valid from Armed; resume cancellation requires a nonzero Will Delay. Terminal
records do not reactivate. Stored decision revisions must increase for lifecycle
changes; they are caller-supplied proof references, not authenticated by row CAS.

Ready can acquire one execution lease. Renewal keeps generation and executor;
reclaim requires expiry and exactly one generation increment. Only that executor
may record Published/Rejected while its lease is valid. Ambiguous execution is
retried with the same immutable publication and server-owned idempotency identity.
The identity is `mqtt-will-v1:` plus SHA-256 of the version-1 canonical key JSON;
it is **not** an ordinary client `ClientMsgNo`. Published metadata version 2
binds this key, and message unique index 8 provides the separate server domain.
The original client number remains content, so a caller cannot forge a colliding
client-domain key. Setup reserves the 79-byte identity tail before accepting a
template. See [Will append identity](mqtt-will-idempotency.md). A row/lease alone
grants no permission to publish: current authorization, fenced execution and
source retention through ambiguous-result resolution remain required.

Armed and terminal rows have no recovery index entry. Waiting/Ready use due time;
Executing uses lease expiry. Complete-key pages are capped at 256 and bodies are
owned copies. Inspection exposes status, references and byte counts, not payload
or opaque properties. Terminal cleanup, global limits and durable idempotency
retention must be coordinated; no unconditional delete API is introduced here.

Slot command **72** is a version-1, at-most-256-KiB CAS envelope with strict
unknown-field/trailing-data checks. This Will slice is the row/execution
primitive. Command 73 below owns atomic Session transition plus old-Will
resolution/new-Will install; direct CAS cannot mutate a referenced live Will.
Source snapshots alone do not validate restored execution leases.

Failure inventory before implementation: key/namespace/generation isolation;
immutable identity/body/target/options; exact versus changed or stale retry;
stale decision proof, early/late cancel, due-time regression/extension, terminal
resurrection; live executor theft, expired executor completion, lease renewal and
reclaim; stable publication receipt under replay; owned input/output byte slices;
bounded bodies/metadata/pages/commands; checksum and unknown-column behavior;
same-batch visibility and neighboring rollback; deadline index changes, pinned
snapshot/inspection, Slot ownership and applied watermark. Product-level Will
scheduling, authorization, atomic lifecycle and duplicate publication remain E2E
requirements. Protocol basis: [OASIS MQTT 5.0, sections 3.1.2.5 and 3.1.3.2](https://docs.oasis-open.org/mqtt/mqtt/v5.0/os/mqtt-v5.0-os.html).

## Atomic Session/Will lifecycle

Slot command **73** carries an at-most-256-KiB version-1 lifecycle mutation: expected
Session revision/generation and complete old owner fence, event, Clean Start,
a proposed Session and optional immutable new Will. Events are InstallWill (1),
Connect (2), NormalDisconnect (3), DisconnectWithWill (4), End (5), WillDue (6).
An initial Connect uses a zero expected fence and creates Session plus Will in
one commit. Existing Connect always advances owner generation; generation changes
only for Clean Start, an ended/expired Session, or takeover of an active Session
whose expiry interval is zero. An expired offline Session cannot be resumed.

The proposed Session carries the next revision and decision time. Will reference
and lifecycle receipt are server-derived and must be zero/empty in the request.
A new Will uses the resulting Session revision as its generation, matches its
UID and complete owner, and starts Armed. At most the old and new Will are touched.
Session, Will rows, deadlines/recovery indexes and apply progress commit together.
No conditional conflict may stage half of the operation. Any neighboring failure
rolls back all of it. Missing or mismatched referenced Will state is corruption,
not permission to ignore an obligation.

Normal disconnect cancels Armed Will. Other closes capture disconnect time and
schedule at the earlier of Will Delay and Session expiry; zero expiry/delay
produces Ready immediately. Same-Session reconnect before the deadline cancels;
Clean Start or Session end preserves/accelerates the publication obligation.
Ready detaches from the live Session, so it survives subsequent replacement.
WillDue makes due Waiting work Ready, ending the Session too when expiry is due.
Explicit expiry cannot prematurely end a live/unexpired Session. Permission and
source-loss endings still require current publication authorization at execution.
An active record with an expired owner lease first requires a durable close
resolution, so reconnect cannot reset the timing of an already-lost connection.
The caller must prove old-owner isolation before Connect; row CAS is not proof.

Optional Session column **29**, `last_lifecycle_digest`, defaults empty for old
rows and stores SHA-256 of the versioned/domain-separated lifecycle request.
Exact retry needs both the resulting Session revision and matching digest, even
when Connect changed owner. Later unrelated writes invalidate the revision test.
Generic Session CAS preserves this receipt and cannot assign/change a Will
reference or change lifetime, owner or connection state while one is referenced.
Direct Will CAS cannot mutate a referenced live Will; detached execution remains
independent. Quota accounting resolves a referenced Will in its same terminal
Session commit, rather than leaving Armed work stranded.

Failure inventory before code: initial atomic creation; install/retry; all old
owner fence components; changed retry and later unrelated updates; counter or
allocator loss; normal/nonzero-reason close and expiry override; delay versus
expiry and zero values; takeover, resume, Clean Start and expired resume; old Will
survival/new Will collision; due execution and premature expiry; generic bypass;
missing referenced Will; quota termination; same-batch visibility and rollback;
legacy receipt default, corrupt receipt, snapshot/replay, inspection redaction,
command bounds/unknown fields/Slot ownership. This deterministic state transition
still requires authoritative usecase orchestration and real-process acceptance.
