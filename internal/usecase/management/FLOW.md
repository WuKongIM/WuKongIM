---
scope: package
summary: Builds entry-independent Manager read models and safety-gated orchestration over narrow cluster, runtime, and diagnostic ports.
---

# Management Usecase Flow

## Responsibility

`internal/usecase/management` owns Manager-facing validation, projections, and
orchestration without depending on HTTP or concrete infrastructure. It covers
node and Slot lifecycle, channels and messages, connections and users,
plugins, logs, diagnostics, DB inspection, Controller tasks, and operations MCP
administration.

`Options` is the composition facade. `App` groups its ports by node, Channel,
user, message, and operations concerns while keeping one stable usecase API.

## Boundaries

- `internal/access/manager` owns routes, authentication, HTTP parsing, status
  mapping, and public response shapes.
- Ports expose entry-independent DTOs. Local/remote selection and concrete
  cluster, storage, RPC, plugin, and runtime mechanics belong to `internal/infra`
  or `internal/app` adapters.
- Read models may combine one bounded set of authoritative sources, but they
  must not invent healthy runtime evidence from desired control state.
- Lifecycle and migration usecases submit validated intents; Controller, Slot
  Raft, and Channel runtimes execute them.

## Main Flows

1. Inventory and fanout requests read one control snapshot, join bounded live
   evidence, and return deterministic per-target rows with unknown, skipped,
   unavailable, and failed evidence explicit.
2. Channel, message, user, connection, plugin, log, and diagnostic operations
   validate Manager input and delegate through the narrow owning port.
3. Irreversible and batch operations build a deterministic plan from current
   evidence, recompute it before execution, verify revision, plan identity, and
   runtime proof, then submit one bounded control intent.

## Invariants and Failure Semantics

- Missing ports and missing required evidence are unavailable or unknown, not
  valid empty state. Desired peers and preferred leaders never fabricate live
  quorum or leadership.
- Irreversible operations fail closed. Final node removal requires the same
  status projection to prove lifecycle, health, Slot, task, Channel, and
  gateway/runtime drain safety, then carries its observed revision as a fence.
- The usecase never directly changes desired peers, Raft membership, durable
  Channel placement, message rows, process state, or filesystem contents.
- Plans and scans have stable ordering and hard page, item, concurrency, and
  time bounds suitable for 256 hash slots and large clusters.
- Read failures remain visible as bounded warnings, blocker reasons, partial
  rows, or typed unavailable errors. Mutation conflicts never degrade into an
  unfenced retry.
- Observers receive bounded classifications and durations only; node, UID,
  Channel, task, address, and credential identities are not metric labels.

## Read First

- [App and dependency groups](nodes.go)
- [Node lifecycle](node_lifecycle.go)
- [Scale-in safety gate](scale_in.go)
- [Slot leader transfer](slot_leader_transfer.go)
- [Business Channel orchestration](channels_biz.go)

## Update Triggers

Update this file when a management domain or port group is added or removed,
an authoritative source or safety gate changes, plan fencing changes, partial-
failure semantics change, or orchestration begins or stops owning a durable
operation.
