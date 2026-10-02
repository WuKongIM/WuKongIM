---
scope: package
summary: Owns Hash-Slot-scoped metadata tables, deterministic batches, membership directories, snapshots, restore, and cache invalidation.
---

# Metadata Storage Flow

## Responsibility
This package stores entity-owned metadata on shared DB primitives through stable `Shard` handles; it must not import Pebble directly.
It does not own product business policy or expose engine-specific APIs.

## Boundaries

- Table specifications and the registry drive primary/index behavior, inspect,
  snapshot, backup, restore, and deletion.
- Multi-Hash-Slot batches lock shards in sorted order, commit once, then publish
  cache invalidations. A neighboring logical rejection may cause one isolated
  rebuild with fresh staged state while retaining the original locks.
- There is no conversation table; table IDs 6 and 7 remain reserved and must
  not be reused.

## Main Flows

1. Typed tables store Channel policy, subscribers, latest state, runtime and
   migration state, plus UID users, devices, memberships, plugins, and events.
2. Ordinary conversation directory scans UID-owned
   `user_channel_membership` by `(uid, activated_at desc, channel_id,
   channel_type)` using encoded string order (length before bytes), and
   returns the complete cursor and `done` flag. Legacy response string sorting
   is a usecase concern, not this cursor index order.
3. Snapshot and restore cover registered row, index, and system spans; restore
   installs isolated portable metadata, replays ordered Slot FSM commands, and
   verifies canonical digests.
4. Active migration tasks expose bounded ID/type cursor pages over the existing
   index, allowing fair executor scheduling without changing stored encodings.
5. Business Channel point reads use a fixed 8,192-entry LRU. Mutations and
   restore invalidate affected or complete cache state after durable commit.
6. Runtime metadata point reads reuse owned decoded rows in an 8,192-entry,
   8 MiB LRU. Typed mutations/snapshots and offline restore chunks disable cache
   hits/fills while active, advancing Hash-Slot generations on entry and exit
   even after failure. Late misses cannot fill a newer generation. Replica slices
   are cloned on return, and authority/routing checks remain outside storage.
7. MQTT sessions separate lifetime/owner generations, revision and deadline pages;
   ended rows retain UID binding. Optional Session column 30 records child reclamation; bounded intent pages precede atomic cursor/accounting/inflight range deletion. Index 3 discovers ended generations only after bounded historical backfill certifies table-22 System-1 coverage; kind 23 pins and strictly checks candidates. Same-batch range masks preserve replay grouping independence; newer lifetimes, source tombstones and Will remain. Subscription writes fence owner/revision and
   preserve generation on option replacement; child receipts prove exact retries.
   Delivery cursors separate backlog accounting, window admission and completion.
   Explicit cancellation Init and offline window release require closed intent; admission/ACK remain active-only and ordinary Init/Account keep their fences.
   Qualified charge receipts use cursor System 1; append/debit and quota ending commit atomically.
   Read kind 18 pins the head; consumption verifies head/successor before unlinking. The bounded inflight
   list preserves earliest ACK gaps; exchange/cursor/session updates are atomic,
   and recovery uses immutable references in original send order.
   Source/UID bindings retain tombstones and discovery/recovery indexes; unknown boundaries block retention. UID-only optional drain columns 29–32 retain a separate monotonic closed-cursor scan; ordinary UID removal requires completed drain proof, while explicit lifetime ending remains separate.
   Retention and candidate pages pin at most limit+1 strict primary/index witnesses, rejecting missing or stale entries.
   Active-source discovery seeks retention-index prefixes; replay discovery also retains primary tombstones.
   Each pinned scan checks at most 65 owner witnesses, skipping whole subscriber prefixes.
   Tombstones keep cleanup discoverable without restoring consumer responsibility or proving GC.
   Will records outlive Session replacement. Session transitions and quota endings
   resolve old Will atomically; new ownership may install a new configuration.
   Delays, execution leases and receipts are distinct; optional Will columns 35/36 retain Preparing/Prepared/Started and frozen hook output. Exact live executors advance phases; takeover preserves them. Rejected/Sealed alone may finish an expired exact Started tuple from trusted durable seal/current-denial proofs, without changing its frozen body. Terminal shapes are strict and bodies are bounded/redacted.
   Bounded MQTT reads pin Session, children and indexes; kind 19 pins channel/member/sequence without the live channel cache.
   Kind 20 scans stable UID directory primary keys, including hidden/tombstoned and non-person candidates; UID binding checkpoints share its 4096-byte ID bound.
   Kind 21 pins person directory/runtime/admission progress; table 26 System 1 retains monotonic invalidations on runtime deletion. Kind 22 pins runtime plus table 3 System 1 retirement without live-cache reads. Private snapshots never replace writable shards; evidence grants no policy or redispatch authority.

