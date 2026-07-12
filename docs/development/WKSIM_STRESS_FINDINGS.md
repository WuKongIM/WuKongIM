# WKSIM Stress Findings

## 2026-07-12 three-node 4500 QPS backing-device separation

- Scenario: 4500 offered QPS, 1000 group channels, 4096 users, 10 members, 30s measured phase, synchronous three-replica quorum commits, one message commit coordinator, and unchanged 400ms p99 gate.
- Host baseline: all three local processes stored data on the same APFS physical store with less than 1% free space. The run delivered 4480.2 actual QPS with zero errors but p99 was 563.1ms. Physical commit p99 was 94-99ms and logical commit request p99 was about 239ms even though each node wrote only about 17.8MB, so the limit was fsync latency rather than sequential bandwidth.
- Same-window split probe: node1 used a 4GiB APFS RAM volume while node2 and node3 stayed on the host filesystem; synchronous commits remained enabled. Node1 physical commit p99 fell to 8.9ms and logical request p99 to 58.8ms, while the two host nodes stayed at 101-128ms physical and 847-907ms logical. This proves the backing device materially controls the observed commit tail.
- All-RAM attribution: the exact-topology three-node run delivered 4496.2 actual QPS, a 0.999 actual/offered ratio, p99 223.2ms, and zero errors. Physical commit p99 was 8.4-10.0ms and logical request p99 was 44.9-59.1ms. This is software-path attribution only, not production durability or capacity evidence.
- Rejected tuning: commit shards=2 raised physical commit p99 to 192-218ms; a unified depth-two concurrent synchronous-commit prototype raised end-to-end p99 to 1695.2ms and also had physical-ordering risk; delivery workers=128 still failed at 506.9ms; PullHint subgroup priority reduced Hint task latency but raised the final p99 to 647.3ms. All code experiments were reverted.
- Next software bottleneck: with fast storage, `post_store_commit_wait` remained about 213-217ms and follower Pull RPC about 97-101ms. The next architectural candidate is a leader-durable `ReplicateBatch` fast path that waits for follower durable apply and falls back to PullHint/Pull; it requires a separate protocol/state-machine change and must not be implemented as an unsafe same-message fsync overlap patch.
- Harness hardening: the local starter accepts `--data-root` / `WK_WUKONGIM_THREE_NODES_DATA_ROOT`, Compose accepts per-node `WK_NODE{1,2,3}_DATA_MOUNT`, and benchmark evidence includes `storage-preflight.tsv` with a low-free-space warning.
- Evidence: host baseline `/private/tmp/wukongim-qps-4500-store-hol-baseline-30s-20260712`, split probe `/private/tmp/wukongim-qps-4500-split-ram-20260712`, and exact all-RAM attribution `/private/tmp/wukongim-qps-4500-all-ram-exact-20260712`. Reproduce the host baseline with `GOWORK=off scripts/bench-wukongim-three-nodes-real-qps.sh --qps 4500 --duration 30s --out-dir <evidence-dir>`; for backing-device attribution, mount the intended volume and rerun with `WK_WUKONGIM_THREE_NODES_DATA_ROOT=<mounted-path>` while keeping synchronous commits enabled.

## 2026-07-12 three-node 4500 QPS Pull observation and completion-gap spin

