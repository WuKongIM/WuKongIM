# PR #1024 scripts timeout: bounded fixture profile

Source remains `c91e37b4e35e1009c9c5cb55ff351a38b870014f`. This is a read-only diagnosis and scratch experiment, not a source change, CI rerun, performance gate pass, or cloud operation. Eighteen collector subprocess invocations were used: six current fixtures, six with only static host responses dispatched by shell, one failed precomputed prototype, and five corrected precomputed cases. No full suite was repeated.

The measured avoidable cost is repeated Python startup inside the fake SSH adapter. A normal case starts 26 Python processes: 25 fake SSH calls and the real terminal helper. Precomputing fixture responses and dispatching them with shell reduces this to the one real helper while retaining the real collector process, 25 SSH calls, all collection paths, and assertions. Five representative cases compare exactly against the sealed V2 artifact after the limited normalization below. This establishes a useful fixture optimization, not sufficient headroom for the Linux gate.

## At most three hypotheses and evidence

1. **Repeated fixture process startup adds fixed cost.** Proven locally: normal fake SSH Python count 26 → 11 when only 15 static host responses move to shell, and 26 → 1 when all representative responses are prepared once. Invalid anchors still execute the original 15 host captures before terminal validation, so they start 16 Python processes before optimization. The existing 79-case artifact contains 1,767 SSH calls, including 1,185 fixed host calls (15 × 79). No calls were removed in the prototype. `jq` remains 19 invocations per case and about 0.05 seconds combined locally; these measurements do not justify changing production collection or mocking jq.
2. **The expanded real-process matrix adds work; scheduling and other tests affect total elapsed time.** Proven work expansion: V1 had 42 process cases, V2 79; local collector test elapsed rose 45.22 → 96.03 seconds. This is not a controlled comparison of only fixture count. V1's shakeout failed early, while V2 completed it, and other tests varied. The Linux timeout occurred before Retention finished, so the last active budget subcase is not a demonstrated cause.
3. **The interval outside scripts-package events also matters.** #1022 generation 2 already used 582.670 of 600 seconds, leaving 17.330 seconds. #1024 includes about 123 seconds from command step to scripts package START. It cannot all be labeled compilation: logs only bound this mixed startup interval. No deterministic attribution to compiler, dependency setup, or contention is available.

## Archived timing comparison

| Archive | Outer seconds | Scripts observed seconds | Collector receipt |
|---|---:|---:|---|
| #1022 first Linux generation 1 | 499.409 | 410.576 | old bounded collector 0.30 s |
| #1022 first Linux generation 2 | 582.670 | 469.310 | old bounded collector 0.35 s |
| #1024 first Linux generation 1 | 600.108, timeout | 477.798, incomplete | Retention CONT, no PASS |
| Mac V1 integration | supplementary local run | 528.812 | 42 cases, 45.22 s; suite had a preserved fixture-environment failure |
| Mac V2 integration | supplementary local 12 m watchdog | 657.607 | 79 cases, 96.03 s; passed |

The #1024 named check was `scripts-integration`, the original 9-minute Go timeout within the 600-second outer wrapper, with `-parallel=2` and `-count=1`. It ended with exit -15 / wrapper 124, zero FAIL/panic, and verified zero remaining owned processes. Parent audit reports 602 top-level PASS and 6 SKIP across packages. The comparison here deliberately filters `./scripts` package: 516 PASS / 6 SKIP before timeout. These counts differ because their package scopes differ.

Retention continued at 18:45:39.843 UTC; the last package event was 18:46:48.971 UTC. Thus 69.128 seconds is an unfinished observed interval, not its measured duration or a completed 79-case receipt. Matched 515 completed scripts tests had aggregate durations 615.59 s in #1022 generation 2 versus 616.70 s in #1024. These overlapping per-test durations cannot be added to package wall time or used to exclude contention.

## Local controlled fixture changes

Each row uses the unchanged real collector. A native wrapper records process names and monotonic spans without arguments or contents. Instrumentation creates extra wrapper processes and inclusive spans overlap; do not add Python, SSH, and timeout totals. The first normal baseline was 2.223 s and remains archived as a cold outlier; the table uses the second normal baseline. The precomputed normal was the first corrected case. There are too few repetitions for a statistical latency claim.

| Case | Current collector seconds | Static host shell only | Precomputed shell | Exact archived semantic comparison |
|---|---:|---:|---:|---|
| Normal | 1.346 | 1.093 | 1.112 | pass |
| Eight large metric responses | 1.363 | 1.092 | 0.866 | pass |
| Missing worker evidence | 1.388 | 1.067 | 1.087 | pass, original failure retained |
| Invalid terminal anchor | 1.093 | 0.775 | 0.792 | pass, original failure retained |
| Formal plus capacity | 1.436 | 1.163 | 1.110 | pass |

