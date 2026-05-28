# wk-sim Stress Findings

This document records issues found while running the Docker Compose `wk-sim`
load loop. Keep entries concise so the final review is easy to scan.

## 2026-05-20 Run 1

Environment:
- Worktree: `.worktrees/wksim-bug-hunt-20260520`
- Baseline unit command: `GOWORK=off go test ./...`
- Compose command: `WK_SIM_VERIFY_RECV=sampled WK_SIM_TRAFFIC_CONCURRENCY=128 docker compose --profile dev-sim up -d --build wk-sim`
- Workload: 1000 users, 500 person channels, 500 group channels, 10 group members, 0.25/s per channel.

### Issue 1: verified receive traffic double-sent recvack frames

Evidence:
- Under sampled receive verification, `/status` repeatedly entered `retrying`.
- Example error: `person recvack: write tcp ...:5100: i/o timeout`.
- Counters after the first minute showed send and receive errors while CPU remained low, pointing to protocol write/backpressure rather than node saturation.

Root cause:
- `StartAutoRecvAck` enables automatic recvack on `matchingPersonClient`.
- Verified person/group workloads still called `RecvAck` explicitly after reading the same recv frame.
- A verified recv could therefore be acknowledged once by the matching reader and again by the explicit workload call.

Fix:
- When automatic recvack is enabled on `matchingPersonClient`, explicit `RecvAck` calls are treated as already handled by the matching reader.
- Regression test: `TestAutoRecvAckSuppressesDuplicateExplicitRecvAck`.

### Issue 2: dev-sim status kept stale readiness errors after target became ready

Evidence:
- `/status` stayed in `waiting` for more than a minute during prepare/connect and continued to show initial `connection refused` errors even after all node `/healthz`, `/readyz`, and `/bench/v1/capabilities` endpoints returned 200.

Root cause:
- `Runner.waitReady` stored every transient readiness error but only cleared `last_error` after the simulator reached `running`.
- Slow prepare/connect made the status API report stale target failures during successful startup work.

Fix:
- Clear `last_error` immediately after target readiness succeeds.
- Regression test: `TestRunnerClearsTransientReadinessErrorBeforePrepare`.

### Issue 3: person send path times out refreshing channel metadata under startup load

Status: observed, next investigation cycle.

Evidence:
- After rebuilding with the recvack fix, sampled verification remained `running` instead of continuous `retrying`, but counters still showed send and receive errors.
- Node logs repeatedly showed `message.send.refresh.failed` for person channels with `context deadline exceeded`, followed by gateway `send_failed` warnings.
- Example channel: `devsim-u-67@devsim-u-66`, channel type `1`.

Initial hypothesis:
- The person-channel metadata refresh path is missing a fast negative/creation cache or has insufficient concurrency during many cold person-channel sends.
- This will be investigated with a focused unit test before any production change.

## 2026-05-20 Run 2

Environment:
- Existing three-node Compose cluster from Run 1.
- Isolation profiles used `--no-build --force-recreate wk-sim` with unique `WK_SIM_UID_PREFIX` values.

### Issue 3 update: person metadata refresh timeout was not reproduced on clean data

Evidence:
- After cleaning `docker/dev-cluster` and `docker/dev-sim`, the default smoke command passed with `connected_users=1000`, `messages_sent=2209`, `send_errors=0`, and `recv_errors=0`.
- A person-only sampled profile (`40` users, `10` person channels, `0` groups, `0.5/s`, concurrency `16`) ran for more than one minute and reached `messages_sent=348`, `send_errors=0`, `recv_errors=0`.

Status:
- Keep as a non-reproduced startup/cold-state observation. No production fix was made in this cycle.

### Issue 4: group sampled verification only reached the local delivery-tag partition

