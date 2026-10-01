# MQTT implementation progress

Full goal: implement the approved [MQTT IM access design](../specs/mqtt-im-access.md).
Status: in progress. The default-off `cmd/wukongim` MQTT listener now composes
existing Session, group/inbox projection, future person-source admission, replay,
delivery and exact ACK capabilities through the shared Gateway and cluster ports.
Real-process Paho/WKProto authenticated interop passes on single-node and three-node
clusters with 256 hash Slots. TOML/env configuration and the complete app/config
unit suites pass with the race detector.

A bounded Will scanner and four-turn cohort now publish due detached work.
Real process Will Delay/abnormal closure/normal cancellation passes in both
topologies; that test exposed and fixed normal-intent loss during cancelled
packet dispatch. See [Will scheduling](../specs/mqtt-will-scheduling.md).

Bounded consumer maintenance now accounts offline/full-window debt, confirms
quota/revocation cleanup, projects contiguous ACK progress and removes proved
closed Channel obligations. Eight real-process cases pass across both topologies;
Session/Will regression suites and focused race/stop contracts also pass. See
[consumer maintenance evidence](mqtt-consumer-maintenance.json). This step reuses
existing recovery indexes and introduces no durable table or RPC format.
[Ended-UID retirement](../specs/mqtt-qualification-retirement.md) additionally
removes old inbox candidate/recovery entries after explicit ending or a newer
lifetime. Six process scenarios verify zero expiry, elapsed expiry and Clean
Start without damaging a successor or future person-source delivery.

A joined [owner sweeper](../specs/mqtt-owner-sweeping.md) now drives the existing
local deadline heap for unregistered reservations and cleanup retries. It retains
uncertain effects and exposes fixed aggregate state; no durable Session policy
or ownership proof changes. App integration reproduces the previous leak and
verifies automatic cleanup; callback barriers verify joined cancellation.

The [offline drain metadata contract](../specs/mqtt-offline-drain.md) now permits
closed-intent cleanup under exact Offline Session fences while preserving
unconfirmed exchanges. [Closed-source maintenance](../specs/mqtt-closed-source-maintenance.md)
now drives these stages from existing binding recovery indexes without local
Owner admission. [Pending removal recovery](../specs/mqtt-pending-removal-recovery.md)
now completes UID checkpoints and final Removing subscriptions, including discovery
before the first binding. Both index streams share the fixed consumer cohort.
Three-node product-worker composition passes controlled interrupted inbox/group
removal. [Product-process fault acceptance](mqtt-unsubscribe-fault-acceptance.json)
now passes all eight inbox/group × committed-intent/final-completion failure ×
single-node/three-node scenarios with 256 Slots (224.552s). Real Paho/WK clients
prove background completion before reconnect, original PacketID/DUP/identity and
fresh resubscription; the confirmation metric is fixed and aggregate. Temporary-copy
gofail builds contain the injection controls; ordinary builds have no dependency.

That process suite exposed three subscription windows, all reproduced before
repair: follower copy before native committed HW, anchor recovery before its
local checkpoint, and a definite Removing CAS rejection after an unrelated parent
revision update. Receivers now wait on their own checkpoints; bounded confirmation
keeps the all-replica requirement. Removing intent rebases at most twice only after
fresh same-Owner/identical-child evidence. Unknown replies, corruption and changed
intent remain failures. Focused race, bounded-request integration and FLOW checks
pass; the [failure inventory](../specs/mqtt-unsubscribe-fault-acceptance.md) and JSON
report preserve source digests, RED evidence, commands and eight process artifacts.
These are controlled request failures and connection closure; abrupt process-crash
and network-partition acceptance still remain required.

[Offline preparation](../specs/mqtt-pending-establishment-recovery.md) now reuses
protected group boundaries and bounded inbox discovery without local Owner
admission. It requires the captured unexpired Offline Session and complete
Preparing intent, and preserves all-replica replay confirmation. Inbox nested
source work now pins the same Owner/child instead of following a takeover.
Focused/full usecase race, deadline integration and existing single-node cluster
composition pass; [evidence](mqtt-offline-establishment-preparation.json) records
the saved baseline and an unfenced nested-source negative control.
These are projection ports only: pending establishment dispatch, final activation,
revocation orchestration and process fault acceptance still remain to be wired.

Later work now reconstructs fresh MQTT generations after restore, with two
restores per single-node and three-node cluster and no resubscription. First
group subscription admission and confirmed restore-response semantics have
their own bounded acceptance. See [restore evidence](mqtt-restore-reactivation/README.md),
[first subscription](mqtt-first-subscribe-admission/README.md) and
[restore admission](restore-admission-response/README.md). The original full
single-node group workload also has a passing [scale result](../specs/mqtt-scale-acceptance.md).

These milestones do not complete MQTT acceptance. Safe uncertain dispatch
recovery, partition/asymmetric-link isolation and the remaining failure/workload
matrix retain their explicit limits. The three-node process-outage scenario
separately checks refused takeover against surviving quorum/independent admission
controls and resumes the original exchange after reachable exact-owner proof;
it does not qualify network partitions. See [outage evidence](mqtt-owner-outage/README.md).
The current ordinary product also repeats the unchanged 100,000-member,
500-connection workload with zero delivery anomalies and all 600 retirements.
The bounded pre-merge review records two open P1 target-contract gaps:
safe completion of Started Wills without a receipt, and aggregate node/cluster
shared-storage admission. Its two Standards findings are repaired and validated;
the review is not a full branch line audit. See the same report for scope,
receipts and limits. Complete MQTT delivery remains unqualified.
[Started Will non-dispatch recovery](mqtt-will-started-recovery/README.md) now
adds an exact bounded Reserved/Admitted/Sealed journal before dispatch, owning-node
boot proof and successor CAS. Positive receipts retain original identity. This
narrows the Started gap only for provably never-admitted work: Admitted-before-SEND,
legacy/lost-journal terminal recovery, journal orphan reclamation and aggregate
shared-storage admission remain outstanding. The final six-case process matrix
passes in 317.404 seconds, with one publication per case and no unexpected
delivery, CONNECT retry or resubscription. These bounded cases do not complete MQTT.

Historical sections below record the narrower evidence available at each step.

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

## Bounded shared replay transfer with an independent content anchor

Source `c71e102be`; the pre-code failure inventory and frozen context are in
[mqtt-replay-transfer.md](../specs/mqtt-replay-transfer.md). `ChannelLog` now
exports pinned pages and atomically imports up to 256 records / 16 MiB. Import
requires an independently accepted full-content prefix plus the receiving
replica's already installed committed activation, source, checkpoint and exact
entry/proposal evidence. It validates canonical envelopes, counters, chain links,
paired manifests and committed proposal tails using bounded point reads.

Inspection found that native log digests do not bind all replay envelope fields:
RedDot and stream fields are examples, and format 1 predates lifetime binding.
The API therefore explicitly requires a separate expected prefix from a verified
current-authority copy/recovery decision. Taking it from the received page would
violate the contract. Tests independently change an unbound native field and a
log-bound payload, reseal the page, and verify rejection by the appropriate check.
This step does not implement the producer of that distributed decision.

Rows, range meters and local coverage share one synchronous commit. Gaps,
extending overlaps, orphan rows/meters and inconsistent retries fail; a failed
last record leaves no partial writes. Fully covered historical retries return
their accepted endpoint even after later progress. A receiver can refill missing
shared content after original-body trim using retained committed identities and
the independently accepted prefix. Source copied-through may temporarily exceed
local recovery coverage; neither partial import nor its return value grants
readiness. The test explicitly controls the external source-release decision.
Import does not change source release, checkpoints or ordinary message rows.

Verified:

- API tests failed before implementation, `/tmp/mqtt-replay-transfer-red.log`.
- Focused `TestMQTTReplayTransfer` tests passed (3.803 seconds),
  `/tmp/mqtt-replay-transfer-focused.log`. Coverage includes bounded pagination,
  owned bytes, original-body trim, disk reopen, exact historical retries,
  generation/key/content corruption, missing local evidence, malformed counters,
  limits, cancellation, overlapping extensions and atomic failure.
- `GOWORK=off go test -race ./pkg/db/message -count=1 -timeout=180s`: passed
  (21.956 seconds), `/tmp/mqtt-replay-transfer-race.log`. The macOS linker emitted
  a non-failing LC_DYSYMTAB warning.
- `GOWORK=off go test ./pkg/db/... ./pkg/quorumlog -count=1 -timeout=180s`:
  all packages passed, `/tmp/mqtt-replay-transfer-regression.log`.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing
  line-budget warnings, `/tmp/mqtt-replay-transfer-flow.log`. Rendering the index
  produced no index diff. `git diff --check` passed.

No durable schema or protocol format changed. These are storage-level tests,
not process-level MQTT acceptance. Current-authority copy receipts, transfer RPC
and learner/migration readiness, source-release replication and consumer-proof
GC remain required. The broader implementation still needs complete subscription
projection, inbox discovery/future-source admission, permission-incarnation
ordering, reliable delivery/ACK/recovery, fenced Will execution, unavailable-owner
proof, app/configuration and full process/load acceptance. Product MQTT admission
stays unavailable; the implementation goal remains active.

## Channel admission for bounded shared replay preparation

Source `330690f90`; frozen context and pre-code failures are recorded in
[mqtt-replay-admission.md](../specs/mqtt-replay-admission.md). The public optional
`MQTTReplayPreparer` facade now reaches the owning reactor, checkpoint workers
and MessageDB adapter. Requests preserve exact Channel/leader epochs and route
generation; the reactor captures HW, rejects future ranges and rechecks current
leader readiness, write fences, admission guard and caller context at completion.
Replay waiters share existing cancellation/eviction ownership but cannot be
consumed by message/source completions or foreign operation IDs.

The worker persists captured HW, confirms committed source identity, prepares one
bounded page and closes its temporary lease on success, error or panic. The
adapter transfers owned opaque original-row bytes and all counters/hashes without
an extra payload clone. Preparation first returns already copied pages before
extending into uncopied history, so retrying a lost short-page response cannot
produce an overlapping extension. No source-release or log-format change occurs.

Verified:

- Unit/API tests were written first and initially failed to compile due to the
  missing facade, tasks and store method: `/tmp/mqtt-replay-admission-red.log`.
  Focused tests then passed, `/tmp/mqtt-replay-admission-focused.log`.
- `GOWORK=off go test -race ./pkg/channel/... ./pkg/db/message -count=1
  -timeout=180s`: every package passed; `/tmp/mqtt-replay-admission-race.log`.
- `GOWORK=off go test -race -tags=integration ./pkg/channel/service
  -run '^TestMQTT(Source|Replay)Service' -count=1 -timeout=120s -v`: passed
  (2.682 seconds), `/tmp/mqtt-replay-admission-integration-race.log`.
  The replay evidence uses a real disk-backed single-node cluster quorum runtime,
  checks concurrent short-page retry, source/leader ordering, stale epoch/route
  rejection, restart recovery, independent returned buffers and retained original
  protection. The existing source-service integration tests also pass. This is
  not multi-node transfer or product MQTT acceptance.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing
  line-budget warnings; `/tmp/mqtt-replay-admission-flow.log`. FLOW index regenerated;
  `git diff --check` passed. macOS LC_DYSYMTAB linker warnings were non-failing.

Next, fresh cluster authority and a bounded routed RPC must expose this facade,
then coordinate independently accepted copy decisions and receiver imports under
current replica membership. Quorum-copy/source-release replication, learner
readiness and consumer-proof GC remain required. Complete subscription projection,
inbox discovery, permission ordering, reliable delivery/ACK/recovery, Will
execution, unavailable-owner proof, app/configuration and process/load acceptance
are still outstanding; the full goal remains active and MQTT product admission
remains unavailable.

## Fresh cluster routing for shared replay preparation

Source `5dea2b8f1`; the pre-code failure inventory and frozen context are in
[mqtt-replay-routing.md](../specs/mqtt-replay-routing.md). Node foreground gates
now expose replay preparation through the hosted Channel service. Fresh Slot
quorum/apply reads bracket local runtime admission or one forwarded call, under a
five-second limit. Exact caller epochs/route, leader, replica/ISR sets, MinISR,
status and write fence must remain valid. Zero/duplicate replica identities are
rejected by a shared source/replay authority validator before runtime effects.
Unrelated lease and logical-retention updates do not invalidate immutable content.

Dedicated foreground mutation RPC 94 uses closed `WMRQ`/`WMRR` version-1 codecs,
4 KiB requests and replies capped at 16 MiB plus 64 KiB framing. Replies echo the
entire request, preserve every prefix/record field and own decoded content.
Length/count/content budgets are checked before allocation; malformed/trailing
frames, changed echoes, unknown versions/statuses and error replies carrying
pages fail. Unknown error text remains generic. The receiver serves only the
named local leader and never forwards again; gateway clear/replacement keeps the
registered transport handler stable. No table or stored format changed.

Verified:

- New routing/codec/Node/transport-ID tests failed before implementation in
  `/tmp/mqtt-replay-routing-red.log`, then focused source/replay tests passed in
  `/tmp/mqtt-replay-routing-focused.log`.
- `GOWORK=off go test -race ./pkg/cluster/... -count=1 -timeout=180s`: all
  packages passed, `/tmp/mqtt-replay-routing-race.log`.
- `GOWORK=off go test -race -tags=integration ./pkg/cluster
  -run '^TestMQTTReplayRoutingThreeNode' -count=1 -timeout=120s -v`: passed
  (15.565 seconds including package overhead),
  `/tmp/mqtt-replay-routing-integration.log`. Evidence
  `mqtt_replay_route_evidence` uses three real Node runtimes, TCP, disk, 256 hash
  Slots and two physical Slots. It covers remote short-page retry, larger-than-
  request content frames, complete byte/digest equality after Channel leader
  recovery, node restart, and rejection after a warmed node loses its majority.
  The new leader derives copies from protected originals; this does not prove
  follower shared-copy import or post-GC distributed recovery.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing
  line-budget warnings, `/tmp/mqtt-replay-routing-flow.log`. Rendering the index
  produced no index diff. `git diff --check` passed. Non-failing macOS
  LC_DYSYMTAB linker warnings remain present.

The next dependency is current-membership copy coordination and receiver import
with independently accepted content decisions, followed by replicated release
frontiers, learner/migration readiness and consumer-proof GC. Complete source
projection/inbox discovery, permission ordering, delivery/ACK/recovery, fenced
Will execution, unavailable-owner proof, app/configuration and full process/load
acceptance remain required. The MQTT product listener is unavailable and the
full implementation goal remains active.


## Current-quorum confirmation of shared replay copies

Source `736e46d7d`; pre-code failure inventory and frozen context are recorded in
[mqtt-replay-copy-quorum.md](../specs/mqtt-replay-copy-quorum.md). The Node copy
facade prepares one bounded leader page and obtains independent current-ISR
confirmations. Receipt success requires the current leader and MinISR distinct
voters, with a strict majority topology. Fresh Slot reads bracket coordination
and receiver storage work; exact membership, epochs/route, status and leader are
bound by a domain-separated digest. No source-release frontier changes.

Body-free foreground RPC 95 uses closed WMCQ/WMCR version-1 codecs, 4 KiB caps,
exact complete request echoes and voter identity checks. Each receiver reads its
existing committed source checkpoint, independently prepares/verifies the full
content prefix, resumes partial local coverage within the original 256-row and
16-MiB budgets, and closes its lease on every exit. Four coordinator slots, four
separate receiver slots and at most four joined workers per coordinator bound
memory and work; successful quorum cancels outstanding calls. Gateway swaps and
Node maintenance remain fenced. No durable table or stored format changed.

The real three-node test exposed a necessary native replication dependency: after
the final append, followers had LEO 4 but checkpoint HW 3, while the leader had
HW 4. Receivers correctly rejected coverage through 4. A regression test preceded
the fix: the leader can now request replay of its installed sequencer's committed
tail through the existing bounded repair owner. Exact full Authority is checked;
there is no caller-supplied HW. This on-demand hint propagates commit progress
without adding another broadcast to every ordinary append. Scheduling is not a
copy receipt; follower confirmation still checks durable storage afterward.

Verified:

- Initial tests failed before implementation in `/tmp/mqtt-copy-red.log` and
  `/tmp/mqtt-copy-integration-red.log`. Native refresh regression first failed
  in `/tmp/mqtt-copy-refresh-red.log`; focused suites then passed in
  `/tmp/mqtt-copy-focused.log` and `/tmp/mqtt-copy-refresh-focused.log`.
- `GOWORK=off go test -race ./pkg/cluster/... ./pkg/channel/... ./pkg/goroutine
  -count=1 -timeout=180s`: all packages passed, `/tmp/mqtt-copy-race.log`.
- `GOWORK=off go test -race -tags=integration ./pkg/cluster
  -run '^TestMQTTCopyThreeNode' -count=1 -timeout=120s -v`: passed
  (14.520 seconds including package overhead), `/tmp/mqtt-copy-integration.log`.
  `mqtt_copy_quorum_evidence` verifies three real Node runtimes, TCP, disk,
  256 hash Slots, two physical Slots, all-voter full-content equality, follower
  restart, leader change, preserved originals and isolated-node refusal.
  This is runtime integration, not product-process MQTT acceptance or post-GC import.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing
  line-budget warnings, `/tmp/mqtt-copy-flow.log`. FLOW index regenerated.
  Non-failing macOS LC_DYSYMTAB linker warnings remain present.

Next, accepted copy/release decisions must be log-replicated and serve as durable
full-content anchors for post-GC receiver import, learner/migration readiness and
consumer-proof GC. Complete source projection/inbox discovery, permission
ordering, delivery/ACK/recovery, fenced Will execution, unavailable-owner proof,
app/configuration and full process/load acceptance remain required. The MQTT
product listener stays unavailable and the full implementation goal stays active.


## Replicated content-anchor format and durable journal

Source `776a843a0`; failure inventory and frozen context are in
[mqtt-replay-anchor.md](../specs/mqtt-replay-anchor.md). Explicit proposal format
5 carries one canonical internal anchor: source activation command, immutable
start, copied-through position, cumulative byte counts and complete-content
digest. The control cannot cover itself. Its independent hash domain and closed
payload preserve native formats 1–3 and source format 4. Business payload bytes
cannot select control semantics, and exact retry preserves the explicit intent.

Message System 14 atomically retains a bounded canonical control-row envelope by
control position, independently of ordinary history. Committed point reads pin a
snapshot and verify source activation, checkpoint and exact proposal/entry proofs.
Pending anchors remain invisible. Original-body prefix trim preserves journals;
uncommitted suffix replacement deletes them. Portable backups omit pending
anchors and require matching committed journals. Unsupported store factories
reject both append and recovery of format 5. The adapter exposes a typed proof
without importing MessageDB contracts outside the existing adapter boundary.

Native quorum append, wire codec, follower/learner repair and authority recovery
now preserve format-5 controls. This is the storage/replication primitive: current
copy-receipt admission and its ordered, idempotent reactor facade are still
unwired. Anchors do not advance source release or establish migration readiness.
They require matching runtimes/tools; binary-only downgrade after new records is
unsupported. Existing binary backup framing is unchanged.

Two additional regressions were reproduced before their fixes. Backup preflight
accepted a manifest labeled format 5 paired with a resealed format-1 entry; it
now requires proposal/entry version equality for every format. Atomic recovery
that replaced an uncommitted activation and installed a new anchor consulted the
old source marker; staging now resolves the replacement batch's incarnation.

Verified:

- Initial format/storage tests failed before implementation in
  `/tmp/mqtt-anchor-red.log`; native admission/reader tests failed first in
  `/tmp/mqtt-anchor-native-red.log`. Regression RED logs are
  `/tmp/mqtt-anchor-backup-red.log` and `/tmp/mqtt-anchor-recovery-red.log`.
- `GOWORK=off go test -race ./pkg/db/... ./pkg/channel/... ./pkg/quorumlog
  ./pkg/cluster/... -count=1 -timeout=180s`: all packages passed,
  `/tmp/mqtt-anchor-race.log`. After the final recovery fix, the affected full
  MessageDB, Channel store/replication and quorumlog suites passed again in
  `/tmp/mqtt-anchor-final-race.log` (MessageDB 25.554 seconds).
- Focused race verification in `/tmp/mqtt-anchor-storage-final.log` confirms
  actual removal of the original prefix and control row, durable proof reads,
  restart, proof-anchored import, backup restoration and corruption rejection.
  Source release in this storage fixture is an explicit test-controlled decision,
  not a production release coordinator.
- `GOWORK=off go test -race -tags=integration ./pkg/channel/replication
  -run '^TestMQTTReplayAnchorQuorum' -count=1 -timeout=90s -v`: passed
  (3.254 seconds including package overhead),
  `/tmp/mqtt-anchor-integration-final.log`. `mqtt_anchor_evidence` verifies three
  voters plus one learner, real disk and the wire codec, committed journal reads,
  restart, authority recovery and no-quorum refusal. This is in-process runtime
  integration, not a product MQTT process test or a current-copy receipt proof.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-anchor-flow.log`. FLOW index regenerated. Non-failing macOS
  LC_DYSYMTAB linker warnings remain present. `git diff --check` passed.

Next, connect fresh current-copy receipts to ordered/idempotent anchor admission,
then source release, accepted-anchor donor import, migration readiness and shared
consumer-proof GC. Complete source projection/inbox discovery, permission
ordering, delivery/ACK/recovery, Will execution, unavailable-owner proof,
app/configuration and process/load acceptance remain required. The MQTT listener
stays unavailable and the full goal stays active.

## Ordered current-copy admission in the durable sequencer

Source `f97570cac`; pre-code failure inventory and frozen context are in
[mqtt-replay-anchor-admission.md](../specs/mqtt-replay-anchor-admission.md).
`MQTTReplayAnchorCommitter` now shares the existing durable Channel sequencer
mutex with ordinary writes. It independently checks the installed leader,
epochs, route, voters, learners and quorum against the supplied current metadata,
and validates sorted distinct current-voter receipts including the leader. The
copy authority hash moved to the neutral Channel contract without changing its
version-1 bytes. This is a trusted internal coordinator contract, not signed or
Byzantine-tolerant evidence, and its caller still must obtain fresh Slot metadata.

Only sequencer-owned recovered HW is checkpointed. A coherent store read obtains
source identity, the latest committed journal entry and the exact command proof
using one reverse seek and bounded point reads. New intervals must start exactly
at the last accepted prefix. Source/Through command identities survive changed
control MessageIDs, restart, leader changes, later anchors and original-body
cleanup; conflicting content cannot obtain a different command for that boundary.
An uncertain pending proposal retains its original immutable row. An uncovered
suffix containing only the preceding anchor returns the existing proof, avoiding
an idle sequence of self-generated controls. No table, index or wire format was
added, and source release remains unchanged.

Review reproduced a proposal-budget regression before its fix: the new helper
initially counted payload bytes without the ordinary 96-byte record overhead.
The integration regression failed with expected backpressure but successful
append, then passed after admission reused complete proposal validation.

Verified:

- Pre-implementation RED evidence: `/tmp/mqtt-anchor-admission-red.log` and
  `/tmp/mqtt-anchor-admission-runtime-red.log`. Budget regression RED:
  `/tmp/mqtt-anchor-admission-budget-red.log`.
- `GOWORK=off go test -race ./pkg/channel/... ./pkg/db/message
  ./pkg/cluster/channels -count=1 -timeout=180s`: all passed,
  `/tmp/mqtt-anchor-admission-race.log` (MessageDB 28.469 seconds).
- `GOWORK=off go test -race -tags=integration ./pkg/channel/replication
  ./pkg/channel/service -run '^Test(MQTT|CommittedReplica)' -count=1
  -timeout=90s -v`: all passed, `/tmp/mqtt-anchor-admission-integration.log`.
  Replication package 4.898 seconds, service package 2.489 seconds. The new
  evidence record verifies three disk-backed voters, actual exchange encoding,
  concurrent/exact retry, accepted-prefix chaining, anchor-only idle behavior,
  restart, leader change, no-quorum refusal and resumption of an uncertain append.
  Final focused race verification also reads the durable original proposal and
  confirms that uncertain retry kept its first MessageID (2.440 seconds,
  `/tmp/mqtt-anchor-admission-final.log`).
  Copy receipts in this fixture are assembled after independently copying each
  actual replica; this is not the Node/RPC coordinator or a product process E2E.
- Storage tests verify pending exclusion, caller-HW rejection, older snapshot
  boundaries, exact command lookup after original trim and missing-journal failure.
  Receipt validation covers changed authority, invalid placement, learner votes,
  duplicates, missing leader/quorum and malformed ranges/counters.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-anchor-admission-flow.log`. FLOW index regenerated; `git diff --check`
  passed. Non-failing macOS LC_DYSYMTAB linker warnings remain present.

