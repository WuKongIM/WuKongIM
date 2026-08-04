# Chat Lifecycle Flow

`chatlifecycle` owns only the pure deterministic configuration model for the
formal or local chat-lifecycle workload. `profile` selects formal versus local
scale, while `mode` separately selects soak versus capacity coordination. It
contains no sockets, HTTP clients, worker loops, secrets, target mutation,
Docker, or host inspection.

```text
config
  -> deterministic plan
  -> worker runtime
  -> bounded snapshots
  -> coordinator verdict
```

The eventual plan and runtime must keep history-independent memory: generated
identity and relationship decisions derive from the stable run seed instead of
retaining unbounded activity history. Target mutation uses public benchmark APIs
and WKProto only. It exercises real sync behavior (`version=0`, runtime-owned
empty last-message sequences, bounded sync limit), never a synthetic history
shortcut.

Every deployment remains a cluster, including a single-node cluster. Planning
uses 12 logical Slot groups over 256 hash slots with three Slot and channel
replicas. The lifecycle runner does not start Docker; it connects to an
already-running target through declared non-secret observation endpoints. A
capacity-mode run requires the formal profile, a typed completed passing
72-hour aged checkpoint, and the fixed 2,000 start/recovery-rate staircase.

Service-node observation, worker control, host metrics, and API-pool endpoints
are absolute credential-free HTTP or HTTPS URLs without query or fragment.
Their duplicate identity lowercases the scheme and host, canonicalizes IP text
and default ports, and cleans the base path while preserving meaningful
non-root paths. Gateway endpoints are credential-free TCP `host:port` values;
their duplicate identity canonicalizes the host/IP and numeric port. API and
gateway pools must not resolve to the same canonical network authority.
