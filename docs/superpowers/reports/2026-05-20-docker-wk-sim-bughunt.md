# Docker WK-Sim Bughunt Report

Date: 2026-05-20
Branch: `perf/wk-sim-loop-20260520`
Worktree: `.worktrees/wk-sim-perf-loop-20260520`

## Scope

- Exercise the Docker Compose three-node development cluster plus `wk-sim`.
- Use simulated traffic to find reproducible bugs and performance issues.
- For each confirmed product issue: reproduce, fix, verify, commit, and keep searching.

## Environment Notes

- The worktree lives under the repository `.worktrees` directory. The parent `go.work` at `/Users/tt/Desktop/work/go/WuKongIM-v2/go.work` selects the original `WuKongIM` module, so Go commands in this nested worktree must use `GOWORK=off`.

## Verification Log

| Time | Command | Result | Notes |
| --- | --- | --- | --- |
| 2026-05-20 | `GOWORK=off go test ./...` | Pass | Full unit-test baseline passed in the isolated worktree. |
| 2026-05-20 | `docker compose --profile dev-sim config --quiet` | Pass | Compose configuration parsed successfully. |
| 2026-05-20 | reduced `scripts/dev-sim-compose-smoke.sh` | Pass | `WK_SIM_USERS=40`, `WK_SIM_RATE=0.5/s`, `WK_SIM_VERIFY_RECV=sampled`; transient recv errors observed during startup recovery. |
| 2026-05-20 | default `scripts/dev-sim-compose-smoke.sh --no-build` with `--timeout 180` | Pass | Reached `running` after about 107s, then emitted traffic. |
| 2026-05-20 | default `scripts/dev-sim-compose-smoke.sh --no-build --timeout 90 --skip-logs` | Fail | Timed out in `state=waiting` with `connected_users=0`, no last error, and no sent messages. |
| 2026-05-20 | `GOWORK=off go test ./scripts -run TestDevSimComposeSmokeDefaultTimeoutCoversHighTrafficStartup -count=1` | Red then pass | Regression test failed with the old 90s default and passed after increasing the default to 180s. |
| 2026-05-20 | `GOWORK=off go test ./scripts ./internal/bench/devsim -count=1` | Pass | Focused unit verification for the smoke script and dev-sim config package. |
| 2026-05-20 | default `scripts/dev-sim-compose-smoke.sh --no-build --skip-logs` | Pass | With the 180s default, fresh Compose startup reached traffic before timeout. Status still showed send errors, tracked separately as BUG-002. |
| 2026-05-20 | clean data default `scripts/dev-sim-compose-smoke.sh --no-build --skip-logs` | Pass | After removing ignored `docker/dev-cluster` / `docker/dev-sim`, the default profile reached `send_errors=0` and `recv_errors=0`. |
| 2026-05-20 | `GOWORK=off go test ./scripts -run TestDevSimComposeSmokeRejectsStatusErrorCounters -count=1` | Red then pass | Regression test proved the script used to pass when `/status` had `send_errors=2` and `recv_errors=1`; it now waits/fails instead. |
| 2026-05-20 | `GOWORK=off go test ./scripts -count=1` | Pass | Full script test suite passed after the stricter status gate. |
| 2026-05-20 | `scripts/dev-sim-compose-smoke.sh --no-up --skip-logs` | Pass | Existing clean Compose stack passed with `send_errors=0` and `recv_errors=0`. |
| 2026-05-20 | `WK_SIM_RATE=0.5/s WK_SIM_UID_PREFIX=stress-u scripts/dev-sim-compose-smoke.sh --no-build --skip-logs --timeout 180` | Fail | With the old Docker node data-plane settings, 0.5/s produced `send_errors=145` and timed out under the stricter smoke gate. |
| 2026-05-20 | `GOWORK=off go test ./docker -run TestComposeNodeConfigsUseExplicitDataPlaneConcurrency -count=1` | Red then pass | Regression test failed while node configs pinned data-plane concurrency to `1`, then passed after explicit data-plane settings were added. |
| 2026-05-20 | `WK_SIM_RATE=0.5/s WK_SIM_UID_PREFIX=stress-u2 scripts/dev-sim-compose-smoke.sh --no-build --skip-logs --timeout 180` | Pass | Clean Compose data plus explicit data-plane concurrency reached traffic with `send_errors=0` and `recv_errors=0`. |
| 2026-05-20 | `GOWORK=off go test ./docker ./scripts -count=1` | Pass | Focused regression suites passed after Docker config and smoke script changes. |
| 2026-05-20 | `WK_SIM_RATE=1/s WK_SIM_TRAFFIC_CONCURRENCY=256 WK_SIM_UID_PREFIX=stress1-u scripts/dev-sim-compose-smoke.sh --no-build --skip-logs --timeout 180` | Stress limit | At roughly 1000 ingress/s target, Compose still produced send timeouts (`send_errors=257`). This is recorded as a capacity boundary, not part of the default smoke gate. |

