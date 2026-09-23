# MQTT implementation progress

Full goal: implement the approved [MQTT IM access design](../specs/mqtt-im-access.md).
Status: in progress; product MQTT admission remains unavailable. Codec/gateway,
six metadata tables with authoritative Slot access, publication propagation,
local replay/source-protection storage, Session acquisition/deadlines, owner
supervision, subscription intent orchestration and the internal gateway/PUBLISH
entry are implemented. Real Paho/TCP
integration passes on a single-node cluster with 256 hash Slots. Distributed
replay/source activation, subscription/delivery, Will execution, recovery/restore
composition and capacity acceptance remain outstanding. No passing product E2E
or capacity claim is made.

## Frozen starting context

Implementation worktree: `.worktrees/mqtt-design`, branch `codex/mqtt-design`.
Starting revision: `227c7815a` (approved design), whose product source matches
`64f73d99b3b0cb8960d40825f76053f5a0000dbd`.

Applicable root `AGENTS.md` SHA-256 at start:
`d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`.
No subtree AGENTS/FLOW applies to the newly introduced `pkg/protocol/mqtt` or
`internal/access/mqtt` at that revision. Gateway analysis read its subtree FLOW
with digest `5f3b0f38b0098341930e3d57275964d74aa882d7bc15257e3d4c61c1517f6985`.
No existing FLOW or protected agent-control file was changed in this milestone.

## Protocol milestone

The codec exposes owned packet values, bounded single-packet decoding and
validated server encoding without WK frames or internal business imports.
A partial transport packet consumes nothing; an oversized announced length is
rejected before waiting for/allocating its body. The codec preserves ordered User
Properties and distinguishes UTF-8 strings from binary password/payload fields.
Application mapping freezes unpadded canonical base64url IDs, device credential
categories and the reserved `wk.` property namespace. It does not perform
credential verification or IM authorization on its own.

Tests were introduced before each behavior slice: framing/CONNECT, properties
and Will, publish/subscription/ACK syntax, server encoding, payload-format and
authentication dependencies, then application mapping. Isolated boundary tests
are justified by the malformed-input inventory in the approved plan and wire
contract. Feature acceptance will still use real product processes.

Eclipse Paho v0.23.0 is an independent test/client dependency, not the server
codec implementation. Its packet decoder rejects MQTT's legal zero-length
DISCONNECT. The server encoder therefore emits the normal reason explicitly;
the server decoder accepts both legal forms.

Verified commands (2026-09-23):

- `GOWORK=off go test ./pkg/protocol/mqtt ./internal/access/mqtt -count=1`
- `GOWORK=off go test ./pkg/protocol/mqtt -fuzz=FuzzDecodeBoundedPacket -fuzztime=20s -parallel=4`
  passed 5,165,826 executions. This is bounded parser robustness evidence, not a
  throughput or protocol-conformance certification.

- `GOWORK=off go test -race ./pkg/protocol/mqtt ./internal/access/mqtt -count=1` passed.
- `GOWORK=off go test ./pkg/protocol/... -count=1` passed all six protocol packages.
- `git diff --check` passed.

## Next dependency chain

1. Build the real standard-client single-node/three-node acceptance scenario.
2. Decouple only the gateway handshake, bounded dispatch and serialized write
   seams required for independent packets, retaining WK/JSON-RPC behavior.
3. Add versioned Slot session/projection contracts and authoritative ownership
   fencing, then publication metadata replication and protected source replay.
4. Complete persistent delivery, expiry/quota/revocation, Will, backup/restore,
   feature readiness gates, metrics, process-level failure and scale validation.

The source protection/replay chain must precede enabling persistent MQTT access;
a connected online-only slice is not completion of this goal.

## Process acceptance in progress

The interop scenario now compiles with standard Eclipse Paho clients, authenticated
WKProto helpers, real cluster startup and bounded JSON result artifacts. Its first
single-node run is intentionally RED: configuration rendering rejects the not-yet
implemented `WK_MQTT_ENABLE` key. No product MQTT connection was accepted and no
passing result artifact was written.

Frozen harness context before edits:

- `test/e2e/AGENTS.md`: `59bb62e0ce12e7b25324108976cfbf5fe3a939a5ff1c93d09e2a8050e7be79b2`
- `test/e2e/suite/FLOW.md`: `3bf6d34b55434d0e4cb2c257c92d354fa30a8e938b2308b10c7112cf7d78df56`

The suite FLOW and catalog now mention MQTT. The scenario has its own domain and
scenario AGENTS files. This red acceptance must not be skipped or counted as a
successful feature gate while gateway and durable capabilities are developed.

Harness validation passed:
- Focused existing WKProto suite-helper tests with `-tags=e2e`.
- Named `flow-doc-contracts` check from the repository policy, with `GOWORK=off`,
  after regenerating the index. It reports 81 compliant FLOW files and 9 existing
  length warnings, with no invalid files.

## Gateway packet milestone

The reusable gateway now accepts an independent `PacketAdapter` and
`PacketHandler`, while preserving existing WK frame APIs. MQTT uses the existing
bounded auth pool, session-ordered SEND mailbox, serialized session writes and
shared idle heap/monitor. Queued and executing packet bytes share a 64 MiB default
budget. The codec limits each coalesced decode to 128 owned packets; partial input
grows amortized. Peer Maximum Packet Size applies to every encoded response.

Accepted activation has one cleanup owner: a failed handshake invokes rollback,
and a completed handshake transfers cleanup to the open/close lifecycle. Entry
callback panics produce fixed diagnostics without logging peer-provided values.
Listener errors and packet-kind observations use the existing observer boundary.
MQTT Keep Alive renews only after complete packets, uses the negotiated 1.5 factor,
and disables the protocol deadline at zero without creating a per-client timer.

Public gateway integration tests were added before behavior changes. They found
and drove fixes for repeated fragment-prefix allocation, missing listener error
routing, missing packet observations and incorrect Keep Alive handling. Existing
WK admission tests also caught an allocation regression during queue refactoring;
rejection again occurs before frame boxing/cloning.

Verified on 2026-09-23:

- `GOWORK=off go test ./pkg/gateway/... ./pkg/protocol/mqtt ./internal/access/mqtt -count=1`
- `GOWORK=off go test -tags=integration ./pkg/gateway/... ./pkg/protocol/mqtt ./internal/access/mqtt -count=1 -timeout=90s`
- `GOWORK=off go test -race -tags=integration ./pkg/gateway/... ./pkg/protocol/mqtt ./internal/access/mqtt -count=1 -timeout=90s`
- Named `flow-doc-contracts` after updating gateway FLOW and regenerating the index.

These checks passed. The product app still has no MQTT listener or durable MQTT
state. Standard-client product interop remains RED as recorded above; session
ownership, tables, replay/protection, reliable delivery, Will, quotas and full
recovery/scale acceptance remain outstanding. The generic gateway is groundwork,
not a complete MQTT feature.

## First durable session row and Slot command

Frozen context before this slice, at gateway milestone `bd4a00378`:

- `pkg/db/FLOW.md`: `49c5fe18bcf98edd7bc072dececaf0114f8d51d77f52cffb96b49f240dd2584e`
- `pkg/db/meta/FLOW.md`: `a998bf21d7d16f8f99cdd12fffb118637244e82aa02c7707053e61761ffa7fe0`
- `pkg/slot/FLOW.md`: `f5ec37c77f41d348086c071e9f3fa18707e18b4e01038c2140570ee670c4bb9f`

The [storage contract](../specs/mqtt-storage-contract.md) freezes new metadata table
22 and Slot command 67. `mqtt_session` retains UID binding after termination,
separates row/session/owner generations, checks exact CAS retries and monotonic
transitions, and maintains a bounded complete-key deadline index. Its checksum
envelope binds data to the physical key. Inspection and existing pinned snapshot
paths include the row and index. The Slot command bounds and versions its body,
rejects invalid ownership envelopes and commits with the applied watermark.

Tests preceded implementation at each metadata/FSM boundary. They cover conflict
and retry behavior, namespace isolation, owner identity and session-generation
guards, same-batch overlays, rollback with a neighboring failure, all-field codec
round trip/corruption checks, deadline maintenance, pinned backup restore, Slot
snapshot/replay and command inspection. The existing command catalog check caught
the missing fixture for command 67; the catalog was updated and the FSM suite
then passed.

Validation (2026-09-23):

- `GOWORK=off go test ./pkg/db/... -count=1` passed.
- `GOWORK=off go test ./pkg/slot/... -count=1 -timeout=90s` passed multiraft/proxy;
  its sole FSM failure was the missing command fixture. After fixing it,
  `GOWORK=off go test ./pkg/slot/fsm -count=1 -timeout=90s` passed.
- `GOWORK=off go test -race ./pkg/db/meta ./pkg/slot/fsm -run 'TestMQTTSession' -count=1 -timeout=45s` passed
  (the macOS linker emitted an LC_DYSYMTAB warning).
- Named `flow-doc-contracts` passed after meta/Slot FLOW updates and index render:
  81 compliant files, no invalid files and the same 9 existing length warnings.

This is one of seven planned tables. Product wiring still proposes no MQTT
commands. CAS does not prove old-owner isolation or supply an authoritative
cluster read. Distributed routing, owner leases/fencing, the remaining tables,
offline transfer, restored-owner invalidation, readiness gates and all original
reliable delivery/Will/scale acceptance requirements remain required work.


## Subscription intent and atomic owner fence

Frozen source context at `c19e28c46`:

- Root `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `49c5fe18bcf98edd7bc072dececaf0114f8d51d77f52cffb96b49f240dd2584e`
- `pkg/db/meta/FLOW.md`: `fa95c3be00881889a00a53468fea9fc4505797f541dc61cbec49b3e821f07dab`
- `pkg/slot/FLOW.md`: `636bf617263ff36124531d02b4790cbb77648030b98c3cffb455acd5339f4c3a`

Metadata table 23 persists exact-topic intent, stable subscription generation,
options and recoverable establishment/removal stages. Slot command 68 checks the
complete owner identity and session revision, then atomically changes intent and
advances session revision without resetting backlog/quota counters. The new
subscription generation is allocated from that never-reused resulting revision.
Active option replacement preserves source cursor identity. Recovery can finish
existing work offline; new intents/options require an active session. Ended and
older session generations allow only cleanup transitions.

Tests were written at the previously approved metadata/FSM boundaries before
implementation. They cover missing/stale owner rejection, exact/changed retry,
phase and generation guards, same-batch visibility, atomic failure, namespace and
generation isolation, complete-key bounded recovery pages, stale index removal,
checksum/codec bounds, pinned snapshot, inspection, owned Slot validation and
FSM snapshot/replay. Review identified a false-retry case: an unrelated session
CAS could advance revision while leaving the same child value. A failing test
reproduced it; storing the exact child mutation revision (column 18) now separates
that case from a genuine completed retry.

This is two of seven planned tables. Projection completion, source protection,
SUBACK, distributed routing/owner isolation, remaining four metadata tables,
shared replay, publication metadata, transfer/restore gates, Will, quotas and
full process/scale acceptance remain outstanding. Product MQTT is still disabled.


Validation for the subscription slice (2026-09-23):

- `GOWORK=off go test ./pkg/db/... ./pkg/slot/... -count=1 -timeout=90s` passed.
- `GOWORK=off go test -race ./pkg/db/meta ./pkg/slot/fsm -run 'TestMQTT(Session|Subscription)' -count=1 -timeout=45s` passed;
  the macOS linker emitted its existing LC_DYSYMTAB warning.
- Named `flow-doc-contracts` passed after regenerating the index: 81 compliant
  files, no invalid files and the same 9 pre-existing length warnings.
- `git diff --check` passed. No product interop result changed; its documented
  RED state remains until the product listener and full persistent chain exist.


## Per-source delivery cursor and quota accounting

Frozen source context at `55bff9dec` (the previous goal turn made verified progress):

- Root `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `49c5fe18bcf98edd7bc072dececaf0114f8d51d77f52cffb96b49f240dd2584e`
- `pkg/db/meta/FLOW.md`: `ccf6db2222e7eb3f92d71f73e344691ffea6064b0fd955abf0a8b21fa8c959b3`
- `pkg/slot/FLOW.md`: `0be0d7393520285333bba5fdb76e74cbe36a0eca45fb1dcebc1f93bf408ffcd5`

Metadata table 24 and Slot command 69 establish one protected-start cursor for
each subscription/source incarnation and atomically count qualified ranges into
both source and session backlog. The cursor separates accounted coverage,
window admission and completed progress; this slice changes only accounting.
Exceeding message or byte quota ends the session with a durable reason in that
same commit. Owner, session/subscription generation and captured authorization
version fence writes; the revision and canonical mutation digest distinguish
exact retry from a changed request or unrelated session write.

Tests were written first at the established metadata/FSM boundaries. They cover
protected-start immutability, stale/missing identity, accounting without false
completion, count/byte termination and retry, offline multi-source accumulation,
takeover and arithmetic overflow, same-batch overlays and full rollback,
complete-key pagination, codec bounds/corruption, pinned backup, inspection and
Slot snapshot/replay. Source coverage is a caller proof, not something these
storage tests establish. These tests do not claim QoS delivery is implemented.

