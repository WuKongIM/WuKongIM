# test/e2ev2/control/slot_leader_transfer AGENTS

This scenario verifies single and batch manual Slot leader transfer through a
real static multi-node `cmd/wukongim` cluster.

## Rules

- Start real `cmd/wukongim` processes through `test/e2ev2/suite`.
- Trigger transfers only through the public manager HTTP routes, including the
  batch plan/execute routes.
- Observe task completion and actual Slot Raft leadership through
  `/manager/slots?node_id=...`.
- Keep assertions black-box; do not import `internalv2/app`,
  `internalv2/usecase`, or storage internals.
- Treat `target_node` as preferred only. Success is any legal non-source Slot
  Raft leader selected by Raft.