Evidence:
- A group-only sampled profile (`40` users, `0` person channels, `3` groups, `12` members, `0.5/s`, concurrency `16`) repeatedly alternated between `running` and `retrying`.
- `/status` stabilized at `messages_sent=2`, `send_errors=0`, `recv_errors=2`, and `last_error="context deadline exceeded"`.
- A temporary WKProto probe prepared one group with four subscribers across three gateways. The sendack succeeded, but only the subscriber connected to the sender/owner node received the group `RecvPacket`; subscribers on the other two nodes timed out.

Root cause:
- `tagDeliveryResolver` expanded only the current node's delivery-tag partition (`deliveryTagLocalUIDs`) when a leader tag contained multiple node partitions.
- The channel owner submitted realtime fanout immediately only for that local partition, so remote partition recipients depended on slow replay paths instead of receiving the committed message promptly.

Fix:
- Resolve every UID present in the current node-local tag body when the leader has the full tag, while follower-local tag bodies still contain only their stored partition.
- Regression test: `TestDeliveryRoutingExpandsAllLeaderTagPartitionsForFanout`.

Verification:
- Targeted routing tests passed with `GOWORK=off go test ./internal/app -run 'TestDeliveryRouting|TestDistributedDeliveryPush|TestLocalDeliveryPush' -count=1`.
- Broader focused tests passed with `GOWORK=off go test ./internal/app ./internal/bench/... ./cmd/wkbench -count=1`.
- Rebuilt Compose group-only sampled profile with prefix `groupfix-u` stayed `running` for more than 5 minutes and reached `messages_sent=494`, `send_errors=0`, `recv_errors=0`, `last_error=""`.

## 2026-05-20 Run 3

Environment:
- Same three-node Compose cluster, rebuilt after `fix: fan out group delivery tag partitions`.
- Profiles used unique `WK_SIM_UID_PREFIX` values and targeted the `wk-sim` service through Docker Compose.

Healthy checks:
- Default mixed profile (`1000` users, `500` person channels, `500` group channels, `0.25/s`, concurrency `128`, receive verification `none`) reached `messages_sent=13525`, `send_errors=0`, `recv_errors=0`.
- Reduced sampled mixed profile (`40` users, `10` person channels, `3` groups, `0.5/s`, concurrency `16`) reached `messages_sent=582`, `send_errors=0`, `recv_errors=0`.
- Larger sampled group profile (`120` users, `10` groups, `40` members, `0.3/s`, concurrency `32`) reached `messages_sent=269`, `send_errors=0`, `recv_errors=0`.

### Issue 5: dev-sim skipped warmup before high-rate group traffic

Evidence:
- A person-only high-rate profile (`500` users, `250` person channels, `1/s`, concurrency `256`, receive verification `none`) reached `messages_sent=6002`, `send_errors=0`, `recv_errors=0`.
- A group-only profile with the same rate shape (`500` users, `250` groups, `10` members, `1/s`, concurrency `256`, receive verification `none`) failed at the first cold run window with `messages_sent=577`, `send_errors=43`, `recv_errors=0`, and `last_error="context deadline exceeded"`.
- Node logs during the failure showed many cold `channelmeta.bootstrap` operations for new group channels, while the simulator immediately entered measured traffic.

Root cause:
- `wkbench` worker flows support a `warmup` phase, and group/person workloads already run warmup at a reduced rate.
- The long-running `dev-sim` supervisor derived no warmup duration and called only prepare/connect before starting repeated measured run windows.
- High-rate group stress therefore mixed cold channel runtime metadata and delivery-tag materialization with the measured traffic window, causing transient sendack timeouts before the system reached steady state.

Fix:
- Add `traffic.warmup` to dev-sim config plus `WK_SIM_WARMUP` override, defaulting Compose and `docker/sim/dev-sim.yaml` to `10s`.
- Run the in-process worker warmup once after prepare/connect and before `/status` transitions to `running`.
- Capture a fresh metrics baseline after warmup so warmup sends and any warmup-only counters are not reported as measured `/status` traffic.
- Update `internal/bench/FLOW.md` to document `prepare -> connect -> warmup -> run windows` for dev-sim.

