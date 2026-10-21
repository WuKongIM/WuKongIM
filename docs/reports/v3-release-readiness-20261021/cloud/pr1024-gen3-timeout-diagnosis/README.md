# PR #1024 generation 3 timeout: bounded read-only diagnosis

Frozen source `44de9452d94edae16129b5781d794f8c1c6c7c57`, tree `b69a44af04f20143b690e7a3860cc45caa95a0f4`. This audit reads the three first-run archives and exact source only. It executes no tests, fixture workloads, builds, CI/network/cloud operations, and changes no repository file or budget. The original failures remain intact. The `diagnosing-bugs` bounded evidence route applies: no new controlled runtime reproduction is claimed.

## Conclusion

The unchanged 600-second outer command envelope has little demonstrated margin for the current suite plus pre-package work. Generation 3 reached the top-level parallel phase about **32.307 seconds later** than generation 2: approximately 14.263 seconds more before the package marker, followed by 18.044 seconds more in the root serial-registration phase. Generation 2's entire command had only 24.046 seconds nominal margin. These are disjoint phase comparisons, not sums of overlapping test durations. They establish budget fragility, not a unique historic cause or a counterfactual guaranteed completion time.

The new pprof registration regression passed in 0.50 seconds; the original interrupt regression passed in 0.05 seconds. They do not explain the tens-of-seconds phase growth. The collector was resumed as root CONT rank 179, after 433.259 seconds in explicit PAUSE, and had no final receipt. It cannot be blamed merely because it was near the tail.

There is a concrete independent optimization candidate: `TestChatLifecycleDirectLabStartBuildsAndQuotesBeforePaidAcquire` overrides only some existing tool seams, so the real script still builds two tools unused by this scenario. A bounded differential fixture experiment can verify that opportunity without changing production behavior, assertions, shared scheduling, or protected limits. Its attributable savings have **not yet been measured**. Repeated whole-suite attempts or a new CI head without a demonstrated change are not justified by this audit.

## Ranked hypotheses recorded before analysis

1. **Envelope composition:** if startup plus current suite leaves insufficient margin, pre-package intervals will repeatedly exceed the 60-second arithmetic difference between outer 600 seconds and package 540 seconds; phase growth before parallel work will consume the previously observed margin. Supported as a structural risk by all three receipts. There is no independent guaranteed 60-second startup budget.
2. **Specific fixture cost:** if a small number of fixtures grew, completed common-test phases should identify where and source should expose fixed or repeated work. Supported for the serial prefix and real build paths, but several apparent largest root duration increases are lock waits, not slower child commands. Attribution of build/run/teardown within successful fixtures is missing.
3. **Scheduling and runner variability:** if ordering and contention alter the tail, CONT order and lock-wait exposure should vary. Scheduling order and lock waits are directly observed; actual CPU contention, cache misses, I/O pressure and a unique historical runner cause remain unmeasured.

## Wall-clock phase table

Values are seconds. The first column is from the API step start (second precision) to Go's package `start`, **not pure compilation**. The next two columns are disjoint package-relative event intervals. `result.elapsed_seconds` includes wrapper cleanup. Package `start` is a Go run-action marker before actual child start, not its precise internal alarm start.

| Generation / head | Step → package marker, approximate | Package marker → first root CONT | Root CONT → last observed safe test event | Wrapper elapsed / outcome |
|---|---:|---:|---:|---|
| 1 / c91e37b4 | 123.173 | 239.552 | 238.246, incomplete | 600.108 / timeout |
| 2 / 2cc562fa | 112.312 | 226.649 | 236.819, all tests terminal | 575.954 / assertion failure |
| 3 / 44de9452 | 126.575 | 244.693 | 227.374, incomplete | 600.100 / timeout |

Generation 2 package terminal elapsed is 463.478 seconds. Generations 1 and 3 have no package terminal event. Package-marker-to-result-emission spans are 477.930 and 473.852 seconds; neither is a completed suite duration. API origins and cleanup mean these table columns must not be used as exact deadline arithmetic.

