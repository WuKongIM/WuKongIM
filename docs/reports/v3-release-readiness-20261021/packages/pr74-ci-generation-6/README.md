# Packages PR #74 — generation 6, first CI attempt

The natural first PR runs for head `983e567f155a982c45ca066e4dc67d33851f0d24` passed. The reviewed tree is `7a498c42b4f5b0e6f86a524878fde651d7301371`; the actual synthetic checkout `1861383911303229fba4cd65eb75019ff26f6a32` has that same tree. This observer performed read-only GitHub/source access and offline receipt verification, without workflow retry, dispatch, cancellation, source or PR edits, or production writes.

Pages run 39623787268 passed 528 Python tests on Linux with zero skips and four publication-control tests. The PR deploy job was skipped. Native run 39623787283 passed the original signed repository installation and its raw receipts: 7 source metadata, 10 local signing, 6 bootstrap, 31 candidate and 8 transport cases.

The new stable job passed all 49 real native cases. Both TEST ONLY versions A=3.0.0 and B=3.0.1 installed on Ubuntu, Debian, Rocky and Alma, with all required installed-CLI checks and exact product source fc18221282f289088ec9b4282fda8e0bfc67a3b8.

Rocky cases 24/25/26 and Alma cases 35/36/37 passed first-A cache creation, repeated-A cache reuse, and exact-B targeting. Each first RPM inventory contains 14 regular files. Each repeated-A receipt has five metadata reads with unchanged device/inode/size/SHA and advanced atime after epoch arming: primary.xml.gz, repomd.xml, repomd.xml.asc, solv and solvx. The three raw repodata file identities are independently matched against completed HTTPS A responses. Refresh still performs signed metadata requests; cache reuse does not mean no network traffic. Four cache roots, reader containers, and final owned resources were cleaned, with zero remaining containers/networks and no cleanup errors.

The archive also preserves SHA512 by-hash fault delivery, both real RPM A-to-B retained payload cases, wrong-signature/mirror/TLS/transport rejections, exact-target rejection, conditional cache behavior, and three actual installed-CLI negative cases. `verify-receipts.py` can be rerun offline with `python3` to assert these raw-receipt facts; its result is `receipt-verification.json`.

This is TEST ONLY fixture acceptance. Published, public-origin verification and source-provenance verification remain false. It neither merges PR #74 nor establishes a production stable release.

Start with `summary.json`, `identity-proof-final.json`, `receipt-verification.json`, and `cache-repair-closure.json`. Raw receipts are under `stable-artifact/` and `artifacts/`. The large legacy job log is losslessly compressed as `legacy-native-job.log.gz`; its original SHA/size are in `legacy-native-log-excerpts.json`. The uncompressed duplicate is excluded from the public allowlist.

Public diagnostics redact URL queries and tokens. The sibling private observer directory (mode 0700, raw stderr files 0600) is excluded. No prior generation payload was modified. `ARCHIVE_ALLOWLIST.txt`, `SHA256SUMS`, `CRC32SUMS`, and `archive-sha256.json` define the finite public archive.