Three of seven tables now have storage primitives. The next slice must persist
only the bounded QoS window, freeze content references and send order, and link
PUBACK removal with contiguous source completion and counter release in one
Slot commit. In particular, out-of-order PUBACK cannot use maximum message
sequence as completion; packet-ID reuse must not overwrite evidence of an older
uncompleted exchange. Remaining full-scope requirements stay unchanged: source
protection/shared replay, publication metadata, source bindings, Will,
authoritative distributed reads/owner isolation, transfer/restore and capability
gates, product interop, permission/recovery/retention behavior and scale evidence.

Validation for cursor accounting (2026-09-23):

- `GOWORK=off go test ./pkg/db/... ./pkg/slot/... -count=1 -timeout=90s` passed.
- `GOWORK=off go test -race ./pkg/db/meta ./pkg/slot/fsm -run 'TestMQTT' -count=1 -timeout=45s` passed
  (the existing macOS LC_DYSYMTAB linker warning remains).
- Named `flow-doc-contracts` passed: 81 compliant, none invalid, the same 9
  pre-existing length warnings. The FLOW index was regenerated.
- `git diff --check` passed. Product acceptance remains RED/unimplemented as
  previously recorded; the checks above are not protocol or capacity acceptance.

Before enabling the runtime, lifecycle Session CAS must preserve counters owned
by the delivery commands within the same generation; a new generation and
restore/import must handle their reset/consistency explicitly. Current storage
CAS is still a general preparatory primitive, not a published product port.


## Durable outbound QoS window and contiguous completion

Frozen source context at `a69d4ce94`; the previous turn made verified progress:

- Root `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `49c5fe18bcf98edd7bc072dececaf0114f8d51d77f52cffb96b49f240dd2584e`
- `pkg/db/meta/FLOW.md`: `16b60640961f2e82e2b55f476a4a2e2da54b07fc5abeac51ffc2d393430653da`
- `pkg/slot/FLOW.md`: `0a7717bcd4bbe3d505c07b31e4e2bea0b6198735ca85387556bf8d92f6506083`

Table 25 and command 70 persist only admitted outbound QoS 1 exchanges. Entries
freeze content version/hash, application identity, Subscription Identifier and
send order. Per-source outstanding links let ACK update adjacent records and
advance only past the earliest remaining gap. Exchange removal, cursor progress,
window credit, backlog counters and Slot apply progress commit together. A
cursor receipt preserves exact ACK results after its exchange row is deleted.

The allocator wraps 65535 to 1, probes at most the durable bounded live count
plus one, and never overwrites an occupied ID. Admission respects both configured
window and peer Receive Maximum. Reconnect may keep more old exchanges than the
new smaller peer limit; runtime resend throttling remains required. Normal
unsubscribe stops new admission but preserves completion of started exchanges.
Lifecycle CAS now preserves same-generation delivery counters and allocators,
and new lifetimes start empty. The earlier report's lifecycle-counter TODO is
resolved for storage CAS; restore/import cross-row consistency is still required.

Tests cover out-of-order ACK gaps, packet-number wrap, frozen references/options,
flow control and reduced peer limits, stale owner/order, normal unsubscribe,
covered-range release bounds, same-batch visibility, rejected admission and full
rollback, original-order pagination/index deletion, pinned backup, inspection,
FSM ownership/snapshot and retry after deleted ACK. Literal pre-change payloads
prove new optional Session/Cursor columns retain older-row readability. Codec
bounds, corrupt values and invalid command envelopes remain checked. Actual
network resend pacing, an occupied-ID collision after a full packet-number cycle,
and corruption recovery of an inconsistent graph still require product-level
acceptance; these storage tests are not a scale or full recovery claim.

Four of seven planned tables now have storage primitives. Remaining work includes
source bindings, durable Will, shared replay and publication metadata, protected
source discovery/retention, authoritative distributed reads, owner isolation,
transfer/restore and capability gates, product access/runtime wiring, permission
ordering, expiry/cleanup, global pressure, process interop and scale acceptance.
The complete approved MQTT scope remains active, and no listener is enabled.

Validation for the outbound window slice (2026-09-23):

- `GOWORK=off go test ./pkg/db/... ./pkg/slot/... -count=1 -timeout=90s` passed.
- `GOWORK=off go test -race ./pkg/db/meta ./pkg/slot/fsm -run 'TestMQTT' -count=1 -timeout=45s` passed,
  with the existing macOS LC_DYSYMTAB linker warning.
- Named `flow-doc-contracts` passed after index regeneration: 81 compliant,
  zero invalid and the same 9 pre-existing length warnings.
- `git diff --check` passed. The product E2E remains RED until full source,
  runtime and listener wiring is complete; no network/capacity pass is claimed.


## Source-owned subscription projections

Frozen source context at `9fec94c0b`:

- Root `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `49c5fe18bcf98edd7bc072dececaf0114f8d51d77f52cffb96b49f240dd2584e`
- `pkg/db/meta/FLOW.md`: `841368778832b8b4a647d9bbdb9a3dd1cfc0f1072d4f875b39df140d0dcd934a`
- `pkg/slot/FLOW.md`: `cc2905828303be34f6e519ceafd853f96263e1bd6d91e72235b133bff91daedd`

Table 26 and command 71 add Channel-source/UID-owned binding CAS, independent
of the Session's Slot. Separate intent, cursor-proof and source-protection
revisions reject stale lifecycle/progress projections. Removed tombstones cannot
reactivate. Missing-key Session-ended tombstones fence delayed prepare; existing
bindings traverse recoverable cleanup before removal. Source cleanup must be
acknowledged before a live Channel binding leaves retention/recovery indexes.

Channel boundaries are captured once. Unknown boundaries have a conservative
zero floor. UID inbox qualification instead persists a monotonic initial-discovery
cursor in encoded primary-key order. Its logical absent generation uses one
reserved NUL key component because the table runtime rejects empty key strings;
Channel generations cannot use that value. Candidate, reconciliation and
retention scans have complete cursors and a 256-row cap. Snapshots/inspection
include all rows and indexes, including retained terminal bindings.

Failure cases and tests preceded implementation. The first RED was the missing
source-binding API; a subsequent failure exposed the empty UID key component,
resolved with the documented canonical encoding. Tests exercise exact/changed
retries, stale intent/progress, immutable identity/boundaries, resurrection,
remove-before-prepare, missing cleanup acknowledgment, source progress without
proof, UID discovery ordering, same-batch visibility/rollback, bounded pages,
index removal, pinned backup, codec bounds/checksums, FSM ownership, snapshot and
replay. No Session row is assumed on the source/UID Slot.

These are deterministic storage primitives, not remote authority or protection
proof. Source/UID routing in the distributed proxy, protected source operations,
first-person-message registration/qualification handshake and coherent GC reads
remain unimplemented. Ordinary raw index scans are candidate discovery only;
concurrent mutations can make a non-snapshot empty page insufficient to prove
absence of consumers. Coalesced projection updates and authority-bound snapshot
reads are required before high-scale reclamation or delivery can use this state.

Five of seven planned tables now have storage primitives. Durable Will, shared
replay and publication metadata, protected source discovery/retention, owner
isolation, distributed reads, transfer/restore consistency and capability gates,
product runtime/listener wiring, permission ordering, expiry/cleanup, global
pressure, real-process interop and scale acceptance remain. The full approved
scope stays active; product MQTT remains disabled and its E2E is still RED.

Validation for this source-binding slice (2026-09-23):

- `GOWORK=off go test ./pkg/db/... ./pkg/slot/... -count=1 -timeout=90s` passed.
- `GOWORK=off go test -race ./pkg/db/meta ./pkg/slot/fsm -run 'TestMQTT' -count=1 -timeout=45s` passed
  with the existing macOS LC_DYSYMTAB linker warning.
- Named `flow-doc-contracts` passed after index regeneration: 81 compliant,
  zero invalid and the same 9 pre-existing length warnings.
- The final focused source-binding/FSM/catalog suite passed after tightening
  canonical UID-key decoding. `git diff --check` passed.


## Durable Will storage and execution receipts

Frozen source context at `934300b18`; the previous turn was verified progress:

- Root `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `49c5fe18bcf98edd7bc072dececaf0114f8d51d77f52cffb96b49f240dd2584e`
- `pkg/db/meta/FLOW.md`: `0a30b90143e4d37b7e6f05c47583d3e91695fffe53bb2148a775b3a9256ed33f`
- `pkg/slot/FLOW.md`: `513bc1394e92a5b0b6e8369b7e86f2d210a7a7ad0785423b9b34504d682387a1`

Table 27 and command 72 add bounded immutable Will content, separate Session
proof and record revisions, cancellable delay, durable publication obligation,
execution leases and terminal publication/rejection/cancellation receipts. An
expired executor cannot complete work; reclaim increments the execution fence.
Old Session generations remain addressable after a replacement. Canonical
server Will identities remain stable across execution retries, but require a
separate append-idempotency domain before publication is wired. Merely placing
a prefix in ordinary client-controlled message numbers would not isolate them.

The payload is limited to MQTT CONNECT's 65,535-byte Binary Data bound and the
opaque versioned publication metadata to 32 KiB. The row uses a 128-KiB checksum
column envelope and the command a 256-KiB strict versioned JSON envelope. Inputs
are cloned before asynchronous batch commit; decoded bytes are owned. Recovery
pages carry full tie breakers and are capped at 256; runtime scheduling must use
smaller count/byte budgets appropriate to full-body rows. Inspection includes
references and byte counts without publication bodies or opaque properties.

Failure cases and tests preceded implementation. Metadata tests cover immutable
intent, exact/stale retry, newer decision proof, cancellation deadline, early
Session-end acceleration, zero delay, worker theft/renewal/reclaim and expired
completion, terminal receipt survival, byte ownership, neighboring rollback,
bounds/checksum/future columns, deadline index changes, pinned snapshots and
redacted inspection. FSM tests cover ordered apply, stale replay, owned hash
Slots, apply watermark, restored receipts and maximum-size/malformed commands.
One failing test initially changed disconnect time beyond its update time; its
fixture was corrected to a structurally valid attempted boundary rewrite so it
checks transition fencing rather than unrelated row validation.

The [OASIS MQTT 5.0 standard](https://docs.oasis-open.org/mqtt/mqtt/v5.0/os/mqtt-v5.0-os.html)
was checked for Will Delay, Session-end/resume/takeover and Message Expiry rules.
This primitive records decisions; it does not yet establish product protocol
correctness. Next, one Slot operation must atomically update the Session,
resolve its old Will and install any new Will. Two independently successful CAS
operations cannot substitute for that boundary. Generic Session CAS and quota
termination must be integrated with it so no lifecycle path strands Armed or
Waiting work. Lease restoration, current authorization and ambiguous publication
resolution also remain required. The opaque publication metadata body still
needs its shared entry-neutral codec and replication/append contract.

Six of seven planned tables now have storage primitives. The full approved goal
remains active: Session/Will atomic lifecycle, shared replay/publication metadata,
protected sources and first-contact discovery, authority/isolation, permission
ordering, global pressure, expiry/cleanup, transfer/restore and rollout gates,
product wiring, process interop and scale acceptance remain incomplete. Product
MQTT is disabled and its E2E remains RED; no real Will publication is claimed.

Validation for this Will storage slice (2026-09-23):

- Focused metadata/FSM Will and complete inspection-catalog tests passed.
- `GOWORK=off go test ./pkg/db/... ./pkg/slot/... -count=1 -timeout=90s` passed.
- `GOWORK=off go test -race ./pkg/db/meta ./pkg/slot/fsm -run 'TestMQTT' -count=1 -timeout=45s` passed,
  with the existing macOS LC_DYSYMTAB linker warning.
- Named `flow-doc-contracts` passed after index regeneration: 81 compliant,
  zero invalid and the same 9 pre-existing length warnings.
- `git diff --check` passed. Last edits after the tests only format comparisons,
  add comments and update documentation.


## Atomic Session and Will lifecycle

Frozen source context at `eaa929047`; the preceding Will-storage turn was verified
progress:

- Root `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `49c5fe18bcf98edd7bc072dececaf0114f8d51d77f52cffb96b49f240dd2584e`
- `pkg/db/meta/FLOW.md`: `50ac24509c5e48b0b4dacbfb4e75207a90127b0d7082fa78409dd685bcbf22cd`
- `pkg/slot/FLOW.md`: `d7dc025e238810347ac1d98e048952e2ad27279eacb031c3b3da585836830777`

Command 73 atomically creates or switches a Session, resolves its prior Will and
optionally installs a new configuration. It covers normal/nonzero-reason close,
resume/takeover, Clean Start, expiry/end and due Will work. New configuration
uses the resulting Session revision as its Will generation. Complete old-owner
and Session fences prevent delayed close/timer work from changing a new owner.
No conditional conflict stages half a decision. Missing or mismatched referenced
Will state fails as corruption instead of discarding an obligation.

Optional Session column 29 stores a domain-separated lifecycle request digest.
Together with the resulting revision it proves exact retry even after owner
change. An unrelated Session write invalidates the revision witness. Generic
Session CAS cannot forge the reference/receipt or bypass the lifecycle while
Will is referenced; direct Will CAS cannot mutate a referenced live record.
Ready publication work is detached and continues under its own execution lease.
Quota accounting resolves old Will in the same commit as its terminal Session
and backlog counters, including when the Session was offline.

