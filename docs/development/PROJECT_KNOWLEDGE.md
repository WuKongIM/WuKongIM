# Project Knowledge

## Channel Runtime

### Long-poll leader lease refresh
- A channel leader metadata refresh that only renews `LeaseUntil` must preserve existing leader-side lane sessions and follower cursors.
- Clearing the lane cursor on a lease-only refresh can make the next replication fetch start from offset `0`, preventing follower progress and HW from advancing for the next append.
- Expired remote channel leader leases must be repaired by evaluating the current leader first; only renew the lease if that leader can still prove it is safe.

## Cluster Membership

### Discovery baseline
- Static `WK_CLUSTER_NODES` remain a discovery baseline; controller node snapshots overlay them so early empty metadata reads do not break existing static clusters.
- Dynamic node join adds ordinary data nodes only; Controller Raft voter changes remain explicit future operator work.
- Dynamic join startup retries JoinCluster synchronously before app HTTP/gateway services start; non-blocking joining readiness is future lifecycle work.
- Controller state-machine join conflicts are stale no-ops; Join RPC prechecks still return explicit conflict errors to callers.