Next connect this primitive to bounded reactor workers and fresh cluster routing,
including coherent accepted-prefix planning and completion that preserves reactor
HW/lifecycle ownership. Then implement replicated release, accepted-anchor donor
repair, learner/migration readiness and consumer-proof GC. Source projection,
inbox discovery, permission ordering, delivery/ACK/recovery, fenced Will execution,
unavailable-owner proof, app/configuration and full process/load acceptance remain
required. The product MQTT listener remains unavailable; the full goal is active.

## Reactor-owned replay anchor admission and cancellation

Source `38e19e035`; the pre-code failure inventory and frozen governing context
are in [mqtt-replay-anchor-reactor.md](../specs/mqtt-replay-anchor-reactor.md).
The neutral `MQTTReplayAnchorRequest`/committer contract is now implemented by the
Channel service as well as the durable sequencer. The service validates before
allocating retained metadata copies, reserves the ordinary append path, and
submits an explicitly typed control on the existing append mailbox/queue.
Membership and acknowledgement slice bytes count toward the queue budget.

Admission and flush check capable storage, the recovered leader, exact installed
placement/epochs/route/status, write admission and committed source range. Typed
`TaskQuorumMQTTAnchor` uses the bounded append pool and is not worker-batched.
Once started, the effect is independent of the requesting observer's cancellation.
Ordinary business appends, source controls and anchor requests retain one ordering
and the existing lifecycle/eviction guards.

A pure control-completion transition advances LEO/HW monotonically and retires
waiters without assigning the new request's message ID or payload to a previously
committed control. Reactor completion returns only the proof and deliberately
omits recent-record cache insertion. Current committed progress is published even
when the observer has canceled or its post-commit guard denies a reply. Foreign
operation/generation results are ignored; changed authority and malformed/source-
mismatched proofs cannot publish progress. Existing append cancellation cleanup
is shared without changing its semantics. No durable schema or wire format changed.

Verified:

- Tests were written before implementation. RED logs:
  `/tmp/mqtt-anchor-reactor-red.log`, `/tmp/mqtt-anchor-reactor-fence-red.log`
  and `/tmp/mqtt-anchor-service-red.log`.
- Focused state-machine/worker/reactor tests passed in
  `/tmp/mqtt-anchor-reactor-focused.log`. Coverage includes old/new controls,
  cancellation, stale/foreign completions, guard failure/cancellation, malformed
  proofs, an enabled recent cache, queue byte bounds and owned member slices.
- `GOWORK=off go test -race ./pkg/channel/... ./pkg/cluster/channels -count=1
  -timeout=180s`: all packages passed, `/tmp/mqtt-anchor-reactor-race.log`.
  Reactor 5.301 seconds, worker 4.721 seconds, cluster/channels 8.564 seconds.
- `GOWORK=off go test -race -tags=integration ./pkg/channel/service
  ./pkg/channel/replication -run '^TestMQTT' -count=1 -timeout=90s -v`: all
  passed, `/tmp/mqtt-anchor-reactor-integration.log`. Service 2.636 seconds,
  replication 3.903 seconds. This also retained the preceding three-voter/learner
  wire/disk/recovery coverage.
- Final service verification after the validation-before-copy review passed in
  `/tmp/mqtt-anchor-reactor-final.log` (1.926 seconds). The single-node cluster
  uses real disk and the native quorum owner: concurrent and idle retries reuse
  the original proof, original row IDs stay unchanged, later business sequences
  stay ordered, the reactor's committed frontier is consistent, and restart
  preserves proofs. A blocked real worker allows the caller to cancel and mutate
  its request; releasing it still persists the exact owned control and permits
  the next business append at the correct position. These are service/runtime
  integrations, not a product MQTT process E2E or fresh Slot RPC admission.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-anchor-reactor-flow.log`. FLOW index regenerated. `git diff --check`
  passed; macOS LC_DYSYMTAB linker warnings remain non-failing.

Next expose coherent accepted-prefix planning and route anchor admission through
fresh Slot authority, the stable cluster gateway and a bounded versioned Node RPC.
Then connect replicated source release, accepted-anchor donor repair, learner and
migration readiness, and consumer-proof shared GC. Complete source/inbox projection,
permission ordering, delivery/ACK/recovery, Will execution, unavailable-owner proof,
app/configuration and full process/load acceptance remain required. The product
MQTT listener remains unavailable and the full implementation goal remains active.

## Fresh Slot admission and Node RPC for replay anchors

Source `0e039062a3a207aaa8b0d31ace929bfaa524c48c`; pre-code failure inventory and
frozen context are in [mqtt-replay-anchor-routing.md](../specs/mqtt-replay-anchor-routing.md).
The cluster service and `Node.CommitChannelMQTTReplayAnchor` now route the existing
neutral committer through fresh Slot reads before and after execution. The complete
copy authority binds ordered placement, status, leader, epochs, route and strict
quorum. Only freshly resolved metadata reaches the reactor; caller lease and
retention values cannot override it. A post-commit fence failure withholds the
proof without rolling back the control.

Internal RPC 96 uses the closed `WMAQ/WMAR` version-1 codec, capped at 8 KiB and
256 acknowledgements. It carries copy evidence and control identity without
message bodies or caller-authoritative membership. Replies echo the full request,
validate prefix/proof association and preserve historical creating authority in
Channel epoch/term/route order. Forwarding binds the receipt's leader exactly,
uses the stable gateway and cannot recurse. Existing foreground transport and
reactor queues bound work; calls have a five-second deadline. No new durable
schema, workers or per-Channel goroutines were introduced.

The first real three-node race test exposed an existing Controller startup
publication race: live peer Step calls read `Runtime.raft` while startup assigned
it. Publication, failed-start reset, promotion cleanup and inbound capture now
use the existing runtime mutex; Raft execution and stop never run under that lock.
The test also now accounts for the native current-term barrier after leader change.
Concurrent metadata installation can legitimately return temporary `not ready`
from its try-lock; the integration retries only explicit readiness/backpressure
errors within fixed attempt/time bounds, never proof conflicts.

Verified:

- Tests preceded implementation. Missing-entry/RPC RED:
  `/tmp/mqtt-anchor-routing-red.log`; historical-authority regression RED:
  `/tmp/mqtt-anchor-routing-history-red.log`. The first Node race trace is retained
  in `/tmp/mqtt-anchor-routing-integration.log`; the intermediate run with one
  temporary-admission failure is `/tmp/mqtt-anchor-routing-integration-fixed.log`.
- `GOWORK=off go test -race ./pkg/controller/... ./pkg/cluster/channels
  ./pkg/cluster/net ./pkg/cluster -count=1 -timeout=180s`: all passed,
  `/tmp/mqtt-anchor-routing-regression.log`. Controller root 2.241 seconds,
  cluster/channels 6.971, cluster/net 1.221, cluster root 8.664. Route/codec tests
  cover fresh pre/post authority, cancellation, absent capabilities, changed
  placement/quorum/status/fence, gateway replacement, wrong serving nodes, malformed
  framing/versions/status/proofs, exact echo, owned bytes and the maximum ack set.
- `GOWORK=off go test -race -tags=integration ./pkg/cluster
  -run '^TestMQTTAnchorThreeNode' -count=2 -timeout=120s -v`: both passed,
  13.26 and 12.61 seconds, package total 27.600 seconds;
  `/tmp/mqtt-anchor-routing-integration-final.log`. Real three-node TCP/disk,
  256 hash slots and two physical Slots exercised real copy receipts, remote
  reactor commit, concurrent/exact/idle retries, chained anchors, ordered business
  messages, serving-node restart, leader change and rejection without Slot quorum.
  The recovered historical proof equals its original despite a later anchor.
  This is Node/runtime integration, not full MQTT product process E2E.
- `GOWORK=off go test -race -tags=integration ./pkg/controller
  -run '^TestRuntime(SingleVoter|Mirror|VoterWires|PromoteControllerVoter|PrepareControllerVoter)'
  -count=1 -timeout=90s`: passed, 4.684 seconds;
  `/tmp/mqtt-anchor-routing-controller.log`.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-anchor-routing-flow.log`. Index regenerated and `git diff --check`
  passed. Existing macOS LC_DYSYMTAB linker warnings remain non-failing.

Next expose coherent accepted-prefix planning through reactor-captured committed
state, then connect replicated source release, accepted-anchor donor repair,
learner/migration readiness and consumer-proof shared GC. Full source/inbox
projection, permission ordering, delivery/ACK/recovery, Will execution, unavailable-
owner proof, app/configuration, migration tooling and full process/load acceptance
remain required. The product MQTT listener remains unavailable and the goal active.

## Coherent accepted-prefix planning through Node and RPC

Source `7ac762878`; the failure inventory and frozen context are in
[mqtt-replay-planning.md](../specs/mqtt-replay-planning.md). `PlanMQTTReplay`
accepts exact Channel/leader/route fences plus source generation. The reactor
captures committed HW, retains an ordinary lookup waiter and submits a typed
checkpoint-pool task. That task checkpoints only the captured frontier and reads
source/latest-anchor evidence from one pinned snapshot. A zero optional command
now skips the exact retry lookup; existing nonzero-command admission semantics
and persisted formats are unchanged.

The returned plan has explicit anchor presence and no local-copy watermark.
`NextRange` derives a bounded page from the accepted prefix or activation start.
It reports no work for a lone latest control; subsequent content includes that
control in the next interval. Cancellation, unsupported capabilities, foreign
worker kinds/operations, replaced generations, stale route/epochs, write admission
and mismatched result HW cannot publish a plan. Temporary leases close on errors
and panic. No new worker pool or per-Channel goroutine was added.

`Node.PlanChannelMQTTReplay` preserves foreground admission. The cluster service
and stable gateway surround the reactor read with fresh Slot checks, comparing
full ordered membership/status/quorum as well as exact fences. Body-free RPC 97
uses closed `WMPQ/WMPR` version 1, a 4 KiB cap, complete request echo, explicit
optional proof and the existing MQTT error catalog. Anchor proof serialization is
shared with RPC 96 without changing its bytes. Both local and forwarded operations
have a five-second deadline; no caller supplies HW or a guessed accepted prefix.

Verified:

- Tests preceded implementation. Storage/Channel RED is
  `/tmp/mqtt-plan-red.log`; cluster/RPC RED is `/tmp/mqtt-plan-route-red.log`.
  Focused green runs are `/tmp/mqtt-plan-focused.log` and
  `/tmp/mqtt-plan-route-focused.log`.
- `GOWORK=off go test -race ./pkg/channel/... ./pkg/db/message
  ./pkg/cluster/channels ./pkg/cluster/net ./pkg/cluster -count=1 -timeout=180s`:
  all passed in `/tmp/mqtt-plan-regression.log`. MessageDB 39.410 seconds,
  Channel reactor 3.641, Channel store 7.845, cluster/channels 7.649 and cluster
  root 17.503. Tests cover pending-journal exclusion, captured older views,
  preserved exact-command reads, absence/malformed proofs, bounded ranges and
  maximum sequence arithmetic, source mismatch, cancellation, lifecycle/worker
  ownership, gateway replacement and closed RPC framing/echo/status handling.
- `GOWORK=off go test -race -tags=integration ./pkg/cluster
  ./pkg/channel/service ./pkg/channel/replication -run '^TestMQTT(Anchor|Plan)'
  -count=1 -timeout=120s -v`: all passed in `/tmp/mqtt-plan-integration.log`.
  Cluster 16.297 seconds, service 2.046, replication 2.366. Existing real-disk
  service and three-node TCP tests now consume planned ranges before copying and
  committing anchors. They verify that local copy-ahead is not accepted progress,
  an idle anchor has no work, subsequent appends resume at the right position,
  restart/leader change retain the latest anchor, an old exact retry cannot regress
  it, and loss of Slot quorum rejects planning. Three nodes use 256 hash slots
  and two physical Slots. This remains Node/runtime integration, not full product
  MQTT process acceptance.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-plan-flow.log`. The index was regenerated and `git diff --check`
  passed. Existing macOS LC_DYSYMTAB linker warnings remain non-failing.

Next connect accepted anchors to replicated source-release decisions, then
accepted-anchor donor repair after original-body reclamation, learner/migration
readiness and consumer-proof shared GC. Full source/inbox projection, permission
ordering, delivery/ACK/recovery, durable Will execution, unavailable-owner proof,
app/configuration, state-transfer tooling and full process/load acceptance remain
required. The product MQTT listener remains unavailable and the full goal active.

## Shared-content repair bound to each replica's committed anchor

Source `8e425b3c2`; frozen context and the pre-implementation failure inventory
are in [mqtt-replay-anchor-repair.md](../specs/mqtt-replay-anchor-repair.md).
Source-release review identified a prerequisite: a replica missing shared content
must authenticate donor data after originals disappear. The storage repair path
now derives its expected full prefix from that replica's own committed System 14
journal, within the same append/checkpoint ownership interval as atomic import.
No caller-supplied expected digest is accepted by this port. Existing standalone
transfer semantics and persisted formats are unchanged.

Pinned exports read proof and content together and must reach the exact requested
anchor within explicit bounds (256 rows / 16 MiB maximum). A shortened page is
rejected because that anchor cannot authenticate its intermediate endpoint.
Imports retain complete retries, preserve an existing prefix, reject gaps/partial
overlaps and verify every committed entry as well as the complete content hash.
Channel's optional `MQTTReplayAnchorTransfer` port preserves owned opaque row bytes
and keeps message-domain types inside the existing adapter.

Verified:

- Tests preceded implementation. Missing storage operations RED:
  `/tmp/mqtt-anchor-repair-red.log`; missing neutral port RED:
  `/tmp/mqtt-anchor-repair-adapter-red.log`. Focused green runs:
  `/tmp/mqtt-anchor-repair-focused.log` and
  `/tmp/mqtt-anchor-repair-adapter-focused.log`.
- `GOWORK=off go test -race ./pkg/db/message ./pkg/channel/store
  ./pkg/channel/replication -count=1 -timeout=180s`: all passed in
  `/tmp/mqtt-anchor-repair-regression.log`; MessageDB 30.341 seconds, store 7.395,
  replication 3.904. Coverage includes originals removed on both sides, restart,
  exact/historical retries, local-prefix continuation, forged native fields,
  missing/pending/corrupt independent proof, bad final rows, gaps, short/oversized
  pages, wrong identity/range, cancellation, closed leases and owned result bytes.
  Test-controlled release models prior source reclamation; no product release
  operation is introduced or claimed.
- `GOWORK=off go test -race -tags=integration ./pkg/channel/replication
  ./pkg/channel/service -run '^TestMQTT(ReplayAnchor|Anchor)' -count=1
  -timeout=90s -v`: all passed in `/tmp/mqtt-anchor-repair-integration.log`;
  replication 3.824 seconds, service 1.940. Three voters plus one learner retain
  committed journals through real disk and native replication wire codecs.
  Journal replication alone does not produce shared content; the learner imports
  through the new port using its independent journal, changes no log frontier,
  and retains exact content across all-runtime restart. This is storage/runtime
  integration; donor page network scheduling and product process E2E remain open.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-anchor-repair-flow.log`. The first check caught an over-length FLOW;
  its prose was condensed, the index regenerated and the check passed.
  `git diff --check` passed. Existing macOS LC_DYSYMTAB warnings are non-failing.

Next connect bounded anchor selection and donor network repair with replicated
source-release decisions and learner/migration readiness, then consumer-proof
shared GC. Full source/inbox projection, permission ordering, delivery/ACK/recovery,
Will execution, unavailable-owner proof, app/configuration, state-transfer tooling
and process/load acceptance remain required. The product MQTT listener remains
unavailable; the full implementation goal is active.

## Foreground exact-replica repair through Node and RPC 98

Source `7b0dcab92`; the frozen context and failure inventory are in
[mqtt-replay-repair-routing.md](../specs/mqtt-replay-repair-routing.md).
`Node.RepairChannelMQTTReplay` now routes an explicitly selected anchor interval
to one current target replica, which fetches from one current donor. Requests
contain identities, fences and finite range budgets, never an expected content
digest or caller HW. The target checks its own committed journal before fetching
and imports through the atomic anchor-bound store port. Fresh Slot checks surround
origin/target/donor work, including a further check immediately before import.
Full ordered placement/quorum/status and write fences must remain unchanged.

Immutable recovery is allowed under an existing migration write fence and may
repair a learner. It does not publish migration readiness. Separate four-slot
receiver/coordinator and donor admission has no waiting queue; nested cross-node
requests cannot occupy the pool needed by donor reads. Calls have five-second
deadlines, with 256-row / 16-MiB transfer limits. No per-Channel goroutines or
background retry loops were introduced. A post-import authority failure withholds
only the receipt; identical durably imported content remains safe to retry.

RPC 98 uses closed `WMDQ/WMDR` version 1, a 4 KiB request bound, explicit repair
and export actions, exact full-request echo and the existing closed status catalog.
Export embeds the unchanged bounded replay-page codec; repair replies are body-free
prefixes. Decoding retains independent content bytes. The service uses foreground
mutation admission, the stable service gateway and matching peers; there is no
fallback to ordinary history or an older lossy encoding.

Verified:

- Tests preceded implementation. Missing entry/repair types RED:
  `/tmp/mqtt-repair-routing-red.log`; focused green:
  `/tmp/mqtt-repair-routing-focused.log` (0.564 seconds).
- `GOWORK=off go test -race ./pkg/channel/... ./pkg/cluster/channels
  ./pkg/cluster/net ./pkg/cluster -count=1 -timeout=180s`: all passed,
  `/tmp/mqtt-repair-routing-regression.log`. Cluster/channels 11.491 seconds,
  cluster/net 4.389, cluster root 8.033. Tests cover missing/pending/foreign/future
  independent proof, wrong endpoints and digests, pre-import/post-import metadata
  changes, cancellation, migration fences, learners, weak quorum, saturation,
  lease/admission cleanup on panic, gateway replacement and strict RPC
  framing/version/action/status/echo/budget/ownership behavior.
- `GOWORK=off go test -race -tags=integration ./pkg/cluster
  -run '^TestMQTTRepairThreeNode' -count=1 -timeout=90s -v`: passed,
  `/tmp/mqtt-repair-routing-integration.log`, test 11.33 seconds/package 12.758.
  Three real TCP/disk nodes, 256 hash slots and two physical Slots use two Channel
  voters and one learner. The learner first has a committed journal but no shared
  content, repairs through origin→target→donor RPCs, retries with a different
  donor, restarts with identical content and retries again. Loss of Slot quorum
  rejects repair even when local content exists. Original-body removal remains
  covered by the storage tests; this Node test does not claim product MQTT E2E.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-repair-routing-flow.log`. Index regenerated; `git diff --check` passed.
  Existing macOS LC_DYSYMTAB warnings remain non-failing.

Automatic bounded anchor/interval selection and donor rotation remain required
before background recovery can replace the explicit one-interval operation.
Replicated source release, learner/migration readiness, consumer-proof shared GC,
source/inbox projection, permission ordering, delivery/ACK/recovery, durable Will
execution, unavailable-owner proof, app/configuration, state transfer and full
process/load acceptance remain open. The product MQTT listener is unavailable and
the full implementation goal remains active.

## Bounded replica-local recovery interval selection

Source `cb65b5e72`; frozen context and the pre-implementation failure inventory
are in [mqtt-replay-repair-planning.md](../specs/mqtt-replay-repair-planning.md).
A pinned storage read now selects the next recoverable anchor interval from actual
local replay coverage, bounded by one exact committed target. It distinguishes a
next interval, completion of that target, and scan continuation. At most 64 journals
are inspected per call. Continuation hints must name an independently committed
anchor whose full cumulative prefix already matches the local meter; callers
cannot use a cursor to skip missing content. Existing journal and metering keys
suffice; no persisted format changes were made.

The next interval starts at local coverage plus one, with exact count/byte budgets
bounded by 256 rows / 16 MiB. Covered journals are verified before skipping; a local
tail point check rejects missing/corrupt frontier evidence. A missing replay table
remains missing even when source release is ahead. Completion is relative to the
requested target, including historical targets after later progress, and changes
no checkpoint, source-release decision or content frontier. The optional Channel
store planner preserves these closed outcomes and derives a neutral replay range
for the existing RPC 98 repair operation.

Verified:

- Tests preceded implementation. Storage RED:
  `/tmp/mqtt-repair-plan-red.log`; Channel/adapter RED:
  `/tmp/mqtt-repair-plan-adapter-red.log`. Focused green logs:
  `/tmp/mqtt-repair-plan-focused.log` (2.813 seconds) and
  `/tmp/mqtt-repair-plan-adapter-focused.log` (Channel 0.901, store 0.753).
- `GOWORK=off go test -race ./pkg/db/message ./pkg/channel/...
  ./pkg/cluster/channels ./pkg/cluster -count=1 -timeout=180s`: all passed in
  `/tmp/mqtt-repair-plan-regression.log`. MessageDB 41.891 seconds, Channel store
  8.922, cluster/channels 7.070, cluster root 14.490. Tests cover original trim on
  both sides, restart between scan pages, partial local copy, historical target
  completion, forged/uncovered/business-position continuations, corrupt/pending/
  missing evidence, closed/canceled calls, counter/range bounds and closed result
  shapes. Test-controlled release remains a fixture, not a product release path.
- `GOWORK=off go test -race -tags=integration ./pkg/cluster
  -run '^TestMQTTRepairThreeNode' -count=1 -timeout=90s -v`: passed in
  `/tmp/mqtt-repair-plan-integration.log`, test 12.57 seconds/package 14.370.
  Three real TCP/disk nodes, 256 hash slots and two physical Slots leave two
  accepted intervals on a learner with journals but no shared content. The test
  drives the real store planner with a one-journal scan budget, observes one
  explicit continuation, repairs both selected intervals through Node/RPC 98,
  proves target completion, then verifies donor switching, restart/exact retry
  and rejection after loss of Slot quorum. This validates the planner/repair
  composition; automatic background scheduling and full MQTT process E2E remain open.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-repair-plan-flow.log`. Index regenerated and `git diff --check` passed.
  Existing macOS LC_DYSYMTAB linker warnings remain non-failing.

Next compose the planner with bounded target-owned recovery steps and donor
rotation, then replicated source release, learner/migration readiness and
consumer-proof shared GC. Full source/inbox projection, permission ordering,
delivery/ACK/recovery, Will execution, unavailable-owner proof, app/configuration,
state transfer and full process/load acceptance remain required. The product MQTT
listener remains unavailable; the full implementation goal is active.

## Target-owned recovery steps through Node and RPC 99

Failure inventory and frozen source are in
[mqtt-replay-recovery-step.md](../specs/mqtt-replay-recovery-step.md).
`Node.StepChannelMQTTReplayRecovery` now delegates one bounded operation to the
exact target replica. That replica owns interval selection from its durable
coverage and committed journals, then prioritizes the current leader and ISR
before learners. Each step tries at most four distinct donors, with 750 ms fetch
budgets inside a five-second operation. Failed rounds return the last attempted
donor; missing or removed hints restart rotation without skipping content.

The closed result preserves the full pre-import plan and distinguishes target
coverage, continued journal scanning, one imported interval and donor retry.
Only a subsequent planning read may report completion after import. Structurally
invalid or mismatched-prefix donor pages rotate; local import errors surface.
The receiver reloads its own committed proof atomically during import. Fresh
Slot authority is checked before planning, each fetch/import and completion;
changed placement/fences and cancellation withhold receipts even after a durable
import. Stable migration write fences and learner placement remain supported.

Existing four-slot receiver/donor admission is reused without nested receiver
reservations or new goroutines. RPC 99 uses closed `WMUQ/WMUR` version 1 envelopes,
full request echoes and a 4 KiB bound; RPC 98 carries the bounded content page.
No table, persisted format, configuration key or product listener was added.

Validation:

