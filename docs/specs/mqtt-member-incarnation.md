# Stable subscriber incarnation

## Contract

MQTT receive authority must distinguish uninterrupted membership from removal
and rejoin, independently of channel-wide mutation versions and UID projections.
The existing subscriber primary key stays unchanged. Optional column 4 stores
`incarnation` in a key-bound version-1 column envelope. Empty legacy values mean
incarnation 1. A new membership allocates a value above 1 from subscriber table
System 1, a persistent per-hash-Slot uint64 high-water mark. No logical table is
added. Existing members keep their value on repeated adds. Every successful
remove/readd, including reset and delete/recreate in one FSM batch, gets a new
value even when the caller reuses the channel mutation version.

Allocation and membership/count changes commit together in canonical UID order.
The counter survives member and channel deletion and snapshots; exhaustion and
corruption fail without partial writes. Ordinary subscriber operations never scan
the group or allocate clocks/randomness in the FSM. Each command writes its final
counter once, not once per added UID. The fixed counter is at most one record per
hash Slot (256 by default). Batch range deletion must invalidate
prior row overlays and hide pre-batch members from later operations.

Old empty rows retain incarnation 1 until removed; idempotent adds do not backfill
or change their authority. Matched writers/tools are required: old binaries can
read keys but erase incarnation values when adding existing members. Rollback
requires a pre-feature backup. Native membership writes emit this format even
while MQTT is disabled. Upgrade the complete cluster and its offline tools with
membership writes stopped; do not resume writes with any older Slot participant.
This format does not support a rolling mixed-writer deployment.
JSONL must preserve both live incarnations and
counter high-water marks, including Slots with no live members. Import installs
exact members rather than allocating new authority, never regresses the counter,
and rejects conflicting existing rows. Missing legacy fields mean incarnation 1.
The v2-to-v3 installer preserves legacy incarnation 1; its original business
comparison excludes this native-only field only after validating that default.
A later rejoin cannot pass that original migration verification.
This slice does not enable MQTT or replace the required fresh Slot receive read.

## Failure inventory frozen before implementation

- Duplicate/add retry or unrelated member change must not revoke existing members.
- Rejoin with an identical channel mutation version must change the incarnation.
- Multiple add/remove/reset/delete/recreate operations within one atomic batch
  must see previous operations; old disk rows/overlays must not restore authority.
- Different channels in one Slot and sorted/deduplicated UIDs must allocate
  deterministically; independent replicas produce identical snapshots.
- Late batch failure must roll back members, counts and the allocator together.
- Exhausted or malformed allocator and malformed/checksum-mismatched member
  values must fail closed. Empty legacy rows and unknown future columns decode.
- Snapshot/backup/reopen must retain active identities and deleted high-water
  marks, so the next allocation cannot reuse a removed identity.
- JSONL export/import/compare must retain uint64 precision, exact live identity,
  counter-only Slots and legacy rows. Exact import retries succeed; changed
  identities fail; invalid or missing sequence witnesses reject new bundles.
- Native subscriber counts, stale-version behavior, pagination, key layouts and
  existing import/export behavior must retain their documented contracts.

## Validation

Tests exercise the public metadata API, native snapshots and the offline bundle
pipeline. Fresh authoritative reads and the production SubscriptionAuthorizer
remain the following integration step; storage identity alone is not a grant.

Verified locally on 2026-09-24:

- `GOWORK=off go test -race -p 2 ./pkg/db/meta ./pkg/db/transfer ./pkg/db/inspect -count=1`
  passed (21.010 s, 126.343 s and 1.913 s). This includes native compatibility,
  offline round trips, sequence-only target rejection and schema/value checks.
- After the final per-command counter-write optimization,
  `GOWORK=off go test -race -p 2 ./pkg/db/meta ./pkg/db/transfer ./pkg/slot/fsm ./pkg/slot/proxy -run 'Test(Subscriber.*|WriteBatchSubscriber.*|MetadataValueCodecsRoundTripCompleteRows|CompatibilityWriteBatch.*|MQTT.*)' -count=1`
  passed (8.767 s, 18.511 s, 1.971 s and 2.312 s).
- The broader default `pkg/db` and Slot FSM/proxy suites passed except for the
  subscriber round-trip fixture's old zero default; the corrected explicit
  incarnation fixture passed in both race runs above.
- Complete migration adapter race suites passed: `internal/infra/migrationv2`
  (237.582 s) and `internal/infra/migrationv3` (21.568 s). After correcting the
  outdated assertion that native proposal format 3 is unknown,
  `GOWORK=off go test -race -p 2 ./internal/usecase/migration -count=1`
  passed (16.401 s). Ordinary migrated messages still select format 1; unknown
  versions remain rejected.
- `flow-doc-contracts` render/check: 86 compliant, zero invalid, nine pre-existing
  length warnings. Existing Darwin linker warnings did not fail the tests.

The pre-existing lexical-versus-encoded subscriber JSONL ordering issue is
recorded in `docs/development/CODE_QUALITY.md`; this change does not fix that
separate limitation. Multi-node receive authorization and revocation acceptance
remain unverified until the following production authority integration.
