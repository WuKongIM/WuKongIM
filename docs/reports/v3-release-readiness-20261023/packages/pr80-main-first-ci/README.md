# packages PR #80 first main push CI

PR80 merged at 2026-10-22T18:59:40Z with exact merge tip e0982275c158b5bd69520c42719ce6f833ee63bc. GitHub commit APIs prove its tree 05fe9cc3727ad49b41e8b197adc8eb0fb99e54d5 equals reviewed PR head b8fc6d5e384630ef1d4c0f00281a836d115456e8; both complete job logs and the SDK typed receipt bind the actual checkout to the merge tip.

The two automatic main-push workflows completed successfully on attempt 1. Pages passed 595 tests in 129.762 s, zero skips, plus four repository contracts. Configure Pages, artifact upload and deployment were deliberately skipped; no Pages artifact was produced. Official SDK contracts passed 49 tests in 1.298 s and input regressions passed 18 tests in 3.967 s, zero skips. The SDK typed receipt records zero errors/failures/skips and provider_network_allowed=false.

The one 1682-byte public SDK artifact ZIP was fully downloaded, matched against GitHub API size/SHA-256, CRC-verified and its sole member hashed. Full job logs and complete bounded API enumerations are retained. Earlier generation 1 and generation 2 manifests and every retained member revalidated unchanged.

No dispatch, retry, source mutation, merge, permission, cloud, NAS or key operation was performed by this collector. Native tests did not run again on main push because that workflow has no push trigger; prior native evidence remains separate, with exact source-tree equality. This is not production stable publication or v3 GA acceptance.
