# MQTT protected source storage

This is the replica storage boundary for the approved reliable-source design.
It does not establish Channel authority or authorize SUBACK. Product MQTT stays
disabled until replicated activation, copy proof and restored-owner fencing are
implemented. A local successful write is never a distributed durability proof.

## Contract

Message table 1 reserves System ID 12 for one source-incarnation state. Its
key-bound checksummed version-1 fixed value contains a bounded UTF-8 generation,
revision, immutable start-after position, copied-through position and the
32-byte digest of the externally verified shared-copy receipt. Creation uses
revision 1, copied-through=start-after and an empty digest. Later apply is CAS,
keeps the incarnation/start, strictly advances copy coverage, requires a nonzero
receipt digest, and cannot exceed the local committed checkpoint. Exact retry
returns the same state. This API materializes a replicated decision; callers
must prove authority and shared-content durability before invoking advancement.
There is no reset/release API that can silently abandon protected obligations.
Creation also installs an explicit zero checkpoint when absent, under the
checkpoint mutex and in the same commit. Later missing checkpoints are corruption,
including on exact retry and suffix truncation; they cannot mean an empty log.

Activation cannot claim content already physically removed. Physical prefix
cleanup stops at copied-through, while ordinary logical history may advance
farther. The existing named replay cursor remains unrelated. Eligible-range
exhaustion is not proof that protection has completed; stored physical progress
records only the clamped boundary. No background loop should spin at that fence.

Protected reads require the exact incarnation, explicit positive range and
bounded row/byte budgets, beginning strictly after copied-through. They hold the
append fence while checking source state,
checkpoint and original rows; they ignore ordinary logical visibility and edits.
Requests beyond HW fail instead of reporting an empty page. A missing expected
position is corruption, never an empty source or permission to skip a gap.
Results own their content. The first oversized row fails the requested budget.

Portable binary backup carries the System record and validates it against the
selected HW; import must not accept malformed state or a frontier above the cut.
Raw source state on restore still needs distributed reactivation before use.
MQTT state JSONL transfer/capability gates and full shared replay remain required.
Older writers ignore this System key and can erase protected content. Activation
therefore requires matched runtimes on every possible owner; binary-only rollback
after activation is unsafe. There is no mixed-writer compatibility claim.

## Failure inventory before implementation

1. A stale/changed CAS, generation switch, rewritten start, regressed copy,
   missing receipt, overflow, invalid UTF-8 or a frontier above HW is accepted.
2. Protection is established behind physical erasure; exact retries require a
   different receipt or are mistaken for a new transition.
3. Ordinary retention deletes an uncopied message, advances physical evidence
   beyond protection, or holds logical history visibility back unnecessarily.
4. Protected reads apply the ordinary retention floor, leak uncommitted content,
   skip a missing source position, exceed row/byte limits, alias stored content
   or accept the wrong source incarnation.
5. Reopen loses protection; backup/import drops, corrupts or permits coverage
   above the selected checkpoint. An unknown/corrupt stored format fails open.
6. Suffix truncation destroys committed protected content. Recovery and snapshot
   replacement must preserve the same fence or fail explicitly; wholesale
   Channel deletion requires the future lifecycle protocol.
7. Replica-local apply is mistaken for cluster activation or shared-copy proof.
   Those are follow-on runtime/replication requirements, not claims of this seam.

## Frozen context

Source `c8f7eb474`; SHA-256 digests:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `d90cb020a5d487214b0dd3fefdc49533d50c7f37c92e217aa139141c9b85761e`
- `pkg/db/message/FLOW.md`: `2bed070602154c811270292e9f2e536a043a4046ec5e44a2da4849eec8f1a463`
