# Pinned physical runtime identity

A Will retry must distinguish a changed route from a deleted/recreated physical
runtime. Channel membership and leader epochs are not stable lifetime identities.
The retained table-3 System-1 floor already changes on physical deletion and is
constant throughout the following live runtime's ordinary updates.

## Failure inventory before implementation

1. Separate floor/runtime reads combine an old live row with a new deletion, or
   a new row with an old floor. Both must come from one native snapshot.
2. Warm runtime cache state replaces the pinned row. A snapshot opened before
   delete/recreate must continue returning the old coherent pair.
3. Route/leader/membership changes spuriously change the physical identity;
   physical delete/recreate or another key/type/Slot wrongly shares identity.
4. A corrupt, swapped, unknown or maximum floor is treated as zero/absence;
   a live row at or below a retained authority floor is accepted as new state.
5. Caller-selected Slots, local follower reads, stale routing, a failed apply
   barrier or unavailable current leader produce convenient cached evidence.
6. RPC drops the view, accepts a foreign key, mixes unrelated results, changes
   old query JSON or allows the new field on another read kind.
7. Snapshot/backup/reopen loses the retained floor. Reads must expose absence
   separately from retirement; absence never authorizes Will republication.
8. Malformed/oversized requests reach an authority barrier or create metadata;
   read cancellation, result ownership and fixed byte/row bounds remain enforced.

## Contract

MQTT read kind 22 adds optional `runtime_channel` query and `runtime` result.
The key is a bounded ordinary Channel ID/type. Routing uses that Channel ID,
not the Will's ClientID. Existing RPC 91 supplies the fresh ReadIndex/apply barrier
and rechecks leadership/mapping after reading one snapshot. The view includes the
exact key, optional live runtime, and `retired_through` from table 3 System 1.
Zero retirement denotes no retained deletion witness; it is not missing runtime.
A live runtime must have channel/leader/route versions above the retirement floor.
Its stable physical identity is the exact key plus `retired_through`; no new table,
column, write path or allocation counter is introduced.

This view does not grant publish permission, prove receipt coverage, authorize
redispatch, or isolate work already admitted before deletion. Business-channel
lifetime changes and distributed restore may need stronger fencing. Matched peers
are required for kind 22; older queries/replies retain their omitted-field bytes.

## Repeatable validation

```sh
GOWORK=off go test -race -p 2 ./pkg/db/meta ./pkg/slot/proxy -count=1
GOWORK=off go test -race -p 2 -tags=integration ./internal/app -run '^TestMQTTPreparedAppendRejectsPhysicalRuntimeRecreationSingleNodeCluster$' -count=1 -v
GOWORK=off go run ./scripts/flowcheck --mode check
```

The failure inventory and metadata/proxy/real-app regressions preceded the
implementation. The initial focused run failed on the missing kind/view contract;
the focused metadata and two-node proxy suites now pass. The proxy fixture
checks remote ownership with 256 hash Slots, while the app integration uses real
Slot create/delete commands and committed Channel reads in a single-node cluster.
The pre-existing retirement tests cover native/portable snapshots and reopen.
These checks are package integration evidence, not product process E2E or scale
acceptance, and do not enable uncertain Will redispatch.

Full related race suites passed: metadata 36.959s and Slot proxy 20.720s. The real
app integration passed under race in 4.639s after correcting a test-only value
comparison. FLOW validation reported 86 compliant files, no invalid files and
the same nine pre-existing size warnings. The integration emitted:

```text
mqtt_runtime_incarnation_evidence: nodes=1 hash_slots=256 physical_delete=true slot_fsm=true source_read_kind=22 coherent_retirement=true old_epoch=1 new_epoch=2 old_route=1 new_route=2 stale_request_absent=true committed_bodies=2
```

## Frozen repository context

Source: `6dca54d07730d20ae815f13f21d739fdfb72937d`. Applicable rules/navigation were read before package work.
SHA-256 values refer to that exact source revision.

| File | SHA-256 |
| --- | --- |
| `AGENTS.md` | `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade` |
| `pkg/db/FLOW.md` | `33c7ba81c18ed8cb377e9bef541526028ef31e64dd03becc7ac1ea6cc14fb10a` |
| `pkg/db/meta/FLOW.md` | `83f853b162fd06efce441cf8f5a1bd77aaa7fa96154bc6327a916fc8f902de3c` |
| `pkg/slot/FLOW.md` | `900d9ea4fcf69135d14036dcccac8c87c4ce096b4b04b031e09a2bf9031eab06` |
| `internal/app/FLOW.md` | `6d5cbc6153b4b6b18d947f27e43a25e01b7ad2673bc253be790abf8bb37fe9c5` |
