---
scope: package
summary: Adapts versioned node RPC frames to local authority, runtime, and management ports.
---

# internal/access/node Flow

## Responsibility

This package owns internal node-to-node RPC handlers, clients, bounded codecs,
version negotiation, and stable status mapping. It transports presence,
delivery, Channel append, lifecycle, backup, diagnostics, management, and
Operations MCP commands to local ports. It does not own routing, conflict,
retry, lifecycle-safety, or business policy.

## Boundaries

RPC service IDs come from the cluster transport. Request and response DTOs
adapt narrow contracts from use cases and runtimes; local implementations are
injected by `internal/app`. Origin-side orchestration chooses the target. The
receiver revalidates only local authority and fences required by the operation,
then calls the configured local port.

## Main Flows

```text
node RPC request
  -> service-specific versioned decoder and bounds
  -> local target/fence validation
  -> authority, runtime, or management port
  -> stable status and versioned response

Channel append forward
  -> exact authority target plus aligned commands
  -> local Channel authority admission only
  -> aligned append results or retryable route status

scheduled backup or restore
  -> small fenced control RPC
  -> node reads/writes shared repository directly
  -> counts and authenticated references only
```

## Invariants and Failure Semantics

- Every wire layout has a fixed magic/version. Encoders emit the latest
  version; decoders accept only explicitly supported older versions and reject
  unknown operations, malformed lengths, oversized collections, truncation,
  and trailing bytes.
- Manager connection RPC version 4 carries the node program version; version 3
  carries the true active total and freshness cursor for bounded remote-node
  pages, and version 2 remains an explicitly decoded compatibility layout
  without that pagination metadata. Connection list/detail reads and mutations
  remain on version 3 for rolling-upgrade compatibility; a version-4
  runtime-summary read from an older peer remains explicitly unavailable until
  that peer is upgraded.
- Channel append RPC never resolves routes, creates proxy Channel state,
  appends outside local authority, or runs post-commit effects elsewhere.
- Transport cancellation and unavailable-target failures map to stable typed
  caller errors without reordering active aligned items.
- Manager latest-message RPC preserves bounded scan saturation as its stable
  backpressure status instead of collapsing it into general unavailability.
- Presence batch lookups preserve input alignment and isolate group-scoped
  stale/rejected results. Compatibility fallback is limited to an explicit
  unsupported-operation response, never arbitrary transport failure.
- Plugin HTTP forwarding calls the node-local route port and must not recurse
  through `HTTPForward`.
- Large backup/archive data never crosses node RPC. Secrets, raw provider
  messages, credential ciphertext, and filesystem paths never cross it either.
- Operations MCP forwarding carries credential identity/digest, never the raw
  token. Profile capture requires one-time consumption of an owner-held lease.
- Reserved service IDs and existing byte layouts are compatibility contracts;
  reuse or incompatible edits require an explicit version transition.

## Read First

- [presence_rpc.go](presence_rpc.go)
- [channel_append_rpc.go](channel_append_rpc.go)
- [scheduled_backup_rpc.go](scheduled_backup_rpc.go)
- [manager_connection_rpc.go](manager_connection_rpc.go)
- [opsmcp_rpc.go](opsmcp_rpc.go)

## Update Triggers

- A service ID, magic version, codec bound, or compatibility fallback changes.
- Local fence revalidation or stable status/error mapping changes.
- A new RPC transports secrets, large payloads, or irreversible authority.
- Target selection, retry, or business policy moves into this adapter.
