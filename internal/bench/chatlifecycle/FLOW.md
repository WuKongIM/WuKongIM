# Chat Lifecycle Flow

`chatlifecycle` owns the deterministic configuration and workload planning
model, narrow lifecycle-specific startup orchestration, bounded message
verification, and redacted evidence retention for the formal, rehearsal, or
local chat-lifecycle workload. `profile` selects formal versus local scale,
`mode` separately selects soak versus capacity coordination, and `stage`
separates a formal evidence claim from a full-scale bounded rehearsal or local
shakeout. It contains one
bounded lifecycle engine loop; the production worker composition adapts the
existing target HTTP and WKProto clients through the same narrow interfaces
used by tests. It does not recreate transport pumps, persist secrets, mutate
targets outside public APIs, invoke container orchestration, or inspect host
internals.

```text
config
  -> deterministic plan
  -> worker runtime
  -> bounded snapshots
  -> coordinator verdict
```

`LoadConfig` accepts exactly one strict YAML document, rejects unknown fields,
and validates the complete configuration before any network request.
`wkbench validate chat-lifecycle --config` exposes that same parser as a
network-free deployment readiness check and additionally requires the formal
Soak profile.
`ReadReport` applies the same fail-closed rule to one bounded JSON checkpoint,
including its schema, fence, time-window, evidence, and verdict invariants.
Neither persisted input contains credentials. The `wkbench` command adapters
load these contracts and resolve credentials separately before composing the
production clients; scheduling, lifecycle, capacity, and verdict decisions
remain in this package.

`wkbench worker --mode chat-lifecycle` selects a dedicated control server; the
default worker mode still uses the generic worker server. All lifecycle
endpoints, including health and info, require one Bearer token checked with a
constant-time comparison. Every mutation carries a nonempty run ID,
assignment ID, and positive generation. An assignment validates the complete
`Config` plus worker ownership before moving through
`unassigned -> assigned -> running -> stopping -> final`; active duplicate
assignments, stale fences, and illegal phase transitions use a closed error
vocabulary. Protocol version 2 added coordinator-owned grant delivery, bounded
readiness in status, and the assignment capability that disables worker-local
primary-rate release. Protocol version 4 additionally exposes only the active
grant sequence and the latest cached grant sequence/failure/runtime code in
status, so a caller deadline cannot erase a later generation-owned terminal
classification. Preflight rejects earlier versions before setup or assignment
mutation. Polling or a request disconnect never acts as a lease. Explicit
stop starts one server-owned bounded drain detached from the request, joins the
existing Engine, caches one identity-free final snapshot, and returns that
same snapshot for matching retries. An unexpected active-generation exit
publishes a process signal so the dedicated worker server shuts down and exits
nonzero. Finalization merges evidence and harness classification through one
closed precedence rule: `product_failure` outranks `harness_invalid`, which
outranks empty. Drain timeout, unexpected exit, snapshot failure, and invalid
snapshot fallback each add their own saturating harness failure and preserve
their flags; only a failed drain whose own context or result reached its
deadline sets `drain_timed_out`. They never downgrade prior product evidence.
The final Evidence and Harness classification fields always contain the same
merged value.

Assignment installation is linearized with its request context while the
worker state lock is held across generation construction. Cancellation before
or immediately after construction leaves the server unassigned and stops any
constructed generation exactly once. `Start` receives the request context; a
cancellation that races a successful return installs a server-owned no-drain
stop task and moves through `stopping -> final`, so a stopped generation can
never remain restartable in `assigned`.

A matching stop is also valid while assigned, including after `Start` fails.
That path skips drain because no Engine generation is running, performs one
idempotent generation cleanup, caches `final`, and permits only a strictly
higher later assignment. Snapshot, checkpoint, and rate handlers capture the
generation pointer, phase, and fence under the server lock, call the
context-aware generation outside the lock, and recheck all three before
handling either a successful or failed result. A late error from a stopped,
reassigned generation is therefore a fence mismatch, never a runtime failure
attributed to the current generation. Request cancellation therefore ends
owner waits without retaining goroutines, while health and status remain
independent of a slow generation call. Late old-generation snapshot,
checkpoint, and rate results are discarded rather than overlaid with a newer
fence. The grant handler additionally serializes one in-flight sequence per
generation. An exact duplicate joins or returns the stable cached result;
stale, gap, changed-payload, state, and fence mismatches fail closed. Once an
external grant crosses engine admission it is accepted even when later work
returns classified or fatal failure, so delivery retry cannot advance the
generator a second time or hide a partially emitted prefix. Cancellation
before engine admission clears the in-flight request without advancing or
caching its sequence, so the exact sequence can be transported again. Every
post-admission success or error remains a stable cached replay boundary.
Classified runtime failures add only their closed `RuntimeFailureCode` to the
generic `runtime_failure` response; raw errors never cross the protocol. Exact
duplicate replay returns the same code. A failed coordinator grant retains the
lowest worker-ID classified code in its bounded result and unavailable-report
terminal summary, so finalization does not erase the worker cause. When the
one-second measured grant request expires first, the coordinator never retries
that mutation: it performs one bounded status-only poll while the exact
sequence remains in flight and retains the cached late runtime code before
worker cleanup. The same exact cached sequence remains admissible if the
generation-ending failure has already moved the worker from running to final;
an active sequence in final is invalid. Other fence, sequence, phase, or
runtime-code projections are ignored rather than copied into evidence.
If an admitted external grant causes generation-terminal teardown, the Done
watcher first fences every new control mutation, waits for that one in-flight
grant to commit its admitted result, and only then publishes the unexpected
final snapshot. Concurrent exact duplicates join that commit, and the exact
cached grant remains replayable after finalization; changed or later grants
remain rejected. This ordering prevents an unexpected-exit phase transition
from turning an admitted terminal result into a fence mismatch.
Engine snapshot,
consistent worker-runtime snapshot, rate update, and bounded drain advancement
expose cancelable forms;
their existing background wrappers retain their prior semantics. A queued
rate command rechecks both caller context and Engine generation before mutating
the allocator. Public Advance crosses a cancellation-aware owner admission
fence, reserves one entry in the worker-online-target-bounded cleanup queue,
detaches and cancels session expiry, and commits the subsequent owner clock,
correlation, completion, retry, and lifecycle advancement under the serial Step
lock. One generation-owned cleanup loop closes sockets, joins drains and
heartbeats, releases verifier state, and removes closing tombstones. The serial
Step boundary never joins that transport work, so coordinator Grant and later
owner work remain available to consume the bounded, non-dropping SENDACK
completion queue. The invariant `online + starting + closing <= online_target`
delays replacement admission while cleanup retains a tombstone, permanently
reserving enough cleanup capacity for every routable session. Engine stop joins
the cleanup loop before closing the remaining online sessions. Public Tick acquires a generation-bound
lease before waiting on the same serial time boundary; Step's private Tick
inherits the enclosing Step lease and does not lock or lease again. The owner
admission atomically rejects a requested time earlier than its committed time
with a classified harness failure before any session, scheduler, generator, or
owner-state mutation; equal time is valid. Caller
cancellation wins only before the post-admission commit check; after commit the
transaction is controlled by generation lifetime. An Advance canceled while
queued therefore leaves session ownership, deadlines, clocks, heaps, counters,
correlations, and evidence unchanged, and every late response uses a one-slot
owner-safe channel.
Engine stop fences control admission, cancels the generation, joins admitted
step/login/session/Tick callers, and then crosses an owner-command barrier
before it closes sessions; an old Tick waiting on the serial time boundary or
owner queue therefore cannot enter a later generation, and a canceled caller
cannot leave SEND using a client that teardown has already closed. Session
queue gauges receive the merged caller-plus-generation context, so canceled
polling cannot pin that owner barrier ahead of socket cleanup.

Request bodies, client responses, and server responses have fixed byte caps
and strict JSON schemas. A running, exactly fenced worker may lease at most
1,200 current revisit candidates. The Engine reconstructs those rows only from
a fixed owner-loop primary index of 12 logical-Slot buckets with at most 100
entries per bucket; it never scans the potentially much larger live timer map
or standby state while leasing. A first successful SENDACK offers the exact
current timer token/version to its bucket. Each Slot also owns a min-heap of
overflow standbys, while the aggregate primary-plus-standby count is bounded by
Engine `WorkCapacity`; every production-eligible live timer is in exactly one
tier. The primary bucket deterministically retains the 100 earliest due timers
by due time, canonical channel ID, and token. Removing a primary immediately
promotes the best valid same-Slot standby, while invalidated, exhausted, or ABA
stale work cannot re-enter. Approval, completion, expiry, replacement, and
later activity remove or rebalance the exact pointer/token/version entry. A timer
invalidated after approval or exhausted at the activity-version boundary never
re-enters or passes admission, and still fails explicitly at its due time. Lease
copies and scans at most 1,200 entries and sorts only after leaving the owner
loop. It includes the highest successful initial SENDACK sequence and last
activity time retained on each timer; completed and expired timers disappear
from live timer and candidate state through the existing cleanup. The sole
completed-state exception is the bounded approval replay tombstone described
below; it retains no raw channel identity or timer work.
Each transient row carries a canonical person-channel ID, recomputed physical
hash slot in the 256-slot space, its owning Slot Raft Group, quiet lower/upper
bounds, deterministic reheat time, a generation-local timer token, and a
post-activity version. The token is unique and stable across timer deferral;
every successful SENDACK advances the version and invalidates any older
quiet-window lease. Both values are transient and never enter snapshots or
reports. Worker protocol validation never trusts the declared hash slot. The
current worker generation uses a strictly validated
continuous 256-to-12 assignment for the reviewed no-migration execution
profile. A lease whose assignment differs from the mapping used to build the
fixed Engine index is harness-invalid. The immutable assignment constructor and
standalone cohort selector can validate another complete live 256-entry mapping,
without assuming equal one-twelfth distribution or modulo ownership, but
preflight does not yet transmit and install that mapping into the worker index.
A migration-active lifecycle proof therefore requires future coordinator
integration; this module alone does not claim that dynamic migration has been
proved.

