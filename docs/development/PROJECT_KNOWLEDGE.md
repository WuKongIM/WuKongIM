# Project Knowledge

## Channel Runtime

### Long-poll leader lease refresh
- A channel leader metadata refresh that only renews `LeaseUntil` must preserve existing leader-side lane sessions and follower cursors.
- Clearing the lane cursor on a lease-only refresh can make the next replication fetch start from offset `0`, preventing follower progress and HW from advancing for the next append.
- Expired remote channel leader leases must be repaired by evaluating the current leader first; only renew the lease if that leader can still prove it is safe.

## Cluster Membership

### Discovery baseline
- Static `WK_CLUSTER_NODES` remain a discovery baseline; controller node snapshots overlay them so early empty metadata reads do not break existing static clusters.
- Static `WK_CLUSTER_NODES` addresses must be unique advertised endpoints; `0.0.0.0` is only a listen bind address and must not be published as a peer address.
- Static cluster heartbeats must publish `WK_CLUSTER_ADVERTISE_ADDR` or the local `WK_CLUSTER_NODES` address; never publish the bound listener address such as `[::]:7000`.
- Discovery must ignore controller metadata addresses that are unspecified bind endpoints and keep the static node address instead.
- Dynamic node join adds ordinary data nodes only; Controller Raft voter changes remain explicit future operator work.
- Dynamic join startup retries JoinCluster synchronously before app HTTP/gateway services start; non-blocking joining readiness is future lifecycle work.
- Controller state-machine join conflicts are stale no-ops; Join RPC prechecks still return explicit conflict errors to callers.
- Active data nodes added by dynamic join do not automatically receive Slot replicas or Leaders; operators must create and start a manager node onboarding job for explicit resource allocation.

## Controller

### Planning writes
- Production controller planning writes must go through Controller Raft proposals; direct Store writes are only for local tests or tools.
- Bootstrap planning rotates `Task.TargetNode` across DesiredPeers by SlotID, and cluster execution transfers the initialized Slot Leader to that target to avoid concentrated initial leader placement.

### Controller Raft transport
- Controller Raft shares the cluster transport server, so its wire message type must stay distinct from slot Raft and observation-hint message types.
- Inbound Controller Raft frames must be addressed to the local node and originate from a different node; drop looped or misrouted frames before calling `RawNode.Step`.
- Controller read RPCs can see `not leader` while Raft elects or fails over; keep those retryable read failures out of ERROR logs.

### Node scale-in
- Manager-driven node scale-in drains a node to `ready_to_remove`; it must not call physical Slot removal or Kubernetes scale-down directly.
- Scale-in manager reads require `cluster.node:r` and `cluster.slot:r`; start/advance/cancel require `cluster.node:w` and `cluster.slot:w`.
