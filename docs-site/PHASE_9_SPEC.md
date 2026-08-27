# WuKongIM v3 Documentation — Phase 9 Specification

## Goal

Complete the bilingual guide foundation that later tutorials, SDK guides, and
API reference pages can link to. Readers should be able to decide whether
WuKongIM fits a workload, use one stable application vocabulary for messages,
channels, users, devices, and conversations, and understand when the current
plugin subsystem is appropriate. Distributed implementation vocabulary stays
in the server-architecture path. The pages must describe promoted v3 behavior
without turning implementation details into product guarantees.

## Published routes

- Product Overview / Core Capabilities
- Product Overview / Use Cases
- Core Concepts / Message
- Core Concepts / Channel
- Core Concepts / User
- Core Concepts / Device
- Core Concepts / Conversation
- Integration / Plugin Extensions

Every route above has matching Chinese and English MDX and is included in
search, sitemap, LLM outputs, and per-page Markdown.

## Source-of-truth boundaries

- Every deployment is a cluster. A one-node deployment is a single-node
  cluster and follows the same Controller, Slot, Channel, routing, and storage
  semantics. The stable route table contains 256 physical hash-slot fences,
  which can map to a different number of logical Slot Raft Groups. These
  implementation concepts belong in Server Architecture rather than the
  application-facing Core Concepts navigation.
- Product capability claims are workload-qualified. Per-channel ordering,
  durable append, replication, offline sync, multi-device sessions, presence,
  operations, plugins, and webhooks are current capabilities; no context-free
  maximum QPS, universal latency, or automatic production safety claim is
  published.
- WuKongIM owns communication infrastructure, not the product account system,
  business authorization, content governance, mobile push provider, analytics,
  or business database. Use-case pages must make these responsibilities and
  the required application-side components explicit.
- A message belongs to one Channel. `client_msg_no`, server message ID, and
  per-Channel message sequence are distinct identifiers. Ordering is scoped to
  a Channel, durable commit is distinct from online delivery, and RECVACK is
  distinct from the Channel commit boundary.
- Plain non-command `NoPersist` is a compatibility terminal-success branch
  without authority resolution or realtime delivery. Only command-style
  `NoPersist` (`SyncOnce` or an existing command-channel ID) enters transient
  authority routing and online delivery-plan admission. Neither branch has a
  durable sequence or offline recovery.
- A Channel is identified by channel ID and channel type. It owns message
  ordering and log replication, while Slot-owned metadata holds policy,
  membership, `ChannelRuntimeMeta`, and migration fences. Large groups use
  bounded membership mutations and paged post-commit fanout; public guidance
  must not imply loading 100,000 members into one in-memory request.
- UID is a business identity, device metadata is durable UID-owned metadata,
  and a concrete connection Session is owner-node-local. Distributed presence
  stores fenced virtual owner routes, not TCP session handles. Stored token
  metadata exists, but the default v3 Beta app composition does not by itself
  make product HTTP endpoints trusted or guarantee stored-token CONNECT
  validation.
- A conversation is a UID-owned projection over Channel-owned committed
  messages. `active_at` determines active-list order; `read_seq` and
  `deleted_to_seq` determine visibility and unread state. Projection or online
  delivery failure does not roll back an already committed Channel message.
- Plugins are node-local `.wkp` processes with node-local desired/observed
  lifecycle state. UID-to-plugin bindings are Slot-authoritative metadata.
  Send hooks are synchronous and fail closed by default; Receive and
  PersistAfter are bounded post-commit effects that cannot change SENDACK.
  Plugin-origin sends still pass through the message usecase and Channel
  authority. Plugin rollout, trust, timeouts, recursion, and secret handling
  remain operator responsibilities.

## Validation

- Navigation tests freeze the eight newly published routes, require both
  locale variants, and keep tutorials, SDK, API, Kubernetes, and all other
  planned routes excluded from public indexes.
- Static-output validation confirms every new guide route appears in sitemap,
  search, LLM outputs, and per-page Markdown.
- Local validation runs `bun run verify`, focused Go tests for the documented
  message, channel, user, conversation, presence, online, plugin, Slot,
  Channel, Gateway, and plugin-host contracts, and the full repository unit
  suite.
- Browser QA covers product capabilities, messages, conversations, and plugin
  extensions in both locales at desktop and mobile widths, including console
  output and horizontal overflow.

## Excluded

- Runtime refactors, new feature promises, configuration changes, or
  operational mutations.
- Scenario tutorials, SDK-specific instructions, API/protocol reference,
  Kubernetes deployment, legacy-site migration, and production cutover.
- Benchmark numbers without a reproducible workload, exhaustive internal type
  catalogs, or claims that plugins and webhooks are durable message queues.
