# InternalV2 Dynamic Node Operations Runbook

## Safety Model

- Dynamic node process startup is outside `wkcli node`.
- `wkcli node` calls only public manager HTTP endpoints under `/manager/nodes*`.
- Joining nodes are not schedulable until activation succeeds.
- Onboarding and scale-in create bounded `slot_replica_move` work with
  `--max-slot-moves`; use `1` unless there is a measured reason to increase it.
- Final removal is fail-closed. If `safe_to_remove=false`, do not retry blindly;
  inspect the printed blockers first.

## Target Setup

```bash
go run ./cmd/wkcli context add prod-a \
  --server http://127.0.0.1:5001 \
  --description "primary manager endpoint" \
  --select
```

Use `--token` on every command when manager auth is enabled:

```bash
go run ./cmd/wkcli node ls --context prod-a --token "$WK_MANAGER_TOKEN"
```

## Add A Data Node

1. Start the new `cmd/wukongimv2` process with seed discovery and join token.

   The new process must use the static cluster seeds, not its own
   `WK_CLUSTER_NODES` single-node bootstrap. The process must publish a stable
   `WK_CLUSTER_ADVERTISE_ADDR`.

2. Wait for manager inventory to show `joining` and fresh health:

   ```bash
   go run ./cmd/wkcli node ls --context prod-a
   ```

   Required evidence:

   ```text
   join_state=joining
   schedulable=false
   health=fresh/alive
   runtime_ready=true
   ```

3. Activate the node:

   ```bash
   go run ./cmd/wkcli node activate 4 --context prod-a
   ```

4. Confirm it is active and schedulable:

   ```bash
   go run ./cmd/wkcli node ls --context prod-a
   ```

5. Move one bounded Slot replica onto the node:

   ```bash
   go run ./cmd/wkcli node onboarding plan 4 --context prod-a --max-slot-moves 1
   go run ./cmd/wkcli node onboarding start 4 --context prod-a --max-slot-moves 1
   go run ./cmd/wkcli node onboarding status 4 --context prod-a
   ```

   Repeat `advance` only after status shows no active onboarding task:

   ```bash
   go run ./cmd/wkcli node onboarding advance 4 --context prod-a --max-slot-moves 1
   ```

## Remove A Data Node

1. Mark the node leaving:

   ```bash
   go run ./cmd/wkcli node scale-in start 4 --context prod-a
   ```

2. Enable gateway drain:

   ```bash
   go run ./cmd/wkcli node scale-in drain 4 --context prod-a --draining=true
   ```

3. Drain Slot replicas with bounded moves:

   ```bash
   go run ./cmd/wkcli node scale-in plan 4 --context prod-a --max-slot-moves 1
   go run ./cmd/wkcli node scale-in advance 4 --context prod-a --max-slot-moves 1
   ```

4. Check final safety:

   ```bash
   go run ./cmd/wkcli node scale-in status 4 --context prod-a
   ```

   Required evidence before final removal:

   ```text
   safe_to_remove=true
   gateway draining=true
   accepting_new_sessions=false
   gateway_sessions=0
   active_online=0
   closing_online=0
   total_online=0
   pending_activations=0
   ```

5. Remove the node:

   ```bash
   go run ./cmd/wkcli node scale-in remove 4 --context prod-a
   ```

## Root-Cause Triage

- `target_health_stale`: check node process liveness, `/readyz`, manager
  health freshness, and control revision catch-up.
- `eligible_node_health_stale`: replacement nodes are not fresh enough for safe
  placement; inspect `wkcli node ls`.
- `blocked_by_slots`: run `scale-in plan` and `scale-in advance` with
  `--max-slot-moves 1`; wait for tasks to clear.
- `blocked_by_slot_leadership`: wait for Slot leadership to move or inspect
  manager Slot status.
- `blocked_by_tasks`: inspect `/manager/controller/tasks` before submitting
  more work.
- `blocked_by_channels` or `unknown_channel_inventory`: do not remove; channel
  runtime inventory could not prove the target is empty.
- `blocked_by_runtime_drain`: keep drain enabled and wait until gateway and
  online counters reach zero.
- `unknown_runtime` or `unknown_control_revision`: treat as unsafe; collect
  node logs, manager status, and readiness gate evidence.

## Local Release Gate

```bash
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile ops
```

Evidence is written under:

```text
data/dynamic-node-readiness-gate/<timestamp>/
```
