# internalv2 Dynamic Node Stage 9 wkcli sim Smoke

This optional manual smoke complements
`test/e2ev2/cluster/dynamic_node_readiness`. It uses public v2 bench,
manager, metrics, and WKProto surfaces only; it does not import server
internals.

## Cluster Shape

Start a Stage 9 cluster with manager HTTP, bench API, metrics, gateway
listeners, and health report settings close to production:

```text
WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL=5s
WK_CLUSTER_NODE_HEALTH_REPORT_TTL=30s
```

For a short local smoke, keep the cluster at three active data nodes before
adding node 4 through seed join.

## Traffic

Run traffic through public `wkcli sim` while lifecycle changes are in progress:

```bash
go run ./cmd/wkcli sim --server http://127.0.0.1:5001 --users 200 --groups 40 --group-members 20 --rate 2/s --status-listen 127.0.0.1:19091 --max-runtime 5m
```

Keep the simulator running while the operator performs the lifecycle flow.

## Lifecycle Flow

Use manager HTTP or the manager UI to perform these steps in order:

1. Seed-join node 4.
2. Wait until node 4 is `joining` with fresh `alive` health.
3. Activate node 4.
4. Start one bounded Slot onboarding move to node 4.
5. Mark node 4 leaving.
6. Enable gateway drain for node 4.
7. Advance one bounded Slot scale-in move away from node 4.
8. Remove node 4 only after `safe_to_remove=true`.

## Evidence To Keep

- `curl http://127.0.0.1:19091/status`
- Manager node list JSON before and after each lifecycle transition.
- Manager scale-in status JSON before final remove.
- `/metrics` snippets for:
  - `wukongim_node_lifecycle_nodes`
  - `wukongim_node_health_freshness_nodes`
  - `wukongim_node_lifecycle_attempts_total`
  - `wukongim_node_scale_in_blockers_total`
  - `wukongim_discovery_membership_revision`

## Pass Criteria

- `wkcli sim` continues to report successful sends during join, activation,
  onboarding, scale-in, and removal.
- Node 4 is never schedulable while it is only `joining`.
- Node 4 becomes schedulable only after activation and fresh `alive` health.
- Final remove is attempted only after Slot, Channel, health, and gateway
  drain blockers are clear.
- Metrics expose the lifecycle and health evidence without high-cardinality
  labels.
