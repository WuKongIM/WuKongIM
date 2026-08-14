---
scope: package
summary: Defines bounded entry-independent backup, repository, export, restore, and Controller coordination DTOs without policy or I/O.
---

# Backup Contracts Flow

## Responsibility

`internal/contracts/backup` contains the canonical DTOs shared by backup
usecases, runtime, node RPC, infrastructure adapters, and app wiring.
It does not execute backup policy, repository I/O, cluster routing, or restore.

## Boundaries

- Scheduling, overlap, retry, retention, restore transitions, Controller
  persistence, repository formats, and node-local storage work stay outside.
- Manager permissions, reauthentication, confirmation, and public projections
  remain in access/usecase layers.
- Repository credentials cross only as encrypted values; public DTOs expose at
  most credential presence.

## Main Flows

1. `ScheduledState` carries one revisioned plan, bounded active/history state,
   one leader-owned repository lease, verification, and Manager session epoch.
2. Export commands send bounded authority/topology intent to data nodes, which
   return counts and authenticated chunk-index references rather than payloads.
3. Restore commands carry exact archive/Slot/topology/activation identity and
   bounded per-replica evidence through maintenance phases.

## Invariants and Failure Semantics

- A job contains exactly one bounded progress row per logical hash Slot and is
  fenced by owner node/term. Archives are complete, never incremental cursors.
- Repository verification binds the complete effective repository and
  credential revision; changes invalidate verification without exposing secrets.
- Restore cancellation ends before switching begins.
- Cross-node repository failures use the bounded stable failure DTO; internal
  causes, bodies, payloads, embedded-secret endpoints, and credentials remain
  node-local.

## Read First

- [Scheduled state](scheduled.go)
- [Scheduled RPC](scheduled_rpc.go)
- [Repository failures](repository_error.go)

## Update Triggers

Update this file when shared DTO ownership, progress cardinality, repository
identity, export receipts, restore fencing, cancellation, or secret projection
changes.
