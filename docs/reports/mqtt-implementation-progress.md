# MQTT implementation progress

Full goal: implement the approved [MQTT IM access design](../specs/mqtt-im-access.md).
Status: in progress; the product has no MQTT listener yet. The codec, generic
gateway and session/subscription/cursor/inflight tables with Slot commands are implemented. Other tables,
cluster session recovery, source retention protection and durable delivery remain
outstanding. No passing product E2E or capacity claim is made.

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
