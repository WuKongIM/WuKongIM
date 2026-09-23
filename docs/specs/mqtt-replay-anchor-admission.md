# Ordered MQTT replay anchor admission

The durable Channel sequencer admits copy evidence under its existing per-Channel
mutex. A typed optional facade requires exact current metadata, a current-voter
copy receipt, and a server-allocated control ID/timestamp. The cluster entry must
obtain metadata freshly before calling it; a receipt alone is not authority.
The sequencer independently compares the installed leader, epochs, route, voters,
learners and quorum, then checkpoints only its own recovered committed frontier.

A pinned bounded store read returns the source, latest committed anchor and an
optional exact command proof. A separate anchor command domain binds source and
accepted Through, permitting retry after restart, later anchors, original-body
retention and a changed control MessageID. The sequencer checks complete content
and source identity before reusing that proof. New acceptance must chain exactly
from the last accepted prefix, even when local replay copying ran ahead. An
uncertain pending anchor is retried with its original immutable proposal.

The journal supports one reverse seek and bounded point verification; no history
scan, new index, per-Channel goroutine, or timer is needed. When the only uncovered
suffix is the preceding anchor itself, admission returns that existing proof and
does not append a new control. New business content may include that position in
a later bounded interval. Neither admission nor its checkpoint releases originals.

## Failure inventory before code

1. Foreign/duplicate/unsorted acknowledgements, a learner vote, absent leader,
   insufficient quorum, changed membership/route/status or malformed counters
   become accepted current-copy evidence.
2. A caller-selected HW checkpoints uncommitted data; a pending journal becomes
   visible; source and anchor reads straddle generations; a historical command
   proof depends on original row retention.
3. Copy Before skips the latest accepted prefix, conflicts on digest/counters,
   belongs to another source, or covers uncommitted positions.
4. Concurrent callers append duplicate anchors; changed retry IDs create new
   controls; restart or leader change loses exact idempotency; uncertain commits
   substitute new row identities instead of retrying the pending proposal.
5. An anchor-only tail produces an infinite chain of internal controls.
6. Failed quorum returns a proof, unsupported stores accept admission, lease or
   snapshot cleanup leaks on errors, cancellation or panic.

Tests precede code. Runtime integration uses disk-backed voters and the actual
exchange codec. This is not fresh Slot routing, reactor/app wiring, automated
source release, learner shared-content readiness or product MQTT acceptance.
Those remain required and must use this same sequencer admission primitive.

## Frozen context

Source `f97570cac`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/channel/FLOW.md`: `2550299c1b67f7f9676a049f68de71f6d793d825df06d2d3e51e5b8572099918`
- `pkg/cluster/FLOW.md`: `211deb63334c56f41268c189b0d77305122e82b084df7f6b23b2ffb7d9783d4d`
- `pkg/db/FLOW.md`: `8b723e6f1402c2361a872e3a6e4021ac4146782ebc9b416cb7b9cc9787bb9d75`
- `pkg/db/message/FLOW.md`: `c6cfb564ac685b601bb5908c6179d835af54a39603c6392be08dd7f6165a81ba`

Admission review adds one regression before its fix: calling the shared locked
commit helper must preserve ordinary proposal byte accounting (the 96-byte row
cost as well as payload bytes). A payload-only limit could admit an anchor over
the configured maximum even though ordinary Commit rejects that size.
