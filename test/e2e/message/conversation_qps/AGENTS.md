# Conversation QPS release gate

This scenario owns fixed public HTTP load tests for `/conversation/list` and
`/conversation/sync` on real single-node and three-node clusters with 256 hash slots.

Run: `WK_E2E_CONVERSATION_QPS=1 WK_E2E_CONVERSATION_QPS_REPORT=/tmp/conversation-qps.json GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_qps -run TestConversationQPSReleaseGate -count=1 -timeout=18m -p=1 -v`

Keep thresholds and dataset sizes in `profile.json`; do not add environment overrides.
Prepare 600 generated groups through public APIs within five minutes. Use the
existing authenticated `/bench/v1/channel-runtime/evict` endpoint to unload only
those exact generated ranges on every node, respecting its busy-runtime guards.
Preparation may wait at most one minute for busy work to settle; HTTP failures
fail immediately. Do not restart processes or mix recovery traffic into the
measurement. Require zero active runtimes across all roles before and after
every phase and zero additional loads during warmup and measurement. No eviction
runs during reads. Warm disk caches are intentional; this is not a physical
cold-disk benchmark. Round-robin requests cover 24 users and all ingress nodes.
Validate every response's exact persisted message identities. Use scheduled
arrivals, bounded workers and a queue of max(workers, ceil(offered QPS * P99
budget in seconds)) entries. All scheduling/queue delay counts toward latency.
Use no measured retries, and fail on drops, errors,
incomplete pages, low QPS, high P99, runtime loads, membership writes, or missing
metrics. Record CPU, heap and allocations; enforce the reviewed per-request
allocation ceiling for each topology in addition to the QPS/P99 floor.
Only non-Linux local diagnostics may record unavailable process CPU as null;
Linux release runs require the CPU counter on every node.

The JSON report must include all 12 base cases, seven stress windows (eight endpoint results),
and source/profile/binary identities. Also retain driver GOMAXPROCS, bounded host/driver
counter snapshots around stress windows, per-scheduled-second timing/drop buckets
and at most 16 slow samples per endpoint in the original release run. These
observations do not change workload, workers, thresholds or enable profiling.
Local dirty-tree reports are diagnostic only; publication requires clean exact-tag
source evidence. Keep pure acceptance-policy tests in the default unit tier.

Capacity diagnosis is separate from publication:
`WK_E2E_CONVERSATION_CAPACITY=1 WK_E2E_CONVERSATION_CAPACITY_REPORT=/tmp/conversation-capacity/report.json GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_qps -run '^TestConversationQPSCapacity$' -count=1 -timeout=20m -p=1 -v`.
Reuse the exact persisted fixture. Diagnose the representative page-100 case for
both endpoints and topologies; the release gate still covers all 12 page cases.
Use 16 driver workers per node, ten-second
bounded doubling probes (at most seven), one midpoint probe, and a fifteen-second
confirmation. Report the confirmed lower bound and observed rejected rate,
not an exact production capacity. Profile page-100 cases separately using the
existing authenticated debug API, with bounded CPU/alloc profiles per node and
a separate driver CPU profile. Profiled phases are excluded from capacity claims.
Only queue drops, latency limits, HTTP 503 refusals and the exact observed
legacy HTTP 400 head/recent-message backpressure and request-admission envelopes (pinned by unit tests) are expected
overload, and each rejects that offered rate. Other HTTP/transport errors,
malformed or incomplete successful
responses and any runtime activation or membership mutation fail diagnosis.

For a local combined run, set `WK_E2E_CONVERSATION_CAPACITY_WITH_GATE=1` alongside
the release-gate environment and run `TestConversationQPSReleaseGate` with a
20-minute timeout. It runs the same six gate phases first on each topology, then
two unprofiled capacity cases using the same prepared fixture. The report must
contain 12 passing base phases, seven passing stress windows and four confirmed capacity cases. CI does not
set this diagnostic flag and retains the fixed 18-minute gate deadline.

Throughput diagnosis is separate from the gate and capacity claims:
`WK_E2E_CONVERSATION_DIAGNOSIS=1 WK_E2E_CONVERSATION_DIAGNOSIS_REPORT=/tmp/conversation-diagnosis/report.json GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_qps -run '^TestConversationQPSDiagnosis$' -count=1 -timeout=35m -p=1 -v`.
It reuses the exact fixture and capacity staircase for page 100 on both
topologies, then records three successive 60-second windows at each confirmed
rate. Preserve refused windows as rejected evidence, never a stability pass;
unexpected errors, mutations or runtime activation abort the diagnosis. Capture
bounded existing per-node CPU/GC, hydration and RPC metrics around every window.
CPU/allocation and five-second execution-trace captures run separately from
steady load. Trace bodies are capped at 64 MiB per node and use only the existing
authenticated debug API. Prefer Linux for CPU and wait attribution; do not
compare absolute capacity across different operating systems as an optimization
gain. The report records the actual binary, source, profile, driver and node
GOMAXPROCS identities; complete diagnosis does not mean every load passed.

