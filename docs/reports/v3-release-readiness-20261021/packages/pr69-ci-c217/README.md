# PR #69 c217 first-run CI acceptance

Candidate `c21736edcf67bac4b5c1c495d1cd18490b1d154a`. Actual PR synthetic checkout `14bf24c9df5e8a0f8e18db8f333fc149d3653bfa` has the identical tree `651ed9cad773ea6368b38d9b77634541d10f1780`.

- Native run39566618368 attempt1 succeeded 2026-10-21T08:10:18Z. Actual local-input signing JSON has 10/10 passing cases, all five positive paths have independent public verification, secret tree removed first, source unchanged. Preceding source metadata JSON has all 7 cases passing. Full unsigned package build and signed repository build/verify/install actually executed; log records `native-package signed repository container validation passed` at08:09:47Z.
- Pages run39566618393 attempt1 succeeded08:07:08Z. Full Python discovery387 tests and separate workflow3 tests passed. Configure/upload/deploy Pages were skipped as intended for this PR.

No workflow retry, cancellation, dispatch, comment or publication was performed. Read downloads had transient EOF; subsequent successful reads used the same artifact IDs. Small JSON ZIP SHA256 was recomputed and members read with ZIP CRC checks; receipts contain member SHA/size. No package artifact was downloaded. First failed CI generation remains unchanged in pr69-native-failure; this success belongs only to the new source.

Suggested PR-body replacement: `CI verified on c21736e: 387 Python tests plus 3 workflow contracts; real metadata7/7 and local-input signing10/10, with independent verification after secret cleanup; complete TEST ONLY signed APT/RPM repository build and install passed. Both first-run workflows succeeded; deployment skipped.`