## Findings

| ID | Status | Area | Symptom | Root Cause | Fix Commit |
| --- | --- | --- | --- | --- | --- |
| BUG-001 | Fixed | Docker dev-sim smoke | Default smoke timeout is 90s, but the Compose profile starts 1000 users with inherited `connect_rate: 10/s`; the script times out in `state=waiting` before traffic starts. | Compose overrides workload size but not simulator connect rate, so connection setup alone needs roughly 100s before the first traffic window. | `32ee9de9` |
| BUG-002 | Fixed | Docker dev-sim smoke | Smoke can report pass while `/status` has non-zero `send_errors` or `recv_errors`; this hid failures observed after dirty-data/restart runs. | The script only gated on `state=running`, `connected_users>0`, and `messages_sent>0`; it parsed neither error counter. | `00429905` |
| BUG-003 | Fixed | Docker dev-sim performance | 0.5/s stress profile timed out with `send_errors=145`; pprof during the run showed heavy RPC/storage work while Docker node configs still had data-plane pool size `1`. | Docker development configs overrode the app's higher default and did not set explicit data-plane fetch/pending limits, so high-traffic dev-sim runs were bottlenecked by a single data-plane lane. | `9dd55f80` |
| PERF-004 | Recorded | Docker dev-sim stress | 1/s stress profile with `WK_SIM_TRAFFIC_CONCURRENCY=256` still produces send timeouts on this machine. | The current Compose development profile is stable at the default 0.25/s and verified at 0.5/s after BUG-003, but 1/s is beyond the verified local capacity. | Not fixed |

## Active Test Matrix

- Compose smoke with reduced laptop-safe simulator load.
- Compose smoke with default dev-sim profile.
- Bounded stress pass with higher `WK_SIM_RATE` / `WK_SIM_TRAFFIC_CONCURRENCY`.
- Focused unit/e2e tests for every confirmed code defect.

## Continuation: main worktree Run 7

| Time | Command | Result | Notes |
| --- | --- | --- | --- |
| 2026-05-20 | `GOWORK=off go test ./pkg/channel/replica -run 'TestApplyFetch(ResultAfterSameLeaderMetaRefreshPublishesDurableLEO|StaleResultAfterMetaChangeIsFenced|TruncateResultAfterMetaChangeIsFenced)' -count=1` | Pass | Regression and stale-fence checks passed after the follower durable apply fix. |
| 2026-05-20 | `GOWORK=off go test ./pkg/channel/replica -count=1` | Pass | Full replica package verification passed. |
| 2026-05-20 | `WK_SIM_RATE=1/s WK_SIM_TRAFFIC_CONCURRENCY=256 WK_SIM_UID_PREFIX=perf2-u scripts/dev-sim-compose-smoke.sh --no-build --timeout 300 --skip-logs` | Partial | Strict smoke still timed out on non-zero send errors, but diagnostics and logs no longer showed `channel: corrupt state`. |

| ID | Status | Area | Symptom | Root Cause | Fix Commit |
| --- | --- | --- | --- | --- | --- |
| BUG-008 | Fixed | Channel replica follower apply | High-rate `wk-sim` stress could produce `channel: corrupt state` after follower durable apply overlapped with same-leader metadata refresh. | The durable write was fenced before mutation, but result publication rejected any role-generation mismatch, leaving runtime LEO behind the durable log when channel key, epoch, and leader were unchanged. | Current change |

## Continuation: main worktree Run 8

