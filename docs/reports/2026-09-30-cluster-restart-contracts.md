# Cluster restart and format contracts

## Scope and acceptance

The operator approved merging PR #978 and resolving the two existing cluster
integration failures. PR #978 merged as
`77c8c71dd394402035641d1c42faf164f3155f27`; its clean worktree and original local
branch were removed after verifying that main contains its tip. This repair
starts from that exact revision. Permission aggregation (#977), paid cloud
resources and 4,500 SEND/s qualification are outside this task.

Required outcomes:

- The same Node can Stop/Start repeatedly, rebuild owned dependencies, retain
  borrowed adapters, and preserve durable messages and sequence allocation.
- Failed startup releases owned adapters so a retry can reconstruct its runtime.
- A three-node cluster can reuse the same Node objects, recover authoritative
  metadata RPC and native quorum exchange, and persist the next record on all
  replicas with 256 hash slots.
- Registered data reopens with unchanged creator provenance. Nonempty data
  without its format marker is rejected without adoption or file writes.

## Diagnosis

Both original failures reproduced with `-race -tags=integration` on the merged
base. `TestNodeDefaultChannelsUseDurableMessageDBStore` was a product lifecycle
bug: Stop closed the Slot runtime but retained the proposer. The next Start
therefore skipped Slot construction. Default task handlers and Slot status
readers also retained their previous owners; the quorum gateway survived its
transport server. Startup rollback retained some of the same references.

Test-first regression probes failed on these references and on three-node
restart. Ownership flags now distinguish constructed proposer/task adapters
from borrowed ones. Stop and rollback release only constructed adapters, and
transport disposal resets both Channel gateways.

Review additionally exposed a construction-error path before the startup cleanup
defer. A new failing-path integration first blocks the Slot metadata directory,
proves owned Controller/transport retention, then verifies cleanup and successful
same-object retry after removing the blocker. Rollback is now installed before
default runtime construction.

The expanded three-node probe then isolated two further effects. Its existing
five-second readiness budget coincided with the default five-second health
renewal interval. The fixture now renews every 100 ms while retaining that
deadline; production defaults are unchanged. After readiness passed, metadata
RPC returned `db: closed`: pending handlers from the old Slot proxy had been
registered before its replacement was constructed. Registration now follows
Slot/proposer construction, so handlers bind the current metadata store.

The repeated single-node fixture now demands a real write-readiness proof within
its original five-second readiness budget. Snapshot readiness alone can precede
leader election; using it before a separate two-second placement poll produced a
deadline failure on the second generation. No production deadline is changed.

`TestDataFormatSingleNodeClusterReopen/unregistered` had an obsolete expected
result. The production contract already rejects nonempty unregistered data.
The corrected integration removes the marker only after Stop, expects
`ErrUnsupported`, and compares streaming SHA-256 hashes of all opaque fixture
files before and after rejection. The registered branch still verifies history,
next sequence and byte-identical format provenance. Format behavior is unchanged.

The full suite also exposed an existing probe-count race. The unchanged main
test failed once in 20 repetitions: its baseline log contained only the initial
configuration entry, then both the leader-election empty entry and the first
write probe appeared in the measured delta. The integration now waits for the
election entry to commit and apply before capturing its baseline, retaining the
strict assertion that 20 probes append exactly one 12-byte command. Production
probe behavior and the existing two-second polling deadline are unchanged.

The next full run passed the unchanged pressure gate (P99 1.456 s) but exposed
five-second startup fixture failures, including the registered format branch.
Two existing seed/control-write tests also called Stop from cleanup before all
concurrent Start calls had returned after a timeout; race stacks identify that
test orchestration overlap. All three copies of that loop in the same file now use the existing `startNodes` helper,
which joins every start result before failure cleanup. The 12-physical-Slot
format fixture uses that same bounded 20-second startup helper instead of the
five-second single-node helper. This is a documented test-budget correction:
these scenarios assert format/transport behavior, while production startup
readiness retains the independent 30-second process E2E deadline. Subsequent
write-readiness and Controller convergence budgets remain five seconds.

## Validation and evidence

Real runtime integrations are necessary for same-object restart: restarting a
process constructs a fresh Node and cannot expose retained object references.
They use real storage, TCP and quorum exchange, and emit bounded JSON lifecycle
facts without credentials or decoding storage. Process-level startup recovery
E2E supplements this seam with public HTTP and WKProto checks.

Evidence is retained outside the task worktree in
`tmp/baseline-integration-repair-20260930/`, including frozen instruction digests,
merge/cleanup receipt, original and strengthened red tests, intermediate probes,
and validation receipts.

- All nine related regression cases pass in the latest full race-enabled cluster
  run: repeated durable restart, three-node quorum restart, both format branches,
  Stop/failed-start release, construction failure/retry, borrowed adapters and
  probe reuse. The probe-count correction also passes 20 repetitions.
- All unit tests under `pkg/cluster/...` and `pkg/dataformat` pass with `-race`.
- The named `flow-doc-contracts` check passes (81 valid FLOW files); its existing
  length-target warnings remain advisory. Cluster FLOW remains above the
  100-line target to preserve its existing cross-domain navigation, within the
  mandatory 150-line limit. Its generated index is updated.
- The first completed repair run failed the unchanged
  `TestThreeNodeSlotElectsAfterControllerAndSlotLeaderStops` three-second election
  deadline. The same failure reproduces on exact main: Controller leadership
  recovers while Slot observers still report the stopped leader at that deadline.
  All other cases pass and no data race is detected. The main comparison is
  retained separately; that initial validation did not establish a fully green integration
  suite or qualified throughput.

`precommit-validation.json` records the tested cluster Go-source tree hash and
the full-suite limitation. For committed-source acceptance, use one clean
`cmd/wukongim` binary with the existing
[startup recovery scenario](../../test/e2e/cluster/startup_recovery/AGENTS.md) and
[Controller bootstrap scenario](../../test/e2e/control/bootstrap_task/AGENTS.md).
Preserve their public protocol/HTTP gates and the original startup deadline;
record the exact revision, binary SHA-256 and outcomes in `final-validation.json`.

## Follow-up: randomized election window

After delivery, the operator asked to continue with the remaining failover
failure. The existing test reproduced three failures in ten runs at clean
`5428aa57811201e20a35a10fce0f1c000c01938b`. A bounded diagnostic retained the
original three-second verdict while observing recovery for up to five seconds.
Three of ten runs recovered after the original deadline at 3.196, 3.584 and
3.273 seconds. At three seconds, direct `FreshStatus` on the owning Raft worker
matched the cached status, excluding stale publication in those observations.

The pinned `go.etcd.io/raft/v3` v3.6.0 implementation uses
`ElectionTick + random(ElectionTick)` before campaigning. Slot defaults of
50 ms and 40 ticks therefore wait between 2 and 3.95 seconds; a three-second
test deadline is inside the legal window. The symptom is an obsolete fixture
budget, rather than evidence requiring a production election-timing change.
These observations do not establish that runtime scheduling has no delays.

The integration now derives its recovery bound from two election intervals plus
one bounded second for voting, publication and durable apply (five seconds for
current defaults). It keeps real default Slot timing and uses 256 hash slots.
Recovery must also complete a normal Node proposal in a newer term, capture its
committed target through bounded owned-worker `FreshStatus`, and advance
committed/applied indexes on both surviving replicas within the same deadline;
leader IDs alone are insufficient. Its existing lifecycle reporter retains
bounded before-stop, after-stop and post-quorum facts.

Follow-up evidence is retained in `tmp/slot-failover-repair-20260930/`: exact
instruction digests, unchanged-repeat receipts, diagnostic probe/diff, and the
updated validation receipts. The temporary diagnostic was removed. An initial
strengthened-test attempt omitted the normal proposal envelope and failed with
`proposal payload too short`; that invalid test setup is preserved separately,
then corrected to use the normal Node proposal boundary. It is not product-bug
evidence. The first follow-up gate passed twenty race-enabled repetitions in
2.064–3.591 seconds per recovery and the full cluster integration suite (365
parent cases, 288.673 seconds), including the original nine regressions, plus
cluster/dataformat unit-race tests.

Spec review then identified that async apply can resolve a proposal before the
worker refreshes cached commit status. Using that cached commit index as the
quorum target could accept a follower that applied only the election entry.
The final proof reads `FreshStatus` on the owning worker after the successful
normal proposal, ensuring the target includes that proposal. This is an
acceptance-test correction, not evidence of another production defect. The
initial Spec finding and validation are retained; final repeated/full-suite
results and committed-binary process acceptance are recorded separately in the
follow-up `precommit-validation.json` and `final-validation.json`. No throughput
qualification is claimed.

## Follow-up: Controller ingress during startup

The final-source full race suite at `2ea30c11a` did not pass. One run hit the
existing 20-second startup bound in the Channel repair fixture; its isolated
three repetitions passed without changing the bound. The next full run passed
364 parent cases but caught Controller startup publication races in
`TestMessageUpdateThreeNodeQuorumAndLeaderTransfer`. Its standalone ten-repeat
loop reproduced the same races once, retaining the actual failure.

The restarted node is reconstructed after Stop completes, and its replacement
transport starts before Controller.Start. Incoming Raft Step therefore reads
the runtime service pointer while startup assigns it; the pointer also exposes
constructor fields without a synchronization boundary. A tighter integration
written before the repair delivers bounded Step, state-sync and leader reads
while eight sequential real voter Start/Stop cycles reopen durable Raft state.
It reported thirteen races in about 0.2 seconds, including state-sync endpoint
publication and the FSM pointer read by its existing callbacks.

The repair uses the existing Controller state lock to publish resource pointers
and snapshot ingress pointers. Each call releases it before queue waits or FSM
snapshots, preserving their cancellation and concurrency. Mirror client
publication and voter preparation cleanup use the same boundary. There is no
new global mutex, transport ordering change, election timing change or parallel
Start/Stop contract. State-sync callbacks still reject an absent current FSM;
they no longer read its pointer without synchronization.

Exact repair checks in `controller-race-repair-validation.json` passed twenty
ingress repetitions (160 reopen cycles), ten original message-update repetitions,
all 90 Controller integration parent cases with race enabled, and the cluster,
Controller and dataformat unit-race suites. Full cluster and committed-binary
process acceptance are retained separately after this additional product fix.
The earlier passing binary receipts belong to `2ea30c11a`, not this repair.

The automatic mixed-send CI at `2ea30c11a` failed during warmup with 82
ReasonNodeNotMatch SENDACKs, before the measured performance phase. This code
can map multiple transient failures to that reason; its root cause is not
established by the reason count. Preserve the artifact and failed-job receipt;
do not describe this as a P99 threshold failure or reuse the previous head's
green performance result. Draft status remains until current validation permits
review. No qualification threshold or scenario is changed.