Every ten minutes, starting ten minutes after the measured-run boundary, the
independent lifecycle-proof module selects exactly 1,200 rows: 100 for each of
the 12 Slot Raft Groups. It prefers revisit timers already proven loaded and
rejects duplicates, malformed identities, a quiet window not exceeding the
natural five-minute idle interval, or an undersupplied Slot cohort as harness
invalid. One proof owns at most one cohort. Explicit runtime probes use only
`ProbeChannelRuntimeAll`, batch at no more than 1,200 identities, require three
distinct service nodes, and have bounded concurrency and per-request contexts.
Each batch is normalized by candidate index and sorted node ID, then all
transient rows are merged in O(nodes × candidates) and applied through one
atomic proof observation. Any malformed or failed batch prevents the entire
poll from advancing; no public result retains those raw rows. The interface
deliberately exposes no eviction operation, and probe latency or transport
failure is recorded separately from product transition evidence.

The proof requires all three runtimes active with exactly one leader and
monotonic LEO/HW/CheckpointHW, then all three naturally missing, then all three active after
reheat with sequence strictly above the initial sequence. Closing/error state,
partial reload, a stuck loaded or partially cooled runtime at the quiet
deadline, role disagreement, watermark regression, or sequence reset is
product failure. Before that deadline, replicas may naturally disappear at
different polls; partial cooling is not yet cold eligible, and a missing
replica becoming active again without reheat is a product transition failure.
Product failures use the fixed identity-free reasons `initial_load`,
`runtime_state`, `role_disagreement`, `watermark_regression`,
`continued_loading`, `premature_absence`, `reheat_timeout`, `partial_reheat`,
`sequence_proof`, `unexpected_reload`, and `control_transition`. The snapshot's
fixed reason counters always total its product-failure counter; an atomic batch
rollback counts only the triggering failure once, and neither errors nor JSON
evidence retain candidate IDs.
After one candidate reaches complete, that candidate is absorbing before any
generic runtime-status, role, or watermark validation: later fixed-cohort polls
may still carry its row, but missing, active, closing, or error rows cannot
mutate its retained watermarks, completion counters, or failure evidence. Other
candidates in the same cohort continue through their independent phases, so a
staggered peer may still complete normally.
Every non-missing partial-cooling row advances all three retained watermarks, so
a later CheckpointHW regression cannot hide between staggered replica exits.
CheckpointHW regression and invalid HW/Checkpoint ordering always classify as
`watermark_regression`; only LEO/HW reset during reload is `sequence_proof`.
Only the same candidate's all-node absence makes it cold-latency eligible. Approval
travels over a second strict fenced worker control call whose response does not
echo the channel ID; the server delegates to `engineWorkerGeneration`, which
calls `ApproveColdRevisitContext`. Owner admission requires the exact canonical
ID, timer token, and activity version, so a stale lease cannot approve a
same-channel replacement timer or a timer with newer activity; exact replay is
idempotent. While the timer is live, its current exact token/version and
`coldConfirmed` bit are the replay state; live approvals consume no completed
tombstone capacity. Immediately before the owner deletes that live timer, it
atomically records a generation-bound completed replay tombstone; the successful
eligible path then admits its real reheat SEND. The tombstone is keyed by the
generation-wide unique timer token and contains
only the activity version, a SHA-256 digest of the canonical channel ID, and a
one-minute expiry. A reverse digest-to-token index rejects same-channel ABA
replacement without a scan. Both maps are capped at 7,200 entries: one
1,200-candidate cohort times the ceiling of the 60-minute maximum revisit delay
over the ten-minute proof cadence, covering six full cohorts that can complete
together even on one worker. Capacity pressure performs one bounded expired-row
scan; if the maps remain full, completion is harness-invalid and leaves the live
timer intact without executing reheat. A full non-expired scan is attempted at
most once by that `Advance`; saturation then stops processing later due work and
is terminal for the worker generation. Generation shutdown runs `Engine.Stop`
to clear live indexes and any heap work already popped into owner state.
Autonomous tick termination stops the engine without joining its own tick loop;
external grant termination cancels ticks, stops the engine, joins the tick loop,
and only then publishes terminal completion. The bounded scan count is an
owner-only CPU audit value and is not exposed by snapshots or reports. Start and
Stop reset both replay maps. This
bounded digest tombstone is the only completed approval state. Activity after
approval clears live admission and records a dedicated bounded harness failure,
and the invalidated timer also fails explicitly rather than silently dropping
at its due time. The approval only unlocks the existing deterministic revisit
timer. The proof intentionally allows early approval only after the all-node
cold observation and strictly before the deterministic `ReheatAt` instant. The
serialized owner reads its clock at admission, so a request queued before the
boundary is rejected if it executes at or after due; that rejection neither
confirms nor removes the indexed timer. A first pre-boundary approval remains
idempotently true from live state through due, then from its completed tombstone
only inside the bounded one-minute retry window, without performing admission
again. Approval at or after that instant, or another absent
observation at that instant without approval, is harness-invalid. At its due time the ordinary
Engine/WKProto SEND path performs the real reheat, and reheat completion latency
uses that due instant as its baseline; control code never manufactures a
sequence. The post-reheat probe supplies the sequence-continuity proof.

Metadata-create parsing accepts only the exact `slot_id` and closed `result`
labels, logical Slot IDs 1 through 12, exact integer counters, and at most one
sample for each Slot/result tuple. The Slot 1 zero baselines preserve all three
global result totals; production startup additionally materializes every result
for all 12 logical Slot groups, while the parser retains absent-as-zero
compatibility. Accounting
accepts exactly three service-node scrapes and uses checked integer arithmetic
to reconcile their fixed 12-Slot vectors. It folds separate bounded 256-hash-slot
person-edge and prepared-group expectations through the current immutable
assignment, then requires every logical Slot's `created` counter to cover its
marked expectation. A provisional product mismatch is rescraped at most five
times across a 100-millisecond context-bounded settle window before the
accounting snapshot is committed; malformed metrics fail immediately and a
stable deficit remains product failure. Per-Slot counters may not regress, `error` must remain zero,
and `already_existing` may increase. A deficit in any Slot is product failure
even when another Slot has excess creates. Every structurally valid checkpoint,
including product-failing evidence, is retained for the final report. The first
product-failing accounting snapshot is sticky through final reconciliation so
a later counter catch-up cannot erase the correctness proof. The sum
of positive per-Slot creates above the marked expectation is retained as
`external_demo_activity` and does not fail the run; their host resource cost
remains visible to the ordinary observers. This accounting contract is report
schema v3; the strict reader intentionally rejects legacy report schemas. The metric
and all fixed-array aggregate snapshots remain low-cardinality and carry no
channel label.

Worker snapshots contain only scalar aggregates, including the fixed terminal
SEND and retry-exhaustion reason breakdown, fixed arrays, and the verifier's
at-most-four evidence classes with at most 64
first and 64 last redacted examples per class. No snapshot, checkpoint, report,
or durable evidence enumerates a UID or channel. Raw candidate identities exist
only in the bounded authenticated lease, probe, and approval request and are
never echoed by approval or copied into aggregate results. Checkpoint reads engine and generator counters through one
engine-owner command; it neither pauses nor restarts workload generation.
The worker client normalizes a request or response-body error to the supplied
context error only when the transport error causally wraps that exact context
error. A non-context request error remains the original transport error even if
the caller is canceled just afterward; an ordinary response-body read error
retains the stable `ErrWorkerResponse` classification.

The assignment generation is the exact checked `Engine` generation rather
than an HTTP-only snapshot overlay. Zero, overflow, reuse, and rollback are
rejected, while the ordinary in-process `Start` API retains its next-generation
behavior. A fresh worker generation initially advances login scheduling with a
nil traffic demand. A non-coordinator assignment retains the original local
allocator behavior after the local online target is observed. A coordinator-
grant assignment never releases that local allocator: its autonomous one-second
step advances login, sync, expiry, retries, and session work, publishes one
sticky atomic readiness bit after the synchronized online target, and accepts
primary work only through the sequenced grant endpoint. Online ownership exists
only after CONNECT and full sync succeed. The target and
all-worker grant vector are immutable assignment-derived generation fields, so
the tick hot path neither builds a full worker snapshot nor calls any session
`QueueSnapshot`. Coordinator Snapshot and Checkpoint retain that complete queue
projection. The traffic-start latch remains set across later churn.
Consequently a long bootstrap can retain at most the configured two-second
burst and cannot accumulate elapsed-time debt. Classified product or harness
workload evidence is recorded without terminating the worker command loop. A
classified owner-clock rollback instead fails before workload mutation, stops
the joined engine, and is published through `Done`; any other unclassified
fatal runtime error follows that same unexpected-exit path.

