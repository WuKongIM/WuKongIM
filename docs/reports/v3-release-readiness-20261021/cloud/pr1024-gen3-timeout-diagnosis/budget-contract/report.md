# Scripts integration budget contract — read-only source audit

Source: `44de9452d94edae16129b5781d794f8c1c6c7c57`; tree: `b69a44af04f20143b690e7a3860cc45caa95a0f4`.
Repository: `/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-terminal-evidence`.
Scope: source, history, and local installed upstream Go source only. No tests, builds, server/runtime commands, network, cloud, or CI operations were executed. No source changes were made. Artifact-run times and generation comparisons belong to the parent audit, not this report.

## Fixed policy and authority

- `.github/review-agent/policy.json:162-175` defines `scripts-integration` as `go test -tags=integration ./scripts/... -count=1 -timeout=9m -parallel=2`, working directory `.`, outer `timeout_seconds=600`, and combined output cap `8388608` bytes in the candidate runner.
- `AGENTS.md:193-196` makes this policy the sole machine-executable authority for check arguments, limits, and selectors. The candidate workflow reads the PR's exact base policy (or fetched `origin/main` for manual dispatch), proves it is an ancestor, and records its digest at `.github/workflows/v3-candidate-regression.yml:143-165`. Therefore the inspected HEAD policy is the current source contract; a particular generation's effective policy requires its retained `identity.json` and frozen `policy.json`, not an assumption that the HEAD file governed that run.
- Candidate scripts job: Ubuntu 24.04, job bound 20 minutes, `GOWORK=off`, `GOFLAGS=-p=2 -json` (`v3-candidate-regression.yml:130-136`). Checkout, policy freeze, static contracts, Go setup with cache disabled, and `go version` happen before `run_command` (`:138-176`). They are outside the 600-second command clock, though inside the job.
- Frozen fields are loaded and passed directly to `runner.run_command` (`:183-207`). The Python static contracts require that relationship, no copied `go test`, no deadline override or filter, and a 20-minute independent job (`scripts/v3_candidate_workflow_contract_test.py:53-91`). These are static structure contracts, not measured duration guarantees.

## Exact clock boundaries

| Clock / marker | Begins | Ends / effect | Consequence |
| --- | --- | --- | --- |
| Candidate outer 600 seconds | `started = time.monotonic()` after evidence directory creation and initial `command.json`/`result.json` writes, before signal setup, file opens, and `subprocess.Popen` (`scripts/run-v3-candidate-check.py:94-141`) | While the driver remains alive, the selector loop checks `monotonic()-started >= timeout_seconds` and selects status `timeout` (`:148-161`) | Includes command process startup, Go driver work, module/cache I/O, dependency preparation/build/link/vet/scheduling, all package test work and child workloads until termination. No separate build allocation. |
| Candidate retained `elapsed_seconds` | Same outer `started` | Calculated after owned-session cleanup, `process.wait(timeout=1)`, and final output drain (`:165-195`) | It includes post-decision cleanup; an elapsed value above 600 is not evidence that the nominal timeout itself was changed. Final JSON write and signal restoration occur after its timestamp. |
| Go `-timeout=9m` | Each package test binary arms `testing.M.startAlarm` inside `M.Run`, after `m.before()` (`Go 1.25.11 testing.go:2326-2337`, `:2661-2668`) | Timer callback calls `m.after()`, requests all stack traces, then panics; timer stops after tests/fuzz seeds/examples (`:2337-2340`, `:2668-2682`) | A 540-second package-test alarm, not an end-to-end `go test` driver clock and not a per-test 540-second allowance. Tests, their scheduling/admission waits, child builds/processes run by tests, and cleanup inside that test execution phase share it. |
| Go JSON package `Action:start` | `test2json.NewConverter` records `time.Now` and emits start (`Go 1.25.11 cmd/internal/test2json/test2json.go:111-129`); called in run-test action (`cmd/go/internal/test/test.go:1444-1466`) | It is emitted before build-failure handling and before actual test command creation/run (`:1468-1485`, `:1626-1661`) | It is a useful package run-action marker, but is not the precise OS process start or internal alarm start. |
| Go driver backup alarm | `context.WithTimeout(ctx,testKillTimeout)` shortly before child command launch (`Go 1.25.11 cmd/go/internal/test/test.go:1603-1626`) | With 9m ordinary tests: `testWaitDelay=54s`; backup `testKillTimeout=10m` (`:802-837`) | Another later-started clock, distinct from the candidate wrapper's numerically equal 600-second bound. It cannot reserve 10m beyond an earlier outer deadline. |

The candidate watchdog is a loop check rather than an asynchronous hard cutoff: output-limit and completed-process handling precede the timeout comparison, and the selector polls with 0.05-second timeout (`run-v3-candidate-check.py:148-161`). A process that has completed when polled is classified by its exit code before the timeout comparison. Do not infer exact microsecond cutoff behavior from the policy number.

