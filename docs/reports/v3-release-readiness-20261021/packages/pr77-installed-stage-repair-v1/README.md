# PR #77 installed sandbox phase diagnostics — frozen local repair

This five-file change adds bounded diagnostic observations around the existing generated installed-sandbox commands. It does not fix or uniquely explain the original timeout, change acceptance, or establish a successful real 49-scenario run.

## Source and original failure

Base/head `19ce29fb2233b3039e29c503963e27bd1fab972a`, tree `8275ccb92c310f488d911b5aa077a962e1faad5c`, worktree `packages-native-preview-fixture`. `source-freeze.json` binds the exact five changed paths and source copies. Its SHA-256 is `38aaaba7eae628c67b85bfc2dbce4ea8411acba6563e853888a120a83266a576`; `implementation.patch` SHA-256 is `71c96b15386813eafbd5609b045866ada0feb99dceea9b6a950f269efae57986`.

The immutable first CI archive remains at `../pr77-ci-generation-1`. Its exact collector handoff hashes are bound in `first-ci-handoff-binding.json`. Run 39660717995 attempt 1, stable job 119432670192, checked out merge `e306351985ed3ee0c235bd120de7032c94ad6d86`. Alma installed command 66 exited 124 after 600.151 seconds; the outer 650-second runner timer did not fire. Original 49-scenario result: 0 completed cases passed, 1 failed, 48 not reached. The 137-byte output proves candidate checksum and RPM signature success, followed by AppStream metadata output. Dependency transaction completion and whether installed CLI began are unknown: acceptance stdout was redirected to an unarchived file. No unique network, DNF, or CLI cause is claimed.

## Scope and interpretation

Only CHANGELOG.md, scripts/validate-production-package-clients.py, tests/native_stable_clients_drill.py, tests/test_installed_cli_acceptance.py, and tests/test_native_stable_clients_drill.py change. The shell emits complete fixed lines `WK_INSTALLED_STAGE_V1:<checksum|signature|dependencies|cli>:<begin|complete>`. RPM emits eight; APT emits six with no RPM signature observations.

Only installed-client command receipts gain `installed_phase_events`, schema `wukongim/native-installed-phase-events/v1`, an ordered list of at most 16 whitelisted `{phase,event}` values, and `truncated`. Duplicates remain diagnostic observations. Unknown, incomplete, prefixed, or CRLF markers do not enter this summary. Output capture, SHA, size, and tail stay authoritative and unchanged. These events cannot satisfy installed CLI receipt validation or turn failure into success.

`original-behavior-preservation.json` records AST equality outside the two narrow implementation seams; removing only marker lines restores each generated shell byte for byte and all other command arguments match. The native 49 cases, validators, commands, images, resource limits, 600/650-second deadlines, 8 MiB capture, and 4096-byte tail remain unchanged. Workflows, policy, producers, public keys, and channel state are unchanged.

## First results and limits

The failure matrix and all nine new test methods were written before implementation. `first-red-tests/` preserves those exact test bytes. First RED ran against unchanged implementation: nine methods, 22 failing assertions including subtests, zero errors. It actually executed generated bash with controlled external-command fixtures, including nonzero failure, missing command, a killed dependency block, and more than 4096 bytes of noise. The same test bytes first passed after implementation: 9/9, 8.236 seconds. Malformed markers, overflow, duplicate order, absent events, invalid installed receipts, unrelated command records, and the existing output limit are covered.

The first full default run completed: 581 tests run = 580 passed + 1 existing local skip, 131.456 seconds, exit 0. `bash tests/contract.sh` passed including four existing embedded tests. Complete command receipts and stdout/stderr are retained. These local controlled tests do not replace real native signing, installed CLI, M1, SDK, or all 49 Linux scenarios; new-head CI remains pending. Earlier D2 tests-after-code history is unaffected and not rewritten.

All owned test commands reached terminal state within recorded deadlines. Controlled timeout children use the existing process-group cleanup; temporary fixtures use scoped cleanup. This repair launched no Docker workload, CI, provider, or cloud operation and accessed no production private key. No commit or push was made. Source is frozen for independent dual review; M2a remains paused.

## Public evidence

`public-allowlist.json` lists the finite payload set. `archive-sha256.json` binds those files and the allowlist by size/SHA-256; it excludes itself. Only bounded public source, controlled fixture output, and validation receipts are included. The original CI archive is referenced rather than modified.
