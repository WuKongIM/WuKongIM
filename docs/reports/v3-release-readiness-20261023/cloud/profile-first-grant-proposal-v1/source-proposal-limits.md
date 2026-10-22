# First-grant profile proposal — NOT IMPLEMENTED OR EXECUTED

Status: proposal only. No profile, HTTP request, server, fake fixture, compilation or test was run for this task. No repository source changed. This proposal must not block the current exit-8 delivery or turn a harness failure into product evidence. It authorizes no next run by itself.

## One recommended implementation

For a separately authorized next attempt, retain A = full-v2 original workload and configuration, rather than the batch128 intervention. Add a temporary profile-driver shell copy and a small supervisor hook under that new attempt's private /tmp directory. Keep the product binary, workload, launcher durations, ports, cleanup ownership and original exit status unchanged. Freeze the source helper SHA below; retain a reviewable diff of the temporary copy. Do not invoke the existing helper unchanged or fabricate a supported threshold to get past validation.

The capture is one CPU request for service node2, `seconds=10`, then exactly one goroutine request targeted at CPU-request submission +5 seconds. No heap, metrics/context capture, repeat or retry. Actual request dispatch/scheduling skew must be recorded. A curl submission timestamp is not proof of the server's exact CPU sampling start; “fifth second” means that documented local submission offset.

## Existing code that can be copied with its ownership intact

All references below use frozen worktree `/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-loss-diagnosis`.

- `scripts/capture-wukongim-local-threshold-pprof.sh:3-6,109-120,389-401`: umask077, disable inherited xtrace before token work, reject CR/LF/edge whitespace, unexport the private token and unset inherited token before curl, supply Authorization through anonymous process-substitution header FD. Preserve this exactly. Never put token in args, output, manifests or curl child environment.
- `:182-205,208-240`: loopback-only URL validation; safe output claim; one immutable first-trigger claim; existing artifact rejection. Duplicate callers do not repeat requests. Directory parents are physically resolved; this is not a general no-follow tree proof.
- `:263-278,309-368,393-408`: retain every spawned curl ownership record, CAPTURE_REGISTERING/SIGNAL_PENDING race guard, HUP/INT/TERM cleanup, kill and wait all registered children, delete partial temporary files, and publish typed partial metadata. The signal path returns0, so profile result must be read from metadata rather than inferred from process status.
- `:385-408`: connect deadline2s; CPU max-time15s for seconds10; snapshot max-time5s. CPU cap8MiB and goroutine cap4MiB are passed to curl `--max-filesize`. Preserve finite command deadlines and add final size validation before promoting a file. These static flags alone do not prove installed curl's hard streaming byte-limit behavior; a headerless-oversize fake is required. Final size validation alone only prevents promotion, not temporary disk growth. A demonstrated byte-bounded curl behavior or a separately bounded streaming sink is required before claiming a hard byte cap.
- `:435-455`: wait/join curl then promote only a nonempty successful temporary file; failed files stay missing and are removed. Existing check does not decode protobuf/gzip or prove a goroutine document is genuine.
- `:375-383,482-501`: measurement-phase start/end validation. Existing helper does not monitor phase continuously during requests; end check can invalidate a crossing but does not promptly cancel at the crossing. The supervisor hook must signal the temporary driver on terminal/stop, and the driver must recheck immediately before delayed goroutine dispatch.

Static test source exists for authentication secrecy (`..._integration_test.go:98`), one-/three-node once capture (`:168,226`), phase crossing/missed start (`:262,296`), duplicate claim (`:329`), missing blob (`:367`), cancellation/join (`:392`), and registration race across HUP/INT/TERM (`:447`). These tests were READ ONLY, not rerun here, and they target the unmodified existing helper. They do not qualify the proposed new schedule.

## Exact capability gaps; minimum temporary changes

