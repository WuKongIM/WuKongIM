# Permission observer-only diagnosis v2: pre-code failure contract

This isolated driver starts from `cfd715c5476f8b793fef25b62b85943528164fa4`.
The original driver, original observer contract and v1 failed run are preserved.
This is diagnostic-only server encoding work measured as the complete owned
three-node CPU integral, including background work; subtract nothing. It never
changes product or fixed-load +5% p99/CPU thresholds or waives historical failures.

## Evidence motivating this driver correction

V1 failed at
`tmp/issue-977-observer-root-20261001/observer-abba.json`, SHA-256
`9bfda7d0da76a85a4530edfbcdd878320ba897f645122bdcacaf01fb6b904b53`.
The four blocks retained 200, 1, 116 and 129 responses. Only the first completed.
Full client parsing breached a 20ms cadence slot; the 64MiB block budget also
rejected valid full identity responses. These unequal counts cannot support a
CPU comparison. The old report/log/raw bodies remain failed evidence.

Observed complete identity bodies were 581,043–581,107 bytes; compressed bodies
were about 31–33KiB. Exactly 200 times 581,107 bytes is 116,221,400 bytes
(110.84MiB). Set a predeclared 256MiB block wire budget, over twice that observed
complete workload, while retaining the original 8MiB individual body guard.
No body, family or response count may be reduced to meet the budget.

The fixed-load AA1 c32 report supplies a second design correction before any
v2 execution: UTC started `14:06:41.509391` and finished `14:07:13.254857`,
only 31.745466s apart, while Go monotonic elapsed time and the native raw span
were 32s. A roughly 255ms wall-clock rollback can make UTC-only CPU-cut bounds
fail even when the actual monotonic cut is timely. The correction below changes
the clock basis, not the strict 20ms bound or any product/fixed-load threshold.

## Unchanged fixed experiment

- One clean frozen old product at `424d03eb298b972ec572261d617972eecb5523c5`,
  one real three-node cluster, 256 Hash Slots, 12 physical Slots, one metadata
  voter and three message voters; GOMAXPROCS=4 per node.
- One offline group history reader and one system UID client. Exactly one
  successful warm SEND materializes the metrics; no SEND enters any CPU block.
- Fixed gzip, identity, identity, gzip order. Exactly 200 complete GET /metrics
  responses at 20ms absolute cadence in each four-second CPU window.
- Same fresh-connection HTTP transport, request shape and server cluster in all
  blocks; only explicit Accept-Encoding differs. No implicit decompression,
  HTTP retry, filtering, caching or catch-up.
- Original three owned Darwin PID CPU cuts and calibration helper. The outer
  runner must calibrate first. Preserve process identity, raw counters, timebase
  and query bounds; four-second window closing lateness >=20ms fails.
- Preserve the original CPUQuery helper and C fixture. Surround each call with
  unmodified `time.Now()` values that retain Go's monotonic component. Before
  gaps are acquisition begin minus outer call start/finish; after gaps are
  outer call start/finish minus the scheduled monotonic deadline. Both outer
  call durations and all four signed monotonic gaps must be nonnegative and
  strictly below 20ms. Missing bounds, reversed monotonic order or any value
  >=20ms invalidate the cut. A fast timer wakeup cannot excuse a late result.
- Independently preserve/check native `after.End - before.Begin` and
  `after.Begin - before.End` spans in CLOCK_MONOTONIC nanoseconds. Both must be
  at least 4s and strictly below 4s+40ms, the outer envelope derived from the
  unchanged <20ms bound on each side, not an extra 40ms allowance per cut.
  Native order, process identity, counters and timebase validation remain
  mandatory. Actual combined cut validity requires monotonic and native bounds.
- Preserve every previous signed UTC gap/duration, outer call UTC timestamps,
  UTC window duration, and signed differences between matching UTC/monotonic
  bounds. Report UTC-bound validity and any UTC/monotonic inconsistency without
  clamping or concealing negative values. UTC rollback is explicit evidence;
  it does not replace the independent monotonic/native qualification basis.
- `CPUValid` remains false until the original interval helper returns after
  validating process identity, raw counters and timebase, and both actual cut
  bounds pass. Deferred raw analysis after a fatal query/interval failure must
  preserve all acquired bodies while keeping the block incomplete. Block
  completion requires `CPUValid` and valid actual before/after bounds in
  addition to all 200 complete responses; no partial CPU interval is qualified.
