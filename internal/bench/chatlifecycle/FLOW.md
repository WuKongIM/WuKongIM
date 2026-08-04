# Chat Lifecycle Flow

`chatlifecycle` owns only the pure deterministic configuration model for the
formal chat-lifecycle soak workload. It contains no sockets, HTTP clients,
worker loops, secrets, target mutation, Docker, or host inspection.

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
already-running target through declared non-secret observation endpoints.
