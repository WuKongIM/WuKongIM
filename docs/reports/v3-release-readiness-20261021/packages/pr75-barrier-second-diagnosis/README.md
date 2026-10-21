# PR75 second request-barrier diagnosis and narrow repair

Original CI run 39636512103 attempt 1, source d824fe37918ee4ef22b1b1745bc02518812563e3 / tree50e099e7adcda9e205c9862dad7e1071022d2a3c, is preserved in original-first-failure.json. It passed 22 cases, failed Rocky case 23 with signed B 3.0.1 at the exact-A gate, and did not reach 26 cases. The original 1070-byte combined output is complete and hash-verified. Original per-phase clocks/cache persistence were not captured; this archive does not uniquely attribute that historical run's timing or GPG trigger.

## Confirmed mechanism

The first DNF command can write wukongim-stable into expired_repos.json while closing after metadata_expire=0 has elapsed. The next command explicitly expires that repository from persisted state; its own effective metadata_expire=-1 does not override explicit expiry. The prior probe delayed after first-command exit, so it did not cover this teardown boundary.

Both fixed native Rocky and Alma images reproduce this with the exact d824 shell (SHA256 1cba6dddb55981e8c4dd0e60f54e108eb10923d06b33008906275c5895c3cb1d), real signed stable bootstrap built by the unchanged production build function, real signed repository and data-only product RPMs. The observer delays 2.1 seconds immediately before the original DNF persistence method, then calls that real method. It does not fabricate/delete the persisted state or change any mtime. It records effective repo ID/options, metadata facts, clocks and actual persistence path.

The observed persistence file is /tmp/client-root/tmp/dnf-cache/expired_repos.json. The later real repository cache is /tmp/dnf-cache/wukongim-stable-5702a8a51b8fa885. The current command persists ["wukongim-stable"], then the second effective -1 command still has native_expired=true and selects B. Applying -1 to both phases while retaining initial makecache --refresh persists [], keeps A metadata, and really retrieves exact signed A via B mirrorlist then B-prefix HTTP 200. Independent fresh-B uses normal command policy and selects B.

No global cacheonly, forced A server responses, state deletion or alternate-version acceptance is used. All native GPG, package-signature and TLS checks remain enabled. These probes validate the complete native bootstrap/download shell with small data RPMs, not full product/installed-CLI/source provenance or all 49 cases.

## Frozen repair

Worktree: /Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-barrier-second-diagnosis
Base: d824fe37918ee4ef22b1b1745bc02518812563e3
Candidate tree (temporary index; no commit): 2119196cef38a159f23a8f5c47f8878e4fde8399
final.patch SHA256: ae81b89b062d474aeb51f56933f0773db536d171524fa75cab8026f56f8960e0
final-source-freeze.json SHA256: de2460d6071efd7e277eed75f8f8a66d900cdbac3d51576c201e361c90362e5c

Only driver, its existing tests, stable transport docs and Changelog change. The switch helper adds the already selected repository policy to the first makecache command too; --refresh and the second-stage flag remain. Existing exact-B-prefix/ordered/same-case/exact-A evidence is unchanged. All other 114 tracked files are byte-identical, including producer, planner, production validator, workflow and remaining delivery-core files.

The new shipping-generated shell SHA256 109be5dbb5dc224f3e6674a32c24c68262b58402244870187176d89d4814ee9c equals the actual successful comparison body in both native probes. There is no claim based merely on a similar hand-written native command.

## Tests-first and validation

- FAILURE-INVENTORY.md precedes shipping changes and lists scope/failure modes.
- Updated existing test was frozen before implementation; 44 tests yielded exactly one assertion failure, no missing API errors.
- After the minimal change, all 44 related tests pass. Test bytes still equal the pre-code freeze.
- Complete local suite: 557 tests pass; 1 pre-existing Mac native-RPM skip.
- Repository contracts and 4 contract tests pass; git diff --check passes.
- Rocky and Alma signed probes each preserve current-strategy red / proposed-strategy green / fresh-B green, with effective config and native persistence evidence.
- All exec sessions completed. Signer and both native containers were removed with exact UUID-label empty-inventory receipts. Only public TEST ONLY signing artifacts were exported after test secret-key destruction.

Full exact-head Linux 49-case acceptance and independent review remain separate requirements. No shipping commit/push, CI dispatch/retry/cancel, production credential input, publication or cloud operation was performed.

## Evidence and reproduction

native-probe-summary.json is the compact phase/HTTP comparison; signed-client.stdout and alma/signed-client.stdout retain complete public probe results. signed-*.receipt.json retains actual Docker commands and cleanup. instrumentation/sitecustomize.py is diagnostic-only and is not shipping code. HYPOTHESES.md records the pre-experiment hypotheses. Original CI, new red/green, source/rules, unchanged-source checks and validation outputs are retained separately.

source-d824.tar.gz is a git archive of the exact clean original source. To reproduce, use a fresh evidence directory, extract that source into a separate directory mounted as /control, and copy the probe scripts/instrumentation there. Point only the runner's control path to the extracted source. Run `python3 run-signed-probe-owned.py build` and then `python3 run-signed-probe-owned.py client`; the latter is bounded and owns cleanup. For Alma use its captured runner image and a separate new output directory. Never rerun into this frozen evidence tree or use the modified working tree as the old-code input.

Primary source references: https://raw.githubusercontent.com/rpm-software-management/dnf/4.14.0/dnf/cli/cli.py ; https://raw.githubusercontent.com/rpm-software-management/dnf/4.14.0/dnf/persistor.py ; previous diagnosis contains libdnf0.69 Repo.cpp and dnf Base source. The source model is backed here by actual installed pinned native behavior.

ARCHIVE_ALLOWLIST is finite. SHA256SUMS covers payloads plus allowlist; archive-sha256.json contains size/SHA256/CRC32 for these files plus SHA256SUMS, excluding itself. Later review evidence belongs separately, without changing this freeze.