- Single network request timeout remains 250ms; encoded and decoded body
  limits remain 8MiB. A missed absolute network slot or >=20ms start lateness
  fails with partial evidence. The last response must finish before window end.

## Separate acquisition from artifact analysis

- Inside each CPU window perform only fresh GET, complete bounded raw entity
  body read, necessary status/encoding/media/cache headers and wire-budget
  checks, and capture schedule/network bounds. Do not hash, gzip-decode, parse
  expfmt metrics or write raw files inside the measured window.
- A complete network read determines whether the next absolute slot was met.
  Explicitly retain network_finished and fetch_finished bounds.
- After CPUAfter, hash every retained raw response, completely decode gzip
  (including CRC), hash logical bytes, and parse/validate every metric family
  with expfmt. Keep family/metric/sample totals, not huge parsed JSON objects.
- Record validation start/finish and raw retention start/finish separately.
  A block cannot complete until all 200 responses have complete network reads,
  valid complete bodies, full parsed family coverage and retained raw files.
  Any analysis or retention failure stays failed; validate/preserve the rest.
- Retain at most 256MiB wire bytes per block in memory, then release each body
  after analysis/retention before the next block. Known Content-Length must
  fit individual/remaining budget; unknown overflow retains a bounded prefix
  marked truncated and failed. Do not retry or hide missing bytes as full bodies.
- Keep raw entity bytes beside WK_E2E_PERMISSION_OBSERVER_REPORT in .raw/.
  Header fields are separate and HTTP transfer framing is excluded. Native
  cut failures still preserve acquired bytes and an explicitly failed receipt.
- Final Product HTTP sync must prove exactly the warm message and More=0;
  final stable topology must match. Existing suite owns process cleanup.

## Unexecuted UTC-only contract history

The following pre-code wording was saved in this document before the clock
basis correction. It governed no v2 measurement and is superseded only as a
clock basis; the same strict 20ms actual-cut limit remains in force:
the prior unexecuted source SHA-256 was
`39f41cd2e999436d2ced21cded2c8360b1e75e13f08a60390a37d02ffc064253`;
the prior contract SHA-256 was
`8732906dbe10c5d7c746b01d9cae2dc2e41921040d58f8f06ad13e049c671c59`.

> Before the v2 run, enforce actual UTC query bounds as well as timer wakeup.
> Record signed `StartedAt - CPUBefore.QueryStartedAt` and
> `StartedAt - CPUBefore.QueryFinishedAt` gaps, both query durations, and signed
> `CPUAfter.QueryStartedAt - ScheduledFinishedAt` and
> `CPUAfter.QueryFinishedAt - ScheduledFinishedAt` gaps. Every gap and duration
> must be nonnegative and strictly below 20ms. Missing query timestamps,
> negative values, reversed wall-clock order or any value >=20ms invalidate the
> cut; preserve their signed values without clamping. Report before/after and
> combined cut-bound validity explicitly. A fast timer wakeup cannot excuse a
> late CPU query result.

## Invocation and edit boundary

Select `TestPermissionObserverCostDiagnosticV2` with
`WK_E2E_PERMISSION_OBSERVER_V2=1`, the original WK_E2E_BINARY and calibrated
WK_E2E_PERMISSION_CPU_PROBE, and a new absolute
WK_E2E_PERMISSION_OBSERVER_REPORT. Use -count=1 -timeout=4m -p=1 -v.
Only permission_observer_cost_test.go and this new v2 contract are edited.
No build/test/commit/push in this editing subtask; the coordinating task owns
compilation and bounded process execution after the fixed-load run stops.

## Frozen instruction inputs before editing

- `AGENTS.md`: SHA-256 `6df6655788a5dc019c78c330728aee6a21e7183ebbec9f2583d7f318a26df2df`.
- `test/e2e/AGENTS.md`: SHA-256 `b3a9e0fed6019fc741f7829b9917d4ce288c7108a9207cdcb718e07238797505`.
- `test/e2e/message/AGENTS.md`: SHA-256 `8ea7d9df983c7c16d637cee48c4e1069ac3a008fa95f4d8f2235bd30ed1842b1`.
- `test/e2e/message/send_ban/AGENTS.md`: SHA-256 `1c5de355e9eca7a931ebfb87c8008d0e7a7b3a199919f88926aea1a7fb3ddaa6`.
- `test/e2e/suite/FLOW.md`: SHA-256 `8101e4379711cc2700416ae801461de9bc2cb3570053278cd0bdf0a94a9d3d14`.
