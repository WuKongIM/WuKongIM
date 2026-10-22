# Object publication — independently completed phase 1

This is a partial, default-disabled preparation slice, not a complete production
publisher. No commit, push, PR, CI, cloud call/write, resource permission, DNS/TLS
change, production key, Keychain or NAS access occurred. Local loopback TLS keys
were generated only in owned temporary directories and are not archived.

## Source and scope

- Worktree: `/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/packages-object-publication`
- Branch: `codex/object-publication`
- Base: `7255102de81b45c23d52e908ccdde1b7799ee3f5`
- Reviewed plan manifest: `acc0877bd3c3bd2f17ad121fce7dce2ea2813c569dc347dcdb6628299e894bd5`
- Patch: `phase1.patch`, SHA-256 `ce7f8e4974bfed061e15f5d180229f5077ba4728d076c38c7bc40dd02e4184a9`
- Exact 8-file source inventory and digests: `source-freeze.json`; governing
  instructions and applicability: `source-rules.json`.

The fixed CLI and manifest refuse upload/activate before input materialization.
All unknown operation/target/resource/config fields stay null. Even an enabled
manifest is rejected because composition is not implemented. No existing
Provider, planner, materializer, source/native helper, client verifier, Pages,
trust or protected agent file changed.

The new helper delegates to original fresh authentication; it never replaces
Git, provenance, attestation or native verification. Tests copy reviewed scripts
into real tiny Git repositories. The clean case reaches a canonical GitHub HTTP
transport fixture and then intentionally fails a main mismatch; dirty/changed
HEAD cases fail before HTTP. None is a production authentication positive.

The same bounded curl probe verifies actual TLS, canonical Host/SNI, exact escaped
URI/query, full bytes/hash/cache, error certificate/SAN/CA, redirect rejection,
body/header limits and total deadline. Curl children and listeners are checked
for cleanup. Fixture results explicitly remain TEST ONLY and publication false.
Production response observations also do not claim complete public publication.

## Test-first evidence and diagnosis

`failure-inventory.md` was frozen before any production implementation. Its later
O1–O12 upload/activate inventory is not implemented or tested in this phase. All
phase-1 test sources were frozen in `red-freeze.json` while the implementation and
manifest were absent. `red-tests/` reproduces exactly those SHA-256 bytes; the
integration source was reconstructed by reversing only the documented fixture
corrections and verified against its pre-implementation hash.

- First RED: fast 9 tests / 9 missing-module errors; integration 7 tests / 8
  missing-module errors, including TLS class setup. The 10 TLS methods existed
  but could not run before the module existed. Original logs/JSON are preserved.
- First implementation: fast 9/9 passed; integration 16/17 passed. The clean Git
  case correctly exposed that its macOS temporary path traversed `/var` symlink,
  so the original checked-path guard refused before GitHub.
- `git-first-failure-diagnosis.log` records the exact original guard failure.
  Fixture root was changed to `.resolve()`; no production verifier was changed.
  The temporary Git environment was also explicitly isolated from global/system
  config, templates and hooks, with commit/tag signing disabled. Final explicit
  integration run passed 17/17, no skips.
- The extra module-style related command had one pre-existing flat-import loader
  error (`test_stable_seal_inputs`) while its 9 new fast cases passed. This is
  recorded as an invalid invocation, not a product failure or passing old suite.
  The repository's correct full discovery command covers the original suite.
- Original `tests/contract.sh` passed, including 4 nested tests;
  `scripts/validate-control.py` passed. `git diff --check` passed.
- Full default-suite terminal result is recorded in `validation-summary.json`
  and `full-default-first.log`; no failure was retried or hidden.

## Repeatable commands

```sh
python3 -B -m unittest discover -s tests -p test_object_storage_publication.py -v
python3 -B tests/provider_sdk/test_object_storage_publication.py --receipt /tmp/object-publication-contract-receipt.json
./tests/contract.sh
python3 -B scripts/validate-control.py
python3 -B -m unittest discover -s tests -p 'test_*.py' -v
```

Real Git/TLS tests are only in the explicit `tests/provider_sdk/` suite, outside
unit discovery. The existing read-only provider workflow runs it explicitly,
rejects zero/skip success through its runner, and retains its finite JSON receipt.
No new dependency install is needed for this phase (local Python/curl/openssl).

## Deferred handoff

Stop at this freeze until root provides M2b's accepted/merged exact source. Then
update the base, write corresponding positive/negative SDK orchestration tests
before implementation, and preserve the two-operation contract: each invocation
fresh→prepare independently; activate full-GETs objects, never PUTs them; previous
only binds observations within that activation. Old upload evidence is not
permission for a later control commit. SDK native fixtures never establish real
GitHub provenance or production authorization.

Real source/candidate/audit identities, independently expected role session ARNs,
resources/permissions, TLS/DNS and publication authorization remain external.
No new approval platform or credential issuer was introduced. The existing
unapproved resource proposal and accepted Mac custody were not changed.
