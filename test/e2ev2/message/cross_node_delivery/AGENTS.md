# cross_node_delivery AGENTS

This scenario proves `cmd/wukongimv2` can run a static three-node cluster where
two users connect to different nodes and exchange online person-channel
messages.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/message/cross_node_delivery -count=1 -timeout 2m
```

## Rules

- Keep assertions black-box through public WKProto entrypoints.
- Start all three nodes with `WK_DELIVERY_ENABLE=true`; delivery-off scenarios
  belong in separate coverage.
- Validate both directions: node1 user to node2 user, and node2 user back to
  node1 user.
