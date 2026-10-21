# D2 mechanical rebase onto accepted packages main

Replayed the single reviewed D2 commit `eae1afc593ad70f3259798bcb40e90b3c7878959` from parent `d824fe37918ee4ef22b1b1745bc02518812563e3` onto freshly fetched `origin/main` `1829c3813d6b21dcfeb1238a640604a4691815bd`.

- New tip: `dded26afa12188d5f1c036af4b9c06c3cbddb46d`
- New tree: `e319ee72e8d30f012fd5e6206d131e2b75c47a09`
- Sole parent: `1829c3813d6b21dcfeb1238a640604a4691815bd`
- Branch: `codex/stable-object-provider`; worktree clean; exactly one D2 commit ahead.

Relative to new main, the commit changes exactly the same eight D2 files. Seven complete files retain their reviewed v2 SHA-256 values. The sole conflict was CHANGELOG: resolution used the exact new-main file plus the unchanged D2 paragraph. Removing that paragraph reproduces new main byte-for-byte. The updated #75 final entry is retained; old-parent wording was not copied back. All other 116 tracked main files are unchanged.

`range-diff.txt` shows only the inherited CHANGELOG context difference. Compared with the old D2 tip, only main's four paths differ: CHANGELOG, stable-audit-transport documentation, native client drill and its tests. `old-tip-to-new-tip.diff` records these; `base-context.diff` records the corresponding parent change. Their raw hunk offsets differ because D2 adds a CHANGELOG paragraph, so an initial raw-byte patch equality audit failed before tests; `source-binding-attempt-1.json` retains that audit mistake. The corrected check proves exact inherited file content and unchanged D2 paragraph instead.

Current-head incremental validation: SDK **22 passed / zero skips**, input **10 passed**, and main-context native-client unit tests **44 passed**, each under a 120-second deadline. `git diff --check` passed; all processes ended and hashes remained stable. A full default rerun was unnecessary for this mechanical rebase: D2 code/tests/lock/workflow are byte-identical, and the isolated main test seam was rerun. The earlier default result remains **567 run = 566 pass + one existing RPM skip on the old base**, not a new-head full-suite claim. Linux CI has not been dispatched here.

Historical Standards P2 remains disclosed: two original HTTP wiring tests followed their implementation. No attempt was made to rewrite that history. The two Spec repairs and unconditional production activation refusal remain unchanged. No complete production-shaped v2/native/live acceptance is claimed.

No push, PR, reset, cloud call or production-key access occurred. Rebase commits used per-command signing disabled. Prior freezes/evidence were verified unchanged; this is a separate handoff directory. Parent owns any subsequent push/PR/CI. `source-freeze.json`, full `implementation.patch` and this directory's manifest bind the final result.