For an equal-load comparison to the 2026-09-12 Linux diagnosis, additionally set
`WK_E2E_CONVERSATION_DIAGNOSIS_FIXED_BASELINE=1`. This skips capacity discovery
and uses the frozen page-100 rates: single-node list/sync 800/240 QPS and
three-node list/sync 1200/360 QPS. The receipt marks `fixed_baseline_load=true`;
its rate fields describe offered comparison load, not newly confirmed capacity.
The fixture, three-minute windows, metrics, profile bounds and failure semantics
remain unchanged. This flag is diagnostic only and cannot alter the release gate.

`WK_E2E_CONVERSATION_CAPACITY_LONG_CONFIRM=1` extends only the diagnostic
capacity confirmation from 15 to 180 seconds, including each bounded halving
retry. Each case records `confirmation_seconds`. Use a 40-minute timeout when
combining it with the gate; the fixed publication phases and thresholds are
unchanged. Preserve rejected attempts, even if a later lower rate passes.

`WK_E2E_CONVERSATION_CAPACITY_FINE_SYNC=1` adds three ten-second page-100,
three-node sync probes at 400, 420 and 440 QPS after the original capacity
confirmation. Confirm passing candidates from highest to lowest for 180 seconds,
stopping at the first pass. Store every probe and rejected confirmation under
`fine_attempts`; only a passing long confirmation sets `fine_confirmed_qps`.
The original attempts and confirmed baseline remain separate. Use a 45-minute
timeout with the combined gate and long confirmation. This fixed diagnostic
refinement never changes the release profile, admission limits or gate verdict.

Fixed backpressure attribution is opt-in:
`WK_E2E_CONVERSATION_BACKPRESSURE=1 WK_E2E_CONVERSATION_BACKPRESSURE_REPORT=/tmp/conversation-pressure/report.json WK_E2E_BINARY=/absolute/candidate GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_qps -run '^TestConversationQPSBackpressure$' -count=1 -timeout=18m -p=1 -v`.
Use Linux with process CPU metrics, three nodes, 48 driver workers, page 100
and the unchanged fixture. Fixed
loads are list 1200 QPS, sync 400 QPS and sync 420 QPS, each in three successive
60-second windows, with no capacity search or retries. Run the exact same harness
once per compared binary, and record any build overlays and hashes separately;
source HEAD is only the checkout base, not proof of overlay contents. Record
bounded per-node persisted-read histogram buckets and RPC/CPU metrics. Separate
CPU/alloc/trace phases never count as capacity evidence. Driver scheduling/queue
wait and HTTP-request percentiles are reported separately and cannot be added.
Recognized refusals reject their window; all windows remain visible. Complete
collection does not imply stable capacity, and it cannot change publication gates.

Peak confirmation uses the same attribution harness with two fixed unprofiled
180-second windows, list 1200 QPS and sync 420 QPS:
`WK_E2E_CONVERSATION_PEAK_CONFIRM=1 WK_E2E_CONVERSATION_BACKPRESSURE_REPORT=/tmp/conversation-peak/report.json WK_E2E_BINARY=/absolute/candidate GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_qps -run '^TestConversationQPSPeakConfirmation$' -count=1 -timeout=12m -p=1 -v`.
Run each exact variant three times with alternating/counterbalanced order,
fresh clusters and the unchanged fixture, 48 driver workers and shared admission
limit. Record binary hashes and any old-source overlay. Each endpoint window is
uninterrupted and has no profiling, retries, rate halving or threshold overrides.
The long_confirmation receipt distinguishes this from three one-minute windows.
Recognized refusals remain rejected windows even when collection completes;
unexpected failures, activation or writes abort. Run the unchanged release gate
separately on the selected candidate. This is diagnostic, not publication evidence.

Sync-prefix comparisons may isolate the unchanged sync 420-QPS workload:
`WK_E2E_CONVERSATION_SYNC_PREFIX_CONFIRM=1 WK_E2E_CONVERSATION_BACKPRESSURE_REPORT=/tmp/conversation-prefix/report.json WK_E2E_BINARY=/absolute/candidate GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_qps -run '^TestConversationQPSSyncPrefixConfirmation$' -count=1 -timeout=9m -p=1 -v`.
This fixed preset sets `sync_only=true` and `long_confirmation=true`, retains
the peak harness's fresh three-node fixture, 48 workers and uninterrupted
180-second window, and omits the unrelated list workload. Run three
counterbalanced old/new pairs and the unchanged 12-case gate separately.

