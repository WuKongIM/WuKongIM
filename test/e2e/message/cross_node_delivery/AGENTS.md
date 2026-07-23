# cross_node_delivery AGENTS

This scenario proves `cmd/wukongim` can run a static three-node cluster where
two users connect to different nodes and exchange online person-channel
messages.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/cross_node_delivery -count=1 -timeout 2m
```

## Rules

- Keep assertions black-box through public WKProto entrypoints.
- Start all three nodes with `WK_DELIVERY_ENABLE=true`; delivery-off scenarios
  belong in separate coverage.
- Enable read-only Manager HTTP and require every node to agree on the actual
  Raft-elected logical Slot leaders for a bounded stability window before
  connecting users. `/readyz` and WKProto availability alone do not prove that
  initial Slot elections have converged.
- Validate both directions: node1 user to node2 user, and node2 user back to
  node1 user.
- After each `RECV`, assert the recipient owner node reports a pending
  `ack_bindings` value through `/top/v1/snapshot?view=delivery`, then send
  `RecvAck` and assert the same owner node returns to `ack_bindings=0`.
