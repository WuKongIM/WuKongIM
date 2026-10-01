# MQTT sealed Started rejection

The exact non-dispatch terminal case is implemented. A Started Will whose owning
node durably seals its unissued attempt can finish Rejected/Sealed after current
permission explicitly denies publication. It preserves the original executor,
server identity and frozen bodies and reserves no successor, including when the
journal is full. Unknown issued, legacy and lost-journal outcomes remain pending.
This narrows the open Started recovery obligation; it does not close the complete
MQTT or aggregate shared-storage target.

Source base: `3f64b984eebb07035091dc11381f79796cbf1f66`. The
[spec](../../specs/mqtt-will-sealed-rejection.md) records the pre-implementation
failure inventory and approved seams. [Frozen context](source-context.json) and
[provenance](provenance.json) contain exact instruction, fixture, source and binary
digests, toolchain/environment and commands. Execution used Go 1.25.11 on Darwin
arm64. Builds used `GOFLAGS=-buildvcs=false`; no product dependency or config changed.

## Red, green and process evidence

The cap-one Reserved tracer on the previous committed runtime failed on the legal
Will's 90-second business-receipt deadline after the same captured executor
actually refused its full journal. The test completed in 100.53 seconds; this
is product RED, not a missing control. The first metadata test separately failed
because the old validator rejected the new terminal shape. Both logs and the
original process fixture are retained. The candidate tracer passed in 76.05 seconds.
Only then was the fixture extended to Admitted and both cluster topologies.

The final single invocation passed 4/4 in 330.445 seconds:

| Cluster | Unissued cut | Executor node | Case seconds |
| --- | --- | --- | --- |
| single-node cluster | Reserved | 1 | 76.14 |
| single-node cluster | version-2 Admitted | 1 | 76.78 |
| three-node cluster | Reserved | 2 | 88.13 |
| three-node cluster | version-2 Admitted | 2 | 88.60 |

Every final receipt under `final/` confirms 256 hash Slots, one logical Slot group,
actual capacity refusal on the captured executor, exactly one legal publication
attempt/receipt, no revoked publication, no CONNECT retry and no resubscription.
After permission restoration the exact executor group was SIGKILLed, joined and
restarted. The persistent recipient reconnected once with Session Present=1 and
remained healthy and quiet for 21 seconds. Reports ran after joined cleanup.
The three-node fixture has three voters; the explicit single-group choice keeps
its pressure on one actual execution journal and does not qualify multi-group load.

## Related validation

- Full race suites passed: metadata 40.357s, Slot FSM 17.452s, Session usecases
  154.720s and app wiring 5.962s. Fast isolated storage coverage was necessary to
  reject impossible restored shapes independently of product workflows; it was
  written before implementation and covers exact tuple/body/decision/expired-grant
  conflicts, historical phases, exact retry, recovery-index removal and snapshot
  preservation through the approved CAS/snapshot contract.
- Ordinary Will Delay/publication/normal cancellation passed both topologies
  with the ordinary binary in 54.386s.
- The four existing Will wiring integrations initially passed 3/4. The empty
  Webhook case failed with `slot/proxy: stale read route` at
  `sessions.Disconnect`, before any WillExecutor invocation. Its unchanged isolated
  race repeat passed in 5.240s and the whole four-test repeat passed in 16.516s.
  The initial failure is retained; its cause is unqualified and no unrelated
  route repair is claimed.
- Named `flow-doc-contracts` passed: 88 compliant, zero invalid, 11 line-target
  warnings. The generated index was refreshed. Existing long navigation files
  retain their module context; four concise usecase proof lines are kept instead
  of undertaking unrelated trimming in this safety change.
- Issued, legacy-Admitted and positive-receipt recovery passed 6/6 across both
  topologies in 245.726s using the correctly instrumented related build. Issued
  and legacy cases retain zero new attempts after exact restart; positive
  recovery retains the original unfinished PacketID/MessageID/sequence with DUP.

The first two related-recovery invocations had insufficient temporary build
coverage: four candidate cases in each failed at `WaitListed`, before business
work; two legacy cases in each passed using the frozen version-1 binary. The
second build added `internal/infra/cluster`, but the three append controls belong
to `internal/runtime/channelappend`. The final build includes that package and
its generated controls were verified before rerunning. All initial logs/receipts
are retained as instrumentation failures, never product RED or completed recovery.
The sealed matrix uses its original sufficient candidate binary; the related
build has the same product runtime changes with additional fault-control packages.

## Standards

Final Standards review: **0 documented violations; 0 actionable smells**.

Runtime and schema safeguards remain exact and bounded. Tests preserve approved
boundaries, joined cleanup and repeatable artifacts. Changelog, knowledge, FLOW,
catalog and progress updates accurately describe this limited recovery case and
compatibility requirements.

The reviewer independently verified all 13 frozen instruction digests, the new
scenario instruction digest, four source hashes, 24 lossless archives, 26
consolidated receipts and 13 raw log summaries. Recorded validation confirms the
four-case matrix, related recovery, race suites, ordinary Will tests and named
FLOW check passed. Initial instrumentation and wiring failures remain clearly
distinguished from successful repeats.

## Spec

Spec final recheck: **0 actionable findings**.

Implementation and final evidence satisfy the sealed-rejection contract. The
four sealed cases and six related recovery cases passed; issued/legacy attempts
remain pending, while positive receipts preserve recovery identity. Ordinary
Will and related race checks passed.

The reviewer independently verified 24 lossless archives, current source/fixture
hashes, frozen instruction hashes and six binary hashes. Earlier instrumentation
and stale-route failures remain accurately reported. Compatibility requirements
and cap-one, one-group, Darwin limits are explicit. Complete unknown-effect
recovery, shared-storage admission and full MQTT qualification remain open.

Review totals: Standards 0 (worst: none); Spec 0 (worst: none).

## Repeatable byte verification

Logs and generated source are retained as lossless gzip archives.
`raw-archives.json` records original/archive byte lengths and SHA-256 digests.
`manifest.json` covers the delivered source, instructions, docs and evidence,
excluding itself. Verify files with
`python3 docs/reports/mqtt-will-sealed-rejection/verify-evidence.py`, or verify
committed Git blobs and archive contents with the same command plus
`--git-ref HEAD`. Verification is read-only.

## Compatibility and limits

Dispatch value 4 uses existing optional column 35; no new table, field, index,
command envelope, queue or worker exists. Shape/transition checks permit only
expired Executing/Started -> Rejected/PermissionRevoked/Sealed on the same exact
tuple and body. Trusted sealing/current-denial evidence remains usecase work.
Only definite expected-revision terminal results permit cleanup; unknown/late
results preserve the journal for authoritative pressure reclamation.

Old binaries reject value 4. All cluster runtimes and tools must match before
using it; rollback needs a pre-feature data generation. Old data remains readable,
with no backfill or mixed-version rollout. Process evidence is bounded to cap one,
one logical group and Darwin. It does not qualify full production capacity,
unknown issued-effect terminal recovery, lost/corrupt journals, arbitrary restore
or Channel deletion, general partitions, shared-storage admission, sustained
throughput, latency SLOs, Linux or complete MQTT activation.