Timeout status maps to wrapper exit 124 (`:183-194`). Cleanup searches the new SID, including separate process groups, and applies TERM then KILL with two-second phase targets (`:19-84`); result records original command status, cleanup completion, observed process exit, and final status. These are cleanup phase targets, not a mathematically exact additive five-second wall limit: enumeration and I/O also take time.

For the root scripts package, `TestMain` does temporary fixture setup before `m.Run()` and removal after it (`scripts/test_main_test.go:14-44`). These pre/post operations are within the driver/outer clocks but outside the internal `startAlarm` window. Their actual duration is not measured by this source audit.

## Concurrency and interpretation limits

- `-p=2` bounds Go package build/test process concurrency. `-parallel=2` only bounds parallel test functions inside each package test binary (Go 1.25.11 `cmd/go/internal/test/test.go:289-303`). They are different controls.
- The scripts helper invokes `t.Parallel`, a two-slot semaphore and a shared timing lock; exclusive and serial-phase paths also exist (`scripts/script_test_helpers_integration_test.go:10-51`; source routing at `script_test_helpers_test.go:38-61`). Static default tests retain ordinary serial scheduling under the integration build.
- Package/test event elapsed times and sums of individual test durations are not the outer wall clock; work may overlap, pause, or wait for admission. A last observed RUN/CONT test is not a demonstrated cause of timeout.
- Time before a package START is an unpartitioned pre-marker interval. It can include driver startup, module/cache/dependency work, builds/linking/vet, scheduler or predecessor waiting (`Go test.go:1402-1419`) and observation boundaries. It must not be labelled pure compilation. The README explicitly says Go build JSON has no per-compiler timing (`.github/workflows/README.md:268-275`).
- If an internal alarm starts B seconds after outer start, its nominal panic is at outer B+540 seconds. When B exceeds 60 seconds, the wrapper's 600-second deadline is nominally earlier. This is arithmetic, not evidence that B was pure build time or a guaranteed 60-second build budget. The JSON START marker is not B exactly.

## What is and is not measured in the repository

No independent cold-start/build/link/vet/test allocation, phase percentile baseline, or executable admission guarantee was found in the inspected named policy, candidate workflow/runner/static contracts, scheduler helpers, relevant workflow documentation, and targeted repository searches for the check and its budget values. The contract supplies ceilings, not expected completion times.

There is prior measured evidence: `docs/superpowers/reports/2026-09-23-deployment-test-stability.md:81-99` reports the then-current canonical check passed and gives root scripts package `503.582s`, with other package durations. This is a different historical source/tree and package output, not a retained end-to-end cold-worker 600-second command receipt or a 60-second startup/build guarantee. Its package values must not be summed as wall time or used to certify the current generation.

History: the 600-second outer setting and 9-minute internal setting already coexisted at original policy introduction `e8ae013d00a20cb69d619680de24d03038392189` (2026-07-30). `b352588cb1e9378b20baaa6f6bff9f96df1ce49e` adds the output cap; it does not change 600. Commit subject/history does not document a measured 60+540 phase design.

## Protected adjustment boundary

A real change to either named-check limit must change `.github/review-agent/policy.json`, the sole frozen authority; a candidate-only edit does not replace the base policy governing its own run. Increasing a wrapper-only deadline, changing copied arguments, retrying, filtering, or prewarming to turn the same run into passing evidence conflicts with the recorded candidate contract (`README.md:252-275`; static contracts `:53-91`). This audit proposes no such change.

Workflow changes touch `.github/workflows`, explicitly protected against Issue Agent automated candidates (`.github/issue-agent/policy.json:30-48`, especially `:35`; `docs/agents/issue-agent.md:103-120`; `AGENTS.md:189-196`). Review control/policy changes require their existing frozen control/base review path; candidate Review Agent control changes never govern their own review (`docs/agents/review-agent.md:98-102`). The literal Issue Agent protected-path list does not separately name `.github/review-agent`; do not falsely attribute that explicit prefix prohibition to that list. Review policy is nevertheless protected named-check authority and is outside this read-only task's authorized mutation scope.

The separate Review Agent Go executor was also read: it passes policy timeout at `internal/runtime/reviewagentverify/runner.go:224-235`, creates its context after sandbox preparation but before command construction (`os_executor.go:181-193`), and separately measures `command.Run` duration (`:213-222`). It is not the candidate Python wrapper and its evidence accounting must not be substituted for this workflow.

## Provenance

`rule-source-sha256.json` contains SHA-256 digests and exact Git blob IDs for source/rule files. The applicable root AGENTS and package-scoped FLOW files were read directly; there is no scripts-root FLOW in this revision. Upstream Go semantics were inspected from the locally installed `go1.25.11` source matching `go.mod:5`; its source digests are included. This does not itself attest which compiler ran on any CI host; bind each generation with its retained `go-version.txt`.