- Failure inventory and tests preceded production code. Missing-contract RED:
  `/tmp/mqtt-recovery-step-red.log`; focused green:
  `/tmp/mqtt-recovery-step-focused.log` (channels 0.743 s, net 1.139 s,
  Node package 1.311 s). Checks include donor rotation/malformed pages, invalid
  hints/proofs/outcomes, stale authority, stable migration fences, cancellation,
  import failure, panic cleanup, admission saturation and gateway replacement.
- `GOWORK=off go test -race ./pkg/channel/... ./pkg/cluster/channels
  ./pkg/cluster/net ./pkg/cluster -count=1 -timeout=180s`: passed all packages,
  `/tmp/mqtt-recovery-step-regression.log` (channels 7.170 s, net 2.017 s,
  Node package 13.893 s). Existing macOS LC_DYSYMTAB linker warnings are non-failing.
- `GOWORK=off go test -race -tags=integration ./pkg/cluster
  -run '^TestMQTTRepairThreeNode' -count=1 -timeout=90s -v`: passed,
  `/tmp/mqtt-recovery-step-integration.log`, test 11.82 s/package 13.313 s.
  Three nodes, real TCP/disks and 256 hash slots exercise origin-to-target RPC 99
  and target-to-donor RPC 98, two imported intervals with one scan continuation,
  a learner target, donor replacement for an exact retry, restart and rejection
  after Slot quorum is lost. This is runtime integration, not product process E2E.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-recovery-step-flow.log`. The generated index is current.

Next connect lifecycle scheduling, replicated source release, learner/migration
readiness and consumer-proof shared GC. Complete source/inbox projection,
permission ordering, delivery/ACK/recovery, Will execution, unavailable-owner
proof, app/configuration, state transfer and full process/load acceptance remain
required. The product MQTT listener remains unavailable; the full goal is active.

## Distinct durable source discovery for background replay

The next scheduling dependency is now available through existing Node/RPC 91.
`MQTTReadSourceOwners` (kind 16) returns at most 64 distinct Channel source
incarnations from the source-binding retention index. One pinned snapshot checks
an index/primary witness per owner and seeks directly beyond that owner's entire
subscriber prefix; the final next-owner probe bounds work to `limit + 1` witnesses.
Preparing and Removing obligations remain discoverable; Removed and UID records
are excluded. Malformed/dangling sampled witnesses fail closed. This is a work
hint, not an audit of skipped rows, a consumer-completion receipt or GC permission.

The complete cursor follows durable encoded string order and preserves source
generations. Empty pages retain the input cursor; replies reject duplicates,
regression, unordered owners, foreign arrays and incorrect last-owner cursors.
The added zero cursor/result fields are omitted so older read kinds retain their
JSON shape. New reads use fresh Slot barriers and fail on old peers that do not
recognize kind 16. No tables, indexes or persisted encodings changed.

Failure inventory and source digests precede code in
[mqtt-source-discovery.md](../specs/mqtt-source-discovery.md).

Validation:

- Missing-contract RED: `/tmp/mqtt-source-discovery-red.log`. Focused green:
  `/tmp/mqtt-source-discovery-focused.log`, metadata 0.877 s, proxy 1.198 s.
  Three source incarnations with 128 bindings each verify prefix skipping under
  a bounded cancellation-check budget, including an unfinished removal; pinned
  snapshots, bad index/primary witnesses and closed RPC outcomes are covered.
  This is algorithmic boundary evidence, not a 100,000-member load measurement.
- `GOWORK=off go test -race ./pkg/db/meta ./pkg/slot/proxy ./pkg/cluster
  -count=1 -timeout=180s`: passed, `/tmp/mqtt-source-discovery-regression.log`
  (29.064 s, 20.843 s, 17.998 s respectively).
- `GOWORK=off go test -race -tags=integration ./pkg/cluster
  -run '^TestMQTTMetadataThreeNode' -count=1 -timeout=90s -v`: first failed during
  initial `WaitClusterReady`, before any new discovery request, in
  `/tmp/mqtt-source-discovery-integration.log`. The same command rerun alone passed
  unchanged in `/tmp/mqtt-source-discovery-integration-retry.log` (test 11.60 s,
  package 12.968 s). Three nodes, TCP/disks and 256 hash slots verified distinct
  generations, pagination, Slot leader transfer, restart and isolated-read refusal.
  The bootstrap timeout was not reproduced or diagnosed as a product fix.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings,
  `/tmp/mqtt-source-discovery-flow.log`. FLOW index regenerated; existing macOS
  linker warnings remain non-failing.

The background worker itself is still outstanding. Next compose this durable
source scan with bounded copy/anchor/recovery turns, fair continuation and joined
start/stop/restore ownership, then replicated source release and consumer-proof
GC. All remaining full-product requirements recorded above remain open; product
MQTT admission is unavailable and the full implementation goal remains active.

## Bounded replay coordination through real cluster ports

The replay usecase now connects fresh Slot placement, accepted-prefix planning,
current-quorum copying, anchor admission and exact-target recovery. One turn does
one copy plus anchor commit, or one bounded recovery step. Copy attempts yield to
recovery even after errors; every attempted replica advances round-robin selection.
Each replica pins its target and retains scan/donor hints across newer anchors.
An import needs another coverage read before target completion is reported.

The body-free cursor is detached and bounded to 256 replica entries. Source or
complete placement changes reset scheduling hints; durable progress comes from
fresh planning. Copy receipts must begin at the complete accepted prefix and fit
the captured frontier and row/byte budgets. Invalid evidence, cancellation and
uncertain replies cannot claim success. Idle anchor-only tails create no controls.
App composition reuses SlotMetaSource and foreground Node APIs; it owns no worker.
No table, index, persisted format or configuration changed.

Failure inventory and frozen source context were written before code in
[mqtt-replay-coordination.md](../specs/mqtt-replay-coordination.md).

Validation:

- Missing-contract RED: `/tmp/mqtt-replay-coordination-red.log`; focused green
  `/tmp/mqtt-replay-coordination-focused.log` (0.625 s). Tests cover pinned targets,
  donor/scan continuation, rotation after failures, placement reset, detached
  cursors, copy-prefix/budget mismatch, malformed proofs, cancellation and invalid
  server identities/timestamps.
- `GOWORK=off go test -race ./internal/usecase/mqttsession -count=1 -timeout=120s`:
  passed (9.797 s), `/tmp/mqtt-replay-coordination-regression.log`.
- `GOWORK=off go test -race -tags=integration ./internal/app
  -run '^TestMQTTGroupSourcePreparationThreeNodeRecovery$' -count=1
  -timeout=120s -v`: passed (test 10.53 s, package 12.222 s),
  `/tmp/mqtt-replay-coordination-integration.log`. The three-node TCP/disk,
  256-hash-slot composition discovers the prepared source through RPC 91, runs
  real copy/anchor/recovery turns and confirms learner content coverage while
  preserving the original Session consumption boundary through owner takeover.
  Permission incarnation remains controlled; no subscription completion is claimed.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-replay-coordination-flow.log`. The generated index is current. An
  initial documentation check caught six Read First links; reduced to the allowed
  five before the passing check. macOS linker warnings remain non-failing.

Background lifecycle/fair source scheduling, replicated source release, replica
readiness and consumer-proof GC remain outstanding. Complete subscription/inbox
projection, delivery/ACK/recovery, Will execution, unavailable-owner proof, product
configuration, state transfer and full process/load acceptance remain required.
The product MQTT listener is unavailable and the full implementation goal is active.

## Managed replay discovery and bounded background work

One optional `mqtt/replay_worker` loop now owns distinct-source discovery across
locally led hash Slots, with separate read/step/turn deadlines and bounded pages
and visits. It retains only a discovery position, pass ordinal and at most one
finite journal-scan continuation per Slot. It does not allocate a per-source cache,
queue or goroutine. Completed copy/import/coverage work and failures yield to the
next source. Only successful advancing scans retain the exact pinned target;
new anchors cannot extend that visit, and errors/placement changes end it.

Cold passes rotate copy/recovery phases, replica selection and initial donor hints.
This preserves participation without retaining every source's process cursor;
actual content progress always comes from durable planning. Partial budgets keep
unstarted sources discoverable. Whole-page validation rejects malformed or late
results before effects. Source removal and Slot loss discard process hints. Stop
joins the exact run; timeout retains ownership and prevents an overlapping restart.
Shared body-free DTOs now live in `internal/contracts/mqttsession`, keeping runtime
independent of usecase construction. App provides a real Node-backed worker factory.
No storage schema, persistent encoding or operator configuration changed.

The failure inventory and frozen context preceded code in
[mqtt-replay-worker.md](../specs/mqtt-replay-worker.md).

Validation:

- Missing-contract RED: `/tmp/mqtt-replay-worker-red.log`. Focused green:
  `/tmp/mqtt-replay-worker-focused.log` (runtime 0.519 s, usecase 0.817 s).
  Coverage includes 256-Slot rotation, two passes over 4,096 failing sources with
  one retained Slot state, partial pages, pinned scans, cold replica/donor rotation,
  invalid pages/continuations, late results and lost-source/Slot hints. This is
  algorithmic boundedness evidence, not the required production load measurement.
- `GOWORK=off go test -race ./internal/runtime/mqttsession
  ./internal/usecase/mqttsession ./pkg/goroutine -count=1 -timeout=120s`: passed,
  `/tmp/mqtt-replay-worker-regression.log` (1.722 s, 9.581 s, 2.559 s).
- `GOWORK=off go test -race -tags=integration ./internal/runtime/mqttsession
  ./internal/app -run '^(TestReplayWorker|TestMQTTGroupSourcePreparationThreeNodeRecovery)'
  -count=1 -timeout=120s -v`: passed, `/tmp/mqtt-replay-worker-integration.log`.
  Runtime package 1.980 s verifies two managed runs, joined cancellation, no overlap
  after Stop timeout, fresh restart hints and per-call deadline handling.
  The three-node TCP/disk, 256-hash-slot app test (10.26 s, package 12.000 s) starts
  actual workers rather than manually pumping coordinator turns. After stopping
  them, an independent learner-target operation reports already-complete coverage
  without importing any content. Session takeover retains the initial consumption
  boundary. Permission incarnation remains controlled, and the subscription stays
  Preparing; this is runtime integration, not full product process E2E.
- Final cold-pass selection alternates copy/recovery on successive visits while
  advancing the target every two visits. After that adjustment, reran
  `GOWORK=off go test -race -tags=integration ./internal/runtime/mqttsession
  ./internal/usecase/mqttsession ./internal/app
  -run '^(TestReplay|TestMQTTGroupSourcePreparationThreeNodeRecovery)'
  -count=1 -timeout=120s -v`: all passed, `/tmp/mqtt-replay-worker-final.log`
  (runtime 1.664 s, usecase 1.901 s, app 12.186 s; three-node test 10.48 s).
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-replay-worker-flow.log`. FLOW index is regenerated. Existing macOS
  linker warnings are non-failing.

Next implement replicated source release, learner/migration readiness and
consumer-proof shared GC, then finish source/inbox projection and subscription
completion. Delivery/ACK/recovery, fenced Will execution, unavailable-owner proof,
product configuration/start-stop-restore composition, state transfer and full
process/load acceptance remain required. Product MQTT admission is unavailable;
the complete implementation goal remains active.

## Replica-local replay coverage in active migration probes

Active migration probes now attach optional replay evidence read from the target
replica's own pinned storage view. Captured native HW selects its latest committed
anchor; independently retained content and historical meters prove coverage.
Missing shared content returns uncovered even when the native log is complete.
Source copied-through is not coverage, pending controls are not requirements, and
release obligations cannot be hidden by a missing anchor. Fresh complete placement,
write fence and runtime authority are checked around the read. Old/unsupported
stores leave evidence absent; ordinary diagnostic probes stay unchanged.
The existing JSON migration RPC preserves the optional field without a table,
index or persisted-format change. No migration admission or source-release decision
is made by this observation alone.

Failure inventory and frozen context preceded code in
[mqtt-replica-readiness.md](../specs/mqtt-replica-readiness.md).

Validation:

- Missing-contract RED: `/tmp/mqtt-replica-readiness-red.log`. Focused green:
  `/tmp/mqtt-replica-readiness-focused.log` (message 2.751 s, channel 1.456 s,
  store 1.249 s, hosted service 0.472 s). Covered missing/partial shared content,
  older captured anchors, pending controls, corruption, cancellation, changed
  authority and unsupported capability. One closed-lease assertion was corrected
  to expect the compatibility layer's existing `channel: closed` error.
- `GOWORK=off go test -race ./pkg/db/message ./pkg/channel/...
  ./pkg/cluster/channels ./pkg/cluster -count=1 -timeout=180s`: all passed,
  `/tmp/mqtt-replica-readiness-regression.log` (message 37.172 s, cluster 10.676 s).
- `GOWORK=off go test -race -tags=integration ./pkg/cluster
  -run '^(TestMQTTRepairThreeNode|TestNativeQuorumColdFollowerRepairProbe|TestClusterChannelRepairProbeLoadsAndRefreshesDurableReplica)'
  -count=1 -timeout=120s -v`: passed, `/tmp/mqtt-replica-readiness-integration.log`
  (package 25.191 s, MQTT three-node test 10.48 s). Real TCP/disks and 256 hash
  slots prove the remote learner reports uncovered before repair and covered
  after restart; native cold/durable follower probes still pass. The storage
  test separately verifies readiness after original-body trimming and reopen.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-replica-readiness-flow.log`. Index regenerated; `git diff --check`
  passed. Existing macOS linker warnings remain non-failing.

Migration admission gates and background recovery under a stable write fence are
next, followed by replicated source release and consumer-proof GC. Product MQTT
admission remains unavailable and the full implementation goal remains active.

## Background replay recovery under a stable write fence

Read-only replay planning now captures the exact migration write fence with HW
and rejects changed, renewed or cleared fences at completion. Fresh Slot placement
and data-plane authority remain mandatory. Fenced coordinator turns recover only
existing committed anchors; no anchor means yield, without new copying or controls.
Native business admission retains its existing fence behavior. No persisted format,
table, configuration or new worker is introduced.

The real three-node test exposed a distinction absent from the first fixture:
native recovery sets quorum read readiness while deliberately leaving
`CommitReady=false` under a write fence. The first implementation checked that
write flag and rejected every planning turn. Bounded diagnostics recorded 125
failed turns with native `RecoveryRequired=false`, followed by a rejected plan
(`/tmp/mqtt-fenced-recovery-diagnostic.log`). The fixture was corrected to model
that state and reproduced RED before changing admission to use the existing
quorum read flag. Business writes remain closed.

The test setup also needed bounded initial anchor establishment and an authoritative
reread of the committed route generation before applying fenced metadata. Immediate
write rejection may be `not ready` during native recovery or `write fenced`; neither
is interpreted as successful business admission. These fixture corrections did not
relax the required learner-content or no-new-anchor assertions.

Failure inventory and exact source context precede code in
[mqtt-fenced-recovery.md](../specs/mqtt-fenced-recovery.md).

Validation:

- Initial RED `/tmp/mqtt-fenced-recovery-red.log`; recovered-read-state RED
  `/tmp/mqtt-fenced-recovery-read-state-red.log`. Tests cover stable/changed/renewed/
  cleared fences, native read/write separation, authority/cancellation, absent
  anchors, target rotation and retained errors without copying.
- `GOWORK=off go test -race ./pkg/channel/... ./pkg/cluster/channels
  ./internal/usecase/mqttsession -count=1 -timeout=120s`: all passed after the
  read-state correction, `/tmp/mqtt-fenced-recovery-regression-final.log`
  (reactor 2.926 s, hosted service 9.546 s, usecase 13.924 s).
- `GOWORK=off go test -race -tags=integration ./internal/app
  -run '^TestMQTTGroupSourcePreparationThreeNodeRecovery$' -count=1
  -timeout=120s -v`: passed, `/tmp/mqtt-fenced-recovery-integration-green.log`
  (test 10.13 s, package 11.913 s). Real TCP/disks, three nodes and 256 hash Slots
  verify managed learner repair under the fence, independent coverage after the
  workers stop, zero newly admitted anchors and continued business-write rejection.
  Permission incarnation is controlled and subscription completion is still absent.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-fenced-recovery-flow.log`. Reactor/usecase FLOW files remain within
  the 100-line target, and the generated index is current.

Next connect replica readiness to migration phase admission and verify graceful
transfer, replacement and automatic failover without a recovery deadlock. Replicated
source release, consumer GC and the remaining full-product work stay open; the
MQTT product listener remains unavailable and the implementation goal stays active.

## Replay coverage gates at migration cutover

Planned leader transfer and replica replacement now require explicit, current
replica-local replay coverage at final catch-up and again in the separate
commit/promote phase. Verification also requires coverage before clearing the
write fence. Missing, malformed, uncovered or recovering evidence yields without
Slot task writes, preserving runnable work. Failover commits a surviving native
leader before applying the replay gate at verification; it never drains a dead
source. No new table, persisted encoding or configuration is introduced.

The real three-node sequence exposed two native integration gaps. After replica
replacement, the source runtime reported HW 7 but its durable checkpoint was 6;
the pinned readiness read at 6 succeeded. Active recovered-leader probes now
checkpoint their captured HW and request existing bounded committed-tail repair
under exact installed authority. Stable fences permit that repair. The following
run passed this phase but graceful drain saw the previous cleared fence: drain
now applies/probes current source metadata first. Temporary native HW lag stays
runnable at both planned-transfer catch-up stages. Storage reads remain pinned
and read-only; the active leader probe owns checkpoint confirmation. Ordinary
runtime diagnostics retain their observational contract.

Failure inventory and frozen context are in
[mqtt-migration-admission.md](../specs/mqtt-migration-admission.md).

Validation:

- Admission RED: `/tmp/mqtt-migration-admission-red.log`; seven-phase/nine-mode
  matrix covers missing/invalid/uncovered/recovering evidence, proof refresh,
  native no-anchor receipts and resuming the same phase without Slot churn.
- Three-node diagnostic `/tmp/mqtt-migration-isolate2.log` distinguishes native
  runtime HW from durable checkpoint and independent content coverage.
  `/tmp/mqtt-migration-progress.log` records the subsequent stale drain fence.
- Checkpoint/refresh RED and green: `/tmp/mqtt-migration-checkpoint-red.log`,
  `/tmp/mqtt-migration-checkpoint-green.log`. Graceful phase order and native-lag
  RED: `/tmp/mqtt-migration-drain-red.log`; hosted package green 5.583 s.
- `GOWORK=off go test -race ./pkg/channel/... ./pkg/cluster/channels
  ./pkg/cluster -count=1 -timeout=180s`: passed;
  `/tmp/mqtt-migration-regression-final.log` (hosted 9.399 s, cluster 12.065 s).
- Real three-node replacement followed by graceful leader transfer passed with
  TCP/disks, authoritative Slot tasks and 256 hash Slots. It verifies the target
  has caught up natively while missing replay, unchanged runnable task/fence,
  explicit bounded recovery and successful membership/leadership transitions.
  `/tmp/mqtt-migration-final-integration.log` (12.72 s); combined race regression
  `/tmp/mqtt-migration-recovery-regression.log` repeats migration (14.83 s) and
  managed fenced app recovery (11.36 s). Migration scanning is deliberately
  controlled by an exact real-task selector; background native replication is real.
- Native cold-follower probing and real three-node replay repair/restart/isolation
  passed under race detection: `/tmp/mqtt-migration-cold-repair.log`
  (tests 5.71 s / 10.13 s, package 17.224 s). The earlier combined regex matched
  only migration/app tests; this separate run uses the exact remaining test names.
- `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-migration-flow.log`. FLOW index regenerated; diff whitespace clean.

Automatic dead-leader failover ordering is covered by executor tests, not yet a
real stopped-node MQTT migration test. Replicated source release, consumer-proof
GC, complete subscription/inbox projection and permission ordering, durable
subscription/downlink/PUBACK entry paths, unavailable-owner recovery, fenced Will
execution, full lifecycle/configuration, MQTT offline transfer and process/load
acceptance remain open. The product listener remains unavailable; the full
implementation goal remains active.

## Stopped-leader failover and resumed ordinary sending

The real three-node failover test now stops the original Channel leader, selects
an ISR target lacking shared replay, and runs the real Slot-backed failover task.
It proves native leader installation before replay recovery, unchanged runnable
verification while content is missing, rejected writes under the fence, repair
from a surviving donor, content equality, fence removal and resumed ordinary
append. TCP, disks and 256 hash Slots are real. Target selection/task scheduling
are controlled; this is not a claim about automatic health-scanner selection or
product MQTT E2E completion.

The first fixture used full write readiness after a node stopped, which also
requires three healthy placement candidates for new Channels. Existing Channel
recovery instead needs the surviving Slot/Channel quorum, so the fixture now
checks fresh authoritative metadata. The next run completed recovery but sending
still dialed the dead cached leader (`/tmp/mqtt-failover-stopped-leader3.log`).
Single and batch append now include typed transport dial failure in their existing
one-shot fresh-authority retry. Exact-version invalidation and ambiguous-send
recovery remain unchanged. Sequential joined polling removes a test diagnostic
race caused by reading a timed-out Eventually callback's error variable.

Failure inventory and frozen context:
[mqtt-failover-recovery.md](../specs/mqtt-failover-recovery.md).

Validation:

- Failing single/batch/permanent-dial regression:
  `/tmp/mqtt-failover-dial-red.log`.
- `GOWORK=off go test -race ./pkg/cluster/channels -count=1 -timeout=120s`:
  passed, 7.534 s; `/tmp/mqtt-failover-dial-green.log`. Includes existing
  metadata-floor, bounded retry and uncertain committed-outcome recovery tests.
- `GOWORK=off go test -race -tags=integration ./pkg/cluster
  -run '^TestMQTTFailoverThreeNodeRecoversMissingContentAfterLeaderStops$'
  -count=1 -timeout=120s -v`: passed, test 11.94 s / package 13.300 s;
  `/tmp/mqtt-failover-stopped-leader-green.log`, with explicit evidence line.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-failover-flow.log`. Generated index is current and diff whitespace clean.

Next connect independently proven source release, then consumer-proof replay GC
and the remaining subscription/downlink/owner/Will/lifecycle/tooling acceptance.
Source release and the MQTT product listener are still unavailable; the full goal
remains active. No schema or wire-format change was needed for this repair.

## Original-source release from an own committed replay anchor

Storage now exposes `ReleaseMQTTSourceAtAnchor` on the canonical Channel log and
compatibility lease. The caller supplies only the generation and anchor position.
Under append/checkpoint ownership it verifies its own committed journal, paired
proposal/entry identities, activation, checkpoint, local shared tail and anchored
cumulative prefix. A synchronous batch advances only System 12 and records the
anchor manifest digest. Exact and older retries validate evidence before returning
the current state. Local materialization revisions need not match across replicas
that skip different intermediate anchors. No table, encoding, checkpoint mutation,
content deletion, caller digest or caller release watermark is introduced.

The storage tests exercise native replicas with identical anchors but missing
shared content; import followed by release; historical anchor release while the
local prefix is newer; protected suffix retention; concurrent release/trim;
corrupt or missing evidence; revision overflow; cancelled/closed operations;
restart and binary backup/restore after original history removal. Proofs rely on
atomic immutable replay prefixes and bounded endpoint verification, not an audit
of every historical body. Shared-content GC is still unavailable.

Failure inventory and frozen context are in
[mqtt-source-anchor-release.md](../specs/mqtt-source-anchor-release.md).

Validation:

- Test-first RED: `/tmp/mqtt-source-anchor-release-red.log` (missing interface).
- Initial focused run exposed a fault-fixture issue: direct engine retention
  corruption bypassed the warm cache. Switching to the retention writer also
  required a valid retained-max sequence before testing the intended source-fence
  violation. These were fixture corrections; production logic was unchanged.
- `GOWORK=off go test -race ./pkg/db/... -count=1 -timeout=180s`:
  all other packages passed, including transfer (100.378 s); the new retention
  fixture was the only failure in message. `/tmp/mqtt-source-anchor-release-db.log`.
- After correcting that fixture, `GOWORK=off go test -race ./pkg/db/message
  -count=1 -timeout=120s`: passed, 31.659 s;
  `/tmp/mqtt-source-anchor-release-message.log`.
- `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-source-anchor-release-flow.log`. Generated index is current.