Verification:
- Regression tests: `TestRunnerRunsWarmupBeforeTraffic` and `TestRunnerUsesWarmupMetricsAsCounterBaseline`.
- Focused tests passed with `GOWORK=off go test ./internal/bench/devsim -count=1` and `GOWORK=off go test ./internal/bench/... ./cmd/wkbench -count=1`.
- Rebuilt Docker image locally and reran the group-only high-rate profile with `WK_SIM_WARMUP=10s`; measured traffic reached `messages_sent=6096`, `send_errors=0`, `recv_errors=0`, `last_error=""`.

## 2026-05-20 Run 4

Environment:
- Same rebuilt three-node Compose cluster after Issue 5 fix.

Stress checks:
- High-rate mixed profile (`500` users, `250` person channels, `250` group channels, `10` group members, `1/s`, concurrency `256`, `WK_SIM_WARMUP=10s`, receive verification `none`) needed several prepare/connect retries on the already-stressed local Compose stack, then reached `running`.
- When `go test ./...` was run concurrently with that stress profile, the simulator later accumulated send errors. A clean rerun without concurrent Go tests reached `messages_sent=40924`, `send_errors=0`, `recv_errors=0`, `last_error=""`.

Status:
- No new reproducible traffic bug was found after the warmup fix. The remaining prepare/connect retries occurred before traffic and recovered automatically.

## 2026-05-20 Run 5

Environment:
- Fresh isolated worktree `.worktrees/bughunt-wk-sim-20260520-180129`.
- Existing Compose project name `wksim-bug-hunt-20260520`, restarted with this worktree's bind-mounted config/data paths.
- Default mixed profile: `1000` users, `500` person channels, `500` group channels, `0.25/s`, concurrency `128`, receive verification `none`, `WK_SIM_WARMUP=10s`.

### Issue 6: low-rate warmup did not activate every default Compose channel

Evidence:
- Default `wk-sim` smoke passed once `/status` reached `messages_sent=2216`, `send_errors=0`, `recv_errors=0`.
- Continued polling showed the same run briefly entered `retrying` at `messages_sent=8099`, `send_errors=22`, `last_error="context deadline exceeded\ncontext canceled"`, then recovered and continued sending.
- Node logs after `/status` switched to `running` showed many `channelmeta.bootstrap` events for previously cold person and group channels.
- Repeating the same default profile with `WK_SIM_WARMUP=40s` and a new UID prefix reached `messages_sent=29264`, `send_errors=0`, `recv_errors=0`.

Root cause:
- Warmup used `10%` of the configured per-channel rate. With the default `0.25/s` rate and `10s` warmup, each workload sent only `125` warmup messages for `500` channels.
- The remaining channels were first activated during measured run windows, so cold runtime metadata bootstrap work could still overlap with traffic and cause sendack timeouts.

Fix:
- Person and group warmup now preserve the reduced-rate behavior but raise the warmup rate enough to schedule at least one message per assigned channel for the configured warmup duration.
- Regression tests: `TestPersonWorkloadWarmupTouchesEveryPairAtLeastOnce` and `TestGroupWorkloadWarmupTouchesEveryChannelAtLeastOnce`.

Verification:
- The regression tests failed before the fix and passed after the warmup rate change.
- Focused suites passed with `GOWORK=off go test ./internal/bench/workload ./internal/bench/devsim ./internal/bench/worker ./cmd/wkbench -count=1`.
- Rebuilt `wukongim-dev:local` and reran the default Compose `wk-sim` profile with `WK_SIM_WARMUP=10s` and a new UID prefix; continued polling reached `messages_sent=26930`, `send_errors=0`, `recv_errors=0`.

## 2026-05-20 Run 6

