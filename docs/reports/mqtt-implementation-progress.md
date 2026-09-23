# MQTT implementation progress

Full goal: implement the approved [MQTT IM access design](../specs/mqtt-im-access.md).
Status: in progress; the product has no MQTT listener yet. No table migration,
cluster session recovery, source retention protection or durable delivery has been
implemented. No product E2E or capacity claim is made.

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
