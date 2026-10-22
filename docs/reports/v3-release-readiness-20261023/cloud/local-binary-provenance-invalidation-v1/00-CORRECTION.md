# Current finding: source provenance is incomplete; embedded revision alone is inconclusive

Read stamp-inference-limits.md before the chronological invalidation.json and README.md. Those preliminary files overstated “actuala804 code” and “79aCLI” inferences. They remain unchanged for the audit trail; this correction supersedes those labels.

Local product c253… and CLI e36e… embed Go1.25.11/a804/clean, while retained command/source evidence points to the79a diagnostic worktree. Independent objdump main.main source paths for both executables point into that worktree. The source content compiled into either executable cannot be concluded from the VCS stamp alone. Go VCS-root/stamping behavior is under independent audit. Strict candidate proof remains paused; this does not by itself prove that the product or workload executed olda804 source.

The explicit small-test WK_E2E_BINARY override and all full-run copy chains are proven. The earliest override construction/copy command is absent from retained invocation data; it remains a gap. The default E2E harness rebuilds once per test process rather than reusing a file merely because it exists. Eight named attempts are enumerated in invalidation.json, with small-v2 compile failure and full-v1 preflight refusal correctly distinguished from actual product runs.

No new build, run, real profile or cloud operation was started after discovery. The source-clean construction document is a plan only, awaiting the independent executable/ELF/stamping audit. Original failure evidence, raw private DBs/identities/profiles and sealed manifests are preserved unchanged.
