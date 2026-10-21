# Terminal evidence collector repair — V2 delivery

Status: implementation is commit-ready at the frozen seven-file worktree snapshot. No commit, push, workflow dispatch, cloud launch or product change was performed by this implementing agent. The original message_loss runtime cause remains unknown; this repair only closes proven diagnostic collection defects.

## Exact source

Base HEAD: cee5d51f325fb84033060f546337d326110adae5.
Worktree: .worktrees/v3-rc-terminal-evidence, branch codex/v3-rc-terminal-evidence.
review-source-freeze.json SHA256: cdc393656f874d1455f7380b839f433adc1accf8768d3987b0cbb05e0019957b.
final-freeze-verification.json confirms all seven files and the base HEAD stayed unchanged through the full suite; no own test processes remained. All source edits stay within collector/helper, integration fixtures and operator documentation; no FLOW, Skill, workflow or protected policy changed.

## Behavior

- Fetch bounded worker-evidence and diagnostic-status documents, bind their exact terminal run/worker generation/sequence/cut, and preserve truthful missing/invalid/oversized/timeout states.
- Collect eight fixed disjoint metric partitions: the original selector factored into six plus two exact existing presence/delivery diagnostic families. The whole collection shares at most eight requests, 24 MiB observed bytes and 60 seconds; each response remains at most 4 MiB. Partial failures retain successful siblings and mark evidence incomplete.
- Anchor metrics to the terminal report, with a canonical closed millisecond query interval and raw nanosecond cut retained. Capacity chronology remains nanosecond exact. Legal formal failure plus full unattempted capacity phase evidence is required for not_applicable; successful warnings cannot exempt capacity.
- Use a non-product environment namespace for test artifact output. The original workload verdict and all product thresholds are unchanged.

## Validation and retained failures

- Independent V2 Standards: zero findings. Spec: both V1 P2 findings closed, 52 independent probes passed, zero new findings. Reports are in ../terminal-evidence-review-v2.
- Focused corrected suite: exit 0, two Go entries, 20 Python methods, 79 process-level fake-SSH cases; 0 failures/errors. See focused-corrected.log and focused-corrected-results.json.
- Full scripts default tier: exit 0, 679 pass records / 391 root tests, one GNU gawk skip, no failures; 32.363 seconds. See full-default-summary.json.
- Full Mac integration supplement: FIRST run exit 0, 1294 pass records / 645 root tests, 11 explicit skips, no failures; 657.684 seconds. The previously failed shakeout test now passes and verifies the artifact namespace fix. See full-integration-summary.json.
- The Mac integration run used -timeout=12m, -parallel=2, -count=1. This is NOT a pass of the protected scripts-integration named check. Linux must still pass the original policy 9-minute command on the final PR. No gate or policy has been weakened.

All original failures remain in V1/V2 files: V1 test-first RED, two fake-SSH quoting fixture failures, original full integration environment conflict; V2 t.Setenv/parallel fixture startup failure, behavioral RED, first focused boundary-mode fixture failure, corrected exact-V1 precision RED, and final green. precision-red-correction.json distinguishes invalid unavailable-based fixture failures from the corrected precision proof: exact V1 helper hash, nonempty real fixture response bytes, eight contract failures and no errors. Missing-end fixture setup was corrected separately without changing its invalid-anchor assertion. No failed evidence was overwritten or promoted as green.

## Reproduction

The source overlay contains the exact seven reviewed files for the stated base. Read the repository instructions before applying it. The focused command is:

    GOWORK=off CHAT_LIFECYCLE_TERMINAL_EVIDENCE_TEST_ARTIFACT=/absolute/output.json go test -tags=integration ./scripts/... -run '^TestChatLifecycleTerminalEvidence' -count=1 -timeout=4m -parallel=2 -v

Every network operation in the new fixture is fake SSH; its artifact contains per-case manifests, request windows and retained file hashes. The existing full integration suite also runs local product fixtures. execution-receipt.json records exact scopes and distinguishes the Mac supplement from remaining Linux gate work.

The public delivery includes only finite local fixture/test logs, sanitized source observations, independent reviews and the frozen source overlay. It excludes the original private cloud archive, cloud raw logs, profiles, SSH configuration and signing/other private keys.