Automatic source release still requires runtime/current-authority routing and
bounded background invocation. Consumer-proof GC, subscription/inbox projection,
permissions, durable downlink/ACK entry paths, unavailable-owner recovery, Will
execution, full lifecycle/configuration, offline tooling and product/process/load
acceptance remain required. Product MQTT access is still unavailable and the full
implementation goal remains active.

## Routed background release of original source prefixes

The target-owned recovery request now accepts explicit `ReleaseSource` intent.
Complete coverage invokes an optional Channel storage release port, with a fresh
full placement/fence check immediately before mutation and the existing serving/
forwarding rechecks before reply. Storage independently verifies its committed
anchor and immutable shared prefix. Scan/import/donor-retry outcomes do not
release; ordinary recovery remains unchanged. Missing capabilities, stale or
cancelled views, and store errors cannot report success. Durable effects preceding
a lost/stale response remain safe and retryable, never rolled back.

RPC 99 v1 retains ordinary recovery. Version 2 represents explicit release intent
and requires a matching `SourceReleased` completion acknowledgement; the exact
versioned request is echoed. Old peers reject v2, and old/missing/unsolicited
acknowledgements cannot complete release. No new RPC number, table, stored encoding,
queue or worker was added. The existing coordinator requests release on its
bounded replica visits, including stable migration fences and learners. It only
retires a completed target hint after the explicit acknowledgement validates.

Failure inventory and frozen context:
[mqtt-source-release-routing.md](../specs/mqtt-source-release-routing.md).

Validation:

- Test-first RED: `/tmp/mqtt-source-release-routing-red.log` (missing contract
  fields and storage capability). New tests cover explicit/ordinary/partial
  intent, absent capability, placement/fence changes before and after mutation,
  cancellation, failure/panic cleanup, learners, stable fences, exact wire echoes,
  version separation, truncations and missing/unrequested acknowledgements.
- The first broad race run encountered disk exhaustion while linking; all running
  test processes were observed terminal before clearing the reproducible Go build
  cache (14 GiB) and retrying with package concurrency 2. Its new learner fixture
  also needed a leader within the remaining ISR. Neither issue changed production
  logic. `/tmp/mqtt-source-release-routing-green.log` retains that failed attempt.
- `GOWORK=off go test -p 2 -race ./pkg/channel/... ./pkg/cluster/channels
  ./internal/usecase/mqttsession -count=1 -timeout=180s`: passed all packages;
  hosted Channels 7.432 s, usecase 9.514 s.
  `/tmp/mqtt-source-release-routing-final.log`.
- Real three-node target recovery, two bounded intervals, intermediate retention
  remaining clamped, explicit release and physical trim, restart/exact retry,
  content equality and isolated Slot rejection passed under race detection:
  `GOWORK=off go test -race -tags=integration ./pkg/cluster
  -run '^TestMQTTRepairThreeNodeLearnerRestartAndIsolation$' -count=1
  -timeout=120s -v`; test 10.10 s, package 11.690 s.
  `/tmp/mqtt-source-release-cluster.log`.
- Managed three-node app composition passed under race detection with 256 hash
  Slots, real TCP/disks, fenced learner repair and source release on all replicas:
  `GOWORK=off go test -p 2 -race -tags=integration ./internal/app
  -run '^TestMQTTGroupSourcePreparationThreeNodeRecovery$' -count=1
  -timeout=120s -v`; test 10.08 s, package 11.817 s.
  `/tmp/mqtt-source-release-worker-active.log`. The test explicitly invokes the
  native retention facade to prove physical deletion is now permitted; it never
  requests source release itself. Cold disk-only followers require their current
  Channel role installed before using that runtime facade, as diagnosed by
  `/tmp/mqtt-source-release-worker-final.log`. Shared proofs remain readable and
  business writes remain fenced after cleanup.
- `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-source-release-routing-flow.log`. Generated index and whitespace pass.

Next implement consumer-proof shared-content GC without weakening the retained
anchor, recovery and readiness contracts. Full subscription/inbox projection,
permission ordering, durable downlink/ACK paths, unavailable-owner recovery, Will
execution, product lifecycle/configuration, offline tools and process/load
acceptance remain required. Product MQTT admission stays unavailable and the full
implementation goal remains active.

## Consumer completion projection

`SourceProgress` now projects the durable cursor's contiguous completed prefix
into its exact Channel-source binding. It uses the existing foreground Node
ports: one source-owned point read, one pinned current Session/exact-cursor read,
and at most one binding CAS. Unchanged floors cause no write; multiple ACKs can
coalesce. Accounting, window admission and a later ACK never skip an earlier gap.
Normal removal caps completion at its sealed end. No table, column, Slot command,
RPC, retained cache or per-consumer worker was added.

Only explicit ended state or a newer Session lifetime records Session termination.
That decision retains `Removing` and the previous protection acknowledgement;
it cannot mark `Removed` or invent the separate source release receipt. Offline,
expired lease, missing rows and read failures prove no completion. Foreign or
incoherent identities/revisions, partial responses, cancellation, clock regression,
overflow and changed CAS receipts fail without claiming a committed projection.
Lost commit replies recover through a fresh read without another write.

Contract and test-first failure inventory:
[mqtt-consumer-progress.md](../specs/mqtt-consumer-progress.md).

Validation:

- Test-first RED for both usecase and composition: missing SourceProgress and
  factory interfaces; `/tmp/mqtt-consumer-progress-red.log` and
  `/tmp/mqtt-consumer-progress-integration-red.log`.
- Focused completion/fault tests passed (4.913 s), followed by
  `GOWORK=off go test -p 2 -race ./internal/usecase/mqttsession -count=1
  -timeout=180s`: passed, 13.397 s. Logs:
  `/tmp/mqtt-consumer-progress-focused.log`, `/tmp/mqtt-consumer-progress-race.log`.
- `GOWORK=off go test -p 2 -race -tags=integration ./internal/app
  -run '^TestMQTTGroupSourcePreparationThreeNodeRecovery$' -count=1
  -timeout=120s -v`: passed, test 10.83 s/package 12.562 s;
  `/tmp/mqtt-consumer-progress-three-node.log`. Three real TCP/disk nodes and
  256 hash Slots verify distinct consumer/source hash Slots, committed ACK-gap
  preservation, one coalesced projection, independent remote reads and pending
  removal after explicit Session end. Subscription activation and publication
  reference admission are controlled test inputs, not product SUBACK/downlink
  acceptance. Existing owner takeover, learner recovery, source release,
  retention and permission checks remain passing.
- `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-consumer-progress-flow.log`. The generated index and whitespace pass.

Next establish the coherent source GC certificate and new-consumer admission
ordering, then shared physical reclamation while preserving anchor recovery and
readiness. Final binding removal, full subscription/inbox projection, permission
ordering, downlink/ACK entry paths, unavailable-owner recovery, Will execution,
product lifecycle/configuration, offline tooling and process/load acceptance
remain required. The full goal is active; product MQTT admission stays unavailable.

## Coherent consumer floor and ordered retention planning

`ReplayRetention` now captures the accepted Channel replay anchor before reading
the first source-owned retention page. A fresh Slot barrier and strict pinned
primary/index view provide the minimum consumer floor, capped by that anchor.
Unknown preparation and Removing obligations continue to constrain the result.
The final fresh placement read rejects changes in membership, route, status or
write fence. Cancellation and unproven/foreign/partial responses return no plan;
results detach placement slices. No anchor means no range and no consumer read.

Admission safety depends on the existing GroupSources ordering: commit unknown
responsibility, then confirm the fresh source tail, then fix the start. A binding
registered after the planner's snapshot therefore cannot choose a start below
its earlier captured anchor. Reading consumers before capturing the anchor would
break that guarantee. The planner performs at most four bounded port calls,
without a subscriber scan, cache, worker, new table/command/read kind or wire format.

The generic index reader previously skipped absent or mismatched primary rows.
New fault tests reproduced both cases returning success. The retention method
now pins its own snapshot for direct calls (or reuses its enclosing read snapshot)
and verifies at most limit+1 witnesses, failing on missing, stale or corrupt
entries. Existing pagination conventions are preserved. This is not a full
integrity audit for arbitrary missing indexes; atomic index maintenance remains
the storage invariant.

Contract, ordering argument and test-first inventory:
[mqtt-replay-retention-planning.md](../specs/mqtt-replay-retention-planning.md).

Validation:

- Storage RED reproduced missing/stale witness skipping;
  `/tmp/mqtt-retention-storage-red.log`. Usecase/composition RED confirmed the
  missing APIs: `/tmp/mqtt-retention-plan-red.log`,
  `/tmp/mqtt-retention-composition-red.log`.
- Focused tests passed: metadata 0.995 s/usecase 1.062 s;
  `/tmp/mqtt-retention-focused.log`.
- `GOWORK=off go test -p 2 -race ./pkg/db/meta ./pkg/slot/proxy
  ./internal/usecase/mqttsession -count=1 -timeout=180s`: passed, respectively
  32.138 s, 15.513 s and 23.199 s; `/tmp/mqtt-retention-race.log`.
- `GOWORK=off go test -p 2 -race -tags=integration ./internal/app
  -run '^TestMQTTGroupSourcePreparationThreeNodeRecovery$' -count=1
  -timeout=120s -v`: passed, test 14.03 s/package 15.736 s;
  `/tmp/mqtt-retention-three-node.log`. Real three-node TCP/disks and 256 hash
  Slots verify ACK-gap floors, authoritative minima, unknown registration
  blocking and post-registration boundary selection alongside prior recovery
  coverage. The test controls subscription/window admission and the additional
  registration; it does not claim complete product projection or physical GC.
- `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-retention-flow.log`. Generated index and whitespace pass.

The resulting plan is not a durable GC certificate. Next replicate the selected
source decision and implement physical reclamation with retained accounting/hash
boundaries so repair, readiness and restart remain valid after prefix deletion.
Final binding removal and all remaining product projection, permission, delivery,
owner recovery, Will, lifecycle/configuration, tooling and process/load acceptance
work remain required. The full goal stays active and MQTT product access unavailable.

## Replicated replay-retirement decision journal

Proposal format 6 now explicitly records retirement of one complete previously
accepted replay anchor. The closed payload includes its immutable prefix,
original control position and exact proposal digest, under a separate hash
domain. Business format selection remains 1–3. The native sequencer preserves
retirement intent through retained/pending retries and conflict reconciliation;
ordinary retries and mixed control flags cannot reuse its receipt. Unsupported
stores reject both append and recovery replacement.

Message System 15 journals the canonical envelope atomically with the exact
proposal. Staging verifies the retained or same-batch activation, a covered
matching anchor and a nonregressing prefix; an equal prefix keeps its original
reference. A covered recovery batch may carry activation, anchor and retirement
together. Point reads independently verify HW, source, anchor, paired proposal
and entry identity. Pending decisions remain unreadable, suffix replacement
removes pending journals, and ordinary history trimming leaves committed proof.
Backup omits pending decisions and preflights journal/reference completeness and
monotonicity; restart and restore preserve the decision.

No shared content/meters are deleted or coverage fabricated. The journal provides
the future pruned prefix's accepted hash/counter baseline; consumer admission is
still controlled in the native-quorum integration test. The product producer,
physical cleanup, suffix-only recovery and readiness changes remain unwired.
Exact format 6 requires matching replicas and tools, with pre-feature backups
for rollback; original business encodings and backup framing are unchanged.

Contract and test-first inventory:
[mqtt-replay-retirement.md](../specs/mqtt-replay-retirement.md).

Validation:

- Contract/storage/capability RED logs: `/tmp/mqtt-retirement-red.log` and
  `/tmp/mqtt-retirement-adapter-red.log`. Initial fixture compilation identified
  the existing core source-state accessor and StoreAppendBatch interface; the
  fixture was corrected to those APIs. Focused tests passed: quorumlog 0.500 s,
  message 2.586 s, replication 0.399 s;
  `/tmp/mqtt-retirement-focused.log`.
- A further staging audit reproduced acceptance after both activation and
  source projections were removed: `/tmp/mqtt-retirement-activation-red.log`.
  Staging now resolves the retained/same-batch activation and checks its committed
  frontier and identity before accepting the reference.
- `GOWORK=off go test -p 2 -race ./pkg/quorumlog ./pkg/db/... ./pkg/channel/...
  -count=1 -timeout=180s`: every DB and Channel package passed, including message
  42.977 s and transfer 96.038 s. One quorumlog legacy-format test still asserted
  that version 6 was unsupported; `/tmp/mqtt-retirement-race.log` records that
  failure. Updating the unknown-version fixture to 7 preserves its native format
  assertions. After the explicit sequencer flag/reader wiring,
  `GOWORK=off go test -p 2 -race ./pkg/quorumlog ./pkg/channel/... -count=1
  -timeout=180s` passed all packages; `/tmp/mqtt-retirement-native-final.log`.
- Sequencer integration RED verified missing explicit intent/reader APIs:
  `/tmp/mqtt-retirement-sequencer-red.log`. Then
  `GOWORK=off go test -p 2 -race -tags=integration ./pkg/channel/replication
  -run '^TestMQTTReplayAnchorQuorumRestartRecoveryAndLearner$' -count=1
  -timeout=90s -v` passed, test 0.83 s/package 3.029 s;
  `/tmp/mqtt-retirement-replicas.log`. Three real disk-backed voters and one
  learner pass actual exchange codecs, quorum commit, control/business retry
  separation, committed refresh, restart and recovered authority. Transport is
  the existing in-process wire-link fixture; this does not claim TCP/process
  product acceptance or independently wired consumer permission.
- `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-retirement-flow.log`. Generated index and whitespace pass.

Next materialize the retired replay baseline and bounded physical cleanup while
preserving metering, transfer, repair, readiness and pruned backup restoration,
then connect ordered consumer admission and current-authority routing. Final
binding removal, full subscription/inbox projection, permission ordering,
downlink/ACK paths, owner recovery, Will execution, product lifecycle/configuration,
offline tools and process/load acceptance remain required. The full goal remains
active; product MQTT admission is still unavailable.

## Bounded historical retirement-anchor selection

The retirement journal stage was committed as `b825be9df`. Its next prerequisite
now selects a complete accepted anchor when the coherent consumer floor lies
between anchors. The MessageDB/Channel optional read port pins an exact captured
anchor and scans at most 64 earlier journals per call. Each visited proof checks
its own source, HW, paired proposal and entry identity. Selection rounds down;
newer commits cannot expand the captured range, and replica-local replay bodies
are unnecessary. There is no new table, stored format or RPC version.

Results distinguish an eligible whole anchor, exhaustion and reverse-page
continuation. Continuations are revalidated as committed, within the capture and
above the supplied floor; making a cursor eligible by changing the floor requires
restarting selection. The typed Channel contract rejects mixed outcomes, forward
cursors and changed capture identities. Selection makes no mutations and grants
no consumer or current-authority permission. The native quorum integration now
selects the anchor before its controlled retirement proposal, and verifies the
same selection on all voters/learner after reopen and authority recovery.

Contract/failure inventory and frozen context:
[mqtt-retirement-anchor-selection.md](../specs/mqtt-retirement-anchor-selection.md).

Validation:

- Storage and typed-adapter RED logs prove the missing APIs before their
  implementation: `/tmp/mqtt-retirement-selection-red.log` and
  `/tmp/mqtt-retirement-selection-adapter-red.log`. Focused storage, contract and
  adapter tests passed (0.805 s, 0.829 s and 0.702 s respectively).
- `GOWORK=off go test -p 2 -race ./pkg/db/message ./pkg/channel/... -count=1
  -timeout=180s`: all passed; message 36.385 s, store 5.584 s and replication
  3.451 s. Log: `/tmp/mqtt-retirement-selection-race.log`.
- `GOWORK=off go test -p 2 -race -tags=integration ./pkg/db/message
  ./pkg/channel/replication -run
  '^(TestMQTTRetirementSelection|TestMQTTReplayAnchorQuorumRestartRecoveryAndLearner)'
  -count=1 -timeout=120s -v`: passed; message 4.216 s, replication 2.361 s.
  `/tmp/mqtt-retirement-selection-integration.log` records original-history trim,
  restart, backup/restore, a stable capture after later commits, absent shared
  content, fourteen proof/cursor fault cases, cancellation and closed leases.
  Three disk-backed voters and one learner retain selection and format-6 intent
  through the existing in-process exchange-codec fixture; this is not a TCP or
  process-level product acceptance test. Consumer admission remains controlled.
- `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-retirement-selection-flow.log`. Generated index and whitespace pass.

Retirement baseline materialization, bounded physical deletion, suffix-only
repair/readiness and pruned backup restoration remain next. The selection port
still needs ordered product admission/current-authority routing. Final binding
removal, subscription/inbox projection, permission ordering, delivery/ACK,
owner recovery, Will execution, product lifecycle/configuration, offline tools
and process/load acceptance remain required. The full goal stays active and the
MQTT product listener remains unavailable.

## Retired baselines, bounded cleanup and suffix-only backups

The MessageDB/Channel store now applies independently committed format-6
retirements. Table 2 System 2 links the original control position to a separate
deleted-through cursor. The exact cumulative prefix comes from the independently
verified journal; the existing local replay frontier advances to that baseline or
keeps a verified later prefix. Under append/checkpoint ownership, one synchronous
batch publishes baseline/frontier and removes at most 256 primary rows plus their
meter range. A key-only seek skips absent positions, with one lookahead. The cursor
reports engine removal; disk bytes are reclaimed by existing compaction.

Logical reads reject retired prefixes immediately, including while cleanup is
partial. Endpoint metering and suffix copy/import retain original cumulative
counters/hashes. Already retired anchors satisfy recovery/source-release
responsibility without claiming their bodies still exist. Readiness and backup
cuts cannot use a retirement newer than their captured HW. Applying a committed
decision does not require downloading an absent old copy; exact/older retries
preserve newer materialization and bounded cleanup progress.

Pruned portable snapshots now use version 3: one optional baseline reference and
only the retained suffix, including a valid empty suffix. Export normalizes the
deleted-through cursor to the retired endpoint because the archive carries none
of those bodies. Restore preflights references, source/proposal proofs, target
compatibility and the full suffix hash chain, then publishes baseline/frontier
atomically after bounded content batches. Equivalent partial target cleanup is
finished with range tombstones; unpruned rollback archives fail. Version-1/2 export
bytes remain unchanged, and matching writers/tools remain mandatory.

An interrupted-restore audit exposed that the legacy version-2 system header
contained a redundant replay frontier installed before its replay rows. Import
now validates that duplicate but defers publication to the replay section's final
commit. The new baseline is excluded from native headers entirely. Version-2/3
interruption tests prove native metadata may have installed while both replay
frontier and baseline remain absent, followed by successful exact restore retry.

Contract/failure inventory and frozen context:
[mqtt-retired-replay-storage.md](../specs/mqtt-retired-replay-storage.md).

Validation:

- Missing core/adapter APIs produced RED before implementation:
  `/tmp/mqtt-retired-storage-red.log`, `/tmp/mqtt-retired-adapter-red.log`.
  Initial core cases passed in 2.405 s; `/tmp/mqtt-retired-core.log`.
- Additional failure inventories reproduced three real gaps before their fixes:
  a copied frontier above HW (`/tmp/mqtt-retired-future-red.log`), a missing replay
  frontier beside a retained baseline (`/tmp/mqtt-retired-missing-state-red.log`),
  and premature coverage during interrupted restore
  (`/tmp/mqtt-retired-restore-order-red.log`). Reads now pin frontier/marker
  absence together; application rejects future coverage; restore publishes last.
- `GOWORK=off go test -p 2 -race -tags=integration ./pkg/db/message
  ./pkg/channel/replication -run
  '^(TestMQTTRetiredReplay|TestMQTTReplayAnchorQuorumRestartRecoveryAndLearner)'
  -count=1 -timeout=120s -v`: passed, message 5.636 s, replication 2.607 s;
  `/tmp/mqtt-retired-integration-final.log`. Coverage includes partial cleanup,
  absent-copy retirement, retained-suffix transfer/metering, import rejection
  below retirement, exact and older retries, reopen, original-source release,
  historical cuts, partial/empty-suffix backup restore, changed proof rejection,
  canceled work and missing/corrupt baseline evidence. Three disk-backed voters
  and one learner apply the decision, clean content and verify readiness through
  reopen and authority recovery using actual exchange codecs. Transport remains
  the in-process wire fixture and consumer admission is controlled.
- `GOWORK=off go test -p 2 -race -tags=integration ./pkg/db/message -run
  '^TestMQTTRetiredReplayInterruptedRestoreDoesNotPublishCoverage$' -count=1
  -timeout=90s -v`: verifies the final version-2/version-3 interruption matrix;
  `/tmp/mqtt-retired-restore-versions.log`.
- `GOWORK=off go test -p 2 -race ./pkg/db/... ./pkg/channel/... -count=1
  -timeout=180s`: all passed after final production edits; message 42.747 s,
  transfer 96.476 s, store 4.365 s. `/tmp/mqtt-retired-final-race.log`.
- `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-retired-flow.log`. Generated index and whitespace pass.

Next connect ordered consumer admission/current-authority retirement production
and bounded replica application to replay recovery. A replica whose old content
was never copied must apply its committed baseline before requesting that retired
content from donors. The producer/coordinator must also avoid a maintenance
feedback loop where copying a retirement-only native tail creates another anchor
and retirement indefinitely; control positions still require coherent cursors.
Final binding removal, complete subscription/inbox projection, permission ordering,
delivery/ACK, owner recovery, Will execution, product lifecycle/configuration,
offline tools and process/load acceptance remain required. The goal is active and
product MQTT access remains unavailable.


## Committed retirement application before replica recovery

RPC 99 version 3 explicitly applies a target's latest committed retirement before
its repair planner or any donor fetch. A pinned HW/source/journal lookup uses one
reverse seek and bounded independent proposal/anchor checks, excluding pending
controls. Current placement is checked again before the store independently
revalidates and applies the decision with a 64-primary-row cleanup limit. No
caller-supplied consumer floor, deletion position, new native control or new
storage encoding is introduced. Ordinary version-1/2 bytes and behavior remain.

The reply separates logical plan completion from pending physical cleanup.
Direct coordinator visits retain their target anchor and rotate; the managed
worker yields each durable cleanup step to other sources and resumes its store
cursor on a later cold pass. Only advancing finite journal scans may request a
worker continuation. An initial coordinator implementation incorrectly reused
that continuation flag; the worker-contract audit reproduced the error before
removing it. Completion waits for cleanup, while source release remains a
separate explicit effect and neither result grants new consumer authority.

Contract/failure inventory and frozen context:
[mqtt-retirement-recovery.md](../specs/mqtt-retirement-recovery.md).

Validation (all terminal passes):

- Missing APIs produced RED before implementation in
  `/tmp/mqtt-retirement-recovery-red.log`. The cleanup/yield regression was RED in
  `/tmp/mqtt-retirement-recovery-yield-red.log` before its fix.
- `GOWORK=off go test -p 2 -race ./pkg/db/message ./pkg/channel
  ./pkg/channel/store ./pkg/cluster/channels ./internal/usecase/mqttsession
  -count=1 -timeout=180s`: all passed; message 63.751 s, Channel 2.407 s,
  store 6.143 s, cluster channels 7.007 s, usecase 29.712 s.
  `/tmp/mqtt-retirement-recovery-race.log`. Boundary cases cover explicit intent,
  absence, future/foreign proof, unsupported stores, metadata changes, stable
  fences, cancellation/panic, malformed outcomes and closed v3 codecs.
- `GOWORK=off go test -p 2 -race -tags=integration ./pkg/db/message
  ./pkg/cluster/channels ./pkg/cluster -run
  '^(TestMQTTLatestRetirement|TestMQTTRetirementRecoverySkipsPrunedBodiesAndResumesCleanup|TestMQTTRepairThreeNodeLearnerRestartAndIsolation)'
  -count=1 -timeout=150s -v`: all passed; message 6.079 s, channels 2.706 s,
  cluster 14.692 s. `/tmp/mqtt-retirement-recovery-integration.log`.
  Real disks prove pending decisions excluded, corrupt evidence rejected,
  absence without writes, and restart discovery. Service-entry integration
  commits a controlled retirement through 65, prunes the donor, and recovers
  only positions 66..67 on a target missing old bodies or retaining 65 old rows;
  the latter cleans 64 rows in its first turn and finishes after reopen.
  The three-node TCP scenario exercises v3 forwarding, learner recovery, original
  source release/trim, restart, and rejection after loss of Slot quorum. It has
  no retirement producer; the real-disk service scenario supplies that decision.
