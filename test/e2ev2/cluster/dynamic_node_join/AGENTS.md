# dynamic_node_join AGENTS

This scenario proves the Stage 2 dynamic-node lifecycle through a real
multi-node cluster.

## Scenario Contract

- Start a static three-node cluster with manager HTTP enabled.
- Start node 4 in seed-join mode with `WK_CLUSTER_SEEDS`,
  `WK_CLUSTER_ADVERTISE_ADDR`, and `WK_CLUSTER_JOIN_TOKEN`.
- Observe node 4 reach `joining`, become publicly ready, then activate it via
  manager HTTP.
- After activation, wait for node 4's public readiness again, then connect
  through its WKProto gateway, send one message, and require a successful
  `SENDACK`.
- Stage 2 must leave existing Slot assignments unchanged during the join and
  activation flow.
