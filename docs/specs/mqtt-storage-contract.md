# MQTT storage implementation contract

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