- After the coordinator yield fix, `GOWORK=off go test -p 2 -race
  ./internal/usecase/mqttsession ./internal/runtime/mqttsession -count=1
  -timeout=120s`: both passed, 15.149 s / 1.887 s;
  `/tmp/mqtt-retirement-recovery-scheduling.log`.
- `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-retirement-recovery-flow.log`. Generated index and whitespace pass.

Remaining next dependency: ordered consumer/current-authority retirement
production, including prevention of maintenance-only copy/anchor/retirement
feedback. Final binding removal, complete subscription/inbox projection,
permission ordering, delivery/ACK, owner recovery, Will execution, product
lifecycle/configuration, offline tools and process/load acceptance remain
required. The goal is active; product MQTT access remains unavailable.


## Bounded planning for maintenance-only replay tails

Planning previously suppressed only a lone latest anchor. A committed retirement
behind that anchor would make an idle source appear copyable again, enabling a
copy/anchor/retirement feedback loop once the producer is connected. The pinned
source/latest-anchor read now optionally verifies every position after the
accepted prefix through captured HW as a format-5 anchor or format-6 retirement.
Native entry identities, paired manifests and matching committed journals are
checked independently; original bodies, payload lookalikes, SyncOnce, speculative
shared-copy progress and later checkpoints cannot substitute for that proof.

At most 64 positions are examined. Longer tails conservatively remain copyable;
subsequent business starts copying immediately after the old accepted prefix and
includes every intervening control. No accepted frontier, source watermark,
retirement decision or storage encoding changes during planning. Workers/adapters
preserve the assertion. RPC 97 request v1 is unchanged; ordinary/error replies
stay v1, while a reply with MaintenanceOnly uses v2 plus a required explicit
marker and full request echo. Old readers reject it; matched replicas remain
required. RPC 99's nested request bytes are unchanged.

Contract/failure inventory and frozen context:
[mqtt-maintenance-tail-planning.md](../specs/mqtt-maintenance-tail-planning.md).

Validation (terminal passes):

- Missing fields produced RED before implementation in
  `/tmp/mqtt-maintenance-tail-red.log`. Boundary tests cover proof propagation,
  unanchored/oversized claims, v2 framing/marker/echo, eight idle coordinator
  visits without copying/anchoring, continued replica recovery, and later business.
- `GOWORK=off go test -p 2 -race ./pkg/db/message ./pkg/channel/...
  ./pkg/cluster/channels ./internal/usecase/mqttsession -count=1 -timeout=180s`:
  all passed; message 46.082 s, store 3.959 s, cluster channels 6.962 s,
  usecase 14.036 s; `/tmp/mqtt-maintenance-tail-race.log`.
- `GOWORK=off go test -p 2 -race -tags=integration ./pkg/db/message
  ./pkg/cluster/channels ./pkg/cluster ./internal/app -run
  '^(TestMQTTMaintenanceTail|TestMQTTRetirementRecoverySkipsPrunedBodiesAndResumesCleanup|TestMQTTAnchorThree|TestMQTTReplayRetention)'
  -count=1 -timeout=150s -v`: message 5.326 s, channels 2.268 s and cluster
  16.155 s passed; `/tmp/mqtt-maintenance-tail-integration.log`. The app selector
  matched no test and contributes no behavioral evidence. Real-disk cases cover
  historical/pending cuts, ordinary/SyncOnce/lookalike business, corrupt/missing
  journals/identities, 64/65-position bounds, original trim, reopen and backup
  restoration. Service storage adapters preserve the retirement-tail assertion;
  three TCP nodes exercise planning replies, restart, leader change and isolation.
- The actual app entry test was then run explicitly:
  `GOWORK=off go test -p 2 -race -tags=integration ./internal/app -run
  '^TestMQTTGroupSourcePreparationThreeNodeRecovery$' -count=1 -timeout=150s -v`.
  Passed in 13.132 s; `/tmp/mqtt-maintenance-tail-app.log`. It preserves ordered
  consumer-floor capture, ACK gaps, unknown-binding protection, fair automatic
  recovery, all-replica original-source release and stable-fence behavior.
  Consumer admission remains fixture-controlled and the product listener disabled.
- `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-maintenance-tail-flow.log`. Generated index and whitespace pass.

Next implement the typed current-authority retirement commit entry and connect
ordered consumer-floor capture plus bounded whole-anchor selection to it. The
producer must preserve exact retries, continue historical selection fairly, and
skip already committed equal/older retirement decisions. Full subscription/inbox
projection, removal/permission ordering, persistent delivery/ACK, owner recovery,
Will execution, product lifecycle/configuration, offline tools and process/load
acceptance remain required. The goal stays active; this prerequisite is not
producer or complete product acceptance.

## Typed native retirement commit admission

The native sequencer now exposes a bounded typed retirement request containing
current placement, captured/candidate whole-anchor proofs, the capped consumer
floor and server record identity. It checks the exact installed membership,
authority and write fence, checkpoints only its own HW, and independently reloads
both committed anchors and the latest retirement. The port trusts the product
caller to capture the anchor before reading fresh consumer obligations; request
validation alone does not prove that ordering. It is not a public consumer API.

Canonical source/Through commands reuse a matching or newer committed decision.
An uncertain pending retry retains its first payload and ID/timestamp even if
the observer supplies new values; other pending commands remain backpressured.
New controls use the existing format-6 majority path and success is reloaded
from the committed journal. This introduces no table or encoding changes and
never applies a replay baseline, deletes content or advances consumer metadata.

Contract/failure inventory and frozen context:
[mqtt-retirement-admission.md](../specs/mqtt-retirement-admission.md).

Validation (terminal passes):

- Missing typed contracts produced RED before implementation in
  `/tmp/mqtt-retirement-admission-red.log`.
- `GOWORK=off go test -p 2 -race -tags=integration ./pkg/channel
  ./pkg/channel/replication -run
  '^(TestMQTTRetirement|TestMQTTReplayAnchorQuorumRestartRecoveryAndLearner)'
  -count=1 -timeout=120s -v`: passed; native replication 2.768 s.
  `/tmp/mqtt-retirement-admission-focused.log`. Actual disk stores and exchange
  codecs, three voters and a non-voting learner verify independent proof and
  placement rejection, bounded admission, cancellation, partial quorum, original
  pending identity, eight concurrent retries, advancing decisions, delayed old
  requests, restart recovery and write-fence rejection. Copied suffixes remain
  readable after admission until explicit baseline application. Consumer/copy
  permission is fixture-controlled; this is not process-level product acceptance.
- `GOWORK=off go test -p 2 -race ./pkg/channel/... -count=1 -timeout=180s`:
  all nine packages passed. `/tmp/mqtt-retirement-admission-race.log`.
- `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-retirement-admission-flow.log`. Generated index and whitespace pass.

Next route this typed operation through the existing reactor append queue, then
fresh Slot/RPC admission and ordered consumer planning with bounded historical
selection. Product MQTT access and the full goal remain incomplete and active.

## Reactor-owned retirement admission

The Channel service now reserves the existing append mailbox and queues one
canonical typed retirement request. Queue/flush validation checks full recovered
authority, status, placement, write admission, captured HW and capable storage;
mixed control intents and changed payloads are rejected. Placement slices are
owned, included in queue byte accounting, and survive caller cancellation. No
blocking storage operation runs on a reactor goroutine.

One typed store-append task calls the native retirement committer. It is not
worker-batched with ordinary append records. Completion checks exact operation,
generation and proof association before applying monotonic control progress.
An already-started commit keeps progressing after observer cancellation or a
later guard rejection, while the caller gets no success proof. Historical retries
never insert the request ID into the recent-record cache. The existing append
cancellation, lifecycle and bounded worker cleanup paths remain shared.

Contract/failure inventory and frozen context:
[mqtt-retirement-reactor.md](../specs/mqtt-retirement-reactor.md).

Validation (terminal passes):

- RED before implementation: the service lacked the optional interface and the
  worker/reactor contracts were missing; `/tmp/mqtt-retirement-reactor-red.log`.
- `GOWORK=off go test -p 2 -race -tags=integration ./pkg/channel/service
  ./pkg/channel/reactor ./pkg/channel/worker -run '^TestMQTTRetirement'
  -count=1 -timeout=90s -v`: all passed; service 1.614 s, reactor 1.307 s,
  worker 1.370 s. `/tmp/mqtt-retirement-reactor-focused.log`. Actual disk service
  integration covers single-node-cluster ordering, eight concurrent retries,
  original identity, HW publication, full reopen, stale route/membership and
  cancellation after the worker starts. Consumer permission is controlled.
  Boundary cases additionally reject mixed/forged controls, future capture,
  incapable storage, wrong operation/generation/source/reference and malformed
  proof; guard/cancellation failures preserve real durable progress. Panic
  containment is checked at the existing pool boundary, where recovery lives,
  rather than incorrectly assuming direct Task.Run contains panics.
- `GOWORK=off go test -p 2 -race ./pkg/channel/... -count=1 -timeout=180s`:
  all nine packages passed; `/tmp/mqtt-retirement-reactor-race.log`.
- `GOWORK=off go test -p 2 -race -tags=integration ./pkg/channel/service
  ./pkg/channel/replication ./pkg/channel/reactor ./pkg/channel/worker
  -run '^TestMQTT' -count=1 -timeout=120s -v`: all passed; service 2.371 s,
  replication 4.031 s, reactor 1.605 s, worker 1.228 s.
  `/tmp/mqtt-retirement-reactor-integration.log`. Includes neighboring source,
  replay and anchor flows and the three-voter/learner native retirement scenario.
- `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-retirement-reactor-flow.log`. Generated index and whitespace pass.

Next add freshly fenced routed retirement/selector entry and connect the ordered
consumer planner, bounded historical continuation and background producer. Full
subscription/inbox projection, permission/removal ordering, delivery/ACK, owner
recovery, Will execution, product lifecycle/configuration, offline tooling and
process/load acceptance remain required. The goal is active; MQTT product access
is still unavailable.

## Routed retirement and bounded historical selection

Node foreground gates and fresh Slot reads now route retirement admission (RPC
100) and historical selection (RPC 101). The mutating request carries a full
placement identity and fixed serving leader; fresh server metadata supplies the
reactor request, ignoring caller lease/retention overlays. Both origin and server
recheck current authority, and a post-commit failure withholds its response.
Gateway replacement cannot recursively reroute an already addressed request.

Selection preserves the exact captured proof, capped floor and bounded cursor
in its request/reply association. It reads committed journals through the existing
four-slot donor-read budget, never publishes a checkpoint and always closes its
store lease. A stable migration fence permits selection; any fence change rejects
the reply. The caller must preserve the original capture/floor across a scan and
restart if its consumer plan changes. Selection grants no consumer permission.

Both version-1 RPCs use bounded closed envelopes, full request echoes, exact
proof association and typed errors. Matching nodes are required; unsupported peers
have no degraded fallback. No table or durable message format changes are made.
The product consumer planner/producer still must authorize these internal ports.

Contract/failure inventory and frozen context:
[mqtt-retirement-routing.md](../specs/mqtt-retirement-routing.md).

Validation (terminal passes):

- Missing contracts/interfaces/IDs produced RED before implementation in
  `/tmp/mqtt-retirement-routing-red.log`.
- `GOWORK=off go test -p 2 -race ./pkg/channel ./pkg/cluster/channels
  ./pkg/cluster/net ./pkg/cluster -run
  '^(TestMQTTRetirement|TestMQTTAnchorNodeForegroundGates)' -count=1
  -timeout=90s -v`: all passed; `/tmp/mqtt-retirement-routing-focused.log`.
  Cases cover before/after authority changes, unsupported capabilities, caller
  cancellation, wrong proofs, gateway swaps, Node gates, bounded admission,
  stable/renewed/cleared fences, lease cleanup including panic unwinding, exact
  RPC request/reply framing, all truncation cuts, typed errors and echo changes.
- `GOWORK=off go test -p 2 -race -tags=integration ./pkg/cluster -run
  '^TestMQTTAnchorThreeNodeRoutingRestartAndIsolation$' -count=1
  -timeout=150s -v`: passed, 19.465 s;
  `/tmp/mqtt-retirement-routing-integration.log`. Three real TCP Nodes with
  256 hash Slots and real disks scan one historical anchor per turn, rounding
  floor 3 down to prefix 2, then commit through the reactor/native quorum. Four
  concurrent retries reuse position 8; an advancing decision occupies position 9
  and old requests reuse it. Reopening the serving node preserves retirement and
  historical selection; losing Slot quorum rejects both operations. Consumer
  permission is fixture-controlled; the product MQTT listener remains disabled.
- `GOWORK=off go test -p 2 -race ./pkg/channel ./pkg/cluster/channels
  ./pkg/cluster/net ./pkg/cluster -count=1 -timeout=180s`: all passed; Channel
  1.234 s, channels 6.961 s, net 1.206 s, cluster 7.738 s.
  `/tmp/mqtt-retirement-routing-race.log`.
- `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-retirement-routing-flow.log`. Generated index and whitespace pass.

Next connect ordered consumer-floor planning, bounded historical continuation,
native retirement commit and replica recovery to the automatic producer. Final
binding removal, complete subscription/inbox projection, permission ordering,
delivery/ACK, unavailable-owner recovery, Will execution, product lifecycle,
offline tools and process/load acceptance remain required. The full goal remains
active; this routed capability does not complete MQTT product admission.

## Idle retirement commit propagation

The three-node retirement scenario exposed an idle-tail gap: native quorum commit
scheduled learners, while voting followers could retain the newest control with
their committed checkpoint still at the previous record. Without a later append,
their retirement reader could not expose the newest committed decision.

The routed serving leader now requires `CommittedReplicaRefresher` before local
admission, verifies the returned proof and fresh authority, requests existing
bounded native tail repair, and rechecks authority before success. Idempotent
retries request repair again. The sequencer supplies HW; caller proofs and
scheduling are never follower receipts. Missing capability and scheduling errors
fail explicitly without undoing already admitted durability. Remote origins do
not refresh their own logs. No table, wire or storage format changes are required.

Validation:

- Before the production fix, the real three-node test failed with `idle voter 1
  must learn the committed retirement`; `/tmp/mqtt-retirement-propagation-red.log`.
  Service failure cases also failed before code in
  `/tmp/mqtt-retirement-propagation-unit-red.log`.
- `GOWORK=off go test -p 2 -race ./pkg/cluster/channels -run
  '^TestMQTTRetirementRoute' -count=1 -timeout=60s -v`: passed, 1.532 s;
  `/tmp/mqtt-retirement-propagation-unit.log`. Includes missing capability,
  scheduling failure/retry, invalid proof, before/after authority changes,
  cancellation and remote-origin isolation.
- `GOWORK=off go test -p 2 -race -tags=integration ./pkg/cluster -run
  '^TestMQTTAnchorThreeNodeRoutingRestartAndIsolation$' -count=1
  -timeout=150s -v`: passed, 18.955 s;
  `/tmp/mqtt-retirement-propagation-integration.log`. Every voter independently
  reads the latest exact retirement proof without another business append or
  test-written checkpoint, before the existing restart/isolation checks.
- `GOWORK=off go test -p 2 -race ./pkg/cluster/channels ./pkg/cluster
  -count=1 -timeout=180s`: passed, 6.970/7.770 s;
  `/tmp/mqtt-retirement-propagation-race.log`.
- `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-retirement-propagation-flow.log`. Generated index unchanged.

The ordered consumer-floor producer, its bounded continuation and automatic
worker composition remain next. Full MQTT scope and product admission remain
unfinished; the active goal is unchanged.

## Ordered consumer retirement producer

`ReplayRetirement.Step` now joins the real ordered retention planner, bounded
historical selection and typed routed commit. Every turn captures accepted source
progress before fresh consumer permission. Unknown responsibility, no anchor,
stable write fences and exhausted selection yield without a new decision. A
verified candidate gets its ID from the shared server allocator and a bounded
timestamp; the returned durable proof must match the requested source/prefix.

Body-free continuation DTOs pin the original capture, conservative floor and
decreasing reverse cursor. A resumed turn always rereads permission. Increasing
consumer progress keeps the older safe floor, so continuing traffic cannot move
the target indefinitely; a lower floor or changed source/authority discards the
visit. Same-authority malformed/future hints fail, and storage independently
revalidates historical captures. Scheduling never proves local GC completion.

App composition uses fresh Slot planning and foreground Node selection/commit.
The three-node scenario first verifies fenced recovery, then explicitly clears
its fixture-owned migration fence before real ACK/progress-driven retirement.
Initial subscription/window admission is still controlled. The managed worker
has not yet been extended with retirement turns, and the product listener remains
unavailable. Frozen context and failure inventory:
[mqtt-retirement-production.md](../specs/mqtt-retirement-production.md).

Validation:

- Unit and app scenarios failed before production types/wiring existed:
  `/tmp/mqtt-retirement-producer-red.log` and
  `/tmp/mqtt-retirement-producer-app-red.log`.
- `GOWORK=off go test -p 2 -race ./internal/usecase/mqttsession -run
  '^TestReplayRetirement' -count=1 -timeout=60s -v`: passed, 1.571 s;
  `/tmp/mqtt-retirement-producer-unit.log`. Covers ordering, finite continuation
  with advancing latest/floors, floor decrease, changed authority/source, unknown
  bindings, fences, exhausted pages, invalid outcomes/proofs/IDs/time, dependency
  errors, cancellation and already-committed retry replies.
- `GOWORK=off go test -p 2 -race -tags=integration ./internal/app -run
  '^TestMQTTGroupSourcePreparationThreeNodeRecovery$' -count=1
  -timeout=120s -v`: passed, 12.695 s;
  `/tmp/mqtt-retirement-producer-app.log`. Three real TCP/disk Nodes with 256
  hash Slots prove real out-of-order ACK gaps prevent retirement; an unknown
  later registration still blocks after completion; fixing its fresh boundary
  permits exactly the accepted whole anchor. Another Node reuses the same proof.
- `GOWORK=off go test -p 2 -race ./internal/contracts/mqttsession
  ./internal/usecase/mqttsession ./internal/app -count=1 -timeout=180s`: passed;
  usecase 13.512 s, app 4.652 s, contracts compiled with no tests.
  `/tmp/mqtt-retirement-producer-race.log`.
- `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-retirement-producer-flow.log`. Generated index updated.

Next connect this producer and its finite continuation to the existing bounded
replay loop, rotating copy/recovery/retirement while preserving per-Slot bounds.
Final binding removal, complete subscription/inbox projection, permission
ordering, delivery/ACK entry, unavailable-owner recovery, Will execution,
product lifecycle/configuration, offline tools and process/load acceptance remain
required. This completes one producer capability, not the full active goal.

## Automatic retirement and repair after original-prefix trim

The existing replay worker now uses `ReplayMaintenance` to rotate bounded copy,
recovery and retirement. Mapping the original two-phase sequence preserves
replica/donor rotation. A reverse retirement continuation has its own body-free
cursor, pins capture/floor/source/authority/pass and must strictly decrease.
Each Slot still retains at most one continuation; durable work/errors yield.
Aggregate commit observations include idempotent decisions and never prove GC.

The real three-node scenario exposed a native prerequisite: after original
history was trimmed and a new authority installed, learner repair restarted at
position one and stayed there. Reopened voters had LEO/HW 8, while the learner
remained at 5 without the retirement decision. Native repair now uses one bounded
follower probe and an independent local identity read after an unavailable
fetch. Only an exact matching tail under an unchanged local frontier advances
the hint. Unknown, malformed, ahead or mismatched evidence cannot skip content.
An already-present final proposal still needs a committed checkpoint or replay;
the probe adds no authority and learners remain non-voting.

Validation (2026-09-24):

- Scheduler types/wiring first failed in
  `/tmp/mqtt-retirement-scheduling-red.log` and
  `/tmp/mqtt-retirement-scheduling-app-red.log`. The real-disk native regression
  failed before the repair change in `/tmp/mqtt-retirement-scheduling-native-red.log`;
  original app evidence is `/tmp/mqtt-retirement-scheduling-app-diag.log`.
- `GOWORK=off go test -p 2 -race -tags=integration ./pkg/channel/replication
  -run '^TestNativeLearnerCatchesUpUnderWriteFence$' -count=1 -timeout=30s -v`:
  passed, 1.874 s; `/tmp/mqtt-retirement-scheduling-native-green.log`. Corrupted
  tail identities block progress, matching tails resume, later commits arrive,
  and missing voter quorum still rejects writes. The fixture retries explicit
  incomplete convergence/read-proof results before asserting installation.
- `GOWORK=off go test -p 2 -race -tags=integration ./internal/app -run
  '^TestMQTTGroupSourcePreparationThreeNodeRecovery$' -count=1 -timeout=120s -v`:
  passed, 15.094 s; `/tmp/mqtt-retirement-scheduling-app-green.log`. Only managed
  workers produce the first decision. Real ACK gaps and unknown registrations
  block it; all three independently reopened stores contain the decision and
  refuse retired export while retaining current coverage. No verifier applies GC.
- `GOWORK=off go test -p 2 -race ./internal/contracts/mqttsession
  ./internal/usecase/mqttsession ./internal/runtime/mqttsession ./internal/app
  ./pkg/channel/replication -count=1 -timeout=180s`: passed; contracts compiled,
  usecase 14.013 s, runtime 1.661 s, app 4.477 s, replication 2.915 s;
  `/tmp/mqtt-retirement-scheduling-race.log`.
- `GOWORK=off go test -p 2 -race -tags=integration ./pkg/channel/replication
  ./internal/runtime/mqttsession -count=1 -timeout=180s`: passed, 5.412/3.038 s;
  `/tmp/mqtt-retirement-scheduling-integration.log`. Includes joined stop/restart,
  paged learner catch-up/promotion and MQTT control recovery.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-retirement-scheduling-flow.log`. Generated index unchanged.

Frozen context and failure inventory are in
[mqtt-retirement-scheduling.md](../specs/mqtt-retirement-scheduling.md).
No table/wire format is added in this slice. Physical compaction is not asserted.
Final binding removal, subscription/inbox projection, delivery/ACK entry, owner
recovery, Will execution, product lifecycle/configuration, offline tools and
process/load acceptance remain required. The full MQTT goal remains active.

Next-step inspection: final binding removal must preserve source cleanup
discoverability. The existing distinct-source scan derives work from non-Removed
binding retention entries; dropping the last binding also drops that scheduling
entry. Removal therefore needs both its separate revision-fenced source-release
acknowledgement and durable discovery of unfinished source cleanup. Do not treat
a Removed CAS or the absence of bindings as proof that every replica completed
retirement. No removal code or changed removal contract was added in this slice.

## Replay discovery survives the final consumer's departure

`MQTTReadReplaySources` adds closed read kind 17 to RPC 91. It seeks table 26
primary owner prefixes, including retained Removed tombstones, in one snapshot.
At most limit+1 (65) rows are decoded per page, independent of subscribers per
source. Encoded owner/generation order remains the cursor order. The existing
worker now requests kind 17; active-source kind 16 and strict retention index 4
keep their existing semantics. Thus removing consumer responsibility no longer
also removes the source's cleanup scheduling hint. No new table, index, row
encoding or backfill is required; older peers reject kind 17 and no fallback is
allowed. Frozen context and failure inventory:
[mqtt-tombstone-source-discovery.md](../specs/mqtt-tombstone-source-discovery.md).

Validation (2026-09-24):

- Before implementation, focused storage/RPC/worker tests failed on absent kind
  17 in `/tmp/mqtt-tombstone-discovery-red.log`. The real three-node managed-loop
  test then failed on `only tombstones remain: background discovery must still
  reach retirement`, 24.518 s; `/tmp/mqtt-tombstone-discovery-app-red.log`.
- `GOWORK=off go test -p 2 -race ./pkg/db/meta ./pkg/slot/proxy
  ./internal/runtime/mqttsession -run 'TestMQTTReplaySources|TestReplayWorker'
  -count=1 -timeout=90s -v`: passed, 1.981/2.147/1.499 s;
  `/tmp/mqtt-tombstone-discovery-unit.log`. Includes bounded prefix skipping,
  pinned snapshot, malformed primary key/value, cancellation, closed replies,
  preserved kind-16 semantics, continuation fairness and worker query selection.
- `GOWORK=off go test -p 2 -race -tags=integration ./internal/app -run
  '^TestMQTTReplayWorkerRetiresSourceWithOnlyTombstones$' -count=1
  -timeout=120s -v`: passed, 13.176 s;
  `/tmp/mqtt-tombstone-discovery-app-green.log`. With no index-4 consumer entries,
  only the real managed worker produces retirement. Independent reopened stores
  on all three replicas show the exact decision, applied retired baseline and
  current coverage; read-only historical export cannot return retired content.
- `GOWORK=off go test -p 2 -race -tags=integration ./pkg/cluster -run
  '^TestMQTTMetadataThreeNodeAuthorityAndRecovery$' -count=1 -timeout=120s -v`:
  passed, 17.358 s; `/tmp/mqtt-tombstone-discovery-cluster-final.log`. Removed
  sources remain discoverable after Slot transfer and process reconstruction;
  they are absent from consumer retention. An isolated leader rejects reads.
  The fixture now waits up to five seconds for the actual authoritative read
  after stopping a node; an earlier route observation does not prove current
  barrier readiness. Initial failures are preserved in
  `/tmp/mqtt-tombstone-discovery-cluster.log` and
  `/tmp/mqtt-tombstone-discovery-cluster-green.log`; no production routing changed.
- Related default race suites passed: metadata 27.175 s, Slot proxy 17.934 s,
  cluster 11.990 s, runtime 1.377 s, app 4.887 s;
  `/tmp/mqtt-tombstone-discovery-race.log`.
- Replay-worker joined lifecycle integration passed, 1.629 s;
  `/tmp/mqtt-tombstone-discovery-worker-integration.log`. Existing real ACK-gap,
  unknown-registration and fenced-learner app regression passed, 16.423 s;
  `/tmp/mqtt-tombstone-discovery-app-regression.log`.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-tombstone-discovery-flow.log`. Generated index unchanged.