The eventual plan and runtime must keep history-independent memory: generated
identity and relationship decisions derive from the stable run seed instead of
retaining unbounded activity history. Target mutation uses public benchmark APIs
and WKProto only. It exercises real membership-directory sync behavior
(`completed_coverage=0`, an empty initial cursor, bounded 200-candidate pages,
and bounded unresolved retries), never a synthetic history shortcut.

Every login first completes WKProto CONNECT and then issues a newly constructed
product `/conversation/list` request with `completed_coverage=0`, empty cursor,
and page limit 200. The target client follows every opaque cursor until
`done=true`, deduplicates cross-page moves, and retries unresolved keys through
bounded `/conversation/retry` batches. No coverage, cursor, or conversation
state can seed a later login. CONNECT and HTTP sync latencies are measured
independently, and traffic is admitted only after the complete HTTP pass
validates. A canceled or failed CONNECT never starts sync; a canceled, failed,
incomplete, or invalid sync never becomes traffic-ready.

Latency snapshots use one fixed 16-bucket layout with explicit bounds at zero,
1/2/5/10/20/50/100/200/500 milliseconds, and 1/2/5/10/30/60 seconds. Negative
fake-clock movement is ignored, zero duration enters the zero bucket, and
values beyond 60 seconds enter the final overflow bucket. Counts, nanosecond
sums, and bucket counts saturate rather than wrap; maximum latency is retained.
`SessionPool` accumulates real factory, CONNECT, and full-sync outcomes for the
whole generation. CONNECT and sync each expose started, completed, failed, and
canceled counters; factory failure and cancellation remain separate. Every
started CONNECT or sync contributes to the same fixed latency histogram whether
it succeeds, fails transport, fails validation, or is canceled. Worker
`sync.failures` means actual failed sync stages only and never aliases scheduler
`LoginSkipped`; unavailable candidates and reservation conflicts remain
scheduler skips without manufacturing startup failures. Startup operation
errors expose only a closed factory/connect/sync stage, a closed reason, and
classification. Factory, CONNECT, and sync transport failures record bounded
harness evidence with stable stage/code pairs. Sync validation preserves its
existing product-versus-harness ownership, while cancellation records counters
but no expected-stop evidence. Only cancellation of the supplied generation or
caller context is expected cancellation; a client-owned timeout returned while
that context remains live is a transport failure. No evidence or public error
contains the raw transport cause or UID.

The ordered drain supplies its `SessionClock` instant to `Verifier`, which
measures registered-at through the first successful SENDACK and the complete
RECVACK transport call. Legacy verifier methods remain verification-only and
never read wall time; deterministic tests and the production pool use the
explicit-clock methods.

Conversation sync accepts at most 499 unique conversation identities. A result
with 500 or more rows is `harness_invalid` because it exceeds the reviewed
per-user evidence bound, even though transport pagination itself is complete.
Every returned conversation has a valid client-facing identity (including peer
IDs for person channels); an attached last message must have positive durable
message ID and sequence. Duplicate final conversations, malformed JSON, invalid
base64 payloads, cursor non-progress, or unresolved retry exhaustion fail the
login with bounded low-cardinality errors.

Identity planning uses zero-based worker IDs. Worker-local index `n` on worker
`w` maps to global index `n*workers+w`; division and remainder recover the
owner without a retained UID map. A lifecycle UID contains a bounded hashed
run/seed namespace plus the exact base-36 global index, so it is reversible and
collision-free within the run without leaking raw run-ID characters. All
deterministic choices use independent semantic-purpose hashes; introducing one
choice cannot consume or shift another choice's output. Bounded choices whose
range is not a power of two reject the biased hash prefix and derive retries
from a separate semantic domain plus attempt number.
Each engine declares the common worker count plus its own worker ID. Within one
login-rate regime, its scheduler retains a monotonic worker-local arrival index
and calls
`GlobalIndex` at the production login boundary. Quotient/remainder partitioning
assigns the 10,000 formal online target as 3,334, 3,333, and 3,333 without
overlapping UIDs.
Steady login arrivals form one checked global rational token stream. Every
worker advances the same cumulative integer prefix but owns only token ordinals
whose modulo is its worker ID; per-worker burst caps partition the global cap.
The owned local attempt ordinal is mapped back to the interleaved global login
ordinal before schedule selection, so three workers preserve exact aggregate
250,000/62,500 daily new/returning counts instead of replaying three 80/20
prefixes. `LoginSchedule` also carries the O(1) cycle-prefix count of prior
`LoginNew` decisions. For a new decision this is its globally consecutive
new-user ordinal, independent of the uneven per-worker identity-index lanes.
`GlobalNewOrdinalFor` also resolves any worker-local new-identity index through
the immutable login/worker least-common-multiple cycle. The cycle visits at
most 100 lane positions, uses checked arithmetic, and retains no plan or result
history; bootstrap users, asynchronous results, relationship history, and
returning candidates all use this same resolver.

Login identity, session bucket, and channel lifecycle class use independent
run-rotated ordinal cycles, giving exact 80/20, 25/50/20/5, and 60/25/10/5
shares without mutable PRNG state. Keyed unbiased draws select values inside
each duration or message-count range. Every session bucket has a positive
integer percentage, which bounds even a local profile to at most 100 buckets.
At 250,000 new users per day the identity growth rate is about 2.9 new
users/second; because new users are 80% of logins, the total login rate is about
3.6 logins/second after bootstrap. Empty-dataset bootstrap is a separate fixed
global 25-login/second phase until 10,000 users are simultaneously online.

Each new relationship plan has a finite two-to-eight-message initial burst over
five to thirty seconds and explicitly requires both endpoints online. Revisit
plans wait ten to sixty minutes and send two to five messages; rotating and
long plans contain only their bounded active durations. All lifecycle classes
stop scheduled activity and cool naturally. The model never emits polling or
keepalive work for a Channel runtime.

The primary SEND rate is one global integer budget, never one bucket per
worker. The coordinator's cumulative weighted boundaries produce per-tick
grants whose sum is exactly the configured global rate; a rotating phase
removes long-run worker rounding drift. The complete fresh, released, and
credit vectors are delivered with one sequence to every worker. Each worker
applies only its indexed released share and never advances a second allocator,
so all retained credit sums to the single global two-second burst without
giving every worker a global-sized bucket. Capacity-rate changes are staged for
the next tick and discard old-rate credit rather than creating retroactive
token debt. Each worker maps its local ordinal through `GlobalIndex`, so
aggregating the three workers reconstructs one non-duplicated global cycle with
exactly 2,000 SENDs/s and the reviewed traffic and payload shares.

The first complete vector crosses all three workers before `CoordinatorRunStart`
exists, so it uses the normal bounded coordinator control-round deadline. Only
after that barrier fixes the measured clock do scheduled grant rounds cap their
deadline to the one-second grant cadence. Bootstrap application latency may
therefore consume bounded pre-clock control time, but it cannot relax, skip, or
catch up any measured grant tick.

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
overflow. Retry exhaustion retains a checked, low-cardinality last-trigger
breakdown: attempt timeout, local transport admission, asynchronous transport
error, retryable SENDACK, or unclassified compatibility completion. The
breakdown is propagated through worker snapshots and final reports without a
UID, channel, message identity, or raw error.
If the final permitted attempt is itself rejected by the load generator's
local transport admission bound, the logical SEND is aborted as the closed
`transport_admission_saturated` harness failure instead of becoming a target
terminal failure. The retained local-admission terminal field remains readable
for compatibility evidence, while new engine-owned exhaustion cannot attribute
an unissued final wire attempt to WuKongIM.

The concurrent verifier registers that attempt-independent identity in an
explicitly bounded pending map together with the fixed maximum of four
distinct wire-attempt `ClientSeq` values. Only a matching registered SENDACK
with positive server message identity completes the logical send. An incomplete SEND accepts a
rejected SENDACK as a retry decision input only when both server identity fields
are zero; nonzero identity on rejection is invalid product evidence. Once a
SEND is acknowledged, another registered overlapping attempt may resolve once
without duplicate/conflict evidence. `ReleaseSend` retains only those unresolved
sibling attempt identities through the existing correlation deadline in a
bounded grace index; unknown sequence values remain product evidence. Explicit
terminal completion is also a product failure. Unknown, duplicate, and
conflicting completions use fixed reason codes and redacted message fingerprints.

Every RECV that carries this exact run-marker prefix is decoded once,
reconstructed through the payload marker and `TrafficModel`, then checked for
person peer versus group channel semantics and strictly increasing sequence per
recipient/channel. A payload without that exact prefix, including another run's
valid marker, is acknowledged when its server identity is positive but never
enters receive, ACK, retry, latency, sequence, correlation, or correctness
counters. A damaged payload that still carries this run prefix remains marked
corruption. Payload
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

