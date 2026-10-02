# MQTT storage capacity under a live TCP partition

Base: `eb4c70b270193b22a520b8fe06b44ec72b19f333`, local unmerged
`codex/mqtt-design`. [Approved specification](../../specs/mqtt-storage-partition.md).
Final validation passed. Both real-process scenarios, the E2E driver/relay race
checks, related message-store tests and the FLOW contract check passed.

## What the scenarios prove

Three real product processes use 256 hash Slots, three Slot replicas and three
Channel replicas. Static membership points to transparent test-owned TCP relays.
The relay identifies the complete accepted socket tuple and exact owned process
ID through bounded `lsof` output. It closes all four directed links involving
node 3, refuses reconnects, and preserves node-local TCP and public listeners.
The two survivors must agree on the actual leaders of all three physical Slots
and retain public quorum. Product processes are never suspended or restarted.

The ordinary-product scenario protects one shared accepted body for two
independent persistent Sessions. For at least 35 seconds of live isolation,
every public ingress refuses another protected source and every node retains
the accepted charge within the configured ceilings. Healing preserves original
identity, Packet Identifier and unfinished QoS 1 DUP replay without SUBSCRIBE.
The first ACK cannot release the other Session's body. Final completion reaches
zero charge, admits a fresh publication and returns to zero after its completion.

The instrumented scenario delays positive-charge physical preparation for
12 seconds at the existing before-commit boundary, then cuts TCP. The actual
batch commit still executes. A separate inert after-commit witness, restricted
to positive charges, must increment exactly once before links heal. Reserved
gauges alone are explicitly insufficient evidence. The node retains the unknown
charge for another ten seconds and refuses additional protected work. After
healing, the original keyed continuation resolves the preparation, accepted
replay identities survive, and normal consumer completion returns all charges
to zero. Fresh delivery then completes with zero residual debt.

## Validation and limitations

| Final check | Result | Duration |
| --- | --- | --- |
| Accepted data through live partition and recovery, ordinary product | PASS | 109.80 s case / 111.873 s package |
| Unknown positive-charge physical preparation, temporary gofail product | PASS | 99.06 s case / 100.738 s package |
| E2E suite helpers with race checking | PASS | 32.394 s |
| `pkg/db/message` tests | PASS | 37.881 s |
| Named `flow-doc-contracts` check | PASS | 88 compliant files, 0 invalid, 16 existing advisory warnings |

The accepted-data receipt retains 5,004 bytes per node throughout 35.008 seconds
of isolation. The unknown-preparation receipt proves one charged physical commit
while cut and retains 10,012 bytes on node 3 versus its 5,000-byte baseline for
another 10.073 seconds. Both receipts finish with `[0, 0, 0]` reserved bytes after
fresh delivery completes. Darwin's race-test linker emitted an LC_DYSYMTAB
warning; all test processes exited successfully with no reported data race.

See [validation results](validation-results.json), [source/binary provenance](validation-source.json)
and the bounded `cases/` JSON receipts. Driver logs and the candidate patch are
losslessly compressed. Frozen instruction/FLOW digests refer to the exact base.
The [Standards review](standards-review.md) and [Spec review](spec-review.md) are
read-only source reviews; they do not supply runtime verdicts.

The first unknown run failed because the harness queried a disabled failpoint's
counter. A later provisional pass lacked an independent physical-commit witness;
it is not final commit-loss qualification. Review identified and closed that
evidence gap, the missing unknown-case link/quorum assertions, field comments,
preallocation bounds and full socket-tuple identity. No product behavior defect
has been established; product changes only add inert gofail instrumentation.
There is no new user-visible configuration or behavior requiring a Changelog entry.

The ordinary product binary is untagged and has no gofail dependency. Race
checking applies to the Go E2E driver/relay and suite helpers, not every product
goroutine. The fault binary is built from a temporary copy only; generated
instrumentation and its module dependency never enter the working tree.

Qualification covers the recorded macOS arm64 environment and a bidirectional
reset/refusal partition around one live node. It does not qualify arbitrary
packet delay/loss, every leader/placement permutation, partitions of other shapes,
membership changes, Linux, large workloads, complete MQTT behavior or a release.
This task creates no cloud resources, external PR/push/deployment or release.

## Repeat

Use Go 1.25.11 with `GOWORK=off`, `lsof` and a verified module cache. Build and
run the ordinary candidate:

```sh
go build -p 1 -buildvcs=false -o /tmp/wk-storage-partition ./cmd/wukongim
WK_E2E_BINARY=/tmp/wk-storage-partition \
WK_E2E_MQTT_REPORT_DIR=/tmp/wk-storage-partition-reports \
go test -p 1 -race -tags=e2e ./test/e2e/mqtt/storage_partition \
  -run '^TestPartitionRetainsAcceptedStorageAndReopensAfterRecovery$' \
  -count=1 -timeout=5m -v
```

Instrument a temporary source copy with the repository helper:

```sh
GOFLAGS='-buildvcs=false -p=1' scripts/build-gofail-binary.sh \
  --out /tmp/wk-storage-partition-gofail --package pkg/db/message
WK_E2E_GOFAIL_MQTT=1 WK_E2E_BINARY=/tmp/wk-storage-partition-gofail \
WK_E2E_MQTT_REPORT_DIR=/tmp/wk-storage-partition-faults \
go test -p 1 -race -tags=e2e ./test/e2e/mqtt/storage_partition \
  -run '^TestPartitionRetainsUnknownPreparationUntilResolved$' \
  -count=1 -timeout=5m -v
go test -p 1 -race -tags=e2e ./test/e2e/suite -count=1 -timeout=3m
go test -p 1 ./pkg/db/message -count=1 -timeout=3m
```

Verify retained receipts, hashes, frozen context and current source:

```sh
python3 docs/reports/mqtt-storage-partition/verify.py --source
```
