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