Receive-sequence memory is budgeted in logical rows rather than preallocated
bytes because Go map bucket overhead is runtime-dependent. Each online identity
can retain at most ten person-relationship channels plus one fixed-group
membership, so worker capacity is the checked local-online count times eleven.
The formal worker ceilings are exactly 36,674, 36,663, and 36,663 rows (110,000
cluster-wide); the local profile retains the 4,096-row floor, and checked
overflow saturates at the common 10,000,000-entry verifier ceiling. Maps grow
only for observed recipient/channel pairs and logout releases all rows for that
recipient.

`SessionPool` owns only traffic-ready online sessions. Its factory receives a
deterministic per-UID CONNECT token, creates a fresh client for every login, and
then delegates CONNECT-before-zero-coverage-full-sync semantics to `RunLoginSync`.
The narrow `WKProtoSessionAdapter` delegates CONNECT, non-waiting SEND
admission, frame reads, PING, RECVACK, close, and numeric queue gauges to the
existing non-dropping `internal/bench/wkproto.Client`; it does not recreate
protocol pumps. A full adapter publication bound, shared-client admission lock,
writer queue, or inflight bound returns `client.ErrSendQueueFull` immediately
and enters the existing bounded logical retry path, so data-plane pressure
cannot hold the serialized engine owner or a coordinator grant RPC. Worker
queue evidence retains the cumulative `transport_rejected` count across all
attempts. The verifier separately retains the subset where this local admission
rejection was the logical SEND's first-attempt failure. Worker correctness
projection subtracts that subset from `first_attempt_failures`, with checked
underflow, so the strict product first-attempt rate measures target behavior
while the load-generator pressure remains explicit harness evidence. Exhausting
the bounded retry path on local admission also stops the worker with
`transport_admission_saturated/harness_invalid`; it never publishes a product
terminal SEND.
The ordered session drain uses an explicit streaming read whose lifetime is
owned only by the generation or caller context; the transport's default
short-operation timeout still bounds CONNECT and control writes but never
detaches an otherwise idle session.
After full sync makes a session traffic-ready, one joined heartbeat loop sends
WKProto PING every 30 seconds. This keeps the owner route active in the
authority presence directory even for idle sessions; the formal 10,000-user
bound therefore owns at most 10,000 heartbeat loops in addition to its already
required receive drains. Each PING uses the single-anomaly timeout and the
client's bounded control-frame writer. An unexpected heartbeat write failure
closes the socket so the ordered drain publishes the existing bounded session
failure and the engine replaces the resulting online-target deficit.
Independent login I/O runs concurrently under one explicit starting-session
capacity and retains only the UIDs whose CONNECT/sync is active; capacity
exhaustion is harness-invalid. Scheduler admission reserves `starting` under
the pool ownership lock before generation-owned CONNECT plus full-sync work is
launched, so concurrent returning plans cannot select the same UID. Results use
a fixed completion queue with the same capacity and are consumed by later
steps; one slow sync never serializes traffic advancement. Only a validated sync starts the recipient's
sole ordered frame drain and heartbeat. The session factory, CONNECT, sync,
drain, and heartbeat are
children of the active engine-generation context; stopping a generation fences
new admission, cancels that context, then joins startup work, drains, and
heartbeats.
Expected logout first removes online admission, then cancels and closes the
socket, joins the drain and heartbeat, and finally releases recipient sequence
state. Engine-driven expiry performs the same ordered cleanup through one
generation-owned, worker-online-target-bounded cleanup queue after detaching
and canceling the session under the serial Step boundary. The closing tombstone
prevents same-UID replacement while cleanup is active. The
WKProto result queue distinguishes a non-terminal asynchronous SEND publication
error, which keeps the same drain online and returns both the wire `ClientSeq`
and stable `client_msg_no` to the engine-owned retry state, from a terminal remote reader
exit. Under the pool ownership lock, an unexpected exit records bounded
evidence before publishing the session offline; socket close and recipient
release remain outside that lock. The UID moves atomically from online to a
bounded closing tombstone for that unlocked cleanup interval. It is not
routable, but it rejects replacement login and remains owned until the old
drain has joined and recipient verifier state has been released. `Engine.Step` derives replacement demand from
the resulting online-target deficit, so no blocking exit callback is part of
the atomic boundary. Public Advance and Tick share the serial Step lock; Step
and Advance cross the monotonic owner-time admission before expiry detachment
or scheduler mutation; neither waits for the cleanup loop. Unknown unexpected read exits remain bounded
`session_read_failed` harness evidence. The pool's UID, user-index, and fixed
group-member routing indexes contain current online sessions only, use
swap-delete on logout, and allocate no per-lookup history.
The aggregate session snapshot also retains `closing` and one fixed teardown
initiator count per connection: expiry, heartbeat failure, remote terminal
read, unclassified read failure, generation stop, or explicit logout.
Transport-close failures are counted separately. These counters are
generation-bounded, monotonic, identity-free, and reset only with a new worker
generation; no UID, socket address, or raw transport error enters the worker
protocol.
Scheduler decisions read only O(1) online, starting, and closing counts. An
aggregate pool snapshot copies client handles and scalar session metadata while
holding the ownership read lock, releases it, and only then samples transport
queue gauges; a slow client gauge cannot block login, logout, or detach.
Each asynchronous startup result retains the plan-time global login and
canonically resolved new-user ordinals. A first-login session retains one
bounded publication bit while it is online; returning sessions are already
published. When a new result is consumed, the engine reconstructs both incoming
and outgoing real edges and activates an edge only when the other endpoint is
online and published. Whichever endpoint result is consumed second therefore
publishes the edge exactly once even when CONNECT and full-sync completion
reverse plan order. Relationship planning never assigns degree or schedule
ordinals from startup completion order and retains no historical activated set.

`TrafficGenerator` streams, rather than retains, each global per-second grant.
It reuses `RateAllocator`, `TrafficModel`, and `GroupCatalog` to preserve the
exact primary traffic, payload, direction, and fixed group-target cycles. A
grant carries worker, generation-scoped logical position, traffic class, and
payload class but deliberately has no sender, packet, or claimed online route.
The engine binds person grants to eligible lifecycle activity and group grants
to a currently online fixed-directory member on the group's unique owner
worker. Group-domain identities use the consecutive global group ordinal, so
the group stream itself has exact one-percent sampled delivery rather than
locking its sampling phase to the 90/10 traffic-kind cycle. The very-large
group remains an independently counted one-per-minute owner-routed canary. The
generator has no product metadata or runtime mutation interface.

The private session scheduler uses the fixed global
`bootstrap_logins_per_second` rate while the empty dataset is bootstrapping.
It partitions that stream exactly across three workers, substitutes new
identities until the online target is first reached, and still requires every
identity to complete a real WKProto CONNECT/CONNACK plus fresh version-zero
full conversation sync. Each worker has 256 bounded concurrent starting slots.
Missed whole attempts and unused per-step credit are discarded, so a delayed
tick or recovered sync path cannot catch up above the fixed global rate. Every
UTC-aligned second gives the workers immutable 9/8/8 shares, so even subsecond
skew across the boundary cannot mix adjacent extra positions; a whole missed
range is discarded.
Fake-clock three-worker churn coverage reaches 10,000 simultaneous online users in 421
seconds and enforces a 15-minute scheduler bound. A coordinator-controlled
worker remains in all-new bootstrap at its local target; the first grant, sent
only after every local share is ready, clears bootstrap credit, bucket phase,
and the unequal fixed-share attempt ordinals on all workers. UID allocation is
not reset. The scheduler then derives steady checked rational
login credit from `new_users_per_day` and the 80% new share, about 3.6 total
logins per second in the formal profile. Steady scheduling preserves the exact 80/20
planned, admitted, and completed split, uses `ReturningCandidate` for real
offline history, and uses the older-history fifth to keep paired fixed-roster
members online across every group category without adding sessions beyond that
80/20 mix. It schedules bounded cold revisits on old edges and replaces expiry
or unexpected terminal exits. `Engine.Step` is the narrow
bounded orchestration boundary; aggregate snapshots expose planned, admitted,
completed, skipped, expired, and replacement counts without exposing scheduler
state. A generation lease covers the whole Step, including time waiting for
the serial Step lock; public Tick has a separate lease covering that same wait.
Worker transport capacity is the sum of currently online session queues rather
than a fixed generation setting, so it may change during churn and drains to
zero after the joined final stop. Coordinator monotonicity therefore treats
only work, retry, and inflight capacities as immutable.
Stop first fences admission and cancels the generation, so a Step inside a
session SEND call or a Tick blocked on the serial boundary returns before
Stop joins every Step, Tick, and login startup and then closes sessions and
cleans engine state. Generation cancellation aborts that incomplete local
attempt without product evidence; Start cannot reset state while old-generation
work is still live.