Binding release permission is explicitly controlled in these fixtures. Formal
revision-fenced source acknowledgement/final removal, source deactivation,
activation interrupted before first binding and eventual safe tombstone pruning
remain lifecycle work. This slice does not claim those contracts or product
MQTT admission. Full subscription/inbox projection, delivery/ACK entry, owner
recovery, Will execution, product lifecycle/configuration, offline tooling and
process/load acceptance remain required; the full goal stays active.

## Source-owned consumer release and final binding removal

`SourceRemoval.Reconcile` now completes an already-Removing Channel binding.
Each turn reads current remote evidence and commits at most one exact source-Slot
CAS. Ended/replaced Session lifetimes need an explicit previously projected end
decision; normal completion requires closed admission and the exact fully drained
sealed cursor. A newer same-topic subscription generation proves old admission
closed without changing the new subscription. Offline/absent state, ACK gaps,
accounting past the seal and mismatched identities cannot release responsibility.

The first source-Slot commit acknowledges the binding revision while retaining
Removing and its recovery/retention indexes. A later independently validated turn
writes Removed and keeps the tombstone. Any intervening binding write invalidates
the acknowledgement; lost responses resume from committed state. This clarifies
the existing separate replicated per-consumer release contract: table 26 owns
that responsibility; no per-consumer record is added to the native Channel log.
Aggregate System 12 protection, copied-through and replay retirement are unchanged.
No table, column, command or wire encoding changes are required. See the frozen
context and failure inventory in [binding removal](../specs/mqtt-binding-removal.md).

Validation (2026-09-24):

- Initial usecase RED: `/tmp/mqtt-binding-removal-red.log`; normal-drain RED:
  `/tmp/mqtt-binding-removal-drain-red.log`; app-composition RED:
  `/tmp/mqtt-binding-removal-app-red.log`. Tests preceded each behavior/wiring slice.
- `GOWORK=off go test -p 2 -race ./internal/usecase/mqttsession
  -run 'TestSourceRemoval|TestSourceProgress' -count=1 -timeout=90s`: passed,
  13.903 s; `/tmp/mqtt-binding-removal-proof.log`. Covers lost acknowledgement/
  removal replies, ACK gaps, subscription replacement, revision invalidation,
  stale/missing/foreign proof, uncertain CAS receipts and cancellation/clock bounds.
- `GOWORK=off go test -p 2 -tags=integration ./internal/app -run
  TestMQTTGroupSourcePreparationThreeNodeRecovery -count=1 -timeout=120s -v`:
  passed, 14.784 s; `/tmp/mqtt-binding-removal-app-green.log`. Real TCP/disks and
  256 hash Slots verify the actual ended Session, source acknowledgement, retained
  consumer index before final commit, node-1-to-node-3 continuation without receipt
  handoff, independent authoritative tombstone read and preserved replay discovery.
  Existing independent message-store reopen/retirement coverage also passes.
