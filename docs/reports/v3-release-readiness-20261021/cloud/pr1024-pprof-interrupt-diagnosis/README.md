# Confirmed local threshold pprof cancellation race

Exact source: `2cc562fa39f7b430c61a4494636f6ee0ec0d631a`. Source and working tree remained unchanged throughout this diagnosis. The only c91→2cc change was the terminal-evidence **test fixture** optimization; it did not touch the pprof helper or this test. The spawn/registration and cancellation code dates to commit `7cacad8e97` (August 14); a September context-capture extension retained the same sequence.

The real helper has a signal window after background curl launch and before the child PID and temporary-file records are appended (`capture-wukongim-local-threshold-pprof.sh:387–394`). The TERM trap immediately terminates and joins only registered PIDs. A child started inside this window can outlive helper exit, retain its HTTP request until its own timeout, and leave an unregistered temporary file.

## Original failure and archived history

The #1024 generation-2 Linux scripts check ended normally with exit 1 after 575.954 seconds, not by timeout. Its unique failing test was `TestLocalThresholdPprofInterruptJoinsRequestsAndWritesPartialMetadata` at line 438: server 2 still had one active request after helper exit. The original test already checked helper exit success, signal-to-exit <=3 seconds, and `partial` / `interrupted` metadata before checking zero requests.

Test CONT: 19:20:44.398641795 UTC. Zero-request failure: 19:20:46.439800941 UTC, 2.041159 seconds later. Final FAIL after server cleanup: 19:20:50.432306791 UTC, 6.033665 seconds after CONT. That final duration matches the CPU curl's `1+5` second max-time. Archived prior passes were 0.04 seconds in #1022 generations 1 and 2 and #1024 generation 1, and 0.09/0.07 seconds in the earlier Mac full suites. These intermittent passes do not disprove a narrow signal race.

One original focused invocation on the frozen current source passed in 0.07 seconds (package 0.961 s). This is explicitly not a reproduction or a fix. No full suite or CI was repeated.

## Falsifiable hypotheses and bounded probes

`plan.md` recorded three hypotheses before probing: missing registration / cancellation propagation; delayed server Context or deferred counter observation; shared-runner contention. The measured causal difference is registration state. The probes use real curl and real Go HTTP handlers with the same ten-second delay/context cancellation pattern as the original test. Only BASH_ENV DEBUG timing hooks were added outside the repository to force a signal at a fixed command boundary. No response or child process was mocked, and the script source was not changed.

| First probe outcome | Registered PIDs | Helper outcome | Request at original two-second check | Context finishes |
|---|---:|---|---|---:|
| Initial diagnostic hook | none | hook failed before requests: unsupported BASHPID on Mac Bash 3.2 | zero requests ever started | n/a |
| First-child, before PID registration | 0 | exit 1; original empty-array/set-u error; metadata still capture_in_progress | node1 active1, orphan curl PPID1 | 6.0802 s |
| After complete first-child registration | 1 | exit0; partial/interrupted | zero, child already gone, no temporary file | 0.064254 s, before helper exit0.074940 s |
| Node2 CPU, before PID registration | 3 | exit0; partial/interrupted | node2 active1, orphan curl PPID1 | 6.0788 s |

The initial hook error, its source/binary/build output, zero-request receipt, and corrected-hook first outcomes are all preserved. The explicitly approved final node2 probe avoided the first-child empty-array behavior and reproduced the original CI's server2/exit0/interrupted failure pattern. Total runtime invocations were one original focused test plus four probe launches: one zero-request hook failure, then three real HTTP measurements. No probe was repeated to obtain a favorable result.

Node2 probe chronology: node2 CPU begins at 0.075280 s; the three registered node1 requests all cancel around 0.0887 s; helper exits normally at 0.105678 s; at 2.122825 s node2 remains active and its curl has PPID1; only at 6.078766 s does Context cancellation occur, followed five microseconds later by the handler decrement. Its `.next` file remains because no registration covered it. This confirms a helper ownership race, not merely a delayed active-counter observation. Contention is not required to trigger the race. The original CI did not record exact signal instruction/PID traces, so the diagnosis does not claim to recover its precise instruction or quantify runner contention.

## Regression seam and minimal fix proposal

Keep the existing nondeterministic real-process interrupt test and its deadlines. Add deterministic integration cases at the same public seam: real helper, real curl, loopback HTTP handlers, actual HUP/INT/TERM. Force the first-child and node2 registration windows with a test-only BASH_ENV timing hook. Before signal, wait for the chosen real request to start. Record request lifecycle, helper exit/metadata, child liveness and temporary-file cleanup into a repeatable artifact. Each case must fail against the frozen original source before any helper edit.

Treat background launch and all PID/tmp/metadata registration as a short signal-deferral critical section. A signal arriving there should be remembered, registration should complete, and the existing terminate/join/temporary cleanup should run immediately afterward. Do not merely kill `$!`, broaden the kill domain, extend curl/test timeouts, or weaken request-zero checks. Cover the first-child Bash 3.2 behavior explicitly so an empty-array error cannot masquerade as successful cancellation. Keep cleanup safe for zero registered children outside the critical section as required by the existing signal contract.

Expected edit boundary: shell helper, its integration test, concise operator Changelog, and necessary PROJECT_KNOWLEDGE entry. No workflow/policy/Skill/FLOW change is required. Root authorized a subsequent tests-first implementation round only after this diagnosis was sealed; no implementation is included here.

## Reproduction and scope

The archived Go probe accepts frozen source root, a **new** output directory, and mode `before-node2` or `after`. Compile its explicit single file, then execute sequentially; expected `before-node2` exit is 1 because it asserts the original zero-request/no-temporary contract.

```sh
GOWORK=off go build -o /tmp/pprof-probe /tmp/wukongim-v3-readiness-20261021/cloud/pr1024-pprof-interrupt-diagnosis/probe-node2-prepared.go
/tmp/pprof-probe /Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-terminal-evidence /tmp/pprof-probe-new before-node2
```

This diagnosis used Go1.25.0, Apple Bash3.2.57, and curl8.7.1; no environment was installed. It proves a local diagnostic-tool bug, not a product messaging root cause or a completed v3 acceptance. All requests and owned process groups were gone at closure. Source/rule digests and cleanup are in final-receipt.json. The public ZIP allowlist excludes compiled binaries; it retains source, first failures, complete probe receipts, metadata and SHA evidence, and sanitized CI excerpts only.