- Scenario: `scripts/bench-wukongim-three-nodes-real-qps.sh --qps 4500`, 1000 group channels, 4096 online users, 10 members, 30s run phase, 32 Channel reactors, 500 Channel RPC/store workers, synchronous three-replica quorum commit, and a 400ms p99 gate.
- Observation added: sampled leader PullBatch metrics now separate batch items/records/payload bytes, submit time, all-future await time, slowest sequential await, total time, RPC worker queue wait, leader mailbox wait, AckOffset apply, handler time, and completed waiters. The benchmark parser and `wkbench metrics classify` report those fields directly.
- Pull finding: at 4500 QPS, leader AckOffset apply and handler work stayed below 0.5ms p99. PullBatch all-future await was about 47-69ms p99, while RPC worker queue wait was about 101-144ms, leader mailbox wait was about 31-45ms, follower Pull RPC was about 215-225ms, and store append/apply worker tasks were about 227-241ms. The remaining foreground tail is scheduling plus storage/quorum wait, not Pull handler CPU.
- CPU root cause: pprof showed `channelWriter.advance` at 14-30% cumulative CPU. An out-of-order append completion waiting for an earlier sequence was incorrectly classified as runnable, so `deactivateLocked` immediately reactivated the writer and spun until the missing callback arrived. The later completion is pending state, but only the missing completion callback can close the ordered-drain gap.
- Fix and proof: `hasRunnableWork` no longer treats a completion gap as runnable; deterministic and race tests cover the dormant gap and ordered drain. In the same 10s 4500-QPS profile shape, `channelWriter.advance` fell to 1.3-1.9% cumulative CPU and the deactivate/coalesce hot chain disappeared. RPC Pull task p99 dropped from roughly 151-182ms to 97-100ms, and post-store commit wait fell from roughly 393-422ms to 324-351ms.
- Reproduction: capture each A/B profile with `GOWORK=off scripts/bench-wukongim-three-nodes-real-qps.sh --qps 4500 --duration 30s --profile-seconds 10 --out-dir <evidence-dir>`, then inspect `<evidence-dir>/004500-qps/pprof/run/004500/*-cpu.pb.gz` with `go tool pprof -top -cum`. The measured local evidence directories are `/private/tmp/wukongim-qps-4500-baseline-pprof-10s-32r-500rpc-20260712` and `/private/tmp/wukongim-qps-4500-no-gap-spin-pprof-10s-32r-500rpc-20260712`; their `sample-validity.tsv` and worker-status snapshots prove that the 10s profiles were captured during the measured run phase.
- Gate result: one 30s run delivered 4480.6 actual QPS with p99 387.0ms and zero errors; the immediate repeat delivered 4494.6 actual QPS with p99 445.5ms and zero errors. This is a large improvement over the prior 786.0ms 4500-QPS repeat, but it is not two consecutive passes, so the stable gate remains 4000 QPS.
- Gate evidence: the two no-profile runs are `/private/tmp/wukongim-qps-4500-no-gap-spin-32r-500rpc-30s-20260712` and `/private/tmp/wukongim-qps-4500-no-gap-spin-32r-500rpc-repeat-30s-20260712`; reproduce either with the same command while omitting `--profile-seconds 10`.
- Rejected experiments: 64 reactors doubled mailbox memory and raised RPC queue wait; 16 reactors, 192 RPC workers, and Pull cap 8 each produced a good first sample but failed the repeat. Multi-target delivery packing reduced queue-command/RPC amplification but made foreground p99 worse in every 30s run, and concurrent owner pushes increased transport bursts and drove Channel append p99 to 746-801ms; those code experiments were reverted.
- Next bottleneck: keep Pull cap 4, 32 reactors, and serial owner push. The next foreground optimization must reduce Channel worker queue and store append/apply head-of-line time without increasing transport concurrency; changing handler logic, reactor count, or delivery fanout is not supported by the current evidence.

## 2026-07-12 three-node 4000 QPS AckOffset Pull batching

- Scenario: `scripts/bench-wukongim-three-nodes-real-qps.sh`, 1000 group channels, 4096 online users, 10 members per channel, synchronous three-replica quorum commit, one message commit coordinator shard, and a 400ms run-phase p99 gate.
- Baseline: the previous 3500-QPS ceiling probe delivered 3483.9 actual QPS with zero send errors but failed at p99 415.5ms. Follower `apply_to_ack_return` p99 tracked the next Pull RPC that carries `AckOffset`; two-item Pull batches were consistently full, and the estimated three-node Pull transport call count was 140244 during the 15s run.
- Change: Pull-led collection windows now collect at most four adjacent items, while PullHint-led windows remain capped at two and both retain the 250us collection window. The first task selects the policy; later mixed kinds or targets can still enter the window and run as serial subgroups. This reduces AckOffset-path call amplification without restoring every RPC-led window to the old 64-item head-of-line risk.
- 3500-QPS A/B: the first 15s run delivered 3490.5 actual QPS with p99 256.4ms and zero errors. Estimated Pull transport calls fell to 100182, about 28.6% below baseline; per-node Pull RPC p99 improved by 8-42%, and follower apply-to-ACK p99 improved by 32-51%. The required 30s repeat delivered 3497.1 actual QPS with p99 303.8ms and zero errors.
- Stable result: 4000 offered QPS passed both 15s and 30s runs. The 30s run delivered 3990.9 actual QPS, a 0.998 actual/offered ratio, p99 284.6ms, and zero errors. This advances the stable three-node gate from 3000 to 4000 QPS without changing synchronous durability or quorum ACK semantics.
- New ceiling: 4500 QPS passed one 15s sample at 4484.4 actual QPS and p99 395.8ms, but the 30s repeat failed at 4483.9 actual QPS and p99 786.0ms with zero send errors. Across nodes, follower Pull RPC p99 rose to 239-241ms, apply-to-ACK rose to 241-249ms, and leader post-store quorum wait rose to 416-458ms; node3 also showed secondary storage and append-batch amplification.
- Remaining bottleneck: the stable ceiling is 4000 QPS. Do not increase the global Pull cap blindly: the current dispatcher can collect multiple targets and executes the resulting subgroups serially, while the server response also waits for the slowest item in the batch. First add leader-side PullBatch observations for submit-to-all-await latency, the slowest future, batch size, and returned records/bytes. Use that evidence to decide whether target-aware collection or bounded subgroup scheduling is worthwhile, then re-run the 4500-QPS 30s gate; physical commit was not the cross-node primary cause of this failure.

## 2026-07-12 three-node 3000 QPS commit, mailbox, and observer tuning

