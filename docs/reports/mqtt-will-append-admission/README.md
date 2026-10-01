# Will append admission recovery acceptance

Source: `68eb337c50267fb2e0114130ef2b028760d17555` plus the working-copy and
binary hashes in [validation source](validation-source.json). The local task
branch remains unmerged. See the [contract](../../specs/mqtt-will-append-admission.md).

## Behavior

A crash after durable Will Admitted previously left an unpublished Started Will
pending. The exact body-free journal now writes version 2 and separates turn
admission from irreversible append permission. The origin router must obtain
that permission immediately before local submission or remote forwarding.
Sealing and permission compete under the existing generation-owned journal lock;
accepted work and node RPC never retain the process-local callback. Routing
retries reuse only the exact original attempt and its original deadline.

A version-2 unissued Admitted record can now be sealed after exact boot retirement
and recovered through fresh policy, a reserved successor and definite Slot CAS.
Positive committed receipts retain the original identity and remain the first
choice. AppendIssued and version-1 Admitted remain positive-only: no elapsed grant,
source-node death or missing receipt authorizes another publication. Unknown
records fail closed. Capacity, bounded reclamation and the existing cohort stay
unchanged; no new worker, waiting queue, body map or configuration is added.

## Evidence

The failure-first tracer reached the pre-send cut, crashed/restarted its exact
executor and failed at the original receipt deadline on the frozen baseline:
52.962 seconds, repeated at 51.847 seconds. The repaired tracer passed in 52.772
seconds. [Baseline overlay](baseline-cut-overlay.patch) and
[baseline hashes](baseline-source.json) make the inert-cut baseline reconstructable.
Preflight/build failures are not counted as RED evidence.

The final matrix passed **14/14 in 681.229 seconds** on Go 1.25.11 darwin/arm64.
Independent Paho MQTT 5 clients and public provisioning exercised single-node
and three-node clusters with 256 hash Slots. Each topology covers Reserved,
pre-send Admitted, pre-append router admission, issued permission, version-1
upgrade, committed publication replay and a delayed accepted append. Safe
recovery cases deliver one frozen original Will, ACK it and observe 15 seconds
of healthy quiet without CONNECT retry or resubscription. Committed replay keeps
PacketID, DUP and message identity. Issued/legacy unknown cases observe 21 seconds
of healthy quiet after exact restart with zero new publication attempts; the
accepted-append case stays at one attempt beyond two ten-second grants and then
completes the original publication. Closed receivers cannot satisfy quiet checks.

An initial matrix passed ten cases and failed four because restarted counters
were disabled or the upgrade fixture called Restart after SIGKILL. Startup-enabled
counters and a case-owned atomic binary alias fixed those harness defects. Product
Go hashes did not change. The four failed artifacts are retained separately from
the final passing matrix.

The ordinary Will regression passed in 53.298 seconds; controlled cap=2 reclamation
regression passed both topologies in 136.575 seconds. App Will wiring integration
passed in 16.751 seconds. All related usecase/router/contracts race checks passed;
internal/app's first compile was interrupted by disappearing shared module files,
then its full race suite passed in 4.548 seconds using a private verified cache.
`go mod verify` passed. The ordinary binary has no gofail dependency.
The named flow-doc-contracts check passed with 88 compliant files, zero invalid
and 11 pre-existing length warnings; final `git diff --check` passed.

[Validation results](validation-results.json) record repeat commands, retained log
hashes and 23 bounded artifacts. Final case reports run after owned client/process
cleanup and contain no credentials, payloads, client identities or raw diagnostics.
Ordinary-regression case artifacts were generated in temporary node roots and
cleaned; its retained log records the passing run. The
[frozen context](source-context.json) records exact-source instruction/navigation
digests. Independent [Standards](standards-review.md) and [Spec](spec-review.md)
reviews report zero findings, including the frozen harness supplement. Their
runtime statements reflect review time; final results are reported here.

## Repeat

Use Go 1.25.11 with a verified module cache. The builder instruments only a
temporary source copy using gofail v0.2.0; ordinary module files remain unchanged.

```sh
GOFLAGS=-buildvcs=false GOWORK=off scripts/build-gofail-binary.sh \
  --out /tmp/wukongim-will-append-candidate-gofail \
  --package internal/usecase/mqttsession --package internal/app \
  --package internal/infra/mqttwill --package internal/runtime/channelappend
```

To build the version-1/RED binary, check out the frozen source revision in a
separate temporary worktree, apply the saved baseline overlay there, and run the
same builder with output `/tmp/wukongim-will-append-baseline-gofail`. It changes
only product cut comments and the failure-first process fixture.

```sh
WK_E2E_GOFAIL_MQTT=1 \
WK_E2E_BINARY=/tmp/wukongim-will-append-candidate-gofail \
WK_E2E_MQTT_LEGACY_BINARY=/tmp/wukongim-will-append-baseline-gofail \
WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-will-append-repeat \
GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/will_recovery \
  -count=1 -timeout=18m -v
```

The legacy binary is required for all 14 cases; without it, compatibility cases
skip. For the RED tracer use the frozen baseline fixture/binary and
`-run 'TestStartedWillRecoveryPreservesOnePublication/1-node-cluster/pre-send$'`.
Build the ordinary binary with:

```sh
GOWORK=off go build -buildvcs=false \
  -o /tmp/wukongim-will-append-ordinary ./cmd/wukongim
```

The validation manifest lists its regression, pressure, integration and race
commands.

## Limits

This qualifies the controlled crash windows and finite observations above.
AppendIssued and legacy Admitted can still remain pending without a positive
receipt. Full 1,024-record pressure, delayed CAS/reclamation races, missing/corrupt
journals, partitions, arbitrary restore/delete and complete terminal recovery of
unknown effects are not process-qualified here. Aggregate shared-storage admission
and the remaining MQTT workload/failure matrix remain open. No Linux run, complete
MQTT qualification or release publication is claimed.
