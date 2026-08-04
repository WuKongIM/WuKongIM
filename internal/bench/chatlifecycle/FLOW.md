# Chat Lifecycle Flow

`chatlifecycle` owns only the pure deterministic configuration and workload
planning model for the formal or local chat-lifecycle workload. `profile`
selects formal versus local scale, while `mode` separately selects soak versus
capacity coordination. It contains no sockets, HTTP clients, worker loops,
secrets, target mutation, Docker, or host inspection.

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

Identity planning uses zero-based worker IDs. Worker-local index `n` on worker
`w` maps to global index `n*workers+w`; division and remainder recover the
owner without a retained UID map. A lifecycle UID contains a bounded hashed
run/seed namespace plus the exact base-36 global index, so it is reversible and
collision-free within the run without leaking raw run-ID characters. All
deterministic choices use independent semantic-purpose hashes; introducing one
choice cannot consume or shift another choice's output. Bounded choices whose
range is not a power of two reject the biased hash prefix and derive retries
from a separate semantic domain plus attempt number.

Login identity, session bucket, and channel lifecycle class use independent
run-rotated ordinal cycles, giving exact 80/20, 25/50/20/5, and 60/25/10/5
shares without mutable PRNG state. Keyed unbiased draws select values inside
each duration or message-count range. At 250,000 new users per day the identity
growth rate is about 2.9 new users/second; because new users are 80% of logins,
the total login rate is about 3.6 logins/second.

Each new relationship plan has a finite two-to-eight-message initial burst over
five to thirty seconds and explicitly requires both endpoints online. Revisit
plans wait ten to sixty minutes and send two to five messages; rotating and
long plans contain only their bounded active durations. All lifecycle classes
stop scheduled activity and cool naturally. The model never emits polling or
keepalive work for a Channel runtime.

The person relationship graph is reconstructed from global indexes and keeps
no adjacency history. Each owner has a run-rotated repeating degree pattern
`3,4,4,5` and owns edges to the next consecutive indexes. Thus every four
owners create exactly 16 unique lower-to-higher relationships, every edge
becomes available when its higher endpoint arrives, and incoming reconstruction
checks only the previous five owners. Fixed-capacity results bound one user's
incoming plus outgoing conversations to ten.

Returning-login planning selects mature historical candidates rather than
claiming they are offline; offline admission remains worker-owned. Four of each
five login ordinals prefer candidates and real adjacent edges created within
the preceding `new_users_per_day` indexes, while one prefers history strictly
before that boundary. Before older history exists, an older preference is
explicitly reported as a fallback to available recent history. If neither
bucket contains a mature candidate, selection is explicitly unavailable and no
historical channel is invented.

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

Burst validation multiplies the nanosecond credit window by the per-second
send rate exactly, rejects non-integral message credit, and bounds the result
to the platform `int` range before comparing `max_global_burst`.

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
and default ports, removes one terminal DNS root dot, and cleans the base path
while preserving meaningful non-root paths. Root-only hosts are rejected.
Gateway endpoints are credential-free TCP `host:port` values;
their duplicate identity canonicalizes the host/IP and numeric port. API and
gateway pools must not resolve to the same canonical network authority.
