# Chat Lifecycle Flow

`chatlifecycle` owns the deterministic configuration and workload planning
model, narrow lifecycle-specific startup orchestration, bounded message
verification, and redacted evidence retention for the formal or local
chat-lifecycle workload. `profile` selects formal versus local scale, while
`mode` separately selects soak versus capacity coordination. It contains one
bounded lifecycle engine loop but no concrete sockets or HTTP clients, secrets,
target mutation, Docker, or host inspection; transport is supplied through
narrow interfaces.

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

Every login first completes WKProto CONNECT and then issues a newly constructed
product conversation-sync request with `version=0`, empty `last_msg_seqs`,
`msg_count=20`, `only_unread=0`, and `limit=500`. No response version, cursor,
or conversation state can seed a later login. CONNECT and HTTP sync latencies
are measured independently, and traffic is admitted only after the HTTP result
passes validation. A canceled or failed CONNECT never starts sync; a canceled,
failed, or invalid sync never becomes traffic-ready.

Conversation sync accepts at most 499 unique conversation identities. A result
with 500 or more rows is `harness_invalid` because the harness cannot prove that
the full directory fit in one response. Each recent message must carry the same
client-facing channel ID and type as its conversation (including peer IDs for
person channels), have a positive message sequence, and appear in strictly
descending sequence order. Duplicate conversations, mismatched recent identity,
duplicate/reascending recent sequences, malformed JSON, or invalid base64
payloads fail the login with bounded low-cardinality errors.

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
each duration or message-count range. Every session bucket has a positive
integer percentage, which bounds even a local profile to at most 100 buckets.
At 250,000 new users per day the identity growth rate is about 2.9 new
users/second; because new users are 80% of logins, the total login rate is about
3.6 logins/second.

Each new relationship plan has a finite two-to-eight-message initial burst over
five to thirty seconds and explicitly requires both endpoints online. Revisit
plans wait ten to sixty minutes and send two to five messages; rotating and
long plans contain only their bounded active durations. All lifecycle classes
stop scheduled activity and cool naturally. The model never emits polling or
keepalive work for a Channel runtime.

The primary SEND rate is one global integer budget, never one bucket per
worker. Cumulative weighted boundaries produce per-tick grants whose sum is
exactly the configured global rate; a rotating phase removes long-run worker
rounding drift. Each worker retains only its own two most recent grant
generations, so all retained credit sums to the single global two-second burst
without giving every worker a global-sized bucket. Capacity-rate changes are
staged for the next tick and discard old-rate credit rather than creating
retroactive token debt.

Primary traffic kind, payload size, and person direction use independent
run-rotated ordinal cycles. Formal cycles are exactly 90/10 person/group,
70/25/4/1 for 256 B/1 KiB/4 KiB/16 KiB, and 70/30 alternating/one-way.
Payloads start with a 104-byte versioned binary marker, so the smallest 256-byte
class carries run, logical-send, worker, sender, target, and stable-message
fingerprints plus strict length, reserved-byte, deterministic-padding, and
checksum validation. Run, sender, and target use 128-bit correctness
fingerprints (about 1.32e-26 birthday-collision probability at three million
identifiers); they are correlation evidence rather than authentication. Raw run
IDs and endpoint identities are never embedded.

One logical SEND deterministically owns one bounded `client_msg_no`. Attempt
zero has no retry delay; attempts one through three reuse that exact identity
with 100 ms, 500 ms, and 2 s bases plus deterministic nonnegative jitter in
`[0, base/5]`. A fourth retry is rejected and duration addition is checked for
overflow.

The concurrent verifier registers that attempt-independent identity in an
explicitly bounded pending map. Only a matching successful SENDACK with
positive server message identity completes it. An incomplete SEND accepts a
rejected SENDACK as a retry decision input only when both server identity fields
are zero; nonzero identity on rejection is invalid product evidence. Once a
SEND is acknowledged or terminal, every later ACK is duplicate/conflicting
product evidence regardless of its reason code. Explicit terminal completion
is also a product failure. Completed entries remain only until the worker calls
`ReleaseSend`, which provides a bounded duplicate/conflict discrimination
window. Unknown, duplicate, and conflicting completions use fixed reason codes
and redacted message fingerprints.

Every protocol-valid RECV is decoded once, reconstructed through the payload
marker and `TrafficModel`, then checked for person peer versus group channel
semantics and strictly increasing sequence per recipient/channel. Payload
decoding, deterministic identity reconstruction, and checksum/padding scans run
outside verifier state locks. SEND/correlation/deadline state and
receive/sequence state have independent locks; neither is held while calling
the narrow `RecvAcker`.

