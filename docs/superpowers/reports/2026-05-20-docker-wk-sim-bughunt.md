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

## Continuation: isolated worktree Run 14

This continuation observed the fresh-image clean 1/s run after PERF-022, captured a new pprof set, and targeted the next confirmed Pebble point-read slice without changing delivery semantics.

| Time | Command | Result | Notes |
| --- | --- | --- | --- |
| 2026-05-21 | Clean override volume, `WK_SIM_RATE=1/s WK_SIM_TRAFFIC_CONCURRENCY=256 WK_SIM_UID_PREFIX=clean12-singlefast-u` continued polling | Fail | Fresh-image clean 1/s improved versus earlier runs but still reached `messages_sent=484552`, `send_errors=222`, `recv_errors=0`; later polling reached `messages_sent=1036854`, `send_errors=593`, so sustained 1/s remains above current local no-error capacity. |
| 2026-05-21 | `/tmp/wksim-rate1-postsingle-pprof-20260521-101429` focused on `pebble.DB.Get` / slot metadata | Evidence | Profiles showed `subscriberResolver.channelMutationVersion -> Store.GetChannel -> slot/meta.GetChannel` still costing about `1.8-2.6%` cumulative per node for store-backed group tags, plus other channel idempotency and checkpoint reads. |
| 2026-05-21 | `GOWORK=off go test ./pkg/slot/meta -run TestGetChannelUsesWarmCacheForRepeatedVersionReads -count=1` | Red then pass | Regression test first counted repeated channel-primary Pebble reads (`got 2`) and passed after `meta.DB` cached hot channel metadata and updated it on channel/subscriber writes. |
| 2026-05-21 | `GOWORK=off go test ./pkg/slot/meta -count=1` | Pass | Full slot meta package verification passed after the cache change. |
| 2026-05-21 | `GOWORK=off go test ./pkg/slot/... ./internal/usecase/delivery ./internal/app -count=1` | Pass | Focused slot/delivery/app verification passed. |
| 2026-05-21 | `git diff --check` and `GOWORK=off go test ./...` | Pass | Whitespace and full unit-test verification passed before committing the channel metadata cache fix. |

| ID | Status | Area | Symptom | Root Cause | Fix Commit |
| --- | --- | --- | --- | --- | --- |
| PERF-023 | Fixed | Slot channel metadata reads | Store-backed group delivery tags still read durable channel metadata on every begin-resolve to fence subscriber mutation versions, keeping `pebble.DB.Get` visible in clean 1/s profiles after derived-channel reads were removed. | `meta.ShardStore.GetChannel` had no hot metadata cache, so stable group channels repeatedly decoded the same Channel row from Pebble even though channel/subscriber mutations already pass through the same local apply path and carry the new subscriber mutation version. | Current change |

## Continuation: isolated worktree Run 15

This continuation rebuilt the local image after PERF-023, started a fresh clean 1/s run, and captured another profile set. The targeted metadata read disappeared above the profile cutoff, while the remaining allocation profile consistently showed `internal/runtime/delivery.(*AckIndex).Bind` around `2.5-3.0%` of alloc_space on all three nodes.

