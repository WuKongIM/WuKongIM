# PR #76 attempt 2 — terminal success with first failure retained

The bounded CI evidence supports merging the previously reviewed head `dded26afa12188d5f1c036af4b9c06c3cbddb46d`, tree `e319ee72e8d30f012fd5e6206d131e2b75c47a09`, under the parent's existing authorization. This collector did not change source, push or merge. PR #76 remains draft and unmerged in the final GET snapshot; mergeability is clean. No third attempt was requested or executed.

[Run 39644526993, attempt 2](https://github.com/WuKongIM/packages/actions/runs/39644526993/attempts/2) completed successfully at 18:12:17Z. Only stable job `119391226142` executed anew. The signed-repository projection `119391228348` retains the original job's 17:31:20–17:37:27 timestamps, runner and every step, and its complete downloaded log is byte-identical to original job `119376462664`. It is not a second signed-repository execution.

The log, receipt and GitHub commit API agree on checkout `3e4b7945206faffc18d6023f2b50311d93bd4348`, whose exact parents are base `1829c3813d6b21dcfeb1238a640604a4691815bd` and the reviewed head. Its tree equals the reviewed tree. All eight source hashes match the rebase freeze; the local worktree is clean. Latest head checks are successful for Pages validation, SDK, signed native and stable; Pages deployment is intentionally skipped on a PR.

- Stable: **49/49** cases passed, no unreached cases. Names and negative-case expectations match the existing complete 49-case contract.
- Installed CLI: **eight** verified acceptances across A/B and Ubuntu, Debian, Rocky and Alma, with exact product source `fc18221282f289088ec9b4282fda8e0bfc67a3b8`, versions 3.0.0/3.0.1, seven required checks and reviewed fixture hashes. All installation sandboxes retain the 600-second limit and normal distribution dependency network.
- Cache: **12** cases, including four independently checked same-inode/content metadata-read proofs. Both RPM barriers bind the same case serial, B mirrorlist before B-prefix exact-A payload, completed response and A provenance. Four cache directories were removed.
- Cleanup: reader and final receipts verified, zero errors, no remaining containers/networks. All recorded commands stayed within driver output/time bounds. Expected negative commands remain visible.
- Inherited attempt-1 results: Pages **567/0 skips**, four contracts; SDK **22/0 skips**, inputs **10/0 skips**; signed native success. These jobs were not rerun. Their full logs and original archive bindings are retained.

New artifact `12368334945` is 140382 bytes; SHA-256 `1be629819ad58afc52a9463de5088b0b477daab6494eef9c1406a9ab331b8af8` matches both GitHub metadata and the upload log. Every ZIP member's CRC and extracted SHA/size were checked. Its native receipt SHA is `25a650d6796a7d1766578bf3931c5cdb4d4868e670635dd918f5bc1056318e18`.

Attempt 1 still failed: Alma A baseline exited 124 at 600.218 seconds; zero scenarios passed, one failed, 48 were not reached. In attempt 2 that installation completed in 23.267 seconds, and all later scenarios ran. This distinguishes repeatability; it does not uniquely establish the first failure's network/root cause. All 71 original payloads, original manifest/archive and 12 initial attempt-2 snapshot files were verified unchanged. They were not overwritten to make the latest result look like a first-run pass.

One historical Standards process P2 remains: two original HTTP wiring unit tests followed their implementation. No production activation, live provider success, complete new D2 production-shaped native integration, or public release/origin acceptance is claimed. Existing production refusal remains unchanged.

`summary.json`, `source-binding.json`, proof JSON files and `verify-terminal-evidence.py` record the bounded verification. `public-allowlist.json` is the finite publication boundary; `final-manifest.json` and `public-archive-binding.json` bind the terminal archive. All collection processes terminated. The parent-level README remains the original in-progress snapshot; this subdirectory is the terminal report.
