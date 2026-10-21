# Packages #71 — first CI acceptance

All first-generation automatic PR checks passed; no rerun or cancellation.

- Source: `fb62e03bb8ceaf300610becdc1f23ff41558c675`, base `51bfedbe88c78758fa05bfb87b13541b4bca4796`. Both actual job checkout logs show synthetic merge `915097dc67fffda99da2fac55f7def27e08e8c29`. GitHub commit APIs prove its tree equals the head tree `8bffb645f4db3a313c2049b022bed90376df4db8`.
- [Pages run 39572187767](https://github.com/WuKongIM/packages/actions/runs/39572187767), attempt 1: success, job completed 2026-10-21 08:51:23 UTC. Full Python suite 410 tests / 104.925 s, zero skips; separate contract suite 4 tests. Configure Pages, Upload Pages artifact and Deploy job all skipped.
- [Native run 39572188094](https://github.com/WuKongIM/packages/actions/runs/39572188094), attempt 1: success, job completed 08:54:52 UTC. All 22 job steps succeeded, including actual metadata 7 cases, local signing 10 cases, new bootstrap 6 cases, unsigned source build, full trusted-tool signed-repository build/verify/install, and tracked-tree mutation check. The full-drill success marker is at 08:54:29 UTC.

The new bootstrap artifact contains 30 executed commands and six passing cases: preview/default-vs-explicit reproducibility, stable reproducibility, APT both install orders plus reinstalls, RPM both install orders plus reinstalls. `scratch_removed=true`, exact source SHA matches actual checkout, package/file identities retained. These are real native unsigned data-only package checks in the pinned, network-disabled image. They do not prove signatures on these preparation packages, a live mirrorlist, stable composition/audit, CDN A/B/cache transitions or public exact-version installation. Stable remains disabled.

All three small artifact ZIP digests were recomputed in full and matched the GitHub API SHA-256; ZIP member CRCs and extracted JSON SHA-256 values were checked. Only the JSON and metadata/verification receipts are retained, not ZIPs, binaries or secret material. Local gh/API/blob reads had transient EOF errors; the read-only GitHub connector supplied complete decoded job logs. These were transport errors while reading evidence, not CI failures or reruns. Expected negative-test JSON printed after the Python suite is test-fixture output, not the native acceptance receipt.

`allowlist.json` and `archive-sha256.txt` list the exact public archive set. `summary.json` retains per-step conclusions, source identity, counts and limits. No external write other than the previously authorized normal push/PR creation occurred during monitoring.

At the authorized final body update (2026-10-21 08:58:24 UTC), REST confirms the exact head remained unchanged, draft=false, open and unmerged. Root owns the pending user merge approval; this agent did not merge.