| Time | Command | Result | Notes |
| --- | --- | --- | --- |
| 2026-05-21 | `DOCKER_BUILDKIT=0 docker build --pull=false -t wukongim-dev:local .` | Pass | Rebuilt local image from `af2597ce`; image ID `f26c3eb668db`. |
| 2026-05-21 | Clean override volume, `WK_SIM_RATE=1/s WK_SIM_TRAFFIC_CONCURRENCY=256 WK_SIM_UID_PREFIX=clean13-chcache-u` | Partial | The run initially accumulated sendack/write errors during early cold pressure and slot leadership churn, then returned to `state=running`; later status reached `messages_sent=365713`, `send_errors=694`, `recv_errors=0` with no recent node WARN/ERROR lines in the last five minutes. |
| 2026-05-21 | `/tmp/wksim-clean13-followup-20260521-104855` CPU/alloc/diagnostics capture | Evidence | `subscriberResolver.channelMutationVersion` no longer appeared above cutoff. Allocation tops still included `AckIndex.Bind` at about `0.17-0.21GB` per node, and diagnostics during the earlier error window showed send latency dominated by store commit/quorum waits rather than CPU saturation. |
| 2026-05-21 | `GOWORK=off go test ./internal/runtime/delivery -run TestAckIndexBindSingleOutstandingSessionAllocationBudget -count=1` | Red then pass | Regression test failed before the reverse-index optimization with `147` allocations for 64 single-outstanding session binds, then passed with the inline reverse-key representation and a budget of `<=60` allocations. |
| 2026-05-21 | `GOWORK=off go test ./internal/runtime/delivery -count=1` | Pass | Full delivery runtime package verification passed after the AckIndex change. |
| 2026-05-21 | `GOWORK=off go test -race ./internal/runtime/delivery -run TestAckIndex -count=1` | Pass | Race check for the AckIndex behavior tests passed. |
| 2026-05-21 | `git diff --check` and `GOWORK=off go test ./internal/runtime/delivery ./internal/app -count=1` | Pass | Whitespace, delivery runtime, and app integration-adjacent verification passed. |
| 2026-05-21 | `GOWORK=off go test ./...` | Pass | Full unit-test suite passed before committing the AckIndex allocation fix. |
| 2026-05-21 | Fresh-image clean 1/s after `f0b51526`, `WK_SIM_UID_PREFIX=clean14-ackidx-u` | Pass so far | Rebuilt image reached `messages_sent=119951`, `send_errors=0`, `recv_errors=0`; diagnostics showed store commit p95 around `3-4ms` and send durable p95 around `118-126ms`. |
| 2026-05-21 | `/tmp/wksim-rate1-ackidx-heap-20260521-111319` heap capture | Evidence | `replica.newPooledLoopDriver` retained about `133-143MB` per node (`29-32%` of in-use heap), traced to every channel replica preallocating a full 2048-entry mailbox. |
| 2026-05-21 | `GOWORK=off go test ./pkg/channel/replica -run TestPooledLoopDriverDoesNotPreallocateFullMailbox -count=1` | Red then pass | Regression test failed with `initial mailbox capacity = 2048`, then passed after pooled loop mailboxes became lazy and grow-on-demand. |
| 2026-05-21 | `GOWORK=off go test ./pkg/channel/replica -count=1` | Pass | Full replica package verification passed after the lazy mailbox change. |
| 2026-05-21 | `GOWORK=off go test ./pkg/channel/replica -run '^$' -bench 'BenchmarkPooledMailboxSubmitResult' -benchmem -count=3` | Pass | Hot mailbox submit path stayed at `0 B/op` and `0 allocs/op` in the benchmark. |
| 2026-05-21 | `git diff --check` and `GOWORK=off go test ./pkg/channel/replica ./pkg/channel/runtime ./internal/app -count=1` | Pass | Whitespace, replica/runtime, and app verification passed after the lazy mailbox change. |
| 2026-05-21 | `GOWORK=off go test ./...` | Pass | Full unit-test suite passed before committing the lazy mailbox fix. |
| 2026-05-21 | Continued `clean14-ackidx-u` run on image predating PERF-025 | Fail | Later status reached `messages_sent=392860`, `send_errors=247`, `recv_errors=0`; recent node logs again showed `conversation.active_hint` flush deadline warnings around the error burst, so this is tracked as a new unresolved 1/s finding. |
| 2026-05-21 | `DOCKER_BUILDKIT=0 docker build --pull=false -t wukongim-dev:local .` | Pass | Rebuilt local image from `5c236f51`; image ID `71065ee5a66c`. |
| 2026-05-21 | Fresh-image clean 1/s after PERF-025, `WK_SIM_UID_PREFIX=clean15-lazymbox-u` | Partial | Early status reached `messages_sent=70531`, `send_errors=0`, `recv_errors=0`; heap dropped to about `206-213MB` per node, and `newPooledLoopDriver` no longer retained the 133-143MB full mailbox slices. |
| 2026-05-21 | `/tmp/wksim-rate1-lazymbox-pprof-20260521-112648` CPU/alloc/diagnostics capture | Evidence | Capture status was `messages_sent=171178`, `send_errors=46`, `recv_errors=0`. Active-hint diagnostics returned `not_found`, recent node logs had no active-hint warnings after startup, and allocation profiles showed `RetryWheel.PopDue` allocating about `56-59MB` per node while delivery retry timers churned. |
| 2026-05-21 | Continued `clean15-lazymbox-u` polling on image predating PERF-027 | Fail | Later status reached `messages_sent=358501`, `send_errors=79`, `recv_errors=0`; a five-minute node log scan still showed no active-hint/timeout/corrupt warnings, so PERF-026 is not confirmed on the rebuilt image and the next confirmed hot slice is retry-wheel allocation churn. |
| 2026-05-21 | `GOWORK=off go test ./internal/runtime/delivery -run TestRetryWheelPopDueIntoReusesCallerBuffer -count=1` | Red then pass | Regression test failed before adding caller-buffer reuse (`PopDueInto` missing), then passed once retry due-entry collection reused the shard scratch buffer with zero allocations in the hot pop path. |
| 2026-05-21 | `GOWORK=off go test ./internal/runtime/delivery -run 'TestRetryWheel(PopDueIntoReusesCallerBuffer\|PopsDueEntriesInTimeOrder\|PopsMultipleDueEntriesInTimeOrder)' -count=1` | Pass | Focused retry-wheel behavior and allocation tests passed. |
| 2026-05-21 | `GOWORK=off go test ./internal/runtime/delivery -count=1` | Pass | Full delivery runtime package verification passed after the retry scratch-buffer change. |
| 2026-05-21 | `GOWORK=off go test -race ./internal/runtime/delivery -run TestRetryWheel -count=1` | Pass | Race check for retry-wheel tests passed. |
| 2026-05-21 | `git diff --check` and `GOWORK=off go test ./internal/runtime/delivery ./internal/app -count=1` | Pass | Whitespace, delivery runtime, and app verification passed before committing the retry allocation fix. |
| 2026-05-21 | `GOWORK=off go test ./...` | Pass | Full unit-test suite passed before committing the retry allocation fix. |
| 2026-05-21 | Continued `clean15-lazymbox-u` polling on image predating PERF-027 | Fail | Later status reached `messages_sent=492481`, `send_errors=127`, `recv_errors=0`; node log scan still showed no recent active-hint/timeout/corrupt lines. |
| 2026-05-21 | `DOCKER_BUILDKIT=0 docker build --pull=false -t wukongim-dev:local .` | Pass | Rebuilt local image from `d315d4a1`; image ID `c23121832344`. |
| 2026-05-21 | Fresh-image clean 1/s after PERF-027, `WK_SIM_UID_PREFIX=clean16-retrybuf-u` | Partial | The run hit an early cold-start burst (`send_errors=40` by `messages_sent=44290`) and then held that error count through at least `messages_sent=157996`, `recv_errors=0`. Recent node logs only showed startup controller election noise. |
| 2026-05-21 | `/tmp/wksim-rate1-retrybuf-alloc-20260521-114614` alloc/heap capture | Evidence | `RetryWheel.PopDue` disappeared from allocation tops. The next confirmed allocation slice was `pooledLoopDriver.submitCommand`, about `0.16-0.17GB` per node (`2.96-3.26%` flat alloc_space), caused by one reply channel per loop command. |
| 2026-05-21 | `GOWORK=off go test ./pkg/channel/replica -run TestPooledLoopReplyPoolReusesReadyReplies -count=1` | Red then pass | Regression test failed before adding pooled reply channels (`acquirePooledLoopReply` missing and wait helper returned no reuse signal), then passed after ready replies were safely returned to a pool. |
| 2026-05-21 | `GOWORK=off go test ./pkg/channel/replica -run 'TestPooledLoop(CommandWaitPrefersReadyReplyOverDone\|CommandWaitReturnsNotLeaderWhenDoneWinsWithoutReply\|ReplyPoolReusesReadyReplies\|SerializesReplicaCommands\|MailboxRingWrapsWithoutDropping)' -count=1` | Pass | Focused pooled-loop behavior and reply-pool allocation tests passed. |
| 2026-05-21 | `GOWORK=off go test ./pkg/channel/replica -count=1` | Pass | Full replica package verification passed after reply pooling. |
| 2026-05-21 | `GOWORK=off go test -race ./pkg/channel/replica -run 'TestPooledLoop(CommandWaitPrefersReadyReplyOverDone\|CommandWaitReturnsNotLeaderWhenDoneWinsWithoutReply\|ReplyPoolReusesReadyReplies)' -count=1` | Pass | Race check for pooled-loop reply waiting and pool reuse passed. |
| 2026-05-21 | `GOWORK=off go test ./pkg/channel/replica -run '^$' -bench 'BenchmarkPooledMailboxSubmitResult' -benchmem -count=3` | Pass | Pooled mailbox submit-result benchmark stayed at `0 B/op`, `0 allocs/op` (`155.8-158.5 ns/op`). |
| 2026-05-21 | `git diff --check` and `GOWORK=off go test ./pkg/channel/replica ./pkg/channel/runtime ./internal/app -count=1` | Pass | Whitespace, replica/runtime, and app verification passed before committing the reply-pool fix. |
| 2026-05-21 | `GOWORK=off go test ./...` | Pass | Full unit-test suite passed before committing the reply-pool fix. |
| 2026-05-21 | Continued `clean16-retrybuf-u` polling on image predating PERF-028 | Fail | Later status reached `messages_sent=392151`, `send_errors=74`, `recv_errors=0`; a recent five-minute node/simulator log scan showed no warning/error lines. |
| 2026-05-21 | `DOCKER_BUILDKIT=0 docker build --pull=false -t wukongim-dev:local .` | Pass | Rebuilt local image from `f8e92cda`; image ID `3a3b25968830`. |
| 2026-05-21 | Fresh-image clean 1/s after PERF-028, `WK_SIM_UID_PREFIX=clean17-replypool-u` | Pass so far | The run reached `messages_sent=112252`, `send_errors=0`, `recv_errors=0` during the first alloc capture window. |
| 2026-05-21 | `/tmp/wksim-rate1-replypool-alloc-20260521-115953` alloc/heap capture | Evidence | `pooledLoopDriver.submitCommand` and `RetryWheel.PopDue` no longer appeared in allocation tops. The next confirmed app-level allocation slice was `deliveryPresenceCache.EndpointsByUIDs`, visible at about `0.04-0.05GB` flat/cum on nodes 1 and 3 because person delivery calls single-UID presence lookups through the batch API. |
| 2026-05-21 | `GOWORK=off go test ./internal/app -run TestDeliveryPresenceCacheSingleUIDUsesSingleLookup -count=1` | Red then pass | Regression test failed while `EndpointsByUID` delegated to `EndpointsByUIDs` (`uidCalls` empty and batch lookup used), then passed after adding a direct single-UID cache path with a one-allocation cached-hit budget. |
| 2026-05-21 | `GOWORK=off go test ./internal/app -run 'TestDeliveryPresenceCache(SingleUIDUsesSingleLookup\|ReusesBatchRoutesWithinTTL\|ExpiresBatchRoutes)' -count=1` | Pass | Focused presence-cache behavior tests passed. |
| 2026-05-21 | `GOWORK=off go test -race ./internal/app -run TestDeliveryPresenceCache -count=1` | Pass | Race check for presence-cache tests passed. |
| 2026-05-21 | `GOWORK=off go test ./internal/app -count=1` | Pass | Full app package verification passed after the single-UID presence cache path. |
| 2026-05-21 | `git diff --check` and `GOWORK=off go test ./internal/app ./internal/usecase/delivery -count=1` | Pass | Whitespace, app, and delivery-usecase verification passed before committing the presence cache fix. |
| 2026-05-21 | `GOWORK=off go test ./...` | Pass | Full unit-test suite passed before committing the presence cache fix. |
| 2026-05-21 | Continued `clean17-replypool-u` polling on image predating PERF-029 | Fail | Later status reached `messages_sent=447121`, `send_errors=689`, `recv_errors=0`; recent logs again showed `conversation.active_hint` deadline warnings on all nodes plus isolated `channel: not ready` route-status warnings and one canceled send. This keeps PERF-026 open for a fresh post-PERF-029 run. |
| 2026-05-21 | `DOCKER_BUILDKIT=0 docker build --pull=false -t wukongim-dev:local .` | Pass | Rebuilt local image from `e0fbbfa1`; image ID `6f6596eb9891`. |
| 2026-05-21 | Fresh-image clean 1/s after PERF-029, `WK_SIM_UID_PREFIX=clean18-prescache-u` | Pass so far | The run reached `messages_sent=113312`, `send_errors=0`, `recv_errors=0` during the alloc capture window. |
| 2026-05-21 | `/tmp/wksim-rate1-prescache-alloc-20260521-121546` alloc/heap capture | Evidence | `deliveryPresenceCache.EndpointsByUIDs`, `pooledLoopDriver.submitCommand`, and `RetryWheel.PopDue` no longer appeared in allocation tops. The next confirmed allocation slice was `replica.cloneRecords`, about `0.07-0.10GB` per node, split across read-log results and follower apply effects. |
| 2026-05-21 | `GOWORK=off go test ./pkg/channel/replica -run 'Test(ReadLogResultUsesOwnedRecordPayloads\|ExecuteFollowerApplyEffectUsesOwnedRecordPayloads)' -count=1` | Red then pass | Regression tests failed while read-log results and follower durable apply requests received cloned payload buffers, then passed after those already-owned record slices were transferred without another deep copy. |
| 2026-05-21 | `GOWORK=off go test ./pkg/channel/replica -run 'Test(ReadLogResultUsesOwnedRecordPayloads\|ExecuteFollowerApplyEffectUsesOwnedRecordPayloads\|FetchReadLogResultAfterLeadershipLossIsFenced\|FetchReadLogResultAfterSameGenerationLEORegressionIsFenced\|ApplyFetchAdvancesCheckpointToMinLeaderHWAndLEO)' -count=1` | Pass | Focused read-log/follower-apply behavior tests passed. |
| 2026-05-21 | `GOWORK=off go test ./pkg/channel/replica -count=1` | Pass | Full replica package verification passed after removing duplicate owned-record clones. |
| 2026-05-21 | `GOWORK=off go test -race ./pkg/channel/replica -run 'Test(ReadLogResultUsesOwnedRecordPayloads\|ExecuteFollowerApplyEffectUsesOwnedRecordPayloads)' -count=1` | Pass | Race check for the new owned-record transfer tests passed. |
| 2026-05-21 | `git diff --check` and `GOWORK=off go test ./pkg/channel/replica ./pkg/channel/runtime ./pkg/channel/store ./internal/app -count=1` | Pass | Whitespace, channel replica/runtime/store, and app verification passed before committing the owned-record clone fix. |
| 2026-05-21 | `GOWORK=off go test ./...` | Pass | Full unit-test suite passed before committing the owned-record clone fix. |
| 2026-05-21 | Continued `clean18-prescache-u` polling on image predating PERF-030 | Fail | Later status reached `messages_sent=622046`, `send_errors=657`, `recv_errors=0`; recent logs showed a `channel: not ready` route-status warning, so sustained 1/s still needs another root-cause pass after PERF-030 is rebuilt. |
| 2026-05-21 | `DOCKER_BUILDKIT=0 docker build --pull=false -t wukongim-dev:local .` | Pass | Rebuilt local image from `0bff0da8`; image ID `5a712709554c`. |
| 2026-05-21 | Fresh-image clean 1/s after PERF-030, `WK_SIM_UID_PREFIX=clean19-ownedrecords-u` | Pass so far | After initial prepare/connect, the run reached `messages_sent=49545`, `send_errors=0`, `recv_errors=0` during the alloc capture window. |
| 2026-05-21 | `/tmp/wksim-rate1-ownedrecords-alloc-20260521-124510` alloc/heap capture | Evidence | `replica.cloneRecords` dropped to `12-14.5MB` per node and only remained in the necessary follower apply boundary. The next confirmed allocation slice was outbound WKProto msg-key sealing: node1 had `EncryptPayloadWithCrypto` at `103.04MB`, with `80.03MB` called by `msgKeyWithCrypto`, plus `RecvPacket.VerityString` at about `0.03GB`. |
| 2026-05-21 | `GOWORK=off go test ./pkg/protocol/wkprotoenc -run TestSealRecvPacketWithCryptoAvoidsMsgKeyScratchAllocations -count=1` | Red then pass | Regression test first failed at `8.0 allocs/op`, then passed after recv msg-key signing reused verification bytes and scratch buffers instead of materializing a string and throwaway encrypted/base64 slices. |
| 2026-05-21 | `GOWORK=off go test ./pkg/protocol/wkprotoenc ./internal/gateway/wkprotoenc -count=1` | Pass | Protocol encryption behavior and gateway wrapper tests passed after optimizing msg-key scratch allocation. |
| 2026-05-21 | `GOWORK=off go test ./internal/gateway/wkprotoenc -run '^$' -bench 'Benchmark(SessionCryptoSealRecvPacket\|SessionCryptoEncryptPayload)$' -benchmem -count=3` | Pass | `BenchmarkSessionCryptoSealRecvPacket` improved from the pre-fix `816 B/op`, `8 allocs/op` to `448 B/op`, `6 allocs/op` (`671-723 ns/op` in the post-fix run); payload encryption stayed at `112 B/op`, `2 allocs/op`. |
| 2026-05-21 | `GOWORK=off go test -race ./pkg/protocol/wkprotoenc ./internal/gateway/wkprotoenc -count=1` | Pass | Race check for protocol encryption and gateway wrapper tests passed; the allocation-budget test is skipped under race instrumentation. |
| 2026-05-21 | `git diff --check` and `GOWORK=off go test ./pkg/protocol/wkprotoenc ./internal/gateway/wkprotoenc ./internal/gateway/protocol/wkproto ./pkg/protocol/codec ./internal/app -count=1` | Pass | Whitespace, protocol encryption/codec, gateway WKProto adapter, and app verification passed before committing the msg-key scratch fix. |
| 2026-05-21 | `GOWORK=off go test ./...` | Pass | Full unit-test suite passed before committing the msg-key scratch fix. |
| 2026-05-21 | Continued `clean19-ownedrecords-u` polling on image predating PERF-031 | Fail | Later status reached `messages_sent=863420`, `send_errors=315`, `recv_errors=0`; a recent ten-minute log scan showed no warning/error lines, so the next sustained-error root-cause pass should use a rebuilt image that includes PERF-031. |
| 2026-05-21 | `DOCKER_BUILDKIT=0 docker build --pull=false -t wukongim-dev:local .` | Pass | Rebuilt local image from `2aca81f5`; image ID `b9c3b9183d8d`. |
| 2026-05-21 | Fresh-image clean 1/s after PERF-031, `WK_SIM_UID_PREFIX=clean20-msgkey-u` | Pass so far | The run reached `messages_sent=135034`, `send_errors=0`, `recv_errors=0` during the alloc capture window. |
| 2026-05-21 | `/tmp/wksim-rate1-msgkey-alloc-20260521-132347` alloc/heap capture | Evidence | PERF-031 verified: `VerityString` disappeared and `EncryptPayloadWithCrypto` was now only called by `SealRecvPacketWithCrypto` (`66-83MB` per node), not by `msgKeyWithCrypto`. The next confirmed app-level allocation slice was static subscriber snapshot paging: `nextFilteredSnapshotPage` accounted for about `0.04-0.06GB` per node. |
| 2026-05-21 | `GOWORK=off go test ./internal/usecase/delivery -run TestSubscriberResolverStaticSnapshotPageAvoidsAllocation -count=1` | Red then pass | Regression test failed at `1.0 allocs/op` for repeated person-channel snapshot pages, then passed at `0 allocs/op` after returning unfiltered static snapshot windows directly. |
| 2026-05-21 | `GOWORK=off go test ./internal/usecase/delivery ./internal/app -count=1` | Pass | Delivery usecase and app routing verification passed after removing static snapshot page allocation. |
| 2026-05-21 | `git diff --check`, `GOWORK=off go test ./internal/usecase/delivery ./internal/app -count=1`, and `GOWORK=off go test -race ./internal/usecase/delivery -run TestSubscriberResolver -count=1` | Pass | Whitespace, delivery/app behavior, and subscriber resolver race verification passed before committing the static snapshot page fix. |
| 2026-05-21 | `GOWORK=off go test ./...` | Pass | Full unit-test suite passed before committing the static snapshot page fix. |
| 2026-05-21 | Continued `clean20-msgkey-u` polling on image predating PERF-032 | Fail | Later status reached `messages_sent=361338`, `send_errors=107`, `recv_errors=0`; a recent five-minute log scan showed no warning/error lines, so send-error root cause still needs a post-PERF-032 rebuilt run. |

