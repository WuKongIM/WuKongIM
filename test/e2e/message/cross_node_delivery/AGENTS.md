# cross_node_delivery AGENTS

This scenario proves `cmd/wukongim` can run a static three-node cluster where
two users connect to different nodes and exchange online person-channel
messages. It also compares explicit two-replica and three-replica profiles with
12 logical Slot groups over 256 physical hash slots.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/cross_node_delivery -count=1 -timeout 2m
```

Run the opt-in sequential same-host 2/2-versus-3/3 comparison with:

```bash
WK_E2E_REPLICA_LOAD_COMPARISON=1 GOWORK=off go test -tags=e2e ./test/e2e/message/cross_node_delivery -run TestThreeNodeReplicaLoadComparisonAtTwoThousandQPS -count=1 -timeout 8m -p=1 -v
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
- The two-replica profile MUST explicitly set both Slot and Channel replicas to
  two. It MUST prove the complete 256-hash-slot coverage and every logical
  Slot's two-voter quorum through Manager HTTP before treating message traffic
  as two-replica evidence.
- The bounded comparison MUST run three sequential fresh-cluster rounds on the
  same host in 2/2-then-3/3, 3/3-then-2/2, and 2/2-then-3/3 order, changing
  only the Slot/Channel replica count. Each profile uses 1,000 online users,
  500 person Channels, a five-second warmup, ten measured seconds at 2,000
  SEND/s, and a ten-second cooldown. Each requires at least 1,900 measured
  SENDACK and verified RECV events per second, zero send or
  receive-verification errors, and SENDACK P99 at or below 400 milliseconds.
  The report includes all P50/P99 samples, their median/mean, and aggregate
  three-node RSS before the measured window and after cooldown.
- Two replicas require both members for Raft quorum. This profile measures the
  healthy-path cost only and MUST NOT be described as tolerating one replica
  loss.
