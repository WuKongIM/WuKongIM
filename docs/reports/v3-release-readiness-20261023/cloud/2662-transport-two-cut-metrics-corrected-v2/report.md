# Two-cut transport evidence, corrected aliases

The six retained Prometheus captures show owner-client timeouts, but do not identify whether an individual timeout occurred in request scheduling, server handling, provider work, or the reply path. Existing sampled service-13 queue waits have a clear tail on nodes 2 and 3. The scheduler sample does not show a 250 ms wait; that does not exclude an unsampled tail or identify response scheduling.

Only six fixed files were numerically parsed once in this corrected pass (6,152,290 bytes); all SHA/stat identities remained unchanged. The separate integrity-only hash read did not reparse metrics. V1 remains frozen as an alias-filter incomplete observation, not zero RPC traffic. Source aliases are ID13 `presence authority` and ID14 `presence owner action`, installed by the cluster transport adapter before the app metrics observer. All source references and hashes are in source-binding.json.

| Node | ID14 ok numeric increase | ID14 terminal timeout counter | ID14 duration samples / sum s | ID14 sampled >250 ms |
|---|---:|---:|---:|---:|
|1|3403|2|110 / 3.802622385|2|
|2|3687|8|118 / 3.951715502|2|
|3|3470|6|112 / 3.728995291|2|

Every observed ID14 timeout series first appears at terminal. Thus the interval timeout count and fraction are unknown; the terminal counts are cumulative observations. Process-start identity is absent at both cuts on all nodes, so even monotone matched increments are numerical changes, not independently identity-proven event totals. The retained facts matched-subset fraction of 0 is explicitly ineligible (`exact_observed_counter_ratio=false`); summary.json correctly projects the complete fraction as null. Missing err/canceled series are not zero.

All ID14 duration samples were ≤500 ms. Histograms have no outcome label, so these samples cannot be joined to timeout counters. ID13 matched ok increases were 8894 / 8598 / 9202; terminal timeout counters were 165 / 92 / 118, with the same unknown-interval limitation. ID13 duration samples were 284 / 274 / 292, with sums 127.110949384 / 67.420605034 / 92.348000033 seconds.

| Node | ID13 queue sampled count / sum s | ID13 queue samples >250 ms / >1 s | ID14 queue sampled count / sum s |
|---|---:|---:|---:|
|1|273 / 0.500743040|0 / 0|112 / 0.148816708|
|2|296 / 100.255955296|50 / 32|107 / 0.174570833|
|3|276 / 36.606013284|33 / 22|111 / 0.189664040|

All sampled ID13 queue waits were ≤5 s; ID14 queue waits were ≤100 / 25 / 25 ms. These accepted-work queue waits precede service execution and do not measure the complete client or reply path.

Scheduler published ok-admission increases (raft/control/rpc) were 16601/2785/64675, 16369/2672/64912 and 15627/2686/64281. No non-ok or bulk series was materialized in the selected cuts; those cases remain unknown. Scheduler rpc wait sample counts were 2021/2029/2008 with sums 1.238875615/1.076035997/1.226871429 s; 9/5/11 samples exceeded 25 ms and all were ≤100 ms. Raft/control samples were also all ≤100 ms. Full source-defined cumulative bucket changes for every observed service and all four requested priorities are in summary.json and per-target facts.json.

Counts are exact published observer batches, asynchronously drained. Duration storage samples one in32 per internal event key (including outcome and frame kind); intentional unsampled durations are not drops. The dropped counter was 0→0 on every node. No sample count or sum is multiplied by32, interpreted as a percentile, or treated as exclusive wall time. Scheduler priorities aggregate services, directions and frame kinds; these data cannot isolate requests versus replies, READ versus other service-14 operations, or the expired message. Non-atomic cuts and drain timing also prevent exact alignment to the earlier ten-second execution traces.

GOMAXPROCS was 2→2 on all nodes. Latest process CPU gauge points were 24.509→50.295%, 30.463→16.007%, 30.581→17.243% (one busy core≈100%). RSS was 630767616→784744448, 597655552→834928640 and 581042176→833028096 bytes. These are two gauge points, not peak CPU/RSS or heap/host headroom. No fixed host-available gauge was present in this pass.

No repository source changed, tests or binaries ran, HTTP/DB/profile requests occurred, or raw trace was read. This report neither resolves message_loss nor assigns its cause. The proposed ignored response-Send path remains a hypothesis requiring narrower correlation evidence.