An expired active owner lease must first be resolved into a durable close before
Connect. Otherwise an overdue lost connection could incorrectly be interpreted
as a fresh timely reconnect and have its Will cancelled. The future authority
orchestrator must supply the proved close time, fresh read barrier and old-owner
isolation; this storage rule neither proves isolation nor permits socket IO.

Failure inventory and public metadata/FSM tests preceded implementation. Tests
cover initial atomic creation, installation, full owner fencing, changed retry,
unrelated writes, normal/delayed/short-expiry/zero-expiry close, resume/takeover,
Clean Start, expired resume, due/early-due, premature expiry, forbidden expiry
increase, reset counter constraints, bypass attempts, new-key collision, missing
references, same-batch visibility, neighboring rollback and active/offline quota
endings. FSM tests cover atomic ordered application, owner-change receipt retry
after snapshot, apply watermark, Slot ownership, redacted inspection and bounded
malformed commands. The existing literal pre-window Session payload also proves
column 29 defaults empty. Its historical Will reference is preserved explicitly
in that codec fixture; new lifecycle fixtures no longer invent unbacked Will IDs.

The previous report's storage-level Session/Will atomicity TODO is resolved.
Product scheduling, publication authorization, old-owner isolation and network
behavior remain unverified. Next work includes the shared entry-neutral
publication codec/replication contract and the server Will idempotency domain,
then protected source/replay integration and authoritative runtime wiring. Full
expiry/cleanup, restore/transfer consistency, capability gates, real-process
interop and scale acceptance remain required. All six metadata tables have
storage primitives, but the shared replay table and full reliable chain remain
incomplete. The full goal stays active and MQTT product access remains disabled.

Validation for the atomic lifecycle slice (2026-09-23):

- Focused MQTT metadata/FSM and inspection-catalog tests passed.
- `GOWORK=off go test ./pkg/db/... ./pkg/slot/... -count=1 -timeout=90s` passed.
- `GOWORK=off go test -race ./pkg/db/meta ./pkg/slot/fsm -run 'TestMQTT' -count=1 -timeout=45s` passed,
  with the existing macOS LC_DYSYMTAB linker warning.
- Named `flow-doc-contracts` passed after index regeneration: 81 compliant,
  zero invalid and the same 9 pre-existing length warnings.
- `git diff --check` passed. Product E2E remains RED until the complete runtime,
  publication, source-protection and listener path exists.

## Publication metadata format and message storage

Frozen source context at `d5f6ee1a6`:

