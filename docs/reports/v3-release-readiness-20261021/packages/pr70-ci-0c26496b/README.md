# Packages #70 first-run CI acceptance

As of2026-10-21 08:28Z, PR#70 remains open draft and unmerged. Head0c26496b624058db813aa1206a7910f0abae6b13, base8b3ae9a08b943e51aa614779a58baab204c584ca. Actual synthetic checkout619b132efe71c8ea1f055a1731382a6be7d27d11 has the identical tree2c4f7d3a9b24a6f67c18b114b8a5c5f1f5f6fa5e.

Both automatic first runs passed: Pages39568724449 completed08:23:54Z with400 full Python tests and3 separate workflow contracts; Configure, Upload and Deploy Pages were skipped. Native39568724312 completed08:27:57Z with7 metadata cases and10 local-input signing cases. All five positive signing paths independently verified after temporary secret removal; all five negative cases passed. The subsequent complete unsigned package build, signed repository build/sign/verify/install and tracked-tree check actually passed. Native log contains `native-package signed repository container validation passed`.

Existing native workflow uses contents:read and no production signing secrets. Only ephemeral TEST ONLY private keys are used; the artifact reports production_keys_used:false and published:false. Stable certificates registered by this change are public inputs. No release, signing-secret creation or deployment was performed. No retry, dispatch, cancellation, comment or merge was performed by this observer.

Only two small JSON artifacts were downloaded. Their complete ZIP digests equal GitHub API digests and member CRC/SHA/size receipts are retained; no product package artifact was downloaded. Occasional GitHub read EOFs did not trigger workflow retries.

Suggested PR-body CI line: `On exact head0c26496b, first automatic runs passed400 Python tests +3 workflow contracts,7 native metadata cases,10 local-input signing cases with independent public verification after secret cleanup, and the complete TEST ONLY signed APT/RPM repository build/verify/install. Actual PR checkout tree equals head; all deployment steps skipped.`
