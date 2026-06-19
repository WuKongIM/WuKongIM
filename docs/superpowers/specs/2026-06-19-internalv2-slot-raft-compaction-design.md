# Internalv2 Slot Raft Compaction Design

## Goal

Let the `internalv2` manager API support manual node-local Slot Raft log compaction so the existing web Slot logs page can trigger compaction through `wukongimv2`.

## Scope

- Add `POST /manager/nodes/:node_id/slots/:slot_id/compact` to `internalv2/access/manager`.
- Keep the response shape compatible with the legacy `internal` implementation already used by `web`.
- Route local and remote node-scoped compaction below the HTTP layer through `internalv2/usecase/management` and `internalv2/infra/cluster`.
- Add a node RPC adapter for remote Slot Raft compaction, separate from the read-only distributed log RPC.
- Add a `pkg/clusterv2.Node` local Slot compaction facade backed by the default Slot Multi-Raft runtime.

## Architecture

The HTTP route remains a thin adapter: it parses `node_id` and `slot_id`, enforces `cluster.slot:w`, delegates to the management usecase, and returns JSON DTOs. The management usecase owns request validation and manager response assembly. The infra adapter chooses local versus remote execution using the selected node ID.

Local execution calls `pkg/clusterv2.Node.LocalCompactSlotRaftLog(ctx, slotID)`, which invokes the default Slot runtime's `CompactLog` and maps `multiraft.LogCompactionResult` into a stable clusterv2 result. Remote execution uses a new manager Slot Raft node RPC service; the target node runs the same local usecase/operator path.

## API Shape

```http
POST /manager/nodes/:node_id/slots/:slot_id/compact
```

Successful responses use the existing web contract:

```json
{
  "generated_at": "2026-06-19T00:00:00Z",
  "total": 1,
  "succeeded": 1,
  "failed": 0,
  "items": [{
    "node_id": 1,
    "slot_id": 9,
    "success": true,
    "applied_index": 10,
    "before_snapshot_index": 4,
    "after_snapshot_index": 10,
    "compacted": true,
    "skipped_reason": "",
    "error": ""
  }]
}
```

## Error Handling

- Invalid `node_id` or `slot_id` returns `400`.
- Missing management wiring returns `503`.
- Missing Slot Raft operator wiring returns `503`.
- Slot not found returns `404`.
- Other operation failures are preserved as per-item errors when the usecase receives a node-local attempt result, or returned as `500` when the operation cannot be started.

## Testing

- Add internalv2 manager HTTP tests for success and permission rejection.
- Add management usecase tests for request validation and result assembly.
- Add infra tests for local and remote Slot Raft compaction routing.
- Add access/node tests for Slot Raft compaction RPC client and handler.
- Add clusterv2 tests proving the local compaction facade uses the default Slot runtime.
- Keep existing web tests as contract coverage; run the Slot logs and manager API tests after backend work.
