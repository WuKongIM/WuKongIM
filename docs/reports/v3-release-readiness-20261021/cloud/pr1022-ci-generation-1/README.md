# PR #1022 — first natural CI generation

结论：本代不能按全绿交付。Candidate E2E 与 Rocky Linux 9 systemd lifecycle 两处失败均原样保留，未重跑、取消、派发、改源、转 ready 或合并。所有 6 条 workflow runs 已终态；scripts job/check API 仍有下述状态不一致，按父任务指示封存交接，未无限轮询。

## Source identity

- PR head: `9a4900dc67ac674791edba11ea9a719cd91ec570`
- Exact base: `aeb45c2164b6a50214928f520edd2a8815b7a976`
- Actual synthetic checkout: `8e8c0ab3321affc8655fb720a308e50bfaca15ca`
- Both Git trees: `9a199e729569efa88fed09ee9025c5720f1dbe55`
- All runs attempt 1. 17 checkout jobs have actual Git log SHA evidence; 2 signal jobs and the three-node aggregate do not checkout. Final PR read remains draft, open, unmerged, exact head/base unchanged.

## Actual results

| Run | Result and scope |
| --- | --- |
| [Candidate 39625524702](https://github.com/WuKongIM/WuKongIM/actions/runs/39625524702) | failure: default passed; scripts command passed; migration 1→1 failed; later messaging packages not run |
| [Three-node 39625524864](https://github.com/WuKongIM/WuKongIM/actions/runs/39625524864) | success: correctness, focused unit/race, all three PR performance profiles and aggregate; nightly/fixed-diagnosis branches skipped by event |
| [Released SDK 39625524550](https://github.com/WuKongIM/WuKongIM/actions/runs/39625524550) | success: Android Maven Central 1.0.5, iOS CocoaPods 1.1.1, Flutter pub.dev 1.1.0 on iOS; real run and cleanup steps succeeded |
| [Native preview 39625524544](https://github.com/WuKongIM/WuKongIM/actions/runs/39625524544) | failure: build, TEST ONLY signed-repository closure, four ordinary installs passed; Ubuntu/Debian/Alma lifecycle passed, Rocky lifecycle failed |
| [Signal 39625524596](https://github.com/WuKongIM/WuKongIM/actions/runs/39625524596) / [39625578509](https://github.com/WuKongIM/WuKongIM/actions/runs/39625578509) | success; two natural PR-event runs, no manual rerun |

Default: 219 tested packages, 15 without tests; 417.516s, exit 0. Scripts: original frozen-base policy command and limits unchanged, 499.409s, 1,493,329 output bytes, no truncation; 649 passing roots / 1,306 pass events, 6 existing opt-in skips. All 15 downward-budget roots and their 112 pass events ran. See `linux-downward-budget-tests.json`. A Linux pass does not erase the retained earlier Mac full-suite 9m timeout.

The four commands actually run under the candidate process wrapper (default, scripts, binary build, ordinary_history) all have `cleanup.verified=true`, `complete=true`, and empty `remaining_processes`. Cross-node and self-chat have explicit `not_run` and no execution/cleanup claims.

### Failure 1: migration connection refusal

[Job 119313591454](https://github.com/WuKongIM/WuKongIM/actions/runs/39625524702/job/119313591454), `TestOriginalOrdinaryHistoryTopology/1_source_nodes_to_1_target_nodes`, `ordinary_history_test.go:174`: after imported history (3 rows) passed, `dial tcp 127.0.0.1:27802: connect: connection refused`. First failed test event 15:35:16.874576226Z. Other four topologies passed. Command exit 1 after 102.556s, cleanup zero, no output truncation. Actual server SHA256 `e0c44e2cbc3f9456097183fabb00f60a35aed21bc56aa577c9240028cf9d66b0`.

Root cause is not established by these artifacts; do not label it a product or fixture cause without separate diagnosis. `first-failure.json`, `first-failure-excerpt.txt`, and artifact `12356928133` retain the exact evidence. Subsequent diagnosis/reproduction or fixes belong to a separate envelope, not this first-run record.

### Failure 2: Rocky bootstrap deadline

[Job 119318017429](https://github.com/WuKongIM/WuKongIM/actions/runs/39625524544/job/119318017429), `TestNativePackageLifecycle`, 306.17s: `package bootstrap and systemd did not reach running or degraded state within 300 seconds`. Logs show package-install stage, Rocky AppStream metadata (28 MB) taking 04:46 at 100 kB/s, then dependency transaction still running. systemd was not PID 1 and the product service lifecycle had not begun.

This is evidence of bootstrap/dependency preparation exhausting its existing deadline; it does not prove an underlying mirror/network fault or product-runtime failure, nor success under a longer timeout. See `native-first-failure.json`, `native-first-failure-excerpt.log`, and the complete job log. Existing SDK/native workflows have cleanup steps/logs but no separate exact zero-inventory receipts; no such receipt is invented here.

### Scripts API inconsistency

Job/check `119309607829` still reports `in_progress / null` in the last API observations, despite all 11 steps including Complete job being success, full log availability, and a passed command/cleanup artifact. The candidate run is terminal failure because of E2E. Keep the API state distinct from the command result. Root may add one subsequent read in a separate follow-up envelope; do not rerun this CI or overwrite this generation. See `scripts-job-direct.json`, `candidate-jobs-cache-bounded.json`, `scripts-job-full-log.log` and their read receipts.

## Additional evidence and limits

Three PR performance profiles each actually measured 180 seconds / 90,000 completions, zero error/drop; window p99 ms: tcp-sendack 178.044/178.103/178.065, channel-append 13.710/6.470/10.103, mixed-send 17.112/21.317/23.526. Correctness smoke has 2,500 online clients, 90 measured seconds, 46,000 measured SEND/ACK; final total 76,002 includes warmup, zero correctness/harness failures, queues and outstanding correlations drained.

Manager was not triggered because the 27 changed paths do not match its filter. These PR checks do not prove a 2h cloud rehearsal, 72h soak, same-data capacity, 30m recovery, or signed native public publication. Paid request state and cloud resources were untouched.

Small artifact ZIPs were fully checked against GitHub API digest/size and ZIP CRC. The 230,318,380-byte correctness ZIP was read only by bounded ranges: 42,653 bytes fetched, inventory plus 6 selected public JSON members CRC-verified. The 77,866,290-byte native ZIP was likewise read only for its inventory/checksums (508 bytes fetched). Large ZIP API digests are declarations, not locally recomputed whole-archive digests. No product binaries, databases, profiles, private key material or signed artifact URLs are in the public allowlist.

Public complete job logs are sanitized; removed authentication-related lines have original hashes in receipts. Raw downloaded ZIPs/logs and raw stderr remain outside this archive in sibling `pr1022-ci-private` (directory 0700/files 0600). `ARCHIVE_ALLOWLIST.txt`, `SHA256SUMS`, and `archive-sha256.json` bind the public payload. No local exec session or background collector remains active.

The reusable read-only collectors are `read-snapshot.py`, `collect-completed.py`, and `read-selected-artifact.py`. Copy collectors into a new follow-up directory before any additional reads; this generation is frozen.