`Engine` owns one bounded command loop for the active generation. One activity
min-heap holds relationship SEND eligibility, a runtime min-heap holds granted
SEND, attempt-timeout, and lifecycle deadlines, the indexed retry heap holds at
most one approved retry per logical message, and the inflight map is explicitly
capacity-bounded. The shared future-work capacity includes every bootstrap
forward relationship's maximum initial burst plus one possible lifecycle timer,
because no activity can drain before the first global traffic barrier. A
separate bounded completion queue lets ordered session
drains report SENDACKs under backpressure without competing with control
commands; joined session expiry never waits inside the owner, long clock
advances consume completions between scheduled work, and shutdown joins drains
before its final completion barrier. After each fixed
32-SEND work quantum with outstanding attempts, the engine yields one Go
scheduler turn and drains completions again. This bounded event-fairness point
does not use wall-clock sleeps or extra queue capacity and works with one
logical processor. All heaps share one checked future-work capacity except the
retry heap, which has its own explicit capacity. New-user observation reconstructs the prior five possible owners plus
the owner's bounded forward set, schedules an initial burst only while both
sessions are online and the peer's new-user publication is complete, and
retains at most one lifecycle deadline for revisit or natural cooling. Rotating
and long channels additionally occupy a fixed active array and swap-delete
index capped by this worker's quotient/remainder share of the configured global
person hot set. Lower worker IDs receive the remainder, so the formal three
worker limits are exactly 2,667, 2,667, and 2,666 and sum to 8,000. A full hot
set never drops a mandatory initial burst: its later hot ownership waits in a
work-capacity-bounded pending array and is promoted when an active deadline
releases a position. Primary
person grants keep channels hot only until their 20-40 minute or 2-4 hour
deadline, after which pending or newly activated relationships reuse the
released positions. Due relationship
activity cannot be sent by clock advancement alone: a person grant from the
single global tick substitutes its target while retaining the grant's worker,
logical ordinal, payload class, and primary denominator. Initial and revisit
messages use distinct generation-scoped lifecycle and revisit identity domains;
primary, group, and canary work have their own domains, so restarts and repeated
activation cannot reuse `client_msg_no`.
Relationship activity heap entries retain only sender, target, direction,
channel, and identity-domain metadata. They do not prebuild or retain a packet,
payload, `client_msg_no`, or wire sequence; grant-time retargeting constructs
that transient state from the actual global traffic grant.
Revisit timers that require cold-runtime evidence also have one bounded active
channel index; only an explicit prior all-node cold approval can let the timer
add revisit activity. An approved revisit uses either online endpoint as the
sender; a returning-login revisit always uses the returning user. If its
required sender, or both ordinary endpoints, are offline, the same timer is
deferred to a later advance instead of being silently deleted. The timer owns
the same checked eligibility window as the revisit activity it would create;
an approved fully-offline timer expires at that boundary, physically releases
its channel index, and records exactly one under-delivery event.
Person routing always requires an online sender. For the verifier's exact one
position in every 100 logical sends, it also requires an online target; other
person sends may keep a channel hot while its peer is offline. A sampled group
or canary send requires a distinct second online fixed-directory member. A
mandatory initial or revisit activity owns an explicit checked eligibility
deadline. A temporarily ineligible due activity is reinserted once just beyond
the current advance boundary, with route scans bounded independently of queue
size. If that deferral would reach or cross the eligibility deadline, the
activity closes immediately instead of inserting an unroutable boundary item.
Bootstrap activity cannot be offered before the measured traffic barrier, so
that first owner-applied grant gives every still-pending activity one full
eligibility window after its rebased due time. Initial relationship
messages also retain their checked delay from generation start: the
relationship's real bootstrap activation time plus its configured offset
inside the 5-30 second window. The barrier rebases those delays and rebuilds
the activity heap once. This preserves the 421-second login-time spacing
between relationships instead of either replaying every item as an overdue
historical burst or collapsing every cold relationship into the same
30-second activation spike.
Each coordinator grant owns one generation-local sender-reservation map and
exhausts distinct online sessions across person traffic, group traffic, and the
optional canary before it may reuse a sender. A duplicate mandatory sender is
deferred inside its existing eligibility window; primary fallback rotates
across the complete bounded active person-channel set until it finds an online
unreserved sender. Only deliberately sparse configurations may reuse a sender
after the complete eligible set is exhausted; the formal 10,000-user gate
requires every first grant send, including the optional canary, to use a
distinct session. This keeps the fixed 32-entry per-session transport queue
from receiving one worker's whole release while still limiting new cold-channel
admission to the coordinator's fixed global 2,000-SEND/second rate. The bounded
map is cleared after every grant and discarded with every generation; it
retains no history.
An active person channel is hot only after one successful SENDACK proves its
first cold write. Until then, every routed logical SEND on that channel carries
the cold attempt deadline, while exactly one queued/inflight/retrying SEND owns
the metadata-create candidate. Later grants may keep rotating through the same
ACK-unproven channel without either excluding its endpoints from the complete
active set or counting another metadata create. Any successful cold SENDACK
marks the active channel warm and clears the candidate fence; terminal cleanup
clears the fence without inventing warmth.
The refresh happens once and does not relax measured-phase deadlines.
At the deadline it is physically removed and records one closed
`offered_load_under_delivery` harness event before any active channel can fill
that grant. Joined shutdown records one aggregate event for pending mandatory
activities that were already offered or are due at the final workload cutoff.
Unoffered activity strictly after that cutoff is normal future cancellation
with its own numeric counter and no harness evidence; a fully drained shutdown
adds no evidence. A missing eligible primary route is harness-invalid under-delivery
before SEND registration and therefore cannot become a retry or product
terminal result.
No historical user or channel owns a goroutine, timer, or retained map row.

Attempt zero plus retries one through three reuse the same Phase 2 logical
identity and `client_msg_no`, while every wire attempt receives a distinct
generation-local monotonic `ClientSeq`. The real transport pending key therefore
keeps overlapping attempts independent; a late ACK is attributed to its exact
attempt. Only the current attempt may schedule or cancel retry work after a
timeout, rejection, or asynchronous transport error; stale outcomes leave it
unchanged. A successful ACK from any registered attempt completes the logical
send exactly once, cancels current timeout/future retry work, and moves
unresolved sibling attempt identities into the verifier's bounded grace index.
The ordinary loaded-hot first-attempt deadline is the configured hot SENDACK
p99.9 bound. A deterministic first person-channel create or an all-node-proven
cold reheat instead uses the configured cold p99.9 bound, so a valid cold
activation is not retried under the shorter hot threshold. Both remain bounded.
Timeouts and closed temporary SENDACK reasons schedule the existing
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

Exact delivery correlation uses one run-keyed position in every 100 person
logical sends per worker and one position in every 100 globally consecutive
group-domain ordinals. A sampled entry has one map row and one indexed min-heap
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

The person relationship graph keeps degree identity and endpoint identity
explicitly separate and retains no adjacency history. Degree uses the globally
consecutive new-user ordinal and the run-rotated `3,4,4,5` cycle; endpoints use
worker-local identity ordinals and map `localOwner+distance` through
`GlobalIndex` on the same owner worker. This remains exact for all 100 login
phases and all four degree phases even though three formal worker lanes contain
83,334, 83,334, and 83,332 new identities. Every four global new-user ordinals
create exactly 16 relationships, 250,000 new owners create exactly 1,000,000
edges, and no activation depends on a different worker's session pool.
Incoming reconstruction derives each prior local owner's degree ordinal from
the same immutable worker-local resolver and checks only the previous five
local owners. Available history and returning-candidate conversations resolve
both incoming and outgoing degree ordinals through that boundary, so they
cannot invent an edge from a raw identity index.
Fixed-capacity results bound one user's incoming plus outgoing conversations to
ten.

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
online roster, while later members span deterministic arrival cohorts. Each
group index modulo the worker count is its unique traffic and roster owner.
Older returning logins rotate only through groups owned by their worker and
select two distinct roster members whose stride-derived identity owner matches
the group owner. With the formal 2,000-group stride and three workers, member
ordinals zero and three are the first compatible pair. If a primary target
currently has no eligible member, the route searches only same-owner groups in
the requested category and retargets to a group with one online member, or two
for sampled correctness, preserving the exact class share. The very-large
canary is never retargeted, is emitted only by its fixed owner, and is kept
reachable by its paired fixed-roster returners. Even the largest group never
allocates a membership slice or history-sized map. Primary group targets use
an exact 80/15/5 small/medium/large cycle. The very-large group is reachable
only through a separately reported one-per-minute canary and is excluded from
the 2,000 SEND/s denominator. When a local catalog omits a primary class, its
weight is deterministically omitted and the remaining available weights are
normalized; every available primary class must still contain at least one
group per worker, and the canary is never promoted into primary traffic. Fixed
group channels add no historical-channel growth, leaving the formal hot set at
8,000 person plus 2,000 group channels. The person target is global: generator
snapshots expose the local quotient/remainder limit, and engine active/pending
ownership enforces that local limit. A single worker retains the full 8,000.

