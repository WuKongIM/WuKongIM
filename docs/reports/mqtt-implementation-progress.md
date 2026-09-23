# MQTT implementation progress

Full goal: implement the approved [MQTT IM access design](../specs/mqtt-im-access.md).
Status: in progress; the product has no MQTT listener yet. The codec, generic
gateway and session/subscription tables with Slot commands are implemented. Other tables,
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