| Time | Command | Result | Notes |
| --- | --- | --- | --- |
| 2026-05-20 | `GOWORK=off go test ./internal/bench/workload -run 'Test(Person|Group)WorkloadWarmupUsesWarmupDurationAsMinimumAckTimeout' -count=1` | Red then pass | Regression tests failed with `context deadline exceeded` before the warmup timeout fix and passed after it. |
| 2026-05-20 | `GOWORK=off go test ./internal/bench/workload -count=1` | Pass | Workload package verification passed. |
| 2026-05-20 | `GOWORK=off go test ./internal/bench/... ./cmd/wkbench -count=1` | Pass | Focused bench and wkbench CLI verification passed. |
| 2026-05-20 | `WK_SIM_UID_PREFIX=loop-fix8-u scripts/dev-sim-compose-smoke.sh --no-build --skip-logs --timeout 360` | Pass | Default Compose smoke reached `connected_users=1000`, `messages_sent=2239`, `send_errors=0`, `recv_errors=0` on the accumulated local cluster. |

| ID | Status | Area | Symptom | Root Cause | Fix Commit |
| --- | --- | --- | --- | --- | --- |
| BUG-009 | Fixed | wk-sim warmup | Default and high-rate Compose profiles could retry forever before `/status` reached `running` on a dirty/stressed local cluster. | Warmup used the shorter measured-run sendack/recv timeout, so cold channel activation latency could cancel the whole warmup phase before it completed. | Current change |

## Continuation: main worktree Run 9

| Time | Command | Result | Notes |
| --- | --- | --- | --- |
| 2026-05-20 | `WK_SIM_RATE=1/s WK_SIM_TRAFFIC_CONCURRENCY=256 WK_SIM_UID_PREFIX=loop-fix8-stress-u scripts/dev-sim-compose-smoke.sh --no-build --skip-logs --timeout 420` | Pass | High-rate mixed profile reached `messages_sent=7034`, `send_errors=0`, `recv_errors=0` before the smoke gate exited. Continued polling reached `messages_sent=107094` with zero errors. |
| 2026-05-20 | diagnostics query after high-rate run | Pass | Cluster diagnostics returned no error events and recent node logs had no corrupt-state, timeout, append-failure, or panic lines. |
| 2026-05-20 | reduced sampled receive `scripts/dev-sim-compose-smoke.sh --no-build --skip-logs --timeout 180` | Pass | Sampled receive profile reached `messages_sent=65` at the smoke gate and `messages_sent=453` during continued polling, with zero send/recv errors. |

## Continuation: isolated worktree Run 10

This continuation used `perf/wk-sim-loop-20260520` with `GOWORK=off`. The default Docker data directory had grown to about `3.2G`, so results distinguish the dirty local volume from a clean override volume under `/tmp/wukongim-clean-20260520-230926`.

