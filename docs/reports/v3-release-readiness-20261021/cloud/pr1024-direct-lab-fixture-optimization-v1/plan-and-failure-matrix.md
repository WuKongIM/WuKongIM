# Direct-lab Start fixture: tests-first bounded optimization

Base HEAD44de9452d94edae16129b5781d794f8c1c6c7c57/treeb69a44af04f20143b690e7a3860cc45caa95a0f4. This follows the separately sealed read-only timeout diagnosis. No product behavior, script implementation, shared scheduling, workflow, policy, limits, full suite, or CI rerun is authorized here.

## Allowed source files after evidence establishes benefit

1. scripts/chat_lifecycle_direct_lab_test.go: remove only TestChatLifecycleDirectLabStartBuildsAndQuotesBeforePaidAcquire.
2. scripts/chat_lifecycle_direct_lab_start_integration_test.go: new integration-tagged home for the same complete test and assertions; inject only existing gate/bundle tool seams with one fail-if-called executable.
3. docs/development/PROJECT_KNOWLEDGE.md: concise scope/fixture knowledge. The existing PR includes user-visible Changelog entries for its other changes; this pure test optimization adds no product change.

No other legacy test is migrated. The existing default-tier process-test debt is recorded without expanding this task.

## Pre-agreed seam and differential method

The actual scripts/chat-lifecycle/direct-lab.sh start entry, with the existing fake bundle builder, plan/cloud/release tools. Two existing but unused command seams are the only optimization variable. Scratch retains the original test body and every original operation-order, file presence/mode, state, and budget assertion. A diagnostic Go shim records and executes the real Go tool, and a final assertion requires no unnecessary Go calls. A and B have the same scratch file, test name, helpers, serial scheduling, tracer and artifact logic. B differs only by providing both unused tool environment inputs. A is expected RED after all original assertions pass, with two successful real builds; B expected GREEN without those builds. Failures are not erased or rerun until green.

Each run has a 90-second outer scratch process-group watchdog and a 70-second Go test alarm. There are at most one baseline and one variant execution unless a separately recorded fixture startup error needs correction. These are local fixture measurements, not the protected Linux named gate, and cannot prove full-suite recovery. No prewarming or cache deletion is performed; build-event elimination and semantic equality, not a single timing ratio, establish the optimization. Real unused tools may be compiled in baseline; no cloud endpoint or product server runs.

Public artifact retains operation trace, actual Go build argv/start/end/exit, process outcome, original assertion completion, required JSON content, file permission/presence and normalized semantic comparison. No ephemeral SSH private/public key bytes or compiled binaries are exported. Only state.json created_at and activated_at are normalized for semantic comparison; ephemeral absolute fixture paths in Go argv are separately normalized for display. Random keys are checked by the original presence/private-mode assertions, not compared by bytes. Timing and the intentional absence of tools/wkcloudgate and tools/wkcloudbundle are reported, not hidden as equal artifacts.

## Failure matrix written before optimization

| Failure mode | Existing/added feedback and expected result |
|---|---|
| Unprovided gate/bundle inputs trigger real unused builds | Scratch diagnostic postcondition sees actual Go calls; baseline target RED with 2 completed builds after all original assertions. |
| Either injected tool is unexpectedly called (even if caller ignores nonzero exit) | Fail-if-called stub appends an unexpected operation to the original exact calls log and exits nonzero; complete existing call-order assertion must reject it. |
| Optimization skips build/materialize/quote/selector/backstop/acquire or changes their order | Original exact 8-operation trace remains byte-identical and required. |
| Request artifacts missing, permissions loosen, state/source/lease/budget change | All original presence/private-mode, active state, and exact policy assertions preserved; JSON semantic equality adds independent A/B evidence. |
| Default tier still executes this real-process case | After source move, default go test -list must not list exact name; integration must list it exactly once. |
| Integration migration loses or duplicates behavior | Extracted old function minus only added stub/2 env lines must equal new function byte-for-byte; focused integration must execute exact named test once and PASS. |
| Scratch timing conflates optimization with t.Parallel migration | A/B stays in same serial scratch file; integration scheduling is separately described and measured, never counted in A/B saving. |
| Other direct-lab tests regress | One related default direct-lab focused run and one integration direct-lab focused run; preserve first results. |
| Missing cleanup or elapsed bound | Exact owned group final receipt lists zero processes; TempDir cleans ephemeral keys/binaries. Keep any initial failure and stop on unattributable result. |

## Validation and delivery boundary

Only after baseline target RED and semantic-preserving B GREEN, land the narrow test fixture change and tier move. Freeze source/rule digests, diff and first run artifacts for Standards/Spec review. No commit/push. Full original-policy Linux CI remains parent-owned and required on a reviewed next head.
