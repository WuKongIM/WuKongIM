# Message preparation and asynchronous append completion

The message usecase now exposes optional `SubmitBatchEach` over an injected
`BatchAdmission`. Permission, directory waves, hooks and hook reauthorization
reuse the synchronous pipeline and join before return. The caller may then
prepare its next batch without joining durable completion. The product gateway
and composition root do not call this interface yet. The original pressure EOF
has not been repaired or requalified; R2/R6 remain incomplete.

## Ownership and ordering

Preparation advances its session prefix on submission or item-local rejection,
not on concurrently changing completion flags. A cold directory head still
fences the next item in that session; independent sessions retain ready progress.
The injected ordered submitter owns canonical Channel execution order and its
fixed budgets. No usecase goroutine, unbounded queue or alternate authorization
path was added.

A separate batch publication owner serializes callback results, retains original
indexes and session order, and joins preparation with all terminal results.
Derived deadline contexts therefore survive admission return and inline hot
completion during cold preparation. Parent cancellation remains attached. The
first emitter error suppresses later emits but still joins accepted ownership
and releases deadline contexts. Completion callbacks run outside the publication
mutex. Admission rejection and wrong result cardinality fail item-aligned.

`SendBatchEach` retains synchronous completion, including its single-item fast
path. Multi-item synchronous and optional asynchronous calls share preparation;
the extra result storage is O(batch size). The publication mutex protects one
batch, not all sessions on a node. Original-load CPU/allocation/latency validation
is still required before activating the new path.

Gateway must retain its overall and shard reservations for queued, executing and
completed-but-unpublished items, order ACKs across batches, and include this work
in drain. The usecase's batch-local result ordering does not supply those rules.
App lifecycle must drain the ordered admission owner before dependent append and
storage runtimes. This change does not add a second freshness policy or change
permission RPC grouping.

## Tests and evidence

Failure contracts and integration tests were written before implementation.
Initial RED reports the absent interface. It is not the original EOF regression.
The first implementation run rejected a nonexistent group: the test fixture had
membership but lacked the group metadata record. The fixture was corrected to
create that record; product existence/permission checks were not weakened.
Both failed outputs remain archived.

Using Linux arm64 Go 1.25.11 in the existing task Docker container, with a source
overlay and networking disabled:

- Seven `TestSubmitBatch` integration scenarios passed 20 repetitions.
- Those scenarios passed race checks five repetitions.
- Existing `TestSendBatch` plus new `TestSubmitBatch` scenarios passed race checks
  three repetitions.
- Complete default suites for message and channelappend passed.

The scenarios cover preparation return versus pending append, sequential hook
preparation, retained deadlines, cold-prefix fencing, inline and concurrent
completion, within-batch session publication, atomic rejection, malformed result
vectors, missing ports/callbacks, emitter errors, fresh user bans, hook identity
reauthorization and parent cancellation. Existing send-ban regression tests
continue to cover the channel policy matrix. These are module checks, not
process-level E2E or throughput qualification.

`flow-doc-contracts` passed from its named policy entry after regenerating the
index. The message FLOW was already above its 100-line target and is now 125
lines. The documented SHOULD deviation retains the existing history/edit and
permission rules while adding the distinct completion-ownership contract; no
validator was weakened.

Artifacts and exact invocations: `assets/send-ban-async-preparation-20260928/`.
There is no new Changelog entry for this inactive capability; add one when the
new product path is wired and its externally observable behavior is validated.