| Time | Command | Result | Notes |
| --- | --- | --- | --- |
| 2026-05-20 | `GOWORK=off go test ./pkg/protocol/... ./pkg/channel/... ./docker ./internal/bench/workload ./internal/bench/worker ./internal/bench/devsim -count=1` | Pass | Focused protocol/channel/bench/docker verification before Docker rebuilds. |
| 2026-05-20 | `GOWORK=off go test ./internal/app ./internal/access/gateway ./internal/bench/workload ./internal/bench/worker ./internal/bench/devsim -count=1` | Pass | App/gateway/bench verification after delivery-ack and auto-recvack changes. |
| 2026-05-20 | `GOWORK=off go test ./pkg/channel/store ./pkg/channel/... ./internal/app ./internal/bench/workload ./internal/bench/worker ./internal/bench/devsim ./docker -count=1` | Mostly pass | One timing-sensitive broad-run failure in `pkg/channel/runtime.TestSessionLongPollRPCTimeoutUsesRecoveryBackoff`; the focused test then passed with `-count=5` and a fresh package run. Recorded in `docs/development/CODE_QUALITY.md`. |
| 2026-05-20 | `WK_SIM_RATE=2/s WK_SIM_TRAFFIC_CONCURRENCY=512 WK_SIM_UID_PREFIX=loop14-nobuf-u` Docker rebuild + smoke | Fail | Initial traffic reached `messages_sent=10528`, then `send_errors=95`; stream decode corruption did not recur, errors were session-scoped sendack timeouts. |
| 2026-05-20 | `WK_SIM_RATE=2/s WK_SIM_TRAFFIC_CONCURRENCY=512 WK_SIM_UID_PREFIX=loop15-ackasync-u` after async delivery ack batching | Fail | Smoke gate passed initially with `messages_sent=12582`, `send_errors=0`, but sustained polling reached `send_errors=78` at `messages_sent=107127`. |
| 2026-05-20 | `WK_SIM_RATE=2/s WK_SIM_TRAFFIC_CONCURRENCY=512 WK_SIM_UID_PREFIX=loop16-dropmatch-u` after no-buffer matcher drop | Fail | Smoke gate passed initially with `messages_sent=11539`, `send_errors=0`, but sustained polling reached `send_errors=84` at `messages_sent=65414`. |
| 2026-05-20 | `WK_SIM_RATE=1/s WK_SIM_TRAFFIC_CONCURRENCY=256 WK_SIM_UID_PREFIX=loop17-verify1-u` on dirty `docker/dev-cluster` | Fail | Sustained polling reached `messages_sent=348756`, `send_errors=372`, confirming accumulated local data/compaction makes the dirty profile less stable than earlier clean runs. |
| 2026-05-20 | Clean override volume, `COMPOSE_PROJECT_NAME=wukongim-clean WK_SIM_RATE=1/s WK_SIM_TRAFFIC_CONCURRENCY=256 WK_SIM_UID_PREFIX=clean1-u` | Pass | Clean-volume run reached `messages_sent=121145`, `send_errors=0`, `recv_errors=0` during continued polling. |
| 2026-05-20 | Same clean1 run, continued longer | Fail | The earlier clean 1/s run later reached `messages_sent=254995`, `send_errors=37`, so the 121k zero-error result was a partial pass, not a sustained capacity fix. |
| 2026-05-20 | Clean override volume, `WK_SIM_RATE=1/s WK_SIM_TRAFFIC_CONCURRENCY=256 WK_SIM_UID_PREFIX=clean2-headroom-u` after Pebble headroom | Fail | Rebuilt clean stack reached `messages_sent=294504`, `send_errors=84`; profiles still showed active delivery/RPC/storage work, so Pebble headroom alone did not fix sustained 1/s. |
| 2026-05-20 | `GOWORK=off go test ./internal/usecase/conversation -run TestActiveHintCacheBackgroundFlushIsBoundedToOneBatch -count=1` | Red then pass | Regression test failed before adding a bounded background active-hint flush path, then passed after periodic flush was limited to one best-effort batch. |
| 2026-05-20 | Clean override volume, `WK_SIM_RATE=1/s WK_SIM_TRAFFIC_CONCURRENCY=256 WK_SIM_UID_PREFIX=clean3-bgbatch-u` | Fail | Background active-hint flush limiting did not remove sustained sendack timeouts; run reached `messages_sent=203120`, `send_errors=151`, and node logs still showed active-hint flush deadlines before the count-index fix. |
| 2026-05-20 | `GOWORK=off go test ./internal/usecase/conversation -run TestActiveHintCacheMaintainsUIDHintCounts -count=1` | Red then pass | Regression test failed before the cache had a UID count index and passed after count maintenance covered submit, flush, prune, and delete-barrier removal. |
| 2026-05-20 | Clean override volume, `WK_SIM_RATE=1/s WK_SIM_TRAFFIC_CONCURRENCY=256 WK_SIM_UID_PREFIX=clean4-count-u` | Partial | Active-hint warnings disappeared and `ActiveHintCache.SubmitHints` dropped out of CPU top lists, but sustained 1/s still reached `messages_sent=204354`, `send_errors=186`; remaining profiles are dominated by delivery fanout, gateway writes, transport RPC, and Pebble point reads. |
| 2026-05-20 | Clean override volume, `WK_SIM_RATE=0.5/s WK_SIM_TRAFFIC_CONCURRENCY=256 WK_SIM_UID_PREFIX=clean5-half-u` | Fail | Post-fix clean 0.5/s sustained polling first reached `messages_sent=166843`, `send_errors=0`, `recv_errors=0`, but later reached `messages_sent=540471`, `send_errors=653`; all three nodes emitted recurring best-effort `active hint cache flush failed` deadline warnings near error bursts. |

