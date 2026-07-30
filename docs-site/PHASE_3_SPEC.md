# WuKongIM v3 Documentation — Phase 3 Specification

## Goal

Publish the bilingual business-integration path that follows Quick Start. A
reader must understand which responsibilities stay in the product service, how
clients discover and connect to WuKongIM, how messages cross the system
boundary, and how to consume supported webhook events safely.

## Published routes

- Integration overview
- Integration Architecture
- Authentication
- Messaging
- Webhooks

Every route above has matching Chinese and English MDX and is included in
search, sitemap, LLM outputs, and per-page Markdown. Plugin Extensions remains
planned.

## Source-of-truth boundaries

- The product service owns accounts, authorization, credential issuance,
  business relationships, moderation, and webhook side effects.
- Clients use `/route` to discover configured gateway addresses and use a
  client SDK or protocol implementation for the long-lived connection.
- The current app gateway performs CONNECT negotiation and encryption, but its
  composition does not enable stored-token verification. `/user/token` stores
  compatible device-token metadata; documentation must not present the current
  default build as a complete production authentication boundary.
- Product HTTP routes, including `/user/token` and `/message/send`, require a
  trusted network or an authenticated reverse proxy in production.
- `/message/send` accepts a base64 payload and returns `message_id`,
  `message_seq`, and a protocol `reason`. On the default persistent-message
  path, a successful SENDACK reflects the durable append decision; delivery,
  conversation projection, plugins, and webhooks are downstream side effects.
  Explicit `no_persist` messages do not carry that durability guarantee.
- Webhooks support `msg.notify`, `msg.offline`, and `user.onlinestatus`.
  Delivery is bounded and best-effort, only HTTP 200 is success, retries are
  finite, and the current sender adds no signature header.
- Architecture and flow visuals use maintainable text-native diagrams. Raster
  artwork is not required for this phase.

## Validation

- The navigation test freezes the five newly published routes and requires
  matching Chinese and English MDX.
- Static-output validation confirms every published route appears in sitemap,
  search, LLM outputs, and per-page Markdown while Plugin Extensions stays
  excluded and noindex.
- Local validation runs the complete `bun run verify` workflow and browser QA
  in both locales.

## Excluded

- Plugin lifecycle and extension implementation guidance.
- SDK-specific installation or API examples.
- Full HTTP API and client-protocol reference material.
- Production deployment, TLS termination, secret distribution, or identity
  provider implementation.