Mixed and hidden diagnosis is opt-in:
`WK_E2E_CONVERSATION_STRESS=1 WK_E2E_CONVERSATION_STRESS_REPORT=/tmp/conversation-stress/report.json WK_E2E_BINARY=/absolute/candidate GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_qps -run '^TestConversationQPSMixedAndHiddenDiagnosis$' -count=1 -timeout=25m -p=1 -v`.
The `stress_diagnosis` section in `profile.json` fixes a three-node, shared-start
600-second list-600/sync-200 window with 24 workers per endpoint (48 total).
Endpoint latency/refusal results stay separate; CPU and allocations are recorded
once for the shared window and must never be duplicated into endpoint costs.
Then use public `/conversations/delete` calls to hide exact generated memberships,
evict the fixture again, and measure first-page-100 and second-page-50 syncs at
20 QPS for 30 seconds each with 48 workers. Cohorts use alternating 50% hidden,
90% hidden, and exactly 99 visible then 100 hidden then one visible membership in
activation/ID order. Verify exact ordered Channel IDs and every recent identity,
including the legitimately empty second page of the 90%-hidden cohort.
Setup mutations are outside measured windows. All reads must remain disk-only,
with zero runtime loads/residency and zero membership writes. Recognized overload
fails that window but remains in the diagnostic receipt; unexpected failures
abort. Compare exact old/new binaries with the same harness. Thresholds for any
release extension must be supported by completed diagnostic evidence.

`WK_E2E_CONVERSATION_STRESS_STAGE=mixed` or `hidden` isolates one fixed diagnostic
stage on a fresh cluster; omission runs both. Preserve earlier rejected windows
when adjusting error-envelope attribution; recognized refusals still fail.
Publication always runs the `stress_gate` preset: mixed list 200/sync 60 QPS for
60 seconds, eight workers each; six hidden windows at 20 QPS for 15 seconds with
eight workers. The v2 report binds this preset and requires all seven windows.
Allocation ceilings apply to the whole shared window, divided by its total
successful requests; never duplicate CPU or allocations into mixed endpoints.

Mixed release-load attribution is diagnostic only:
`WK_E2E_CONVERSATION_MIXED_ATTRIBUTION=1 WK_E2E_PRODUCT_SHA=<exact-product-commit> WK_E2E_BINARY=/absolute/product WK_E2E_CONVERSATION_MIXED_REPORT=/tmp/conversation-mixed/report.json GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_qps -run '^TestConversationQPSMixedAttribution$' -count=1 -timeout=12m -p=1 -v`.
Require four visible Linux AMD64 CPUs. Preserve three fixed 60-second windows
using the unchanged release mixed preset and HTTP connection bounds. Record
harness and product identities separately, retain per-scheduled-second timing/drop
buckets and at most 16 slow arrival samples per endpoint, plus host/driver counters around
each window, then capture bounded server allocations and simultaneous server
and driver CPU profiles during a separate ten-second workload. Rejected windows
remain evidence, unexpected failures or runtime/membership mutations abort, and
collection completion never qualifies publication. The read-only
`conversation-qps-diagnose.yml` verifies the optional exact prior binary digest.

Message-update same-host Linux diagnosis is opt-in:
`WK_E2E_MESSAGE_UPDATE_SOAK=1 WK_E2E_MESSAGE_UPDATE_REPORT=/tmp/message-update/report.json WK_E2E_BINARY=/absolute/product GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_qps -run '^TestMessageUpdateThreeNodeSoak$' -count=1 -timeout=12m -p=1 -v`.
Use the unchanged 600-group fixture, three real processes, 256 Hash Slots,
12 physical Slots, three replicas, and node GOMAXPROCS=2. Preserve three
60-second mixed windows at the release mixed rates (list 200, sync 60 QPS),
with the existing bounded driver and exact response checks. Compare the same
compiled harness and resource limits against exact baseline/candidate binaries;
record dirty source manifests and binary hashes. Set
`WK_E2E_MESSAGE_UPDATE_EDITED=1` only for the candidate: edit every tail once
through public APIs before measurement, retaining 256-byte payloads and the
original identities/order. Then measure changed-page and empty-page delta reads
at 100 QPS for 60 seconds each, validating cursor coverage and edited contents.
These fixed-cursor replays measure server read cost, not recommended SDK traffic.
Collect bounded CPU and allocation profiles during a separate ten-second mixed
load after the measured windows. Preserve rejected windows; completion is not
acceptance. This diagnostic neither changes release thresholds nor establishes
multi-host production capacity, online notification fanout, or sustained edit
write capacity.

The delta diagnostic separately records selected-channel cold recovery: checking
the retained original uses committed reads and may load that channel runtime.
Warm exactly that channel before its steady windows, record the recovery time
and load/residency changes, then require zero additional runtime loads and
membership writes with stable residency during each delta window. Preserve the
original zero-residency invariant for every conversation-only window. Evict
fixture runtimes again only after delta measurement, before separate profiles.

For changed-delta CPU attribution, also set
`WK_E2E_MESSAGE_UPDATE_DELTA_PROFILE=1` with the edited diagnostic. Preserve the
entire fixture, three mixed windows and both 60-second delta windows. Only the
subsequent separate ten-second profiling workload changes to changed-delta
100 QPS with eight workers, retaining the already recovered channel. Mark this
choice in the receipt; enforce exact edited content, zero errors/drops, no
additional runtime loads or membership writes, and stable residency. Profiles
remain bounded to eight CPU seconds and 32 MiB per body. Compare before/after
binaries in counterbalanced order on fresh clusters, with at least three runs
each and identical assigned resources; record all runs rather than selecting
the fastest one. This is diagnostic evidence, not a release gate.