Post-fix search:
- Default mixed profile after Issue 6 fix stayed healthy through `messages_sent=26930`, `send_errors=0`, `recv_errors=0`.
- Reduced sampled mixed profile (`40` users, `10` person channels, `3` groups, `0.5/s`, concurrency `16`, sampled receive verification) stayed healthy through `messages_sent=840`, `send_errors=0`, `recv_errors=0`.
- High-rate mixed stress (`500` users, `250` person channels, `250` groups, `1/s`, concurrency `256`) produced intermittent send timeouts after initial traffic and recovered.
- Accumulated-data `0.5/s` default-sized stress (`1000` users, `500` person channels, `500` groups) did not reach `running` within the smoke timeout on this machine and showed client-canceled send attempts during retry cleanup.

Status:
- No additional functional correctness bug was isolated after Issue 6.
- The high-rate observations are treated as a local Docker capacity/performance boundary unless reproduced on a clean stack with a required acceptance target.

## 2026-05-20 Run 7

Environment:
- Main worktree with the default three-node Compose cluster and `wk-sim`.
- High-rate mixed stress used unique `WK_SIM_UID_PREFIX` values, `WK_SIM_RATE=1/s`, `WK_SIM_TRAFFIC_CONCURRENCY=256`, receive verification `none`, and `WK_SIM_WARMUP=10s`.

### Issue 7: follower durable apply could be hidden by same-leader metadata refresh

Evidence:
- A high-rate mixed stress run failed with send timeouts and node diagnostics reporting `channel: corrupt state`.
- Error diagnostics concentrated in `replica.follower.apply_durable` and `replica.leader.durable_append_store`, often after metadata refreshes on the same channel leader.
- A regression test reproduced the race by blocking follower durable apply, applying same-leader metadata with the same channel epoch and leader, then releasing the already-committed durable write. Before the fix, the apply returned `channel: stale metadata` and runtime LEO stayed behind durable LEO.

Root cause:
- Follower durable apply validates the effect fence before mutating storage.
- Same-leader metadata refresh can still increment the local `roleGeneration` while that durable write is in progress.
- The result path treated any role-generation mismatch as stale, even when channel key, channel epoch, and leader were unchanged. The committed store write was therefore not published back to runtime state, leaving runtime LEO below the durable log. Later append/fetch paths could observe that split-brain state and report `channel: corrupt state`.

Fix:
- Allow a follower apply result with a role-generation mismatch to publish only when channel key, channel epoch, and leader are unchanged and the replica is still a follower or fenced leader.
- Epoch and leader changes continue to fence stale durable results.
- Regression test: `TestApplyFetchResultAfterSameLeaderMetaRefreshPublishesDurableLEO`.
- Flow documentation updated in `pkg/channel/replica/FLOW.md`.

Verification:
- Regression test was verified red before the code change with `channel: stale metadata`.
- Focused tests passed with `GOWORK=off go test ./pkg/channel/replica -run 'TestApplyFetch(ResultAfterSameLeaderMetaRefreshPublishesDurableLEO|StaleResultAfterMetaChangeIsFenced|TruncateResultAfterMetaChangeIsFenced)' -count=1`.
- Full replica package tests passed with `GOWORK=off go test ./pkg/channel/replica -count=1`.
- Rebuilt `wukongim-dev:local` and reran the same high-rate mixed stress. The smoke still timed out because the strict gate requires `send_errors=0`, but diagnostics no longer returned corrupt-state errors and node logs did not show `channel: corrupt state`; remaining errors were send timeouts around slot leadership churn.

Status:
- Corrupt-state defect fixed.
- Remaining high-rate timeout/churn observation is the next investigation target.

## 2026-05-20 Run 8

Environment:
- Main worktree after Issue 7 was committed.
- Existing Compose cluster had accumulated data from repeated default and high-rate `wk-sim` runs.
- Default and high-rate `wk-sim` profiles were rerun with fresh UID prefixes.

### Issue 8: warmup used measured-run operation timeout for cold channel activation

