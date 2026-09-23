---
scope: package
summary: Defines bounded broker-scoped MQTT session and exact owner identities without credentials or runtime handles.
---

# MQTT Session Contracts Flow

## Responsibility

This package defines entry-neutral broker/ClientID keys and exact execution
owner identities. It owns no storage, routing, packet or socket behavior.

## Boundaries

- UID authentication and durable Session acquisition belong to usecases.
- Owner execution and connection-ID allocation belong to the local runtime.
- Node RPC encodes complete values without turning absence or timeout into proof.

## Main Flows

1. Validate bounded UTF-8 identity components before lookup or serialization.
2. Carry Session generation separately from connection owner generation.
3. Preserve node, unique registry boot and non-reused connection ID for isolation.

## Invariants and Failure Semantics

- Namespace/ClientID are independent of UID/device conflict policy.
- Identities contain no token, payload, concrete session or execution deadline.
- Reconstructed owner registries require a new boot identity, including restore.

## Read First

- [Owner identity](owner.go)
- [Execution contract](../../../docs/specs/mqtt-owner-execution.md)

## Update Triggers

Update when identity components, bounds or generation meanings change.
