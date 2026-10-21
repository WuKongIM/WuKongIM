# PR #1024 — performance CI generation 3

Exact head `44de9452d94edae16129b5781d794f8c1c6c7c57`, base `cee5d51f325fb84033060f546337d326110adae5`, attempt 1. [Run 39664872153](https://github.com/WuKongIM/WuKongIM/actions/runs/39664872153) completed **success**, 7 successful jobs / 2 conditional skips. Actual checkout `c1a832afe71a46294a2e46054e26ea8568f6c315` is logged by all six source-consuming jobs and has exactly head tree `b69a44af04f20143b690e7a3860cc45caa95a0f4`; the aggregate job has no checkout. Product object and workflow equality is recorded separately. Baseline reuse was false (`performance_inputs_changed`), so these are fresh measurements for this head.

## Actual results

Each seam ran 60 seconds warmup followed by three 60-second measurement windows at 500 requests/s. All nine windows planned, started and completed 30,000 requests each: 270,000 total, 0 errors and 0 drops. Scheduled-to-completion P99 in ms:

| Seam | Window 1 | Window 2 | Window 3 |
| --- | ---: | ---: | ---: |
| mixed-send | 32.549273 | 20.541515 | 15.454831 |
| channel-append | 7.971909 | 9.160240 | 10.809948 |
| tcp-sendack | 178.335572 | 178.251687 | 178.314672 |

The three-node correctness step measured 90 seconds, 2,500 online connections, 46,002 SEND/ACK (511.133333/s). Whole-run cumulative totals include warmup: 76,002 SEND/ACK; sampled delivery 840/840; expired/loss/duplicate/corruption/sequence-regression counters all zero. Retry and transport-rejection counters were zero at qualification and final cuts. Final pending/outstanding/work/retry/inflight/session counts were zero; three product queue rows converged to queue/inflight zero. Local step outcome was clean/complete; the final engine outcome remained operator_stop/operator_requested after the planned bounded step, with zero cleanup errors. No independent post-shell OS PID inventory is available, so session/queue convergence is not runner-wide zero-process proof.

Unit/race checks and the integration arrival-saturation command actually ran and passed (22 successful package result lines across three commands). The nightly direct qualification and fixed Linux diagnosis jobs were skipped by workflow conditions. Mixed flight was `not_triggered`, trace_bytes=0; this is not a successful diagnostic capture. Nonverbose unit/race output cannot enumerate all inner skips. P99 is not maximum latency, and hosted CI is not a controlled same-hardware comparison or a long-duration release qualification. The adjacent candidate run still failed its original scripts time limit.

## Integrity and bounded acquisition

The three small seam artifacts were fully downloaded with API SHA256 and all-member CRC verification. Correctness artifact 12375564745 is 230,016,155 bytes; only 56,968 bytes of ZIP ranges were downloaded to inspect the inventory and 22 selected members. All selected member CRC/SHA checks passed; 14 public members are retained, while raw application/coordinator logs are represented only by scan results and hashes. **The complete correctness ZIP SHA256/CRC was not recomputed**; its whole-archive digest is API-declared only. Six selected node/service logs had zero matches in the bounded WARN/ERROR/panic scan; this does not substitute for unselected diagnostics.

`final-source-and-jobs.json`, `performance-window-summary.json`, `correctness-summary.json`, `flight-summary.json`, and `correctness-members-integrity.json` contain exact facts and limits. The first local summarizer encountered `KeyError: exit_code` because its assumption did not match an artifact result schema; that observer error is retained in `summary-first-observer-error.json`, followed by the corrected successful summary command. It was not a CI failure or rerun.

The finite public allowlist excludes raw ZIPs/profiles, authentication material, temporary signed URL queries, private acquisition stderr, binaries, and executable-named AGENTS/FLOW files. No dispatch/rerun/cancel, source/PR mutation, cloud/provider access or new workload was performed.