7. Startup-only snapshot installation validates the complete immutable stream
   before deletion, writes at most 8 MiB or 65,536 records per batch (one larger
   valid record may occupy its own batch), and fences incomplete installs with
   a physical-Slot global marker. Completion atomically publishes the snapshot
   applied index and removes the marker. The caller must keep the Slot absent
   until installation succeeds; ordinary runtime replacement remains atomic.

## Invariants and Failure Semantics

- Recovery certificates share the FSM mutation batch and applied watermark.
  Database incarnation, sequence seal and incomplete-install state fence reuse.
  A stale live anchor cannot revive a certificate after an unclassified write.
  Known disjoint FSM writes and fenced startup installs invalidate their own
  certificate, preserving neighboring Slots even without a snapshot anchor.
- Event sequence pages scan a pinned native iterator and retain a bounded heap,
  so event-key order cannot truncate results before the sequence cursor.
- Offline event import installs one exact historical projection, its last event
  idempotency result and the full message cursor atomically. It never replays
  reducers; exact retries succeed and changed or advanced target state fails.
  Shared preflight validation rejects invalid projection/cursor combinations
  and projections that cannot fit the bounded native sequence page.
- Membership writes update obsolete/new activation index keys atomically;
  ordinary SEND never touches membership.
- Subscriber `source_version` fences stale cross-Slot writes. Rejoin resets
  visibility from one captured Channel tail; personal read/hide/activation
  preserves source version and rejects tombstones.
- Command-channel membership is a separate UID table with start/ACK sequence
  and no ordinary activation, read, or delete fields.
- Subscriber rows, count, mutation version and join incarnations commit after UID sort/deduplication.
  Re-add preserves incarnation; removal/rejoin allocates from table 5 System 1; deletion retains its high water.
  Range tombstones fence staged/disk rows. Legacy empty rows mean 1; snapshots/JSONL preserve identity.
- Person directory incarnation changes atomically advance the runtime append route, rejecting overflow. Runtime metadata, Channel latest sequence and event reducers stay monotonic
  and idempotent; runtime deletion retains table 3 System 1 authority floors and atomically withdraws person-directory tasks/readiness. Only explicit create reopens a retired identity above its floor; cold callers must reread assigned versions. Create-only batches never overwrite existing rows.
- MQTT session CAS cannot rebind UID or regress generations. Snapshot/inspection
  includes the row and deadline index; storage CAS alone proves no owner fencing.
  Product MQTT is default-off and opt-in as a development preview; full rollout qualification remains pending.
  Offline `HasMQTTState` seeks whole registered MQTT row/index/system keyspaces,
  including retained/orphan fences, independently of caller Slot ranges.
- The Channel read cache is capacity-bounded, independently locked from shard
  lookup, and exposes current entries and capacity through `MetricsSnapshot`.
- An imported `conversation_hidden_through_seq` is list-only state. Optional
  fixed-value tails preserve old rows; marked rows require matching binaries.
  Same-generation projections preserve it, while a new source generation replaces it.

- Channel-owned message-update tables store latest payload/index, head/incarnation and replica activation proof, idempotency results, and separate body-free pending checkpoints. CAS and notification progress use same-batch overlays. Pinned reads bind head/index/body; a bounded Slot group shares one request-scoped snapshot across its logical shards after the caller establishes its fresh authority/apply barrier; an update sequence of zero proves dependent rows empty only within that snapshot, allowing exact-ID reads to stop before unused point lookups; channel deletion removes every edit span, and bounded retention-index cleanup removes target payloads, requests and pending state after the original retention floor.

Metadata table 28 stores the one bounded, body-free MQTT storage ledger. Encoded/decoded rows share a 256 KiB limit for at most 1,024 historical nodes; exact CAS results preserve bootstrap debt and membership revision. `mqtt_storage.go` owns ledger validation and transaction semantics.
- User and source-Channel send policies have independent apply-time CAS versions.
  Dedicated policy and optional Channel-info mutations return the previous policy
  from atomic same-batch apply for audit, without changing persisted rows; they observe same-batch state;
  ordinary user/channel upserts preserve policy. Permission snapshots return
  policy-only user projections and point membership facts. Format 2 requires
  matching binaries; offline transfers preserve exact policy values and versions.

## Read First

- [Metadata database](db.go), [Schema registry](schema.go), [Transaction helpers](tx_helpers.go), [Snapshots](snapshot.go)

## Update Triggers
Update when ownership, batches, memberships, indexes, source fences, runtime/event state, snapshots, restore or caches change.