- Scenario: `scripts/bench-wukongim-three-nodes-real-qps.sh --qps 3000`, 1000 group channels, 4096 online users, 10 members per channel, synchronous three-replica quorum commit, and a 400ms run-phase p99 gate.
- Baseline: eight commit coordinator shards sustained 2979-2984 actual QPS with no send errors, but p99 was 633-820ms. Stage metrics classified storage commit as the bottleneck; gateway queues and bounded worker pools were not saturated.
- Commit finding: eight coordinators shared one Pebble DB and fragmented synchronous group commit. One shard increased logical requests per physical commit by about 3.9x, reduced physical commit frequency about 72%, and reduced physical commit p99 from 155-222ms to 57-61ms. The three-node Docker and real-QPS configurations now use one shard while preserving explicit overrides.
- Benchmark correctness: report latency limits now use only the measured `run` phase, so cold warmup p99 cannot fail steady-state capacity. Run CPU profiling now waits for the matching worker run ID with `active_phase=run`, validates the same state after capture, and records both worker statuses plus `sampler.tsv` under a directory for each QPS attempt; the previous sampler finished before message traffic began.
- Mailbox finding: each Channel reactor preallocated three 1024-entry channels of 1096-byte `Event` values. The old 128-reactor benchmark shape reserved about 411MiB of mailbox slots per node while observed mailbox depth stayed zero. Reducing the three-node benchmark default to 32 reactors cut peak RSS from roughly 1.4-1.7GiB to 0.73-1.03GiB without reducing actual QPS or introducing mailbox/worker queue pressure.
- CPU finding: valid run profiles showed non-terminal `ObserverDrain.ObserveTransport` spending heavily in two selects per event. Replacing that path with a counted atomic admission fence plus one non-blocking send removed the redundant select while preserving strict drain-on-stop and terminal cleanup semantics. Its microbenchmark improved from 86.6-88.8ns/op to 77.0-78.4ns/op with zero allocations. The final tagged run profile placed `ObserveTransport` at 2.35-3.76% cumulative CPU across the nodes. An earlier stopped-bit-only prototype was faster but was rejected because it could enqueue after `Stop` had drained.
- Verification: the final fenced 30s run sustained 2994.8 actual QPS, 0.998 actual/offered ratio, p99 346.8ms, and zero send errors. A separately profiled 15s run passed both start/end run-phase validation at 2993.4 QPS, p99 332.5ms, and zero errors.
- Ceiling probe: 3500 offered QPS still delivered 3483.9 actual QPS with zero errors, but p99 reached 415.5ms. A single-variable 2ms commit flush window passed one 15s sample at 3492.9 QPS and 226.4ms p99, then failed the required 30s repeat at 3495.3 QPS and 573.1ms p99, so the 1ms default remains. The long run localized the spike to follower apply plus post-store quorum wait on one node rather than gateway, mailbox, or worker-pool saturation.
- Remaining bottleneck: the stable 3000 QPS result remains classified as storage commit/quorum wait. The next 3500-QPS optimization should reduce follower-apply commit queueing and quorum-tail amplification under the existing synchronous durability contract. Transport `writev` consumes about 30-31% cumulative CPU in the final profile, but write frames/syscall and bytes/syscall metrics are required before changing its coalescing policy.

## 2026-06-19 wkcli sim three-node memory growth

- Scenario: `./scripts/smoke-wkcli-sim-wukongim-three-nodes.sh --duration 1h --users 1000 --groups 1000 --members 10 --rate 3/s`, where `--rate` is per group and therefore targets about 3000 group messages/s before backpressure.
- Evidence: live node pprof during the user run showed multi-GB heap dominated by Slot Raft metadata replication and retention paths: `pkg/cluster.encodeSlotRaftBatch -> encoding/json.Marshal -> bytes.growSlice`, `pkg/raftlog.cloneEntry`, `raftpb.Entry.Unmarshal`, and `pkg/slot/multiraft.cloneEntry/Propose`. Node logs also contained a large `conversation_active` stale-route post-commit error storm.
- Root cause: the default Slot Raft transport encoded raw Raft messages through JSON, forcing base64 expansion and large transient buffers, while wukongim could not tune the existing Slot Raft log compaction window. The stale-route post-commit path amplified CPU and log volume but was not the primary heap owner.
- Fix: Slot Raft transport batches now use a versioned binary frame carrying raw `raftpb.Message` bytes; cluster exposes Slot log compaction config to `cmd/wukongim`; local wukongim script configs use `WK_CLUSTER_SLOT_LOG_COMPACTION_TRIGGER_ENTRIES=1000` and `WK_CLUSTER_SLOT_LOG_COMPACTION_CHECK_INTERVAL=5s`; `ConversationAuthorityClient.AdmitActiveBatch` retries route movement in a small bounded window; expected post-commit route failures log at WARN instead of ERROR.
- Verification: the relevant cluster, `cmd/wukongim`, infra, and app tests passed. A short three-node smoke with `--duration 1m --users 100 --groups 100 --members 10 --rate 0.1/s` passed with `messages_sent=528` and `send_errors=0`; evidence: `data/wkcli-sim-three-node-smoke-codex-fix/`.

