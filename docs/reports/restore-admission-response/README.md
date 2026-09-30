# Restore admission response

The operator approved diagnosing generic restore HTTP 503 after the first MQTT
subscription repair. Development remains on `codex/mqtt-design`; the MQTT repair
is commit `ab8b19a988788917648bf5cae2b406fc59ee5c93`. The restore repair uses that
commit as its source base. Governing source digests are captured in
[initial context](frozen-context.json) and [controlled context](controlled-frozen-context.json).

## What the evidence proves

The earlier [HTTP 503 receipt](../mqtt-restore-reactivation/restore-response-503.json)
does not establish admission or identify a failed service phase. A normal
three-node diagnostic runs both restore/MQTT cycles successfully, with positive
admission, successful lease cleanup and successful preflight on all three nodes.
[Fixed signals](run-1-signals.txt), [public assertions](normal-three-node.json)
and [probe provenance](diagnostic-provenance.json) retain that bounded observation.
It neither reproduces nor explains the earlier failures.

The test-first [controlled RED](controlled-release-red.json) enables a temporary
build's inert lease-release fault after backup. The single restore request returns
HTTP 503, the fault is hit once, and read-only authenticated Manager state observes
ActiveRestore in a fresh fixture. The original failed response remains a test
failure; no restore mutation is retried. This proves that a later cleanup error
can misreport an admitted job. It does not prove that the injected cause occurred
in the historical requests. [RED provenance](controlled-red-provenance.json)
identifies the original admission implementation plus the inert fault comment.

## Repair and bounds

Immediately before admission, StartRestore rereads the plan and complete archive
operation, requiring the captured token, kind, archive, coordinator, term and
timestamps to match, with an unexpired lease. One existing Controller CAS consumes
that lease and publishes ActiveRestore while preserving newer unrelated state.
The active job retains the existing source-archive deletion protection. Known
admission needs no subsequent cleanup transaction that could downgrade its receipt.

Failed admission releases only unchanged captured lease authority. Unknown
acquisition or admission remains unknown, including an applied CAS with a lost
reply. An empty returned job is not proof of non-admission. A secondary cleanup
error annotates the primary error without adding its own definite-conflict
classification. Foreign authority is preserved; admission never gains automatic
mutation retry. The existing bounded failed-admission cleanup remains unchanged
in duration and conflict attempts.

This changes no wire, storage or configuration schema and adds no worker, queue,
per-user state or member fanout. Admission remains a constant-size authority
comparison plus the existing 256-slot job and Controller CAS. The previous
post-admission cleanup read/CAS is removed. Existing preflight, maintenance,
replica staging, verification, rollback and MQTT restoration remain required.

## Validation

The failure inventory and all 18 isolated modes were written before the functional
repair. Twelve modes fail meaningfully on the original implementation. The final
focused race test and full backup use-case, infrastructure, runtime, Manager and
app race suites pass. The named `flow-doc-contracts` check passes with 87 compliant
FLOW files and 11 pre-existing length warnings. [Validation](validation.json)
retains bounded results and raw-output hashes.

The repaired temporary build passes process cases in single-node and three-node clusters:
two restores per topology, no process restart, no resubscription, original MQTT
identity and Packet ID preserved, DUP replay, post-backup state removed, and one
fresh delivery. The old cleanup fault remains enabled with zero hits.
[Single-node cluster](controlled-green-1.json) and [three-node cluster](controlled-green-3.json)
receipts identify the assertions. [Candidate provenance](candidate-provenance.json)
records the ordinary and temporary binary hashes and exact source/fixture hashes.

The ordinary build also passes both topologies with two restore/MQTT cycles each:
[single-node cluster](ordinary-green-1.json) and [three-node cluster](ordinary-green-3.json).
The controlled process suite takes 945.620 s and the ordinary suite 955.152 s.
Together they complete eight final restores without mutation retry, process
restart or resubscription. Neither packet nor restore deadlines were enlarged.
These bounded acceptance cases do not qualify arbitrary host load, coordinator
failover, external repositories or 100,000-member workloads. Raw configuration,
credentials, payloads and logs remain outside Git; reports contain fixed phases,
counts and public assertions.

## Reproduce

```sh
GOWORK=off go build -buildvcs=false -o /tmp/wukongim-restore ./cmd/wukongim
WK_E2E_BINARY=/tmp/wukongim-restore WK_E2E_MQTT_REPORT_DIR=/tmp/restore-ordinary GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/restore -run '^TestRestoreReactivatesPersistentMQTTWithoutProcessRestart$' -count=1 -timeout=25m -v
scripts/build-gofail-binary.sh --package internal/usecase/backup --out /tmp/wukongim-restore-gofail
WK_E2E_BINARY=/tmp/wukongim-restore-gofail WK_E2E_GOFAIL_MQTT=1 WK_E2E_MQTT_REPORT_DIR=/tmp/restore-fault GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/restore -run '^TestRestoreAdmissionSurvivesUnavailableArchiveLeaseCleanup$' -count=1 -timeout=25m -v
GOWORK=off go test -race ./internal/usecase/backup ./internal/infra/backup ./internal/runtime/backup ./internal/access/manager ./internal/app -count=1
```
