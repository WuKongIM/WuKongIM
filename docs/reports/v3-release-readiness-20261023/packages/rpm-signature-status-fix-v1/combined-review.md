## Standards

## Standards

No mandatory violations or actionable Fowler-baseline findings in the three-file working-copy diff against `7255102de81b45c23d52e908ccdde1b7799ee3f5`. No branch commits, staged-only changes or untracked files are omitted.

`scripts/verify-production-package-site.py:408` keeps the repair inside the existing candidate-only verifier and adds an English explanation of the path/status boundary. The exact header, single RSA/SHA256 status row and candidate-ID comparison preserve the isolated-candidate responsibility required by `docs/release-contract.md` (“Signing”). No key topology, signer, publisher or channel control changes are introduced.

`tests/test_verify_production_package_site.py:970` uses one focused result helper and six readable failure-oriented methods. Recorded RED source hashes match the unchanged base implementation, while the RED test hash matches the frozen final test file; this supports root `AGENTS.md`’s tests-before-code requirement. The author’s 35-case green and eight-case real Linux signed-fixture result bind the frozen verifier. These were inspected, not rerun by this reviewer; the default suite remains outside this review’s completion claim.

`CHANGELOG.md:5` supplies the required concise operator-facing entry. All twelve Fowler heuristics were considered, with repository rules taking priority; no unnecessary abstraction or worthwhile extraction was identified. Tool-enforced checks are not repeated as findings.

No package-local `AGENTS.md`, `FLOW.md` or `CONTEXT.md` exists. All three source hashes match before and after review. Spec reports were not read, and no source edits or cloud operations were performed. This isolated defect does not establish the cause of the earlier unrecorded native failure.

Summary: 0 mandatory violations; 0 heuristic findings.

## Spec

# Spec

**No Spec findings: no missing requirement, incorrect implementation or scope creep identified.** Reviewed the three frozen working-copy files against `7255102de81b45c23d52e908ccdde1b7799ee3f5`; commit list and untracked-file list are empty.

The change satisfies failure-matrix lines 12–16: “path text is not cryptographic status,” hexadecimal `bad` in a valid key ID is allowed, unsigned digest-only output fails, and the exact package header is required. It strips only the actual path prefix, then recognizes the expected RSA/SHA256 signature row. This is a narrow repair, not a replacement verifier.

Failure-matrix lines 17–20 require nonzero exits and “NOKEY, NOT OK and BAD … on stdout or stderr” to fail, plus missing/duplicate/unsupported/wrong-candidate rows. Exit checking, explicit failing statuses, exactly one accepted signature row and candidate suffix binding remain. The upstream RSA/SHA256 packet check, isolated candidate-only key database, full hashed issuer comparison and exactly-one successful candidate are unchanged; an output key ID alone does not become signature authority.

I replayed the sealed original good/bad rpmkeys outputs and 13 bounded mutations through the frozen function: 15/15 passed. This was parser-only, with no native tool execution. Separately, the author's pinned-image native receipt binds the reviewed verifier and shows all eight real cases passing: identical bytes in five named paths, corrupted payload refusal, unsigned refusal and wrong-certificate refusal. This meets failure-matrix lines 22–25 without new signing or installation.

Recorded tests-first evidence shows 35 focused tests initially failed with 10 failures against the original verifier, then all 35 passed. Default suite: 587 tests, one existing local RPM-tool skip; four shell contracts passed. These results were inspected, not rerun.

All three source hashes matched before/after. No source changes, cloud calls, containers or network activity occurred during review. As required by failure-matrix lines 6–8, this fix does **not** establish the cause of the earlier unrecorded native failure.

Standards: 0 new mandatory or heuristic findings. Spec: 0 open findings.