| ID | Status | Area | Symptom | Root Cause | Fix Commit |
| --- | --- | --- | --- | --- | --- |
| PERF-024 | Fixed | Delivery ack index allocations | Clean 1/s allocation profiles showed `AckIndex.Bind` as a recurring allocation source while most wk-sim routes have only one outstanding client ack per session. | The ack reverse index allocated a per-session `map[ackKey]struct{}` on every first bind, even though the common case only needs one reverse key for session-close cleanup and legacy lookup. | `f0b51526` |
| PERF-025 | Fixed | Channel replica mailbox memory | Fresh clean 1/s heap profiles showed `replica.newPooledLoopDriver` retaining roughly `133-143MB` per node, the largest Go heap owner after the AckIndex fix. | Pooled replica execution preallocated the full mailbox slice for every channel runtime (`2048` `pooledLoopMessage` slots by default), even though most channels have shallow queues and only need capacity during bursts. | `5c236f51` |
| PERF-026 | Investigating | Conversation active-hint flush pressure | The clean 1/s `clean14-ackidx-u` run stayed error-free past 227k messages but later hit `send_errors=247`; recent node logs showed active-hint flush deadline warnings on all nodes. | Unknown yet. PERF-020 bounded the periodic flush, but sustained 1/s can still align best-effort active-hint writes with foreground send pressure. Needs fresh evidence on a rebuilt image that includes PERF-025 before changing code. | Not fixed |
| PERF-027 | Fixed | Delivery retry timer allocations | Clean 1/s allocation profiles after PERF-025 showed `RetryWheel.PopDue` allocating about `56-59MB` per node, and send errors continued without active-hint warnings on the rebuilt image. | Every retry tick allocated a new due-entry result slice even though the shard drains retry timers serially and can safely reuse a scratch buffer between ticks. | `d315d4a1` |
| PERF-028 | Fixed | Channel replica loop command allocations | Clean 1/s allocation profiles after PERF-027 showed `pooledLoopDriver.submitCommand` allocating about `0.16-0.17GB` per node while processing append/fetch/progress loop commands. | Pooled replica loop command submission allocated a fresh buffered reply channel for every command; successful replies can be safely reused, while canceled commands must keep their private channel to avoid stale sends. | `f8e92cda` |
| PERF-029 | Fixed | Delivery presence cache single-UID lookup | Clean 1/s allocation profiles after PERF-028 still showed `deliveryPresenceCache.EndpointsByUIDs` in the allocation top list even though person delivery resolves one UID at a time. | `deliveryPresenceCache.EndpointsByUID` delegated to the batch `EndpointsByUIDs` path, allocating a result map and missing slice for every single-UID person lookup instead of using the authoritative single-UID API and cache entry directly. | `e0fbbfa1` |
| PERF-030 | Fixed | Channel replica owned-record cloning | Clean 1/s allocation profiles after PERF-029 showed `replica.cloneRecords` at about `0.07-0.10GB` per node after larger allocation sources were removed. | Read-log result handling and follower durable apply both deep-cloned record payloads even though those boundaries already own the record slices after `LogStore.Read` or after the earlier follower effect clone. | `0bff0da8` |
| PERF-031 | Fixed | WKProto recv msg-key scratch allocations | Clean 1/s allocation profiles after PERF-030 showed outbound WKProto sealing as the next app-level allocation slice: `EncryptPayloadWithCrypto` was `91-103MB` per node, with node1 attributing `80.03MB` to `msgKeyWithCrypto`, and `RecvPacket.VerityString` also allocated during recv msg-key signing. | Recv msg-key signing converted verification bytes to a string and back to bytes, then called payload encryption only to hash its base64 result, allocating throwaway encrypted/base64 buffers for every sealed recv packet. | `2aca81f5` |
| PERF-032 | Fixed | Static subscriber snapshot page allocations | Clean 1/s allocation profiles after PERF-031 showed `nextFilteredSnapshotPage` allocating about `0.04-0.06GB` per node while resolving derived/static subscriber pages. | Static snapshot paging allocated a new result slice even when no `seen` filters were present; person and other derived snapshots can return the immutable window from the token's static snapshot directly. | `f8a78045` |

