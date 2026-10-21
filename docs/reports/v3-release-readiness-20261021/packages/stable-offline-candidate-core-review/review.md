# Stable offline candidate — final core static review

**No new blocking static findings** in final-freeze-v1, base `0cd625cbee214927a81b0a1015d825a6f53fbf28`. This is a core code review, not a native execution or release approval. The actual 31-case Linux TEST ONLY drill remains unverified at this review point.

## Identity and scope

The independent SHA/size comparison matched all 16 frozen files, the supplied patch, and the live worktree. All 30 protected files independently matched their exact base Git blobs, including channel/public-key manifests, production publication workflows, signer, key validator, canonical archiver and preview transport planner. The detailed identity file records per-file values and applicable rule digests.

Content review covers the five plan/prepare/compose/audit/verify scripts, README/CHANGELOG/release contract, and five new structural test files. Root owns the native drill, its unit harness and workflow review; their bytes were verified here but that content review was not duplicated. No source edits or test/GPG/build/native/cloud executions were performed by this reviewer.

## Closed early findings

- **R1:** `prepare` retains its boolean `candidate`; both preexisting Path loops now use `inventory_path`. The early regression and later twelve preview prepare / twenty-nine preview verifier successes were read. AST inspection confirms no `for candidate` target remains in `prepare`.
- **R2:** the shared output guard rejects every existing linked/special ancestor before mkdir or staging, resolves inputs/outputs and rejects containment in either direction. Prepare applies it before creating directories and after normalizing the three outputs; composer and audit output also invoke it. The source/control alias test first failed twice (including a real control-tree write), then all four prepare contracts passed and explicitly asserted no builder call or nested output.
- Malformed candidate tar originally leaked `ArchiveError`; the final audit wrapper converts that failure into `AuditReceiptError`. The saved first-red and four-test green establish this error boundary without relaxing archive validation.

## Core checks

Explicit stable plus offline-candidate flags select the new branch. Preview retains its original schema validation; no schema-driven fallback, channel enabling, cloud upload or old publisher adaptation was introduced. Candidate plan/snapshot/audit identities remain distinct and audit/base/package Release IDs remain null. First generation rejects a base, retained/removed versions, history and rotation. The production source branch binds seven original assets and the full existing preflight evidence; TEST ONLY has a separate fixture schema, two packages and null source/control GitHub identity.

Plan reconstruction uses an independently supplied external plan and exact public control/source/local producer byte hashes. The production `control_sha` is accurately documented as an operator-declared revision, not proof of a Git tree or current GitHub provenance. Candidate local modules load from the producer checkout rather than the control data directory. Source/control bytes are rechecked before exporting results.

Composer reuses the existing family-tree closure checks, binds exact current signer receipt identities, copies the public audit inputs itself, exports the separate stable bootstrap paths and marks status/index/installer as an unpublished candidate. It uses exclusive final directory export for candidates. The independent verifier requires an external plan, compares embedded plan/control/source evidence, closes the archive and site trees, verifies actual current-key SHA256 APT detached and cleartext signatures, and verifies actual RPM package and repomd signatures using isolated candidate public-key databases. Package headers, reviewed bootstrap contents/download alias bytes, APT indexes/gzip/by-hash and RPM compressed/open metadata closure remain checked. It does not accept self-reported receipt fingerprints as cryptographic proof.

Audit creation/verification reconstructs from the externally supplied plan/control/source/family receipts, extracts through the unchanged canonical archiver, compares exact archived copies, calls the independent verifier, and rehashes the original archive. The receipt name binds strict version and actual snapshot digest; no nonexistent audit Release ID is invented. Future sealing is explicitly an outer binding to unchanged candidate archive bytes, not filling null IDs inside the archive.

## Existing validation and remaining boundary

The author's final local log ran **446 Python tests in 103.844 seconds: OK, one existing native RPM skip**. Publication-control validation and four contract tests passed; the local validation receipt records diff-check success. The tests include expected failure-output fixtures, so their printed `passed:false` JSON snippets do not mean the unittest run failed. These are read receipts, not reruns by this reviewer and not substitutes for the real native drill.

The freeze has not yet demonstrated the 31-case native end-to-end path in Linux. Stable remains disabled and unsealed; production signing, immutable audit sealing, OSS/CDN activation, public client verification and final v3 release readiness are outside this change's completed claims.
