# Spec

**No Spec findings: no missing requirement, incorrect implementation or scope creep identified.** Reviewed the three frozen working-copy files against `7255102de81b45c23d52e908ccdde1b7799ee3f5`; commit list and untracked-file list are empty.

The change satisfies failure-matrix lines 12–16: “path text is not cryptographic status,” hexadecimal `bad` in a valid key ID is allowed, unsigned digest-only output fails, and the exact package header is required. It strips only the actual path prefix, then recognizes the expected RSA/SHA256 signature row. This is a narrow repair, not a replacement verifier.

Failure-matrix lines 17–20 require nonzero exits and “NOKEY, NOT OK and BAD … on stdout or stderr” to fail, plus missing/duplicate/unsupported/wrong-candidate rows. Exit checking, explicit failing statuses, exactly one accepted signature row and candidate suffix binding remain. The upstream RSA/SHA256 packet check, isolated candidate-only key database, full hashed issuer comparison and exactly-one successful candidate are unchanged; an output key ID alone does not become signature authority.

I replayed the sealed original good/bad rpmkeys outputs and 13 bounded mutations through the frozen function: 15/15 passed. This was parser-only, with no native tool execution. Separately, the author's pinned-image native receipt binds the reviewed verifier and shows all eight real cases passing: identical bytes in five named paths, corrupted payload refusal, unsigned refusal and wrong-certificate refusal. This meets failure-matrix lines 22–25 without new signing or installation.

Recorded tests-first evidence shows 35 focused tests initially failed with 10 failures against the original verifier, then all 35 passed. Default suite: 587 tests, one existing local RPM-tool skip; four shell contracts passed. These results were inspected, not rerun.

All three source hashes matched before/after. No source changes, cloud calls, containers or network activity occurred during review. As required by failure-matrix lines 6–8, this fix does **not** establish the cause of the earlier unrecorded native failure.
