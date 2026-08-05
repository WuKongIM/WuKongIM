# WuKongIM v3 Documentation — Phase 10 Specification

## Goal

Publish the first bilingual scenario tutorials: direct chat and groups through
100,000-member workloads. Each tutorial must connect business-service
responsibilities, trusted HTTP mutations, client connection behavior, durable
message semantics, conversation state, and operational verification without
inventing an SDK API or weakening current security boundaries.

## Published routes

- Tutorials landing page
- Tutorials / Direct Chat
- Tutorials / Groups & Large Groups

Each route has matching Chinese and English MDX and is included in search,
sitemap, LLM outputs, and per-page Markdown. Message Push and AI & IoT remain
planned, visible, `noindex`, and excluded from those public indexes.

## Source-of-truth boundaries

- Product services own accounts, relationship and group lifecycles,
  authorization, moderation, mobile push, and durable business workflows.
  WuKongIM owns connection, Channel ordering, durable message logs, online
  delivery, offline sync, and UID-owned conversation projections.
- Current product HTTP routes have no general product-authentication
  middleware. Tutorial `curl` commands are trusted development/service-side
  examples and MUST NOT be presented as browser- or mobile-client calls.
  Stored token metadata is not automatically validated by the default v3 Beta
  Gateway composition.
- A direct send uses the peer UID with `channel_type=1`; the server derives the
  canonical person Channel. Applications MUST NOT construct or persist the
  internal canonical Channel ID as their relationship identity.
- A group uses a product-owned group ID with `channel_type=2`. The product
  service creates the Channel metadata and reconciles ordinary subscribers.
  Membership changes are durable Slot metadata and are distinct from the
  Channel message log.
- Durable sends retain a stable `client_msg_no`, reach quorum commit before a
  successful SENDACK/HTTP success reason, and keep online delivery,
  conversation projection, webhooks, and plugin effects outside that commit
  boundary.
- Conversation rows are UID-owned projections. Person Channel IDs are mapped
  back to peer UIDs by compatible HTTP adapters. Unread state is per UID and
  does not prove that every device received an online delivery. `ClearUnread`
  advances through the newest server-visible message; its optional request
  sequence is only a fallback when no newest message exists, not an exact
  client-side read-progress marker.
- Subscriber mutations are de-duplicated and internally chunked. Public
  large-group workflows additionally submit bounded application batches and
  reconcile checkpoints; they MUST NOT place a 100,000-member snapshot in one
  request. Partial progress across multiple requests is expected and repaired
  by the product-owned reconciler.
- `channel.large_group_subscriber_threshold` defaults to 500. After ordinary
  subscriber mutations, a count greater than the configured threshold marks
  the Channel large. Post-commit fanout pages members and builds bounded
  delivery plans; one durable message is not copied into 100,000 Channel logs.
- A successful group SENDACK does not prove all members are online, delivered,
  acknowledged, or projected into conversations. Capacity validation must
  cover hot-Channel commit, member paging, Presence routing, owner queues,
  Session writes, reconnect sync, CPU, memory, disk, network, and tail latency.

## Validation

- Navigation tests freeze the Tutorials landing page and first two published
  children while leaving Message Push and AI & IoT planned.
- Static-output validation confirms the three new routes appear in sitemap,
  search, LLM outputs, and per-page Markdown.
- Local validation runs `bun run verify`, focused Go tests for the compatible
  API, message, channel, conversation, and channelappend contracts, and the
  full repository unit suite.
- Browser QA covers both tutorials in both locales at desktop and mobile
  widths, including console output and horizontal overflow.

## Excluded

- New runtime behavior, SDK method signatures, API reference completeness,
  production authentication implementation, or benchmark promises.
- Message Push, AI streaming, IoT command tutorials, Kubernetes, legacy-site
  migration, deployment, DNS, or production cutover.
