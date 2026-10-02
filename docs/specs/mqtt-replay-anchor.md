# Replicated MQTT replay content anchors

An explicit format-5 internal proposal records the complete immutable shared
prefix accepted by Channel copy coordination. Its fixed version-1 payload binds
the format-4 source command, immutable StartAfter, copied Through, cumulative
accounted/stored byte counts and SHA-256 prefix digest. Through precedes the
control's own log position. Ordinary business format selection stays 1–3, and
format 4 retains its exact activation payload and hash domain.

Message System ID 14 is a journal keyed by the control's log position. A
key-bound version-1 envelope retains its canonical original row, independently
of history retention. Appending or replacing an exact proposal writes this
journal atomically. A bounded point read requires the committed checkpoint,
source activation and complete proposal/entry proofs; pending records never
become accepted anchors. Prefix retention keeps journal evidence; suffix
replacement removes only uncommitted journals. Portable backups omit pending
anchors and reject missing, orphaned, malformed or mismatched committed entries.

An anchor does not change System 12, prove a learner ready, or release originals.
The runtime still must admit the current copy receipt before creating this
control. After GC, receivers may use a locally committed anchor as independent
expected content for replay import. Historical anchor payloads cannot be supplied
by the transfer sender as their own authority. MQTT product admission remains
unavailable until the complete recovery/delivery flow is connected.

The control is itself an internal source position. Copy scheduling must stop
when the only uncovered suffix is the latest anchor control; a new business
append can cover it with the next page. It must never create an idle infinite
sequence of anchors solely to cover the preceding anchor.

## Failure inventory before implementation

1. Business records acquire control semantics merely by matching payload bytes;
   old formats change hashes; malformed/future payloads, multiple control rows,
   foreign source incarnations or a Through covering the control itself pass.
2. Exact append writes a control without its journal, publishes a pending anchor,
   accepts missing source protection, or advances source release as a side effect.
3. Retention deletes the only complete-content anchor; restart or replica replay
   changes it; a sender's forged summary becomes independent recovery authority.
4. Journal corruption, missing proposal/entry pairs or a checkpoint below the
   control yields a valid read. Suffix replacement retains an orphan journal.
5. Backup includes pending controls, omits committed anchors, accepts orphaned or
   changed journal content, or preserves a journal without its durable proof.
6. Unsupported stores acknowledge format 5, exact retry crosses control/business
   domains, or native quorum/recovery transport drops the explicit control kind.

Tests precede implementation. Format/storage evidence alone is not product MQTT
acceptance or current-copy receipt admission; those paths remain explicit work.

## Frozen context

Source `776a843a0`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/channel/FLOW.md`: `d93461d1bbb58e6cef4f05a77eecab5e1ca431e2221fb1081fa95fb05366d261`
- `pkg/db/FLOW.md`: `8b723e6f1402c2361a872e3a6e4021ac4146782ebc9b416cb7b9cc9787bb9d75`
- `pkg/db/message/FLOW.md`: `672471325349894a23a749f6425ee0f8be6f6526acd5bb63feda234a6cc19a9a`

Backup review found another proof obligation: a format-5 manifest cannot be
paired with a version-1 entry digest, even if all other identities and the tail
digest match. A regression fixture must reject that downgrade before importing
any rows; proposal and entry versions must agree for every backup format.

Recovery staging must use the replacement batch's source incarnation when it
replaces an uncommitted activation and installs an anchor in the same atomic
batch. Reading the old engine marker alone would incorrectly reject a valid
replacement; a regression covers this before the staging fix.

## Remaining admission and recovery integration

The sequencer admission primitive now reads the latest committed anchor and
validates exact command retries (see [admission](mqtt-replay-anchor-admission.md)).
The reactor/fresh cluster facade must chain the next copy page
from that accepted prefix (or the activation start), and recheck the receipt's
exact current membership before ordered control append. This keeps every newly
accepted recovery interval within the existing row/byte bounds even if local
copy preparation has run ahead. A repeated accepted prefix must reuse committed
proof rather than append another control, and an anchor-only tail must stay idle.
Current-copy validation exists in the typed sequencer port; fresh cluster entry
wiring, release decisions, post-GC donor repair and learner/migration readiness
remain required beyond the format/storage primitive.
