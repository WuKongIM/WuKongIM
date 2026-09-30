# Product Gateway pipeline activation

The default product Gateway now uses the deferred message preparation port and
one node-owned ordered Channel submitter over the existing Router. Permission
and hook preparation stays ordered, Channel submission preserves canonical-key
FIFO dependencies, and Gateway publishes ACKs in physical-session order. Success
still requires durable append. The original Gateway admission limits cover
queued, executing and completed-but-unpublished records throughout this work.
This is an activated candidate, not a claim that the original EOF or R2/R6 passes.

## Bounds and lifecycle

The app derives workers, records and payload capacity from normalized existing
Gateway settings; multiplication overflow fails configuration. There is no new
config knob, per-SEND goroutine or second ingress allowance. Explicit injected
message/gateway handlers keep their existing execution behavior. The access
wrapper shares the original presence, terminal and shutdown handler bindings;
prechecks and error mapping are common to joined and deferred entrypoints.

Stop/rollback joins Gateway publication, closes ordered submission workers, then
drains append and dependent runtimes. Constructor failure and stop-before-start
also release workers. Maintenance Pause fences and joins callbacks without
terminating the generation; Resume cannot reopen a closed or non-idle owner.

A test-first integration failure exposed premature dependency restart after a
canceled restore suspension. Resume now joins the same paused owner before
restarting dependencies. Timeout keeps the fence and accepted work intact. The
Group remains responsible for durable writes outliving routing-result callbacks.

## Functional validation

Failure contracts preceded the implementation. Access tests cover aligned
mapping, prechecks, callback cardinality, core-only publication and session seal.
App tests cover default/injected composition, constructor cleanup, stop timeout
continuation and failed-restore drain continuation. Ordered maintenance tests
exercise fencing, timeout, idle resume and permanent close.

All related Linux arm64 component integration tests passed with race detection
five times. Default app, access/gateway, message, channelappend and gateway subtree
tests passed. The Linux product binary is frozen in `preflight.json`; the source
fingerprint is over its 22 changed/overlay files, with HEAD fd1580ed1 as the base.

The first single-node E2E failed before SEND with ReasonAuthFail. The previous
product binary fails the identical harness too: the fixture omitted credentials
while default authentication is enabled. The test now registers its device Token
through public HTTP and connects with it; both real-process single-node scenarios
pass without weakening auth. The harness rebuild has its own binary identity.

The full default send_ban E2E package passed: nine top-level scenarios, including
single/three-node policy matrices, four Gateway UID/Channel distributions,
non-replica ingress, unavailable Slot precedence, manual leadership transfer,
concurrent metadata/write recovery and rejection without delivery. The opt-in
100,000-member scenario was skipped and is not claimed as rerun here. Public
JSON reports are retained alongside the full process test log.

Three-node TCP wire probes also passed for 1,200 person and 1,200 group SENDs,
checking ACK client sequence, sender input order and recipient message sequence.

FLOW index regeneration and the named flow-doc-contracts check pass. Existing
100-line advisory targets are exceeded in app/channelappend FLOW; the documented
SHOULD deviation retains existing lifecycle/routing contracts and adds the new
ownership boundary rather than dropping related invariants. No validator changed.

## Pressure validation status

The frozen next diagnostic is a zero-hold control followed by the same 25–30s,
380ms message-WAL completion floor at 5,000 Channels / 4,500 SEND/s / batch cap 32.
The actual Fdatasync and original error still execute once. A separate diagnostic
binary reuses the bounded WAL and runtime flight probes; the obsolete historical
Gateway recorder is omitted to avoid replacing the new core with old source.
No builds, offline parsing or cleanup may overlap measured SEND. The clean
production binary is separate from this diagnostic. Short diagnostics do not
replace uninterrupted clean 30-minute R2 or three fresh complete R6 pairs.

Artifacts: `assets/send-ban-product-pipeline-20260928/`. All failed runs remain
retained alongside later results; root-cause and qualification claims must follow
the actual results, not the component checks above.

## Original-load result: still failing

The zero-hold control passed 60s / 270,000 SENDs, P99 56ms, zero pending and
permission busy. The 380ms completion-floor arm failed with EOF at 26.451425s,
119,031 calls, 6,217 pending and permission busy=0. Node 3's failure snapshot
contains async_dispatch_queue_full=1. All three nodes completed three padded
syncs and had a fourth active at capture; actual completed Fdatasync durations
were 0.796–6.105ms, padded to 380.009–381.532ms. No OOM or disk-guard exit.
This reproduces the original symptom: activation alone is insufficient.

Post-failure goroutine snapshots show all 128 ordered-submit workers per node
in single-item Router calls: local/remote 35/93, 63/65, 34/94. The full captured
traces contain completed and explicitly unfinished submitter waits around
1.42s. These snapshots and top-N intervals are not a unique first-rejection
request join; they do not measure completed-but-unpublished records separately.
Source confirms each queued ready job still executes as an independent Router
call. The next falsifiable intervention is to coalesce already-ready independent
jobs within the existing record/byte micro-batch targets, preserving each job's
callbacks and canonical dependencies. Do not enlarge queues/workers/timeouts.

All eight control/fault traces passed complete Go trace parsing and verified
lossless gzip roundtrips; fault traces additionally retain full-stream event
counts and bounded wait summaries. Raw copies were removed only after byte/hash
verification; compressed evidence remains. R2 and R6 remain unqualified; no clean
30-minute run was repeated after this failure merely to seek a passing outcome.