`LocalConfig` is the reviewed three-node, three-worker shakeout baseline. It
keeps the formal topology and real zero-coverage paginated sync
(`completed_coverage=0`, 200 candidates per page, 500-conversation evidence
bound) while using 100 online users, 250,000 new users/day, 100
SENDs/s, an 80-person/20-group hot set, a fixed 16/3/0/1 group catalog with
1,000 members in the very-large group, a 5,000-channel node bound that covers
the five-minute loaded relationship window at the retained formal arrival
rate on every replica, 12 runtime
samples, two seconds/200 SENDs of burst credit, and 10/20/30-minute timeline
checkpoints. Its three service, worker, service-host-metrics, HTTP API, and TCP
gateway roles plus one separate load-host-metrics role use replaceable, unique
loopback declarations by default. The native shakeout retains this fixed local
run ID so repeated integration runs exercise the same deterministic decisions;
fresh run directories and reserved port ranges provide process/data isolation.

Formal and local execution are traffic-denied until one black-box preflight
passes. Static validation first proves three distinct service-node, worker,
service-host-metrics, API-pool, and separately addressed TCP-gateway
declarations plus one load-host-metrics declaration, so an invalid topology
performs no I/O. Authenticated service checks then prove
health/readiness, effective 12-logical-Slot/256-physical-hash-slot topology,
3/3 replicas, the profile's exact Channel bound (50,000 for formal), forced-GC
metrics, required Bench capability, and a complete live Slot view from all
three nodes. Worker health/info and TCP
gateway connectivity are checked through narrow replaceable boundaries; no
assignment is installed by preflight. A structurally healthy initial leader
distribution is admitted; only the continuous observer owns the ten-minute
leader-imbalance failure window. The product metrics registry materializes
true zero series for the closed `max_channels` activation-rejection label and
all three metadata-create results on every configured logical Slot Raft Group,
so a clean 12-group cluster exposes the complete strictly required vector
without inventing an event or treating a missing family as zero.

Each host-metrics declaration carries an exact `mountpoint` and `device`. The
bounded native endpoint and parser expose exactly one data-filesystem pair,
the system-filesystem pair, host CPU and memory utilization, and, on the load
host, bounded Prometheus-directory bytes. It also forwards a closed, fixed-role
process inventory with unit uptime, cumulative CPU jiffies, and RSS for every
service, worker, coordinator, proxy, analysis, Prometheus, and collector
process. Formal preflight requires the roles for each host, while formal and
capacity reports persist the fixed four-host/thirteen-role arrays so resource
attribution is auditable without PID-cardinality metrics. Every later formal
observation rechecks that stage-specific required inventory: a WuKongIM unit
exit is an immediate product/server-crash signal, while another required
workload or evidence-process exit makes the harness invalid. Missing or duplicate
formal evidence makes the harness invalid. The root collector refreshes every
15 seconds; the host endpoint requires both file mtime and the unique embedded
success timestamp to be no more than 45 seconds old, so a stopped collector
cannot leave an indefinitely valid `up` snapshot. Formal service data minimum is 500,000,000,000 usable
bytes and the load data minimum is 200,000,000,000 bytes. Any system or data
filesystem below 5 percent free, or Prometheus at 140,000,000,000 bytes under
its 150 GB retention cap, returns an infrastructure failure and emits one
narrow coordinated-stop signal; the observer does not own assignment control.
CPU above 90 percent, memory above 85 percent, both the aggregate service
runtime queue and the bounded Channel worker queue, and
each worker work/retry/inflight/transport queue above 80 percent require
uninterrupted five-second observations for 15 minutes before infrastructure-
capacity attribution. Worker queue evidence comes from the coordinator's
already bounded three-worker checkpoint cuts and retains only fixed counters
and the current continuity state. A missing queue sample or a gap longer than
two observation cadences invalidates continuity instead of resetting a breach
into an apparent clean window. Service queue families pair depth and capacity
by their exact bounded label set and retain the maximum pool utilization, so an
idle pool cannot average away another pool's saturation. A paired zero-depth,
zero-capacity series declares an inactive queue or a queue bounded only by
bytes and is excluded from item utilization even when its item depth is
positive. Its raw depth still contributes to the aggregate queue-depth signal.
A load worker's offered-rate underdelivery is separate infrastructure evidence.
Latency becomes product
failure only when the whole measured window has complete four-host resource
evidence and no threshold-high sample; missing or mixed evidence is
insufficient evidence.

After preflight, the target observer immediately polls and then repeats at the
configured cadence, which is five seconds in the reviewed defaults, using
bounded state. Each round starts exactly one goroutine per service node, shares
one context bounded by the smaller of that cadence and five seconds across the
node's ordered health/readiness/cluster calls, joins the fixed result slice in
node order, and only then reads the clock for continuous-window accounting.
Parent cancellation releases the shared round and ticker. The observer requires
complete stable reports for all 12 logical Slot groups, one leader, three
desired replicas, three live voters, and leader-only progress. Hot Slot groups
are an optional bounded, unique declaration; an empty declaration means all 12
workload groups. Health and readiness failures own the service-health window;
missing or invalid debug-cluster snapshots own the cluster-health window. A
replica `match` index ahead of commit is healthy replicated-but-uncommitted
progress and contributes zero committed-entry lag. Structurally inconsistent cluster views share one bounded
30-second failure window. Replica progress owns one bounded failure window per
logical Slot; a healthy observation resets that Slot's window, so lag moving
between different Slots cannot be combined into one continuous failure. Any
healthy service sample likewise resets the 30-second service window. Leader
balance compares each node with the
exact rational `slots/nodes` share rather than assuming 4/4/4; a deviation above
20 percent must remain continuous for ten minutes before product failure. When
the observer terminates the coordinator, `CoordinatorResult.ObserverCode`
retains the closed observer reason so a pre-report process exit remains
diagnosable without raw endpoint responses.

One coordinator process owns exactly one non-resumable assignment generation.
After preflight passes, `GroupSetup` streams the fixed group catalog through the
existing `/bench/v1/channels` and `/bench/v1/channels/subscribers` APIs. Channel
rows use bounded consecutive-index batches; subscriber rows reconstruct one UID
at a time and retain only one bounded member batch. Each subscriber request is
capped at 1,000 UIDs so the request remains within one subscriber Raft command,
including for the 100,000-member group. Setup never emits a person channel. Deterministic versioned batch
IDs make a partial target failure safe to replay against the product's set-like
channel/subscriber mutations.

Setup idempotency is deliberately scoped to that one coordinator lifecycle. It
retains only one active `run_id`, one versioned catalog fingerprint, and a
complete bit plus one in-flight channel. Target I/O never holds the state mutex:
an exact concurrent retry waits on that channel with caller cancellation, while
a different run or fingerprint fails before waiting or target mutation. Failure
closes the flight so one exact retry can replay, and success makes every exact
retry a no-op. The fingerprint streams every group descriptor and covers the
profile, seed, worker/owner partition, fixed catalog counts, per-group category
and member cardinality, group ID, and explicit identity/catalog/member/owner
derivation versions. An exact completed retry performs no target writes; a
partial exact retry deterministically replays from the first batch; another run
or another shape fails before any target write. The existing target mutation
responses expose accepted counts but no authoritative per-group shape digest,
so this is not process-external idempotency. Coordinator or worker failure
forbids resuming the same run; a later process must use a new `run_id`.

The coordinator then builds exactly three assignments with one shared
`run_id + assignment_id + generation` fence. User indexes are the existing
interleaved lanes `worker_id + local_index*3`; quotient/remainder counts cover
the configured global online prefix without overlap or gaps. A single
coordinator `RateAllocator` owns the `1/1/1` rate-weight vector and the one
global two-second credit bound. No allocator tick is consumed until all three
current-version statuses report traffic-ready; this bootstrap barrier is bounded by
the configured warmup duration. A readiness poll starts only while the injected
clock is strictly before the warmup deadline, and its shared context is capped
by the smaller of the normal status timeout and the remaining warmup. All three
ready responses are accepted only if the poll also finishes strictly before
that deadline; equality is timeout, not success. Continuous observation starts
immediately after the successful Start round and remains active throughout that
readiness barrier, so a product or harness result can terminate bootstrap. The
local shakeout uses the same fixed global 25-login/second bootstrap rate so its
100-user synchronized population fits inside the shorter ten-minute warmup;
after readiness it keeps the reviewed 250,000-new-users/day steady arrival
rate. Its smaller online population and evidence label bound that non-formal
run. The
operator stop channel also owns bootstrap cancellation: preflight, fixed-catalog
setup, assignment, Start, and readiness status calls receive a derived context
that is canceled when that channel closes. A stop before the initial grant
barrier returns `stopped` without claiming a checkpoint or report and uses the
independent cleanup context for every attempted worker; after the barrier, the
same channel enters the normal terminal evidence-cut path. The coordinator then
produces one complete
fixed three-worker grant vector per logical second and sends that same vector,
sequence, rate, burst, and credit evidence to all three exact-fence grant
endpoints concurrently. One transport failure may retry the identical sequence;
an unconfirmed vector stops the run. A failed grant records one bounded
`plan`, `delivery`, `tick`, or `coverage` reason in the coordinator result and
unavailable-report terminal summary, so remote diagnosis can distinguish a
worker RPC deadline from scheduler-clock or coverage failure without retaining
raw error text. A delivery deadline may additionally read the current-version
status projection for at most one ordinary control-round timeout; it never
resends the grant and retains only a valid late cached runtime code. The first
pre-clock grant response round uses the ordinary shared control-round deadline
and synchronously drains its newly admitted work before the measured clock is
frozen. Each later measured grant response confirms only bounded generation-owned
engine admission; the existing worker tick loop drains that work independently,
so SENDACK, retry, or target pressure cannot consume the one-second control RPC.
Work, retry, inflight, transport, correctness, and observer bounds remain the
terminal backpressure evidence rather than a hidden grant delay. Each later
measured response round is capped at the one-second grant cadence. Scheduled grant timestamps
must be nonzero, non-future, and younger than one cadence. The first accepted
tick must fall in `[1s, 2s)` after the captured ticker start; every later tick
must fall within 10 milliseconds of one logical second after the preceding
accepted tick. This narrow bound admits platform timer timestamp quantization
without accepting a delayed, skipped, or catch-up tick; invalid ticks fail
closed without advancing the grant plan. A status or cutoff branch that drains
a concurrently published grant samples the clock after receiving that grant;
it never compares the new tick against a pre-receive wall-clock sample.
Coverage checks use that same 10ms
tolerance so a due ticker has time to publish after the one-second boundary;
once consumed, its timestamp must still satisfy the strict tick rules.
Final-cutoff and status branches inspect an already queued grant before they
may complete the run. Each worker emits only its vector share and never advances
an equivalent local allocator, so bootstrap phase differences or delivery retry
cannot multiply the global budget.

