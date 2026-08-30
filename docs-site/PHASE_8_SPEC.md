# WuKongIM v3 Documentation — Phase 8 Specification

## Goal

Publish the bilingual server-architecture path. A reader must be able to trace
one cluster decision from Controller intent through Slot authority and Channel
runtime ownership, then follow one client message from Gateway admission to
durable acknowledgement and asynchronous delivery. The pages must describe the
current promoted runtime instead of carrying forward stale package paths or
pre-v3 topology assumptions from older wiki material.

## Published routes

- Architecture overview
- Controller
- Slots
- Channels
- Transport
- Message Flow
- User Routing

Every route above has matching Chinese and English MDX and is included in
search, sitemap, LLM outputs, and per-page Markdown.

## Source-of-truth boundaries

- Every deployment remains a cluster, including a single-node cluster. The
  default route table keeps 256 stable physical hash-slot fences; those hash
  slots map onto logical Slot Raft Groups and must not be described as 256
  independent Raft Groups.
- `pkg/controller` owns durable cluster intent. Controller voters replicate
  commands through Controller Raft; non-voters mirror the materialized state
  file. The Controller Raft WAL and applied boundary are authoritative, while
  `cluster-state.json` is the atomically saved materialized state. Preferred
  leaders are intent; observed Raft leaders remain authoritative at runtime.
- `pkg/slot` owns distributed metadata, not Channel message logs. A key is
  hashed to a physical hash slot, mapped to a logical Slot Raft Group, and then
  routed to that group's observed leader. Mutations commit through Multi-Raft
  and a Slot FSM; authoritative reads follow the current leader and fail closed
  when routing or leader evidence is unavailable.
- `pkg/channel` owns ordered Channel logs. Slot-owned `ChannelRuntimeMeta`
  supplies leader, replicas, ISR, epochs, retention, and write-fence intent;
  Channel runtime replicas enforce those fences, append durably, replicate,
  and advance the committed high watermark. A Channel leader is distinct from
  the Slot leader that owns its metadata.
- Client TCP/WebSocket Gateway traffic and node-to-node cluster transport are
  separate boundaries. Cluster transport multiplexes bounded typed RPC, Raft,
  replication, and notification traffic over advertised node addresses. Queue,
  payload, concurrency, timeout, and backpressure limits are part of the
  correctness boundary rather than optional performance decorations.
- The durable send path is Gateway -> access adapter -> entry-agnostic message
  usecase -> authority-routed channelappend writer -> cluster Channel service ->
  Channel quorum commit -> SENDACK. Conversation projection, recipient routing,
  online delivery, plugins, and webhooks are post-commit effects and cannot
  retroactively change an acknowledged durable append.
- Online sessions are node-local. UID presence authority is an in-memory,
  target-fenced directory owned only for hash slots currently led by the node.
  Activation is pending until authority registration and conflict actions
  succeed; delivery resolves exact authority targets, groups routes by owner
  node, and validates the final session locally. Unknown or stale targets fail
  closed.
- Diagrams are explanatory projections of these boundaries. They must not
  imply that all data shares one consensus group, that Controller state carries
  high-frequency sessions, or that asynchronous delivery is part of Channel
  quorum commit.

## Validation

- Navigation tests freeze all newly published routes, require both locale
  variants, and keep Kubernetes, SDK, API, tutorials, and remaining guide pages
  planned.
- Static-output validation confirms every architecture route appears in
  sitemap, search, LLM outputs, and per-page Markdown while planned routes
  remain excluded.
- Local validation runs the complete `bun run verify` workflow, focused Go
  tests for the documented Controller, Cluster, Slot, Channel, Transport,
  Gateway, presence, send, channelappend, and delivery contracts, and the full
  repository unit suite.
- Browser QA covers architecture overview, message flow, and user routing in
  both locales at desktop and mobile widths, including console output and
  horizontal overflow.

This is the Phase 8 boundary. Later phases publish the maintained Kubernetes,
SDK, API, tutorial, and remaining guide routes; Phase 24 leaves no maintained
navigation entry planned.

## Excluded

- Runtime refactors, new architecture, configuration changes, or operational
  mutations.
- Copying old wiki diagrams or package names without validating them against
  the current promoted runtime.
- Exhaustive internal type references, unstable implementation call graphs,
  benchmark numbers, or tuning prescriptions detached from workload evidence.
- API/protocol reference, SDK internals, scenario tutorials, Kubernetes
  deployment, legacy-site migration, and production cutover.