1. **Trigger:** existing enum at shell:121-124 accepts only actual_offered_ratio, sendack_p99, terminal_product_failure. Add a fixed temporary `first_grant_receipt` mode; do not pass a false threshold or label the source helper as already supporting firstgrant. Replace the threshold bracket metadata with a new proposal-specific schema containing receipt hash/schema/generation, receipt.started_at, receipt_seen_utc, capture submission times, node_id2, requested schedule and actual offset. Preserve partial/valid/result vocabulary clearly within that new schema.
2. **Clock source:** supervisor currently waits for diagnostic-status.json (`supervise.py:200-214`) on a250ms loop. Instead check bounded fresh `report/run-start.json` before those optional metric cuts and immediately after terminal/stop checks. Require schema `wukongim.chat_lifecycle.run_start/v1`, expected stage, exact expected run/assignment hash and generation, valid timezone-aware started_at/expected_end_at, receipt file <=4KiB, and one matching generation-owned claim. Reject stale/symlink/mismatched/invalid receipts without network. Use existing known identity/hash math; do not invent an assignment from the receipt itself. Do not create run-start.json or declare readiness from online counts. Record detection lag; skip as `missed_early_window` if lag>2s or backward/uncertain wall-to-monotonic mapping. Two seconds is a proposed reviewable policy, not an existing product limit.
3. **Receipt semantics:** `start_receipt.go:13-22` defines a post-sync/post-first-grant clock. Coordinator `coordinator.go:731-747` first joins successful grant delivery, takes grantBarrierAt, then calls Begin. `production_controller.go:183-196` probes dataset before atomically writing the receipt (`start_receipt.go:38-55`). Therefore receipt publication/detection can lag the true first grant. It can trigger the earliest available retained proof, not capture retroactively at t0. Record this limit explicitly.
4. **Node label:** source shell:129-131 allows one or three URLs, but `:291-295,463-473` labels one URL as node-1. Temporary fixed-node mode must independently set physical label node-2 and index0; use node-2 filenames and node_id2 metadata. Get the exact loopback node2 debug URL from the already materialized local configuration, not cloud addresses. No extra node URLs may be queried.
5. **Selection:** remove heap launch (`:468-470`) and optional context branch from temporary fixed mode. Mark heap as `not_requested`, and change final completeness predicate (`:492-499`) to require only CPU and goroutine; absence of heap must not silently become failure or pretend a heap exists. Preserve missing CPU/goroutine partials.
6. **Schedule/ownership:** launch CPU with existing start_profile_request; register one finite5s timer child with the SAME registration guard and cleanup ownership before waiting on it. Do not put `sleep5; curl...` into an unmanaged background subshell. Wait the tracked timer specifically, clear its PID after join, check pending signal/live measurement/terminal fence, then call existing registered start_profile_request once for goroutine. General profile join runs only after this dispatch; calling it immediately would block on CPU and move goroutine to +10..15s. Timer has a separate kind and no blob, so generic promotion must never treat it as a profile. Signal handling joins timer and both curl children, including signals during timer/CPU/gor registration. Absolute capture deadline is CPU submission +15s; goroutine timeout5s fits nominally by +10s. If dispatch delay would exceed overall deadline or measurement ends, skip rather than start late work. Record timer scheduling skew; never claim exact server timing.
7. **Supervisor integration:** spawn the temporary driver once in a separately owned process group, with env token supplied privately and bounded file output; record PID/PGID at creation. Keep supervisor's main loop/diagnostic watchdog independent and nonblocking. On coordinator terminal, stop request, clock confound or hard cleanup, fence future requests, send TERM once to driver, wait boundedly, and escalate only its owned group if needed. Join before cleanup/report finalization; preserve original coordinator/launcher exit, including exit8. Do not enlarge diagnosis hold or workload duration to finish a profile. If hard cleanup leaves insufficient budget, retain partial/skipped result. Existing launcher hold only triggers on exit7 (`launcher.sh:358-364`); do not assume exit8 grants90s.

## Failure matrix and necessary fake-only checks before a real capture

No fake checks below have been run. Implement tests before changing the temporary driver, use fake curl or an isolated loopback fixture, and never contact a real product or cloud endpoint during qualification. Test harness start/listener/sleep cases belong to integration tier if ever committed to scripts; current proposal creates no test/source file.

