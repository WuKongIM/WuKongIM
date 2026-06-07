# Message DB Commit Shards Design

## Goal

Raise the stable 16k real-QPS ceiling by reducing tail queueing in the message DB commit coordinator without weakening durable sync or per-channel ordering.

## Evidence

Baseline 16k runs were not stable. Gateway async send stayed low-latency, while DB request p99 and ChannelV2 quorum wait moved together. Capping commit batch bytes to 128KiB reduced physical commit p99, and a 2ms flush window improved average p99, but repeated runs still failed on either p99 or actual ratio. Increasing only store apply workers did not fix throughput and increased DB request p99.

## Design

Add a configurable shard count to the message DB commit coordinator. The default remains one shard, preserving existing behavior. When more than one shard is configured, logical commit requests are routed by a stable hash of `commit.Request.Partition`; each shard owns one existing coordinator instance.

The message DB compatibility path already holds the channel append lock from prepare through commit completion. That lock remains the per-channel ordering guard. Batch requests will use a stable representative channel key as the request partition instead of the current lane-only batch partition so requests distribute across shards under multi-channel load.

Durability remains unchanged: each shard still commits its Pebble batch with sync enabled. Metrics remain low-cardinality and aggregate queue depth across shards so existing Prometheus/Grafana surfaces continue to work.

## Config

Add `WK_CLUSTER_COMMIT_COORDINATOR_SHARDS`. A value of `0` or `1` keeps one coordinator. Benchmark scripts may set a higher value for high-QPS validation.

## Validation

Unit tests cover shard routing, effective config, and aggregated queue observation. Performance validation reruns the three-node real-QPS 16k script multiple times with the selected shard count.

Validation note: local three-node 16k experiments showed `2` and `4` commit shards reduced queue fill but increased physical commit count and commit-request tail latency, so shards remain default-off. The bench script keeps `WK_CLUSTER_COMMIT_COORDINATOR_SHARDS=0` and uses bounded ChannelV2 store append/apply workers for the current local high-QPS profile.