A sequence observation becomes ACK-confirmed only after `RecvAcker` succeeds.
Until then, an exact retransmission with the same recipient, channel, server
message identity, sequence, and marker identity retries RECVACK without being
counted as a duplicate. Reuse of that sequence for a different identity is
conflicting product evidence. Payload, identity, or sequence validation
failures are still acknowledged when the server message identity is positive;
only nil packets or packets without trustworthy positive server IDs are not
acknowledged. Raw, context, and otherwise unclassified RECVACK errors default
to `harness_invalid`; only the explicit closed product-error wrapper attributes
a RECVACK failure to the product. Evidence never copies the underlying error.

P3.4 must feed each recipient from exactly one session drain in wire order.
Logout must cancel and join that drain before calling `ReleaseRecipient`, which
deletes the session's bounded monotonic state instead of accumulating historical
channels.

`SessionPool` owns only traffic-ready online sessions. Its factory receives a
deterministic per-UID CONNECT token, creates a fresh client for every login, and
then delegates CONNECT-before-version-zero-sync semantics to `RunLoginSync`.
The narrow `WKProtoSessionAdapter` delegates CONNECT, SEND, frame reads,
RECVACK, close, and numeric queue gauges to the existing non-dropping
`internal/bench/wkproto.Client`; it does not recreate protocol pumps.
Independent login I/O runs concurrently under one explicit starting-session
capacity and retains only the UIDs whose CONNECT/sync is active; capacity
exhaustion is harness-invalid. Only a validated sync starts the recipient's
sole ordered frame drain. The session factory, CONNECT, sync, and drain are
children of the active engine-generation context; stopping a generation fences
new admission, cancels that context, then joins startup work and drains.
Expected logout and expiry first remove online admission, then cancel and close
the socket, join the drain, and finally release recipient sequence state. The
WKProto result queue distinguishes a non-terminal asynchronous SEND publication
error, which keeps the same drain online and returns the stable
`client_msg_no` to the engine-owned retry state, from a terminal remote reader
exit, which atomically removes online ownership, records product evidence, and
signals a scheduler replacement. Unknown unexpected read exits remain bounded
`session_read_failed` harness evidence. The pool's UID and user-index routing
indexes contain current online sessions only and allocate no per-lookup
history.

`TrafficGenerator` streams, rather than retains, each global per-second grant.
It reuses `RateAllocator`, `TrafficModel`, and `GroupCatalog` to preserve the
exact primary traffic, payload, direction, and fixed group-target cycles. A
grant carries worker, generation-scoped logical position, traffic class, and
payload class but deliberately has no sender, packet, or claimed online route.
The engine binds person grants to eligible lifecycle activity and group grants
to a currently online fixed-directory member. The very-large group remains an
independently counted one-per-minute canary. The generator has no product
metadata or runtime mutation interface.

The private session scheduler derives checked rational login credit from
`new_users_per_day` and the 80% new share, which is about 3.6 total logins per
second in the formal profile. Bootstrap substitutes new identities until the
online target is first reached. Steady scheduling preserves the exact 80/20
planned, admitted, and completed split, uses `ReturningCandidate` for real
offline history, schedules bounded cold revisits on those old edges, and
replaces expiry or unexpected terminal exits. `Engine.Step` is the narrow
bounded orchestration boundary; aggregate snapshots expose planned, admitted,
completed, skipped, expired, and replacement counts without exposing scheduler
state.

`Engine` owns one bounded command loop for the active generation. One activity
min-heap holds relationship SEND eligibility, a runtime min-heap holds granted
SEND, attempt-timeout, and lifecycle deadlines, the indexed retry heap holds at
most one approved retry per logical message, and the inflight map is explicitly
capacity-bounded. A separate bounded completion queue lets ordered session
drains report SENDACKs under backpressure without competing with control
commands; long clock advances consume completions between scheduled work, and
shutdown joins drains before its final completion barrier. All heaps share one
checked future-work capacity except the retry heap, which has its own explicit
capacity. New-user
observation reconstructs only the prior five possible relationship owners,
schedules an initial burst only while both sessions are online, and retains at
most one lifecycle deadline for revisit or natural cooling. Rotating and long
channels additionally occupy a fixed active array and swap-delete index capped
by the configured person hot set; primary person grants keep those channels hot
only until their 20-40 minute or 2-4 hour deadline, after which newly activated
relationships reuse the released positions. Due relationship
activity cannot be sent by clock advancement alone: a person grant from the
single global tick substitutes its target while retaining the grant's worker,
logical ordinal, payload class, and primary denominator. Initial and revisit
messages use distinct generation-scoped lifecycle and revisit identity domains;
primary, group, and canary work have their own domains, so restarts and repeated
activation cannot reuse `client_msg_no`.
Revisit timers that require cold-runtime evidence also have one bounded active
channel index; only an explicit prior all-node cold approval can let the timer
add revisit activity, and expiry physically removes that index.
Person routing always requires an online sender. For the verifier's exact one
position in every 100 logical sends, it also requires an online target; other
person sends may keep a channel hot while its peer is offline. A sampled group
or canary send requires a distinct second online fixed-directory member. A
missing eligible route is harness-invalid under-delivery before SEND
registration and therefore cannot become a retry or product terminal result.
No historical user or channel owns a goroutine, timer, or retained map row.