Evidence:
- After repeated stress runs, even the default Compose `wk-sim` smoke could stay in `waiting` and then retry before `/status` reached `running`.
- Node logs showed warmup traffic activating cold person/group runtime metadata, followed by many send failures with `context canceled` after the first sendack timeout canceled the warmup phase.
- Increasing `WK_SIM_WARMUP` to `40s` still failed on the same accumulated cluster, showing the problem was the per-message wait cutoff rather than only the warmup scheduling window.
- Regression tests reproduced the issue with a delayed sendack: warmup failed when the measured-run `AckTimeout` was shorter than the warmup duration.

Root cause:
- Warmup exists to absorb cold runtime metadata bootstrap before measured traffic starts, but person/group workloads reused the same per-message sendack/recv timeout as measured traffic.
- The default measured timeout is `5s`; on a loaded local Compose cluster, a cold channel activation can exceed that while still being useful warmup work.
- The first timeout canceled the whole warmup phase, and the supervisor retried from `waiting`, so the smoke never reached measured `running` traffic.

Fix:
- During warmup, person and group workloads now raise sendack/recv waits to at least the warmup duration while preserving any longer explicit timeout.
- Measured run behavior keeps the shorter operation timeout.
- Regression tests: `TestPersonWorkloadWarmupUsesWarmupDurationAsMinimumAckTimeout` and `TestGroupWorkloadWarmupUsesWarmupDurationAsMinimumAckTimeout`.
- Flow documentation updated in `internal/bench/FLOW.md`.

Verification:
- The new regression tests failed before the code change with `context deadline exceeded` and passed after the warmup timeout change.
- Full workload tests passed with `GOWORK=off go test ./internal/bench/workload -count=1`.
- Focused bench tests passed with `GOWORK=off go test ./internal/bench/... ./cmd/wkbench -count=1`.
- Rebuilt `wukongim-dev:local` and reran the default Compose `wk-sim` smoke on the accumulated local cluster with prefix `loop-fix8-u`; it reached `running` with `connected_users=1000`, `messages_sent=2239`, `send_errors=0`, `recv_errors=0`.

Status:
- Fixed and verified on the previously failing default Compose profile.

## 2026-05-20 Run 9

Post-fix search after Issues 7 and 8:
- Default Compose smoke with prefix `loop-fix8-u` reached `connected_users=1000`, `messages_sent=2239`, `send_errors=0`, `recv_errors=0`.
- High-rate mixed profile (`1000` users, `500` person channels, `500` groups, `1/s`, concurrency `256`, receive verification `none`) passed the smoke gate with prefix `loop-fix8-stress-u` and continued healthy through `messages_sent=107094`, `send_errors=0`, `recv_errors=0`.
- Diagnostics query after the high-rate run returned no error events; recent node logs had no `channel: corrupt state`, send timeout, append failure, or panic lines.
- Reduced sampled mixed profile (`40` users, `10` person channels, `3` groups, `0.5/s`, concurrency `16`, sampled receive verification) reached `messages_sent=453`, `send_errors=0`, `recv_errors=0` during continued polling.

Status:
- No additional reproducible performance or correctness issue was found in the Docker Compose `wk-sim` profiles exercised in this cycle.

## 2026-05-21 Group Fanout Triage

Environment:
- Main worktree at `643083a068f2b757f5d80b0862f6532a04d735e3` with existing uncommitted local changes.
- Evidence captured by `scripts/dev-sim-perf-triage.sh` under `docs/development/perf-runs/`.
- Target profile: clean three-node Compose stack, `500` users, `250` group channels, `10` group members, `1/s`, concurrency `256`, receive verification `none`, default warmup.