## 2026-06-12 transport runtime hot-path surgical refactor follow-up

- Scenario: `GOWORK=off ./scripts/bench-wukongim-three-nodes-real-qps.sh --qps 4000`, 1000 group channels, 4096 users, 10 members, 30s measured duration, after the transport runtime scheduler/write/RPC/OwnedBuffer hot-path refactor.
- Evidence: `docs/development/perf-runs/20260612-171931-three-node-real-qps/`.
- Result: 2464.0 actual QPS, 0.616 actual/offered ratio, p99 458.1ms, p95 340.8ms, max 890.0ms, and 1 send error. Server CPU peaked at node1=248.8%, node2=318.1%, node3=261.0%.
- Pool pressure: channel runtime store-apply/store-append remained the main visible saturation point. Node2 reached `channel/store_apply` 64/64 and `channel/store_append` 62/64; node1/node3 also showed high store apply utilization. `transport/service_executor` stayed low at 0.137/0.184/0.141 utilization.
- Live pprof evidence: `docs/development/perf-runs/20260612-171931-three-node-real-qps-live15/004000-qps/pprof/live/`. The live sampling perturbed latency and should be used only for attribution; that run produced 3714.0 actual QPS, p99 2351.6ms, and no send errors.
- Live pprof attribution: `pkg/transport/internal/conn.(*Conn).writeLoop -> writeOutboundBatch -> wire.WriteFramesInto -> net.Buffers.WriteTo -> syscall.writev` remained visible at roughly 18-20% cumulative CPU per sampled node, down from the earlier ~1/3 write-loop share but still material. `Scheduler` observation was not a top hot path.
- Finding: the transport runtime hot-path refactor improved local microbenchmarks and removed scheduler observation scanning, but this three-node 4000 QPS scenario is still bounded primarily by channel runtime store append/apply/RPC pressure. Remaining transport write syscall cost is lower than the baseline but still worth a later protocol/write aggregation pass if channel runtime pressure is relieved.

## 2026-06-12 three-node real-qps 4000 pprof CPU triage

- Scenario: `./scripts/bench-wukongim-three-nodes-real-qps.sh --qps 4000`, 1000 group channels, 4096 users, 10 members, 30s measured duration, current dirty worktree.
- Baseline evidence: `docs/development/perf-runs/20260612-153217-three-node-real-qps/`.
- Baseline result: 1945.6 actual QPS, 0.486 actual/offered ratio, p99 5348.4ms, no send errors. Server CPU peaked at node1=375.2%, node2=362.2%, node3=284.9%.
- Repeat evidence without post-run pprof: `docs/development/perf-runs/20260612-153217-three-node-real-qps-live/`. Result stayed consistent at 1977.4 actual QPS, p99 4010.7ms, and `channel/rpc` nearly saturated on all nodes.
- The script's built-in `--profile-seconds 30` captures CPU after the benchmark run, so its profiles mostly show idle/runtime wait (`kevent`, `pthread_cond_wait`, `usleep`) and are not representative of the high-CPU window.
- Live 15s pprof evidence: `docs/development/perf-runs/20260612-153217-three-node-real-qps-live15/004000-qps/pprof/live/`. The live sampling perturbed throughput, so use it only for hot-path attribution.
- Live pprof root cause: about one third of sampled CPU per node is `pkg/transport/internal/conn.(*Conn).writeLoop -> writeOutbound -> wire.WriteFrame -> net.Buffers.WriteTo -> syscall.writev`. `Conn.writeLoop` receives scheduler batches but writes each outbound frame separately, producing a high syscall/writev rate under group fanout and conversation-active RPC traffic.
- Secondary CPU source: `conversationactive.Manager.MarkActive -> observeCache -> cacheObservation` accounts for about 10-13% cumulative CPU during live sampling. `cacheObservation` scans the whole UID conversation active cache after each admission batch.
- Amplifier: baseline node logs contain 15010/13417/14002 `channelappend post-commit failed` entries on node1/node2/node3, all matching `conversation_active: internal/usecase/conversation: stale route`. These are best-effort post-commit failures, not SENDACK errors, but they add RPC/logging pressure and coincide with high `channelappend_effect_error_delta` and post-commit backlog.
- Finding: the high CPU is primarily transport write syscall overhead driven by many small internal RPC/response frames, with additional conversation-active cache observation scans and post-commit stale-route failure amplification. The throughput failure is classified as mixed channel runtime/storage/replication backpressure rather than gateway queue exhaustion.
- Post-fix smoke evidence: `docs/development/perf-runs/20260612-160102-three-node-real-qps/004000-qps/` after transport batched writes, incremental conversation-active cache observation, and one fresh-route retry for active-batch admission. Result remained 1920.2 actual QPS, ratio 0.480, p99 3816.2ms, no send errors. The stale-route post-commit amplifier dropped sharply (`channelappend_effect_error_delta` 4/2/193; stale-route log counts 4/2/193), but channel runtime RPC/store append/apply pools were still near saturation, so the remaining throughput bottleneck is below the fixed CPU amplifiers.

