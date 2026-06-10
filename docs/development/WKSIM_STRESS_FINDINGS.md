# WKSIM Stress Findings

## 2026-06-09 three-node real-qps 5000 channelwrite triage

- Scenario: `./scripts/bench-wukongimv2-three-nodes-real-qps.sh --qps 5000`, 1000 group channels, 4096 users, 10 members, 30s measured duration.
- Baseline evidence: `docs/development/perf-runs/20260609-222608-three-node-real-qps/`.
- Baseline result: 2651.9 actual QPS, p99 2334.2ms, no send errors. `channelwrite` had no router errors/backpressure/channel-busy rejections, but default `WK_DELIVERY_CHANNEL_WRITE_EFFECT_WORKERS=0` resolved to 2 per reactor and capped `channelv2-store-append` inflight at 20 per node.
- One-variable experiment: `WK_DELIVERY_CHANNEL_WRITE_EFFECT_WORKERS=8 ./scripts/bench-wukongimv2-three-nodes-real-qps.sh --qps 5000`.
- Experiment evidence: `docs/development/perf-runs/20260609-223119-three-node-real-qps/`.
- Experiment result: 4917.6 actual QPS, p99 977.7ms, no send errors. `channelv2-store-append` and `channelv2-store-apply` reached 64 inflight workers, shifting the bottleneck to storage/replication and exposing `channelwrite` post-commit backlog on node1/node2.
- Middle experiment: `WK_DELIVERY_CHANNEL_WRITE_EFFECT_WORKERS=4 ./scripts/bench-wukongimv2-three-nodes-real-qps.sh --qps 5000`.
- Middle experiment evidence: `docs/development/perf-runs/20260609-223824-three-node-real-qps/`.
- Middle experiment result: 4338.4 actual QPS, p99 1550.2ms, no send errors, and no post-commit backlog. This confirms that 4 workers reduces the append-effect bottleneck but still does not fully utilize downstream append capacity for the 5000 offered-QPS workload.
- Fix applied: the default `ChannelWriteEffectWorkers` value was raised from 2 to 8 per reactor while preserving explicit `WK_DELIVERY_CHANNEL_WRITE_EFFECT_WORKERS` overrides.
- Fix verification evidence: `docs/development/perf-runs/20260609-224215-three-node-real-qps/`.
- Fix verification result: 4981.9 actual QPS, p99 638.8ms, no send errors, and no `channelwrite` router errors, backpressure, channel-busy rejections, local admission rejections, effect errors, or post-commit backlog.
- Finding: the baseline had a `channelwrite` append-effect concurrency bottleneck. After the default worker fix, this workload reaches the offered QPS target, but the 400ms p99 gate still fails because the remaining tail is classified under ChannelV2/storage/replication stages rather than `channelwrite` admission.

## 2026-06-09 three-node real-qps 10000 channelwrite effect-workers 16

- Scenario: `WK_DELIVERY_CHANNEL_WRITE_EFFECT_WORKERS=16 ./scripts/bench-wukongimv2-three-nodes-real-qps.sh --qps 10000`, 1000 group channels, 4096 users, 10 members, 30s measured duration.
- Evidence: `docs/development/perf-runs/20260609-224830-three-node-real-qps/`.
- Result: 8068.4 actual QPS, 0.807 actual/offered ratio, p99 769.1ms, p95 584.3ms, max 1339.1ms, and no send errors.
- `channelwrite` admission stayed open: router errors, router backpressure, channel-busy rejections, route-not-ready router results, timeouts, and local admission rejections were all zero.
- `channelwrite` post-commit pressure appeared at this offered rate: max post-commit backlog was 5275 and effect error delta was 91329, mostly post-commit `route_not_ready`/`other` results.
- Downstream pressure was visible outside `channelwrite`: `channelv2-store-append`, `channelv2-store-apply`, and `channelv2-rpc` reached worker saturation; slot scheduler showed dirty/requeued admission pressure.
- Finding: increasing effect workers to 16 does not make 10000 offered QPS pass. It removes `channelwrite` admission as the limiter, but overdrives post-commit/downstream ChannelV2, storage/apply, replication, and slot scheduling stages.

## 2026-06-09 channelwrite effect worker instrumentation

