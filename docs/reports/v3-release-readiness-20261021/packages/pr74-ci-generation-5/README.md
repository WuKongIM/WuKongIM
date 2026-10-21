# Packages PR #74 — generation 5, first CI attempt

This archive preserves the natural first PR CI run for head `de43579a9fa88bf70f64e6dcd926a2ddf6057496`, reviewed tree `8748c88e1c8c4e8f5370581ed42e282f5ef60cfc`. The synthetic checkout `fcbf46ae1286ca6cc2892d5d5f91f83c65187533` has the same tree. No workflow was retried or dispatched, no source was edited, and no production resource was changed by this observer.

Pages run 39616623261 passed 518 Python tests on Linux with no skip and four publication-control tests. Its PR deployment job was skipped. Native run 39616623120 passed the existing signed APT/RPM repository job and all original receipts (7 source metadata, 10 local signing, 6 bootstrap, 31 candidate and 8 transport cases).

The new stable job failed at case 24, `rocky-linux-9-cache-A-first`: 24 of 49 cases executed, 23 passed, one failed, 25 not reached. The preceding real DNF command 165 exited 0, verified the downloaded product signature, and all nine serial-25 mirror/metadata/payload responses were 200 with completed server writes. Command 167 then exited 1 in `cache_inventory`: `cache contains an unsupported file`. This combined guard checks regular-file status, link count == 1, and a 64 MiB size bound. The receipt does not identify the rejected path or stat values, so the exact predicate/root cause is not established by this run. Do not count RPM cache persistence or repeated-read atime verification as passed.

The repaired Rocky A-to-B case 23 still passed. Both A=3.0.0 and B=3.0.1 installed-CLI baselines passed on all four distributions with exact TEST ONLY product source fc18221282f289088ec9b4282fda8e0bfc67a3b8. Ubuntu and Debian repeated-cache receipts contain matching device/inode/SHA/size with atime advanced after epoch arming. Later Rocky and Alma cases were not reached.

Original cleanup receipts show verified reader shutdown, three verified cache removals, and final zero labeled containers/networks with no errors. TEST ONLY public-origin, publication, and provenance flags remain false; this is not a production release acceptance.

Start with `summary.json`, `identity-proof-final.json`, and `first-failure.json`. Original receipts are under `stable-artifact/` and `artifacts/`. The original 13 MB legacy job log is losslessly available as `legacy-native-job.log.gz`, with the uncompressed SHA/size in `legacy-native-log-excerpts.json`. The uncompressed duplicate is excluded from the allowlist.

GitHub read failures are retained separately from CI failures. Public read diagnostics redact URL query strings; any raw sensitive stderr is restricted to the sibling local private observer directory (mode 0700, files 0600), which is excluded. The initial GraphQL EOF is an observed tool-output record, not an invented raw capture. No previous generation payload was changed. `ARCHIVE_ALLOWLIST.txt`, `SHA256SUMS`, and `archive-sha256.json` define the bounded public archive.
