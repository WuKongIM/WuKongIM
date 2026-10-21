# Local shakeout at 0672c1159 — completed operator stop, warnings retained

The one native Mac run used clean source `0672c115950f65e6d92381aa5b3ea9f0a20fc790` (tree `ad05497c99502a4d076bacb7dee150d10e939d28`), the committed local profile (2500 online, 500 SEND/s, burst1000), Go1.25.11 and three real product nodes. It was never rerun. No cloud action or threshold change occurred.

The unchanged `--stop-after 1800` timer begins at coordinator process startup. Wrapper start 07:35:26Z; measured start 07:36:51.219782Z; final end 08:06:04.333745Z; wrapper finished08:06:11Z. Reported monotonic measured duration is1753.104288875 seconds. Raw helper and wrapper exit130; terminal native verdict is `operator_stop/operator_requested`, never formal `pass`. This satisfies the documented bounded local helper outcome; it is not a complete 1800 measured-second test, two-hour rehearsal, Linux formal run, 72-hour soak, capacity staircase or recovery qualification.

Final SEND=SENDACK=876529; RECV=RECVACK=1099924. First-attempt failures, terminal sends, receive ACK failures, losses, duplicate/corrupt/sequence-regression observations, harness failures and underdelivery are0. Preserve retry2, transport_rejected2, planned cancellation134, generation_stop2500, and natural session expiry1515. Correlation sampled10758/delivered10758 with no pending/outstanding; this is the recorded sampling boundary, not every recipient's independent proof.

Hot SENDACK:810725 samples,102 above400ms, maximum663.132291ms. Fixed buckets place cumulative P99 in(100,200]ms and P99.9 in(200,400]ms; these are interval bounds, not exact quantiles. Native latency warnings/anomalies are0. Cold/reheat/full-sync histograms remain in the full report (reheat sample count0).

Final resource reducer:351 complete rounds,0 missing,0 sustained CPU/memory/service-or-worker queue saturation events. One isolated CPU-high sample is retained. The separate15s observer has116 measured cuts per endpoint; observed CPU range roughly22–69%, memory61–64%. Four exporters are on the same physical Mac. Only one exporter provides some disk0 IOPS/throughput cuts; utilization/service-time/read-write-split are unavailable. Short observation cannot establish six-hour heap or24-hour goroutine growth. Post-run free disk was about77.6%, above the unchanged5% reserve.

All12 recorded process identities exited, SID94726 was empty, and ports15000–15063 closed. No raw-token match in the bounded scan and no panic/fatal log match. However, **product WARNs are present**:93 pre-final node1 presence-plan route-not-ready warnings, plus shutdown warnings including6 component-stop timeouts. Read `warning-classification.md`; native final cleanup0 does not prove clean shutdown of each product component. These warnings are not relabeled harmless and no post-hoc gate is introduced.

- `summary.json`: derived numbers, per-node canonical log counts, resource/correctness/latency bounds and limitations.
- `run/report/final.json` and `.md`: native terminal report; final JSON SHA256 `b4ae55c54ea4fa41d4cc1d2fb5874c1e55f0621e9f5336a676cd30ecdd92da76`.
- `run/evidence/identity.tsv`, `preflight.json`, `command.json`: source/config/binary/toolchain/command bindings.
- `result.json`, `cleanup-verification.json`, `credential-scan.json`, `observation-summary.json`: independent orchestration/cleanup/scan status.
- `run/logs/`, `observations/`, `progress.jsonl`: original5s worker cuts, bounded15s host and5s process captures (startup/shutdown missing samples preserved).
- `warning-source-freeze.json`: exact source/rule file hashes read for the offline warning classification.
- `ARCHIVE_ALLOWLIST` and `SHA256SUMS`: frozen small-text archive. Binaries, databases, worker data and caches remain outside the archive.

Offline projection can be repeated with `python3 summarize-run.py --root <complete-original-run-root> --output <new-summary.json>`. It refuses overwrites and rehashes both actual binaries, so **the complete retained original run, including run/bin, is required**. The small archive alone is sufficient to inspect/checksum all recorded reports/logs/receipts, but not to rerun that binary hash verification. The script does not start any process or load. Post-run disk space is an observation taken at projection time and may differ if reprojection occurs later.

Historical100SEND/s failure and two local scripts gate timeouts remain separately sealed; this successful workload run does not erase them. Linux scripts gate status is tracked separately by the parent task.
