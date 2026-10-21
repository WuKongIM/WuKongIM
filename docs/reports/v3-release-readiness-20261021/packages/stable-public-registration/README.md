# Stable public certificate registration review

Uncommitted nine-file change in packages-stable-public-keys, based on c21736e. One sole public role declaration, the two exact approved public certificates, a narrow validator integration and documentation/tests. It does not sign or publish, creates no secret or Environment, and keeps stable disabled.

The existing public certificate validator checks both stable certificates under the same30/45/180-day and RPM RSA policy. Full and shortened16/8-hex identifiers are unique across all four channel/family identities. Completely absent registration supports historical controls; every partial/unknown/linked registration is rejected. The same preview signing return value is preserved.

Tests were written first:13 tests initially24 assertion failures/1 error, then13 pass. Existing30 control tests first had one expected-key-list assertion failure (the original test disables preview, not registered stable public keys); its expectation and name were made explicit, then30 pass. Related103 tests (including the13) covering composition, archive, verification and transport planning pass. Existing contract.sh including3 workflow tests passes. Exact first-run logs remain unchanged.

Protected control/preview certificate/composer/archive/verifier/signer/workflow hashes are individually unchanged in protected-after.json. Only public-production input copies and their public receipt were read; no Mac private directory or Keychain was accessed. Existing tests generate only temporary TEST ONLY keys. No commit, push, PR, CI dispatch or publication has been performed. Independent review is pending.

See implementation.patch and implementation-summary.json for complete source freeze, test results and public provenance.

## Final reviewed local commit

Root completed independent review and independently passed the real CLI; see root-review.md. The nine-file change was committed and replayed once onto merged main8b3ae9a. Final local head0c26496b624058db813aa1206a7910f0abae6b13 is clean and unpushed. Before/after full trees and patch bytes are identical; all19 protected hashes remain unchanged. rebase-verification.json records the exact identities. Initial implementation-summary.json intentionally preserves its pre-commit state.