## Continuation: isolated worktree Run 16

This continuation investigated sustained sendack timeouts on the fresh image that included PERF-032. The server-side diagnostics retained only successful events while wk-sim still accumulated send errors, so this pass targeted simulator-side read contention that could hide foreground sendack waits behind long background idle reads.

| Time | Command | Result | Notes |
| --- | --- | --- | --- |
| 2026-05-21 | Fresh-image clean 1/s after PERF-032, `WK_SIM_UID_PREFIX=clean21-staticpage-u` | Fail | The run first hit retry around `messages_sent=74158`, `send_errors=66`; later it recovered to `messages_sent=218526`, `send_errors=134`, `recv_errors=0`. Last errors were wk-sim sendack read timeouts/context cancellations, not server-side retained diagnostic errors. |
| 2026-05-21 | `/tmp/wksim-rate1-staticpage-alloc-20260521-134247` alloc/heap/CPU capture | Evidence | `nextFilteredSnapshotPage` and `nextUnfilteredSnapshotPage` disappeared after PERF-032. Remaining profiles were split across replica fetch, WKProto frame encode/seal, transport RPC encode/decode, tag presence batch lookup, and state publication; no single server allocation fix explained sendack timeouts. |
| 2026-05-21 | `/tmp/wksim-current-pref033-evidence-20260521-140739` diagnostics snapshot | Evidence | Existing pre-fix simulator run reached `messages_sent=1112408`, `send_errors=417`, `recv_errors=0`; node diagnostics on ports 15001/15002/15003 each returned `Counter({'ok': 500})`, with slow but successful lane-poll events and no retained error events. |
| 2026-05-21 | `GOWORK=off go test ./internal/bench/workload -run TestAutoRecvAckIdleReadYieldsToForegroundMatcher -count=1` | Red then pass | Regression test first failed because the auto recv-ack drainer used an unbounded parent read context (`hasDeadline=false`), then passed after idle background reads were bounded and yielded the shared reader to queued foreground matchers. |
| 2026-05-21 | `GOWORK=off go test ./internal/bench/workload -run 'TestAutoRecvAck(IdleReadYieldsToForegroundMatcher\|ContinuesAfterIdleReadTimeout\|CanDropUnverifiedRecvFrames\|ReadMatcherDropsUnverifiedRecvFrames\|DrainsAndBuffersRecvFrames\|SuppressesDuplicateExplicitRecvAck)' -count=1` | Pass | Focused auto recv-ack behavior stayed green after the reader-yield change. |
| 2026-05-21 | `GOWORK=off go test ./internal/bench/workload ./internal/bench/worker ./internal/bench/devsim -count=1` | Pass | Focused bench workload/worker/devsim verification passed before broader validation. |
| 2026-05-21 | `GOWORK=off go test -race ./internal/bench/workload -run TestAutoRecvAck -count=1` | Pass | Race check for auto recv-ack tests passed. |

| ID | Status | Area | Symptom | Root Cause | Fix Commit |
| --- | --- | --- | --- | --- | --- |
| PERF-033 | Fixed | wk-sim auto recv-ack read contention | Sustained clean 1/s runs could accumulate wk-sim sendack read timeouts while server diagnostics retained only successful events. | The background auto recv-ack drainer held each wrapped client's shared `ReadFrame` slot with the long dev-sim operation timeout when the socket was idle; foreground sendack/recv matchers queued behind that idle read and could exhaust their shorter operation timeout before owning the reader. | Current change |
