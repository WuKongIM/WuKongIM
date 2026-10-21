# PR #1024 generation 4 — first-attempt performance evidence

Exact head `f419ea8f82c8bd16a777cfc3bf6cbf8c85be60dd`, base `cee5d51f325fb84033060f546337d326110adae5`; actual PR checkout `0c575fd63bcf8164332ca28053039f8fdf84137f` has the same tree `a13367b2dc4658e6c27a1992f240544636e5178d`. All executing jobs independently show that checkout. Run **39670542860 / attempt 1** completed success: seven jobs succeeded and two conditional jobs skipped. The aggregate has no checkout and consumes same-run dependency outcomes. Baseline reuse was false (`performance_inputs_changed`). No old-head result substitutes for these runs.

The three fixed seams each ran a 60-second warmup and three measured 60-second windows. Their nine actual windows planned, started and completed **270,000** operations with zero errors/drops. Scheduled-to-completion P99 milliseconds:

- mixed SEND: 228.694186 / 161.767524 / 374.830034;
- channel append: 6.278279 / 7.767269 / 9.036604;
- TCP SENDACK: 178.189503 / 178.176045 / 178.223060.

All windows meet the unchanged 400 ms budget. P99 is not maximum latency and hardware variation is not controlled. The unit/race job retained 22 successful package-summary lines; nonverbose logs do not expose every internal subtest/skip.

The separate correctness smoke measured 90 seconds, with 2,500 online connections and **45,994 SEND/SENDACK** (511.044/s). Final sampled delivery is 840/840 with zero expiration/duplicate/conflict. Across warmup and measurement, final SEND/SENDACK is 76,002/76,002; retry_attempts and transport_rejected are **one**, not zero. Current queues and sessions converged to zero; engine cleanup_error_count=0. Final engine `operator_stop/operator_requested` is the bounded smoke's planned stop, not a two-hour or long-duration qualification. No independent post-shell runner-wide PID inventory is provided by this workflow.

Unlike prior generation no-trigger observations, mixed SEND **did trigger** its first-cohort early signal (six completions above 400 ms), exported a 6,898,669-byte trace and retained successful native trace-analysis text. `trace_complete=true`, `handlers_completed=true`, but `counters_complete=false`: all five retained cuts explicitly list missing `/sys/fs/cgroup/cpu.max`. Preserve this diagnostic limitation; the actual nine-window gate still passed. Raw trace/pprof binaries are excluded from this public allowlist.

Three bounded seam ZIPs were downloaded completely (largest 4,578,728 bytes, bounded at 8 MiB), matched API whole-ZIP SHA256 and passed all member CRCs. The 229,288,763-byte correctness ZIP was **range-read only**: 22 selected members, 57,327 downloaded range bytes, each selected member CRC/SHA plus evidence checksum verified; 14 safe members retained publicly. Its whole-ZIP SHA is API-declared only and whole-ZIP CRC was not recomputed. Original selected logs remain private; public output includes only bounded hash/aggregate scans.

Job log authentication-related lines are redacted with original line hashes. No raw credentials, temporary signed URLs, private ZIPs, binaries, actual AGENTS/FLOW files, provider operation, rerun, dispatch, cancellation or publication is included. Earlier-generation failures/count clarifications remain unchanged. Scripts/E2E candidate verdicts are separately archived in `../candidate`.
