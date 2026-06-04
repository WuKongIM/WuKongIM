# wukongimv2

`cmd/wukongimv2` is the standalone verification entry for `internalv2/app`.

During phase 1, it validates the `SEND -> SENDACK` skeleton on single-node
clusters and static multi-node clusters. It reuses existing `WK_` configuration
keys where possible and is a migration-period verification entry, not a
complete production replacement for `cmd/wukongim`.

Run it with an explicit config file:

```sh
go run ./cmd/wukongimv2 -config ./scripts/wukongimv2/wukongimv2.conf
```

The runnable local config lives beside the helper scripts under
`scripts/wukongimv2/` and only includes keys currently parsed by this
standalone entry.

For a local single-node cluster, use the helper script:

```sh
scripts/start-wukongimv2-single-node.sh --clean
```

The script builds `cmd/wukongimv2`, starts the single-node cluster config,
waits for `/readyz`, and keeps the node running until Ctrl+C. Logs are written
under `data/wukongimv2-single-node-logs/`.

For a local static three-node cluster, the fastest path is the helper script:

```sh
scripts/start-wukongimv2-three-nodes.sh --clean
```

The script builds `cmd/wukongimv2`, starts all three nodes, waits for `/readyz`,
and keeps the cluster running until Ctrl+C. Per-node logs are written under
`data/wukongimv2-three-node-logs/`.

To start the same configs manually, run these commands from three terminals:

```sh
go run ./cmd/wukongimv2 -config ./scripts/wukongimv2/wukongimv2-node1.conf
go run ./cmd/wukongimv2 -config ./scripts/wukongimv2/wukongimv2-node2.conf
go run ./cmd/wukongimv2 -config ./scripts/wukongimv2/wukongimv2-node3.conf
```

All nodes in a static cluster must share `WK_CLUSTER_ID` and
`WK_CLUSTER_NODES`. Each node keeps its own `WK_NODE_ID`,
`WK_NODE_DATA_DIR`, `WK_CLUSTER_LISTEN_ADDR`, API listener, and gateway
listener ports.

For `wkbench capacity send --profile person` runs, configure:

```ini
WK_API_LISTEN_ADDR=127.0.0.1:5001
WK_BENCH_API_ENABLE=true
WK_METRICS_ENABLE=true
WK_EXTERNAL_TCPADDR=127.0.0.1:5100
# Optional: 0 lets clusterv2 derive max(4, GOMAXPROCS).
WK_CLUSTER_CHANNEL_REACTOR_COUNT=0
# Optional: 0 keeps ChannelV2 runtime defaults for blocking store workers.
# WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS=0
# WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS=0
# Optional: tune gateway SEND async sharding and micro-batch collection.
# WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_DISPATCH_WORKERS=0
# WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT=1ms
# WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_RECORDS=512
# WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_BYTES=524288
# Optional: 0 lets wukongimv2 derive a CPU-aware gnet event-loop count.
# WK_GATEWAY_GNET_NUM_EVENT_LOOP=0
```

With metrics enabled, scrape the standard Prometheus endpoint:

```sh
curl -fsS http://127.0.0.1:5001/metrics
```

For local testing without running Prometheus, keep two snapshots and classify
them with wkbench:

```sh
curl -fsS http://127.0.0.1:5001/metrics > /tmp/wk-before.prom
# run the measured SEND -> SENDACK load
curl -fsS http://127.0.0.1:5001/metrics > /tmp/wk-after.prom
go run ./cmd/wkbench metrics classify --before /tmp/wk-before.prom --after /tmp/wk-after.prom
```

The `/bench/v1/*` routes are benchmark-only and currently support the phase-1
`SEND -> SENDACK` target surface; they do not represent delivery, fanout, or
management API support.
