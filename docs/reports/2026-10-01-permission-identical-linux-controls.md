# Permission performance controls — 2026-10-01

The unchanged native acceptance procedure rejects an identical old binary in 8 of 12 sequential comparisons and 5 of 12 burst comparisons. This establishes variability in this shared-host experiment; it does not establish that the candidate is healthy, identify the cause of that variability, or waive any failed candidate row. The original candidate still has six sequential failures, and the rejected gzip experiment still has nine. No product runtime change is delivered here.

## Fixed experiments

The A/A plan was saved before the first run. Both legacy `old` and `new` filenames use the same frozen old binary (`424d03eb298b972ec572261d617972eecb5523c5`, SHA-256 `24f08c976e52784003bd467635d0a87b1173bcd4ff4e63f3002d6fd0adc844db`) and `TestPermissionCallerBaseline`. CPU calibration ran first and passed: getrusage 51.559ms versus native probe 50.809583ms. The original driver is `2a4a1f4e57a8cb3d5586f10bb1585717c37ea0ab`; its seven source fingerprints and unchanged acceptance verifier are retained.

Three predetermined A/A pairs preserve all four placements, 32 independent devices, 64 sequential SENDs, two joined 32-caller burst waves, 32 ban and 32 unban controls, and exactly 161 committed history entries per placement. Each cluster has 256 Hash Slots, 12 physical Slots, three message voters and one metadata voter. The original 20ms full-metrics sampler and whole-node native user+system CPU cuts remain. No padding, profiles, timeline, extra stage families, retries or replacement observations were used. All 24 placement/control cases and 48 SEND windows passed their functional assertions.

The Linux diagnostic is one predetermined old→candidate pair on Linux ARM64 Docker Desktop, using the pinned existing `golang:1.25.11-bookworm` image `sha256:63cacf247cfd45aa03d105a9a86e2811f978514997be98068979ba6e4c2d0534`, four-CPU quota, 4GiB memory and container loopback networking. Old revision is the same `424d03e`; candidate is clean `028702ed0c03d72f7566951732fcb2f8b74f4749`, whose product runtime is unchanged from `c03fa6cc1712e886d9359822e23be716fdb4534e`. The original driver source is unchanged; Linux binaries use Go 1.25.11, ARM64, CGO disabled. The full fixture and 20ms full-metrics sampler remain. Six fixed histogram families are retained only in the existing boundary scrapes on both sides, adding no scrape. All eight placement/control cases and 16 SEND windows passed.

The original CPU helper is Darwin-specific, so this Linux diagnostic does **not** measure a native whole-node CPU integral and cannot pass the original combined p99/CPU gate. Platform, storage and quota differ. It is neither Linux AMD64 capacity qualification nor a substitute for the existing 500 SEND/s gates. Both owned containers exited and were removed; all 18 A/A node PIDs had exited. An unrelated pre-existing container and all other worktrees were preserved.

## Identical-binary results

The +5% rule is unchanged and applies independently to both p99 and CPU in every comparison. At 64 SENDs, the reported p99 is the maximum ACK latency. CPU includes all three nodes, background work and cut/sampling overhead. Percentages are derived from complete retained observations.

| Pair | Placement | Sequential p99 old→same binary (ms) | p99 change | CPU change | Within both limits |
| --- | --- | --- | --- | --- | --- |
| 1 | same-slot-remote | 15.837→17.867 | +12.82% | +5.82% | no |
| 1 | two-slots-one-remote-leader | 27.498→27.149 | -1.27% | +1.62% | yes |
| 1 | two-remote-leaders | 12.872→16.642 | +29.29% | +0.08% | no |
| 1 | two-slots-local-leader | 42.772→43.035 | +0.61% | +16.90% | no |
| 2 | same-slot-remote | 16.850→20.140 | +19.53% | +0.70% | no |
| 2 | two-slots-one-remote-leader | 29.757→35.951 | +20.82% | -14.90% | no |
| 2 | two-remote-leaders | 17.405→14.182 | -18.52% | -30.77% | yes |
| 2 | two-slots-local-leader | 27.740→24.633 | -11.20% | -27.63% | yes |
| 3 | same-slot-remote | 43.475→15.630 | -64.05% | -19.77% | yes |
| 3 | two-slots-one-remote-leader | 27.195→27.061 | -0.49% | +16.09% | no |
| 3 | two-remote-leaders | 15.731→18.237 | +15.93% | -1.40% | no |
| 3 | two-slots-local-leader | 49.344→31.575 | -36.01% | +10.52% | no |

| Pair | Placement | Burst p99 change | Burst CPU change | Within both limits |
| --- | --- | --- | --- | --- |
| 1 | same-slot-remote | -7.17% | -2.33% | yes |
| 1 | two-slots-one-remote-leader | -8.44% | -0.88% | yes |
| 1 | two-remote-leaders | +2.04% | -0.89% | yes |
| 1 | two-slots-local-leader | +9.46% | +13.72% | no |
| 2 | same-slot-remote | +13.97% | +37.45% | no |
| 2 | two-slots-one-remote-leader | +12.30% | -0.62% | no |
| 2 | two-remote-leaders | +3.43% | -20.48% | yes |
| 2 | two-slots-local-leader | +14.89% | +59.39% | no |
| 3 | same-slot-remote | -1.00% | +9.72% | no |
| 3 | two-slots-one-remote-leader | +1.36% | -0.08% | yes |
| 3 | two-remote-leaders | +2.97% | +3.61% | yes |
| 3 | two-slots-local-leader | +2.08% | +3.13% | yes |