- Change: added `channelwrite` effect worker gauges for per-reactor/stage worker inflight, worker capacity, queue depth, and queue capacity. The real-qps parent summary now exposes `cw_wkr` (max effect worker utilization) and `cw_eq` (max effect queue fill).
- Verification scenario: `WK_DELIVERY_CHANNEL_WRITE_EFFECT_WORKERS=16 ./scripts/bench-wukongimv2-three-nodes-real-qps.sh --qps 10000`.
- Verification evidence: `docs/development/perf-runs/20260609-230422-three-node-real-qps/`.
- Verification result: 6949.5 actual QPS, 0.695 actual/offered ratio, p99 960.5ms, no send errors, `cw_wkr=1.000`, `cw_eq=0.023`, and max post-commit backlog 4830.
- Finding: node1 and node3 had `post_commit` effect workers at 16/16 across all 10 channelwrite reactors in the after snapshot, while max post-commit effect queue depth was only 24/1024. This confirms worker saturation in the post-commit path, not channelwrite admission backpressure or effect queue capacity exhaustion.

## 2026-06-09 channelwrite post-commit worker fix

- Root cause: `EffectWorkerCount` also bounded per-message recipient-authority dispatch concurrency, so raising effect workers from 8 to 16 multiplied both outer post-commit workers and inner recipient dispatch fanout.
- Fix: add `WK_DELIVERY_CHANNEL_WRITE_RECIPIENT_DISPATCH_CONCURRENCY` with a default of 4 and wire it independently from `WK_DELIVERY_CHANNEL_WRITE_EFFECT_WORKERS`.
- Superseded fix direction: failed post-commit recipient dispatch was briefly changed to bounded timer retry, but the final local design makes post-commit conversation/push side effects best-effort: failure is logged through `PostCommitFailureObserver`, counted by effect metrics, dropped without retry, and post-commit cursor checkpoint/replay is disabled.
- Verification focus: rerun 10000 offered QPS with `WK_DELIVERY_CHANNEL_WRITE_EFFECT_WORKERS=16 WK_DELIVERY_CHANNEL_WRITE_RECIPIENT_DISPATCH_CONCURRENCY=4` and compare `cw_wkr`, `cw_eq`, `cw_pc`, and post-commit effect errors against `docs/development/perf-runs/20260609-230422-three-node-real-qps/`.
- Verification evidence: `docs/development/perf-runs/20260609-232618-three-node-real-qps/`.
- Verification result: 8646.2 actual QPS, 0.865 actual/offered ratio, p99 700.9ms, no send errors, `cw_wkr=1.000`, `cw_eq=0.026`, and max post-commit backlog 5053.
- Follow-up finding: the fix improved actual throughput from 6949.5 to 8646.2 at 10000 offered QPS, but the 400ms p99 gate still fails. Runtime pressure remains in ChannelV2/store-apply saturation and slot scheduler dirty/requeued admission rather than channelwrite admission.

## 2026-06-09 channelwrite best-effort post-commit

- Change: post-commit conversation/push side effects are best-effort. Failed recipient dispatch is logged as `internalv2.app.channelwrite.post_commit_failed`, observed by effect metrics, dropped without retry, and does not checkpoint or replay through the post-commit cursor path.
- Verification scenario: `WK_DELIVERY_CHANNEL_WRITE_EFFECT_WORKERS=16 WK_DELIVERY_CHANNEL_WRITE_RECIPIENT_DISPATCH_CONCURRENCY=4 ./scripts/bench-wukongimv2-three-nodes-real-qps.sh --qps 10000`.
- Verification evidence: `docs/development/perf-runs/20260609-235649-three-node-real-qps/`.
- Verification result: 7946.8 actual QPS, 0.795 actual/offered ratio, p99 769.6ms, no send errors, `cw_wkr=1.000`, `cw_eq=0.023`, max post-commit backlog 496, and post-commit effect error delta 1548.
- Log evidence: after-run node logs contain 162, 281, and 2705 `internalv2.app.channelwrite.post_commit_failed` entries for node1, node2, and node3 respectively. The remaining p99 bottleneck is still ChannelV2 store-apply saturation and slot scheduler dirty/requeued admission, not channelwrite admission.

## 2026-06-10 three-node real-qps 10000 precise post-commit errors