## 2026-06-09 three-node real-qps 5000 channelwrite triage

- Scenario: `./scripts/bench-wukongim-three-nodes-real-qps.sh --qps 5000`, 1000 group channels, 4096 users, 10 members, 30s measured duration.
- Baseline evidence: `docs/development/perf-runs/20260609-222608-three-node-real-qps/`.
- Baseline result: 2651.9 actual QPS, p99 2334.2ms, no send errors. `channelwrite` had no router errors/backpressure/channel-busy rejections, but default `WK_DELIVERY_CHANNEL_WRITE_EFFECT_WORKERS=0` resolved to 2 per reactor and capped `channel/store_append` inflight at 20 per node.
- One-variable experiment: `WK_DELIVERY_CHANNEL_WRITE_EFFECT_WORKERS=8 ./scripts/bench-wukongim-three-nodes-real-qps.sh --qps 5000`.
- Experiment evidence: `docs/development/perf-runs/20260609-223119-three-node-real-qps/`.
- Experiment result: 4917.6 actual QPS, p99 977.7ms, no send errors. `channel/store_append` and `channel/store_apply` reached 64 inflight workers, shifting the bottleneck to storage/replication and exposing `channelwrite` post-commit backlog on node1/node2.
- Middle experiment: `WK_DELIVERY_CHANNEL_WRITE_EFFECT_WORKERS=4 ./scripts/bench-wukongim-three-nodes-real-qps.sh --qps 5000`.
- Middle experiment evidence: `docs/development/perf-runs/20260609-223824-three-node-real-qps/`.
- Middle experiment result: 4338.4 actual QPS, p99 1550.2ms, no send errors, and no post-commit backlog. This confirms that 4 workers reduces the append-effect bottleneck but still does not fully utilize downstream append capacity for the 5000 offered-QPS workload.
- Fix applied: the default `ChannelWriteEffectWorkers` value was raised from 2 to 8 per reactor while preserving explicit `WK_DELIVERY_CHANNEL_WRITE_EFFECT_WORKERS` overrides.
- Fix verification evidence: `docs/development/perf-runs/20260609-224215-three-node-real-qps/`.
- Fix verification result: 4981.9 actual QPS, p99 638.8ms, no send errors, and no `channelwrite` router errors, backpressure, channel-busy rejections, local admission rejections, effect errors, or post-commit backlog.
- Finding: the baseline had a `channelwrite` append-effect concurrency bottleneck. After the default worker fix, this workload reaches the offered QPS target, but the 400ms p99 gate still fails because the remaining tail is classified under channel runtime/storage/replication stages rather than `channelwrite` admission.

## 2026-06-09 three-node real-qps 10000 channelwrite effect-workers 16

- Scenario: `WK_DELIVERY_CHANNEL_WRITE_EFFECT_WORKERS=16 ./scripts/bench-wukongim-three-nodes-real-qps.sh --qps 10000`, 1000 group channels, 4096 users, 10 members, 30s measured duration.
- Evidence: `docs/development/perf-runs/20260609-224830-three-node-real-qps/`.
- Result: 8068.4 actual QPS, 0.807 actual/offered ratio, p99 769.1ms, p95 584.3ms, max 1339.1ms, and no send errors.
- `channelwrite` admission stayed open: router errors, router backpressure, channel-busy rejections, route-not-ready router results, timeouts, and local admission rejections were all zero.
- `channelwrite` post-commit pressure appeared at this offered rate: max post-commit backlog was 5275 and effect error delta was 91329, mostly post-commit `route_not_ready`/`other` results.
- Downstream pressure was visible outside `channelwrite`: `channel/store_append`, `channel/store_apply`, and `channel/rpc` reached worker saturation; slot scheduler showed dirty/requeued admission pressure.
- Finding: increasing effect workers to 16 does not make 10000 offered QPS pass. It removes `channelwrite` admission as the limiter, but overdrives post-commit/downstream channel runtime, storage/apply, replication, and slot scheduling stages.

## 2026-06-09 channelwrite effect worker instrumentation

- Change: added `channelwrite` effect worker gauges for per-reactor/stage worker inflight, worker capacity, queue depth, and queue capacity. The real-qps parent summary now exposes `cw_wkr` (max effect worker utilization) and `cw_eq` (max effect queue fill).
- Verification scenario: `WK_DELIVERY_CHANNEL_WRITE_EFFECT_WORKERS=16 ./scripts/bench-wukongim-three-nodes-real-qps.sh --qps 10000`.
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
- Follow-up finding: the fix improved actual throughput from 6949.5 to 8646.2 at 10000 offered QPS, but the 400ms p99 gate still fails. Runtime pressure remains in channel runtime store-apply saturation and slot scheduler dirty/requeued admission rather than channelwrite admission.

