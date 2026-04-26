# Project Knowledge

## Channel Runtime

### Long-poll leader lease refresh
- A channel leader metadata refresh that only renews `LeaseUntil` must preserve existing leader-side lane sessions and follower cursors.
- Clearing the lane cursor on a lease-only refresh can make the next replication fetch start from offset `0`, preventing follower progress and HW from advancing for the next append.

## Cluster Membership

### Discovery baseline
- Static `WK_CLUSTER_NODES` remain a discovery baseline; controller node snapshots overlay them so early empty metadata reads do not break existing static clusters.
- Dynamic node join adds ordinary data nodes only; Controller Raft voter changes remain explicit future operator work.
