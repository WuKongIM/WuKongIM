# MQTT shared storage capacity acceptance

Base: `1aff256b2bd9bc71a69cf5e365553357a69c3bc9`, local unmerged
`codex/mqtt-design`. Exact working-source and binary hashes are in
[validation source](validation-source.json); approved scope and failure-first
design are in the [specification](../../specs/mqtt-storage-capacity.md).

## Resulting behavior

New protected publications reserve the original and future shared replay on
every voter and learner before any original mutation. MQTT, WKProto, Product
HTTP, forwarded publication and Will use that same admission. Multiple Sessions
sharing one source body consume one reservation on each retaining replica.
Funding receipts never count as durability votes. Definite refusal leaves no
partial original, and committed idempotent retries retain their original identity.

Exhaustion preserves accepted replay, independent Session acknowledgements and
native maintenance. Only exact durable canceled/deleted proof returns capacity;
unknown physical effects retain node-owned debt across Channel handle closure.
Canonical startup/restore scans rebuild charges, and every storage node registers
its legacy debt before cluster grants become spendable. A node-wide, bounded
escrow amortizes the authoritative Slot command; one grant is not allocated per
message. New format-7 recovery barriers are internal maintenance rather than
consumer messages. Ordinary SyncOnce business content remains charged.

Definite whole-invocation refusal can seal an exact same-boot Will attempt and
avoid adding uncertainty to its current MQTT producer operation. Earlier
unknown submissions still suppress that capability. Closed RPC codes preserve
readiness/pressure families and reject actual success contradictions. Background
cancel retries skip busy locks and transfer physical work/locks/pins to the
existing managed commit coordinator. Caller expiry never returns credit or
releases accepted commit ownership.

## Recorded validation

All process cases use independent Paho MQTT 5 clients, public provisioning/send
receipts and fixed public metrics, with **256 hash Slots**. No MQTT row inspection
or direct usecase invocation supplies process acceptance.

| Scenario | Result | Evidence |
| --- | --- | --- |
| Full ingress/shared body, two independent ACKs, Will recovery and exact producer reconnect, 1/3 nodes | 2/2 passed | Final normal matrix |
| Aggregate tracer and joined restart/replay/retirement/reopened admission | 2/2 passed | Final normal matrix |
| Independent node and cluster ceilings, 3 nodes | 2/2 passed | Final normal matrix |
| Legacy over-limit data, all three nodes stopped/joined onto candidate | Passed | Final normal matrix |
| Concurrent distinct sources/ingress nodes and rejected-source healthy quiet, 1/3 nodes | 2/2 passed | Final normal matrix |
| Unknown physical prepare/cancel/retirement | 3/3 passed, every cut exercised | Final temporary-copy gofail matrix |
| Ordinary product MQTT/WK interop and Will Delay/normal cancellation, 1/3 nodes | 4/4 passed | Ordinary untagged, non-gofail binary |
| Online restore twice, racing CONNECTs, no process restart/resubscription, preserved PacketID/DUP/body identity, fresh delivery once | 1/3 nodes passed | Pre-periodic-repair candidate, see provenance below |
| Will pre-append, issued unknown, committed replay and accepted append beyond grant, 1/3 nodes | 8/8 passed | Final help-wording gofail candidate |
| Busy append/checkpoint/budget admission and slow managed physical commit | Passed, including race | Failure-first isolated integration cases |

The final capacity matrices passed **9/9 in 308.239 seconds** and **3/3 in
57.464 seconds**. Ordinary interop and Will passed in 38.912 and 51.243 seconds.
The selected eight Will interruption cases passed in **383.935 seconds**;
two legacy-admitted cases matched the suffix selector and explicitly skipped
without a legacy binary. This is not the previous full 14-case qualification.
The periodic integration repair passed in 1.840 seconds and its race run in
2.970 seconds. Related message-store, MQTT entry, node codec, cluster adapter,
replication and native-proposal unit packages passed on the final runtime source.
The final config wording unit suite passed in 0.869 seconds.

Restore used the earlier recorded candidate: single-node two-cycle acceptance
passed in 283.41 seconds; the three-node rerun passed in 710.790 seconds after
preserving funding `NeedFrom` readiness through RPC. These runs precede the final
managed periodic cancellation repair and are not labeled as tests of that newer
path. The new path has separate isolated ownership/deadline/race acceptance and
final process fault coverage. A later change only clarifies config help, comments
and the example to include MQTT-disabled storage nodes; its exact
[wording delta](post-validation-help.patch.gz) is retained. Runtime capacity behavior
did not change after the 12-case matrix.

An initial broad related-package gate failed a nil-engine reflection contract
and timed out the full MQTT usecase suite under concurrent real-process load.
The nil-engine maintenance result was corrected; isolated full MQTT usecase
(132.046 seconds) and message-store (41.638 seconds) reruns passed. The later final
message-store rerun passed in 36.419 seconds. This does not claim that the initial
failed command or a repository-wide gate passed.

