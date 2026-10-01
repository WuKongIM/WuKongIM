# Will attempt reclamation acceptance

Source: `3f8f0960af900492032b09feeaaf9bcfc73c4449` plus the exact working-copy
source and binary hashes in [final provenance](final-provenance.json). The local
task branch is unmerged. See the [contract](../../specs/mqtt-will-attempt-reclamation.md).

## Behavior

Failed cleanup previously retained journal capacity permanently. Reservation
pressure now runs one nonwaiting page in the existing Will execution cohort.
The journal captures at most 16 checksummed body-free identities over its fixed
1,024-record inventory. Overlapping pages advance one filename start position;
every record becomes first within a wrap even when later authority reads time out.
Reads run outside journal locks, with 250 ms per read and 750 ms per page.

Only a valid fresh exact Slot row with strictly newer execution, or the identical
Published/Rejected execution, authorizes deletion. Missing, corrupt, current,
inconsistent and uncertain evidence retains the record. Cleanup returns the
original reservation error; a later ordinary turn must reread, claim and authorize.
No cleanup, elapsed grant or missing row grants non-dispatch proof. Stop joins
this existing cohort before journal closure.

## Process evidence

The failure-first independent Paho MQTT 5 scenario uses real product processes,
public HTTP provisioning and 256 hash Slots. Temporary gofail reduces the journal
cap to two, pauses one exact worker after Started and fails another Will's terminal
cleanup. The same ClientID across lifetimes pins discovery to one Slot. The test
requires capacity refusal on that executor, then another publication after cleanup
is reachable. SIGKILL/restart must recover the first Will; persistent reconnect
does not SUBSCRIBE again.

The baseline failed at third-Will receipt after verified capacity refusal: 55.193
seconds, repeated at 55.646 seconds against the final fixture. The repair's first
single-node cluster run passed in 69.444 seconds. The complete single-node and
three-node cluster matrix passed in 131.333 seconds. Both cases observe three
distinct business publications, zero unexpected deliveries and one exact restart.
The precrash three-second healthy quiet window allows Paho's manual PUBACK batch
to flush; the final quiet window is 15 seconds. Neither accepts closed transport.

The final ordinary Will regression passed in 54.538 seconds; the unchanged six-window
recovery matrix passed in 313.697 seconds against the delivered instrumented binary.
[Validation receipts](validation.json) bind commands, log hashes and the ten
bounded case artifacts to exact provenance. Focused Will/MQTT race and flow-doc-contracts
passed: 88 compliant FLOW files, zero invalid and 11 existing length warnings.
An earlier app race link failed with ENOSPC; obsolete task-owned temporary build
artifacts were removed, then the check passed.

The pressure reports run after all cleanup and exclude credentials, client identities, payloads and
raw logs. [Frozen context](frozen-context.json) records exact-source AGENTS/FLOW
digests and pre-implementation test/inventory hashes. [Two-axis review](reviews.md)
records the finding and its resolution.

## Repeat

Use Go 1.25.11. The script enables gofail in a temporary source copy; ordinary
module files and the fixed 1,024-record capacity stay unchanged.

```sh
scripts/build-gofail-binary.sh --out /tmp/wukongim-will-reclamation-candidate-gofail \
  --package internal/usecase/mqttsession --package internal/app \
  --package internal/infra/mqttwill --package internal/runtime/channelappend
WK_E2E_GOFAIL_MQTT=1 \
WK_E2E_BINARY=/tmp/wukongim-will-reclamation-candidate-gofail \
WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-will-reclamation-matrix \
GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/will_reclamation \
  -count=1 -timeout=6m -v
```

## Limits

This qualifies two controlled lower-cap process cases and their finite windows.
Production-cap stress, strict-supersession reclamation, missing/corrupt record
behavior under pressure, partial-page deadline fairness and delayed unknown claims
are not process-qualified here. Missing/current/unknown records can still exhaust
capacity and fail closed. Already-admitted unknown-effect terminal recovery,
legacy/lost journals, arbitrary restore/delete, partitions and aggregate shared
storage admission remain open. This is not complete MQTT or release acceptance.
