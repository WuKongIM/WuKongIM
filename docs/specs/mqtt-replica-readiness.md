# Replica-local MQTT replay readiness evidence

Migration currently proves the native Channel log frontier, but that does not
prove an independent shared replay copy exists. Before releasing original source
protection, migration must also retain verifiable recovery content. This change
adds the bounded read evidence used by that gate; source release and migration
phase admission are not implicitly authorized by the new observation.

A pinned storage read binds one caller-captured committed frontier, the latest
committed anchor at/below it, and this replica's complete replay prefix/meter.
It reports the required anchor/Through and whether local content covers it.
An absent replay table is lag, not completion. A covered older anchor is verified
at its own cumulative meter even when the local copy is ahead. Source copied-through
is never content proof, and an anchor behind an already released prefix is invalid.
No source/anchor with no released obligation preserves native-channel readiness;
pending controls do not become committed requirements. This is not an audit of
unselected historical journals, nor permission to discard replay data.

The Channel adapter exposes a storage-neutral capability. Active migration probes
attach optional evidence from the actual store after checking fresh Slot metadata,
then recheck complete placement, write fence and runtime authority. Ordinary
observational probes remain unchanged. Missing evidence from old/unsupported
stores stays explicit; it must not become a positive migration admission proof.
The existing internal JSON migration RPC carries the optional bounded field;
older replies leave it absent. No table, index or persisted format changes.

## Failure inventory before code

1. Native HW, source copied-through, a pending anchor or a donor's digest is
   accepted as local shared-content readiness; a missing replay table says ready.
2. A locally copied prefix ahead of the requirement has a wrong historical meter;
   malformed source/checkpoint/journal/entry evidence is treated as absence.
3. A caller supplies future HW, or a read changes state, releases originals,
   advances the checkpoint or resurrects trimmed ordinary history.
4. Readiness changes after trim/restart, a closed lease escapes ownership, or
   cancellation/changed placement/fence/runtime still returns a readiness receipt.
5. Node/RPC drops the field, unsupported stores fabricate success, or adding
   migration evidence changes ordinary diagnostic probes.

Tests precede implementation at storage/adapter/service seams and the existing
three-node learner recovery integration. Full migration gates, fenced background
recovery and replicated source release remain the next connected requirements.

## Frozen context

Source `9279d4d72`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/channel/FLOW.md`: `2ba2d86f1b4ac413c66f873546b60f5dc955c60814185b2f866184fda618bcfe`
- `pkg/cluster/FLOW.md`: `756ea5d026cae3194d7ccdda2c778c8aa979b755a2a7bb8531b30cb4064ce877`
- `pkg/db/FLOW.md`: `8b723e6f1402c2361a872e3a6e4021ac4146782ebc9b416cb7b9cc9787bb9d75`
- `pkg/db/message/FLOW.md`: `327128d0872d8461d6d8b8e520dc7620ac828e9ebb0d892d273d9cf14131079d`