- Scenario: `WK_DELIVERY_CHANNEL_WRITE_EFFECT_WORKERS=16 ./scripts/bench-wukongimv2-three-nodes-real-qps.sh --qps 10000`, 1000 group channels, 4096 users, 10 members, 30s measured duration.
- Evidence: `docs/development/perf-runs/20260610-002516-three-node-real-qps/`.
- Result: 7241.9 actual QPS, 0.724 actual/offered ratio, p99 1264.5ms, p95 661.4ms, max 2697.4ms, and no send errors.
- Foreground channelwrite remained clean: router errors, router backpressure, channel-busy rejections, router route-not-ready, router timeouts, and local admission rejections were all zero. No `internalv2.infra.cluster.channel_append_batch_failed` log was emitted.
- Post-commit effect errors were 3384 total: `other` deltas were node1=320, node2=2072, node3=778; `route_not_ready` deltas were node2=121 and node3=93.
- Precise log fields show all `route_not_ready` post-commit failures came from `phase=recipient_route_resolve` with underlying `clusterv2: no slot leader`, concentrated in the first three seconds of the run.
- The dominant `other` post-commit failures came from `phase=recipient_dispatch`, with the underlying error `conversation_projector: internalv2/usecase/conversation: stale route`. Conversation authority metrics recorded large stale-route admit counts on node1 and node3.
- Finding: the observed `route_not_ready` is a transient UID route/no-slot-leader condition in post-commit recipient authority resolution, not foreground append. The larger remaining post-commit error source is conversation authority target staleness during recipient dispatch.

## 2026-06-10 conversation projection no-retry follow-up

- Change: `ConversationAuthorityClient.AdmitPatches` no longer retries route-not-ready, stale-route, or not-leader admission errors. It resolves current UID authority targets once, admits each target group once, and returns the first error directly.
- Change: channelwrite's app-level conversation projector logs `internalv2.app.channelwrite.conversation_projection_failed` at ERROR level before returning the failure to the post-commit processor. The outer `internalv2.app.channelwrite.post_commit_failed` observation still carries phase and dispatch context.
- Rationale: conversation projection is a post-SENDACK best-effort side effect and does not require strong consistency with durable channel append or push delivery.
- Verification scenario: `WK_DELIVERY_CHANNEL_WRITE_EFFECT_WORKERS=16 ./scripts/bench-wukongimv2-three-nodes-real-qps.sh --qps 10000`.
- Verification evidence: `docs/development/perf-runs/20260610-005323-three-node-real-qps/`.
- Verification result: 7821.4 actual QPS, 0.782 actual/offered ratio, p99 959.9ms, p95 606.6ms, max 2025.3ms, and no send errors.
- Channelwrite foreground stayed clean: router errors, router backpressure, channel-busy, route-not-ready router results, timeouts, local admission rejects, and `internalv2.infra.cluster.channel_append_batch_failed` logs were all zero.
- Post-commit no longer builds backlog: `channelwrite_post_commit_backlog_max=0`, effect queues were empty in the after snapshot, and post-commit effect errors dropped to 257 total. Those errors split into `recipient_route_resolve` route-not-ready/no-slot-leader=179 and `recipient_dispatch`=78. Conversation projection direct logs were 132 total: no-slot-leader=122, stale-route=8, and transport canceled=2, all concentrated in the first second of the measured run.
- Finding: removing conversation projection retry prevents the previous post-commit worker/backlog amplification. The run still fails the 400ms p99 gate, but the remaining bottleneck is ChannelV2/storage/replication/Slot scheduling pressure rather than channelwrite admission or post-commit retry.

## 2026-06-10 post-commit route_not_ready root cause and fix

- Error cause: post-commit UID routing used `RouteKeys([]uid)` against the foreground clusterv2 router. The default Slot leader observation loop publishes local Multi-Raft `Status.LeaderID` every 10ms. When Multi-Raft status temporarily reports `LeaderID=0`, `routing.Table.cloneWithLeaders` deleted the previous `SlotLeaders[slotID]`, so later UID route lookups returned `clusterv2.ErrNoSlotLeader` even though the cluster had already passed `/readyz`.
- Why it surfaced mostly in post-commit: foreground channel append stayed clean and fenced by ChannelV2 authority; the failures were best-effort side effects after SENDACK, either before recipient grouping (`recipient_route_resolve`) or while conversation projection resolved each UID (`conversation_projector`).
- Fix applied: clusterv2 now treats a zero Slot leader observation as unknown and keeps the last known non-zero leader in the foreground router until a new non-zero leader is observed. Route lookup errors now also preserve lower-level key/index/hash-slot context through `mapRouteError`, so future logs can identify the exact failing batch key/hash slot.
- Remaining expected errors: a real authority movement can still produce `stale_route` or `not_leader`; conversation projection remains best-effort and logs/drops without retry.
- Verification scenario: `WK_DELIVERY_CHANNEL_WRITE_EFFECT_WORKERS=16 ./scripts/bench-wukongimv2-three-nodes-real-qps.sh --qps 10000`.
- Verification evidence: `docs/development/perf-runs/20260610-010649-three-node-real-qps/`.
- Verification result: actual QPS 7078.6, p99 874.9ms, no send errors, `router_route_not_ready_delta=0`, `post_commit_backlog_max=0`, and post-commit effect errors dropped to 5. Logs contained no `no slot leader`; all 5 post-commit failures were `conversation_projector: stale route`.
- Remaining bottleneck: the 400ms p99 gate still fails because pressure is outside channelwrite: Slot scheduler inflight reached 1/1 and ChannelV2 store-apply reached 64/64, with slot scheduler dirty/requeued admission pressure.

