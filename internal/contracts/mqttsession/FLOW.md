---
scope: package
summary: Defines bounded MQTT identities and body-free replay scheduling DTOs shared by usecase and runtime.
---

# MQTT Session Contracts Flow

## Responsibility

This package defines entry-neutral broker/ClientID keys and exact execution
owner identities, plus body-free replay cursors/results shared by usecase and
runtime. It owns no storage, routing, packet or socket behavior.

## Boundaries

- UID authentication and durable Session acquisition belong to usecases.
- Owner execution and connection-ID allocation belong to the local runtime.
- Node RPC encodes complete values without turning absence or timeout into proof.

## Main Flows

1. Validate bounded UTF-8 identity components before lookup or serialization.
2. Carry Session generation separately from connection owner generation.
3. Preserve node, unique registry boot and non-reused connection ID for isolation.
4. Carry bounded per-replica scan hints and cold scheduling passes without turning
   them into durable evidence; explicit target completion confirms original-source
   release, never shared-content GC, readiness or subscription admission.
5. Retirement continuations are exclusive with recovery hints and hold one immutable
   capture/floor; each turn revalidates consumers. Commit observations are not replica cleanup.

## Invariants and Failure Semantics

- Namespace/ClientID are independent of UID/device conflict policy.
- Identities contain no token, payload, concrete session or execution deadline.
- Reconstructed owner registries require a new boot identity, including restore.

## Read First

- [Owner identity](owner.go)
- [Replay scheduling DTOs](replay.go)
- [Retirement scheduling DTOs](replay_retirement.go)
- [Execution contract](../../../docs/specs/mqtt-owner-execution.md)

## Update Triggers

Update when identity components, bounds or generation meanings change.