## 2026-06-09 channelwrite best-effort post-commit

- Change: post-commit conversation/push side effects are best-effort. Failed recipient dispatch is logged as `internal.app.channelwrite.post_commit_failed`, observed by effect metrics, dropped without retry, and does not checkpoint or replay through the post-commit cursor path.
- Verification scenario: `WK_DELIVERY_CHANNEL_WRITE_EFFECT_WORKERS=16 WK_DELIVERY_CHANNEL_WRITE_RECIPIENT_DISPATCH_CONCURRENCY=4 ./scripts/bench-wukongim-three-nodes-real-qps.sh --qps 10000`.
- Verification evidence: `docs/development/perf-runs/20260609-235649-three-node-real-qps/`.
- Verification result: 7946.8 actual QPS, 0.795 actual/offered ratio, p99 769.6ms, no send errors, `cw_wkr=1.000`, `cw_eq=0.023`, max post-commit backlog 496, and post-commit effect error delta 1548.
- Log evidence: after-run node logs contain 162, 281, and 2705 `internal.app.channelwrite.post_commit_failed` entries for node1, node2, and node3 respectively. The remaining p99 bottleneck is still channel runtime store-apply saturation and slot scheduler dirty/requeued admission, not channelwrite admission.

## 2026-06-10 three-node real-qps 10000 precise post-commit errors

- Scenario: `WK_DELIVERY_CHANNEL_WRITE_EFFECT_WORKERS=16 ./scripts/bench-wukongim-three-nodes-real-qps.sh --qps 10000`, 1000 group channels, 4096 users, 10 members, 30s measured duration.
- Evidence: `docs/development/perf-runs/20260610-002516-three-node-real-qps/`.
- Result: 7241.9 actual QPS, 0.724 actual/offered ratio, p99 1264.5ms, p95 661.4ms, max 2697.4ms, and no send errors.
- Foreground channelwrite remained clean: router errors, router backpressure, channel-busy rejections, router route-not-ready, router timeouts, and local admission rejections were all zero. No `internal.infra.cluster.channel_append_batch_failed` log was emitted.
- Post-commit effect errors were 3384 total: `other` deltas were node1=320, node2=2072, node3=778; `route_not_ready` deltas were node2=121 and node3=93.
- Precise log fields show all `route_not_ready` post-commit failures came from `phase=recipient_route_resolve` with underlying `cluster: no slot leader`, concentrated in the first three seconds of the run.
- The dominant `other` post-commit failures came from `phase=recipient_dispatch`, with the underlying error `conversation_projector: internal/usecase/conversation: stale route`. Conversation authority metrics recorded large stale-route admit counts on node1 and node3.
- Finding: the observed `route_not_ready` is a transient UID route/no-slot-leader condition in post-commit recipient authority resolution, not foreground append. The larger remaining post-commit error source is conversation authority target staleness during recipient dispatch.

## 2026-06-10 conversation projection no-retry follow-up

- Change: `ConversationAuthorityClient.AdmitPatches` no longer retries route-not-ready, stale-route, or not-leader admission errors. It resolves current UID authority targets once, admits each target group once, and returns the first error directly.
- Change: channelwrite's app-level conversation projector logs `internal.app.channelwrite.conversation_projection_failed` at ERROR level before returning the failure to the post-commit processor. The outer `internal.app.channelwrite.post_commit_failed` observation still carries phase and dispatch context.
- Rationale: conversation projection is a post-SENDACK best-effort side effect and does not require strong consistency with durable channel append or push delivery.
- Verification scenario: `WK_DELIVERY_CHANNEL_WRITE_EFFECT_WORKERS=16 ./scripts/bench-wukongim-three-nodes-real-qps.sh --qps 10000`.
- Verification evidence: `docs/development/perf-runs/20260610-005323-three-node-real-qps/`.
- Verification result: 7821.4 actual QPS, 0.782 actual/offered ratio, p99 959.9ms, p95 606.6ms, max 2025.3ms, and no send errors.
- Channelwrite foreground stayed clean: router errors, router backpressure, channel-busy, route-not-ready router results, timeouts, local admission rejects, and `internal.infra.cluster.channel_append_batch_failed` logs were all zero.
- Post-commit no longer builds backlog: `channelwrite_post_commit_backlog_max=0`, effect queues were empty in the after snapshot, and post-commit effect errors dropped to 257 total. Those errors split into `recipient_route_resolve` route-not-ready/no-slot-leader=179 and `recipient_dispatch`=78. Conversation projection direct logs were 132 total: no-slot-leader=122, stale-route=8, and transport canceled=2, all concentrated in the first second of the measured run.
- Finding: removing conversation projection retry prevents the previous post-commit worker/backlog amplification. The run still fails the 400ms p99 gate, but the remaining bottleneck is channel runtime/storage/replication/Slot scheduling pressure rather than channelwrite admission or post-commit retry.

