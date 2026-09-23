# Durable publication metadata

The entry-neutral `pkg/protocol/publication` value accompanies the original IM
message body. It imports no MQTT packet, gateway, storage or usecase types.
Absent bytes mean a native publication with existing IM behavior; empty bytes
are not an encoded metadata value. Presence must survive the same reliable
boundary as the message. This format alone does not enable product MQTT access.

Version 1 is big endian: version byte (1), source byte (MQTT=1, Will=2), original
QoS byte (0 or 1), signed positive acceptance time in Unix milliseconds, three
uint16-length UTF-8 strings (publisher namespace, ClientID, original topic), a
uint16 property count, and ordered tagged property values. Will uses acceptance
time zero: its expiry clock uses the immutable source message append timestamp,
not CONNECT, disconnect, delay, executor acquisition or replay-copy time.
Ordinary MQTT uses the original ingress acceptance time, preserved on retries.
Publisher identity is namespace plus ClientID; Session generation is deliberately
absent so No Local survives reconnect. The original topic is provenance; outbound
personal topics are reconstructed for the recipient's subscription.

Property kinds are independent durable IDs: payload format=1 (byte), message
expiry=2 (uint32 seconds), content type=3 (uint16 string), response topic=4
(uint16 string), correlation data=5 (uint16 binary), user property=6 (two uint16
strings). Only user properties repeat. Order and duplicate user keys are
preserved. Zero expiry is distinct from absence. Will Delay, Topic Alias,
Subscription Identifier and other exchange/connection fields are not publication
content. The reserved client message number remains in the ordinary message
field; outbound reserved properties are server-generated.

The complete encoded value is limited to 32 KiB and 128 properties. Namespace
and ClientID are at most 1,024 bytes each; the original topic is at most 2,048.
The application limit includes identity and format overhead, so a wire-valid
property block can exceed it and must be explicitly rejected without truncation.
All strings are valid UTF-8 without NUL; topics are nonempty without wildcards.
Unknown versions/kinds, trailing data, unused variant fields, duplicate singleton
properties and invalid values fail closed. The enclosing row checksum and exact
proposal identity must protect these bytes; this format does not add a redundant
checksum. Encode/Decode own their output, with bounded allocation before parsing.

Before implementation, the approved durable-codec/application-mapping seams
cover: a literal v1 fixture; all supported property values/order/duplicates;
input/output byte ownership; truncation, length/count amplification, oversize,
unknown versions/kinds and trailing data; invalid UTF-8, identities, QoS, sources,
times, duplicate singleton properties and hidden variant data; absent versus zero
expiry, Will publication-time basis and overflow; packet-to-value mapping without
reserved-property forgery or silent loss. Product acceptance must additionally
prove metadata survives routing, quorum, snapshot, backup and shared replay.