Within the serial prefix, the real three-node shakeout is one contained interval: 127.388 / 121.433 / 129.657 seconds. Removing that interval from the prefix leaves 112.163 / 105.216 / 115.036 seconds. Thus generation 2 → 3 growth is 8.224 seconds inside shakeout and 9.820 seconds elsewhere in the serial prefix. The script deliberately runs a 90-second operator-stop workload after readiness and builds service plus CLI beforehand (`run-wukongim-three-node-chat-lifecycle-shakeout.sh:352-353,1680,1720`; test `wukongim_three_node_chat_lifecycle_script_integration_test.go:397-443`). No individual build/ready/drain phase receipt survived this successful test, so the extra 8.224 seconds cannot be assigned to compilation or workload CPU.

No whole-suite completion time can be extrapolated from generation 3: 29 root tests were still explicitly paused; four had CONT without terminal, including schema parents waiting on descendants. These are different states, not 33 simultaneously running workloads.

## Completed roots: observed duration versus lock wait

The scripts integration gate invokes `t.Parallel` **before** a two-slot admission channel and shared `sync.RWMutex`. Heavy tests take an RLock; timing-sensitive tests take the exclusive Lock (`script_test_helpers_integration_test.go:18-40`). A CONT therefore does not prove the test acquired the lock or started its fixture.

| Root test (short name) | Gen1 / Gen2 / Gen3 reported duration | What the evidence supports |
|---|---|---|
| SetupBoundsAndResumesGitHubCLIDownload | 3.73 / 0.15 / 21.97 | Gen3 CONT at package +374.520 while the worktree-build reader spans +353.694 to +396.330; setup passes +396.489. The 21.810-second overlap and lock source support admission waiting, not a 22-second download. The fixture also passed its under-8-second command assertion. No exact acquisition timestamp was logged. |
| AnalyzeSignalsCloseWindowAndKillDiagnosisGroup | 16.43 / 4.90 / 24.50 | Gen3 first child RUN is 19.334 seconds after root CONT. Three real signal child cases remain approximately 1.72 / 1.71 / 1.74 seconds versus Gen2 1.58 / 1.60 / 1.59. This isolates a long pre-child interval consistent with the exclusive lock; it is not 19.6 seconds of slower signal cleanup. |
| AnalyzeBoundsCodexEnvironmentProbes | 3.51 / 3.45 / 9.17 | Gen3 first child RUN follows CONT by 5.584 seconds; actual version/login cases remain approximately 1.78 / 1.81 seconds. |
| WorktreeRevision build | 45.91 / 36.96 / 42.64 | A real nested Git fixture plus fresh link and metadata validation. The required fresh build ID is part of its assertion; replacing it with an arbitrary shared binary would remove coverage. Phase cost is not separately logged. |
| NativePackageBootstrapFailureDiagnostics | 20.42 / 20.35 / 20.43 | Stable cost with real bounded timeout/large-output cases, not a new regression. Its elapsed waits test the production contract and should not simply be shortened. |

`all-test-comparison.json` records complete safe event-derived rows for every test, with explicit PAUSE wait separated from observable nonpaused intervals. The latter can still include locks, child joins and OS scheduling; it is not CPU time and must not be summed into a suite wall time. Root parent intervals overlap descendant intervals.

Collector scheduling: root CONT ranks 150 / 9 / 179; explicit PAUSE waits 373.851 / 208.453 / 433.259 seconds. Generation 2 alone completes in 14.79 seconds on the optimized fixture. Generation 3 has 2.636 seconds between CONT and the last safe test event; 4.421 seconds to result emission is a wider observation span that includes possible timeout/cleanup. Neither proves a 79-case completion or measures the collector's full runtime. Generation 1 uses the older Python-backed SSH fixture, so it is not a like-for-like collector runtime baseline.

## Executable next choices

### Recommended: verify unused direct-lab fixture builds