| ID | Status | Area | Symptom | Root Cause | Fix Commit |
| --- | --- | --- | --- | --- | --- |
| PERF-010 | Fixed | Channel append/store | `wk-sim` profiles spent a noticeable share of CPU in Pebble point reads on durable append/apply. | Follower apply and trusted contiguous append paths re-read existing row/idempotency indexes even when the caller already had a sequential log proof. | Current change |
| PERF-011 | Partial | Docker gateway ingress | Compose stress had limited ingress headroom even after data-plane settings were improved. | Docker node configs did not pin gnet event-loop/read/write-buffer headroom, so local stress depended on runtime defaults. | Current change |
| BUG-012 | Fixed | WKProto streaming decode | Under stress, simulator errors sometimes looked like malformed `CONNACK`, `SENDACK`, or `UNKNOWN[0]` frames. | `decodeLengthWithConn` ignored short-read/timeout errors while decoding the variable remaining length, which could desynchronize the stream and report misleading packet decode failures. | Current change |
| BUG-013 | Fixed | wk-sim recovery | Dev-sim could not reliably reconnect the failed session after sendack timeouts/decode errors. | Person/group workloads only wrapped `io.EOF` sendack failures as `SessionError`; timeout/decode sendack failures lost the UID context needed for targeted recovery. | Current change |
| PERF-014 | Fixed | wk-sim receive-ack drain | With `WK_SIM_VERIFY_RECV=none`, recv frames were still buffered by auto recv-ack paths even though no verification waiter would consume them. | The background drainer and explicit sendack matcher used the same buffering policy as sampled/full receive verification. | Current change |
| PERF-015 | Fixed | Delivery ack routing | Goroutine profiles during 2/s stress showed gateway connection goroutines blocked in `deliveryAckBatchNotifier.NotifyAck` while waiting for delayed remote ack batch flushes. | Batched remote delivery acks are best-effort from the client protocol perspective, but the notifier made the gateway frame handler wait up to the batch delay/RPC result. | Current change |
| PERF-016 | Partial | Channel Pebble point lookups | Dirty local Compose data (`docker/dev-cluster` about `3.2G`) made 1/s and 2/s profiles regress into sendack timeouts; pprof showed `pebble.DB.Get` around 9-12% cumulative, primarily idempotency misses and metadata reads. | Channel-store Pebble options did not enable Bloom filters, so point-lookup misses over accumulated SSTables were more expensive. New SSTables now carry Bloom filters, but old dirty SSTables need compaction/rebuild before fully benefiting. | Current change |
| CAP-017 | Recorded | Local Docker capacity | Clean 1/s passed beyond 121k messages with zero errors, but longer clean 1/s runs later produced sendack timeouts; dirty 1/s and dirty/accumulated 2/s also produced sendack timeouts. | The local Compose profile is verified for default and bounded 0.5/s smoke, but sustained 1/s remains above the current no-error capacity on this machine. | Not fixed |
| PERF-018 | Fixed | Conversation active hints | 1/s clean profiles and node logs showed active-hint flush deadlines and `ActiveHintCache.SubmitHints`/capacity enforcement in CPU top lists. | The cache rebuilt per-UID counts by scanning the whole hot hint map on every submit, and the periodic best-effort flush could drain multiple batches in one tick. | Current change |
| CAP-019 | Recorded | Remaining sustained bottleneck | After PERF-020, clean 0.5/s held zero send/recv errors past 272k messages, but clean 1/s still hits periodic send/write/sendack timeouts. | Current 1/s profiles no longer show one dominant defect; the remaining cost is split across channel long-poll/data-plane, gateway writes, delivery fanout, transport RPC, commit coordination, and Pebble reads/writes. | Not fixed |

## Continuation: isolated worktree Run 11

This continuation kept the clean Docker override volume pattern and focused on the active-hint flush deadline warnings that reappeared during the long 0.5/s run.

