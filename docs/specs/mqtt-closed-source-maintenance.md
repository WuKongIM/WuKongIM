# MQTT closed-source maintenance

Continue interrupted source sealing and unadmitted backlog release from the
existing Channel-binding recovery index, independently of a live local Owner.
Reuse SourceDrain's persisted boundary, seal and bounded accounting head.

## Failure inventory before implementation

1. Offline or foreign-owned closed subscriptions never drain because Seal needs
   a local Owner; creating a fake Owner or reactivating a Session bypasses fencing.
2. Background cleanup accepts active/preparing intent, another UID, lifetime,
   operation, source or authorization. It must reread exact current authority.
3. Taking over during reads/writes affects a successor; ended Sessions enter the
   wrong cleanup path. Capture one exact Owner and never follow a replacement.
4. Interrupted boundary/cursor/seal/debit writes cause lost debt, duplicate debit
   or moved boundaries. Lost replies must resume from durable stages.
5. Missing cursors are treated as empty active obligations. Only unproved
   preparation may create a cancellation cursor after source protection proof.
6. Accounting release crosses its bounded range, destroys an inflight exchange
   or ACK gap, or requires current receive permission after intent is closed.
7. Invalid/partial/extra read envelopes, malformed receipts, cancellation, clock
   regression, panic or write ambiguity report success or permit another effect.
8. The consumer cohort fails to call closed-source draining, accounts closed
   intent, treats pending range work as completion, or adds per-source workers.
9. Source draining is mislabeled complete UNSUBSCRIBE recovery: final UID drain,
   pending subscription completion and discovery before a first binding still
   require their own orchestration and process-level failure acceptance.

Actual metadata/Session usecases and callback seams test failure boundaries that
the public process does not expose. Existing quota/ACK process cases are regression
coverage, not interrupted-unsubscribe acceptance. No new table/index is needed.

## Implemented composition

`SourceDrain.ReconcileClosed` captures one exact parent Owner/UID from fresh
routed metadata, then invokes the same seal/cursor/debit stages used by foreground
`Seal`. The background scope checks cancellation and the captured identity; it
grants no live execution capability and permits active or offline non-ended
parents only after closed intent is independently checked. Foreground Seal and
SealGroup still require admitted local operations and their original live lease.

`Owners` is optional at construction for this background use; foreground calls
reject a missing registry. There is no synthetic reservation or Session activation.
Read envelopes reject unrelated runtime/admission/membership/directory payloads.
Session mutations retain exact tuple/revision CAS. Cross-Slot binding CAS may
only advance that original closed obligation, never a successor generation.

The existing ConsumerMaintenance cohort invokes draining for a closed or
superseded subscription before progress/removal. Each turn releases at most one
accounting range; pending work stays recovery-indexed and yields without claiming
removal. Ended/replaced lifetimes retain their existing separate cleanup path.
UID qualification handling does not call Channel draining or accounting. App
wires the existing replicated SourceProtector; no new worker, queue, table,
column, command, RPC or configuration field is added.

Unknown preparing boundaries still require exact source-generation protection.
A missing cursor is acceptable only before durable progress/activation/seal.
Background attempts preserve the same fixed end, PacketID, delivery order,
content reference and ACK gaps as foreground retries. At most eight point reads,
two binding CAS writes, one cancellation initialization and one bounded window
mutation occur in a turn. Only successful authoritative rereads complete it.

## Remaining acceptance

This completes scheduling of existing Channel-binding cleanup. It does not
complete the subscription or UID drain checkpoint, discover pending intent before
its first binding, reclaim ended records, grant unavailable-owner proof or
reactivate restored runtimes. Full process-level interrupted UNSUBSCRIBE and
crash/scale qualification remain required.

## Evidence

New failure-first contracts failed compilation before ReconcileClosed and the
required Drain composition existed. Focused contracts then passed in 14.969s;
complete usecase/app race suites passed in 83.502s/4.898s. They cover offline
bounded ranges, lost seal/debit replies, protected cancellation initialization,
malformed/partial authority, closed-intent gating and consumer orchestration.

The three-node race integration passed in 22.205s. After an interleaved ACK
invalidates an earlier release CAS, it disconnects the original Owner and resumes
from node 3 without Owner admission, preserving the outstanding exchange. This
uses real routed Slot/source persistence with controlled establishment/window
fixtures, not a product listener. Eight existing product quota/completion cases
pass in 205.444s across both topologies; they are regression coverage.

Commands, frozen governing context, implementation hashes, integration assertions
and all eight process JSON artifacts are preserved in the
[evidence report](../reports/mqtt-closed-source-maintenance.json). FLOW checks
remain 87 compliant, zero invalid and nine existing warnings.
