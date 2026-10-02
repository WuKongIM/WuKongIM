# MQTT replay repair from locally committed anchors

The approved durable-store recovery seam gains bounded export/import operations
identified by a committed anchor position. Both sides obtain the expected full
prefix from their own format-5 journal and installed committed log proofs. A
donor-supplied digest is never the receiver's authority. This closes the storage
dependency required before original-body release; network scheduling, replicated
release, learner readiness and consumer-proof shared GC remain separate work.

An export must reach the requested anchor's exact Through within explicit bounds
(at most 256 records / 16 MiB); an incomplete page is rejected. Import verifies
the local committed anchor and canonical page while holding append/checkpoint
ownership through the existing atomic content/meter/frontier commit. It accepts
only an exact extension or a fully covered identical retry. Recovery can resume
from an existing prefix, but a prefix inside a pending page is never overwritten.
Neither operation advances HW, source release or ordinary history. No new schema,
log format or consumer state is introduced.

## Failure inventory before implementation

1. A donor supplies a self-consistent forged native field omitted by old log
   hashes, and its own endpoint becomes the expected content digest.
2. A pending, missing, foreign or corrupt local anchor/activation/checkpoint is
   accepted; a stale local copy or caller-selected position substitutes for proof.
3. A bounded export returns a short page that cannot be authenticated by the
   requested end anchor, or silently supplies data after/before that interval.
4. A bad last record leaves partial content; gaps/partial overlaps overwrite the
   local frontier; retries after later progress or restart change accepted data.
5. Deletion of ordinary bodies prevents recovery, or repair recreates ordinary
   history, changes source protection/checkpoints, or acts as learner readiness.
6. Cancellation, closed leases, oversized pages or invalid request bounds escape
   validation; Channel adaptation loses complete content fields or aliases storage.

Tests precede implementation at the already approved durable-store and Channel
store adapter seams. Corruption injection requires storage isolation; these tests
are not full product process E2E acceptance.

## Frozen context

Source `8e425b3c2`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `8b723e6f1402c2361a872e3a6e4021ac4146782ebc9b416cb7b9cc9787bb9d75`
- `pkg/db/message/FLOW.md`: `33632c27eba23319656f10c54524b4ad896a7f362dadab4fbd52d0d044b9ca1f`
- `pkg/channel/FLOW.md`: `1665091b60464b7135b657b00eae5246c603a310426ceeab68a0fbfa567eeaba`
