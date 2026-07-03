# test/e2ev2/control/bootstrap_task AGENTS

This scenario verifies ControllerV2 bootstrap Slot tasks through a real
multi-node `cmd/wukongim` cluster.

## Assertions

- Observe task completion through public manager HTTP routes.
- Use `node_id` scoped Slot status when actual node-local Slot Raft evidence is
  required.
- Treat a ready multi-node cluster as a multi-node cluster, not as standalone
  processes.