## 2026-06-10 post-commit route_not_ready root cause and fix

- Error cause: post-commit UID routing used `RouteKeys([]uid)` against the foreground cluster router. The default Slot leader observation loop publishes local Multi-Raft `Status.LeaderID` every 10ms. When Multi-Raft status temporarily reports `LeaderID=0`, `routing.Table.cloneWithLeaders` deleted the previous `SlotLeaders[slotID]`, so later UID route lookups returned `cluster.ErrNoSlotLeader` even though the cluster had already passed `/readyz`.
- Why it surfaced mostly in post-commit: foreground channel append stayed clean and fenced by channel runtime authority; the failures were best-effort side effects after SENDACK, either before recipient grouping (`recipient_route_resolve`) or while conversation projection resolved each UID (`conversation_projector`).
- Fix applied: cluster now treats a zero Slot leader observation as unknown and keeps the last known non-zero leader in the foreground router until a new non-zero leader is observed. Route lookup errors now also preserve lower-level key/index/hash-slot context through `mapRouteError`, so future logs can identify the exact failing batch key/hash slot.
- Remaining expected errors: a real authority movement can still produce `stale_route` or `not_leader`; conversation projection remains best-effort and logs/drops without retry.
- Verification scenario: `WK_DELIVERY_CHANNEL_WRITE_EFFECT_WORKERS=16 ./scripts/bench-wukongim-three-nodes-real-qps.sh --qps 10000`.
- Verification evidence: `docs/development/perf-runs/20260610-010649-three-node-real-qps/`.
- Verification result: actual QPS 7078.6, p99 874.9ms, no send errors, `router_route_not_ready_delta=0`, `post_commit_backlog_max=0`, and post-commit effect errors dropped to 5. Logs contained no `no slot leader`; all 5 post-commit failures were `conversation_projector: stale route`.
- Remaining bottleneck: the 400ms p99 gate still fails because pressure is outside channelwrite: Slot scheduler inflight reached 1/1 and channel runtime store-apply reached 64/64, with slot scheduler dirty/requeued admission pressure.

## 2026-06-10 channelwrite append ants default tuning

- Scenario: `scripts/bench-wukongim-three-nodes-1000ch.sh --qps 5000`, 1000 group channels, three-node local cluster, default channelwrite reactors.
- Baseline default used 8 append workers per reactor and failed quickly: 24.9 actual QPS, p99 67.0ms, 1 send error, 890 channelwrite pool-full submissions, and 5.078 MiB/s peak one-way internal transport.
- `WK_DELIVERY_CHANNEL_WRITE_APPEND_WORKERS=64` passed with 4793.6 actual QPS, p99 149.7ms, no send errors, and 18.417 MiB/s peak one-way internal transport.
- `WK_DELIVERY_CHANNEL_WRITE_APPEND_WORKERS=96` produced the best foreground result: 4829.0 actual QPS, p99 80.2ms, no send errors, 110 channelwrite pool-full submissions, and 17.298 MiB/s peak one-way internal transport.
- `WK_DELIVERY_CHANNEL_WRITE_APPEND_WORKERS=128` passed but regressed: 4718.9 actual QPS, p99 97.8ms, 41534 channelwrite pool-full submissions, and 17.223 MiB/s peak one-way internal transport.
- Raising post-commit workers with append fixed was counterproductive for foreground latency: append=96/post_commit=32 cleared channelwrite pool-full submissions but dropped to 4652.1 actual QPS and p99 148.5ms; append=96/post_commit=16 dropped further to 4507.8 actual QPS and p99 198.3ms.
- Fix applied: keep prepare and post-commit defaults at 8 workers per reactor, and raise the append-stage default to 96 workers per reactor. This makes the default append ants capacity roughly 960 on the 10-reactor benchmark host, matching the manually observed capacity increase without overdriving best-effort post-commit work.
- Default verification evidence: `docs/development/perf-runs/20260610-114217-three-node-1000ch/` passed with 4557.3 actual QPS, p99 126.0ms, no send errors, append pool capacity 960, and 17.157 MiB/s peak one-way internal transport. The remaining pool-full submissions were all `stage=post_commit`, confirming foreground append admission was not saturated by the new default.

## 2026-06-10 three-node 1000ch 10000 QPS sender-key limit