## 2026-06-10 channelwrite append ants default tuning

- Scenario: `scripts/bench-wukongimv2-three-nodes-1000ch.sh --qps 5000`, 1000 group channels, three-node local cluster, default channelwrite reactors.
- Baseline default used 8 append workers per reactor and failed quickly: 24.9 actual QPS, p99 67.0ms, 1 send error, 890 channelwrite pool-full submissions, and 5.078 MiB/s peak one-way internal transport.
- `WK_DELIVERY_CHANNEL_WRITE_APPEND_WORKERS=64` passed with 4793.6 actual QPS, p99 149.7ms, no send errors, and 18.417 MiB/s peak one-way internal transport.
- `WK_DELIVERY_CHANNEL_WRITE_APPEND_WORKERS=96` produced the best foreground result: 4829.0 actual QPS, p99 80.2ms, no send errors, 110 channelwrite pool-full submissions, and 17.298 MiB/s peak one-way internal transport.
- `WK_DELIVERY_CHANNEL_WRITE_APPEND_WORKERS=128` passed but regressed: 4718.9 actual QPS, p99 97.8ms, 41534 channelwrite pool-full submissions, and 17.223 MiB/s peak one-way internal transport.
- Raising post-commit workers with append fixed was counterproductive for foreground latency: append=96/post_commit=32 cleared channelwrite pool-full submissions but dropped to 4652.1 actual QPS and p99 148.5ms; append=96/post_commit=16 dropped further to 4507.8 actual QPS and p99 198.3ms.
- Fix applied: keep prepare and post-commit defaults at 8 workers per reactor, and raise the append-stage default to 96 workers per reactor. This makes the default append ants capacity roughly 960 on the 10-reactor benchmark host, matching the manually observed capacity increase without overdriving best-effort post-commit work.
- Default verification evidence: `docs/development/perf-runs/20260610-114217-three-node-1000ch/` passed with 4557.3 actual QPS, p99 126.0ms, no send errors, append pool capacity 960, and 17.157 MiB/s peak one-way internal transport. The remaining pool-full submissions were all `stage=post_commit`, confirming foreground append admission was not saturated by the new default.

## 2026-06-10 three-node 1000ch 10000 QPS sender-key limit

- Scenario: `scripts/bench-wukongimv2-three-nodes-1000ch.sh --qps 10000`, default `sender_pick=first_online`, 1000 group channels, 4096 users, 10 members, 15s measured duration.
- Evidence: `docs/development/perf-runs/20260610-123619-three-node-1000ch/`.
- Result: 6550.6 actual QPS, 0.655 actual/offered ratio, p99 124.2ms, no send errors, no channelwrite router errors, no backpressure, no channel-busy rejections, no local admission rejections, and no channelwrite pool full/errors/saturation.
- Direct cause of low actual QPS: wkbench planned 150000 run messages but dispatched only 98259; 51741 were dropped as `pending_window_expired`. The group scheduler keys by sender UID, and `first_online` limits each selected sender to one in-flight sendack wait, so max active senders was only 448.
- One-variable experiment: `scripts/bench-wukongimv2-three-nodes-1000ch.sh --qps 10000 --sender-pick round_robin`.
- Experiment evidence: `docs/development/perf-runs/20260610-123939-three-node-1000ch/`.
- Experiment result: 9828.8 actual QPS, 0.983 actual/offered ratio, p99 321.7ms, no send errors, max active senders rose to 2103, and pending-window drops fell to 2493.
- Channelwrite finding: channelwrite admission was not the limiter. Foreground append effects waited on `Appender.AppendBatch`: append effect avg was about 54-56ms in the first run and 92-93ms in the round-robin run, while post-commit avg stayed near zero. Metrics attribution classified the remaining tail as ChannelV2/storage commit and replication wait.
- Follow-up: `bench-wukongimv2-three-nodes-1000ch.sh` now defaults to `sender_pick=round_robin`; use `--sender-pick first_online` only when intentionally reproducing the sender-key-limit baseline.
