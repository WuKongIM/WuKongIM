# Sequential SEND repair and bounded review scopes

The forced idle permission collection delay is repaired, but overall comparative
performance acceptance remains **FAILED**: 6 of 12 sequential rows still exceed
the unchanged 5% p99 or whole-node CPU budget. All 12 burst rows pass. No failure
was replaced or retried. This is not a release or merge approval.

## Frozen source and diagnosis

Runtime: `ec21c1a586941ffd74cfb497f0da4522941bc1a4`; its binary explicitly binds
the worktree Git directory and reports `vcs.modified=false`. The predetermined
three-pair driver is `2a4a1f4e57a8cb3d5586f10bb1585717c37ea0ab`, with all Go
harness hashes recorded. Original old product: `424d03eb298b972ec572261d617972eecb5523c5`.
Later placement diagnostics use driver
`713871701b440bab6e8ab5536e5a0a1621c37984` and are kept separate.

An identically traced old/new same-remote-Slot diagnostic measured mean permission
spans of 0.165 / 1.471 ms. A one-variable 1-ms-to-1-ns probe measured 0.200 ms but
failed burst reduction (64 RPC envelopes), and stopped before policy/history
controls; it is a failed probe, not a qualified repair. The actual repair reads
one/two distinct facts synchronously only while the Store is idle. Concurrent
work keeps its bounded 1-ms cohort; every path retains fresh authority reads,
conservative credits, cancellation, the 30-second ceiling and Close join.

The minimized process-level no-wait check failed at 1.621 ms before repair and
passed at 0.375 ms afterward, with 64 burst RPC/barriers reduced to 4 and exact
161-message history. Three-node CPU/allocation profiles use a separate 256-SEND
phase, four-second captures, 4096-byte allocation sampling and exact 417 history.
Their public request bounds do not certify complete traffic overlap. CPU samples
were insufficient to attribute permission cancellation overhead; no cancellation
redesign was inferred from them.

Independent review found and then verified two repairs: preserve completed Slot
facts on internal cancellation by checking the caller context, and release a
test barrier before Close on failure cleanup. The healthy-Slot regression was
written and run red before repair; cohort/idle integration race now passes.
Final Standards and Spec each have zero remaining implementation findings.

## Predetermined old/new qualification

All six fresh runs pass functional/count controls across four placements, with
64 completed unique ACKs per window, ban/unban controls, exact 161-message full
history per case and matched topology/config/harness identities. CPU evidence
preserves three owned process identities, raw ticks, the Mach timebase and
before/after bounds. Original ownership sampling/cut overhead is included.
Both concurrency 1 and 32 now require every p99 and whole-node CPU change <=5%.

| Pair | Placement | Old / new sequential p99 ms | p99 change | CPU change |
| --- | --- | --- | --- | --- |
| 1 | same-slot-remote | 20.413 / 14.965 | -26.69% | -10.94% |
| 1 | two-slots-one-remote-leader | 20.022 / 32.188 | +60.76% | +15.73% |
| 1 | two-remote-leaders | 14.088 / 32.038 | +127.41% | +32.52% |
| 1 | two-slots-local-leader | 23.573 / 19.161 | -18.72% | -13.94% |
| 2 | same-slot-remote | 17.981 / 17.823 | -0.88% | -8.00% |
| 2 | two-slots-one-remote-leader | 28.052 / 32.028 | +14.17% | +8.06% |
| 2 | two-remote-leaders | 12.237 / 32.315 | +164.08% | +49.94% |
| 2 | two-slots-local-leader | 35.818 / 16.065 | -55.15% | -19.53% |
| 3 | same-slot-remote | 17.932 / 16.945 | -5.50% | -8.10% |
| 3 | two-slots-one-remote-leader | 15.880 / 32.101 | +102.15% | +4.67% |
| 3 | two-remote-leaders | 22.329 / 21.384 | -4.23% | +8.35% |
| 3 | two-slots-local-leader | 31.671 / 20.038 | -36.73% | -0.68% |

Burst p99 changes range -92.51% to -81.25%; whole-node CPU changes range -92.30% to
-84.93%. The remaining sequential failures are retained, including approximately
32-ms maxima in several remote two-Slot windows. Subsequent traced two-Slot
permission means were 0.200 / 0.193 ms and admitted waits were a few microseconds;
the original 32-ms spikes did not recur in that diagnostic. An additional traced
window with native CPU cuts also did not reproduce them. An unchanged-main
`623091def1951fea6746bd9ac3b63a8a9ba30208`
control had sequential maxima 24–44 ms. These controls narrow the evidence but do
not explain away any failed qualification or establish the remaining cause.
Shared-Darwin unpaced 64-SEND p99 equals the maximum; one-voter metadata placement
is not HA/capacity evidence. The fixed Linux 500 SEND/s/400-ms gates remain unchanged.

## Delivery scopes and validation boundaries

The protected Review Agent accepts only `main` bases and charges complete changed
file contents, rather than diff hunk sizes. Runtime #981 is 27 files,
1,012,614 bytes / 20,252 lines. Driver/report metadata #980 is a separate bounded
scope. Raw historical evidence lives in #984, #985, #986, #987 and #988; current
sequential evidence lives in #989 and #990. The [layout and reconstruction
guide](2026-10-01-permission-evidence-layout.md),
[manifest](2026-10-01-permission-sequential-manifest.json) and
[archive receipt](2026-10-01-permission-sequential-archive.json) bind the unchanged
archive bytes. Policy/workflows were not changed.

#980 must land before #981 and the runtime branch must then integrate that main
head. Complete send-ban validation uses the independently frozen driver against
the exact runtime binary; the runtime-only branch currently retains main's older
simultaneous overload fixture and is not claimed to pass that older full harness.
No merge, formal Review Agent command, release, paid resource or failed-CI retry
was authorized or performed. All PRs remain Draft.

Full composite 281 go-unit/go-vet and format/FLOW checks passed; final runtime
cohort/idle race and format/FLOW checks passed. Full send-ban E2E passed in
339.485 seconds, producing ten passed JSON reports
against the frozen runtime binary and driver. Exact-runtime first-attempt Linux
[run 36759581740](https://github.com/WuKongIM/WuKongIM/actions/runs/36759581740)
passed all nine 60-second 500-SEND/s windows: each completed 30,000
requests with zero errors/drops; maximum scheduled-to-completion p99 was
178.226873 ms (unchanged 400-ms budget). Merge preview
`e9909fa320e1c8a6f8be1bd8efd8ceb232078ebf` has the exact runtime source tree.
Manager Chromium and Ubuntu/Debian/Rocky/Alma native preview checks also passed.
Skipped nightly/diagnosis jobs are not pass or capacity evidence. These absolute
Linux gates do not replace the failed sequential comparative gate.
