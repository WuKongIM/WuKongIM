# MQTT replay planning for maintenance-only tails

Before retirement production is enabled, an idle source must not create an
endless copy/anchor/retirement feedback loop. Planning now optionally proves
that every position after the latest accepted replay prefix and through its
captured HW is a format-5 anchor or format-6 retirement. The original native
entry, paired proposal and matching committed journal are independently checked
in the same snapshot as the source/latest anchor. Payload text, SyncOnce flags,
local shared-copy progress and the existence of a later retirement are not proof.

The check examines at most 64 positions. A longer tail conservatively remains
copyable rather than scanning unbounded history or claiming false idleness.
Business, activation and other native controls remain copyable. When subsequent
business arrives, copying starts immediately after the accepted prefix and
includes all intervening maintenance positions, preserving contiguous counters.
An idle proof does not release sources, advance an anchor, or retire content.

The typed planning result carries MaintenanceOnly. RPC 97 retains request v1;
ordinary/error replies retain v1. A reply carrying this new assertion uses v2
with an explicit final marker and complete request echo, so older readers reject
it rather than ignoring it. RPC 99's nested request and existing versions remain
unchanged. Matched runtimes are required. No schema or native format is added.

## Failure inventory before implementation

1. Retirement after an anchor is treated as business and reproduced indefinitely;
   a stale plan or local copy cursor silently skips real business or its counters.
2. An uncommitted control, payload lookalike, ordinary SyncOnce record, missing
   entry/proposal/journal, foreign source or changed checksum proves an idle tail.
3. A proof borrows a later HW or mixes snapshots, needs reclaimed original bodies,
   scans an unbounded suffix, or changes storage while answering a planning read.
4. A worker/adapter/RPC drops the assertion, an old peer accepts it silently, or
   malformed optional fields, response version, echo or bounds become idleness.
5. Repeated coordinator passes stop replica recovery, create fresh controls, or
   miss subsequent business. Trim, reopen and backup/restore lose valid evidence.

Tests precede code at storage, adapter/worker, RPC and coordinator boundaries.
Real-disk service/native replication tests supply controlled retirement decisions;
producer admission and complete product process acceptance remain required.

## Frozen context

Source `51afeffe294641216bec6ec71929914652e6e2d6`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `3560287fef837ef40dcf04754ac1037a28ec85d26dae7748f465ab9524c624fe`
- `pkg/db/message/FLOW.md`: `533b9e51f65acb4b8d33b34f1f5813473c903f1f026722a6407f32b8b39df2c0`
- `pkg/channel/FLOW.md`: `e7554cb4040f4693099ad649b6e9f6910be91398a5d672653fa3ef0e33accb02`
- `pkg/channel/worker/FLOW.md`: `7d09cfbfb2678e9932d0b35930bb5e8601969d3b74b619dcccf8c9ffd2dc0b87`
- `pkg/cluster/FLOW.md`: `37a6431bcd7fc1b6894034c4db45d9dfdff5c0fc31e55014ab89c20069cd7966`
- `internal/usecase/mqttsession/FLOW.md`: `c26ce0c8c3bf5d574acf30c29f61aca5f84762ae9595c6a5169c02965400e452`
- `internal/app/FLOW.md`: `0136b2bb52f840127e53884e5e7e2115302ede68ef8cc4e1a19819d043a8da64`
