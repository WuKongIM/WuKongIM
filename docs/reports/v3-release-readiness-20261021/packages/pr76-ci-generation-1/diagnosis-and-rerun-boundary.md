# Bounded diagnosis and proposed distinguishing experiment

This is read-only analysis of attempt 1. No local replay or rerun was executed. The diagnosing-bugs evidence route is used because the task excludes test execution and changing source. Root subsequently chose to dispatch one failed-job-only rerun after this archive is sealed; this collector does not dispatch it.

The exact Alma command, including container resource limits and mounted public fixtures, is preserved in `first-failure.json`. Its inner sequence is:

```sh
timeout --kill-after=10s 600s bash -euc '
printf "%s  %s\n" "$WK_PACKAGE_SHA256" /candidate.rpm | sha256sum --check --strict
rpm --import /reviewed-key.asc
rpm --checksig /candidate.rpm
dnf --setopt=localpkg_gpgcheck=1 --setopt=timeout=60 install -y python3 /candidate.rpm
python3 /acceptance.py --fixtures /fixtures --version "$WK_EXPECTED_VERSION" --commit "$WK_EXPECTED_COMMIT" >/evidence/installed-cli.json
'
```

Hash and RPM signature success are visible. The last and only subsequent line is Alma AppStream metadata 30 MB / 05:59. `docker start --attach` exits 124 after 600.218 seconds. The coordinator's distinct 650-second bound did not fire (`timed_out:false`). There is no dependency-resolution-complete, transaction, or CLI acceptance output. The data does not locate the remaining approximately 241 seconds or uniquely establish a network cause. It does not show package installation or CLI execution reached.

Ranked, falsifiable hypotheses (unconfirmed):

1. Distribution repository/mirror transfer delay: same source on another runner should change repository progress/latency while product identity stays fixed. Supporting evidence: preceding Rocky AppStream took 04:48 and a normal mirror returned 403; #75 corresponding Alma completed in 15.029 seconds.
2. Runner/container resource or transport path pressure: another runner should change elapsed time; bounded CPU/I/O/network observations would distinguish it. No such metrics were retained in attempt 1.
3. Mutable distribution metadata/package choices or local package input differences: comparing repository metadata and transaction plans would reveal a difference. Exact images, product binaries and existing controls match, but signed fixtures are freshly generated and public distro metadata is not frozen.

`pr75-comparison.json` proves all 97 pre-existing scripts/tests/manifests/keys/toolchain files and the native workflow are byte-identical; the complete commit diff is only the reviewed D2 eight files. All four built binary SHA-256/size pairs, image pins and host tools match. The differing `go version -m` strings differ only in their initial temporary path. The SDK lock is installed in a separate job and the native failing path does not import the new SDK/input libraries. This is strong exclusion of a changed native code path, not proof of complete causal independence or a unique external cause.

The existing contract (`docs/release-contract.md:560–581`, `docs/stable-audit-transport.md:129–135,190–197`) requires fresh installed containers, normal distribution dependency repositories, local-package signature checks and the existing 600-second limit; timeout remains a failed gate. Preinstalled dependency images, global cache-only mode, moving installation out of this boundary, extra retries or longer deadlines would change that contract. No such fix is proposed here. The 49 scenarios do not all run this installation step: only baseline A/B and installed-CLI rejection cases do, so describing 49 repeated distro installs would also be inaccurate.

No packages-specific automatic CI retry policy was found. The main repository Workflow README's preservation-before-deliberate-rerun statement is in the V3 candidate regression section (lines 285–294), and its once-only review infrastructure retry is specifically Review Agent (lines 799–803); neither automatically authorizes this collector to retry package CI.

One root-authorized same-head failed-job-only attempt, retaining the first failure and complete second result separately, can distinguish repeatability without changing the production contract. GitHub documents that reruns preserve the original GITHUB_SHA and GITHUB_REF and support failed-job-only selection: [GitHub documentation](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/re-run-workflows-and-jobs). A second pass would show that the symptom did not recur on that attempt; it would neither erase the first failure nor prove network causation. Root chose exactly one `gh run rerun 39644526993 --failed` after archive readiness; no third attempt is authorized by this decision.
