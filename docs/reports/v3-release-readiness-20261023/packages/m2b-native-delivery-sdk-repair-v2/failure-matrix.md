# M2b review repair v2: tests before code

Existing seven-file scope, no production Provider change required. Original93-file archive and hashes remain immutable. Review3 P2 items are the approved seams.

1. Public evidence: actual SDK HTTP adapter responses, not fake backend state. PUT/GET record source/object SHA/size/cache, status/outcome, streamed byte digest/count/EOF/errors; config before/after and returned batch IDs; all refresh submitted URLs and actual Tasks/TotalCount, including missing/duplicate responses and submission/query timeout. No Authorization, signed URL, SecurityToken, request secret or arbitrary error text. Per-object binding records current/retained/source context. Original8MiB receipt cap remains.
2. Network: explicit one-attempt guard self-test, then zero unexpected connect attempts for the complete SDK stage. An attempt swallowed by refused() or converted to ProviderError must still fail. A simultaneous stage error remains primary; the guard result is independent. No actual socket reaches the OS.
3. Cleanup: stage error plus rmtree PermissionError retains both; cleanup-only error fails; removal no-op also fails. Every path writes a bounded incomplete receipt and returns nonzero; cleanup must not mask source-stage type/location or already recorded effects. Successful cleanup/zero unexpected network still permits only TEST ONLY fixture_complete.

New unit/failure regressions precede implementation. Full native→SDK20 is rerun after repair, with existing SDK46/input13 assertions preserved except adding required success receipt fields. These failure-composition tests patch only sdk_cases/rmtree at their explicit failure boundary; no native verifier success is mocked for the20-case result.
