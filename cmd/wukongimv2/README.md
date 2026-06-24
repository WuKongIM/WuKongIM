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
under `data/wukongimv2-single-node-logs/`. This helper enables the app-managed
Prometheus process by default; set `WK_PROMETHEUS_ENABLE=false` before running
the script to disable it. When Prometheus is enabled and
`WK_PROMETHEUS_BINARY_PATH` is unset, the script first builds Prometheus into
the wukongimv2 embed assets and then builds wukongimv2, so the resulting
`wukongimv2` executable carries the Prometheus binary.

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

Static bootstrap nodes use `WK_CLUSTER_NODES` to form the initial Controller
voter set and `WK_CLUSTER_JOIN_TOKEN` to validate future JoinNode RPCs. A
future dynamic data node uses `WK_CLUSTER_SEEDS`, `WK_CLUSTER_ADVERTISE_ADDR`,
and the same `WK_CLUSTER_JOIN_TOKEN` instead; it must not set
`WK_CLUSTER_NODES`, because seed-join mode runs as a Controller mirror and does
not bootstrap a new voter set.

For `wkbench capacity send --profile person` runs, configure:

```ini
WK_API_LISTEN_ADDR=127.0.0.1:5001
WK_BENCH_API_ENABLE=true
WK_METRICS_ENABLE=true
WK_EXTERNAL_TCPADDR=127.0.0.1:5100
# Optional: 0 lets clusterv2 derive max(4, GOMAXPROCS).
WK_CLUSTER_CHANNEL_REACTOR_COUNT=0
# Optional: 0 keeps ChannelV2 runtime defaults for blocking workers.
# WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS=0
# WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS=0
# WK_CLUSTER_CHANNEL_RPC_WORKERS=50
# Optional: tune gateway SEND async sharding and micro-batch collection.
# WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS=128
# WK_GATEWAY_RUNTIME_ASYNC_SEND_QUEUE_CAPACITY=131072
# WK_GATEWAY_RUNTIME_ASYNC_AUTH_WORKERS=16
# WK_GATEWAY_RUNTIME_ASYNC_AUTH_QUEUE_CAPACITY=8192
# WK_GATEWAY_RUNTIME_ASYNC_POOL_RELEASE_TIMEOUT=100ms
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

To let `wukongimv2` manage a local Prometheus process for the node in manual
`go run` usage, keep `WK_METRICS_ENABLE=true` and enable the Prometheus block in
the config:

```ini
WK_PROMETHEUS_ENABLE=true
WK_PROMETHEUS_BINARY_PATH=
WK_PROMETHEUS_LISTEN_ADDR=127.0.0.1:9090
WK_PROMETHEUS_DATA_DIR=./data/wukongimv2-single-node-data/prometheus
WK_PROMETHEUS_RETENTION_TIME=360h
WK_PROMETHEUS_SCRAPE_INTERVAL=15s
WK_PROMETHEUS_SCRAPE_TARGETS=[]
```

`WK_PROMETHEUS_SCRAPE_TARGETS=[]` derives one scrape target from
`WK_API_LISTEN_ADDR`. Leave `WK_PROMETHEUS_BINARY_PATH` empty to use the
embedded Prometheus binary; set it to an absolute path only when you want to
override the embedded binary with an external Prometheus executable.

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