Worker status polling and the measured final timeline begin only after the
initial grant barrier. The production hook uses a wall-clock-only report
window; process-local monotonic readings are stripped before `Elapsed` is
calculated so the JSON report validates identically after restart or transfer.
It atomically writes one
`wukongim.chat_lifecycle.run_start/v1` receipt at that boundary with the stage,
start, expected end, generation, and only hashed run/assignment identities.
Every successful three-worker evidence cut also atomically replaces
`diagnostic-status.json` in that report directory and emits one compact JSON
line to the command diagnostic stream. The status contains current `online`,
`starting`, `closing`, and `traffic_ready` gauges, the fixed teardown reason
counters for each worker and their checked totals, plus at most 64 recent
aggregate change events. Finalize writes one last post-stop cut so explicit
generation stop and cleanup failures are distinguishable from an earlier
connection collapse. The compact status log line additionally includes the
checked aggregate message counters and terminal-reason breakdown, so a terminal
cut distinguishes attempt timeout, local admission, transport failure,
retryable SENDACK exhaustion, non-retriable SENDACK, and generation cleanup.
It contains no UID, Channel, message identity, address,
credential, path, or raw error. This running contract is intentionally
separate from terminal reports so Analysis MCP can diagnose a connection drop
before `final.json` exists. A fresh production controller arms the observation source at that exact barrier
before probing the live dataset digest. The digest proof may take longer than
one observer phase, but it therefore cannot consume the first exact-hour
forced-GC evidence window; a failed digest remains a terminal Begin failure and
the armed source is discarded with that failed run. A rehearsal therefore
proves that all 10,000 users completed CONNECT plus a fresh
zero-coverage full sync and all workers accepted the first complete 2,000 SEND/s
grant before its two-hour clock begins. The coordinator owns one normal
observation cutoff at the stage duration: two hours for rehearsal,
`thresholds.timeline.final` for the 30-minute local shakeout, and 72 hours for
formal. The healthy observer deliberately
has no success terminal result. Before the cutoff, an observer product or
harness result ends the run with that failure. At the exact cutoff, a
rehearsal or local coordinator cancels and joins the observer child; only the
resulting healthy `stopped` permits the one final checkpoint followed by
bounded worker stop and final aggregation. A passing formal-chain cutoff is
different: its in-memory continuation transfers the same still-running
observer result channel and cancel owner alongside the exact assignments and
grant sequence. The capacity coordinator adopts that child without invoking
`Observer.Run` again, and cancels and joins it only at the terminal capacity
cut. The production controller likewise keeps the same observation source,
lifecycle proof loop, metadata ledger, and dataset identity while replacing
only the report/evaluator stage. A
product or harness result racing with that cancellation remains a failure. An
outer caller cancellation instead returns coordinator `stopped` without a
checkpoint. At the formal 24-hour threshold, the production hook captures one
non-terminal qualification report from the same live generation while grants,
workers, service nodes, and observation continue toward 72 hours. Assignment,
start, grant, status, checkpoint, aggregation, or runtime failures remain
`harness_invalid`.

Assignment, Start, status, and checkpoint rounds each launch exactly three
requests with one shared at-most-five-second deadline, join every attempted
request, reject a valid-looking result returned after that deadline, and
validate results in worker-index order. Every grant round uses the same bounded
concurrent shape; only measured grant rounds use the stricter one-second cap.
A blocked control request
therefore cannot starve the final cutoff indefinitely. Each control round
retains each response's error and validity evidence. An ordinary non-context
error or a nil-error invalid response remains `harness_invalid` regardless of
when an outer cancellation is observed. With no stage evidence, a canceled
parent makes assignment, Start, grant, status, or final checkpoint `stopped`;
an invalid response can participate in that result only when its error causally
matches the canceled parent. A canceled final checkpoint is never aggregated
as completed. The round's own deadline always remains a stage failure, even if
the parent is canceled later. Grant, status, and observer termination reasons
are frozen before joining the observer or starting failure cleanup, so a later
caller cancellation cannot replace an established stage result. An observed
product or harness failure has priority over the frozen round reason, while an
observer `stopped` preserves a stage failure and otherwise reflects a causal
caller cancellation.
Failure cleanup and successful
final stop likewise launch the fixed worker set concurrently under one shared
total cleanup deadline, while still attempting every applicable worker.
Observer product or harness failure keeps precedence when it races a grant or
status harness failure. After a terminal decision, the coordinator first
captures the moving pre-stop cut, then stops all workers and obtains their
stable final snapshots. Only then may the production hook join lifecycle work,
refresh the live dataset identity, reconcile exact per-Slot metadata-create
counts, and atomically write the final report. Capacity terminal failures use
this same joined observation/stop/finalize path when production hooks are
installed; an early reducer failure cannot skip terminal evidence capture.

Before dispatching the concurrent assignment round, the coordinator marks all
three workers attempted. If a response is lost after any worker installs the
assignment, cleanup therefore still sends an independently bounded exact-fence
stop to every worker, using a background context even when the
caller is canceled. Stop conflicts and cleanup errors never overwrite the
original harness cause. Only validated assignment responses permit the later
start and grant barriers. Failed preflight performs no setup or assignment, and
failed setup performs no assignment. The same coordinator object refuses a
second run or generation reuse.

Worker status and snapshot responses carry the exact non-secret control fence.
Every dynamic snapshot additionally receives a worker-server-owned monotonic
`snapshot_sequence`; the cached final response keeps one stable sequence across
matching stop retries. Aggregation accepts exactly workers 0, 1, and 2 from one
fence, requires the fixed 16-bucket latency schema, uses checked sums, and
rejects missing/duplicate workers, overflow, stale sequence or uptime, and any
monotonic counter/histogram/evidence regression before advancing its fixed
three-worker baseline. Session teardown counters participate in the same
regression and checked-sum rules.

The standalone verdict reducer has the closed outcomes `pass`,
`rehearsal_pass`, `passed_with_capacity_warning`, `product_failure`,
`insufficient_evidence`, `harness_invalid`, `infrastructure_failure`, and
`operator_stop`. `rehearsal_pass` is valid only
for the bounded rehearsal and never substitutes for a formal pass; its report
always warns that six-hour/24-hour/72-hour windows are incomplete. Within one
atomic evidence batch its deterministic precedence
is product, infrastructure, harness, then operator. The first terminal outcome
and fixed cause never change; later cleanup failures increment a saturating
count and retain only the last 16 closed cleanup codes. Pass can be frozen only
at or after the configured final instant and only with nonzero correctness
traffic, post-warmup evidence for all three latency classes, all three queue
baselines, and fresh complete six-hour heap plus 24-hour goroutine windows for
every node. Missing terminal evidence is harness invalid, never pass. Duplicate or regressing observation
time, invalid schema, counter regression, arithmetic overflow, or exhausted
unexpired ring capacity freezes a harness-invalid verdict.

Correctness uses cumulative counters and exact 128-bit rational comparisons.
Loss, duplicate persistence, corruption, sequence regression, terminal SEND,
or activation rejection is immediate product failure. The whole-run first-
attempt failure rate excludes explicitly attributed local non-waiting SEND
admission rejections and is strictly below `1/10,000`, so equality fails; the
rolling one-minute rate is at most `1/1,000`, so equality passes. Queue
saturation and observer gaps are harness invalid. The one-minute reducer is a
fixed 16-entry ring sized for the reviewed five-second cadence.

