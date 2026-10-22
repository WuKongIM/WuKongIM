# Local UID-flight candidate delivery

Committed clean branch `codex/v3-uid-flight-candidate` at **5a8c42fa69ddc15ef341e2992148c7c7fc36eb79**, directly based on d349a7bdd2ca8f6bd4683fbccbf83010d3f9cd3e. Worktree: `/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-uid-flight-candidate`. Parent can merge this exact branch tip; no cherry-pick, push, PR, main checkout edit, service/load/cloud action was performed. The original2ab RED worktree and sealed record remain unchanged.

The candidate replaces coarse cold hash-lane ownership with UID+five-field-authority proof coalescing, keeps original512/256 packing and owner gate4, and releases/signals newly owned proof before joining older flights. It retains no route cache or offline marker, clones UID bytes, preserves current Directory fencing/FIFO/tombstones, and keeps a live canceled-leader follower retryable under existing outer retries. There are no new goroutines, retries, public knobs or timeout increases. Admission is bounded by4 cohorts,2048 keys,32MiB per newly owned subset and128MiB total owned UID clones.

Compatibility findings changed the initially proposed1/4MiB budgets before implementation. Stored UID validation allows65535 bytes;512 maximum-size identities fit32MiB. Thus packing is unchanged and no new owner rounds are introduced for any storable UID page. Arbitrary impossible-to-store/CONNECT cold query strings exceeding the new32MiB subset limit can newly return typed not-ready; Unreleased/project knowledge explicitly record this boundary. The byte budget covers registry clones rather than all caller/Directory/RPC heap. The separately sealed `../uid-flight-candidate-final-spec-v1` is the **final review specification**; earlier4MiB design artifacts are historical.

| Check | Result | Wall seconds |
| --- | --- | --- |
| Old-code public-seam baseline |23 PASS /13 FAIL events; meaningful behavioral RED, no compile error |6.374 |
| Same focused tests after production change |36 PASS /0 FAIL |1.094 |
| Complete usecase/runtime presence packages |106 PASS /0 FAIL |1.158 |
| Same complete packages under race |106 PASS /0 FAIL |18.940 |
| Actual owner-adapter membership/boot/deadline/256-target recovery contracts under race |4 PASS /0 FAIL |53.972 |
| Frozen-policy flow-doc-contracts |PASS |0.086 |
| Frozen-policy go-format |PASS |2.282 |

Go tests each use an explicit Go1.25.11 executable, GOTOOLCHAIN=local, GOPROXY/GOSUMDB off, GOWORK off, GOMAXPROCS1 and-p1; integration tests have90-second runtime and120-second outer bounds. Race requiresCGO1. Full argv/environment overrides and complete stdout/stderr are retained. The owner-adapter race link emitted an Apple LC_DYSYMTAB warning but completed exit0 with no race finding. Every owned test process joined and all recorded process groups were independently zero at cleanup.

Local FLOW checks initially rejected six navigation references, then the stale generated index. Both complete failures are retained; navigation was reduced to five, canonical FLOW_INDEX regenerated, and the unchanged named check subsequently passed. Its20 existing advisory line-target warnings remain non-failing. Source/rule/policy hashes and exact before/after patches are included.

These regressions validate a controlled cold-recovery isolation mechanism and its local resource/proof contracts. They do not establish the natural workload or cloud message-loss root cause, performance improvement, Linux acceptance, or production release readiness. Exact-candidate process E2E and independent Standards/Spec review remain with the parent task. No further test repetition is planned absent a new failure/change.
