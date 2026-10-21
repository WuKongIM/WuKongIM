# Spec review evidence

Review scope: base/HEAD `de43579a9fa88bf70f64e6dcd926a2ddf6057496`; `git diff HEAD`; no commits in scope; no untracked files. Four files only. Patch SHA256 `d46227c5c27e57f4588704e4d9114609881a36f2f29e4fc37fcf9fef6ff43ec6`. Author source-freeze SHA256 `551a8954bdaabc7817a8cd41f6a3f0658dc85e9c89c79b81488bcaaea2a11877` independently verified. Source unchanged after probe.

## Specification and implementation mapping

- Diagnosis README line 11 binds the canonical mirrorlist URL to cache hash `5702a8a51b8fa885`; line 13 says “仅豁免上述 canonical 精确父路径 + 五名集合 + lstat socket”, without diagnostic file:// hash or broad regex. Repair failure matrix last paragraph adds `nlink=1` and `size=0`. Driver lines 39–42 and 469–474 match all predicates conjunctively. Exact canonical SHA calculation and native-path receipt independently checked. No requirement to enforce socket permission bits was specified; lstat actual socket type is the condition.
- Diagnosis README line 13 says “普通文件同名仍按 regular/nlink/size 检查” and keeps limits. Driver lines 475–486 retain old ordinary-file checks, bytes/count budgets, content hashing and timestamp arming. Exception has exactly five possible relative paths. Five same-name regular files are covered in shipping tests; independent probe confirms ordinary hashing and oversized rejection, FIFO/symlink/hardlinks rejected without reading.
- Repair matrix rows 2, 8–9 forbid fake metadata/reuse and require actual read proof. `cache_evidence` lines 490–501 is AST-identical to base. Independent live/closed sockets-only probes fail metadata gate; no-read proof fails; real OS read succeeds; altered device/inode/SHA/size each fails. Socket under metadata-looking wrong path is rejected. Socket lstat fields are unchanged by arm.
- Diagnosis README line 13 requires bounded escaped relative stat diagnostics. Driver lines 464–467/475–476 supply relative repr[:240], mode, nlink and size. Independent FIFO probes with newline and long paths retain escaped bounded diagnostics, no absolute root/raw newline.
- Diagnosis README line 13 keeps metadata proof, client commands/signature checks and 49 cases. Full AST comparison shows only `cache_inventory` changed; other 23 functions/classes unchanged, including client assembly, cache_evidence, exercise and owned cleanup. Forty protected files byte-match base (including 12 producer entries in author scope). No delete/kill/connect path added to inventory.
- Diagnosis README lines 15, 17 and 19 correctly separate diagnostic removal/agent termination, unavailable original CI file stat, file:// empty-repository mechanism replay and preserved failed probe history. Full Linux native 49 and cache-repeat are not proved by this change or this review.

## Validation boundary

`probe.py` is independently written for this review and only performs local AF_UNIX/filesystem and static identity probes. `python3 -B probe.py` completed with 95 assertions passing, 0 skips. Count includes 40 protected-file identity checks and source/scope checks, not 95 independent end-to-end cases. Actual macOS socket hardlink creation succeeded and nlink=2 was rejected. No shipping tests or source edited; no full suite, Docker, cloud, SSH, Actions, network, signing or secrets used.

Author receipts read, not re-executed: 10 added tests first red (4 failure + 4 error), then 10 pass/0 skip; 41 related pass; 528 full Python pass/1 existing tool-availability skip; 4 contracts pass; diff-check pass. Full 49 remains pending. These are author receipts, distinct from the independent probe.

## Frozen sources

- `source-freeze.json` records exact four file hashes and the independently checked base/protected identities.
- `probe-results.json` records all 95 assertions.
- `reviewed.patch` is the exact reviewed diff.
- `author-validation-summary.json` preserves author result boundaries.
- `spec-inputs/` preserves the local originating specification and path proof.

## Four reviewed file SHA256

| File | SHA256 |
| --- | --- |
| CHANGELOG.md | b7ff304f312363305f0514710e15c145ad02ede241b8dd62c9c75eefe4a66be0 |
| docs/stable-audit-transport.md | e5becbfad61fc9455caf746875c5b18f2d53787d00679ab62c8b5dde596fffa1 |
| tests/native_stable_clients_drill.py | 18a0d098f9cb9203e12b58180e64b8691708d0032adfa1ab463f7a1295c23f4e |
| tests/test_native_stable_clients_drill.py | f8b1a8e128453c34b8a054a01bb342f43a6f6d97be2dd33c44b430dd1a80890a |
