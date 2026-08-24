---
scope: package
summary: Owns entry-independent plugin desired state, candidate selection, hook orchestration, and PDK-compatible host RPC mapping.
---

# Plugin Usecase Flow

## Responsibility

`internal/usecase/plugin` manages node-local plugin desired state and observed
lifecycle, selects eligible hooks, maps entry-independent values to the PDK
wire model, and implements the business side of compatible plugin host RPCs.

It orchestrates synchronous Send hooks and post-commit Receive/PersistAfter
hooks while keeping process, transport, cluster, and append implementations
behind narrow ports.

## Boundaries

- `internal/access/plugin` owns host RPC decoding, body limits, timeouts, and
  wire responses.
- Process launch, sandbox paths, Unix sockets, and local runtime inventory
  belong to runtime adapters; cluster and remote-node routing belong to infra.
- Plugin-origin messages re-enter the normal message usecase. They do not
  bypass permission, authority, append, or `NoPersist` semantics.
- Receive and PersistAfter are best-effort post-commit effects and cannot alter
  SENDACK, durable append, membership, or online delivery results.

## Main Flows

1. Lifecycle operations validate identity, merge and persist node-local desired
   state, preserve secret redaction, register observed runtime state, refresh
   candidate caches, and notify only advertised config-update hooks.
2. Foreground hooks and host RPCs select eligible plugins in priority order,
   clone boundary payloads, restrict allowed mutation, and route message send,
   committed reads, cluster snapshots, conversations, and bounded HTTP through
   injected usecase ports.
3. Post-commit Receive and PersistAfter select eligible bound candidates under
   recursion, deduplication, and timeout fences; their independent failures do
   not change the durable or online-delivery outcome.

## Invariants and Failure Semantics

- Candidate order is priority descending then plugin number ascending. Desired
  disabled state always suppresses observed runtime availability.
- Send hooks fail closed unless `FailOpen` is explicitly configured; a plugin
  may change payload or reject, but cannot replace sender, Channel, session, or
  routing identity.
- Origin and hook-depth controls prevent unbounded plugin-send recursion while
  retaining the normal message path.
- Receive skips transient, command, scoped, incomplete, system-origin, and
  sender-recipient cases defined by the compatibility contract. One recipient
  failure does not discard independent siblings.
- Request, response, header, query, body, UID-batch, worker, timeout, and
  deduplication state are bounded. Payload ownership changes are explicit.
- Remote HTTP forward executes exactly one target-node local route hook;
  cluster fanout remains deliberately unsupported and fails before partial work.
- Secret values never reappear in Manager-facing desired-state projections or
  low-cardinality observations.

## Read First

- [App and ports](app.go)
- [Lifecycle and config](config_lifecycle.go)
- [Send hooks](send_hook.go)
- [Offline Receive hooks](receive.go)
- [Plugin invocation](invocation.go)

## Update Triggers

Update this file when desired-state ownership, lifecycle transitions,
candidate ordering, hook recursion or failure policy, host RPC ownership,
payload mutation, or post-commit eligibility changes.
