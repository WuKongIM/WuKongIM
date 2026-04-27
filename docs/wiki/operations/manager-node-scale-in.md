# Manager-Driven Node Scale-in Runbook

## Scope

- Supports one non-controller data/gateway node at a time.
- Does not remove physical Slots and must not call `RemoveSlot`.
- Does not scale Kubernetes automatically; the operator performs the final `kubectl scale` step.

## Required Safety

- Target node must be the Kubernetes StatefulSet tail Pod, verified by explicit mapping or operator confirmation.
- Wait for `status=ready_to_remove` and `safe_to_remove=true` before reducing replicas.
- Controller strict reads, Slot runtime views, active hash-slot migrations, onboarding jobs, reconcile tasks, and runtime connection/session counters must all be safe.
- `force_close_connections` is only an action request; it never overrides non-zero or unknown connection counters.

## Procedure

1. Call `POST /manager/nodes/{node_id}/scale-in/plan` with `confirm_statefulset_tail=true` and `expected_tail_node_id` set to the target.
2. If the report has no blocking reasons, call `POST /manager/nodes/{node_id}/scale-in/start` with the same tail confirmation.
3. Watch `GET /manager/nodes/{node_id}/scale-in/status` until blockers and progress counters converge.
4. Call `POST /manager/nodes/{node_id}/scale-in/advance` to perform bounded leader-transfer progress when needed.
5. Wait for active/closing online connections and gateway sessions to reach zero.
6. Only after `ready_to_remove`, manually scale the StatefulSet down by one replica.
7. Verify `/readyz`, manager node/slot status, and cluster health after Kubernetes removes the Pod.

## Rollback

- Before Pod removal, call `POST /manager/nodes/{node_id}/scale-in/cancel` to resume the node.
- After Pod removal, do not reuse the PVC or bring the Pod back without following the recovery procedure for the cluster state.
- If strict controller reads are unavailable, keep the node Draining or cancel; do not scale down based on stale local observations.
