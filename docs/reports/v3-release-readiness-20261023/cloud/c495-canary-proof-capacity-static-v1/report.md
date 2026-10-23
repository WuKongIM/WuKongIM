# c495 canary/proof-capacity hypothesis audit

The proposed self-evicting whole-roster FIFO loop is refuted by the exact c495 code. This is static analysis and bounded derivation from retained configuration/inventory; no new test, workload, build, network or DB read occurred. It neither proves a runtime regression passed nor identifies the expired message's cause.

## Cache identity and capacity

internal/runtime/presence/recovery.go:8 fixes the proof FIFO at1,024 UID entries **per authority HashSlot**. authoritySlot owns recovered/recoveryOrder/recoveryNext (directory.go:38-62), and each Directory shard contains a map of those independent authoritySlots (:31-36). Thus1024 is not a node-global or physical-Slot-global limit. Positive and negative proofs share each HashSlot's cache. Eviction deletes only recovered proof and requires a fresh owner query; it never fabricates offline. App wires RequireRecovery and exact fenced recovery before serving lookups (internal/app/wiring.go:395-405). These constants have no configuration override.

The exact100k roster reconstructed without publishing UIDs spans all256 HashSlots, with333–465 members per HashSlot. All are below1024 for this roster alone. The retained pre-run convergence snapshot at22:25:21.285071Z distributes authority HashSlots85/86/85 over nodes1/2/3 and canary members32,214/34,251/33,535. These are fixed-roster counts under that retained mapping, not online-route counts or a proof the mapping stayed unchanged during the later failure. All12 physical Slots contain voters1/2/3; a data replica is not the authoritative owner for every HashSlot.

Other groups' UID queries can increase a HashSlot's working set beyond1024 and evict canary or ordinary proofs. This audit does not derive a bound for the combined workload or rule out concurrent eviction between installation and a waiter reread. That narrower contention scenario remains possible and unmeasured; it is different from requiring all100k canary proofs resident at once.

## Pages are consumed before subsequent recovery

AuthorityRecovery.EndpointsByTargets (internal/usecase/presence/recovery.go:59-102) packs at most512 UIDs/256 targets per recoverPage call. recoverPage rereads Directory for **that page** after successful owned/joined work (recovery_flights.go:64-87). Its routes/errors return immediately to the outer flush; the outer loop appends returned routes into its caller-owned result before starting the next page (recovery.go:67-82). It never recovers every page and then rereads the original100k UID list from Directory. A later page's proof eviction cannot erase already copied caller results. Failed groups remain explicit; no route cache was added to flights.

A direct high-level Endpoints call with1025 UIDs in one exact target therefore pages512/512/1 and accumulates results despite FIFO capacity1024. The existing TestAuthorityRecoveryPagesLargeTargetWithoutDiscardingEarlierResults already codifies exactly this shape (recovery_test.go:143-171). This task inspected but did not rerun it. The existing late-waiter test deliberately evicts a just-installed proof before the waiter rereads and requires ErrRouteNotReady, with no silent retry or stale offline result (uid_flight_integration_test.go:497-548).

The UID-list client path also consumes results per256-UID page and appends them to its returned map (internal/infra/cluster/presence.go:299-355); it never requires whole-list proof residency. That map has input-proportional caller working memory, including explicit nil entries for confirmed offline UIDs. The normal durable delivery path uses aligned target batches, not that large UID-list map.

## Actual delivery and scheduling bounds

Retained node TOML confirms256 HashSlots,12 initial physical Slots, fanout_page_size512, push_batch_size512 and event_queue_size1024. App passes the same512 limit to both Channel append dispatch and delivery Runtime maxPlanRecipients (internal/app/wiring.go:750-751; delivery_wiring.go:35-38). Large subscribers are fetched in bounded pages and dispatched before the next subscriber page (internal/runtime/channelappend/recipient.go:146-182). Each admitted delivery plan contains at most512 total recipients (:295-354); Runtime rejects a larger plan (runtime/delivery/runtime.go:450-467).

Therefore100k recipients imply196 configured512-recipient pages/plans for one full scan, with the last160; these can queue and need not all have been admitted or executed before a failing handoff. They are not196 simultaneous owner-recovery cohorts. Queue identity is exact Channel ID/type (plan_queue.go:50), and a Runtime runs at most one plan per Channel (:169). Completion requeues that Channel at the ready tail (:188-198), behind unrelated ready Channels. One active canary plan can resolve across several UID-authority nodes, with at most four leader calls per client stage. This is bounded concurrency, not a proved hard starvation bound for a new small group.

A caution limits this scheduling argument: service13 PresenceAuthority and service14 PresenceOwner both use mixed-service execution contexts rather than CancelRunning (pkg/cluster/net/transport.go:354-357). A canceled caller can finish its local delivery plan while an already-started remote handler remains active. Later canary plans or unrelated callers may overlap those handlers and occupy node-local recovery cohorts. Hence one local plan per Channel does **not** prove one total outstanding canary-derived authority handler per node or rule out four-cohort contention. This task makes no exhaustion or starvation finding.

## Canary online population and timing

The two generation-local canary anchors (session.go:26-29,509-526) keep up to two qualifying owner-worker sessions from normal expiration. They do not mean that only two roster members are online. The fixed canary has index1999 and stride2000; in a model with successful contiguous bootstrap identities0..9999, five of its members are online and two qualify as anchors. The retained safe cuts show10,000 total online/traffic-ready users, but no per-roster inventory. They cannot independently prove an actual canary online count of5, or exclude extra/skipped login identities.