Evidence:
- Baseline `smoke-default` and `sampled-correctness` passed before stress isolation.
- First clean `group-fanout` at `1/s` and concurrency `256` failed once with `messages_sent=26303`, `send_errors=49`, `recv_errors=0`, and a client-side sendack read timeout on `sim-group-158`.
- One-variable follow-ups passed at `0.5/s` with concurrency `256`, `person-hotpath` at `1/s` with concurrency `256`, and `group-fanout` at `1/s` with concurrency `64`, `128`, `192`, and `256` on clean stacks.
- During the failing window, node CPU was moderate, simulator CPU was not saturated, channel execution queues were empty at capture time, and pprof was spread across gateway writes, transport RPC, delivery fanout, channel replication, and Pebble writes rather than one dominant hot path.
- The failing run also had startup Raft election churn and a burst of `channelmeta.bootstrap` before measured traffic, while later identical shape runs recovered and kept running.

Classification:
- Category: transient startup/load-sensitive sendack timeout in local Docker Compose group fanout, not a deterministic server defect.
- Confidence: medium. The original failure was real, but it was not reproduced by the same `1/s` + `256` shape after isolated one-variable reruns.

Decision:
- No code change in this cycle. A regression test would have to assert an exact transient 30s client read timeout, which is not a focused server defect.
- Keep `smoke-default`, `sampled-correctness`, and clean `group-fanout 1/s concurrency 256` as the current verification set.
- If this recurs, collect diagnostics around the exact sendack timeout window and test one startup variable only, preferably longer warmup or delayed measured-run start, before changing service config or code.

## 2026-05-28 wukongimv2 Single Hot Channel Triage

Environment:
- Local `cmd/wukongimv2` single-node cluster, one WKProto TCP listener, metrics enabled, clean temp data directory.
- Workload: one group channel, 128-byte payloads, SEND -> SENDACK only, 256-512 connected sender clients.

Evidence:
- With the old adaptive gateway async SEND default on a 10-GOMAXPROCS host, the server started 640 SEND shards and exposed only about 205 buffered SEND slots for the single hot channel shard. A 12k QPS run hit `async_dispatch_queue_full` and closed one session.
- Setting worker count to 64 raised per-shard capacity to 1024 and removed queue-full closes.
- After changing the adaptive default to 8 shards per GOMAXPROCS, minimum 64 and maximum 256, the same host used 80 shards and total queue capacity 81920.
- New default results: 12k target reached 11962.90 QPS, 0 errors, p99 20.16ms; 18k target reached 17929.55 QPS, 0 errors, p99 67.33ms; 24k target with 512 clients reached 20420.49 QPS, 0 errors, p99 173.89ms; 30k target did not increase throughput.

Classification:
- Category: gateway dispatch shard sizing caused hot-channel queue headroom loss before channelv2 saturated.
- Confidence: high for the queue-full symptom. At the higher plateau, remaining pressure is mixed gateway dispatch wait and channelv2 append/worker latency.

Decision:
- Default gateway async SEND worker sizing should preserve hot-channel burst room instead of scaling to hundreds of shards on small hosts.
- Keep Prometheus close-reason and async SEND queue metrics as the primary attribution surface for this class of issue.

## 2026-05-28 wukongimv2 wkbench Single Person Channel Check

Environment:
- Local `cmd/wukongimv2` single-node cluster from the current dirty worktree, clean temp data directory, metrics and pprof enabled.
- Workload: one generated person channel, two connected users, 128-byte payloads, SEND -> SENDACK only, receive verification disabled.

Evidence:
- 10 QPS smoke passed with 0 connect/sendack errors.
- Offered 50, 100, and 150 QPS completed within the configured 10s run window with 0 errors.
- Offered 155, 160, 175, and 200 QPS still returned successful reports, but worker latency sums exceeded the 10s run window, so the effective single-sender throughput flattened near 146-150 QPS.
- Offered 300 QPS failed with one client read timeout, `sendack_error_rate=0.000333`, and a coordinator wait timeout while the worker drained queued work.
- Metrics classifier reported no observed gateway or channelv2 queue pressure at capture time; the limiting behavior was the wkbench single-sender schedule/sendack model for this scenario.

