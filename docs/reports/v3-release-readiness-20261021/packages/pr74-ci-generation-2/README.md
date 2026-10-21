# PR #74 — generation 2 CI audit

Frozen head `4ba39668c892ca17eb33b165afe993ccc683161f`; base `335cd2569d2b62de577916ed86c097fd3a22547e`. Actual PR checkout `0186483a5a8574c1e521252c26773dd4d7d18690` and head share complete tree `95e50d2549e51436d8a9ef76ab5bb004bca36ef6`. API and actual checkout logs are retained.

| First automatic run, attempt 1 | Actual result |
| --- | --- |
| [Pages 39598091032](https://github.com/WuKongIM/packages/actions/runs/39598091032) | Success: 489 tests, no skips; 4 contract tests; deployment skipped. |
| [Native 39598091049](https://github.com/WuKongIM/packages/actions/runs/39598091049) | Failure: original signed repository job succeeded; new stable four-client job failed. |
| [Original native job 119215337918](https://github.com/WuKongIM/packages/actions/runs/39598091049/job/119215337918) | 7 source + 10 local signing + 6 bootstrap + 31 candidate + 8 transport cases passed; full signed repository build/sign/verify/install passed. |
| [Stable client job 119215338064](https://github.com/WuKongIM/packages/actions/runs/39598091049/job/119215338064) | Image preparation passed. One of 49 planned composite cases started; zero composite cases passed. Failed at baseline-A-four-download-and-installed-cli. |

Ubuntu 24.04 completed download (exit 0) and the original installed CLI acceptance command (exit 0). The following Debian 13 download exited 100: its stable bootstrap installed, but apt-get update reported OpenSSL `unexpected eof while reading` for Release. The HTTPS server recorded status 200 before body completion. Exact TLS root cause is not established by this read-only audit. The standalone Ubuntu installed receipt was inside the subsequently removed validator scratch and is not retained; its actual command and exit are retained in the public driver receipt.

A separate observed gap: Ubuntu requested SHA512 by-hash, received 404, and fell back to mutable Packages.gz. Download success is not successful by-hash acceptance. A/B switching, cache reuse, negative cases, and four complete installed-client gates were not reached. No old-head success substitutes for this result.

The actual TEST ONLY product build used source `fc18221282f289088ec9b4282fda8e0bfc67a3b8`, tree `3254faa77819a1dc7c4b7230b567ec1115be5a7a`, Go 1.25.11 linux/amd64. A/B are genuine builds with TEST ONLY versions 3.0.0/3.0.1 and bootstrap 1.1.1/1.1.2. Per-binary digests and recipe are in the receipt. Fixture secret removal is true. Cleanup verified zero remaining containers/networks, with no cleanup errors, after removing five exact-run containers and one network. Cache exercise did not start.

The first failed merge-commit GET (EOF) and empty stdout remain alongside successful read 2. That transport read failure is not a workflow failure. No dispatch, retry, cancellation, source modification, publication or external resource mutation was performed for this audit. Stable remains unverified for release; public-origin and source-provenance flags are false.

Evidence: `summary.json`, `first-failure.json`, full job logs, five artifact receipts and HTTPS JSONL. Every full artifact ZIP was temporarily downloaded, its digest recomputed against GitHub's API digest and member CRC checked; only public members remain. The 13 MB original native log is preserved locally and archived losslessly as deterministic gzip (round-trip SHA verified in `full-log-encoding.json`). The raw log is the only evidence file excluded from the bounded archive; compressed bytes contain its complete content. `ARCHIVE_ALLOWLIST.txt` lists the evidence payload; `archive-sha256.json` and `SHA256SUMS` bind its bytes. Archive those three manifest files as well, without recursive self-hashing. Generation 1 remains unchanged.
