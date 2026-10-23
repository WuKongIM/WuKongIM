# Bounded metrics-fetch diagnostic candidate

Candidate **56c287493a5e9fc3df65ea0631b99612672cd678**, tree **582e5bc024147b32aa68074d04c041391af89b96**, based on merged **27fa6d63cf3b2b6c3a9a28575f917d60cc054ded**. Branch `codex/v3-metrics-fetch-diagnostics` has a clean dedicated worktree. The 13-file diff contains 1,176 added and 22 removed lines, including 920 lines of tests written before production changes. Nothing was pushed; no PR, service workload or cloud action was started.

The root approved option A of design manifest `92edcd4b2ecc08e3daddafebcb942511bc88fed550742094740264e2379d3901`. The candidate classifies failures in the single existing HTTP acquisition loop, wraps Metrics errors with safe closed text while preserving the original chain, and projects the fixed three-source mask/kind vector only for `meta_checkpoint / metrics_fetch`. The three original source errors stay in a private fixed array; the aggregate unwraps only the previous harness-invalid sentinel. Parent context precedence, all-three joining and address fallback remain intact. Source positions follow `observation.service_nodes`, never physical node IDs.

The approved 54-case matrix was mapped to tests before production edits. The first RED compiled and ran but included a test-fixture mistake: its Authorization expectation omitted the existing `Bearer ` prefix. That first RED and its test hashes remain unchanged. The fixture was corrected while production was still untouched; v2 test hashes were frozen, then the meaningful RED v2 ran. No v2 test changed through implementation and validation.

| Check | Result | Counts | Elapsed |
| --- | --- | --- | --- |
| First RED, old production | Expected failure plus documented fixture issue | 127 failing / 325 passing test events; 8 failing / 14 passing top-level tests | 12.438s |
| RED v2, old production | Behavioral failure, compiled successfully | 122 failing / 330 passing events; 8 failing / 14 passing top-level tests | 2.211s |
| Same v2 selection, GREEN | Passed | 452 passing events / 22 top-level tests | 6.936s |
| Same v2 selection with race | Passed | 452 passing events / 22 top-level tests | 22.977s |
| Full target, chatlifecycle and all wkcli packages | Passed | 2,452 passing events / 1,109 top-level tests; no test failures/skips | 61.397s |
| Named go-format | Passed | Policy-resolved selector | 1.564s |
| Named flow-doc-contracts, final docs/index | Passed | 20 existing nonblocking FLOW length warnings retained | 0.082s |

The full suite reports three packages with no test files; these are package-level skips, not skipped tests. Exact elapsed values, argv, working directories, toolchain/offline environment, return codes and output bounds are authoritative in each receipt. New tests use synthetic in-memory HTTP transports/readers and deterministic contexts/channels, without TCP servers or sleeps. Existing complete package suites include their pre-existing tests. `coverage-execution.json` resolves every coverage pattern for all 54 cases to passing full-suite test events.

`candidate-receipt.json` proves the entire metrics parser/helper suffix, target numeric limits, metadata settle constants/loop, and finalization verdict/report function remain byte-identical to the base, along with the existing coordinator, CLI production composition and check policy. The root checkout remains unchanged. All invoked commands completed; no task-owned background workload was started.

This candidate adds observability. It does not identify c495's unretained failed round, repair the remaining expired-message case, grant report completeness or provide Linux performance acceptance. Independent review and any subsequent finite workload remain separate work.