Exact source: `scripts/chat_lifecycle_direct_lab_test.go:289-410` passes `WK_CHAT_LAB_CLOUD_TOOL`, `WK_CHAT_LAB_CHAT_TOOL`, `WK_CHAT_LAB_BUNDLE_BUILDER` and release-state fakes. It omits the existing `WK_CHAT_LAB_GATE_TOOL` and `WK_CHAT_LAB_BUNDLE_TOOL` seams. `scripts/chat-lifecycle/direct-lab.sh:315-329` consequently builds both `wkcloudgate` and `wkcloudbundle`. The `start` fixture checks a complete build/materialize/quote/selector/acquire trace and produced state/private artifacts, but does not call those two extra tools. Three CI measurements of the whole test are 13.91 / 12.43 / 14.14 seconds, entirely in the serial prefix.

A separate bounded scratch A/B should preserve the exact scenario, operation trace, artifacts, modes and assertions; record actual Go build argv and phase times; inject fail-if-called stubs for the two **already existing** seams only in B. It must show the unused builds disappear and stubs remain unused, not merely a faster exit. Preserve the first result and all cleanup. No source optimization is proven by this report alone. A successful local comparison would justify a narrow test-only change and later first full unchanged Linux gate, not declare that gate repaired.

Three WKDB smoke tests also each build the same CLI (`scripts/wkdb_import_smoke_script_integration_test.go:17,54,85`), but no sharing change is proposed now: savings, immutable-source identity and fixture lifetime would need their own evidence. Do not touch the intentional fresh worktree-link test or replace real process assertions with source matching.

### If measured safe optimization cannot provide useful margin

Escalate a **budget contract design for maintainer review**, not an automatic override. The sole authority remains `.github/review-agent/policy.json`; current frozen base policy still governs this candidate. A proposal would need a justified startup/test/cleanup accounting model and retained cold-run phase distributions, while retaining the actual test workload, assertions and product performance thresholds. The current three observations establish fragility but cannot justify a specific new numerical ceiling or its reliability percentile. Splitting compilation/setup accounting or changing outer headroom would require protected control review and independently bound artifacts. Do not prewarm, filter, retry-to-green, modify a candidate-only timeout, or relabel an incomplete result as pass.

## Corrected count appendix (old receipts preserved)

The old generation 1/2 `scripts-first-failure-analysis.json` summaries derived counts from sanitized stdout and missed eight root PASS lines whose **test names** contain token/secret/authorization terms. This audit re-extracted only `Time/Action/Package/Test/Elapsed` directly from the SHA/CRC-verified original ZIPs, never raw Output. Generation 1 roots are **610 PASS / 6 SKIP / 40 incomplete**; generation 2 **649 PASS / 6 SKIP / 1 FAIL**. Generation 3 was already extracted before redaction and remains **618 PASS / 6 SKIP / 33 incomplete**. These counts include all `scripts/...` packages; root package-only counts are separately labelled in `generations.json`.

`count-correction-appendix.json` lists all eight safe names and old/new counts. This changes no exit status, failed assertion or release verdict. The original archive and summary bytes remain unchanged. The CI audit agent was notified to keep the new appendix separate from immutable old receipts.

## Evidence and limits

- `input-files.json`: original inputs and SHA-256, including original ZIP identities. Raw ZIPs/logs, private data and binaries are not copied to this public report.
- `generation-*-safe-events.json`: only safe structured event fields, including PAUSE/CONT missing from older `test-events.json` projections.
- `phase-timing-table.json`, `generation-*-test-intervals.json`, `all-test-comparison.json`: machine-readable reduction.
- `analyze_timeline.py`: offline-only reproducible parser. Run it against the retained original archive paths; it never invokes Go or a fixture.
- `budget-contract/report.md` and digest inventory: independent contract/source audit of wrapper, policy, workflow documentation and matching local Go source.
- `source-freeze.json`: exact source/rule digests before delivery; clean HEAD/tree retained.

The public artifact is finite and allowlisted. There are no new runtime process IDs, cloud resources, or test failures from this diagnosis. This is not a release-ready verdict or a Linux gate pass. A real cloud run's prior message-loss root cause remains outside this test-timeout diagnosis.
