# MQTT Will publication receipts

Status: local receipt storage and binary backup implemented; current Channel
authority routing, Will execution and product admission remain pending.
This implements the retained-proof prerequisite in the Will execution failure
inventory. It does not itself execute, authorize or schedule a Will.

## Failure inventory before implementation

- Append and follower apply write a compact receipt in the same durable batch as
  a keyed Will. Body/metadata/client identity changes must change its SHA-256.
  Native and unkeyed template writes retain their existing storage semantics.
- Physical history trimming retains the receipt and duplicate admission still
  rejects the server key even after ordinary index 8 disappears. Uncommitted
  suffix truncation/replacement removes only the corresponding suffix receipts;
  it cannot reuse a retained-prefix receipt.
- Reads pin the receipt, checkpoint and original row/retention evidence. An
  uncommitted receipt cannot report publication, and a missing original without
  a physical-retention witness is corruption. A local read is not Slot/Channel
  authority or a proof that an absent receipt was never published.
- Key-bound checksums, strict version/length/key validation and original content
  comparison reject changed identities, zero fields, malformed state and gaps.
  Historical rows without receipts stay readable through existing APIs; missing
  receipt capability cannot be silently treated as a proof of nonpublication.
- Reopen and committed binary backup preserve retained receipts after body
  deletion. Export excludes receipts above the selected HW, import preflights
  them, and allocation high-water statistics include receipt MessageIDs.
  New populated backups use version 4; native versions 1–3 remain supported.
- Backup/import rejects corrupt, duplicate, out-of-cut, downgrade-marked and
  inconsistent receipts. Live row reconstruction must not overwrite an imported
  receipt that disagrees with its immutable original content.

## Storage contract

Message table 1 System 16 is keyed by server Will key and sender UID, retaining
MessageSeq, MessageID, original ServerTimestampMS and a versioned SHA-256 of the
length-delimited UID, client message number, body and publication metadata.
The version-1 fixed envelope is bound to its exact key. No eighth logical table,
per-session body copy, or new public listener is introduced.

Ordinary prefix retention preserves these compact records; the old body/index
APIs keep their visibility semantics. Receipts do not authorize new publications,
release pending tasks, or replace fresh authority checks. Whole-channel deletion,
restore activation, executor fencing, receipt retirement/resource quotas, Node
routing, offline JSONL and actual Will execution remain required product work.
Matched writers/tools and a pre-feature rollback generation are mandatory.

Restore review also requires rejecting an existing target receipt with the same
key but different immutable content, before changing any target row. An equal
checkpoint is not a receipt-content proof; legacy live-row imports must respect
an existing receipt as well.

## Verification

Storage tests first failed because the receipt API/projection did not exist.
Focused tests then verify append modes, content identity, committed cut filtering,
trim/reopen, suffix rollback, recovery-prefix conflicts, v4 byte/stream restore,
legacy original materialization and original timestamps. Additional failure-first
checks reproduced and fixed target-proof overwrite and missing trim witnesses;
preflight now rejects both before creating target catalog data. The byte import
facade reuses the streaming preflight, including for version-1 live-row archives.

```sh
GOWORK=off go test -race -p 2 ./pkg/db/... -count=1
GOWORK=off go test -race -p 2 -tags=integration ./internal/app -run '^TestWillIdempotencySingleNodeClusterIndependentOfClientNumber$' -count=1 -v
```

The app integration exercises the existing real single-node cluster Will SEND
path with 256 hash slots. It is not a Will executor, a routed receipt-read test,
or process-level MQTT acceptance. Receipts remain compact retained state; their
bounded retirement/admission policy must be composed before product enablement.
