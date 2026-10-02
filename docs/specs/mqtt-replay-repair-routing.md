# Bounded cross-node MQTT replay repair

One foreground operation repairs an explicitly selected accepted anchor interval
on a target replica using one donor replica. The request binds Channel identity,
source generation, Channel/leader/route fences, donor/target, anchor position and
row/byte budgets. It contains no content digest or caller-supplied checkpoint.
This is the network recovery building block; automatic anchor selection, donor
rotation, replicated source release and migration readiness remain required.

The origin routes once to the exact target. The target reads its own committed
anchor before requesting content, then imports through the atomic anchor-bound
store port. The donor independently verifies its journal while exporting a
complete anchor interval. Fresh Slot reads precede and follow each operation;
the target also rechecks before import. Ordered membership, status, quorum and
write fence must remain unchanged. Both nodes must be current replicas; a learner
may receive content. Existing migration write fences permit this immutable
recovery operation without allowing source activation or ordinary writes.

Separate four-slot, no-queue receiver/coordinator and donor admission bounds
cross-node nesting without deadlock. Calls have five-second deadlines and transfer
at most 256 rows / 16 MiB. A failed post-import fence suppresses the receipt but
cannot undo committed identical content. RPC 98 is a closed versioned request
with two actions (repair/export), full request echo and the existing closed error
catalog. Export embeds the unchanged bounded replay-page encoding; repair returns
only the verified prefix. No new schema or log format is introduced.

## Failure inventory before implementation

1. A stale, absent, duplicated or replaced member set, wrong serving node, weak
   quorum, foreign source/range or future anchor authority is accepted.
2. A pending/missing receiver anchor triggers a donor fetch or imports a donor's
   self-certified content. Short pages, mismatched endpoints and forged digests
   reach storage or produce a successful receipt.
3. Metadata changes before import or after commit publish a receipt; repair
   advances follower HW, resets its prefix or acts as migration readiness.
4. Saturation, recursive routing, lease leaks, cancellation or shutdown leave
   unbounded work or pinned stores. Donor and receiver admission deadlock.
5. RPC framing, action/status/version, exact echo, row/byte caps or owned byte
   lifetimes permit malformed/oversized/lossy replies or inconsistent actions.
6. Learners cannot repair, migration write fences prevent necessary recovery,
   or a changed fence is silently ignored. Restart loses exact repaired content.

Tests precede implementation at approved Channel/cluster/RPC and Node integration
seams. Real-process MQTT acceptance remains a separate mandatory final gate.

## Frozen context

Source `7b0dcab92`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/channel/FLOW.md`: `23d54a403cf241fa75f1d47ab4837764b21a4a9cf12040fb499fb17b2eb68cb7`
- `pkg/cluster/FLOW.md`: `7ccfbeacecf121ec1cecb85c7a62db223d5e432e3675a1ba15ef9f9fb46ef619`
