# 56c three-node local profile analysis

All six approved captured profiles were decoded once. All 12 fixed cached-pprof top/traces commands completed on their first attempt with exit 0 and empty stderr, within 30 seconds / 1 MiB stdout / 16 KiB stderr per command. No timeout, truncation, retry, limit extension, network, DB, heap, new profile request, build, product/workload execution or cloud action occurred. The 51 original files read as inputs/references/tool/source/metadata are unchanged in size, SHA and mtime; the exact 56c real-Git source remains clean. Raw profiles, traces and full decoded symbol stacks stay outside this public allowlist in the 0700 private scope.

## Goroutine samples

| Node | UTC sample | All goroutines | Delivery idle / total | Recovery quota wait | Executing owner RPC | UID-flight join |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| 1 | 23:39:27.728675 | 2522 | 303 / 320 | 15 | 4 | 0 |
| 2 | 23:39:27.732950 | 2591 | 298 / 320 | 22 | 4 | 0 |
| 3 | 23:39:27.746700 | 2498 | 285 / 320 | 12 | 4 | 0 |

Each recovery row is a disjoint partition of that node's `recoverPage` cohort: totals 19, 26 and 16. All twelve observed owner-read holders across these separate cuts were sampled through `Conn.Call`; caller contexts are inbound presence RPC 4, inbound 3 + other 1, and inbound 4 respectively. Singleton/batch operation counts are 3/1, 1/3 and 0/4. Their source-line counts match remote singleton (`presence_recovery.go:182`) and remote batch (`:196`) operations. Four observed cohorts on each node is consistent with the bounded-admission contract, but this stack cut does not distinguish cohort-count, key-count or UID-byte admission bounds.

The other delivery-worker categories are remote authority RPC 6/3/23, bounded parallel-leader join 8/5/7, owner push RPC 2/7/2 and recovery quota wait 1/6/3. Node2 retains one unclassified delivery worker, without forcing it into an existing class. No UID-flight joins were sampled. Named recovery, delivery, holder and all-RPC cohorts overlap; do not add those totals. Full tables remain in the per-node aggregates and `safe-summary.json`.

No Mutex/RWMutex frame, owner-registry read function or server-side ReadOwner function was sampled in any of the three full GOR profiles. Only the generic goroutine-lifecycle Registry wrappers appear in the selected Registry symbols. This does not establish absence of lock contention or server reads at other times. Recovery admission `select` at `recovery_flights.go:156` is after `r.mu.Unlock()` at :155, so those quota waits are not evidence that the admission mutex is held. The source mapping proves the selected branch, not a holder's identity, wait duration, remote-serving cause or deadlock.

## CPU samples

| Node | Header start UTC | Header end UTC | Profile duration | On-CPU samples | Sample count |
| --- | --- | --- | ---: | ---: | ---: |
| 1 | 23:39:22.684236 | 23:39:32.871096167 | 10.186860167 s | 2.08 s | 208 |
| 2 | 23:39:22.684240 | 23:39:32.817024667 | 10.132784667 s | 2.10 s | 210 |
| 3 | 23:39:22.684229 | 23:39:32.850369625 | 10.166140625 s | 2.16 s | 216 |

The leading flat symbols are syscall.syscallX (1.06 / 0.78 / 0.91 sampled seconds), syscall.syscall (0.35 / 0.40 / 0.35), runtime.madvise (0.10 / 0.33 / 0.22), and syscall.syscall6 (0.15 / 0.11 / 0.13). These are sampled on-CPU weights; they do not measure disk or off-CPU latency or prove syscall/root-cause dominance. No cross-run or Linux performance comparison is made.

The approved first-grant receipt is UTC 23:39:22.590978 and the terminal supervisor observation is 23:39:45.812617. Profile header windows and nominal sampler phases cannot prove continuous active SEND during these captures; active SEND end remains unknown. The three snapshots are separate cuts, not a precisely synchronized whole-cluster observation.

## Adapter validation and execution limits

The failure matrix and all 18 synthetic tests were written before adapter implementation. First RED is the absent adapter import, not a claim that 18 data cases ran red. A subsequent first implementation test exposed overly broad rejection of macOS /tmp and /var canonical aliases; that failed log is retained. A minimal path guard correction accepts only those exact OS aliases, and the unchanged test SHA `b362427295095a914b617ce1d2deaead41bf27bd176fed3147885ca8532ba688` then passes all 18 methods. Fixtures use no captured data. Decoder functions/semantics and classification priorities are preserved; only the input path is parameterized and three historical observed-count assertions are removed. The original semantic Conn.Call holder assertion remains and passes for the new data. Fresh unknown categories remain explicit.

One execution deviation is retained: the first three Python GOR workers inherited the executor environment instead of the proposed allowlist. Application worker/adapter AST and source review show no environment reading, child commands or networking, and stdout is only a fixed closed receipt; no environment values or raw profile strings were printed. This does not make those three runs environment-allowlist compliant. CPU workers and all pprof processes used the explicit allowlist. No profile was rerun to repair history. The first GOR worker source hash was reconstructed from the recorded write and single later mutex-projection patch, and is not claimed a contemporaneous pre-exec hash. Both source versions and exact argv/exit/output bounds are retained. Decoder stdout uses a fixed small schema plus a post-completion byte guard; it is not a streaming stdout-cap implementation. The decoder still uses the inherited 4 MiB decompression cap and 8 MiB private-output cap.

Raw mutex product-frame contexts initially generated in unsealed aggregates were moved to private files before sealing; public output contains only nearest-symbol/line/count projections, not reconstructed stacks. No original artifact changed and no profile was decoded again.

This is a bounded local diagnostic observation. The source run still had message_loss=1, 10,502 SEND=ACK, retries=149 and exit 8 without a final report; the root's new finalization evidence is mask4 / kinds [empty, empty, parse]. These profiles do not identify the failing metric line, associate any stack with the failed message, resolve message loss, establish the prior cloud root cause, demonstrate a successful regression or qualify a v3 release.
