# pkg/hashslot Flow

## Responsibility

`pkg/hashslot` owns the logical hash-slot routing table, hash-slot migration
state encoding, key-to-hash-slot calculation, and deterministic hash-slot
rebalance planning.

## Boundaries

- This package is a neutral utility shared by legacy control-plane code,
  Slot FSM commands, and future canonical cluster packages.
- It may depend on `pkg/slot/multiraft` for physical Slot IDs.
- It must not import `pkg/cluster`, `pkg/controller`, `pkg/clusterv2`,
  `pkg/controllerv2`, `internal`, or `internalv2`.