- Root `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `49c5fe18bcf98edd7bc072dececaf0114f8d51d77f52cffb96b49f240dd2584e`
- `pkg/db/message/FLOW.md`: `ac910f21296d227ca70b6a6410501e12aab4efb4d5d0233a7176e8b6e8c2415d`

The new entry-neutral `pkg/protocol/publication` format freezes publisher
namespace/ClientID, origin, original QoS/topic, ordered properties and the expiry
clock. It owns no wire packets, sessions or authorization. The complete value,
including identity and encoding overhead, is capped at 32 KiB/128 properties.
Unknown/partial formats, unsupported property variants, duplicate singleton
properties, invalid strings and oversized values fail without truncation.
User-property order and duplicate keys are retained. Native messages omit it.
Ordinary MQTT uses original ingress time; Will uses the immutable source append
timestamp, so delay and replay cannot restart or shorten its publication clock.

PUBLISH/Will mapping produces owned content and canonical bytes after application
topic and reserved-key checks. Will Delay stays outside forwarded content;
`wk.client_msg_no` remains the message's separate field. Retain, QoS 2, aliases,
client subscription identifiers and forged reserved output attributes fail.
Mapping is not authentication or IM payload authorization, and its Will client
number still does not provide a server idempotency domain.

Message table 1 gains optional bytes column 21 (`publication_metadata`). Typed
append/read, follower apply, binary backup/import and restore visitors preserve
it. Native columns, missing-field defaults and codec-1 bytes remain unchanged.
Nonempty metadata uses compatibility record codec 2, with a mandatory append
timestamp and bounded metadata suffix; older readers reject that version.
Exact proposal format 3 binds all format-2 semantics plus the entire metadata
under a new digest domain. Formats 1/2 reject nonempty metadata instead of
certifying an unbound value; original native hashes remain unchanged. Exact
retry after binary backup import retains the publication proof.

Tests preceded implementation at the approved codec/mapping/storage boundaries.
They cover a literal v1 value, semantic and size failures, truncation, independent
byte ownership, expiry presence/zero/Will basis/overflow, packet mapping,
invalid-batch rollback, reopen, follower apply, binary backup and visitors,
partial record rejection and altered metadata versus exact proposal proof.
Budget checks cover reads, exact client-number lookup and native/recovery
representations. The latter two initially failed because their counters omitted
metadata; both now include it. Existing native storage fixtures and regressions
remain green. IM send validation still rejects empty payloads; no new empty-body
exception was introduced in the storage tests or product policy.

Validation (2026-09-23):

- `GOWORK=off go test ./pkg/db/... ./pkg/quorumlog/... ./pkg/protocol/... ./internal/access/mqtt -count=1 -timeout=90s` passed.
- `GOWORK=off go test ./pkg/channel/... ./pkg/slot/... -count=1 -timeout=90s` passed.
- Focused race tests for publication codec, access mapping, quorum identities
  and message storage passed. After the final budget fixes, the complete message
  package and its publication race tests passed again. macOS emitted the existing
  LC_DYSYMTAB linker warning.
- Named `flow-doc-contracts` passed after index regeneration: 83 compliant,
  zero invalid and the same 9 pre-existing length warnings.
- `git diff --check` passed.

This is verified storage groundwork, not complete product replication. Next
propagate the value through SendCommand, Channel records/clones, append adapters,
RPC/quorum exchange, restore consumers and JSONL offline transfer, including
all admission/read/fanout byte budgets. Business retry resolution must preserve
the original stored clock, and server Will idempotency needs a distinct domain.
Shared replay, source protection, owner isolation, authoritative runtime wiring,
cleanup/restore fencing and capability gates remain required. Product process
E2E is still RED and MQTT access remains disabled. The full goal stays active.

## Channel publication propagation

Frozen source context at `9f39a4ce2`:

- Root `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/channel/FLOW.md`: `8115b685d72f6043547cd1aaa24f693623360192cf095cc43c7072d7b49c1050`
- `pkg/channel/reactor/FLOW.md`: `fa6df60855d02e1dccfad25c32cd8c19c205515a53c777a5f5c8acbcdc0cd2d3`
- `pkg/channel/worker/FLOW.md`: `da9c03f3b2480ae1601938e205ed47981a2b6a6c6248b2f66f88ae930a107752`
- `pkg/cluster/FLOW.md`: `3955a06f0484970ff495d60648d99cf2efba96718fdcc4d86ef5afd8efecb6c8`

Channel Message/Record now carry publication metadata. Admission, append results,
record caches, quorum proposals, donor pages and durable reads preserve their
ownership, and mixed proposals select format 3 independently of record order.
The MessageDB adapter reuses canonical record codec 2 and propagates encoding
errors before submitting single/batch append, follower apply or suffix replacement.
Invalid content changes neither records nor HW. Memory storage validates the same
publication format and clamps size hints to at least the actual content size.

Quorum exchange 6 carries metadata in replication and recovery replies, validates
bounded content/expiry/size and binds the exact proposal proof. It deliberately
requires matched peers for all data-bearing exchanges, including native traffic
while MQTT remains unavailable. This deployment requirement is in the Changelog.
Channel RPC 11 adds the bounded value to single/batch append, pull and read
responses, rejects lossy downgrade, and preserves older native layouts. Literal
codec-10 append and conversation-response fixtures prove the former bytes;
conversation badge gates remain at 10 rather than moving with the current codec.

Tests preceded each behavior change. They cover owned source/result/read bytes,
exact recovery and changed-proof rejection, single-node cluster admission,
malformed/truncated/oversized RPC values, older-peer refusal, aggregate persisted
read budgets, edit-growth continuation and storage validation without partial
mutation. Native response allocation regression tests caught a per-head interface
allocation in metadata validation; direct message inspection restored the prior
budget. A final memory-storage test also caught body-only size hints bypassing
read budgets; both leader and follower insertion now retain the actual size.

Validation (2026-09-23):

- `GOWORK=off go test ./pkg/channel/... ./pkg/cluster/... ./internal/infra/cluster/... -count=1 -timeout=90s` passed after codec, ownership, budget and storage-error changes.
- `GOWORK=off go test -race ./pkg/channel/store ./pkg/channel/service ./pkg/channel/replication ./pkg/cluster/channels ./pkg/cluster -run TestPublication -count=1 -timeout=90s` passed; the existing macOS LC_DYSYMTAB linker warning remains.
- After final storage validation/size-hint changes, the full Channel suite and
  focused publication storage race tests passed again.
- Named `flow-doc-contracts` passed after regeneration and shortening the Cluster
  FLOW to its existing hard limit: 83 compliant, zero invalid, 9 length warnings.
- `git diff --check` passed.

Next connect entry-neutral SendCommand/committed-envelope contracts, product
Channel-append runtime, infrastructure adapters and product node RPC. Durable
idempotency still needs publication-content matching with the original accepted
clock and a separate server Will domain. JSONL transfer, restore consumers,
shared replay, protected source retention, authoritative session execution,
owner isolation, cleanup, capability gates and real-process/scale acceptance
remain required. Existing product E2E is still RED; no MQTT listener is enabled
and the full implementation goal remains active.

## Send metadata and original-content retry proof

Frozen source context at `804299269`:

- Root `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/contracts/channelappend/FLOW.md`: `c186be06331770c1e287e9f374b322dc3d7f8008316776b9b0bfaca436de43c6`
- `internal/runtime/channelappend/FLOW.md`: `a74b3398389cf8fa429f4a11bef8bc65d86141aae6472a0e6fdf1a6f0c791894`
- `internal/infra/cluster/FLOW.md`: `44ca466e7fd926d47d24eb24e1d368ccbce4dc20517fbea79f4dea863e92e62f`
- `internal/access/node/FLOW.md`: `908b49b5616c2da4472aab07c0f81d4d2ede43804d941f42be651e3c073826bd`
- `internal/app/FLOW.md`: `a2b94e35aa9ebedc57a139a951566612d40278ace020571cac8015f803930d36`
- `pkg/protocol/publication/FLOW.md`: `a812e55a5b8ff513163e0f758d3f45fd76c1a8ba2ae8dea7d1d00fd6a18ff714`
- `pkg/cluster/FLOW.md`: `84f7cd138b9a999cdd0e8ce31516482fd8c86b11865688abf297426895b4821e`

SendCommand, append messages and committed/transient envelopes now preserve
owned metadata through the product runtime and infrastructure mappings. Product
append request 3 carries the value after each command; native-only requests keep
the literal version-2 layout. Bounds, syntax, strict prefixes, mislabeled frames
and mutable input isolation are covered. Invalid content is rejected before
route preparation or message-ID allocation, including transient sends.

Business retry lookup/coalescing compares exact MQTT bodies plus semantic
publication content. The publication package owns comparison and fingerprint
layout, excludes only ingress time, and validates both values. Hash matches still
require exact comparison. Matching retries keep the original record/expiry clock
and produce no second post-commit effect. The committed proof budget and exact
record match both include metadata.

A new real app integration test initially reproduced `channel: log conflict`
when retrying after a history edit. Original committed batch reads now delegate
through the current Channel Leader and preserve HW/retention fences while omitting
edit overlays. Idempotency uses that explicit port; user-facing history keeps
its overlay. This is not a protected replay read below the history floor.

Validation (2026-09-23), with tests written before each implementation change:

- `GOWORK=off go test ./pkg/protocol/publication ./pkg/cluster/... ./internal/contracts/channelappend ./internal/runtime/channelappend ./internal/infra/cluster ./internal/access/node ./internal/usecase/message ./internal/app -count=1 -timeout=90s` passed.
- `GOWORK=off go test -race ./pkg/protocol/publication ./internal/contracts/channelappend ./internal/runtime/channelappend ./internal/infra/cluster ./internal/access/node -run TestPublication -count=1 -timeout=90s` passed; existing macOS linker warnings remain.
- `GOWORK=off go test -tags=integration ./internal/app -run TestPublicationSingleNodeClusterRetryAfterEdit -count=1 -timeout=60s` passed with 256 hash slots after reproducing the failure. It verifies original metadata, edited history, stable retry IDs, and changed-QoS rejection without another record.
- The integration retention test passed. The three-node edit/failover test passed
  original reads on all nodes but initially failed after stopping its old leader
  with a transport connection refusal. Its isolated rerun
  (`GOWORK=off go test -tags=integration ./pkg/cluster -run '^TestMessageUpdateThreeNodeQuorumAndLeaderTransfer$' -count=1 -timeout=90s`)
  passed. This records the observed intermittent failure without claiming its
  independent failover timing is resolved.
- Named `flow-doc-contracts` passed after regeneration: 83 compliant, zero
  invalid and 9 existing length warnings. `git diff --check` passed.

Owner-push RPC still needs lossless metadata/time/setting projection. Separate
server Will idempotency, JSONL transfer, restore consumers, shared replay,
protected source retention, session execution, owner isolation, cleanup,
capability gates and process/scale acceptance remain required. Product MQTT
E2E is still RED and the listener remains unavailable; the full goal is active.

## Lossless owner-push content

Frozen source context at `d2975716b`: root `AGENTS.md` remains
`d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`;
`internal/access/node/FLOW.md` is
`456bab390a29044e80d250a00532c95ee92548cdb2b481070265561c4c655459`;
`internal/runtime/delivery/FLOW.md` is
`82ce3526ea629c78e67abb809b56ba2606ec40756c64fc5021e853b5e4bd008b`.

The owner-push compatibility projection and request version 2 now preserve all
committed-envelope fields, including publication bytes, original timestamp,
setting, topic, expiry and SyncOnce. Sequence-zero transient messages use the
same content path. Each nonzero extension selects version 2; envelopes with no
extensions keep literal version-1 bytes, and responses remain version 1. No
lossy downgrade is attempted. Native sends carrying append timestamps therefore
also require upgraded owner nodes, as recorded in the Changelog.

Tests preceded the changes and reproduced loss of each extension and acceptance
of malformed metadata. They now verify both independent projection directions,
bounded decoding, every incomplete prefix, version refusal, the native literal
fixture and an actual client/handler codec round trip with canonical owner-push
results. The common metadata decoder validates before copying and is shared with
append RPC. This is entry-level in-memory integration, not MQTT process E2E.

Validation: complete tests for node access, delivery runtime/infrastructure,
online-delivery contracts and app passed (`GOWORK=off go test` with those five
package paths, `-count=1 -timeout=90s`). Node-access publication race tests passed
with the existing macOS linker warning. Named `flow-doc-contracts` passed after
regeneration with 83 compliant files, zero invalid and 9 existing warnings;
`git diff --check` passed.

The publication path still needs offline JSONL/restore preservation and rollout
capability gates. Persistent execution, server Will idempotency, shared replay,
source protection, owner isolation and full product/scale acceptance remain
unfinished; the listener remains unavailable and the full goal stays active.

## Offline publication preservation

Frozen source context at `1ab735c25`: root `AGENTS.md` remains
`d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`;
`pkg/db/FLOW.md` is
`49c5fe18bcf98edd7bc072dececaf0114f8d51d77f52cffb96b49f240dd2584e`;
`pkg/db/message/FLOW.md` is
`7915801bd8c77f7068623e6927143cbe75d0c88aea874c79b75d927d33448a21`.

Message inspection returns independently owned optional publication bytes.
JSONL export/import preserves them as `publication_metadata_b64`, absent on native
rows. Preflight bounds base64 allocation, validates canonical content, requires
a positive original source timestamp and checks expiry arithmetic before writes.
Import batching includes metadata bytes. Both verify modes bind exact metadata
SHA-256; native JSONL and digest fixtures remain unchanged. Older strict bundle
readers reject the new field rather than silently dropping it.

Tests were written first and reproduced missing export data, false equality for
metadata-only changes, absent decoding support and byte-budget undercounting.
Real-store tests cover ordinary/Will export, validation, import, reopen,
inspection ownership and both comparison modes with 256 hash slots. Invalid
base64/versions/bounds, expiry overflow and missing source time fail preflight.
An additional RED test caught that metadata without an expiry property could
otherwise pass preflight with a zero timestamp and fail later during storage;
the preflight check now matches storage's positive-timestamp requirement.

Validation: `GOWORK=off go test ./pkg/db/... -count=1 -timeout=90s` passed.
After the final preflight correction, the complete transfer suite and its focused
publication race tests passed again. The existing macOS linker warning remains.
Named `flow-doc-contracts` passed after regeneration: 83 compliant, zero invalid,
9 existing warnings. `git diff --check` passed.

Read-only audit found that Node restore verification and installation retain
seekable message snapshot bytes and delegate to the already covered canonical
backup reader/importer; they do not reconstruct payload-only records. This audit
does not constitute complete product MQTT restore acceptance. MQTT state tables,
owner fencing after restore, separate Will append identity, shared replay/source
protection, runtime execution and capability/process/scale gates remain required.
The listener remains unavailable and the full goal is active.

## Separate Will append identity

Source context is `84b99eb1d`; the applicable frozen digests, durable layouts
and pre-implementation failure inventory are recorded in
[`mqtt-will-idempotency.md`](../specs/mqtt-will-idempotency.md).

Publication metadata v2 now carries a canonical server Will intent key. Ordinary
content and unkeyed configuration templates retain v1 bytes. Message unique
index 8 derives its key from that identity and sender UID; native client index 4
is unchanged. Wills keep their original client number and use nonunique index 3
for history lookup. Durable row verification, append/follower validation,
recovery replacement, truncation, retention and backup import preserve domain
separation. Will lookups use point proofs without adding a native negative-filter
cache or scan. The legacy row-free reservation API cannot populate this domain.

The Channel store, Node and infrastructure adapter expose the separate lookup
capability. Product retry still proves the original record through the current
Leader's committed read; a missing server capability fails closed. Send admission
rejects unkeyed Will templates before routing or ID allocation. CONNECT mapping
reserves the 79-byte identity tail before accepting configuration, while ordinary
MQTT retains its existing metadata limit. Runtime execution must bind the durable
Will row's key; these bytes alone prove no lease or authorization.

Tests were written first. Codec/store API tests initially failed to compile
because identity support was absent. The real app integration then reproduced
the substantive retry bug: a Will committed alongside a native message with the
same client number, but its retry failed with log conflict through the old lookup
domain. That test now passes with 256 hash slots, two independent Will intents,
stable retry results, native retry and changed client/body rejection. Separate
RED tests caught acceptance of an oversized future publication and admission of
an unbound Will template before their guards were implemented. Store coverage
includes one-batch collisions, strict/server-allocated/follower modes, invalid
identity, reopen, portable backup/import, domain-safe deletion, retained-prefix
recovery checks and a valid-looking index tuple pointing at another Will.

Validation passed:

- Full publication, `pkg/db/...` and `pkg/channel/...` suites; after adding the
  lookup ports, complete Channel store, `pkg/cluster/...`, infrastructure cluster
  and app suites passed again. Complete MQTT access and channelappend runtime
  suites passed after their final admission changes.
- App integration tests for Will-domain retry and publication retry after edit
  passed together (`-tags=integration`, `-count=1`, `-timeout=70s`).
- Focused race tests for publication, MessageDB, infrastructure cluster and MQTT
  access passed; the existing macOS linker warning remains.
- Named `flow-doc-contracts`: 83 compliant, zero invalid, 9 existing warnings.

Still required: fenced Will execution and current permission checks, retention
through ambiguous publication-result resolution, shared replay/source protection,
persistent session runtime, owner isolation, MQTT state transfer/restore fencing,
capability gating and full process/scale acceptance. The product listener remains
unavailable and the full implementation goal stays active.


## Source-protection storage boundary

Frozen source `c8f7eb474` and applicable digests are recorded in
[`mqtt-source-protection.md`](../specs/mqtt-source-protection.md), together with
the failure inventory written before code. Shared replay itself remains pending.

Message System 12 now stores a key-bound checksummed source incarnation,
monotonic CAS revision, immutable activation boundary, copied-through position
and externally verified receipt reference. Local apply checks shape, current
revision, unchanged incarnation/start, strictly advancing coverage and local HW;
it cannot prove distributed activation or the receipt's origin. There is no
product caller or permission to activate MQTT from this primitive alone.

Physical retention clamps its eligible range at copied-through while logical
history may advance. Empty-log protection explicitly avoids the existing raw
reader's zero-as-unbounded sentinel. This extra boundary test first reproduced
physical deletion of the first protected message, then passed after the scan
was suppressed at an empty eligible range. Protected reads check incarnation
and local HW under append ownership, return owned original bytes within row and
payload/metadata budgets, and reject missing positions instead of skipping them.
Suffix truncation cannot erase a protected source's committed positions.
An additional RED checkpoint-loss test drove explicit zero-checkpoint creation
in the same commit, with append/checkpoint lock ordering. Once source state
exists, a missing checkpoint fails reads, exact retries and truncation closed.

Binary backup exports/imports the System record and validates it against the
selected HW. Tests cover state/read preservation after reopen and portable
restore, safe cleanup boundaries, identity/CAS/receipt failures, protection after
physical erasure, uncommitted reads and missing original rows. The initial API
checks were RED before implementation; focused source tests now pass.

Validation: complete `GOWORK=off go test ./pkg/db/... ./pkg/channel/... -count=1
-timeout=90s` passed, as did focused MessageDB source-protection race tests. The
existing macOS linker warning remains. Named `flow-doc-contracts` passed after
index regeneration: 83 compliant, zero invalid, 9 existing warnings.
After the checkpoint guard, the complete MessageDB suite and focused source race
tests passed again.

This is replica storage groundwork, not distributed reliability acceptance.
Replicated activation/copy receipts, shared content and counting indexes, source
lifecycle/deletion, every-entry admission and first-person-message registration,
restore fencing, MQTT state JSONL transfer, capabilities and full session/network
execution remain necessary. Product MQTT stays unavailable; the full goal remains
active. Old writers can ignore this protection, so mixed-writer activation is
explicitly unsupported.

## Shared replay replica table and portable recovery

Frozen source `7d53fd4c4`, applicable digests and the pre-code failure inventory
are recorded in [`mqtt-shared-replay.md`](../specs/mqtt-shared-replay.md).
Repository boundary review keeps message bodies out of Slot metadata commands.
The seventh logical table is message-domain table 2, in the original Channel
partition, with independent source-incarnation/position/content-version keys.
No ordinary global MessageID index entry is created for a shared copy.

Bounded copies require protected, committed, contiguous original source records.
They synchronously commit immutable content, prefix counters, small metering
index 2 and table System 1 together. Repeated copies read existing content after
source cleanup; errors cannot leave a partially copied page. Original publication
properties and all message fields are retained. `PayloadSize` is canonicalized
to actual payload length because append/fetch callers can supply different local
size hints for identical publications. A two-replica storage test first reproduced
different content/proofs from those hints and now proves equality. Copying does
not advance source System 12: no local digest is claimed as quorum evidence.

Range counts use two small cumulative endpoints without fetching bodies. Read
pages validate contiguous digest links and own their bytes. Copying across a
missing durable tail first passed incorrectly in a RED test, then failed closed
after explicit tail verification. The shared keyspace survives ordinary history
cleanup and database reopen, without per-session content duplication.

Pinned binary backups use version 2 only when replay content exists, including
the frontier and each original-content row. Native-only streams keep version 1.
Both new-format import entrypoints preflight the entire stream and target replay
coverage before writing; restore rebuilds metering and publishes the frontier
last. A RED target-conflict test exposed source-state overwrite by older backups;
v1/v2 imports now reject conflicting replay coverage before that overwrite.
Tests also cover exact import retry, corrupt replay under a repaired outer CRC,
snapshot pinning during another copy, all ordinary history already removed,
source/cut mismatch, missing positions, bounds and key-bound row corruption.

Validation passed:

- `GOWORK=off go test ./pkg/db/... ./pkg/channel/... -count=1 -timeout=90s`.
- After size-hint canonicalization, the complete MessageDB suite and focused
  `-race` replay tests passed. The existing macOS linker warning remains.
- Named `flow-doc-contracts`: 83 compliant, zero invalid, 9 existing warnings;
  the FLOW index was regenerated. `git diff --check` passed.

All seven logical tables now have storage groundwork. This slice does not add
distributed replay replication, source activation, migration/learner catch-up,
consumer-proof GC, MQTT-state JSONL, restored-owner fencing or product wiring.
Those and the persistent session/network runtime remain required; MQTT access
stays unavailable and the full implementation goal remains active.

## Authoritative MQTT metadata access

Frozen source `9f031e9ae`, applicable digests, the pre-code failure inventory and
the exact wire/routing contract are in
[`mqtt-slot-access.md`](../specs/mqtt-slot-access.md).

The six metadata tables now have a distributed Slot facade and foreground-gated
`cluster.Node` entrypoints. A versioned length-delimited namespace/ClientID hash
keeps all Session lifetimes and children together. Source bindings share existing
Channel-ID/UID routing. All seven write commands require deterministic committed
results; missing result capability fails before submission, and fenced or malformed
results cannot masquerade as success. Additional RED checks exposed a missing
Session-state field being accepted in cursor results; decision fields now reject
missing state, inconsistent termination reasons and impossible Will references.

Read RPC 91, format 1, supports bounded point reads and recovery pages. The actual
Slot leader establishes a fresh ReadIndex/durable-apply barrier, reads primary
rows and secondary indexes from one pinned snapshot, and rechecks authority and
routing. Session children share that snapshot with their current Session. Scans
name a logical hash Slot rather than equating it with a Raft group. No stale-local
fallback or cached negative result is allowed. Requests/replies, selected fields,
page counts, identities and complete continuation cursors are bounded/validated.
The final-page cursor retains existing table semantics; a failing remote-page
test caught an initially over-strict validator before it was corrected.

The metadata snapshot and proxy/Node APIs were first exercised by failing tests
before implementation. Focused cases cover stale owner mutation, remote reads
when the origin has no row, fresh barriers, source versus Session routing,
detached Will ownership, wrong hash-Slot ownership, route changes, malformed RPC,
missing proposal results, response shape and foreground maintenance gates.

Validation:

- Complete default suites passed: `GOWORK=off go test ./pkg/db/meta
  ./pkg/slot/... ./pkg/cluster/... -count=1 -timeout=90s`.
- The focused snapshot/proxy race run passed with the pre-existing macOS linker
  warning: `GOWORK=off go test -race ./pkg/db/meta ./pkg/slot/proxy
  -run '^TestMQTT(Read|Slot|Routing|Writes|SourceAnd)' -count=1 -timeout=90s`.
- Real cluster integration passed in 12.46 seconds: `GOWORK=off go test
  -tags=integration ./pkg/cluster
  -run '^TestMQTTMetadataThreeNodeAuthorityAndRecovery$' -count=1 -timeout=90s -v`.
  It used three nodes, 256 logical hash Slots, two physical Slots and three
  replicas; the tested Session mapped to hash Slot 223 / physical Slot 2.
  Authority moved from node 1 to node 2, revision 2 survived reconstruction of
  the old leader from its durable directory, and an isolated leader rejected
  both reads and writes. This is real metadata Raft/transport coverage, not
  process-level MQTT client acceptance or connection-owner isolation.
- An earlier broad attempt failed during compilation because the filesystem ran
  out of space. Clearing only regenerable Go build cache recovered about 13 GiB;
  the complete command above subsequently passed. No source/data was removed.
- After tightening committed decision validation, the complete Slot proxy suite
  and its focused `-race -run '^TestMQTT'` suite passed again.
- Named `flow-doc-contracts` passed after regenerating the index: 83 compliant,
  zero invalid and the same 9 existing warnings. Updated FLOW files remain at
  their previous line counts. `git diff --check` passed.

No listener, MQTT runtime, owner isolation, source activation, shared replay
replication, restore fencing or rollout capability gate is introduced here.
These remain required by the approved full goal; product MQTT stays unavailable.

## Owner-local execution and quiescence RPC

Frozen source `ec0367594`, applicable digests and the pre-code failure inventory
are in [`mqtt-owner-execution.md`](../specs/mqtt-owner-execution.md).

New entry-neutral identity contracts separate broker/ClientID, Session and owner
generations, node, registry boot and locally issued connection IDs. The local
runtime bounds pending/active/closing owners and admitted operations, checks
monotonic lease expiry at admission, and maintains one indexed deadline per
retained owner. Renewal cannot resurrect an expired owner or grow stale timers.
Aggregate diagnostics are constant-time. Callbacks and waits stay outside the
registry lock; sweep pages and shutdown cleanup are bounded.

Quiescence permanently fences new work and cancels admitted scopes, but succeeds
only after the injected physical-close callback and every explicit scope finish.
Failures, panics and cancellation retain the fenced owner and capacity. Concurrent
requests share one close attempt. Absence proves inactivity only for a retired,
registry-issued identity in this exact boot; registry reconstruction needs a new
boot. No per-owner worker or unbounded tombstone map was introduced.

Node RPC 92 uses bounded version-1 `WKMQ`/`WKMq` frames and exact identity echoes.
It targets only the socket owner and cannot turn unsupported responses, malformed
replies, foreign boots or transport errors into isolation proof. It retains the
existing mutation execution-cancellation policy and is not registered in app yet.

Tests were written before each implementation. The first runtime run failed on
missing contracts; the first RPC run failed on missing codec/service symbols.
Channel-coordinated tests cover pending admission, lease expiry without a sweep,
close errors/panics, retry/coalescing, cancellation versus scope completion, stale
close isolation, capacity, bounded deadlines, shutdown and malformed RPC frames.
They use no real sleeps or network listeners. Validation passed:

- `GOWORK=off go test ./internal/contracts/mqttsession
  ./internal/runtime/mqttsession ./internal/access/node ./pkg/cluster/net
  -count=1 -timeout=90s`.
- `GOWORK=off go test -race ./internal/runtime/mqttsession ./internal/access/node
  -run 'TestOwner|TestMQTT' -count=1 -timeout=60s`; the existing macOS linker
  warning remains. Local log: `/tmp/mqtt-owner-execution-race.log`.
- Named `flow-doc-contracts`: 85 compliant, zero invalid and the same 9 existing
  warnings after regenerating the index. `git diff --check` passed.

Gateway close currently requests transport closure without exposing physical
completion; this must be fixed before supplying the runtime callback. Distributed
lease acquisition/derivation, foreground app composition and durable lifecycle
orchestration also remain required. Local lease expiry alone does not prove
already admitted effects drained. The product MQTT listener remains unavailable;
the full approved implementation goal is still active.

## Gateway physical close completion

Frozen source `0ddb24890`, applicable digests and the pre-code failure inventory
are in [`gateway-transport-close-proof.md`](../specs/gateway-transport-close-proof.md).
The pinned gnet v2.9.7 implementation calls OnClose before residual writes and the
socket close syscall. We therefore use CloseWithCallback completion; the queued
actor close event, ordinary Conn.Close and logical Session.Close are not evidence.

Gateway Context now carries a per-connection TransportCloser capability without
allocating another per-packet callback. CloseTransportAndWait fences new inbound
and outbound admission, cancels request work and joins physical closure. It never
enters the ordinary lifecycle close callback or waits for open/business cleanup;
normal transport notification still performs that ordered cleanup. The original
close reason survives it. Missing transport or Session capabilities fail explicitly.
The Session fence is nonblocking even when an earlier encoder has entered; owner
scopes must still cover that earlier effect through its completion.

The gnet transport lazily retains one close receipt and fences new write paths.
All waiters share its one submission. Submission and callback completion must
both succeed, including synchronous callbacks and ambiguous enqueue errors. A
failure remains failure; cancellation stops waiting without queuing another close
or erasing the request. No waiter goroutine, timer or per-connection worker was
added. Physical isolation uses raw close for WebSocket, not a graceful close-frame
handshake, and does not claim client receipt of buffered application data.

The first focused test run failed on the missing capabilities before code was
written. A further pre-fix failure exposed cancellation between core's admission
fence and transport submission: an already canceled transport wait must still
request close. Core checks preexisting caller cancellation before changing its
admission state, but cancellation after that point cannot suppress socket closure.
Default tests cover that boundary, coalescing, callback/submission ordering,
sticky failures, unsupported capability and independent lifecycle cleanup.

Validation passed:

- `GOWORK=off go test ./pkg/gateway/... ./internal/access/gateway
  ./internal/access/mqtt -count=1 -timeout=90s`.
- `GOWORK=off go test -race ./pkg/gateway/session ./pkg/gateway/core
  ./pkg/gateway/transport/gnet -run
  'TestOutboundFence|TestCloseWait|TestContextPhysicalClose'
  -count=1 -timeout=60s`.
- `GOWORK=off go test -tags=integration ./pkg/gateway/core
  ./pkg/gateway/transport/gnet -run
  'TestPhysicalTransportCloseProof|TestPacketProtocol|TestTCPListenerDelivers|TestTCPAndWebSocketListenersShareOneEngineGroup'
  -count=1 -timeout=90s -v`.
  Real TCP and WebSocket sockets exchanged MQTT CONNECT/CONNACK, then proved
  physical close and rejected late writes while the business close callback was
  deliberately blocked. Repeated waits succeeded without depending on that
  callback. The JSON evidence for both networks is printed in the reproducible
  test output; local retained logs are `/tmp/mqtt-transport-close-integration.log`,
  `/tmp/mqtt-transport-close-default.log` and `/tmp/mqtt-transport-close-race.log`.
- Named `flow-doc-contracts`: 85 compliant, zero invalid, same 9 existing warnings.
  The gateway FLOW remains 100 lines. `git diff --check` passed.

This closes the gateway proof seam identified in the preceding slice. MQTT owner
RPC registration, conservative authoritative lease derivation, lifecycle usecase
orchestration, product authentication/listener composition, distributed replay
and remaining accepted recovery/acceptance work are still outstanding. These are
real gateway transport integrations, not the full process-level MQTT product E2E.
The product listener remains unavailable and the full goal remains active.

## Authenticated Session acquisition, renewal and disconnect

Frozen source `571963d33`, applicable context digests and the pre-code failure
inventory are in [`mqtt-session-acquisition.md`](../specs/mqtt-session-acquisition.md).
The new entry-neutral usecase verifies the existing device credential and Will
permission, preserves ClientID/UID binding, proves exact old-owner isolation and
rereads current Slot authority before proposing. It rechecks authorization after
isolation, reserves a bounded local owner, commits the atomic Session/Will
transition, and only then activates execution. Concurrent acquisition conflicts
do not loop through evicting successors. Resume preserves delivery counters,
allocators and lifetime quotas; new lifetimes reset them.

Lease grants capture their monotonic deadline before the proposal and keep it
through acknowledgement. Durable milliseconds round upward by less than one
millisecond, while the local deadline remains unchanged. Renewal preserves the
Session's other fields; confirmed ownership loss or invalid clock/evidence fences
local admission immediately. A failed candidate is fenced independently of caller
cancellation and receives bounded physical cleanup. Fence itself neither waits
for transport nor claims isolation. Expired active owners first commit abnormal
disconnect at the recorded execution boundary before reconnect decides their
Will and offline lifetime.

Disconnect records observation before metadata/RPC/drain delays, joins exact
owner isolation outside admitted scopes, and rereads ownership before committing
the Will decision and expiry. Slow isolation cannot turn an accepted normal
disconnect into a Will publication or restart its offline lifetime. Stale owners
cannot disconnect a successor. An originally zero expiry cannot be extended.

Tests preceded implementation. The initial RED run found the missing usecase and
nonblocking owner Fence. Additional failing regressions were written before fixes
for known-ended/invalid-clock renewal retaining admission, millisecond truncation
putting the durable lease before its local gate, dependency panic leaking a renewal
scope, and slow disconnect isolation changing normal intent/offline timing.

Validation passed:

- `GOWORK=off go test ./internal/usecase/mqttsession
  ./internal/runtime/mqttsession -count=1 -timeout=60s`.
- `GOWORK=off go test -race ./internal/usecase/mqttsession
  ./internal/runtime/mqttsession -count=1 -timeout=90s`.
  Final package times were 2.982 and 1.581 seconds; the pre-existing macOS linker
  warning remains. Log: `/tmp/mqtt-session-acquisition-race.log`.
- `GOWORK=off go test -tags=integration ./internal/app
  -run '^TestMQTTSessionAcquisitionThreeNodeRPC$' -count=1 -timeout=90s -v`.
  Final test time was 7.84 seconds. Three real nodes used 256 logical hash Slots,
  two physical Slots and three replicas. The Session mapped to hash Slot 223 /
  physical Slot 2 with metadata leader 1; connection ownership moved 2 -> 3 -> 1
  while preserving Session generation 1 and advancing owner generation to 3.
  Real device-token verification rejected bad credentials before isolation;
  remote RPC 92 waited for an admitted scope to drain; stale disconnect was
  rejected and Receive Maximum 1 survived renewal. The reproducible evidence is
  printed by the test and retained in `/tmp/mqtt-session-acquisition-integration.log`.
  An earlier run passed business assertions but failed temporary-directory
  cleanup ordering; one parent directory now outlives joined node shutdown and
  both subsequent complete runs passed.
- Named `flow-doc-contracts` passed after regenerating the index: 86 compliant,
  zero invalid and the same 9 existing warnings. `git diff --check` passed.

This integration composes real Slot authority, authentication and owner RPC, but
uses controlled physical-close callbacks. Real TCP/WebSocket close was verified
in the preceding gateway slice; neither test substitutes for full process-level
MQTT product acceptance. Product configuration/listener composition, valid
unreachable/restarted-owner recovery proof, source activation and shared replay
replication, delivery/ACK/Will workers, revocation, restore and the remaining
approved acceptance work are still required. The product listener stays
unavailable and the full implementation goal remains active.

## Current IM permission checks for Will setup

Frozen source `21e808f9c`, applicable context digests and the pre-code failure
inventory are recorded in the Will setup section of
[`mqtt-session-acquisition.md`](../specs/mqtt-session-acquisition.md).

The message usecase now exposes a read-only publish-permission query containing
only the authenticated sender and ordinary person/group destination. It reuses
the existing business policy and its reason precedence, but requires authority
and bypasses the optional SEND cache for every fact. It cannot accept system-
device or request-scoped controls, reinterpret a command suffix or encoded person
conversation, create person directories, run send hooks or append a message.
Missing authority, invalid input, cancellation and infrastructure errors remain
explicit. The app Will adapter only translates sibling DTOs and maps a confirmed
negative policy result to `ErrWillDenied`. Setup grants no future execution.

Tests were written first and failed on the missing query, adapter and denial
error before production code was added. Coverage includes a warm SEND cache
followed by member removal, denylist and sender-ban changes; group policy order;
person normalization and disband; absence of send side effects; configured system
UID versus forbidden device bypass; malformed targets; missing/canceled authority
and preserved storage errors.

The existing three-node acquisition integration now composes the real cluster
permission adapter and this Will authorizer. It stores an allowed Will, removes
the publisher from the group, rejects replacement CONNECT before closing the
accepted owner or changing its revision, then verifies normal disconnect cancels
the durable Will. It retains the earlier owner 2 -> 3 -> 1 assertions and uses
256 logical hash Slots, two physical Slots and three replicas. It still uses
controlled physical-close callbacks, not a complete MQTT product listener.

Validation:

- `GOWORK=off go test ./internal/usecase/message ./internal/usecase/mqttsession
  -count=1 -timeout=90s` passed (0.390 / 1.895 seconds).
- `GOWORK=off go test -race ./internal/usecase/message
  -run '^TestPublishPermission|^TestSend.*Permission' -count=1 -timeout=90s`
  passed in 1.884 seconds with the pre-existing macOS linker warning; log:
  `/tmp/mqtt-will-authorization-race.log`. `git diff --check` passed.
- `GOWORK=off go test -tags=integration ./internal/app
  -run '^TestMQTTSessionAcquisitionThreeNodeRPC$' -count=1 -timeout=90s -v`
  passed in 9.35 seconds. Evidence includes `armed_durable=true`,
  `revoked_replacement_rejected_before_isolation=true` and
  `normal_disconnect_cancelled=true`; log:
  `/tmp/mqtt-will-authorization-integration.log`.
- Named `flow-doc-contracts` passed after index regeneration: 86 compliant,
  zero invalid and the same 9 existing warnings. The already-over-target app and
  message FLOW files retain concise new navigation needed for this permission
  boundary; restructuring their unrelated flows is outside this change.

The Will worker, execution-time reauthorization, product MQTT composition and all
remaining recovery/replay/delivery acceptance work are still required. This seam
does not enable the product listener or complete the full implementation goal.

## Session and Waiting Will deadline reconciliation

Frozen source `4c329e595`, applicable context digests and the pre-code failure
inventory are in [`mqtt-session-deadlines.md`](../specs/mqtt-session-deadlines.md).

`ReconcileDeadline` takes one complete observed owner, rereads current Slot
authority and commits at most one lifecycle event. Missing/replaced owners are
fenced; renewed live leases and already-ended rows are no-ops. Expired active
owners still require the existing exact quiescence proof and abnormal disconnect
path. Unreachable owners remain errors, not presumed dead from elapsed time.
Delayed reconciliation retains the recorded execution boundary for Will Delay
and offline lifetime rather than starting those clocks at cleanup time.

Offline reconciliation reads a referenced Waiting Will and its current Session
from one authoritative snapshot. It validates the complete reference/owner,
rejects missing or inconsistent evidence, and uses the existing atomic WillDue
command to detach Ready work. The offline Session stays resumable until its own
expiry; later expiry ends it without rewriting detached work or resurrecting a
cancelled Will. Counters, allocators, quotas and UID binding survive. A concurrent
reconnect wins or loses the existing owner/revision CAS; there is no local retry
loop that could overwrite a successor.

The first tests failed on the missing method before implementation. Deterministic
storage tests cover renewed/stale candidates, unknown isolation, late execution
boundaries, Will Delay before expiry, terminal idempotence, cancelled/Ready Will
preservation, reconnect during proposal, corrupt/foreign/missing snapshots,
uncertain/malformed/conflicting replies, overflow, clock regression and cancellation.

Validation passed:

- `GOWORK=off go test ./internal/usecase/mqttsession
  ./internal/runtime/mqttsession -count=1 -timeout=90s` (4.178 / 1.153 seconds);
  log: `/tmp/mqtt-session-deadlines-default.log`.
- `GOWORK=off go test -race ./internal/usecase/mqttsession -count=1 -timeout=90s`
  (5.423 seconds), with the existing macOS linker warning; log:
  `/tmp/mqtt-session-deadlines-race.log`.
- `GOWORK=off go test -tags=integration ./internal/app
  -run '^TestMQTTSessionAcquisitionThreeNodeRPC$' -count=1 -timeout=90s -v`
  passed in 11.66 seconds. The three-node/256-hash-Slot integration retains
  acquisition and current-permission assertions, then uses another node to
  promote a delayed Will and end its offline Session. Evidence reports
  `delayed_will_ready=true`, `expired_session_ended=true`, and
  `detached_will_preserved=true`; log: `/tmp/mqtt-session-deadlines-integration.log`.
- Named `flow-doc-contracts` passed after reducing Read First navigation to its
  five-reference bound and regenerating the index: 86 compliant, zero invalid,
  same 9 existing warnings. `git diff --check` passed.

This adds the authoritative advancement operation, not its periodic scheduler or
a publisher. Scheduling must fairly page both Session and Waiting Will indexes;
Session expiry alone misses shorter Will delays. Actual Will execution remains
dependent on fenced claims, current authorization and source retention through
ambiguous append resolution. Product MQTT composition, replay/delivery/recovery
and the full process-level acceptance remain unfinished; the goal stays active.

## Bounded node-owned deadline scheduling

Frozen source `065ff2174`, applicable digests and the pre-code failure inventory
are in [`mqtt-deadline-worker.md`](../specs/mqtt-deadline-worker.md).

One optional managed MQTT singleton now rotates Session-deadline and Will-recovery
index pages over the node's current locally led logical hash Slots. It validates
the bounded unique Slot list, drops cursors for lost ownership, and alternates
index/Slot positions even when a page fails or exhausts the visit budget. Defaults
are 256 hash Slots, 200ms interval, a two-second turn, 250ms per dependency call,
eight pages of at most 16 rows and at most 64 visited candidates. There is no
per-session worker, timer, queue or detached timeout task.

Each complete page is validated before any lifecycle call. Cursor ordering matches
the persisted deadline/length-prefixed identity/generation tuple. Only visited
rows advance process hints; failed candidates stay durable and retry after wrap.
Unvisited rows survive partial turns, and future boundaries reset that stream.
Ready/executing Wills are visited without lifecycle mutation so they cannot block
Waiting work; actual publication remains a separate fenced responsibility.

Start owns an explicit lifetime independent of its startup context. Stop cancels
and joins the exact loop before dependencies close; a timed-out stop retains it
and rejects an overlapping restart. A joined restart starts with fresh cursors.
The task registry adds only fixed `mqtt/deadline_worker` labels. Observations carry
aggregate counts and duration, without identities, bodies or error strings.

Unit and integration tests were written before the implementation and failed on
missing worker/catalog symbols. They cover all 256 Slots and both indexes, tiny
visit budgets, failed/unvisited cursor progress, future boundaries, detached Will
work, ownership loss, malformed lists/pages, encoded tie ordering, bounded
configuration and joined stop/restart. Additional failing tests exposed two
pre-fix errors: late nil results after per-call timeout were accepted, and future
candidates did not consume the visit budget. Both are fixed; source lists/pages
are rejected if their context expired before return, and every inspected candidate
consumes budget. Active calls are still joined rather than abandoned.

Validation passed after the fixes:

- `GOWORK=off go test ./internal/runtime/mqttsession
  ./internal/usecase/mqttsession ./pkg/goroutine -count=1 -timeout=90s`
  (0.823 / 4.358 / 1.435 seconds); `/tmp/mqtt-deadline-worker-default.log`.
- `GOWORK=off go test -race -tags=integration ./internal/runtime/mqttsession
  -run '^TestDeadlineWorker' -count=1 -timeout=90s` passed in 1.398 seconds,
  including joined shutdown and overdue dependency results. The existing macOS
  linker warning remains; `/tmp/mqtt-deadline-worker-race.log`.
- `GOWORK=off go test -tags=integration ./internal/runtime/mqttsession
  ./internal/app -run '^TestDeadlineWorkerJoinedStopAndFreshRestart$|^TestDeadlineWorkerRejectsLateSuccessAfterCallDeadline$|^TestMQTTSessionAcquisitionThreeNodeRPC$'
  -count=1 -timeout=90s -v` passed. The real three-node test took 11.94 seconds
  with 256 logical hash Slots / two physical Slots / three replicas. Its deadline
  phase now starts the real workers instead of manually calling reconciliation;
  evidence reports `owned_hash_slot_scans=true`, `automatic_reconciliation=true`,
  `delayed_will_ready=true`, `expired_session_ended=true`, and
  `detached_will_preserved=true`. Runtime lifecycle evidence reports two managed
  runs, joined canceled work and fresh restart cursors. Log:
  `/tmp/mqtt-deadline-worker-integration.log`.
- Named `flow-doc-contracts` passed after index regeneration: 86 compliant,
  zero invalid, same 9 existing warnings. `git diff --check` passed.

The three-node composition remains an integration harness with controlled socket
close callbacks, not a complete product MQTT listener. Product startup/stop/restore
wiring, live-owner renewal/cleanup scheduling, valid unavailable-owner fencing,
Will execution with retained idempotency, distributed replay/delivery and full
process-level acceptance are still required. MQTT admission remains unavailable
and the original full implementation goal stays active.

## Authenticated inbound PUBLISH bridge

Frozen source `62129e7bb`, applicable digests and the pre-code failure inventory
are in [`mqtt-publish-entry.md`](../specs/mqtt-publish-entry.md). This slice adds
no database table or product listener/configuration.

`internal/access/mqtt.Publisher` obtains the authenticated UID from an admitted
owner operation, rejects mismatched connection identity and privileged device
flags, maps owned publication bytes, checks current ordinary-topic permission,
and calls the existing message usecase. ClientID never becomes a privileged
DeviceID; PacketID remains protocol correlation, separate from client_msg_no,
MessageID and ClientSeq. QoS 0 persists without PUBACK. QoS 1 success requires a
valid committed ID/sequence; explicit permission/business rejection gets a valid
MQTT reason, while uncertain results produce no PUBACK and request closure.

The operation remains held through reply enqueue. Dependency contexts now combine
owner cancellation with the earlier request/entry deadline. A failing regression
caught the initial use of a gateway-only context, which did not cancel Send during
takeover; this is fixed. Lease/cancellation checks after dependencies suppress late
replies, and fixed diagnostics redact arbitrary dependency error/panic text.

Inspection of the real append router exposed a second boundary: Future.Wait and
remote forwarding can return while admitted Channel work continues. Before
releasing an uncertain Send's local scope, MarkUncertain permanently fences and
retains its owner. Physical closure plus zero local operations wakes all quiescence
waiters with isolation-unproved. Repeated marking, Sweep, Close, lease expiry and
capacity pressure cannot turn this into a successful proof. The state is bounded
by owner capacity and exposed only as an aggregate count. A valid committed or
definite rejection receipt resolves the append result even when the entry context
has expired; no late ACK is emitted. This registry intentionally provides no
unproved clearing/reset path. Recovering these uncertain attempts needs a separate
completion/fencing proof before full product takeover can be advertised.

Tests were written before the new entry/runtime implementation; initial runs
failed on missing Publisher/operation symbols. The discovered cancellation and
uncertain-completion bugs received failing regressions before their fixes.

Final validation passed:

- `GOWORK=off go test ./internal/access/mqtt ./internal/runtime/mqttsession
  ./internal/usecase/mqttsession -count=1 -timeout=90s`: 0.411 / 0.643 / 6.190s;
  `/tmp/mqtt-publish-entry-default.log`.
- `GOWORK=off go test -race ./internal/access/mqtt ./internal/runtime/mqttsession
  -run 'TestPublisher|TestOwnerOperation|TestOwnerUncertain' -count=1 -timeout=90s`:
  1.877 / 2.361s; `/tmp/mqtt-publish-entry-race.log`. Existing macOS linker warnings
  remain, with no race report.
- `GOWORK=off go test -tags=integration ./internal/app
  -run '^TestMQTTPublishSingleNodeCluster|^TestMQTTSessionAcquisitionThreeNodeRPC|^TestPublicationSingleNodeClusterRetryAfterEdit'
  -count=1 -timeout=90s -v` passed. The new real single-node cluster test uses 256
  hash Slots, real device-token/Session authority and production message wiring.
  It proves committed metadata, QoS 0 persistence, cross-PID retry deduplication,
  reuse of a completed PID for a distinct message, and immediate denial after
  membership removal. It took 2.99s; existing three-node acquisition/deadline and
  edited-history retry tests took 11.77 / 3.12s. Log:
  `/tmp/mqtt-publish-entry-integration.log`.
- Named `flow-doc-contracts` passed after index regeneration: 86 compliant,
  zero invalid, the same 9 pre-existing line-count warnings. `git diff --check`
  passed.

Transport writes in the new app integration are controlled callbacks. This is
not the full Paho/WK process-level acceptance test. Product listener/start-stop-
restore composition, uncertain/unreachable owner recovery proof, live renewal,
subscription/durable source activation, shared replay delivery, actual Will
execution and recovery/tooling/pressure acceptance remain required. Product MQTT
admission remains unavailable, and the full implementation goal remains active.

## Bounded connection renewal and asynchronous lifecycle cleanup

Frozen source `f9a926c81`, applicable digests and the pre-code failure inventory
are in [`mqtt-connection-supervisor.md`](../specs/mqtt-connection-supervisor.md).
Inspection of the gateway showed that ordinary close callbacks can run
synchronously inside a PUBLISH handler. Calling joined Session disconnect there
would wait for that same handler's admitted scope. This slice supplies the
necessary scheduling boundary before the full PacketHandler can be connected.

`internal/runtime/mqttsession.Connections` registers only an exact owner with an
installed live lease. Each retained owner owns one indexed heap record; repeated
registration or close notification adds no queue entry and does not replace the
first disconnect intent. One managed scheduler and a bounded worker pool renew
halfway through the installed lease and execute cleanup outside entry callbacks.
Defaults are 16 workers, one worker cohort of queued jobs, a one-second call
budget and 250ms retry delay. Capacity includes active, queued, executing and
failed cleanup records. There are no per-owner goroutines or timers, and normal
scheduling uses O(log N) heap operations rather than registry scans.

Renewal success requires a newer lease actually installed in Owners. Cleanup
requires exact physical/local quiescence plus a terminal lifecycle result;
unresolved publish barriers, failed callbacks and late nil results remain
retained. Callbacks receive copied intent and fixed error handling. Snapshot
counters and fixed `mqtt/connection_scheduler` / `mqtt/connection_worker` tasks
carry no identities or arbitrary error text.

The Session disconnect command now accepts an optional trusted node-local
monotonic observation. Queued cleanup and retries preserve this original time;
future or wall-only observations fail before isolation. App adapts renewal and
disconnect DTOs to the existing Session usecase, without adding protocol logic or
changing storage schemas. Existing synchronous callers keep their current clock
capture behavior.

Stop permanently fences owner admission, preserves accepted intent and joins all
registered work. A failing regression found that a failed cleanup at the heap
head could hide live connections behind their future renewal dates. Stop now
makes one bounded-by-capacity O(N) heap pass to expedite those live records;
failed cleanup retains its retry delay. A timed-out Stop keeps the same run and
its dependencies alive for a later join. App must separately close unregistered
Owners. Restore requires a new supervisor and a fresh registry boot, not restart
of this stopped lifetime.

Tests and the failure inventory preceded implementation. Final validation:

- `GOWORK=off go test ./internal/runtime/mqttsession ./internal/usecase/mqttsession
  ./internal/access/mqtt ./pkg/goroutine -count=1 -timeout=90s` passed in
  1.166 / 5.413 / 2.376 / 2.065 seconds; `/tmp/mqtt-connections-default.log`.
- `GOWORK=off go test -race -tags=integration ./internal/runtime/mqttsession
  -run '^TestConnections|^TestConnectionConfiguration' -count=1 -timeout=60s`
  passed in 3.220 seconds, including nonblocking first-intent acceptance,
  bounded capacity/concurrency, false renewal, panic/retry, overdue results,
  joined stop and the failed-cleanup ordering regression. Existing macOS linker
  warnings remain; `/tmp/mqtt-connections-race.log`.
- `GOWORK=off go test -tags=integration ./internal/runtime/mqttsession
  ./internal/app -run '^TestConnections|^TestMQTTConnectionSupervisorSingleNodeCluster$|^TestMQTTSessionAcquisitionThreeNodeRPC$|^TestMQTTPublishSingleNodeCluster'
  -count=1 -timeout=90s -v` passed. The new real single-node cluster test uses 256
  hash Slots, real device-token/Session authority, automatic renewed revisions,
  a held entry scope, and original disconnect clock/expiry. It took 4.05 seconds;
  existing PUBLISH and three-node Session tests took 2.86 / 12.67 seconds. Log:
  `/tmp/mqtt-connections-integration.log`.
- Named `flow-doc-contracts` passed after index regeneration: 86 compliant,
  zero invalid, the same 9 existing line-count warnings. `git diff --check` passed.

The new app integration uses controlled transport-close callbacks; it does not
replace full product MQTT/Paho process-level acceptance. Gateway CONNECT/open/
rollback/close mapping and product start/stop/restore registration are still
pending, alongside subscription/delivery, distributed replay/source activation,
Will execution, uncertain/unreachable-owner recovery proof, state transfer and
pressure acceptance. Product MQTT admission remains unavailable and the original
full implementation goal stays active.

## Gateway entry and real Paho transport integration

Frozen source `ffdf821a9`, applicable context digests and the pre-code failure
inventory are in [mqtt-gateway-entry.md](../specs/mqtt-gateway-entry.md). No table,
Slot command or product configuration changes are needed for this slice.

`internal/access/mqtt.Handler` maps CONNECT credentials/Will and negotiated limits
to Session acquisition. Registration precedes acceptance. An owner operation
spans the gateway-owned CONNACK enqueue and transfers exactly once to open or
rollback cleanup. Optional generic `PacketAuthResult.CheckReply` checks live
activation immediately before enqueue; its error/panic prevents CONNACK and
rolls back. Acquisition has a bounded timeout; the handshake operation instead
follows the gateway request lifetime. Both successful and rejected CONNACK honor
the peer's full uint32 Maximum Packet Size. Fixed errors contain no secrets.

Private accepted connection evidence feeds Publisher and owner-gated PING. Client
DISCONNECT validates reason direction and expiry; original zero expiry cannot
be extended. Close/rollback callbacks release handshake state and enqueue cleanup
without waiting for their own packet scope. One normal-intent ordering regression
proved that Connections must receive intent before explicit fencing, otherwise
concurrent renewal can synthesize abnormal cleanup first.

Real Paho then exposed TCP EOF racing queued DISCONNECT. Before the fix, normal
client shutdown left its Will waiting. A deterministic decode/close regression
also failed. The MQTT adapter now retains one constant-size, immutable receipt
of the first fully decoded DISCONNECT, with trusted monotonic observation and
only reason/expiry/server-reference presence. The entry validates it on close;
no client diagnostic strings, properties or payload are retained. This does not
execute pending messages after closure or substitute socket close for isolation.

Tests preceded implementation and each discovered fix. Verification:

- `GOWORK=off go test ./internal/access/mqtt ./pkg/gateway/... -count=1 -timeout=90s`
  passed for all listed gateway packages; `/tmp/mqtt-gateway-default.log`.
- `GOWORK=off go test -race -tags=integration ./internal/access/mqtt ./internal/app
  ./pkg/gateway/core -run '^TestHandler|^TestMQTTGatewayPahoSingleNodeCluster$|^TestPacketProtocol|^TestPhysicalTransportCloseProofTCPAndWebSocket$'
  -count=1 -timeout=120s -v` passed: 2.909 / 5.945 / 2.605 seconds. No data races;
  existing macOS LC_DYSYMTAB linker warnings remain. Log: `/tmp/mqtt-gateway-race.log`.
- The Paho scenario uses real gnet TCP, durable device-token validation, actual
  message/Session usecases and a single-node cluster with 256 hash Slots. It
  proves invalid-token non-eviction, committed PUBACK, same-ID physical takeover,
  persistent Session resume, normal/abnormal Will decisions, small-CONNACK
  rollback and joined cleanup. TCP and WebSocket physical-close regression also
  passed. The test emits a reproducible `mqtt_gateway_evidence` summary.
- Named `flow-doc-contracts` passed after index regeneration: 86 compliant,
  zero invalid, the same 9 existing line-count warnings. `git diff --check` passed.

This is internal app integration, not full product/process-level acceptance.
Subscription/unsubscription/downstream ACK entry, reliable delivery, distributed
source activation/replay, Will execution, uncertain/unavailable-owner recovery,
app lifecycle/restore wiring, state transfer and pressure acceptance remain
required. Product MQTT config stays unavailable and the full goal stays active.

## Recoverable subscription intent orchestration

Source `2a0e94b0a`, frozen context and the pre-code failure inventory are recorded
in [mqtt-subscription-orchestration.md](../specs/mqtt-subscription-orchestration.md).
This slice uses existing table 23 and Slot command 68 without a schema change.

`mqttsession.Subscriptions` derives UID from admitted owner execution, checks
current Session/child evidence and receive authorization, then persists Preparing
before asking the projection port to establish protected recoverable sources and
cursors. A matching exact-intent receipt, fresh owner/child read and permission
recheck precede Active. Active option replacement keeps generation, operation,
target and authorization version; it never resets delivery progress. QoS 2
subscription requests are granted at most QoS 1. Changed permission incarnation
fails closed for the separate revocation mechanism.

Unsubscribe commits Removing before projection seals new matching, and Removed
only after matching completion evidence. It requires owner evidence but not
receive permission, so revoked subscriptions can still be removed. Outstanding
exchanges/content remain the projection/delivery lifecycle's responsibility;
this usecase does not touch inflight rows. Reconcile resumes existing intent
under a new owner of the same durable Session lifetime, preserving its stable
generation and operation identity. A concurrent parent renewal is accepted only
after rereading an unchanged exact child and the same owner.

Admission defaults to 128 retained subscriptions, 16 pages of 64 rows, and a
five-second call budget. Preparing and Removing count toward quota; Removed
tombstones consume scan budget until safe cleanup. All counted pages must share
one Session revision, which also fences the new-intent CAS against concurrent
admission. Exhausted scan budgets or invalid cursor/evidence cannot imply space.
No background worker, per-client lock or unbounded retry loop is introduced.

A deterministic regression showed that an already-cancelled parent could still
activate intent while its context.AfterFunc callback was pending. Execution now
checks parent cancellation synchronously at each boundary. Every exit, including
dependency panic, releases its scope; dependency panic text is not returned.

Verified:

- `GOWORK=off go test ./internal/usecase/mqttsession ./internal/access/mqtt
  ./internal/runtime/mqttsession -count=1 -timeout=90s`: passed in
  6.134 / 1.222 / 0.871 seconds; `/tmp/mqtt-subscriptions-regression.log`.
- `GOWORK=off go test -race ./internal/usecase/mqttsession -run '^TestSubscriptions'
  -count=1 -timeout=60s`: passed in 3.920 seconds. Existing macOS LC_DYSYMTAB linker
  warning only; `/tmp/mqtt-subscriptions-race.log`.
- `GOWORK=off go test -tags=integration ./internal/app
  -run '^TestMQTTSubscriptionIntentSingleNodeCluster$' -count=1 -timeout=60s -v`:
  passed in 3.195 seconds. A real single-node cluster with 256 hash Slots verifies
  unavailable-source Preparing retention, owner resume, stable identity,
  concurrent real renewal, current membership rejection and removal after revoke.
  `/tmp/mqtt-subscriptions-integration.log` includes reproducible evidence with
  `projection=controlled distributed_source_proof=false`.
- Named `flow-doc-contracts` passed: 86 compliant, zero invalid and the same 9
  existing line-count warnings. `git diff --check` passed.

The successful projection receipts in these tests are explicitly controlled
fixtures, never product adapters or independent source durability evidence.
Actual replicated protection/projection, future-person-source admission, offline
reconciliation, revocation ordering, source/cursor limits, safe tombstone GC and
SUBACK/UNSUBACK entry remain required. Product MQTT admission remains unavailable;
these usecases and tests do not substitute for process-level delivery acceptance.

## Protected source checkpoint integrity

Source `6d462a582`; frozen context and the failure inventory precede code in
[mqtt-source-checkpoint-integrity.md](../specs/mqtt-source-checkpoint-integrity.md).
While tracing distributed activation, storage regressions demonstrated that both
raw checkpoint setters could lower protected HW, nine mutation paths could
recreate a lost explicit checkpoint, and both suffix truncation facades could
finish while another operation owned the checkpoint commit mutex.

Checkpoint reads now validate source evidence first, then require a well-formed
explicit checkpoint covering copied-through. Reading in that order avoids
combining pre-activation absence with a newly atomically installed source.
Missing/corrupt evidence also rejects no-op HW writes, exact append retries,
fetched appends and snapshot installation without partial state. The legacy raw
setter still supports non-monotonic native checkpoints, while protected HW
cannot regress. Typed and compatibility suffix cuts hold append then checkpoint
through commit; after waiting, they recheck the committed protection frontier.

This adds bounded point reads, not history scans, queues or unbounded cached
state. No schema, RPC or replicated source authority was added.

Verified:

- Pre-fix regressions failed on the intended behavior in
  `/tmp/mqtt-checkpoint-red.log` and `/tmp/mqtt-checkpoint-race-red.log`; the latter
  proves both cuts returned success while the checkpoint mutex remained owned.
- `GOWORK=off go test ./pkg/db/message ./pkg/channel/store
  ./pkg/channel/replication -count=1 -timeout=120s`: passed in
  17.762 / 5.819 / 2.353 seconds; `/tmp/mqtt-checkpoint-regression.log`.
- `GOWORK=off go test -race -tags=integration ./pkg/db/message
  -run '^TestMQTTCheckpoint' -count=1 -timeout=60s`: passed in 4.134 seconds;
  `/tmp/mqtt-checkpoint-race.log`. Existing macOS LC_DYSYMTAB linker warning only.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, the same nine existing
  line-count warnings; `/tmp/mqtt-checkpoint-flow.log`. `git diff --check` passed.

Distributed activation still needs a durable decision carried through authority
recovery and learner repair. Existing exact proposals replicate message records,
while System 12 is still independently materialized replica state; broadcasting
local CAS calls cannot establish the missing guarantee. The protection decision
must precede subscriber success, survive leader replacement and protect content
before any replica can trim it. Committed control materialization and recoverable
metadata intent are implementation alternatives under evaluation, not completed
mechanisms. Source-copy replication, projection wiring and full product process
acceptance remain required; the full implementation goal stays active.

## Source activation carried by the exact Channel log

Source `9625f6ba3`; frozen context and the pre-code failure inventory are in
[mqtt-source-log-activation.md](../specs/mqtt-source-log-activation.md).
The earlier alternatives are resolved for initial activation: an explicitly
selected format-4 proposal shares the existing Channel sequencer, voter quorum,
exact retry, authority recovery and learner repair. Native record format
selection remains 1–3; payload bytes cannot implicitly select a control.

Format 4 requires one canonical internal SyncOnce record and a distinct entry
hash domain. Its command determines the reserved `mqtt-log-v1:` source generation.
Message System 13 stores the first activation manifest in a checksummed fixed
value; it is part of the same synchronous append as the record and exact indexes.
A pending marker clamps trim at the preceding boundary without publishing active
source state. The covering checkpoint commit atomically creates System 12.
Repeated controls retain the first generation/start. Uncommitted suffix removal
and replacement update the pending marker in the same batch; committed source
obligations remain protected. Storage factories without the explicit capability
reject activation append/recovery instead of acknowledging only message bytes.

Checkpoint reads on activated logs pin the marker/source/HW view, avoiding false
corruption when initial materialization races a reader. Native reads add one
bounded marker lookup, not a snapshot or history scan. Physical trim shares the
checkpoint fence. A regression found that the old local source CAS could create
a reserved generation or install unrelated state over a pending control; both
now fail before writing. Local state still carries no independent quorum proof.

Binary backup excludes pending controls above its committed cut and preserves
committed activation, source state and exact identities together. Preflight
cross-checks the first manifest, generation and boundary. Backup framing and
manifest widths remain unchanged; old format validators reject format 4.
Matched runtimes and tools remain required. No new metadata table or Slot
message-body command was added.

Verified:

- The pre-implementation format/API gate failed as expected in
  `/tmp/mqtt-log-activation-red.log`; local-CAS bypass regressions failed before
  their fix in `/tmp/mqtt-activation-cas-red.log`.
- `GOWORK=off go test ./pkg/quorumlog ./pkg/db/... ./pkg/channel/...
  -count=1 -timeout=120s`: passed; `/tmp/mqtt-log-activation-regression-final.log`.
  Message storage completed in 30.095 seconds, metadata in 29.049, transfer in
  19.982, replication in 3.854 and Channel storage in 6.984. Two older assertions
  reserving version 4 as unsupported now reserve version 5; native version
  selection/hash coverage remains unchanged.
- `GOWORK=off go test -tags=integration ./pkg/channel/replication
  -run '^TestMQTTSourceActivationQuorumRestartRecoveryAndLearner$'
  -count=1 -timeout=30s -v`: passed in 2.242 seconds;
  `/tmp/mqtt-log-activation-integration.log`. Three real disk-backed voters plus
  one learner exchange actual encoded/decoded batches, restart all stores,
  recover a new leader, retain protected content despite logical retirement and
  reject success without quorum. The test emits a reproducible evidence line.
  It runs replication runtimes in-process; it is not product process acceptance.
- `GOWORK=off go test -race -tags=integration ./pkg/quorumlog
  ./pkg/db/message ./pkg/channel/replication
  -run '^TestMQTT(LogActivation|SourceActivation|Activation|Checkpoint)'
  -count=1 -timeout=90s`: passed in 1.383 / 4.168 / 2.726 seconds;
  `/tmp/mqtt-log-activation-race.log`. Existing macOS LC_DYSYMTAB warning only.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing
  line-count warnings; `/tmp/mqtt-log-activation-flow.log`. The initially oversized
  message FLOW was condensed to the allowed limit. `git diff --check` passed.

Next integration must admit this control through the product Channel reactor
and source owner, observe committed source state, and complete the recoverable
subscription projection before SUBACK. No product code selects this proposal flag
yet. Distributed shared-copy proof/advancement, migration/restore transfer,
future-person-source admission, reliable delivery, permission-incarnation fencing
and the other full MQTT requirements remain incomplete. The full goal stays
active and the product listener stays unavailable.

## Fenced source admission through the Channel facade

Source `2354eaab9`; the frozen context and pre-code failure inventory are in
[mqtt-source-channel-admission.md](../specs/mqtt-source-channel-admission.md).
`MQTTSourceActivator.EnsureMQTTSource` now admits first activation through the
existing reactor append queue and durable quorum owner. Native Append/AppendBatch
cannot select controls by payload. The request requires Channel/leader epochs,
route generation and an allocator-issued message identity with stable timestamp.
The Channel remains reserved against eviction through confirmation.

The reactor captures its own committed HW, then a typed task in the bounded
checkpoint pool persists that boundary and reads one pinned activation/source/HW
view. Pending controls beyond the captured boundary and unrelated local CAS
state cannot claim committed protection. Temporary leases close on success,
error and panic. Source queries share existing lookup cancellation and lifecycle
guards; completion checks generation, epochs, route, leader readiness, write
fencing, the admission guard and synchronous caller cancellation. Unsupported
stores and the legacy non-quorum mode reject the capability.

Already active protection returns without another control record. Concurrent
first requests may append redundant controls but retain the first generation and
protection start. The returned committed boundary is separately suitable for a
new subscription's initial cursor; its previously chosen cursor must survive
intent retries. This capability is local leader evidence, not fresh Slot routing,
permission authorization or a distributed subscription receipt.

Validation found and fixed two issues before delivery: the compatibility method
initially returned the wrong nil-handle error, caught by the existing complete-
surface lifecycle test; and a synchronous cancellation inside the admission guard
could reach a successful reactor completion. The latter received a failing
regression before the fix and now rechecks context after the guard returns.

Verified:

- The initial API gate failed before implementation as expected in
  `/tmp/mqtt-source-admission-red.log`; cancellation regression evidence is in
  `/tmp/mqtt-source-admission-cancel-red.log`.
- `GOWORK=off go test ./pkg/quorumlog ./pkg/db/message ./pkg/channel/...
  -count=1 -timeout=120s`: passed; `/tmp/mqtt-source-admission-regression-final.log`.
  Message storage took 19.847 seconds; all Channel packages passed. The earlier
  run's compatibility error is recorded in `/tmp/mqtt-source-admission-regression.log`.
- `GOWORK=off go test -race -tags=integration ./pkg/channel/service
  ./pkg/channel/reactor ./pkg/channel/worker ./pkg/db/message
  -run '^TestMQTTSource' -count=1 -timeout=90s -v`: passed in 2.070 / 1.420 /
  1.788 / 3.494 seconds; `/tmp/mqtt-source-admission-race-final.log`. Existing
  macOS LC_DYSYMTAB linker warnings only. The real disk-backed single-node cluster
  service test covers native payload isolation, ordered activation, repeated
  admission without append, restart/recovery and protected trim. Another test
  covers 16 concurrent first admissions. The test emits
  `mqtt_source_channel_evidence`; these in-process runtimes are not product
  process E2E acceptance or a new multi-node source-routing test.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine line-count
  warnings; `/tmp/mqtt-source-admission-flow.log`. Message FLOW remains at the
  150-line limit; Channel FLOW is 124 lines. `git diff --check` passed.

Next work is the authoritative source-owner route and recoverable subscription
projection, including its permission incarnation and first-DM future-source
handshake. Distributed shared-copy proofs, transfer/GC, reliable delivery,
Will execution, product lifecycle/config wiring and full process acceptance
remain required. The product MQTT listener stays unavailable and the full
implementation goal remains active.

## Source authority routing and fresh Slot metadata

Source `2a6fceea2`; frozen rules and the pre-code failure inventory are in
[mqtt-source-routing.md](../specs/mqtt-source-routing.md). Node now routes
`EnsureChannelMQTTSource` through the Channel service and dedicated RPC 93.
Origin and serving leader confirm runtime metadata through fresh Slot quorum/
apply barriers before and after activation. Explicit caller epochs and route
generation survive forwarding unchanged. The serving handler is local-only,
uses the recovered runtime and follows ServiceGateway replacement. Source RPC
has a closed bounded codec, exact request echo and zero source on every error.

The additional runtime-meta `get_fresh` operation requires codec 3, verifies
derived hash/physical Slot mapping and leadership, and cannot fall back to a
legacy or cached read. Both found and absent results require a fresh barrier.
Regression tests first reproduced acceptance of unrelated batch/cursor reply
fields and late oversized-frame rejection; implementation now rejects those
before accepting a point-read result. The restart fixture explicitly waits for
Slot write authority after startup readiness; transient election failures remain
errors rather than becoming cached success.

Verified for this slice:

- Initial API compilation failed as expected before implementation:
  `/tmp/mqtt-source-routing-red.log`. Additional failing shape regressions:
  `/tmp/mqtt-source-routing-shape-red.log`.
- `GOWORK=off go test -tags=integration ./pkg/slot/proxy ./pkg/cluster/channels
  ./pkg/cluster/net ./pkg/cluster -run '^TestMQTTSource' -count=1 -timeout=100s -v`:
  passed, `/tmp/mqtt-source-routing-test-final.log`.
- `GOWORK=off go test ./pkg/slot/proxy ./pkg/cluster/... -count=1 -timeout=120s`:
  passed, `/tmp/mqtt-source-routing-regression.log`.
- The same four focused packages with `-race -tags=integration`, the source
  filter and a 120-second timeout: passed, `/tmp/mqtt-source-routing-race.log`.
  The three real Node runtimes use TCP, disk, 256 hash Slots and two physical
  Slots. Evidence `mqtt_source_route_evidence` covers remote activation, ordinary
  append ordering, repeat confirmation, Channel leader recovery, restart and
  rejection of warmed authority after the other two nodes stop. This is not
  process-level product MQTT acceptance. macOS linker warnings were non-failing.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine pre-existing
  line-budget warnings; `/tmp/mqtt-source-routing-flow-final.log`.

Next is recoverable subscription projection over these routed primitives,
including permission incarnation, initialized cursors, first-DM future-source
handshake and source responsibility recovery. Shared-copy durability/transfer/GC,
reliable delivery, Will execution, app lifecycle/configuration and full process
acceptance remain required. The product MQTT listener stays unavailable and the
full implementation goal remains active.

## Recoverable group source and cursor preparation

Source `c4586d078`; the pre-code failure inventory and frozen context are in
[mqtt-source-preparation.md](../specs/mqtt-source-preparation.md).
`GroupSources.Prepare` connects routed Channel protection to authoritative source
bindings and Session cursor initialization. It derives the group and principal
from current intent/Owners, commits an unknown-boundary binding before choosing
the protected tail, saves that boundary once, initializes the cursor with an
exact owner/revision CAS and activates the binding using its committed revision.
It leaves the subscription unchanged. An infrastructure adapter resolves fresh
runtime metadata and supplies the app allocator's message ID to the Channel facade.

Real metadata fault tests cover lost replies after each of four commits, parent
renewal, owner/permission changes, source identity/generation changes, corruption,
panic, cancellation and clock failures. A regression first demonstrated another
binding commit after cancellation inside the clock callback; synchronous checks
now reject it before the next effect, without relying on context callback timing.

Verified:

- API gates failed before implementation in `/tmp/mqtt-source-preparation-red.log`
  and `/tmp/mqtt-source-preparation-integration-red.log`; the clock-cancellation
  regression failed before its fix in `/tmp/mqtt-source-preparation-cancel-red.log`.
- `GOWORK=off go test -race ./internal/usecase/mqttsession ./internal/infra/cluster
  -count=1 -timeout=120s`: passed (10.833 / 3.506 seconds),
  `/tmp/mqtt-source-preparation-regression-final.log`.
- `GOWORK=off go test -race -tags=integration ./internal/app
  -run '^TestMQTTGroupSourcePreparation' -count=1 -timeout=120s -v`: passed,
  `/tmp/mqtt-source-preparation-integration-final.log`. Three real Node runtimes
  use TCP, disk and 256 hash Slots. Evidence `mqtt_source_preparation_evidence`
  proves remote protection, a lost committed cursor reply, owner 1→3 recovery,
  unchanged original boundary and rejection after membership removal. Permission
  incarnation is explicitly controlled; subscription remains Preparing. This is
  not full projection, listener wiring or process-level MQTT acceptance. Test
  teardown now stops runtimes before removing their temporary directories.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine line-budget
  warnings; `/tmp/mqtt-source-preparation-flow.log`. The generated index is current.
  macOS LC_DYSYMTAB warnings were non-failing; `git diff --check` passed.

The next dependency is the complete projection contract: durable inbox discovery
and future-person-source handshake, permission-incarnation/delivery ordering,
shared-content recovery and removal/drain. The prepared-source result deliberately
does not implement `SubscriptionProjection` or produce its completion receipt.
Shared-copy transfer/GC, reliable delivery, Will execution, app/configuration,
unavailable-owner proof and full process/load acceptance remain required. Product
MQTT admission stays unavailable; the full implementation goal remains active.
