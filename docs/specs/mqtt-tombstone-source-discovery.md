# MQTT replay discovery after the last consumer leaves

Replay work must remain discoverable after the last binding becomes Removed.
The existing active-source discovery reads retention index 4, which deliberately
excludes Removed consumers. It cannot serve as the complete replay-work catalog.

Reuse table 26 primary rows, including retained removal tombstones. A bounded
snapshot scan validates one row per Channel-source incarnation and seeks past
that entire owner prefix. It examines at most limit+1 rows, independent of group
subscriber count, retains encoded key order and excludes UID qualifications.
No new table, index, value layout or backfill is needed. This is a scheduling
hint, never evidence of consumer completion or permission to delete content.
Retention planning continues using strict primary/index witnesses in index 4.

`MQTTReadReplaySources` is closed read kind 17 on RPC 91; it uses the existing
body-free source-owner result and fresh hash-Slot barrier. Kind 16 retains its
active-source semantics. Old peers reject kind 17; the replay worker must not
downgrade to kind 16 and silently lose cleanup. All participating binaries must
match. Slot snapshots and restart already preserve these primary tombstones.

The existing worker switches to kind 17 while retaining all per-Slot bounds,
continuation checks and joined lifecycle. A tombstone does not stop source
protection or prove replica cleanup. Source deactivation, safe tombstone pruning,
activation interrupted before the first binding remain separate lifecycle work;
no row deletion is introduced. [Revision-fenced binding release](mqtt-binding-removal.md)
now uses a separate source-Slot acknowledgement and retained Removed tombstone.

## Failure inventory before implementation

1. The final Removed transition makes existing replay work undiscoverable, or
   tombstones become active consumer obligations again.
2. Discovery scans all subscribers, merges source incarnations, changes encoded
   order or loses work after cursor wrap, snapshot or restart.
3. Malformed primary keys/values or foreign UID owners produce usable hints;
   cancellation/closed storage returns partial success or leaks iterators.
4. Kind 17 accepts unrelated collections, invalid/repeated/regressed owners,
   incorrect cursors or incomplete short pages. Old queries change their JSON.
5. RPC bypasses fresh Slot authority, permits entity routing for a recovery scan,
   or a warmed isolated former leader serves authoritative rows/absence.
6. Worker still requests kind 16, so direct storage tests pass but cleanup stalls
   after the last consumer leaves. Observation counts substitute for durable proof.

Tests use the approved storage, closed Slot RPC, worker and real cluster seams.
Controlled binding transitions test discovery only, not completed product removal.

## Frozen context

Source `e7bd33e476e47057ec0fc1b3cd371ae9ec946386`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `3560287fef837ef40dcf04754ac1037a28ec85d26dae7748f465ab9524c624fe`
- `pkg/db/meta/FLOW.md`: `216911f06435aad7b272534b4d89f6b63e893e0769cb2bcb65a160ca4552f4de`
- `pkg/slot/FLOW.md`: `144acbb23ecc6006a3dd4eabaddc93db17be970b811a5a04d23687406f63ce90`
- `pkg/cluster/FLOW.md`: `bd9136f0c2005024a7a41771e8a84c08642882cc92d057c1fce9dbb7bd7f5a73`
- `internal/runtime/mqttsession/FLOW.md`: `f2132124bc0ebd4781dd70b3579f86139272c3c9167af08bfa089df52aeed664`
- `internal/app/FLOW.md`: `6bda470cfe3860653a903a19762f7f6cd1748315c3eddb0cdd61924faf31934d`
