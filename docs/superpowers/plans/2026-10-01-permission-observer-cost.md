# Permission observer-only compression diagnosis

This pre-code contract is recorded before adding the bounded process-level probe.
It is diagnostic-only and never qualifies or replaces product p99/CPU acceptance.

## Fixed inputs

- Exact frozen clean old product `424d03eb298b972ec572261d617972eecb5523c5`;
  driver base `2a4a1f4e57a8cb3d5586f10bb1585717c37ea0ab`.
- One real three-node cluster, 256 Hash Slots, 12 physical Slots, one metadata
  voter, three message voters, GOMAXPROCS=4 on every node. Manager placement
  chooses system-UID non-owner ingress and one source group on the same remote
  Slot, as the original baseline. One offline history reader, one system client,
  exactly one warm SEND before measurements, then zero SENDs during all blocks.
- Four blocks in fixed order: gzip, identity, identity, gzip. Every block
  schedules exactly 200 complete GET /metrics responses at absolute 20ms cadence
  across a fixed four-second window. Only Accept-Encoding changes between blocks.
  Both modes share one transport, HTTP request headers and parser.
- Three owned node PIDs use the original native Darwin CPU cuts/helpers. The
  outer runner must execute the original CPU calibration first with the exact
  same probe binary. Preserve raw counters, timebase and interval identities.
- Every response keeps headers, scheduled/start/network-finish/processed-finish
  times, lateness, HTTP entity-body bytes before Content-Encoding decoding,
  byte counts, wire SHA-256, decoded-body SHA-256 and full-parse family/metric/
  sample counts. Full raw bodies permit independent family replay.
  Transfer framing is excluded from the retained entity body; headers are separate.
  Raw response files live beside WK_E2E_PERMISSION_OBSERVER_REPORT in .raw/.
  Retain at most 64MiB encoded body bytes per block in memory. Persist only
  after that block's native CPU after-cut, then clear retained bytes before
  the next block. Known Content-Length exceeding the remaining budget fails
  before reading; an unknown-length overrun preserves a bounded failure prefix.
  No raw-file write enters any measured CPU window.

## Failure contract

- Missing/dirty/wrong old source identity, missing native probe, or non-Darwin
  host fail before cluster measurement. Driver, original helper and instruction
  hashes are retained; there are no private product imports.
- 250ms maximum per request and 8MiB maximum encoded or decoded body. Status
  must be 200; gzip is explicitly requested and manually decoded/CRC-checked;
  identity must have no compression. No retry, filter, cache or partial parser.
- Expfmt must parse the complete body, validate every family/metric type and
  retain complete raw bytes plus family/metric/sample totals. Required warm
  permission/Go families must exist; no parsed full sample objects enter JSON.
- Any missing cadence slot, start lateness >=20ms, request crossing the next
  slot after decode/validation, or request failure marks the
  block failed with retained partial evidence. Do not catch up or replace data.
  Still preserve its four-second wall/CPU cut and continue the remaining fixed
  blocks if the process/CPU evidence remains available.
- A completed block requires exactly 200 successful responses; every block
  has fixed four-second wall duration; closing lateness >=20ms also fails.
  Native cut query overhead is separately
  recorded and not subtracted, and the three-node CPU integral remains scoped
  to that entire observer-only block.
- Final Product HTTP history must terminate with More=0 and contain exactly
  the single warm ID/sequence; final topology fingerprint must match initial.
  The suite owns process cleanup. Partial failures remain failed receipts.
- No baseline, fixed-load or shared suite source changes; edit only this contract
  and permission_observer_cost_test.go. No build/commit in this editing subtask.

## Invocation

Run the original TestPermissionCPUProbeCalibration first, then set
WK_E2E_PERMISSION_OBSERVER_COST=1, WK_E2E_BINARY to the old frozen product,
WK_E2E_PERMISSION_CPU_PROBE to the calibrated original Darwin probe, and
WK_E2E_PERMISSION_OBSERVER_REPORT to a new absolute JSON path. Select
TestPermissionObserverCostDiagnostic with -count=1 -timeout=4m -p=1 -v.

## Frozen instruction inputs

- `AGENTS.md`: SHA-256 `6df6655788a5dc019c78c330728aee6a21e7183ebbec9f2583d7f318a26df2df`.
- `test/e2e/AGENTS.md`: SHA-256 `b901697ccd3d02f153b8c1cb1b680fb5124ff290ef8bb99798324f4a94fa4c7c`.
- `test/e2e/message/AGENTS.md`: SHA-256 `492088795d7269065cd442fab5163b043d17212577e917d2f48e0235fb893de6`.
- `test/e2e/message/send_ban/AGENTS.md`: SHA-256 `1d9b7fb93b35a63183529adcae789278a9af1f3cb8bd330e328497d9b50665b6`.
- `test/e2e/suite/FLOW.md`: SHA-256 `541013b411cec5831daf1e0ef8709f13b3c6008c95917204a2b64ec5afa14b03`.