- Related full default race suites: usecase 20.664 s and app 4.710 s;
  `/tmp/mqtt-binding-removal-race.log`. Darwin's existing linker warning appears;
  both suites pass without reported races.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-binding-removal-flow.log`. The generated index reflects app FLOW length.

The normal-removal seal is controlled in these focused fixtures. End capture,
unadmitted backlog release, source deactivation, activation interrupted before
first registration, and safe tombstone pruning still need their lifecycle
orchestration. Complete subscription/inbox projection, delivery/ACK entry,
owner recovery, Will execution, product lifecycle/configuration, offline tooling
and process/load acceptance remain required. Product MQTT is still unavailable;
the full implementation goal remains active.


## Unsubscribe sealing, quota release and cancelled preparation

`SourceDrain.Seal` now reads closed intent and its pinned Session/cursor under
an admitted current owner, seals the durable accounting end on the source Slot,
then uses the existing window advancement to release Pending-minus-Inflight
counts/bytes. Outstanding exchanges, PacketIDs, send order and ACK gaps remain.
Lost seal/window replies resume without resetting the start or subtracting quota
twice. A concurrent ACK invalidates the old Session CAS; the next turn retains
the seal and recomputes only the unadmitted remainder. A new same-topic generation
and its independent quota are untouched. Already-Drained tombstones complete a
retried unsubscribe instead of permanently returning conflict.

Interrupted preparation also cancels: an unknown start first requires fresh
replicated protection, while a missing unproven cursor receives explicit empty
cancellation initialization. Slot command 69 gains operation 3 (CancelInit),
which requires active exact Session ownership and closed/replaced subscription
intent. Ordinary Init/Account remain unchanged, existing cursors cannot reset,
and no backlog is added. Existing row/index/envelope formats are preserved; old
nodes reject op 3 and all participants must match. No new table is required.
Frozen context and failure inventory: [source drain](../specs/mqtt-source-drain.md).

Validation (2026-09-24):

- Usecase RED: `/tmp/mqtt-source-drain-red.log`; storage cancellation RED:
  `/tmp/mqtt-cursor-cancel-red.log`; cancellation composition RED:
  `/tmp/mqtt-source-drain-cancel-red.log`; app wiring RED:
  `/tmp/mqtt-source-drain-app-red.log`.
- Further failures before fixes exposed retrying an already-removed binding
  (`/tmp/mqtt-source-drain-retry-red.log`) and treating an equal newer closure
  revision as stale (`/tmp/mqtt-source-drain-replacement-red.log`). The initial
  cancellation run (`/tmp/mqtt-source-drain-cancel-green.log`) also rejected the
  protector port's generation by incorrectly borrowing native replay validation;
  validation now stays with the actual protector contract. Subsequent focused
  drain/removal tests passed, 8.462 s;
  `/tmp/mqtt-source-drain-replacement-green.log`.
- `GOWORK=off go test -p 2 -race -tags=integration ./internal/app -run
  TestMQTTGroupSourcePreparationThreeNodeRecovery -count=1 -timeout=120s -v`:
  passed, 20.880 s; `/tmp/mqtt-source-drain-app-final.log`. Real three-node TCP,
  disks and 256 hash Slots exercise actual unsubscribe/source sealing, remote
  cancellation Init, an ACK committed between seal and window release, stale-CAS
  rejection and successful retry. Remaining inflight references and quota are
  independently read through other nodes. Subscription establishment and window
  content admission remain explicitly controlled; this is not product delivery E2E.
- Full related default race suites passed: metadata 34.194 s, Slot FSM 25.975 s,
  Slot proxy 12.174 s, MQTT usecase 23.871 s, app 4.742 s;
  `/tmp/mqtt-source-drain-race.log`. Existing Darwin linker warnings appear;
  there are no reported races or failed tests.
- Named `flow-doc-contracts`: 86 compliant, zero invalid, nine existing warnings;
  `/tmp/mqtt-source-drain-flow.log`. Generated index reflects app FLOW length.

SourceDrain handles an existing exact Channel binding. Full projection must still
resolve/discover all group/inbox sources, fence preparation interrupted before its
first binding, schedule progress/drain/removal and handle source deactivation and
safe tombstone cleanup. Complete delivery/ACK entry, unavailable-owner recovery,
Will execution, product lifecycle/configuration, offline tooling and process/load
acceptance remain required. Product MQTT admission is unavailable; the full goal
remains active.


## Group cancellation before the first binding

`SourceDrain.SealGroup` now resolves the exact current closed group intent and
at most two cursors. Existing cursors identify the original source without
another protection call. If preparation stopped before both cursor and binding,
replicated protection resolves the source and a new unknown binding records the
original Preparing revision (`Subscription.Generation`), preserving the later
closure witness. The existing drain fixes the cancellation boundary and seals.
Current intent is rechecked before registration and after sealing; no receive
authorization is needed. Missing bindings behind initialized cursors, extra
cursors, foreign sources and changed intent fail without resetting progress.
No new schema, command, worker, subscriber scan or per-source cache is added.

Validation (2026-09-24):

- Tests preceded the implementation: missing `SealGroup` produced RED at
  `/tmp/mqtt-group-drain-red.log`; focused GREEN took 2.907 s at
  `/tmp/mqtt-group-drain-green.log`.
- Full related race suites passed: MQTT usecase 24.155 s and app 4.839 s,
  `/tmp/mqtt-group-drain-race.log`; existing Darwin linker warnings only.
- `GOWORK=off go test -p 2 -race -tags=integration ./internal/app -run
  TestMQTTGroupSourcePreparationThreeNodeRecovery -count=1 -timeout=120s -v`
  passed in 19.019 s; `/tmp/mqtt-group-drain-app.log`. Real three-node TCP/disk
  with 256 hash Slots verifies native protection with no first binding, uncertain
  cleanup registration, independent remote reads, owner transfer from 1 to 3,
  stale-owner rejection and successful cancellation. Existing concurrent-ACK and
  inflight-preservation coverage also passes. Establishment/window admission
  remain controlled; no product listener or product E2E claim is made.

Named `flow-doc-contracts` passed: 86 compliant, zero invalid, nine existing
warnings; `/tmp/mqtt-group-drain-flow.log`. Formatting and diff checks are clean.

Frozen source/rules and failure inventory are in
[group removal discovery](../specs/mqtt-group-removal-discovery.md).
Unattended ended-Session discovery before first registration, full group/inbox
establishment and shared-content readiness, source deactivation/pruning, delivery
and ACK entry, owner recovery, Will execution, product configuration/lifecycle,
offline tooling and process/load acceptance still remain. The full goal is active.


## Concrete group projection and shared-replay confirmation

`GroupProjection` implements the existing subscription projection port for group
intents. It validates the complete current request, derives protected preparation
through GroupSources, confirms shared replay, then rechecks exact intent and
permission before returning a receipt. Remove uses group drain discovery/sealing
without requiring receive permission or discarding outstanding exchanges. App
composition uses real foreground Node, fresh Slot metadata and source protection.
Inbox requests explicitly fail until their independent admission path exists.

`ReplayCoordinator.Confirm` captures active placement and an authoritative plan.
A missing/below-boundary anchor advances at most one ordinary bounded turn and
returns pending. A covered anchor (including a verified maintenance-only tail)
requires an independently complete recovery result from every configured replica,
including learners, for that exact full anchor. Partial imports, journal scans,
pending retirement and conflicting proofs cannot certify completion. Placement
is reread after all replies. No source-release flag, new GC authority, retry loop,
per-source cache, table or wire format is introduced; managed maintenance retains
responsibility for long recovery scans and scheduling fairness.

Validation (2026-09-24):

- Tests before implementation: replay confirmation RED at
  `/tmp/mqtt-projection-replay-red.log`, projection RED at
  `/tmp/mqtt-group-projection-red.log`, app composition RED at
  `/tmp/mqtt-group-projection-app-red.log`.
- Focused confirmation/projection tests passed, 1.590 s;
  `/tmp/mqtt-group-projection-green.log`. They cover incomplete learners,
  retirement cleanup, different anchors, initial/new fences, changed placement,
  cancellation/unavailability, preserved maintenance tails and exact-owner intent.
- Full related race suites passed: MQTT usecase 24.795 s, app 4.834 s;
  `/tmp/mqtt-group-projection-race.log`. Existing Darwin linker warnings only.
- `GOWORK=off go test -p 2 -race -tags=integration ./internal/app -run
  TestMQTTGroupSourcePreparationThreeNodeRecovery -count=1 -timeout=120s -v`
  passed, 20.150 s; `/tmp/mqtt-group-projection-app-retry.log`. The initial run
  returned `channel: not ready` before the test's expected replay-pending outcome;
  the harness now allows bounded initial retries while still requiring pending
  intent, an independent learner probe proving missing shared content, then real
  recovery and Active completion. It verifies owner transfer 1→3, an intervening
  native publication, unchanged original cursor and real projection removal.
  Unlike earlier establishment fixtures, this scenario has no fabricated
  projection receipt. Permission incarnation remains controlled and no product
  listener or product E2E acceptance is claimed.

Named `flow-doc-contracts` passed: 86 compliant, zero invalid, nine existing
warnings (`/tmp/mqtt-group-projection-flow.log`); formatting/diff checks are clean.

Frozen context and failure inventory: [group projection](../specs/mqtt-group-projection.md).
The complete goal remains active. Inbox/future-person admission, unattended
ended-Session discovery before registration, source deactivation/pruning, full
consumer accounting/delivery/PUBACK entry, unavailable-owner recovery, Will
execution, product lifecycle/configuration, offline tools and process/load
acceptance remain required.

## Anchored consumer pages through cluster routing

The storage consumer read now pins one snapshot, verifies an independently
committed anchor and local full-prefix endpoint, and returns a bounded page that
may stop before that anchor. Existing repair export still requires its exact
complete endpoint. Pages own immutable canonical content and cumulative proofs;
missing, pending, foreign, incomplete or corrupt evidence returns no page. Reads
do not mutate source protection, consumer progress, retirement or ordinary history.

The Channel adapter and foreground Node facade expose this contract through
RPC 102. Both origin and serving leader recheck fresh Slot placement and stable
write fences. Serving storage work has four dedicated slots without a waiting
queue and a five-second deadline; it never runs on a reactor goroutine. A distinct
versioned envelope echoes the exact anchor around the bounded replay codec.
Unknown peers fail explicitly. There is no new table, row codec or Slot command.

Validation (2026-09-24):

- Tests preceded storage, adapter, routing and Node implementations. RED logs:
  `/tmp/mqtt-consumer-read-red.log`, `/tmp/mqtt-consumer-adapter-red.log`,
  `/tmp/mqtt-consumer-routing-red.log`, `/tmp/mqtt-consumer-node-red.log` and
  `/tmp/mqtt-consumer-app-red.log`.
- Focused race tests across MessageDB, Channel store, cluster channels/transport
  and Node passed (`/tmp/mqtt-consumer-race.log`). The Channel-store filter matched
  no tests; the full suite below supplies its actual verification.
- Full race suites for `pkg/channel/store`, `pkg/cluster/channels` and
  `pkg/cluster/net` passed in 4.480 / 6.946 / 1.413 s;
  `/tmp/mqtt-consumer-ports-race.log`. Existing Darwin linker warnings only.
- `GOWORK=off go test -race -tags=integration -p 2 ./internal/app -run
  '^TestMQTTGroupSourcePreparationThreeNodeRecovery$' -count=1` passed in
  21.161 s (`/tmp/mqtt-consumer-app.log`). After physical original-prefix trim on
  all three replicas, two origins remotely read the same short page under a stable
  migration fence, continue through the next publication, verify caller byte
  ownership and reject an absent anchor. Existing projection/removal/reopen checks
  also pass. This is real TCP/disk runtime integration, not product process E2E.
- Named `flow-doc-contracts` passed: 86 compliant, zero invalid, nine existing
  warnings (`/tmp/mqtt-consumer-flow.log`). Formatting and diff checks passed.

Frozen context and failure inventory: [consumer read contract](../specs/mqtt-anchored-consumer-reads.md).
The full goal remains active. Typed content interpretation, subscription
qualification/accounting, window admission and delivery/PUBACK still require
implementation; inbox/future-person admission, ended-Session discovery, source
deactivation, owner recovery, Will execution, product lifecycle, offline tools
and process/load acceptance are also outstanding.

## Typed consumer messages and native control classification

Consumer Node/RPC reads now return original Message values, immutable content
references and cumulative proofs. Storage pins the anchor, canonical shared rows
and each retained committed entry/paired proposal in one snapshot. Explicit
native formats 4/5/6 identify internal controls; a payload lookalike or ordinary
SyncOnce message does not. Missing or inconsistent proof returns no partial page.
The Channel adapter transfers strictly decoded fields and owned payload/metadata
without using ordinary history's opaque compatibility fallback.

RPC 102 v2 carries typed messages once using frozen message codec 11, exact request
association, bounded canonical-content counters and a total reply cap. Old v1
requests/replies fail explicitly. Generic copy/repair transfer formats, durable
rows and Slot commands remain unchanged. No permission, expiry/QoS qualification,
Session accounting, delivery or GC authority is inferred from these reads.

Validation (2026-09-24):

- Tests preceded the storage, adapter and routed changes. RED artifacts:
  `/tmp/mqtt-typed-storage-red.log`, `/tmp/mqtt-typed-adapter-red.log`,
  `/tmp/mqtt-typed-routing-red.log`, `/tmp/mqtt-typed-app-red.log`.
- Storage tests passed, 4.045 s (`/tmp/mqtt-typed-storage-green.log`): original
  fields/metadata, controls and lookalikes, missing/foreign native proof, trim,
  receiver reopen and caller byte ownership. Routed/adapter focused tests passed
  (`/tmp/mqtt-typed-routing-green.log`), including every reply truncation, typed
  validation failures, old envelopes, exact error status and owned decoded bytes.
- Focused MQTT race suites passed for MessageDB and Node, 40.237 / 1.733 s
  (`/tmp/mqtt-typed-storage-node-race.log`). Full Channel-store and cluster-channel
  race suites passed, 6.000 / 7.055 s (`/tmp/mqtt-typed-ports-race.log`). Only the
  existing Darwin linker warnings were observed.
- Three-node race integration passed in 21.150 s (`/tmp/mqtt-typed-app.log`):
  `GOWORK=off go test -race -tags=integration -p 2 ./internal/app -run
  '^TestMQTTGroupSourcePreparationThreeNodeRecovery$' -count=1 -timeout=120s`.
  Two origins read typed messages after physical original trim under the stable
  migration fence; actual payload/sender, pagination, ownership and missing-anchor
  rejection are checked alongside existing projection/removal/reopen coverage.
- The nested message codec was then explicitly pinned to its unchanged value 11;
  focused RPC tests passed again (`/tmp/mqtt-typed-rpc-final.log`).
- Named `flow-doc-contracts` passed: 86 compliant, zero invalid, nine existing
  warnings (`/tmp/mqtt-typed-flow.log`). Formatting/diff checks passed.

Frozen context/failure inventory: [typed consumer content](../specs/mqtt-typed-consumer-content.md).
This is runtime/storage integration, not product process E2E. Qualification and
accounting, delivery/PUBACK, inbox and future-person admission, unattended ended
Session discovery, source deactivation, unavailable-owner recovery, Will execution,
product lifecycle, offline tools and process/load acceptance remain required.

## Exact outbound acknowledgement orchestration

`Acknowledgements` accepts the caller-captured cursor/PacketID/DeliveryOrder under
one exact current local Owner. It reads current Session/inflight through Node and
performs at most one existing command-70 ACK with the captured parent revision.
It validates complete read shape, owner/UID/clock and the committed reply. Ordinary
unsubscribe or receive denial cannot prevent completing an admitted exchange;
Session termination and ownership loss still fence it. Missing exchanges return
an explicit absent observation, with no mutation or claim of network delivery.
A different key/order conflicts instead of releasing a reused PacketID.

App composition supplies foreground Node and the same local Owners registry.
The three-node progress scenario now uses this real acknowledgement usecase for
out-of-order completion and duplicate ACKs; source progress/retention still verifies
the resulting gap independently. Admission and accounting remain controlled.
This does not connect product PUBACK dispatch or prove packet-to-send binding.

Validation (2026-09-24):

- Tests preceded implementation: `/tmp/mqtt-ack-red.log` and app composition RED
  `/tmp/mqtt-ack-app-red.log`. The first runtime test attempt exposed a fixture
  missing its subscription identifier during controlled window admission. After
  matching that existing storage contract, focused tests passed in 3.749 s
  (`/tmp/mqtt-ack-green.log`); no production window rule was relaxed.
- Public usecase coverage includes ordinary unsubscribe, receive denial, ACK gaps,
  lost replies, duplicates, wrong source/order/packet, stale/expired owners,
  malformed/partial reads, callback panic, revision races, invalid receipts and
  cancellation after commit. Failures return no success; mutations retain their
  committed state when only reply observation fails.
- Full MQTT-usecase and app race suites passed, 28.676 / 4.751 s
  (`/tmp/mqtt-ack-race.log`); existing Darwin linker warnings only.
- `GOWORK=off go test -race -tags=integration -p 2 ./internal/app -run
  '^TestMQTTGroupSourcePreparationThreeNodeRecovery$' -count=1 -timeout=120s`
  passed in 20.130 s (`/tmp/mqtt-ack-app.log`). Real three-node TCP/disk with
  256 hash Slots validates exact-order rejection, duplicate absence, gap-safe
  completion and independent cross-node source progress/retirement checks.
- Named `flow-doc-contracts` passed: 86 compliant, zero invalid, nine existing
  warnings (`/tmp/mqtt-ack-flow.log`); formatting and diff checks passed.

Frozen context and failure inventory: [outbound acknowledgements](../specs/mqtt-outbound-acknowledgements.md).
The full goal remains active. Entry send binding/PUBACK dispatch, actual durable
window admission and consumer qualification/accounting, reconnect delivery, inbox
and future-person admission, ended-Session discovery, source deactivation, owner
recovery, Will execution, product lifecycle, offline tools and process/load
acceptance remain outstanding.

## Outbound gateway exchange binding

The internal Handler now sends trusted admitted QoS 1 exchanges through the
existing serialized gateway writer. It retains only cursor/PacketID/DeliveryOrder
identities, bounded by Receive Maximum and 1024. A nonblocking send gate preserves
order with no new queue/worker; a connection cannot resend an attempted order.
PUBACK captures the exact binding before the real acknowledgement usecase.
Negative reasons complete; unknown/duplicate IDs cannot create additional credit.
Failed writes, uncertain ACKs and callbacks close/fence instead of discarding the
persistent exchange. Original ordered properties, server identities and remaining
expiry are mapped without sharing payload storage or truncating content.

Pre-code failure inventory/frozen source digests are in
[mqtt-outbound-gateway.md](../specs/mqtt-outbound-gateway.md). Public entry tests
started RED for missing types/methods. The real Paho/gnet single-node cluster test
now retains an unacknowledged exchange across physical connection takeover,
checks the same PacketID/body/properties with DUP, and independently observes
Slot state after the client's manual PUBACK. The initial test setup used an
incorrect subscription generation; matching the parent revision contract fixed
that fixture. Subscription/accounting/content-reference admission stays controlled;
this is internal integration, not product process E2E or source-proof acceptance.

Validation (2026-09-24):

- `GOWORK=off go test -p 2 ./internal/access/mqtt -count=1` passed (0.308s).
- `GOWORK=off go test -race -p 2 ./internal/access/mqtt ./internal/usecase/mqttsession ./internal/app -count=1`
  passed (1.724s / 32.285s / 4.814s).
- `GOWORK=off go test -race -tags=integration -p 2 ./internal/app -run '^TestMQTTGatewayPahoSingleNodeCluster$' -count=1 -timeout=90s`
  passed (6.172s); only the existing Darwin linker warning appeared.
- `flow-doc-contracts` passed: 86 compliant, zero invalid, nine existing length warnings; `git diff --check` passed.
- Product listener, autonomous delivery/recovery and capacity acceptance remain pending.

## Qualified backlog range receipts

[Qualified accounting](../specs/mqtt-qualified-accounting.md) extends the existing
cursor table with System-1 receipts and optional columns 25–27. Each positive
receipt covers at most 256 source positions and owns sorted charged position/byte
pairs; empty coverage adds no receipt. Command 69 operation 4 fences the exact
subscription revision, appends the chain and updates Session/cursor quotas in one
Slot commit. Version 1 prohibits legacy accounting; upgrade cannot guess existing
unadmitted backlog. No payload or per-message inflight row is duplicated.

Window admission selects the first outstanding charge and exact bytes. Advance
debits one head prefix and checks the successor before unlinking; ACK retains the
existing frozen exchange/gap contract. Read kind 18 pins Session/cursor/head and
rejects missing, inconsistent or unrelated state. SourceDrain releases one range
per turn; explicit pending preserves the fixed end and Removing intent. Matched
writers/tools are required, and rollback needs a pre-feature backup. MQTT JSONL
transfer remains outstanding.

Validation (2026-09-24):

- The pre-implementation failures are `/tmp/mqtt-qualified-red.log` and
  `/tmp/mqtt-qualified-routing-red.log`; focused storage/FSM/proxy/usecase tests
  passed after correcting the initially omitted optional-column encoder.
- Additional corruption tests failed before the corresponding guards were added
  (`/tmp/mqtt-qualified-corruption-red.log`): partial optional tuples, headless
  byte debt, and successor count/bytes/revision/time inconsistency. The guards
  now reject these before any progress. The old unknown-column compatibility
  fixture moved from newly allocated column 25 to still-unknown column 28.
  Focused tests passed (`/tmp/mqtt-qualified-corruption-green.log`).
- `GOWORK=off go test -race -p 2 ./pkg/db/meta ./pkg/slot/fsm ./pkg/slot/proxy ./internal/usecase/mqttsession -count=1`
  passed (29.728s / 16.203s / 14.290s / 33.921s), logged in
  `/tmp/mqtt-qualified-race.log`.
- `GOWORK=off go test -race -tags=integration -p 2 ./pkg/cluster ./internal/app -run '^(TestMQTTMetadataThreeNodeAuthorityAndRecovery|TestMQTTGroupSourcePreparationThreeNodeRecovery)$' -count=1 -timeout=150s`
  passed (16.322s / 21.029s), logged in `/tmp/mqtt-qualified-integration.log`.
  Real three-node TCP/disk and 256 hash Slots verify linked/empty coverage,
  exact retry, quota-ending preservation, leader transfer, disk reconstruction
  and both receipts consumed through the restarted facade. The existing source
  preparation/unsubscribe/recovery integration also passes. Only existing Darwin
  linker warnings appeared. Qualification inputs remain controlled fixtures.
- FSM snapshot/restore retains the auxiliary state; a neighboring logical failure
  leaves no partial receipt. Malformed head/tail and incorrect debit tests fail
  closed, and ordinary unsubscribe preserves inflight ACK gaps.
- Named `flow-doc-contracts` passed: 86 compliant, zero invalid and nine existing
  length warnings (`/tmp/mqtt-qualified-flow.log`); formatting and diff checks passed.

Source qualification/accounting orchestration (online and offline), durable
window admission, autonomous reconnect/delivery and current receive permission
are still required. Inbox/future-person source admission, unattended ended-Session
discovery, source deactivation, unavailable-owner isolation/recovery, Will
execution, product listener/restore composition, offline MQTT tools, metrics and
process/load acceptance remain outstanding. Product MQTT is still disabled and
no process-level E2E or capacity result is claimed.

## Consumer accounting from original messages

[Accounting](../specs/mqtt-consumer-accounting.md) now computes a bounded source
page's charges for both online and offline Sessions. Its public input is the exact
cursor identity; Session/owner/subscription/source evidence and original content
come from authoritative ports. It checks coherent parent/child state, active source
binding, fresh placement/committed anchor and current receive permission, then
uses command 69 operation 4 with all captured fences. It neither creates inflight
exchanges nor depends on available window capacity.

Native persistent messages default to QoS 1; MQTT/Will preserve publication QoS.
No Local compares publisher namespace/ClientID, while native messages from the
same UID remain eligible. Internal classification comes from the typed reader,
not SyncOnce. MQTT, Will and native expiry retain their original clock; expired,
internal and effective QoS 0 positions advance coverage without debt. One fixed
evaluation time is recorded, with clock regression and lifetime deadline checks
before the proposal. Quota termination retains debt and returns the exact observed
owner for lifecycle cleanup; this is not an isolation proof. There is no worker,
network send, per-consumer cache or implicit retry.

App composition uses fresh SlotMetaSource and foreground Node ports. The real
three-node original-trim/consumer-progress scenario now calls this accounting
usecase instead of injecting counts. It independently reads anchored content for
exact bytes/hash/identity during controlled window admission, then exercises the
actual acknowledgement usecase, ACK gaps and cross-Slot retention. Subscription
activation/window admission and authorization-version fixtures remain controlled;
product subscription/delivery scheduling is not represented by this test.

Validation (2026-09-24):

- Public usecase and app tests preceded implementation and failed for missing
  APIs (`/tmp/mqtt-accounting-usecase-red.log`, `/tmp/mqtt-accounting-app-red.log`).
  Focused usecase tests passed in 3.649s. Online/offline qualification, original
  expiry/QoS/No Local, missing/corrupt source proofs, options/owner/permission
  races, canceled/panicking dependencies, lost replies, zero-charge coverage and
  quota-ending results are covered using the real metadata commit seam.
- Full usecase/app race suites passed (37.366s / 4.925s) in
  `/tmp/mqtt-accounting-usecase-race.log`.
- A further failure-first clock guard rejects evaluation/proposal regression
  within the same turn (`/tmp/mqtt-accounting-clock-red.log` then GREEN); a
  failure-first aggregate guard rejects cursor debt above Session totals before
  returning an idle result or attempting a proposal (`/tmp/mqtt-accounting-counter-red.log`).
  After both guards, all `TestAccounting` cases passed with `-race` in 5.103s
  (`/tmp/mqtt-accounting-counter-green.log`).
- `GOWORK=off go test -race -tags=integration -p 2 ./internal/usecase/mqttsession ./internal/app -run '^(TestAccounting.*|TestMQTTGroupSourcePreparationThreeNodeRecovery)$' -count=1 -timeout=150s`
  passed (4.224s / 19.537s), `/tmp/mqtt-accounting-final.log`, with real TCP/disk,
  three nodes and 256 hash Slots. Original physical trim, shared replay recovery,
  actual accounting and subsequent ACK/source progress all pass. Existing Darwin
  linker warnings only.
- Named `flow-doc-contracts` passes: 86 compliant, zero invalid, nine existing
  length warnings (`/tmp/mqtt-accounting-flow.log`).

The full goal remains active. Discovery/fair scheduling and quota-owner cleanup,
actual durable window admission, send-time permission/revocation ordering,
autonomous initial/reconnect/QoS 0 delivery, inbox/future-person admission,
unattended ended-Session cleanup/source deactivation, unavailable-owner isolation,
Will execution, product/restore lifecycle, MQTT offline tools, metrics and complete
process/load acceptance remain outstanding. No product listener or capacity claim.


## Window admission from original messages

[WindowAdmission](../specs/mqtt-window-admission.md) now prepares the next action
from authoritative Session/cursor/accounting-head, active subscription and source
binding under an exact Owners scope. It reads bounded anchored original content,
rechecks placement/current receive permission, and uses current new-delivery
QoS/No Local/expiry. Original charge receipts determine every debit. Each turn
consumes at most one receipt; skipped prefixes preserve existing ACK gaps, and
option changes cannot resurrect previously uncharged QoS 1 messages.

QoS 1 admission commits through command 70 and then point-reads the exact durable
PacketID/order/content reference before returning a prepared delivery. Full is
flow control; lost/malformed replies, invalid readback or owner changes expose no
packet. Existing exchanges are never expired or ACKed by preparation. QoS 0
returns an uncommitted candidate with a private captured owner/revision/debit;
CompleteQoS0 is reserved for a trusted sender after successful enqueue. Public
presentation fields cannot redirect completion; repeated or changed-parent
completion fails without rebasing. Subscription downgrade retains old charges
until this completion. No schema, command or RPC format changed.

App composition uses the real Node and Owners ports. Three-node coverage after
original physical trim now uses both actual accounting and actual window
preparation, independently compares returned original content, and completes
through the exact acknowledgement usecase before checking cross-Slot retention.
Subscription activation and the permission-version fixture remain controlled;
this test does not perform autonomous gateway delivery or product-level E2E.

Validation (2026-09-24):

- The failure inventory and public usecase/app tests preceded implementation.
  Missing-API RED evidence: `/tmp/mqtt-window-red.log` and
  `/tmp/mqtt-window-app-red.log`. The first run additionally caught a test fixture
  missing the required SyncOnce flag on an explicitly internal control; that
  fixture was corrected, without weakening the typed-reader validation.
- Full `GOWORK=off go test -race -p 2 ./internal/usecase/mqttsession ./internal/app -count=1`
  passed (51.245s / 5.530s), `/tmp/mqtt-window-race.log`. Cases cover original
  identity and bytes, expired/No Local/internal skips, original charge debit,
  QoS downgrade, forged/repeated/stale completion, receipt boundaries, full
  windows, malformed/missing evidence, changed permissions/options/placement,
  clock failures, cancellation, callback panic and ambiguous commits/readback.
- `GOWORK=off go test -race -tags=integration -p 2 ./internal/app -run '^TestMQTTGroupSourcePreparationThreeNodeRecovery$' -count=1 -timeout=150s`
  passed (21.886s), `/tmp/mqtt-window-integration.log`, using real TCP/disk,
  three nodes and 256 hash Slots. Existing Darwin linker warnings only.
- Named `flow-doc-contracts` and formatting/diff validation are recorded in
  `/tmp/mqtt-window-flow.log`; the final check has 86 compliant files, zero invalid
  and nine existing length warnings.

Prepared delivery remains separate from network admission. The full goal still
requires send-time permission/revocation ordering, old-exchange recovery before
new delivery, QoS 0 gateway enqueue, autonomous initial/reconnect/accounting
scheduling and quota-owner cleanup. Inbox/future-person sources, unattended
ended-Session cleanup/source deactivation, unavailable-owner isolation, Will
execution, product/restore lifecycle, offline MQTT tools, metrics and complete
process/load acceptance also remain outstanding. The product listener stays
unwired; no capacity or complete-product claim is made.


## QoS 0 gateway enqueue and bounded property headroom

[QoS 0 gateway](../specs/mqtt-qos0-gateway.md) now accepts a trusted prepared
original for the exact open owner and enqueues through the common gateway writer.
It shares the QoS 1 nonblocking sending gate but creates no PacketID, ACK binding
or durable exchange, and consumes no Receive Maximum credit. The trusted sender
still owns preparation serialization, current receive permission and private
CompleteQoS0 after successful enqueue. Error/panic/cancellation across writing
fences/closes without retry or invented completion; expired new candidates yield
without writing or closing an otherwise live connection.

QoS 0/1 share original-content mapping, ordered property preservation and server
identity generation. Native, MQTT and Will expiry retain their original clocks;
the earlier native/publication deadline is forwarded. Begun QoS 1 remains valid
with remaining interval zero; QoS 0 cannot start after expiry. Explicit QoS 1
redelivery now additionally requires SessionPresent on the connection.

The generic MQTT adapter gains independent outbound codec limits while its
existing New constructor remains symmetric. App composition reserves 136 output
properties and 64 KiB property bytes for original metadata plus bounded server
fields, preserving the inbound settings and peer packet-size cap. No truncation,
new schema or wire format is introduced. This addresses the previously identified
case where server attributes pushed a fully accepted input above the output count.

Validation (2026-09-24):

- Failure inventory, public entry, generic adapter and app tests preceded code;
  missing-API RED logs: `/tmp/mqtt-qos0-red.log`, `/tmp/mqtt-qos0-app-red.log`.
  A helper name initially collided with the existing inbound mapper; it was
  renamed before successful validation.
- `GOWORK=off go test -race -p 2 ./internal/access/mqtt ./pkg/gateway/protocol/mqtt ./internal/app -count=1`
  passed (1.733s / 1.943s / 6.304s), `/tmp/mqtt-qos0-race.log`. Coverage includes
  mixed send reentrancy, full credit, absent ACK composition for QoS 0, original
  and downgraded QoS, native/Will/deadline combination, expiry/clock errors,
  foreign/malformed candidates, canceled/fenced owners, callback failures,
  independent codec limits and unchanged peer caps.
- `GOWORK=off go test -race -tags=integration -p 2 ./internal/app -run '^(TestMQTTGatewayPahoSingleNodeCluster|TestMQTTProtocolReservesBoundedOutboundPropertyHeadroom)$' -count=1 -timeout=120s`
  passed (6.420s), `/tmp/mqtt-qos0-integration.log`. Real Paho/TCP/gnet and a
  single-node cluster with 256 hash Slots verify a 128-property input, preserved
  133 outbound user properties, QoS 0 while QoS 1 credit is occupied, unchanged
  durable debt/exchange counts, takeover/DUP identity and authoritative PUBACK.
  Outbound source/window admission remains explicitly controlled in this test.
- Existing Darwin linker warnings only. Named `flow-doc-contracts` results are
  recorded in `/tmp/mqtt-qos0-flow.log`: 86 compliant, zero invalid and nine
  existing length warnings; formatting/diff checks are clean.

Full implementation remains active. Autonomous sender/current-permission ordering,
reconnect-first exchange recovery, private QoS 0 completion composition and fair
accounting/delivery scheduling remain required. The previously recorded inbox,
source cleanup, unavailable-owner isolation, Will execution, product/restore,
offline tools, metrics and process/load acceptance work is also still required.
Product MQTT is not enabled; these tests do not establish process-level acceptance
or capacity.

## Existing-exchange recovery preparation

[ExchangeRecovery](../specs/mqtt-exchange-recovery.md) now reads one existing
exchange in DeliveryOrder under exact Owners admission. Its authoritative pinned
Session/cursor and retained active/removing source binding prove original
responsibility and permission incarnation. Current subscription rows/options are
not used to rewrite a begun exchange, so ordinary unsubscribe and a replacement
subscription generation preserve PacketID, QoS and SubscriptionIdentifier.
Current receive permission still must match the original authorization version.

A shared private anchored-original reader now serves WindowAdmission and recovery.
It captures the committed anchor, reads a bounded exact source range and rechecks
placement. Recovery compares immutable ID/sequence/version/hash/bytes, rechecks
permission and point-reads the exact exchange before returning. Unrelated ACKs may
update links; target disappearance, new admission, foreign ownership or changed
identity yields without caller-cursor progress. No recovery preparation writes
ACK/window/progress, expires an exchange or interprets Receive Maximum as deletion
permission. Empty results also require current clock/owner evidence.

App composition uses fresh Node/SlotMetaSource and Owners ports. The real
three-node physical-trim test now independently compares original content during
accounting, window admission and existing-exchange recovery before exercising ACK
gaps and retention. It remains an internal composition test with controlled
subscription activation; no autonomous sender or product listener is represented.

Validation (2026-09-24):

- The failure inventory and public usecase/app tests preceded code. Missing-API
  RED: `/tmp/mqtt-exchange-recovery-red.log` and
  `/tmp/mqtt-exchange-recovery-app-red.log`. The concurrent-admission fixture uses
  actual WindowAdmission, since lifecycle Session CAS correctly refuses direct
  allocator changes.
- Full usecase/app race suites passed (51.635s / 5.357s),
  `/tmp/mqtt-exchange-recovery-race.log`, before the final two evidence guards.
- Empty-result clock regression and contradictory readback debt each failed
  before their corresponding guards were added:
  `/tmp/mqtt-exchange-recovery-guards-red.log` and
  `/tmp/mqtt-exchange-recovery-debt-red.log`.
- Final `GOWORK=off go test -race -tags=integration -p 2 ./internal/usecase/mqttsession ./internal/app -run '^(TestExchangeRecovery.*|TestWindowAdmission.*|TestMQTTGroupSourcePreparationThreeNodeRecovery)$' -count=1 -timeout=150s`
  passed (11.075s / 19.969s), `/tmp/mqtt-exchange-recovery-final.log`. The final
  source covers actual metadata commits, unsubscribe sealing/replacement,
  connection takeover, expired begun exchange retention, ACK/new-admission races,
  corrupt/foreign/missing proofs, current permission/placement changes,
  cancellation/panic/clock failures and real TCP/disk three-node original trim.
- Named `flow-doc-contracts` passed with 86 compliant files, zero invalid and
  nine existing length warnings (`/tmp/mqtt-exchange-recovery-flow.log`). Only
  existing Darwin linker warnings appeared; formatting/diff checks are clean.

The goal remains active. This read-only preparation does not establish complete
connection recovery or grant network sending. The sender must serialize old
exchange recovery before new admission, enforce final current receive permission,
apply gateway credit/order, and complete QoS 0 after enqueue. Autonomous scheduling,
inbox/future-person sources, unattended cleanup/source deactivation, unavailable
owner isolation, Will execution, product/restore composition, offline tools,
metrics and full process/load acceptance remain required. No product MQTT listener
or capacity result is claimed.

## Original QoS 0 at-most-once correction

The [preclaim contract](../specs/mqtt-qos0-preclaim.md) supersedes the earlier
all-QoS-0 post-enqueue completion description in this report. Inspection of the
sender crash window showed that enqueue followed by cursor completion could
repeat original QoS 0 on takeover. Original MQTT/Will QoS 0 now consumes its
uncharged source position before returning a candidate. Only an Applied receipt
exposes one attempt; unchanged, lost, malformed, canceled or panicking replies
cannot expose another candidate. Loss between claim and enqueue is permitted.
Original QoS 0 with a positive accounting charge is rejected as invalid evidence.

The private completion token records the preclaim. CompleteQoS0 verifies ownership
and the committed revision, then returns unchanged without a second mutation;
unrelated ACK/renewal/option revisions do not invalidate this no-op. Original QoS 1
downgraded to QoS 0 still retains its captured revision and charges until enqueue.
This distinction follows [OASIS MQTT 5.0](https://docs.oasis-open.org/mqtt/mqtt/v5.0/os/mqtt-v5.0-os.html),
sections 4.3.1 and 3.8.4: original QoS 0 is at most once, while QoS 1 delivered at
QoS 0 may duplicate. No table, field encoding, command or RPC format changed.

Validation (2026-09-24):

- Failure inventory and changed expectations preceded implementation. RED evidence
  is `/tmp/mqtt-qos0-preclaim-red.log`, including both MQTT/Will takeover without
  completion, ambiguous claim outcomes, unrelated parent revision and contradictory
  positive charges.
- Full `GOWORK=off go test -race -p 2 ./internal/usecase/mqttsession -count=1`
  passed (60.309s), `/tmp/mqtt-qos0-preclaim-race.log`. Tests use actual metadata
  commits and live Owners, including actual Connect takeover; source/placement
  ports remain controlled fixtures. They do not simulate product process crashes
  or establish network delivery. Existing Darwin linker warning only.
- Named `flow-doc-contracts` passed with 86 compliant files, zero invalid and nine
  existing length warnings (`/tmp/mqtt-qos0-preclaim-flow.log`). Frozen context
  digests were verified against `db1f9bccdf57413e81ccbf8616fbb280c7bcf563`;
  formatting and diff checks passed.

The full implementation goal remains active. The correction is a prerequisite for
sender composition, not a completed sender. Final receive-permission admission,
explicit revocation ending, reconnect-before-new ordering, downgraded-QoS-0
completion reconciliation, fair scheduling and all previously recorded product,
source, Will, offline-tool and process/load acceptance work remain outstanding.

## Exact-owner explicit Session ending

[App.End](../specs/mqtt-session-ending.md) accepts a trusted end decision, reads
current Session authority, proves the exact owner's transport/execution quiescence,
and rereads before committing the existing atomic lifecycle End command. Ended
and offline rows still require isolation; they are not evidence that a socket
stopped. A stale request cannot follow a successor. Repeated ending preserves the
first reason and detached/cancelled Will work; the next connection receives a new
lifetime with Session Present=false while the ClientID stays UID-bound.

Ending preserves delivery counters, exchange identities, allocators and source
responsibility for verified cleanup. Any live Will becomes ready atomically with
ending, but no publication is performed. Observation is captured before isolation;
an already expired active owner first records its original abnormal disconnect.
This bounded two-command path retains the original Will clocks rather than
restarting them at delayed observation/cleanup. Uncertain replies stop the turn;
retries re-read authority. No table, schema, command or RPC format changed.

Validation (2026-09-24):

- Failure inventory, public lifecycle tests and the app integration extension
  preceded implementation. Missing-API RED evidence is in `/tmp/mqtt-end-red.log`
  and `/tmp/mqtt-end-app-red.log`.
- The first focused run exposed a test expectation that retained the first
  exchange's links from before the second admission. The fixture now captures the
  actual linked exchanges immediately before End, then proves they are unchanged.
  No product behavior was weakened to satisfy that expectation.
- `GOWORK=off go test -race -tags=integration -p 2 ./internal/usecase/mqttsession ./internal/app -run '^(TestEndSession.*|TestMQTTSessionAcquisitionThreeNodeRPC)$' -count=1 -timeout=150s`
  passed (4.115s / 13.557s), `/tmp/mqtt-end-validation.log`. Coverage includes actual
  metadata/Owners, drained effects, retained charged/unadmitted and inflight debt,
  Will timing/cancellation/detachment, non-resuming reconnect, stale identities,
  changed owners, invalid evidence, cancellation, callback panic and ambiguous
  commit outcomes. Only existing Darwin linker warnings appeared.
- The three-node case uses real TCP, disk, 256 hash Slots, foreground authority
  and remote owner RPC. It verifies remote draining, the ended reason and ready
  Will on every node, repeated end, fresh lifetime and stale-end rejection.
  Physical transport close callbacks remain controlled; this is internal app
  integration, not autonomous product MQTT process acceptance.
- Named `flow-doc-contracts` passed after keeping Read First within its five-link
  limit: 86 compliant files, zero invalid and nine existing length warnings
  (`/tmp/mqtt-end-flow.log`). All seven frozen context digests match source
  `5e6e1819c5cd9cc434069b43ae2f1d0aa1a66678`; formatting/diff checks passed.

The goal remains active. The sender still needs final receive-permission ordering
and definitive-revocation handling that invokes End after releasing its scope.
Reconnect-before-new delivery, QoS 0 completion reconciliation, fair scheduling,
inbox/future sources, unattended source cleanup, unavailable-owner isolation,
Will execution, product/restore composition, offline tools, metrics and full
process/load acceptance remain required.

## Bounded connection sender and gateway sink

[Sender](../specs/mqtt-sender.md) now freezes a connection's existing exchange
ceiling and serializes each turn through a nonblocking gate. Pending completion
precedes existing unsent exchange recovery, which precedes new source admission.
Old exchanges on a resumed connection retain identity/DUP; admissions begun on
this connection, including a lost admission reply, use DUP=false. Busy preserves
the sent cursor and stores no body. Original QoS 0 preclaims may be lost when not
enqueued, but cannot be returned as another attempt.

Preparation privately captures permission identity. Immediately before enqueue,
the sender rechecks current Session and exact exchange or QoS-0 subscription,
then current receive authorization. This last authoritative authorization is the
ordering point, with exact owner execution held through enqueue. Definitive
denial fences and closes, releases scopes, then calls End; unavailable authority
does not end a valid Session. Ambiguous writes close without same-connection
retry. Failed ending stays pending for another bounded turn, unless superseded.

Only successfully enqueued downgraded QoS 0 can leave a private body-free pending
completion token. Reconciliation checks the exact current cursor/head and original
debit before rebasing across an unrelated revision; a committed-but-lost reply
is recognized without another send. The public CompleteQoS0 method remains strict.
No schema, command or RPC changed, and no new worker/queue is introduced.

Handler.BindDelivery supplies the accepted Connection and sink over the existing
SendQoS1/SendQoS0 paths. It retains credit, PacketID/order binding, peer/property
bounds and current physical identity, maps definite busy/expiry outcomes, and
emits standard terminal feedback before requesting closure. Close intent is not
isolation proof. App composition builds the sender from Node and Owners ports.

Validation (2026-09-24):

- The failure inventory and public usecase, app and sink tests preceded their
  corresponding implementation. Missing-API RED logs: `/tmp/mqtt-sender-red.log`,
  `/tmp/mqtt-sender-app-red.log`, `/tmp/mqtt-sender-sink-red.log`.
- `GOWORK=off go test -race -p 2 ./internal/usecase/mqttsession ./internal/access/mqtt ./internal/app -count=1`
  passed before the final operation-pressure correction (58.942s / 1.619s / 4.795s),
  `/tmp/mqtt-sender-race.log`. New coverage includes
  actual takeover/order/identity, busy yielding, ambiguous admission and enqueue,
  final revocation and scope release, infrastructure denial distinction, reentrant
  turns, concurrent renewal, lost QoS-0 completion, and original QoS-0 non-retry.
- `GOWORK=off go test -race -tags=integration -p 2 ./internal/app -run '^(TestMQTTGatewayPahoSingleNodeCluster|TestMQTTGroupSourcePreparationThreeNodeRecovery)$' -count=1 -timeout=150s`
  passed (25.048s), `/tmp/mqtt-sender-integration.log`. Real Paho/TCP exercises the
  new gateway sink, QoS 0 at full credit, takeover/DUP and exact PUBACK with
  controlled source/window admission. The real three-node, 256-hash-Slot case
  independently compares trimmed anchored originals delivered by the actual sender
  to a controlled sink before verifying real ACK gaps and retention. These are
  complementary internal composition tests, not one product process acceptance
  scenario. Existing Darwin linker warnings only.
- A later review found that temporary Owners operation saturation was classified
  as a closed connection by the gateway, and as an error by the sender. Both
  failures were reproduced before their guards changed:
  `/tmp/mqtt-sender-pressure-red.log` and `/tmp/mqtt-sender-owner-red.log`.
  Saturation now yields Busy before/inside a turn and at gateway admission;
  releasing capacity preserves the same send order and healthy connection.
- Final `GOWORK=off go test -race -tags=integration -p 2 ./internal/usecase/mqttsession ./internal/access/mqtt ./internal/app -run '^(TestSender.*|TestDeliverySink.*|TestOutbound.*|TestMQTTGatewayPahoSingleNodeCluster|TestMQTTGroupSourcePreparationThreeNodeRecovery)$' -count=1 -timeout=150s`
  passed (4.749s / 2.620s / 23.617s), `/tmp/mqtt-sender-final.log`, including the
  pressure correction and both real integration scenarios.
- Named `flow-doc-contracts` passed: 86 compliant files, zero invalid and nine
  existing length warnings (`/tmp/mqtt-sender-flow.log`). All nine frozen context
  hashes match `4ee4ece2104a6cb298a1ac42cea03bfdc337cb22`; formatting/diff checks passed.

The full goal remains active. The production receive-authority adapter (including
stable membership incarnation), fair source/accounting/delivery scheduling, inbox
and future-person sources, unattended cleanup/source deactivation, unavailable-owner
isolation, Will execution, product/restore composition, offline tools, metrics and
complete process/load acceptance remain required. Product MQTT is not enabled.

## Source binding tombstone retirement

Design: `docs/specs/mqtt-tombstone-retirement.md`. Acknowledged Removed rows of
an ended Session lifetime are retired atomically: a monotonic per-(owner,
client) System-2 fence rejects resurrection of generations <= ClosedThrough, a
per-Channel-owner System-3 marker keeps replay discovery after the last row, and
the row is deleted (Slot command 77, proxy/Node routing, ConsumerMaintenance
turn). `wukongim_mqtt_consumer_events_total{event="retired"}` counts deletions.
Replay marker clearing (Slot command 78) is implemented to the Node surface but
has no caller: copy-ahead persists replay state before anchors, so no current
signal proves an owner has nothing left to clean.

Verified: `GOWORK=off go test ./pkg/db/... ./pkg/slot/... ./pkg/cluster/... ./internal/... -count=1`
reported no failures after the retirement wiring; `./pkg/metrics
./internal/runtime/mqttsession ./internal/app` passed after the metric change.

Remaining: in-lifetime (UNSUBSCRIBE) tombstones retire only after Session end;
marker clearing awaits a replicated never-started or replica-reclaimed proof;
100k-member churn retirement load is not measured.

## Live-Session unsubscribe tombstone retirement

The System-2 fence also stores `(LiveSessionGeneration, SubscriptionThrough)`.
SourceRetirement retires a live Session's Removed tombstone after proving, from
fresh bounded reads (16 pages of 64), that the Session is unchanged and every
subscription through that generation is Removed. First inserts with the same
Session generation and subscription generation <= the watermark conflict; the
watermark only advances. Slot command 77 carries exactly one of
`closed_through` or `live_subscription_through`.

Commits: `090e542fa`, `357f9f708`, `a151221d9`, `b3e169389`.
Validation: `GOWORK=off go test ./pkg/db/... ./pkg/slot/... ./pkg/cluster/... ./pkg/metrics/... ./internal/... -count=1`
reported no failures.

Known limits: an old subscription never unsubscribed holds the watermark below
it; sessions with more than 1024 subscriptions keep tombstones until the
lifetime ends. Replay marker clearing stays deferred. Remaining goal items:
crash recovery, scale acceptance, production receive-authority adapter, fair
scheduling and full process/load acceptance. Product MQTT is not enabled.

## Crashed owner boot retirement

Design: `docs/specs/mqtt-crashed-boot-retirement.md`. Before any MQTT owner,
RPC, listener or worker exists, `Retirements.Recover` takes an exclusive flock
on `<data>/mqtt/retired-owners/LOCK` for the process lifetime. Started-boot
markers without a receipt are then proven crashed and receive a receipt bounded
by MaxUint64; the current boot writes its own marker. Graceful `Record` removes
the marker after its receipt; `Close` releases the lock afterwards. Corrupt,
foreign or renamed markers and a second process fail MQTT startup closed.
Late durable effects stay fenced by takeover OwnerGeneration CAS. The proof is
node-local; partition and unavailable-node recovery are not covered.

Validation (2026-09-28):

- `internal/infra/mqttowner` failure-inventory tests were written first (RED:
  `Recover`/`Close` undefined), then pass.
- E2E `GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/crash_recovery`:
  SIGKILL of the owner process group with an unacknowledged QoS 1 delivery,
  restart of the same spec, Session Present, same Packet Identifier with DUP,
  one post-recovery delivery, no extras, no resubscription. Before the fix the
  resumed CONNECT got CONNACK 0x88. Artifact:
  `mqtt-session-owner-crash.json` under `WK_E2E_MQTT_REPORT_DIR`.
- E2E regressions `mqtt/session`, `mqtt/reclamation`, `mqtt/will` pass.
- Unit `./internal/... ./pkg/cluster/... ./pkg/metrics/...` and Linux build pass.

Remaining: scale acceptance, production receive-authority adapter, fair
scheduling, partition/unavailable-node owner isolation, replay marker clearing
and full process/load acceptance. Product MQTT is not enabled by default.

## Group scale acceptance (partial)

Design: `docs/specs/mqtt-scale-acceptance.md`; E2E `test/e2e/mqtt/scale`.
Fixes found by the scenario:

- `7ca74cc95`: request-owned Channel metadata applies (MQTT source, replay,
  anchor, retirement, Will receipt, prepared append) now wait for a contended
  shard lock within the request deadline instead of failing as not ready,
  which closed concurrent same-group SUBSCRIBE as `unconfirmed`.
- `356ebc031`, `264cae694`: Removed source bindings stay in the recovery index
  while scheduled, so background maintenance actually retires them; unprovable
  retirement backs off from 1s to 10min.
- `05908c513`, `34138e6d9`: consumer scan continues a stream while its full
  page is wholly admitted, cools empty streams for 16 turns, and reserves half
  of each turn for streams with an unfinished cursor. Previously 768 streams
  at 32 pages/turn left one Slot's backlog waiting a full rotation.

Validation (2026-09-29): 2,000 members / 500 persistent subscribers / 10
churned subscribers passed (`/tmp/mqtt-scale-500d/mqtt-scale.json`): 0
duplicates/missing/reordered/wrong identity, `retired_delta` 10 of 10.
Before `34138e6d9` the same run retired 0 in 4 minutes (3 of 3 runs).

Open:
- Delivery latency is far too high: fanout p50 59s / max 65s for 2 messages
  to 500 subscribers; post-churn delivery 41s. Not yet diagnosed.
- The default 100,000-member / 500-subscriber run has not passed; one earlier
  attempt closed one SUBSCRIBE as `unconfirmed` after `7ca74cc95`, not
  reproduced in 4 later 500-connection runs.
- `unsubscribe_recovery` cold-subscribe tests fail intermittently on the
  pre-change baseline too (known, unresolved).

## Group delivery latency diagnosis (in progress)

`152b6dcbe`: a fresh delivery cursor whose start is newer than the latest
replay anchor now yields `ErrNotReady` from accounting (a regressed anchor after
accounting still returns `ErrEvidence`), and window admission before the first
accounting receipt is idle instead of `ErrConflict`. These removed most failed
turns on the traced clients but did not change delivery latency.

500 subscribers / 2,000 members (single-node cluster, 256 hash Slots), SEND ack
to last receipt:

- 16 delivery workers (default): p50 49–82 s across three runs.
- 64 workers (`WK_MQTT_WORKERS=64`, experiment only): p50 30.3 s, max 37.6 s;
  exactly-once/order/identity and 10/10 retirements still held.

Breakdown (traced clients, debug build): the replay worker revisits the group
source about every 6.4 s, so a new message waits roughly 3–13 s for an anchor.
Delivery then continues for ~40 s: the gap between one client's turns is p50
1.0 s but p90 15.2 s, and turn stages have near-zero p50 with p90 tails of
36–76 ms (Session/recovery/select reads), 258 ms (accounting) and 208 ms
(enqueue). Quadrupling workers roughly halved latency, so both scheduling
throughput and contended per-turn reads contribute. Delivery is never woken by
Channel commits; turns rely on the 1 s idle poll.

### Slot read barrier measurement

`e912935e7` adds `wukongim_slot_read_barrier_duration_seconds{result}`. One
500-subscriber run (2000 members) recorded 398,746 `ok` barriers, mean 8.1 ms:
58% within 0.5 ms, 33% in 10-25 ms, 6% in 25-50 ms, none above 0.5 s, and 4
`deadline`. A second run measured 14,893 barriers/s while 500 subscribers were
idle but only 2,765/s during group fanout. Barrier volume is high, yet fanout
throughput is not bounded by barrier issue rate; the fanout bottleneck is still
unidentified. Barrier coalescing is not justified by this evidence yet.

## Bounded idle delivery and source wakes

Design and failure inventory: [mqtt-idle-delivery](../specs/mqtt-idle-delivery.md).
ConnectionDelivery now skips Slot reads after a full quiet pass for at most ten
seconds, checking exact local Owner execution first. Atomic wake invalidation
survives running turns; pressure, errors, unfinished scans and pending children
cannot install a quiet hint. All actual delivery/permission checks remain fresh.

The existing scheduler bounds a reverse Channel-source interest index and
coalesces targeted wakes without another task map, worker or publication queue.
Post-commit envelopes and timely confirmed replay anchors wake interested local
connections. Over-limit inbox sets fall back to ordinary polling. Lost/remote
hints recover through full refresh. Scale now explicitly uses twelve initial
Raft groups and 256 hash Slots, and records/asserts post-fanout quiet read cost.

Validation (2026-09-30): unit suites, focused race checks, real scheduler and
single-node/three-node app delivery passed; process interop, persistent
reconnect/future inbox/takeover, graceful restart/shutdown and SIGKILL recovery
passed. The subscription-entry fixture needed the same real anchor-wake port as
product composition; its unchanged three-second delivery assertion then passed.
The final 2,000-member/500-subscriber run passed with 10/10 churn retirements,
zero delivery discrepancies, 2,178.71 idle barriers/s and last-receipt p50 27.783s.
See [500-subscriber artifact](mqtt-idle-scale-500.json).
Detailed commands/limits are in the design's validation evidence.

The final default 100,000-member run stopped in public HTTP member preparation
after 726.31s: 95,000 members' batches confirmed, the last 5,000 unconfirmed at
the twelve-minute deadline, MQTT subscription/delivery not reached. See
[failure artifact](mqtt-idle-scale-full.json). Full scale acceptance is incomplete.
An old-binary reproduction
attempt at each of 500/100/32 connections stopped at cold SUBSCRIBE as
`unconfirmed`, before the performance window; no matched latency comparison or
repair of that intermittent problem is claimed. Delivery latency and complete
MQTT failure/scale acceptance remain open.

## Ordinary member preparation concurrency

[Design/evidence](../specs/ordinary-membership-proposal-scheduling.md). Profiling
found the two ordinary UID projection submitters waiting on Slot Future.Wait;
a ten-second CPU profile had 0.69s of samples. The opt-in 10,000-member process
probe first failed its 30-second HTTP setup budget. Changing only the supervised
proposal limit from two to eight passed without profiling: 12.000s preparation,
10,000 confirmed projected rows, unchanged logical command count and three
public conversation samples. Cancellation/failure tests were written first;
they verify joining admitted commands, accounting confirmed rows and preserving
the original error. Focused race and the cluster unit suite passed.

The original 100,000-member/500-connection/20-message/600-churn workload now
confirmed all member-preparation requests, then stopped in cold SUBSCRIBE
(214.45s total): 2 deadline and 13 canceled closures, 485 active owners at failure.
Active owners do not prove successful SUBACKs; MQTT fanout and churn were not
reached. See [failure artifact](mqtt-scale-membership-8-failure.json). Complete
scale acceptance remains open. The former twelve-minute member-preparation
timeout was removed in this observed run; the subscription failure remains
unrepaired.

Multi-node public directory/recipient checks also passed, including UID requests
from a non-replica ingress. One initial pagination SEND returned HTTP 408;
the unchanged focused rerun passed. This timeout is not claimed repaired.
`go vet` and the named FLOW contract check passed.

## Scale producer lifecycle and failure evidence

The scale fixture now reports completed phases and a bounded failed-phase JSON.
A separate diagnostic full run prepared 100,000 members in 122.664s, subscribed
500 clients in 13.088s, received the twenty initial publications in 174.186s,
and finished all three 200-subscriber churn rounds in 19.374s. It then failed
at the WKProto producer's post-churn SEND because the fixture sent no heartbeat
during more than three minutes of inbound inactivity.
[Diagnostic failure artifact](mqtt-scale-profile-sender-failure.json); the
profile flag is explicit and this is not accepted as a full pass.

The authenticated producer now sends real PING/PONG every fifteen seconds with
one canceled/joined loop. Heartbeat errors fail the test; it never reconnects or
changes SEND identity. The original deadlines, sizes, delivery assertions and
retirement bounds remain intact. The unprofiled full run prepared 100,000 members
in 119.072s, subscribed 500 clients in 13.772s and reached every initial receipt
in 142.207s, with 1,975.1 idle barriers/s (3.95/subscriber/s). It failed during
round 2 resubscription with twelve `subscribe/conflict` closures; post-churn
delivery and retirement were not reached. [Failed artifact](mqtt-scale-churn-conflict-failure.json).

A separate unprofiled 2,000-member/500-connection/two-message run passed all
600 churn retirements with zero missing/duplicate/reordered/incorrect identities
and five confirmed producer heartbeats. [Reduced delivery/full churn artifact](mqtt-scale-churn-600.json).
This validates the fixture heartbeat and full churn shape at its recorded size;
it does not replace the 100,000-member/twenty-message acceptance. Full scale and
the intermittent subscription conflicts/deadlines remain open.

## Subscription final CAS contention

A deterministic test-first reproduction independently confirmed a completion
CAS bug: renewal after projection's final read left the exact child unchanged
but made activation/removal fail. The nineteen-case failure matrix covers both
stages, changed Owner/child, repeated renewal, no parent advance, port errors,
lost applied replies, cancellation, receive revocation and another remover's
exact final commit. Final completion now makes at most three proposals after
definite rejection, rereading exact Owner/full child and parent progress while
retaining the original projection receipt. Activation reauthorizes each time;
unknown outcomes still fail without another proposal.

Focused race checks passed (6.721s), the full usecase unit suite passed
(119.501s), real single-node cluster subscription/request integration with race
passed (app 5.245s, usecase 3.438s), and vet/FLOW checks passed. This fixes the
proven completion race; it does not identify every previous process conflict.
Temporary source-location diagnostics also observed `channel: backpressured`
at one cold subscription, and another diagnostic attempt exceeded the unchanged
three-minute fanout wait. Neither is claimed repaired. The final unprofiled full workload at `bc41096de`
prepared all 100,000 members in 129.668s, subscribed all 500 clients in 14.821s,
and reached all initial receipt counts in 166.175s. Quiet barriers were
1,835.4/s (3.67/subscriber/s). Round 0 resubscription still caused three
`subscribe/conflict` closures, before post-churn delivery/retirement verification.
[Failure artifact](mqtt-scale-completion-failure.json) and [repeat command/binary provenance](mqtt-scale-completion-provenance.json).
The isolated completion race is repaired; other subscription contention and
full scale acceptance remain open.

The final fixed-position diagnostic build of the same product revision passed
a 2,000-member/500-connection/two-message/600-churn workload: all 600 tombstones
retired, no delivery discrepancies, and five confirmed publisher heartbeats.
[Diagnostic churn artifact](mqtt-stage-churn-600.json). With twenty initial
messages, a separate attempt exhausted the unchanged three-minute receipt wait
before churn, without capturing a new subscription error position.
[Diagnostic failure](mqtt-stage-fanout-failure.json),
[exact probe bounds/provenance](mqtt-stage-diagnostic-evidence.json).
Temporary wrappers were built through a Go overlay outside the repository and
are absent from the product source. Full scale acceptance, the remaining
subscription conflicts/backpressure and fanout latency remain unresolved.

## In-flight churn contention

The new opt-in [in-flight probe](../specs/mqtt-churn-pressure.md) tightens the
remaining failure loop by churning before initial delivery drains. It captures
nested replica-recovery receiver pressure, quota-scan parent changes, rejected
group cursor Init and valid newer accounting snapshots during drain. Repairs
retain typed recovery pending, allow at most three preparation attempts after
definite/read-only rejection under the exact original child/Owner, and yield
before any stale drain mutation. Unknown errors and anchor outcomes never gain
a retry signal. The first protected source boundary stays fixed.

Seventy-six new route/preparation/drain cases include deterministic real renewal
and ACK interleavings, unknown/lost replies, cancellation and corruption. Focused
race checks pass in 8.173s; the full usecase race suite passes in 176.921s. The
ordinary 2,000-member/twenty-message/500-connection probe passes 1,100 subscriptions,
600 removals, 500 exact fresh receipts and every retirement with zero subscription
closure observations. [Probe artifact](mqtt-churn-repairs-500.json),
[failure locations, bounds and provenance](mqtt-recovery-yield-evidence.json).
Isolated three-node 64-cold-admission and single-node/three-node app composition
gates pass (93.186s and 38.161s); preceding parallel deadline failures remain
recorded without a causal claim. The full run at `dd8e49bb9` subsequently fails
initial fanout; its evidence and diagnosis follow below. Historical failures
above remain evidence of earlier revisions.


## Full fanout failure evidence

The unprofiled full run at `dd8e49bb9` confirms all 100,000 members in 123.791s
and all 500 subscriptions in 13.403s, then exhausts the unchanged three-minute
initial receipt wait. Public owner gauges show 497 active/held owners at failure.
Closure origin was not captured; churn, post-churn delivery and retirement were
not verified. [Failure artifact](mqtt-scale-churn-final-failure.json),
[exact source/binary/command provenance](mqtt-scale-churn-final-provenance.json).
The four deterministic churn repairs and narrower passing probes remain valid;
full scale acceptance is incomplete.

The fixture now preserves bounded receipt histograms, independent Paho closed/
incomplete counts and the harness process dump before cleanup on failure. A
closed incomplete persistent client fails early because it cannot receive future
publications; open clients retain the original receipt timeout and every success
assertion remains unchanged. The [failure inventory](../specs/mqtt-fanout-closure.md)
precedes these diagnostic changes.

A temporary terminal-error overlay with 2,000 members, 500 clients and twenty
messages reaches all 10,000 initial receipts in 148.785s, then fails at one
unsubscribe/conflict. Reducing only messages to two passes three churn rounds
and all 600 retirements; increasing only rounds to ten fails on round 8
resubscription with two subscribe/deadline closures. These different outcomes
do not reproduce or repair the original full fanout failure.
[Diagnostic artifacts, bounds and provenance](mqtt-fanout-closure-evidence.json).


The original-size terminal-boundary overlay repeats the initial receipt timeout
with all 500 clients open and 13–19 observed receipts each; terminal ACK, Sender,
renewal, gateway and control probes capture no closure error. This identifies
a throughput failure independent of transport closure in that run, without
explaining the earlier three lost owners.
[Full boundary evidence](mqtt-full-dd8e-boundaries-provenance.json).

A separate 20-second CPU/goroutine profile records 70,756 successful Slot barriers
with 413.421s aggregate wait and 10.66s CPU samples. One goroutine sample places
ten of sixteen delivery workers in Accounting read barriers, four in window
read barriers and two in proposal futures; the scheduler waits for queue space.
A bounded repeated-accounting deferral passes thirteen isolated safety cases,
but shows no throughput benefit at 500 clients or in a sequential 64-client pair.
The implementation and test prototype are archived outside the repository,
and product behavior, FLOW and release notes retain their original semantics.
[Rejected experiment and artifacts](mqtt-accounted-delivery-refresh-evidence.json).

The original-product 64-client terminal-boundary probe completes every initial
and fresh receipt, exact identities/order and 192 retirements, then fails its
unchanged idle threshold (10.80 barriers/client/s). No unsubscribe conflict
recurs. This is another failed diagnostic scenario, not complete scale
acceptance or a repair of the intermittent conflict.
[Result](mqtt-unsubscribe-dd8e-boundaries-64.json).


## Delivery stage/read budget diagnosis (source 5dfa36d88)

No product optimization is adopted. Four serial diagnostic scenarios retain
original worker, retry, deadline and receipt settings. The original-size v1
attempt confirms 100,000 members but fails initial SUBSCRIBE before fanout;
a 2,000-member v1 run fails fanout with eight closed incomplete clients. v1
read attribution is invalid because Owner operation context derivation drops
caller diagnostic labels. Corrected overlays explicitly carry only that marker.

The v2 2,000-member/500-client/twenty-message run repeats the three-minute
receipt timeout with all 500 clients open (8,821 observed receipts). The v3
run receives all 10,000 initial publications in 141.029s, then fails round 2
unsubscribe with two public unsubscribe/conflict closures; it does not verify
post-churn delivery or complete retirement. These are failed scenarios.

Two corrected steady windows attribute about 84% of aggregate delivery
execution to actual Slot ReadBarrier waits. WindowAdmission takes 55.56% and
56.80%, Accounting 28.64% and 29.38%, recovery 4.12% and 3.04%. Window/auth
read waits take about 74% of window wall time. Normalized online barriers
are 39.24 and 29.13 per accepted gateway enqueue, including failed attempts;
an enqueue remains distinct from a Paho receipt or durable acknowledgement.
The v3 steady window has 379 typed Channel backpressure and 48 window conflicts;
all 17,219 exchange-recovery calls return empty. v2's other errors remain unknown.

This evidence prioritizes a bounded compound routed Channel plan/original-read
regression seam over another Accounting hint or an unmeasured worker increase.
No new port, protocol, cache or product behavior is implemented. Existing
focused race validation passes; copied overlay source, fixed raw aggregates,
exact commands, binary hashes and frozen instruction digests are preserved.
[Measurement and pre-implementation failure inventory](../specs/mqtt-delivery-read-budget.md),
[repeatable artifacts](mqtt-delivery-stage-diagnostic/README.md).
