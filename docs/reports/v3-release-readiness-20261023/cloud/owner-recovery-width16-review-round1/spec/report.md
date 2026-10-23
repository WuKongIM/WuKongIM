# Spec axis review — width16 experimental candidate

Reviewed the clean exact head `4cb45b3409b155ca6a0eeb3d72d1b05bbee817ac` / tree `1f2826553a264053e954229ac2581bcf48afe6ad` against merge-base `9e5d8eeadb7b4c1998857640d2160c3af3232996`. One commit: `Tune bounded cold presence recovery concurrency`.

**Findings: 0.** No missing/partial implementation requirement, unrequested behavior, or incorrectly implemented requirement was found within this reversible experiment scope.

- Production admission changes are limited to 16 cohorts, 16×512 = 8,192 retained keys and the independent adapter gate of 16. The byte cap is explicitly `128 << 20`; the unchanged per-subset cap is `32 << 20`. This implements static-plan report lines 7–11 and 19 without silently deriving 512 MiB.
- The exported-path fixture holds actual owner I/O for 16 independent single-UID pages and separately 16 full 512-UID pages. It verifies the seventeenth waits/cancels, follower cancellation does not cancel its leader, capacity is reusable, and joined work finishes. The independent adapter fixture holds all 16 invocations despite child timeouts, then verifies seventeenth cancellation and fresh admission. These cover report lines 15–18 and failure-matrix lines 11–17.
- The real byte fixture retains four 512×65,535-byte pages plus 2,048 bytes, blocks the next byte while cohort/key capacity remains, and verifies admission after release. Existing quota proof-before-wake coverage is adapted rather than removed. Ownership/proof/follower logic outside the constants is unchanged (report lines 9, 15, 17–19).
- Page packing, owner/deadline/retry behavior, fences, transport `CancelRunning` policies, parser/report/verdict behavior remain outside the diff. FLOW/index/knowledge/CHANGELOG changes describe only the new bound (report lines 11 and 19).

The read-only review inspected recorded local validation summaries; it did not repeat tests. Behavioral efficacy remains unverified until the single controlled B experiment. This is neither a root-cause finding nor Linux/long-stability/GA acceptance (report lines 25–29). Pending accepted-message correlation remains the parent’s separate evidence gate; no PR, merge or B run is approved by this review.