| Case | Required result / repeatable artifact |
| --- | --- |
| Matching fresh receipt | Exactly2 requests to only node2: CPU seconds10 at first accepted receipt, goroutine at CPU-submission+5s within documented scheduling tolerance;0 heap/metrics; request transcript and monotonic timing receipt; node-2 filenames |
| Missing, partial/oversized, malformed, symlink, old generation, wrong hash/stage or late receipt | Zero request, typed skipped/invalid reason; no fabricated readiness |
| Repeated receipt or concurrent hook | One claimed generation; no repeated CPU or goroutine |
| Phase already drain / terminal already observed | Zero request; original exit code preserved |
| Terminal at +2s before delayed goroutine | CPU canceled and joined, timer joined, goroutine never requested; typed partial |
| Phase changes after final precheck | End state invalid; supervisor cancellation joins every child; narrow dispatch race explicitly observable |
| HUP/INT/TERM during CPU registration, timer registration/wait, gor registration, and joins | No missed child, orphan timer/curl or post-terminal request; partial metadata; repeatable process/active-request zero proof |
| CPU or gor HTTP401/429/5xx, empty/truncated body, dial stall, server hang | No retries; per-kind missing/partial; all requests finish within2s connect/15s overall/5s snapshot bounds |
| Known-length and headerless oversized CPU/gor streams | Hard temporary byte bound demonstrated for the installed transfer path; no oversized promotion; otherwise implementation remains unqualified |
| Bad token / xtrace inherited / child args+env scan / output scan | Zero leaked token; anonymous header FD only; bad token rejected before output/network |
| Only CPU or only gor succeeds | Partial remains explicit; no heap requirement; correctly labeled physical node2 |
| Output write failure, preexisting artifacts, symlink output | No overwrite of prior evidence; typed artifact error; benchmark exit remains unchanged |
| Hard deadline / cleanup while profile active | Profile joins within existing budget; never postpones benchmark stop/cleanup; exit8 retained |

Minimum fake acceptance receipt should include fixture_only=true, driver SHA/source-copy diff SHA, original helper SHA, request count/kind/node/timestamps, CPU/gor byte counts/status, signal boundary and timer ownership, joined active-child count0, original exit preservation, and hashes of fake artifacts. Existing repository tests need not be rerun now; none of their old results establishes these new assertions.

## Delivery decision

Deliver current exit8 with its own evidence. Treat this profile as an optional next-run diagnostic proposal. The bounded one-node CPU10s plus one delayed goroutine can help locate contention if the workload is already otherwise qualified, but it changes observation overhead and does not prove the loss cause. No real profile should be started until the new temporary driver diff and its necessary fake ownership/schedule/byte-bound checks exist and pass.

## Frozen source hashes

- `AGENTS.md`: `2220f49835b8dba20bafbc277854421222765be52e874fc6ee994bcd3ee0d746`
- `internal/bench/FLOW.md`: `038bc550839e6601ee1669e950de73bc4e9a4d72a607f39ce7009eadd913b76f`
- `internal/bench/chatlifecycle/FLOW.md`: `9bbe671968a6a7b20bde9b3900b0cca05cd758badf6443c18131372c3119a5cd`
- `scripts/capture-wukongim-local-threshold-pprof.sh`: `b17347aea7acdfc6320c7db547065893814b105b7fbe5b6f9655b8b044b0780a`
- `scripts/wukongim_local_threshold_pprof_script_test.go`: `75f692b9046d39cd2c72c72335342de000a079fe9a3efefa78841efbda35d679`
- `scripts/wukongim_local_threshold_pprof_script_integration_test.go`: `8cdd6bb91e10a6f08fb84e710b16b2dff15bd3bd329c6f45fbeeb3391dd35311`
- `internal/bench/chatlifecycle/start_receipt.go`: `4b81aa65fed8f07221a2d55dd6cde2e7cb13476a7cb0de2948cf97a6e3f2fa7e`
- `internal/bench/chatlifecycle/production_controller.go`: `fef17bdf5ade437c5c68d58cfd9a9519a2a4a875bee2097ecb84f734ce7b4dba`
- `internal/bench/chatlifecycle/coordinator.go`: `83e035c6120fff170d6ef798675792b8036a146b9586c2c1930c03117bf3a4d2`
