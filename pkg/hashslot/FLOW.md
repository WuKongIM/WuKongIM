---
scope: package
summary: Provides neutral Hash Slot routing tables, migration encoding, key hashing, and deterministic rebalance planning.
---

# Hash Slot Utility Flow

## Responsibility

This package owns logical Hash Slot routing state, migration encoding,
key-to-Hash-Slot calculation, and deterministic rebalance planning.
It does not execute cluster changes, persist plans, or own migration policy.

## Boundaries

- It is a neutral utility shared by control-plane code and Slot FSM commands.
- It may depend on `pkg/slot/multiraft` for physical Slot IDs.
- It must not import `pkg/cluster`, `pkg/controller`, or `internal` packages.

## Main Flows

1. Hash a stable key into the configured logical Hash Slot space.
2. Read or update the routing table and encode migration state.
3. Produce a deterministic rebalance plan from current table and target inputs.

## Invariants and Failure Semantics

- Identical inputs produce identical routing and rebalance output.
- Physical Slot identity is explicit and is not inferred from deployment size.
- Callers own cluster execution, fencing, persistence, and migration side effects.

## Read First

- [Hash Slot table](hashslottable.go)
- [Rebalance planner](rebalancer.go)

## Update Triggers

Update this file when key hashing, routing state, migration encoding, physical
Slot mapping, or rebalance determinism changes.