Protocol basis: [OASIS MQTT 5.0](https://docs.oasis-open.org/mqtt/mqtt/v5.0/os/mqtt-v5.0-os.html),
sections 3.1.3.2.4, 3.3.2.3 and 3.8.3.1. Durable IDs and the combined application
size limit above are WuKongIM-specific.

## Message storage and exact identity

Message table 1 gains optional bytes column **21**, `publication_metadata`, in
family 0. Missing/empty values retain native semantics; writers omit an absent
column and keep the existing checksum envelope version. Typed reads, follower
apply, portable binary backup/import and restore visitors preserve owned bytes.
Read, exact-lookup, commit and recovery budgets count metadata as content.

The message compatibility record uses codec **2** only when metadata is present.
Its 45-byte fixed header and seven uint32-length fields are unchanged except for
the first version byte. The final payload field is followed by an obligatory
big-endian int64 append timestamp and uint32-length metadata. Version 2 requires
nonempty valid metadata, a positive timestamp and no trailing bytes. Native
messages continue to emit codec 1, including its existing optional timestamp
extension. Older readers reject codec 2 instead of silently dropping metadata.

Exact proposal/entry identity format **3** uses the domain
`wukongim/channel-entry/v3` followed by NUL. It hashes the same ordered fields as
format 2, then the uint64 length and complete metadata bytes. New mixed proposals
select the highest required format regardless of record order. Existing format-1
and format-2 hashes remain unchanged; both reject a record with nonempty metadata.
The generic quorum contract binds bytes, while the publication/storage boundaries
validate their meaning. Changing a metadata value cannot satisfy the original
proposal proof or a restored exact retry.

Storage failure coverage precedes its implementation: invalid batch rollback;
native absent values; owned reads across reopen; follower apply and binary
backup/import/visitor preservation; partial record rejection; exact proposal
retry versus altered attributes; read, lookup and recovery byte budgets.

Channel propagation failure inventory: admission, append results, record caches,
exact recovery and committed reads must preserve independently owned metadata;
body-only size hints must not bypass queue/cache/read/recovery budgets. Quorum
exchange must preserve and validate the value and reject older lossy formats.
Channel RPC must cover single/batch append, pull/recovery, history and conversation
responses, including their nested batches. A downgrade must fail explicitly when
metadata is present. Decoders must reject truncated, malformed and oversized
values before copying them. Native codec-10 messages and conversation badge
fields must retain their exact previous bytes and semantics.

Channel messages/records, admission, caches, append results, exact recovery and
committed reads now preserve the metadata and count it in content budgets.
MessageDB adaptation propagates encoding errors before any request mutation;
the memory backend enforces the same publication validation.

Channel RPC **11** appends a uvarint-length metadata value after each message or
record's Expire field; zero length means absent. Versions through 10 retain their
original field layouts, including codec-10 conversation badges. Single/batch
append and nested read/pull responses refuse lossy downgrades. Quorum exchange
**6** inserts the length-prefixed metadata after the record body and before
SizeBytes. Only exchange 6 is supported: all replicas must run matched versions,
including native traffic before MQTT activation. Record SizeBytes includes both
body and metadata; exact proposal validation rejects malformed content and
understated sizes. Publication clock validation remains at durable admission.

This remains infrastructure groundwork. SendCommand and product runtime/RPC DTOs,
JSONL offline transfer, restore consumers, append-idempotency content checks,
server Will idempotency and cluster capability gates still require propagation.
The product listener remains disabled. Do not emit these records in a mixed
cluster or downgrade/restore them with older tooling. Full activation requires
matched owners, voters, learners and recovery tools; rollback after activation
requires a compatible pre-feature backup, not a binary-only rollback.

## Send-path content and retries

Content comparison and lookup fingerprints belong to the publication codec:
callers must not depend on its byte offsets. Lookup hashing ignores only the
ingress clock, rejects malformed values and is never a substitute for exact
content comparison. Native values keep an allocation-free absent-value path.

Committed retry proof reads the original Channel content through current
authority, HW and retention fences. User-facing history may overlay later edits;
those edits cannot invalidate an otherwise identical original publish retry or
become its comparison body. Failure coverage includes an app-wired send, edit,
clock-only retry and changed-QoS rejection against a real single-node cluster
with 256 hash slots, plus routed original reads on a three-node cluster.

Owner-push request `WKVD` version 2 preserves the complete committed envelope:
after the version-1 envelope fields, append setting byte, topic string, expiry
uvarint, append timestamp varint, SyncOnce boolean and bounded metadata bytes;
routes follow that extension. Version 1 remains byte-identical for envelopes
without any extended value. Responses stay version 1. An older owner must reject
version 2 instead of accepting a request with lost publication attributes.
Failures to cover before implementation: each independently nonzero extension
selects version 2, native version-1 fixture stability, every truncated prefix,
unknown/mislabeled version, oversized or malformed metadata, independent byte
ownership in both projection directions, and a real client/handler round trip
preserving the entire event and classified recipient results.

The next propagation boundary includes SendCommand, durable Message, committed
and transient envelopes, appender mappings and the product append node RPC.
Each clone must own metadata bytes; borrowed synchronous values remain immutable.
The product RPC emits version 3 when any command has metadata and retains exact
version-2 native requests. Version 3 adds a bounded value after each command's
existing tail, before the item timeout; decoders accept both explicit layouts.

Business retries with the same sender/client key must match original publication
content: source, QoS, publisher identity, topic and ordered properties. Only the
server-assigned MQTT ingress timestamp may differ. A successful duplicate keeps
the original durable record and therefore its original expiry clock; it does not
replace metadata or repeat post-commit delivery. Different publication content
must not coalesce or pass committed-idempotency proof. This comparison is not an
MQTT Packet Identifier exchange ledger and does not grant a server Will domain.

Failure coverage before implementation: metadata loss or aliasing in any clone,
append/envelope mapping or forwarding; malformed/oversized/truncated RPC fields;
native version-2 byte drift; changed QoS, identity, topic, property order/value or
expiry accepted under a reused key; timestamp-only retries needlessly duplicated;
committed proof returning different metadata or omitting its read-byte budget;
coalesced retries emitting multiple effects; invalid metadata reaching allocation
or either persistent/transient append. Native payload-only behavior stays intact.
