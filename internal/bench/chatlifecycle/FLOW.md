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

`LocalConfig` is the reviewed three-node, three-worker shakeout baseline. It
keeps the formal topology and real sync request (`version=0`, `limit=500`,
`message_count=20`) while using 100 online users, 1,000 new users/day, 100
SENDs/s, an 80-person/20-group hot set, a fixed 16/3/0/1 group catalog with
1,000 members in the very-large group, a 500-channel node bound, 12 runtime
samples, two seconds/200 SENDs of burst credit, and 10/20/30-minute timeline
checkpoints. Its three service, worker, host-metrics, HTTP API, and TCP gateway
roles use replaceable, unique loopback declarations by default.

All profiles require the active group hot set to equal the checked group
catalog total, and the combined person/group hot set to fit the per-node active
channel allocation bound used by the planner. Catalog categories are each
bounded by 2,000, the positive catalog total cannot exceed 2,000, membership is
fixed, and very-large member/cadence metadata is present exactly when a
very-large group category is present. The declared worker count must equal the
worker observation count. The formal validator additionally retains the exact
2,000-group and 100,000-member reviewed shape.

Service-node observation, worker control, host metrics, and API-pool endpoints
are absolute credential-free HTTP or HTTPS URLs without query or fragment.
Their duplicate identity lowercases the scheme and host, canonicalizes IP text
and default ports, and cleans the base path while preserving meaningful
non-root paths. Gateway endpoints are credential-free TCP `host:port` values;
their duplicate identity canonicalizes the host/IP and numeric port. API and
gateway pools must not resolve to the same canonical network authority.