The original acceptance verifier exits **1**, with exactly eight sequential and five burst rejections. Its byte-identical replay is an expected evidence result; it is not a passing product gate. Most sequential request medians stay near 12ms, while tails and whole-node CPU still vary. Identity rejection is evidence that this experiment cannot attribute every +5% breach to a source change; it is not proof that all prior candidate regressions are measurement noise.

## Linux completed-population observations

| Placement | Sequential p99 old→candidate (ms) | Change | ACK median old→candidate (ms) | Burst p99 old→candidate (ms) |
| --- | --- | --- | --- | --- |
| same-slot-remote | 19.249→14.455 | -24.91% | 9.934→10.030 | 199.454→17.487 |
| two-slots-one-remote-leader | 13.409→13.946 | +4.00% | 9.771→10.389 | 192.014→17.192 |
| two-remote-leaders | 13.418→14.887 | +10.95% | 10.384→9.927 | 179.888→14.412 |
| two-slots-local-leader | 12.651→14.784 | +16.86% | 9.137→9.530 | 170.546→17.595 |

Two of four sequential p99 changes exceed +5%: two remote leaders (+10.95%) and local leader (+16.86%). The same remote leader row is +4.00%, within the p99 limit. Burst p99 drops by 89.68–91.99%, and candidate envelopes/barriers decrease as the original cohort assertions require. Sequential envelopes, Slot groups and fresh-barrier counts stay unchanged. These are observations from one fixed pair, with no native CPU qualification.

| Placement | Sequential RPC mean old→candidate (µs) | RPC completed count per side | Append wait mean old→candidate (ms) | Append completed count per side |
| --- | --- | --- | --- | --- |
| same-slot-remote | 229.77→179.34 | 64 | 4.076→3.542 | 64 |
| two-slots-one-remote-leader | 231.39→205.78 | 64 | 3.710→4.026 | 64 |
| two-remote-leaders | 202.40→203.13 | 128 | 3.902→3.879 | 64 |
| two-slots-local-leader | absent (local envelope) | 0 | 3.397→3.731 | 64 |

These means use each histogram’s own completed count. They are not request-aligned spans and must not be added together or subtracted from ACK latency. RPC timing excludes waiting before RPC stage entry. Bursts also reduce append operation counts through batching, so their append means cannot be interpreted as one span per SEND. Missing series remain explicit in the raw cuts.

## Hypotheses and remaining work

1. **Shared experiment variability:** the identical-binary rejection prediction was observed. Cold WAL/storage, sampling and host scheduling remain possible contributors; A/A alone cannot choose among them. Previously retained request/WAL traces identify rotation and sync delays for their selected requests, without proving the cause of these new windows.
2. **A mandatory 1ms sequential collection delay:** candidate source already contains the idle one/two-distinct-fact synchronous path. The ordinary 64 sequential requests have 64 cohorts and unchanged fact/barrier counts. The completed RPC means show no uniform candidate increase. This evidence does not support adding another idle fast path, and it does not exclude all waiting outside the measured stages.
3. **Sampling CPU tied to longer windows:** full-metrics sample counts remain in every raw window and grow with elapsed duration. Whole-node CPU and request duration cannot be treated as independent here. No sampled CPU is subtracted and no CPU total is normalized to change the gate.
4. **Different WAL rotation positions:** existing traced examples support a possible storage contribution, but this Linux pair has no WAL trace. The hypothesis remains unconfirmed for these specific windows.

A useful next bounded probe is precise whole-process CPU measurement on Linux with the same functional matrix and an identical-binary control, preserving its independent diagnostic status. A repair must then satisfy the original unprofiled p99/CPU procedure and applicable 500 SEND/s gates. No failure is replaced, and no durability, quorum, Hash Slot, metadata freshness or ownership bound is weakened.

## Evidence and replay

The companion archive contains all six A/A reports with raw CPU cuts and ACKs, both full Linux reports with stage cuts and exact histories, execution plans/logs, build identities, frozen instructions, the seven original driver sources, the candidate cohort source, retained prior failed verdicts, cleanup receipts and an offline verifier. Product/driver binaries are retained separately in the local persistent artifact directory; their hashes and build records are public in the archive.

Extract the companion archive into a new directory and run `python3 verify-controls.py`. This verifies manifest hashes, source/binary identities, the original failed A/A verdict, exact functional/history records and the derived Linux summary without starting processes or rebuilding products. Nine rehashed semantic mutations are required to fail, including missing ACK/history/stage data, PID identity reuse, fabricated Linux CPU, changed binary/threshold and unearned qualification.

Original failed acceptance and rejected gzip/WAL evidence remain in [Draft #995](https://github.com/WuKongIM/WuKongIM/pull/995). Its automatic non-skipped checks passed at `9f2de632087e3c6707a448992f4c1a99a9d38437`; nightly/fixed Linux diagnosis were skipped. Those checks do not waive the separately failed native sequential acceptance.