| Time | Command | Result | Notes |
| --- | --- | --- | --- |
| 2026-05-21 | Clean override volume, `WK_SIM_RATE=0.5/s WK_SIM_TRAFFIC_CONCURRENCY=256 WK_SIM_UID_PREFIX=clean6-activeparallel-u` with an experimental parallel active-hint touch proposal | Rejected | The experiment made 0.5/s worse (`send_errors=25` by `messages_sent=23636`) and still produced active-hint deadline warnings, so the code and test were reverted. |
| 2026-05-21 | `GOWORK=off go test ./internal/app -run TestConfigDefaultsConversationActiveHints -count=1` | Red then pass | Regression test failed while app defaults were `2s` / `1024`, then passed after changing the default active-hint flush interval and batch size to `10s` / `32`. |
| 2026-05-21 | `GOWORK=off go test ./docker -run TestComposeNodeConfigsBoundActiveHintFlushFanout -count=1` | Pass | Docker node configs now explicitly pin the bounded best-effort active-hint flush settings used by the clean dev-sim run. |
| 2026-05-21 | `GOWORK=off go test ./internal/usecase/conversation ./internal/app ./docker ./pkg/slot/proxy -count=1` | Pass | Focused package verification passed after reverting the rejected parallel proposal and keeping the bounded flush tuning. |
| 2026-05-21 | `git diff --check` | Pass | Whitespace check passed before commit. |
| 2026-05-21 | `GOWORK=off go test ./...` | Pass | Full unit-test suite passed before commit. |
| 2026-05-21 | Clean override volume, `WK_SIM_RATE=0.5/s WK_SIM_TRAFFIC_CONCURRENCY=256 WK_SIM_UID_PREFIX=clean8-hintseq32-u` | Pass | Sustained polling reached at least `messages_sent=272791`, `send_errors=0`, `recv_errors=0`; node log scan showed no active-hint deadline, timeout, error, panic, fatal, or corrupt lines for the run window. |
| 2026-05-21 | Clean override volume, `WK_SIM_RATE=1/s WK_SIM_TRAFFIC_CONCURRENCY=256 WK_SIM_UID_PREFIX=clean9-rate1-u` | Fail | Clean 1/s still reached `messages_sent=217342`, `send_errors=160`; quick log scan found no active-hint warnings, and pprof showed distributed transport/channel/gateway/delivery/commit/Pebble cost. |

| ID | Status | Area | Symptom | Root Cause | Fix Commit |
| --- | --- | --- | --- | --- | --- |
| PERF-020 | Fixed | Conversation active-hint flush fanout | Long clean 0.5/s runs reintroduced active-hint flush deadline warnings and correlated sendack timeouts even after the active-hint hot path was made O(1). | The background flush default of `1024` hints every `2s` could fan out into many UID hash-slot Slot Raft proposals, so a best-effort hint path periodically competed with foreground sends. | Current change |

## Continuation: isolated worktree Run 12

This continuation kept the clean 1/s Docker dev-sim run running as background evidence and targeted the next profile slice with a narrow TDD fix. The running image was not rebuilt for this code change, so Docker counters in this section are diagnostic context, while the fix is verified by focused and full Go tests.

| Time | Command | Result | Notes |
| --- | --- | --- | --- |
| 2026-05-21 | Clean override volume, `WK_SIM_RATE=1/s WK_SIM_TRAFFIC_CONCURRENCY=256 WK_SIM_UID_PREFIX=clean10-rate1-u` continued polling | Partial | The run progressed beyond `messages_sent=813689` with `send_errors=43` and `recv_errors=0`; errors did not grow during the last observation window, but the local image predates the latest code change. |
| 2026-05-21 | `go tool pprof` on `/tmp/wksim-rate1-error-pprof-20260521-092728/node{1,2,3}.cpu.pb.gz` focused on subscriber metadata resolution | Evidence | Error-window profiles showed `tagDeliveryResolver.BeginResolve` spending about `2.25-3.40%` cumulative CPU in `subscriberResolver.channelMutationVersion -> Store.GetChannel -> Pebble DB.Get`; code inspection showed the lookup ran before channel-type dispatch, including person channels whose subscriber set is derived from the channel ID. |
| 2026-05-21 | `GOWORK=off go test ./internal/usecase/delivery -run TestSubscriberResolverSkipsMetadataForDerivedPersonChannel -count=1` | Red then pass | Regression test failed while person-channel resolution touched metadata (`getChannelCalls=[{u2@u1 1}]`), then passed after derived person/agent/temp sources skipped durable subscriber metadata reads. |
| 2026-05-21 | `GOWORK=off go test ./internal/usecase/delivery -count=1` | Pass | Full delivery usecase package verification passed after the metadata-read removal. |
| 2026-05-21 | `git diff --check` and `GOWORK=off go test ./...` | Pass | Whitespace and full unit-test verification passed before committing the subscriber metadata hot-path fix. |