Decision:
- Current measured single person-channel SEND -> SENDACK capacity is about 150 QPS for this single-sender workload.
- `wkbench` should add a hot-channel capacity mode that keeps one logical channel fixed, supports configurable sender fan-in, stops at the measured window instead of draining all scheduled messages, and reports backlog/undelivered scheduled messages.

## 2026-05-28 wukongimv2 wkbench Hot Channel Check

Environment:
- Local `cmd/wukongimv2` single-node cluster from the current dirty worktree, clean temp data directory, metrics and pprof enabled.
- Workload: one generated group channel, 128-byte payloads, SEND -> SENDACK only, receive verification disabled, `wkbench capacity hot-channel`.
- Evidence: ignored run directory `tmp/wkbench-hot-channel/20260528-231605`.

Evidence:
- Baseline 64 senders at 100 offered QPS passed with 96.40 actual QPS, 0 errors, and p99 9.49ms.
- With strict `min-actual-ratio=0.95`, 500 offered QPS failed on actual ratio only; actual was 445.30 QPS, 0 errors, p99 13.94ms, and no observed queue pressure.
- Raising sender fan-in from 64 to 256 did not improve the low-offered actual-ratio shape, so subsequent probes treated actual QPS as the primary capacity signal.
- High offered probes with 256 senders passed with 0 errors through 2.8M offered QPS; the best clean point reached 56,966.20 actual QPS with p99 19.90ms.
- 2.9M offered reported worker_failed with 0 send errors and 56,713.80 actual QPS; 3.0M and 3.2M offered produced SENDACK read timeouts.
- Metrics attribution near the boundary reported ChannelV2 append/worker p99 around 20-23ms and gateway queue depth near zero.

Classification:
- Category: current local hot-channel SEND -> SENDACK zero-error boundary is about 57k actual QPS for this single-process wkbench and single-node wukongimv2 setup.
- Confidence: medium. The boundary is reproducible enough for local guidance, but the workload has large scheduled backlog at extreme offered rates, so multi-worker wkbench should be used before treating this as the server-only ceiling.

Decision:
- Treat 2.8M offered / 56,966 actual QPS / 0 errors as the best clean result from this run.
- Treat 3.0M offered / 55,787 actual QPS / 256 send timeouts as the first clear failure point.

## 2026-05-29 wukongimv2 Single-Node Cluster 1000-Channel NoSync Check

Environment:
- Local `cmd/wukongimv2` single-node cluster from `scripts/start-wukongimv2-single-node.sh`, clean `data/wukongimv2-node-1`, metrics and pprof enabled, `WK_CLUSTER_CHANNEL_REACTOR_COUNT=32`, gateway async SEND workers 128.
- One variable changed from the durable-sync run: `WK_CLUSTER_COMMIT_COORDINATOR_SYNC=false`, which skips physical fsync for grouped channel appends.
- Workload: custom `wkbench run`, 1000 fixed group channels, 4096 online users, 10 members per channel, 128-byte payloads, SEND -> SENDACK only, receive verification disabled, 10s warmup, 15s measured window.

Evidence:
- The run completed 150,000 run-phase sends in 15s with 0 connect/sendack errors.
- Run-phase actual throughput was 10,000 QPS; run-phase SENDACK p50 was 2.32ms, p95 3.20ms, p99 3.84ms, and max 24.34ms.
- Warmup p99 was 58.78ms.
- Metrics classification reported `no_observed_queue_pressure`, gateway dispatch wait p99 about 4.97ms, and ChannelV2 append p99 about 4.97ms.

Classification:
- Category: the previous 1000-channel p99 ceiling was dominated by synchronous physical fsync/group-commit durability, not CPU saturation or gateway queue capacity.
- Confidence: high. The no-sync control removed the blocking durable fsync boundary and immediately cleared the 10k QPS / 400ms p99 target.

Decision:
- Keep durable sync as the default.
- Expose `WK_CLUSTER_COMMIT_COORDINATOR_SYNC=false` as an explicit benchmark/performance mode for controlled environments that accept loss of latest acknowledged messages on process or host crash.
