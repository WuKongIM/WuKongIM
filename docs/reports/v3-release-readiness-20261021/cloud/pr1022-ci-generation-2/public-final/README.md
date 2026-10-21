# PR #1022 — first CI on b3fdb777 (generation 2)

Six natural first-attempt runs completed successfully. The exact candidate is `b3fdb777b9270128fe56086bc47562b321c2c947`, base `aeb45c2164b6a50214928f520edd2a8815b7a976`; actual product checkout is synthetic merge `8805fe2e09d4171ff761bfa4699859c6ba4fed17`. GitHub commit API confirms both candidate and merge have full tree `ae5a4175337f10f521a13a6c704c1b3040cf2bd2`. Complete sanitized job logs identify actual checkout output for every executing job. Signals and the aggregate status job do not check out product code.

- Candidate default: 219 tested packages pass, 15 without tests; frozen base-policy arguments, cwd and bounds match. The new 19-case replication regression is included in the complete exact-source package pass (0.711 s); nonverbose Linux logs do not contain individual subcase events. Local 19-case red/green evidence remains separate.
- Scripts integration: 649 root tests pass, 6 existing skips, 0 fail, 0 unfinished; all 15 downward-budget roots / 112 related pass events. Wrapper 582.670 s under the unchanged 600 s outer and 9-minute Go bounds. Exact cleanup zero.
- E2E: 5 migration topologies pass, 55 exact full-history checks, 0 temporary503 retries. Cross-node: 4 roots pass and one existing opt-in 2000QPS comparison skips. Self-chat: single-node and three-node clusters both pass, including invalid authentication, complete history and whole-cluster restart. Build and all three suites exited0 with bounded complete output and independently verified zero owned processes.
- Performance: tcp-sendack, mixed-send and channel-append each completed 3×30,000 scheduled requests at500QPS, zero errors/drops, all window P99 below400ms. Actual values are in performance-summary.json.
- Three-node correctness: clean final;75502 sends/ACKs, no terminal failures, losses, duplicates, corruption, sequence regressions, harness errors or underdelivery. Final queues/correlation clear. One retry and one transport rejection are retained rather than reported as zero.
- SDK: Android1.0.5, CocoaPods1.1.1 and Flutter1.1.0 all completed actual message round trips with the released Web peer and successful existing cleanup steps.
- Native: package/CLI, signed TEST ONLY metadata, installation and operator-owned lifecycle all pass on Ubuntu24.04, Debian12, Rocky9 and Alma9.

There are22 jobs:20 successful,2 expected non-PR diagnostic/nightly skips. API pages explicitly fit one bounded100-item page; total_count equals returned count. No run was retried, dispatched or canceled. No repository source or PR state was changed by this observer.

## Limits

Original generation1 failures and Mac timeout are retained unchanged. The historical TCP-refusal and per-voter cause remain unknown; the new E2E ran without a503 retry, so it does not establish that the NeedFrom branch executed in that run. Signed public stable publication and formal2h/72h cloud qualification remain separate.

Small artifacts were fully checked against API byte-size/SHA256 and ZIP CRC. The229MB correctness and77.9MB native archive were read only by authenticated bounded ranges (42,847 and509 bytes); selected entry CRC and local hashes were verified, not whole-archive SHA. No product binary, DEB/RPM, private key, raw authentication log or temporary signed URL is in the public archive. Raw fetched ZIPs/logs stay in the separate private directory. Existing SDK cleanup-step success is recorded without claiming an additional OS PID inventory.

## Archive

`public-final/` is the finite public copy. Files above64KiB are losslessly gzip-compressed; ARCHIVE_ALLOWLIST.json maps archived paths to original relative paths and both hashes. Run `python3 verify-public.py` inside the archive to verify every byte; optional `--restore-to /absolute/new-directory` reconstructs original filenames in a fresh directory for offline analysis. No network or workflow operation occurs. Original generation1 material is referenced, not rewritten.