The canary generator initializes its first due time at worker generation start+one minute (traffic.go:243 and394-395). Engine generation reset precedes online bootstrap (engine.go:1006); every grant checks one due canary after primary admission (:1348). Thus an overdue canary may enter the first measured grant; the cadence is not necessarily one minute after measured first grant. Admission may wait for a suitable sender/recipient, and generated Canary counts advance at admitted work, not SENDACK.

Safe retained cuts/diagnostic-status/worker-evidence omit Generated.Canary, per-canary SENDACK timestamps and plan entry timestamps. Empty worker logs add no evidence. This audit does not infer two canary ACKs from total messages minus a nominal500/s schedule. The first actual canary SENDACK/plan time and per-roster online count remain unavailable unless another independently sealed receipt supplies them. No raw UID or Channel identifier is published here.

## Verdict and correct next seam

The global1024/whole-roster-reread impossibility hypothesis is refuted. Cold canary work, concurrent proof churn and residual remote requests remain unconfirmed load/fairness hypotheses. The proper controlled extension is a public Directory+AuthorityRecovery call exceeding one HashSlot's FIFO capacity, with mixed positive/negative proof and a real per-Channel bounded delivery queue. Verify page results are consumed before eviction, evicted proof later returns unknown, unrelated ready work progresses under the existing free cohort budget, and four held cohorts do not admit a fifth. A separate real-transport cancellation test must address residual remote handlers. Keep512/256/4 budgets, exact fences, fresh rereads and no fabricated offline proof. controlled-test-seams.json records these proposals; no implementation or execution occurred.


## Service13 deadlines and bounded retained metrics

Service13 has no uniform250ms client deadline. EndpointsByTargets passes the caller context directly into Node.CallRPC (internal/access/node/presence_rpc.go:704); register/commit/abort/unregister/UID-lookup/touch clients do likewise via callService (:776-788). During durable delivery the caller budget is5s for the entire plan, including presence and later push, not5s per RPC or retry (internal/runtime/delivery/runtime.go:17,184-187,505,522). App delivery wiring does not override PlanTimeout; retry and leader stages reuse the parent context. The250ms child deadline is for service14 owner reads, not service13.

| Service13 operation | Confirmed production originating context | Budget |
| --- | --- | --- |
| Target lookup op8; legacy UID lookup op5 during delivery | Delivery runPlan | Entire plan5s default; leaf adds no narrower timeout. Other direct callers may provide another budget. |
| Register1, Commit2, Abort3 during gateway activation | activationTimeoutPresence.Activate then presence.App.Activate/finishPendingRoute | Shared activation3s, exactly configured in retained TOML; app/app.go:842-844 and usecase/presence/activate.go:26,49-53. Earlier inherited deadlines win. |
| Unregister4 through EnqueueUnregister | Provided caller context with WithTimeout | At most3s or shorter remaining parent lifetime; infra/cluster/presence.go:270-277. Direct UnregisterRoute preserves caller context (:260-267). |
| TouchRoutes6 from periodic worker | Worker lifecycle cancellation context | No per-flush or RPC timeout in app/presence_touch.go:154,203-214,285-291,411 and infra/cluster/presence.go:786-791. Finite work counts do not bound elapsed time. Lower-level no-deadline fallback not independently traced here. |

The default cluster node constructs TransportServer without Service overrides (pkg/cluster/node_defaults.go:208-211). Service13 therefore receives30s handler timeout and5s queue timeout, with CancelRunning=false as a mixed read/mutation service (pkg/cluster/net/transport.go:322-357). A started remote handler may outlive the caller; that possibility does not prove a retained slow handler was an abandoned lookup.

The fixed parser reads only the six already retained first-measured/terminal Prometheus files, with SHA/size/mtime checks, emitting only allowlisted service13/pool/task/queue numeric aggregates. Terminal client result counters contain59 timeouts and2 cancellations. Missing first-cut result series are absent, not observedzero. Admission/handler completions at first cut are5814/5802,6025/6015,5787/5769; terminal9674/9672,10473/10473,9980/9979. Queue depth iszero at all cuts. Inflight first12/10/18 and terminal2/0/0 are non-atomic cuts, not whole-window bounds.

Sampled terminal handler bins include>2.5s and<=5s on nodes1/2; node3 maximum sampled bin<=0.5s. Exported labels do not identify operation, so samples may be mutations or reads. They do not prove a lookup exceeded its5s plan deadline or caused the expired message. Handler transport result "ok" may contain an encoded application error. Durations are sampled1/32; observed drops at these cuts arezero. No retained cut pairs a sample to a canary, failed message or individual RPC.

The reproduction owner separately confirmed safe retained cuts omit Generated.Canaries, first canary SENDACK/plan time and actual canary online inventory. No extra query was made.

## Binding and limits

source-binding.json hashes exact source/rules and bounded inputs. Prior c495-owner-rpc-static-trace-v1 remains separately frozen (manifest SHA256 aa623973097c55f19b4ce4eed376488113a97610d55fd72519710dd9266f1fd0); its63-file excerpt set is not copied. This scope contains no raw UIDs, Channel IDs, logs, profiles, credentials, binaries or private metric dumps. The source clone remained clean. No test, build, workload, network access, probe, DB query or source mutation occurred. This is not runtime proof or a root-cause finding.
