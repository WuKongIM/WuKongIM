# Routed automatic MQTT source release

The existing target-owned recovery step gains an explicit `ReleaseSource`
intent. Ordinary recovery keeps its existing behavior. When requested, complete
coverage is acknowledged only after the same target independently materializes
source release from its own committed anchor. Import/scan/retry outcomes never
release. This uses the same four bounded receiver slots, five-second deadline,
one store lease and fresh Slot placement checks; no new worker or RPC is needed.

Immediately before the storage mutation the target rechecks complete membership,
source authority and the exact write fence; both serving and forwarding sides
also recheck before acknowledging. Stable migration fences permit this operation
on already committed anchors, including learners. Changed authority, absent
capability, missing proof, local storage errors and cancellation fail closed.
An error after durable release is an uncertain response, never a rollback; retry
revalidates the anchor and returns the same monotonic cleanup boundary.

RPC 99 version 1 remains ordinary recovery. Version 2 explicitly requests source
release and carries a completion acknowledgement bit. Each reply echoes the exact
versioned request. A version-1 reply cannot confirm version-2 work, and an older
server rejects version 2 instead of silently skipping release. There is no new
table or stored encoding. Matching nodes remain required for MQTT activation.

The existing replay coordinator requests release on its bounded per-replica
visits. Only complete coverage plus an explicit release acknowledgement retires
that scheduling hint. Cold-pass rotation, deadlines, error fairness and joined
worker lifecycle stay unchanged. This releases original source rows, not shared
content: consumer-proof replay GC and product MQTT admission remain required.

## Failure inventory before implementation

1. Ordinary recovery, a partial import or a scan silently releases originals;
   merely having the native anchor is mistaken for having shared content.
2. The target releases after membership/fence changes, or reports success from a
   missing capability, failed store, cancelled request or stale post-commit view.
3. An old reply or missing/unsolicited acknowledgement falsely completes a
   release request; wire echoes ignore the release intent or flags are unbounded.
4. The coordinator forgets to request release or accepts incomplete evidence,
   abandons a failed target, or turns fenced recovery into new copying.
5. Background recovery succeeds but original retention remains stuck; deletion
   passes the anchored prefix or destroys shared content. Restart/retry or Slot
   isolation loses the boundary, bypasses authority or leaks receiver resources.

## Frozen context

Source `a4c3f7e42d979b8733dbafbf4a84998d2630985b`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/channel/FLOW.md`: `625cca5058c446f910a62218a18ec68abc3b400c86fbe518e72a93fe6a6a5233`
- `pkg/cluster/FLOW.md`: `b02147ed70c38b82532498bea458452226eab1d1fd854c001ab560616caaac5b`
- `internal/usecase/mqttsession/FLOW.md`: `fcc4cdc6060cf3a1eb3043575a2157aee5ea176b97962e07744d4b5fb18afb89`
- `internal/contracts/mqttsession/FLOW.md`: `f1fa2d1e6bbbb4694fe476b6599f87da01ce76b4ebf9cdbe6a4ceab73d9d5c69`
- `internal/app/FLOW.md`: `a5e41cfb2f7e1f4e28dfcec244bb31f991ca220365d79c61328373dfbe8b120f`

- `pkg/db/FLOW.md`: `8b723e6f1402c2361a872e3a6e4021ac4146782ebc9b416cb7b9cc9787bb9d75`
