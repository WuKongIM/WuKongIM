# Native drill review refinement — before implementation

Preserve v1 freeze and root-review-v1.json. The first focused red run is review-refinement-first-red.log: 12 tests, 4 failed subcases and 4 errors.

Failures to prevent:
- Exit 2 usage, exit 1 traceback, missing files and a different validation reason must never count as the intended rejection. Require exit 1 and the exact controlled CLI prefix plus a case-specific bounded reason, with no other output. Record only declared reason category and raw output hashes.
- Source resolution, public control and embedded archive plan mutations must remain canonical valid JSON, with only one leaf changed; parser errors do not cover anchors.
- Candidate signing receipt closure currently precedes crypto. Keep original wrong-issuer replacements as receipt-integrity cases. Add separate current-key and wrong-family crypto cases with only archived APT signature facts rebound, leaving external original receipts unchanged. Invoke the existing full verifier and its actual GPG implementation, not a new verifier.
- Renamed signed-repomd mutation covers signing-receipt integrity only; it does not exercise native open-checksum semantics. Existing shared validator tests cover that separate mechanism.
- The default transport descriptor must have its complete existing shape, preserving null fixture IDs; stable scope rejection is the intended reason. No fabricated GitHub provenance.

No native/Linux result is claimed by these local harness tests. No core CLI, production config, key custody, pins or permissions change.