Savings for these cases are about 0.23–0.50 seconds per invocation. The exact archived distribution is 21 cases with 15 SSH calls, 57 with 25, and one with 27. A deliberately coarse weighting using invalid-anchor, normal, and formal samples gives about 101.1 → 81.1 instrumented seconds, approximately 20 seconds saved but approximately 81 seconds of total remaining collector work. This is a local workload estimate only, not a Linux prediction; it illustrates why a 20-second saving must not be compared directly with the baseline's 17-second margin as proof of passing. The older collector had only one 0.35-second case. Runner variation and scheduling remain material.

## Exact comparison rules and first failure

`semantic-comparison-rules-corrected.json` lists the entire normalization allowlist. For each representative case:

- The scenario display label is mapped to the explicit baseline label.
- `manifest.observed_at` alone is set to a common timestamp. The original manifest bytes are reconstructed and verified against their original byte count and SHA before canonicalization; original and normalized SHA values remain in the receipt.
- Exit code, complete/failure state, every capture field, report hash, raw/normalized window, query, byte limits, budgets, request order, and every other retained file path/byte count/SHA are compared exactly. No temporary paths or errors are ignored.

Calls are parsed after execution from commands actually supplied to SSH, not synthesized from expected calls. The equivalence scope is the existing fixture call schema: document keys and metric query/start/end/step are explicit; the 14 older host captures retain the legacy `other` classification, plus one `targets` entry. This does not claim an archived byte-for-byte comparison of all original SSH argv, which the baseline artifact did not record.

The first precomputed prototype failed its normal case because a shell document-path pattern omitted a trailing wildcard. The real collector reported incomplete worker/status evidence and the strict comparison rejected it. `precomputed-first-run.log`, `precomputed-first-failed-results.json`, `profile_precomputed_cases.first-failed.py`, and its trace are retained. The corrected five-case run passed on its first attempt. The failing prototype is not product evidence, and the comparison was not relaxed. The sixth corrected repetition was omitted to preserve the total 18-invocation budget.

## Narrow next implementation proposal

Limit changes to `scripts/testdata/chat_lifecycle_terminal_evidence_test.py`, with the Go integration wrapper touched only if artifact metadata or a fixture-specific executable-launch assertion requires it. Do not change production collector/helper behavior, workflows, policy, timeout, test parallelism, test tier, or the 20-method / 79-real-process matrix.

Prepare deterministic response bytes and transport outcomes once per case in the existing Python fixture driver; use a small shell SSH adapter to select by the actual command, enforce its actual requested byte limit, preserve partial stdout and nonzero exit, and log actual requests for post-run parsing. Keep large bodies in files rather than shell variables. Do not add a background daemon or parallel cases. Preserve dynamic boundary fixtures by deriving their responses from the actual query parameters or checking actual query bounds against independently prepared fixture bounds; do not make expected outputs conceal an incorrect query.

Before implementation, write the fixture failure matrix and tests for all current mode semantics: document identities/boundaries, missing/empty/malformed/oversized/unavailable/partial/timeout; metrics query family, timestamp precision and closed boundaries, type/family/error, empty/large/oversized/partial responses; malformed commands and unmatched routes; response byte clipping and ordered actual calls; exact retained artifacts and interpreter-launch budget. Preserve a genuine RED against the current adapter. Map each existing case to equivalent assertions; the five prototype cases do not establish all-79 equivalence. Run focused integration first, then the original protected Linux named check once on the reviewed candidate. A further timeout remains a failure to investigate, not permission to increase limits.

No protected paths are needed for this proposal. No runtime/product message-loss fix is inferred from fixture timing. Additional broad optimizations should require new evidence.

## Repeatable scratch command

The prototype replay is intentionally separate from repository tests. It requires the frozen source files and the local native trace binary; it creates a fresh output directory, refuses an existing directory or drifted source, and runs the five representative collector processes sequentially. It was syntax-checked, not executed again in this round. Original measurement receipts are immutable and will not be overwritten.

```sh
python3 /tmp/wukongim-v3-readiness-20261021/cloud/pr1024-fixture-profiling/replay_precomputed.py /tmp/pr1024-fixture-replay-new /Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-terminal-evidence
```

This replay requires a new explicitly bounded measurement round if executed by the agent; the current 18-invocation allocation is exhausted. After a source edit, use an independently prepared checkout of the frozen c91 source. The script does not create checkouts, run GitHub Actions, touch cloud resources, or read credentials.

`final-receipt.json` records the final HEAD/source hashes, clean status, invocation count, and process cleanup snapshot. `SHA256SUMS` and `allowlist.json` enumerate only this bounded public diagnosis directory. Earlier original CI/full-suite records remain in their existing immutable archives.