- Scenario: `scripts/bench-wukongim-three-nodes-1000ch.sh --qps 10000`, default `sender_pick=first_online`, 1000 group channels, 4096 users, 10 members, 15s measured duration.
- Evidence: `docs/development/perf-runs/20260610-123619-three-node-1000ch/`.
- Result: 6550.6 actual QPS, 0.655 actual/offered ratio, p99 124.2ms, no send errors, no channelwrite router errors, no backpressure, no channel-busy rejections, no local admission rejections, and no channelwrite pool full/errors/saturation.
- Direct cause of low actual QPS: wkbench planned 150000 run messages but dispatched only 98259; 51741 were dropped as `pending_window_expired`. The group scheduler keys by sender UID, and `first_online` limits each selected sender to one in-flight sendack wait, so max active senders was only 448.
- One-variable experiment: `scripts/bench-wukongim-three-nodes-1000ch.sh --qps 10000 --sender-pick round_robin`.
- Experiment evidence: `docs/development/perf-runs/20260610-123939-three-node-1000ch/`.
- Experiment result: 9828.8 actual QPS, 0.983 actual/offered ratio, p99 321.7ms, no send errors, max active senders rose to 2103, and pending-window drops fell to 2493.
- Channelwrite finding: channelwrite admission was not the limiter. Foreground append effects waited on `Appender.AppendBatch`: append effect avg was about 54-56ms in the first run and 92-93ms in the round-robin run, while post-commit avg stayed near zero. Metrics attribution classified the remaining tail as channel runtime/storage commit and replication wait.
- Follow-up: `bench-wukongim-three-nodes-1000ch.sh` now defaults to `sender_pick=round_robin`; use `--sender-pick first_online` only when intentionally reproducing the sender-key-limit baseline.

## 2026-06-11 three-node 1000ch 10000 QPS channelwrite backpressure

- Scenario: `scripts/bench-wukongim-three-nodes-1000ch.sh --qps 10000`, default round-robin senders, 1000 group channels, 4096 users, 10 members, 15s measured duration.
- Baseline evidence: `docs/development/perf-runs/20260611-115501-three-node-1000ch/`.
- Baseline result: 1821.1 actual QPS, p99 436.6ms, 1 send error, and `fail_fast` stopped the worker after `ReasonSystemError`.
- Failure mapping: node1 logged `internal.access.gateway.send_failed` for channel 582 with `internal/message: backpressured`; metrics showed exactly one `channelwrite` local router backpressure and no local admission rejection.
- Channelwrite pressure: append pool had no full submissions, but post-commit pool saturated on all nodes (`post_commit full`: node1=107089, node2=132151, node3=126018). Post-commit effects averaged 476-595ms, while append effects averaged 118-126ms.
- Downstream pressure was concurrent: channel runtime store-append/store-apply reached worker saturation, Slot scheduler had dirty/requeued admissions, and storage commit/request p99 stayed high.
- One-variable experiment: `WK_DELIVERY_CHANNEL_WRITE_POST_COMMIT_WORKERS=32 scripts/bench-wukongim-three-nodes-1000ch.sh --qps 10000`.
- Experiment evidence: `docs/development/perf-runs/20260611-120017-three-node-1000ch/`.
- Experiment result: 1088.1 actual QPS, p99 403.5ms, still 1 send error, and post-commit pool full remained high at 92539 total. Raising only post-commit workers reduced but did not remove channelwrite post-commit saturation or foreground backpressure.
- Finding: the current 10000 offered-QPS run fails because the workload overdrives bounded channelwrite/channel runtime capacity. The foreground abort is a single channelwrite local `ErrBackpressured` surfaced as `ReasonSystemError`; the dominant sustained pressure is post-commit effect pool saturation plus downstream channel runtime storage/replication and Slot scheduler pressure.

## 2026-06-12 three-node real-qps 4000 channelappend CPU profile

- Scenario: `./scripts/bench-wukongim-three-nodes-real-qps.sh --qps 4000`, 1000 group channels, 4096 users, 10 members, 30s measured duration. CPU pprof was captured manually during the load window from API ports 5011/5012/5013.
- Evidence: `docs/development/perf-runs/20260612-173851-three-node-real-qps-4000-pprof/`.
- Result: 2626.9 actual QPS, 0.657 actual/offered ratio, p99 3633.9ms, no send errors. Server CPU peaked at node1=350.5%, node2=301.7%, node3=344.4%.
- Pprof finding: the largest application stack is `internal/runtime/channelappend.(*channelWriter).advance`, about 25-40% cumulative per node. Its cost is mostly writer lock/activation churn, post-commit checks, context checks, append/commit scheduling, and runtime copy/zero/scheduler work. Transport runtime write/read and `writev` account for about 12-22%; Prometheus metrics are about 5%; Pebble/storage CPU is only about 4-5%.
- Runtime evidence: each node had about 5.3k goroutines, dominated by gateway async send collectors, channel runtime append future waiters, and ants workers. Channel runtime store-apply/store-append/RPC pools and transport service workers hit saturation on at least one node.
- Post-commit evidence: `channelappend_effect_error_delta=32452`, and all sampled after-run post-commit errors were `phase=conversation_active` with `internal/usecase/conversation: stale route` (node1=7258, node2=15599, node3=9595). This amplifies channelappend post-commit work and logging but is not the foreground SEND failure source.
- Finding: the high CPU is not caused by raw Pebble writes. It is mostly channelappend post-commit state-machine scheduling and transport fanout under excessive concurrency, with channel runtime replication/quorum wait causing the long p99 tail (`append_post_store_commit_wait_p99` about 2.38-2.45s).
