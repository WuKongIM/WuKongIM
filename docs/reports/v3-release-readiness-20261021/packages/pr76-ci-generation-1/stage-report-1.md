# PR #76 first Linux CI — bounded stage report

Observed 2026-10-21T17:37:02.374631+00:00. First attempts only, fixed head `dded26afa12188d5f1c036af4b9c06c3cbddb46d` / base `1829c3813d6b21dcfeb1238a640604a4691815bd`. Actual checkout is PR merge `3e4b7945206faffc18d6023f2b50311d93bd4348`, whose parents exactly bind this base/head and whose tree `e319ee72e8d30f012fd5e6206d131e2b75c47a09` equals the reviewed head tree.

Verified public completed evidence:

- Pages run 39644526518: success. Linux default **567 passed / 0 skipped**, contract **4 passed**. Production deployment job skipped by PR policy.
- SDK run 39644526769: success. Actual Python 3.12.3 hash-locked install, SDK **22 passed / 0 skipped**, focused inputs **10 passed**. Uploaded receipt matches checkout/tree/lock/test names/counts; artifact ZIP digest matches GitHub's recorded digest.
- Native run 39644526993 remains in progress. Public interim receipts show source metadata 7, local signing 10, bootstrap 6 and stable candidate 31 passing TEST ONLY cases. Their uploaded archive digests and available exact control-file hashes/source identities are verified.

Remaining required evidence: terminal signed APT/RPM repository job including real native verification/install, and stable four clients/A-B **all 49 cases**, exact native product/trusted source and cleanup receipt. No interim receipt or local result substitutes for those gates. Current stage is unsigned/signed repository job post-toolchain cleanup and stable49 exercising clients.

Only GET requests and local evidence writes occurred. No rerun, cancellation, push, merge, or production call. Original tests-after-code Standards P2 remains historical; no production activation or complete original-D/live publication claim. Monitoring continues with bounded intervals; final closure is pending.
