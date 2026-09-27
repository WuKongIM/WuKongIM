# Startup recovery E2E

Use real processes and public HTTP/WKProto only. Cover a 256-hash-slot
single-node cluster and persist a JSON report with observed recovery stages.
Never read or mutate live storage internals. Snapshot creation uses the
public Manager Slot compaction endpoint. Verify device credentials both
from the snapshot and from the committed suffix after restart.

Also cover certified reuse in a three-node cluster and after moving a replica
to a fourth node, with explicit public credentials. A frozen older binary is
opted in through `WK_E2E_STARTUP_OLDER_BINARY` to verify safe fallback after writes.
Also upgrade twelve physical Slots from that older binary, with all twelve
snapshots and with one snapshotless neighbor, then prove two further restarts
reuse every anchored Slot while credentials continue changing. Await both HTTP
and WKProto readiness; dynamic restart starts its static seeds before the joined node.

The opt-in Linux scale scenario creates three million user/device records
through public HTTP. Freeze the cleanly stopped fixture before comparing
baseline/candidate runs under real 2/4 GiB cgroup limits with swap disabled.
Use Manager pagination for complete identity/device/token-presence inventory
and WKProto for deterministic token-value samples. Keep startup measurements
separate from subsequent inventory/profile work, and label profile coverage.
Fixture copies remain opaque filesystem artifacts; do not decode live storage.

`WK_E2E_STARTUP_SCALE_INTERRUPT=1` kills after a logged install begins, proves
completion was not observed, and then performs the ordinary complete validation.
The initial install event proves durable preparation, not a nonzero data batch.
`WK_E2E_STARTUP_EXPECT_STAGE` asserts the selected recovery path; certified reuse
must not rewrite snapshot data. The separate `TestStartupRecoveryScaleProfile`
uses a diagnostic-only instrumented binary and `WK_E2E_STARTUP_CPU_PROFILE`;
it skips the full inventory and must never count as scale acceptance.
