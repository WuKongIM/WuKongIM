## Summary / 变更摘要

The local three-node shakeout admitted 2,500 users at 100 SEND/s, but mandatory startup activity could not drain before the unchanged eligibility deadlines. Raise the local default to 500 SEND/s with a two-second burst budget; retain the user count, mandatory activity, deadlines, and latency/correctness thresholds. The regression exercises the real worker factory and grant allocator across all three workers for 360 virtual seconds and preserves the historical 100 SEND/s failure. The helper checks both default and overridden rendered rates.

On Bash 5, bare successful returns from EXIT/signal traps could skip Analysis close/local cleanup or the active child's join. Explicit successful returns let cleanup finish while preserving the original command/operator exit status. New process tests retain 17/143 failures, close-failure warnings and all cleanup steps. The build fixture reuses dependency cache while forcing and asserting a fresh binary build ID and exact selected revision; metadata inspection explicitly requests text despite inherited Go JSON flags. Profile assertions use portable fixed-string grep. Also fix a pre-existing test-only sync.Pool copy detected by vet.

An independent Linux scripts-integration job reads the exact frozen-base named-check arguments, working directory and limits, runs the full check once, and retains streamed JSON events plus identity/cleanup receipts. It retains the original nine-minute package and 600-second outer limits, with no test filtering or automatic retry. Default tests and migration/messaging E2E remain independent of that job's result.

## Release notes / 发布说明

- [x] Every user-visible change is recorded under CHANGELOG.md Unreleased.

## Verification / 验证

- Test-first startup-budget/default-render regressions passed. Earlier complete local default/vet/format/module/FLOW/workflow checks passed; the later test-only pointer correction also passed focused test/vet checks.
- The Bash 5 cleanup test first failed in all eight 17/143 branch cases, then passed after the patch. Actual Bash 5.2.21 on macOS passed 9 focused top-level tests / 15 subcases; actual Bash 3.2 passed the new eight cases and the real signal/join test. This is shell-version evidence, not an Ubuntu validation claim. Independent review found no blocking issue in this patch.
- On this final update, the named go-format check, default scripts package, both shells' syntax checks and diff checks passed. The exact fresh-build/JSON fixture and portable profile assertion also passed their focused regressions.
- One native macOS 30-minute helper run on 0672c115950f65e6d92381aa5b3ea9f0a20fc790 completed its expected coordinated operator stop (exit 130) with final.json and clean owned process/port cleanup. Its measured interval was 1,753.104 seconds; SEND/ACK were 876,529, and reported errors/harness-invalid/under-delivery were zero. Retain the two retries, 102 hot samples over 400 ms, route-not-ready warnings and component-stop deadline warnings for review. This is a local precheck, not formal Linux/72-hour qualification.
- Both original macOS full scripts attempts timed out at the unchanged nine-minute limit. Earlier Linux attempts also failed; the [JSON-observed Linux run](https://github.com/WuKongIM/WuKongIM/actions/runs/39565861218) retained ten top-level assertion failures and its 600-second outer timeout. None is replaced by focused green evidence.
- The original Linux observer-join exit 255 remains unlocated and unmodified: 20 bounded Bash 5 probes and one existing-test run on macOS did not reproduce it. The new exact-head Linux full gates must decide their own result. This PR remains draft pending that first CI result; no merge or rerun is requested here.

Final update head: `d7affe274935e78a644f901400db15ff1353a2c9`.

[Original startup diagnosis and failures](https://github.com/WuKongIM/WuKongIM/tree/9a23014ba7e38d9a57770e317cf40fc21bd288cd/docs/reports/v3-release-readiness-20261021/cloud/local-shakeout-grant-diagnosis) · [Startup repair independent review](https://github.com/WuKongIM/WuKongIM/tree/9a23014ba7e38d9a57770e317cf40fc21bd288cd/docs/reports/v3-release-readiness-20261021/cloud/local-shakeout-repair-review)