The named `flow-doc-contracts` check passes with 88 compliant files, zero invalid
and 16 length warnings. The extended module navigation facts justify retaining
the advisory 100-line target deviations; every FLOW stays within the mandatory
150-line bound. `go mod verify`, Go formatting and whitespace checks pass.
The ordinary binary contains no gofail dependency. The independent
[Standards](standards-review.md) and [Spec](spec-review.md) reviews report no
remaining actionable finding; neither reviewer executed runtime tests.

## Failure-first evidence and artifacts

The config-only frozen baseline accepted a second protected source beyond the
budget, reproducing the business failure in 6.729 seconds. Its
[config overlay](red/config-shim.patch.gz), [receipt](red/aggregate-storage-tracer.json)
and original driver log are retained. The initial fixture predates the later
capacity-metric assertions; those assertions require an accounting candidate.

Additional retained product REDs include partial original funding, restart
admission not reopening, a recovery barrier closing MQTT, ghost debit after
unknown cancellation, and same-ClientID CONNACK 0x88 after definite capacity
refusal. The unknown-preparation old-candidate fixture was GREEN and is not
claimed as a RED; its ownership repair was grounded in static evidence. The
periodic lock/managed-owner tests were written and failed before their repair.
Missing binaries/module dependencies, instrumentation preflight errors and
resource-contention setup failures are not counted as product REDs.

[Validation results](validation-results.json) list exact commands, build/source
provenance and SHA-256 hashes. Bounded case JSON files live under `cases/`; failure
receipts live under `red/`; losslessly compressed driver logs are separate under
`logs/`. Case JSON contains public identities/counts/assertions, never credentials
or message payloads. Temporary process roots/binaries are not committed. Frozen
instruction/navigation digests refer to the exact base source revision.

```sh
python3 docs/reports/mqtt-storage-capacity/verify.py --source
```

## Repeat

Use Go 1.25.11 and a verified module cache, with `GOWORK=off`. Commands are also
recorded in the manifest. Build a normal process candidate:

```sh
go build -buildvcs=false -tags=e2e -o /tmp/wk-capacity ./cmd/wukongim
WK_E2E_BINARY=/tmp/wk-capacity \
WK_E2E_MQTT_REPORT_DIR=/tmp/wk-capacity-reports \
go test -p 1 -tags=e2e ./test/e2e/mqtt/storage_capacity \
  -run '^(TestIngress|TestAggregate|TestThreeNodeStorageLimits|TestConcurrentSources)' \
  -count=1 -timeout=8m -v
```

For legacy debt, build base `1aff256b2bd9bc71a69cf5e365553357a69c3bc9` in a separate
temporary checkout after decompressing and applying `red/config-shim.patch.gz`, set
`WK_E2E_MQTT_STORAGE_LEGACY_BINARY` to that config-only binary, and run
`TestThreeNodeRegistersLegacyDebtBeforeAdmission`. The fixture stops and joins all
three nodes before new-format work; it does not qualify mixed-version operation.

Use `scripts/build-gofail-binary.sh` to instrument a temporary source copy only:

```sh
GOFLAGS=-buildvcs=false GOWORK=off scripts/build-gofail-binary.sh \
  --out /tmp/wk-capacity-gofail --package pkg/db/message \
  --package internal/usecase/mqttsession --package internal/app \
  --package internal/infra/mqttwill --package internal/runtime/channelappend
WK_E2E_GOFAIL_MQTT=1 WK_E2E_BINARY=/tmp/wk-capacity-gofail \
WK_E2E_MQTT_REPORT_DIR=/tmp/wk-capacity-faults \
go test -p 1 -tags=e2e ./test/e2e/mqtt/storage_capacity \
  -run '^TestUnknown' -count=1 -timeout=3m -v
go test -p 1 -race -tags=integration ./pkg/db/message \
  -run '^TestMQTTStoragePeriodicCancellation' -count=1 -timeout=90s -v
```

## Operational limits

Defaults are 8 GiB per node and 64 GiB across replica reservations. Set
`mqtt.storage_cluster_bytes` identically on every storage node, including nodes
with MQTT disabled. These are logical reservations; ordinary history, metadata,
WAL, compaction and other physical amplification need separate disk headroom.
Escrow chunks can conservatively deny a send until proven idle credit is returned.

New ledger/command/system records, exchange version 7, closed RPC capabilities
and native format 7 require matching cluster binaries/tools and a cold rollout.
Old untyped format-1 barriers cannot be relabeled using payload spelling;
ambiguous/corrupt evidence keeps admission or completion closed. This does not
qualify rolling downgrade, partitions, arbitrary storage corruption, all native
transfer/membership permutations, 100,000-member workloads, Linux execution,
complete MQTT qualification or release publication. No cloud resources or
external PR/push/release were created.