Latency input is cumulative threshold-bound evidence, not a caller-supplied
percentile: every hot, cold, and sync counter set repeats the exact configured
p99 and p99.9 duration limits and nested cumulative counts above those limits
and above ten seconds. Schema mismatch or regression is harness invalid.
Pre-warmup and the first late sample establish only a delta baseline. Later
deltas enter fixed 64-entry five-minute rings; exact one-percent and one-per-
thousand quantile edges pass. A continuously breached rolling result fails only
after a full five minutes, while a shorter breach is a fixed warning. Operations
over ten seconds increment a saturating count and retain only 16 anomaly rows;
they do not independently terminate the run.

Every formal-Soak latency cut also carries one closed attribution derived from
the same cut's four-host process/resource round, all bounded worker queues, and
the monotonic offered-load underdelivery counters. The five-minute reducer
retains that attribution across the breached window: any overlapping sustained
CPU, memory, service/worker queue saturation, or load underdelivery makes the
breach an `infrastructure_capacity` warning and execution continues; complete
below-threshold headroom makes it product latency; incomplete or merely
threshold-high-but-not-yet-sustained evidence is `insufficient_evidence`.
Infrastructure warnings remain sticky through finalization but never mask a
later correctness, infrastructure-safety, or headroom-backed product failure.

Resource reduction keeps three independent node states and never averages a
leaking node away. Only exact-hour forced-GC samples with finite, nonnegative,
integral uint64 heap and goroutine gauges enter the derived fixed-capacity
six-hour and 24-hour rings. Growth strictly above five percent fails; equality
passes. The forced-GC read may complete after its canonical hour only within
one observer phase, the capped round latency, and the configured recoverable
cluster-unhealthy window; the evidence timestamp remains the exact hour, and a
later sample is harness invalid before any resource I/O. Queue-only observations
may arrive more frequently. Warmup establishes
each node's queue and inflight baseline, and an explicit burst-end observation
must return both gauges to that baseline; a burst without a baseline is harness
invalid, and an active burst at finalization is product failure. All one-minute,
five-minute, six-hour, and 24-hour histories remain fixed-size across 72 hours.

After an exact passing 72-hour formal checkpoint, capacity mode probes the same
live dataset generation and runs 10-minute stabilization plus 20-minute
measurement steps. Coarse rates rise 25 percent; refinement uses 10 percent;
the search is capped at eight hours and then proves 30 minutes of recovery at
2,000 SEND/s. When no breakpoint is found, the report records an explicit
lower bound. After successful recovery, a resource or queue breakpoint becomes
`passed_with_capacity_warning/infrastructure_capacity`; a latency-only
breakpoint with all declared headroom gates passing becomes `product_failure`;
mixed evidence that cannot establish either attribution becomes
`insufficient_evidence`. Correctness, evidence validity, process, cluster,
disk, budget, and expiry failures retain their stronger terminal semantics.
Capacity status ticks opt the production evidence hook into periodic bounded
checkpoint cuts, so all four worker queues and terminal resource/safety signals
continue to advance between the longer measurement boundaries. A saturation
already active at the formal boundary, or one observed in an earlier capacity
window and recovered before a later boundary, remains a sticky
`passed_with_capacity_warning` rather than disappearing from the result. The
30-minute recovery accepts earlier in-window saturation only after the terminal
resource and queue evidence is currently below threshold; that recovered event
still produces the sticky capacity warning.

Rehearsal and formal startup bind the Lease creation/expiry instants, the exact admitted
quote line items, prior committed cost, and the ¥1,350/¥1,500 limits. Every
five-second production observation recomputes conservative accrued cost:
host-hours round actual Lease age upward, the retention-risk allowance is
charged in full, and all non-loopback load-host transmitted bytes round upward
to public-egress GiB so private traffic can only overestimate spend. Reaching
the operational stop emits an infrastructure budget terminal signal. One hour
or less before Lease expiry emits the separate expiry-risk terminal signal,
leaving cleanup reserve. These checks continue through the two-hour rehearsal
and through the same observer across Soak and capacity; they are not startup-
only admission checks.

The standalone checkpoint recorder binds one validated configuration, start
instant, and exact worker fence. It accepts only complete three-worker snapshot
cuts and has no Start, Stop, assignment, grant, or traffic-control capability.
The qualification cut therefore cannot restart or reassign workers. A formal
72-hour pass requires an earlier qualification from the same recorder and
generation; snapshot sequence and uptime must continue monotonically, and
every worker uptime must cover the measured run window. Terminal evidence may
produce a final report immediately, while a terminal qualification forbids any
later continuation.

Persisted reports use the versioned JSON/Markdown schema, the canonical
effective-configuration SHA-256 digest, exact threshold values and threshold
version, design profile, topology proof, hashed run/assignment fence, stable
worker indexes/generations, explicit warmup/qualification/final instants, and
bounded aggregate message, sync, lifecycle, metadata-create, latency,
per-node resource, cluster, verdict, and capacity evidence. Optional samples
contain only a closed class, stable index, and SHA-256 hash. Raw credentials,
UIDs, Channel IDs, payload markers, arbitrary error text, and open string
vocabularies are rejected. Warning codes are projected without recalculating
or changing the supplied verdict. Each report file is written mode 0600 through
a synced sibling temporary followed by rename and directory sync; failed
validation occurs before the existing destination can be replaced. Formal
capture prepares against a cloned aggregation baseline and commits recorder
sequence state only after both JSON and Markdown writes succeed, so an output
failure may retry the identical worker snapshot cut.

The native local shared-storage staircase deliberately remains outside this
persisted verdict vocabulary. A qualification cut immediately after warmup is
the counter baseline; an operator stop after the fixed measured interval first
closes SEND grant admission and then uses the existing bounded worker drain.
The separate local-step classifier requires at least 2,500 online sessions at
the baseline, exact SEND/SENDACK delta equality, at least 90 percent of offered
throughput, zero terminal/correctness failures, empty correlation and worker
queues after drain, complete product/storage evidence, and continuous service,
worker, host-metrics, and local process-sampler processes. Product evidence is
complete only when host/process rounds and worker-queue cuts have no missing
samples. Less than ten percent filesystem free is
`storage_confounded`, overlapping WuKongIM work is `host_confounded`, and
missing evidence is `insufficient_evidence`; none is a product-capacity or
formal verdict.

Capacity admission accepts only a validated final passing formal-stage Soak report of
at least 72 hours whose persisted dataset-reference digest still matches a
later exact three-node live-target probe. The caller supplies only the frozen
checkpoint reference: after static checkpoint validation and successful
preflight, `Coordinator.Run` synchronously invokes the probe before group setup
or worker mutation. All three direct responses must carry distinct nonzero node
IDs, the same checkpoint dataset/process-generation digest, `live_aged` state,
and coordinator-stamped observation times inside that exact probe call. A cached
aggregate, restarted generation, duplicate node, stale response, or clean-data
substitute is rejected. The capacity invocation starts after the report's end
and cannot substitute a clean dataset. The reducer retains no
unbounded history: it owns one current phase, checked pass/fail bounds, fixed
counters, and only the latest 32 of at most 192 measured steps.

The aged-data staircase starts at 2,000 SEND/s. A step has ten minutes of
stabilization followed by twenty minutes of measurement; a passing coarse step
increases by exactly 25 percent with integer round-up. The first failed step
freezes the outer boundary, and refinement advances from the latest passing
rate in exact ten-percent increments until no additional increment lies below
the first failing rate. Capacity-gate failure records the boundary instead of
ending immediately, while a correctness failure remains an immediate product
failure. Once the boundary is found, the reducer schedules exactly 2,000
SEND/s for one uninterrupted 30-minute recovery. Recovery passes only when
error rate, latency, queues/inflight, cluster lag, resource pressure,
readiness/health, and lifecycle activity all return to their accepted ranges;
failure to recover is a product failure.

A live capacity-rate change is one concurrent, exact-fence, three-worker
control round under one shared deadline, launched asynchronously while the
single grant owner continues delivering the old one-second cadence. A partial
or invalid round is harness-invalid and leaves the owner plan unchanged. Once
all three running, traffic-ready statuses are valid, only the grant owner may
stage the new rate, and only for a Tick strictly after the control result. The
allocator applies it on that Tick, discards every credit generation retained at
the old rate, and starts the next stabilization or recovery clock only after
the complete new-rate grant is successfully delivered. Capacity evidence is
also collected asynchronously at the exact completed window while grants
continue. Parent cancellation is distinguished from a bounded evidence failure,
and the coordinator cancels and joins every in-flight evidence or rate round
before stopping workers or returning. No rate transition restarts workers or
service nodes, changes the assignment generation, or cleans the target dataset.

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

Service-node observation, worker control, service/load host metrics, and API-pool endpoints
are absolute credential-free HTTP or HTTPS URLs without query or fragment.
Their duplicate identity lowercases the scheme and host, canonicalizes IP text
and default ports, removes one terminal DNS root dot, and cleans the base path
while preserving meaningful non-root paths. Root-only hosts are rejected.
Gateway endpoints are credential-free TCP `host:port` values;
their duplicate identity canonicalizes the host/IP and numeric port. API and
gateway pools must not resolve to the same canonical network authority.
Only host-metrics declarations may carry filesystem selectors; their
mountpoint is an absolute clean path and their device is a bounded exact label.