Attempt zero plus retries one through three reuse the same Phase 2 logical
identity. Timeouts and closed temporary SENDACK reasons schedule the existing
100 ms, 500 ms, and 2 s deterministic delays; non-retriable SENDACK reasons
complete immediately. A late successful SENDACK removes a scheduled retry in
O(log n), and every accepted SENDACK physically removes that attempt's timeout
heap entry. Work queue, command queue, retry heap, inflight, or per-advance CPU
budget saturation is closed `harness_invalid` evidence and never becomes a
product terminal result. A new engine generation starts only after prior session
drains join, then clears bounded verifier indexes, counters, evidence, allocator
credit, and queue ownership. The generation number remains in the checked
logical identity prefix, so no first-run identity reaches the second run even
though per-domain ordinals restart from zero.

Exact delivery correlation uses one run-keyed position in every 100 logical
sends per worker. A sampled entry has one map row and one indexed min-heap
deadline; successful ACK-plus-RECV delivery and deadline expiry both physically
remove both indexes. A terminal SEND remains until that deadline so expiry also
records its confirmed sampled loss. RECV correlation is observed before
sequence-capacity admission, so a saturated sequence tracker cannot manufacture
sampled loss. A positive successful SENDACK updates correlation even after the
SEND was terminal, completed, released, or otherwise unknown; its independent
duplicate/conflict/unknown completion result remains product evidence.
Pending, sequence, and correlation capacity exhaustion is `harness_invalid`,
while loss, corruption, duplicate delivery, sequence regression, and terminal
send failure are `product_failure`. Aggregate counters and per-class fixed
first/last redacted examples are mutex-protected, deeply copied in snapshots,
and bounded independently of elapsed run history. Product failure takes
precedence and cannot be cleared by later success or harness evidence.

The person relationship graph is reconstructed from global indexes and keeps
no adjacency history. Each owner has a run-rotated repeating degree pattern
`3,4,4,5` and owns edges to the next consecutive indexes. Thus every four
owners create exactly 16 unique lower-to-higher relationships, every edge
becomes available when its higher endpoint arrives, and incoming reconstruction
checks only the previous five owners. Fixed-capacity results bound one user's
incoming plus outgoing conversations to ten.

Fast unit allocation gates cover UID round trips, five-edge reconstruction,
payload choice, login/channel schedules, and one-at-a-time group/member
reconstruction. UID, group ID, peer, and canonical person-channel strings are
necessarily transient allocations because they are the protocol-visible
result; their per-operation budgets include small compiler/runtime headroom but
do not permit retained history or repeated owner-UID formatting. A formal
250,000-user/1,000,000-edge scanner retains only counters and a checksum, also
reconstructs payload, schedule, group, and one group member at a time, and
compares repeated post-GC retained heap/object deltas with a much smaller scan.
The scanner and post-GC `KeepAlive` use the same fixture pointer, so model state
mutated during reconstruction cannot disappear before measurement. Its 128 KiB
relative heap allowance is calibrated through the same warm-up, scan, double-GC,
three-sample-median path with a test-only one-byte-per-user retained slice: the
245,000-user scale difference must trip the heap gate. The complementary object
allowance detects many small live objects that byte growth alone could obscure.
Together these gates detect history-sized slices/maps without constructing a
100,000-member slice or relying on a machine-specific absolute heap size.

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

The fixed formal group catalog contains 1,600 small, 300 medium, 99 large, and
one 100,000-member very-large group. A group descriptor retains one checked
member base plus the fixed catalog-size stride and reconstructs one UID at a
time. Member zero is the catalog index, so every class intersects the initial
online roster, while later members span deterministic arrival cohorts. Even the
largest group never allocates a membership slice or history-sized map. Primary group targets use
an exact 80/15/5 small/medium/large cycle. The very-large group is reachable
only through a separately reported one-per-minute canary and is excluded from
the 2,000 SEND/s denominator. When a local catalog omits a primary class, its
weight is deterministically omitted and the remaining available weights are
normalized; the canary is never promoted into primary traffic. Fixed group
channels add no historical-channel growth, leaving the formal hot set at
8,000 person plus 2,000 group channels.

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