| ID | Status | Area | Symptom | Root Cause | Fix Commit |
| --- | --- | --- | --- | --- | --- |
| PERF-021 | Fixed | Delivery subscriber metadata | Clean 1/s profiles still spent measurable CPU in subscriber metadata `GetChannel` reads during delivery begin-resolve, even for person channels. | `BeginSnapshotWithRequest` read durable subscriber mutation metadata before dispatching by channel type, so derived person/agent/temp subscriber sets paid a Pebble point lookup even though their membership is encoded in the request/channel ID and has no durable subscriber list. | Current change |

## Continuation: isolated worktree Run 13

This continuation rebuilt `wukongim-dev:local` with the legacy Docker builder and local base-image cache because BuildKit metadata lookup against Docker Hub was unavailable. The fresh clean 1/s run confirmed the subscriber metadata hot-path was removed from profiles, then exposed the next allocation-heavy person delivery path.

| Time | Command | Result | Notes |
| --- | --- | --- | --- |
| 2026-05-21 | `DOCKER_BUILDKIT=0 docker build --pull=false -t wukongim-dev:local .` | Pass | Rebuilt the local image from committed code without network metadata pulls. |
| 2026-05-21 | Clean override volume, `WK_SIM_RATE=1/s WK_SIM_TRAFFIC_CONCURRENCY=256 WK_SIM_UID_PREFIX=clean11-derived-u` | Partial | Fresh-image run reached traffic quickly; it stayed at zero errors through about `messages_sent=84629`, then produced sendack bursts while continuing to recover. This keeps sustained 1/s recorded as above local no-error capacity. |
| 2026-05-21 | `/tmp/wksim-rate1-derived-pprof-20260521-095028` focused subscriber profile | Evidence | `subscriberResolver.channelMutationVersion` no longer appeared above the profile cutoff; remaining cost was dominated by transport RPC, gateway writes, delivery fanout, long-poll, storage commit, and allocation pressure. |
| 2026-05-21 | `GOWORK=off go test ./internal/app -run TestDistributedDeliveryPushSinglePersonRouteAllocationBudget -count=1` | Red then pass | Regression test measured `29` allocations for the one-route remote person delivery path before the fast path and passed once grouping maps/slices were skipped for single-route delivery. |
| 2026-05-21 | `GOWORK=off go test ./internal/app -run 'TestDistributedDeliveryPush(SinglePersonRouteAllocationBudget|SingleRemoteNodeUsesCallerGoroutine|BatchesPersonRoutesByRecipientChannelView|RunsRemoteNodesWithBoundedParallelism)|TestLocalDeliveryPushBuildsPersonChannelViewPerRouteUID' -count=1` | Pass | Focused delivery routing behavior and allocation-budget checks passed. |
| 2026-05-21 | `GOWORK=off go test ./internal/app -run '^$' -bench 'Benchmark(LocalDeliveryPushPersonRoutes|DistributedDeliveryPushPersonRouteViews|DistributedDeliveryPushGroupBatchRoutes|DistributedDeliveryPushGroupFanoutFrameReuse)$' -benchmem -count=3` | Pass | Existing delivery benchmarks remained functional; 256-route benchmark allocation counts were intentionally unchanged because the fix targets the wk-sim one-recipient person route. |
| 2026-05-21 | `git diff --check` and `GOWORK=off go test ./...` | Pass | Whitespace and full unit-test verification passed before committing the single-route delivery allocation fix. |

| ID | Status | Area | Symptom | Root Cause | Fix Commit |
| --- | --- | --- | --- | --- | --- |
| PERF-022 | Fixed | Delivery single-recipient fanout | After derived subscriber metadata reads were removed, profiles still showed significant `runtime.mallocgc` under delivery/gateway paths; wk-sim person channels usually fan out to a single non-sender route. | `distributedDeliveryPush.Push` and `deliveryPushItems` still allocated local/remote grouping maps and route-view maps even when there was only one remote person route; local person delivery also allocated a frame cache map for one route. | Current change |
