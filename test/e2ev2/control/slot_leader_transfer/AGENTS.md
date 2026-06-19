# test/e2ev2/control/slot_leader_transfer AGENTS

This scenario verifies manual Slot leader transfer through a real static
multi-node `cmd/wukongimv2` cluster.

## Rules

- Start real `cmd/wukongimv2` processes through `test/e2ev2/suite`.
- Trigger the transfer only through the public manager HTTP route.
- Observe task completion and actual Slot Raft leadership through
  `/manager/slots?node_id=...`.
- Keep assertions black-box; do not import `internalv2/app`,
  `internalv2/usecase`, or storage internals.
- Treat `target_node` as preferred only. Success is any legal non-source Slot
  Raft leader selected by Raft.
