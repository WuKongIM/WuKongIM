# WuKongIM v3 Documentation — Phase 13 Specification

## Goal

Publish a reader-oriented SDK chooser and the platform-neutral
client-integration foundation that sits between the executable JavaScript/Web
golden path and SDK-specific API references. An integrator must be able to find
current official source, select an exact platform candidate, and design
identity, connection, messaging, payload, conversation, offline, push,
multi-device, reconnect, and error behavior without guessing method names or
promoting an unverified SDK platform.

Phase 13 also publishes source-checked Channel Type, Device Flag / Level, and
Message Flag dictionaries so prose, clients, protocol tooling, and LLM output
share one current set of numeric facts.

## Audience and completion outcome

The primary audience owns an end-user client plus a trusted product backend.
After this phase, that integrator can produce:

1. a platform and SDK shortlist that separates repository availability,
   tutorial publication, and executable verification;
2. a backend-controlled identity and credential contract;
3. a client state machine that separates transport open, CONNECT, recovery,
   and product-ready state;
4. a message state model that separates durable commit, online write,
   RECVACK, and business completion;
5. distinct conversation, unread, message-pull, and pagination cursors;
6. a push design in which durable sync remains the message authority;
7. a multi-device policy backed by a product device registry;
8. a bounded, phase-aware reconnect and retry policy;
9. repeatable release evidence tied to an exact compatibility target.

## Published routes

Phase 13 adds matching Chinese and English MDX for exactly these 13 routes:

- `/sdk/choose-sdk`
- `/sdk/common-guides`
- `/sdk/common-guides/identity-and-token`
- `/sdk/common-guides/initialization-and-connection`
- `/sdk/common-guides/messaging`
- `/sdk/common-guides/custom-messages`
- `/sdk/common-guides/conversations-and-unread`
- `/sdk/common-guides/offline-and-push`
- `/sdk/common-guides/multi-device`
- `/sdk/common-guides/reconnect-and-errors`
- `/api/dictionaries/channel-types`
- `/api/dictionaries/device-flags`
- `/api/dictionaries/message-flags`

The SDK and dictionary indexes are updated to route readers into this material.
At the Phase 13 boundary, every non-JavaScript platform group, complete
platform API reference, platform-capability guide, and upgrade guide remained
planned. Phase 15 later published the source-aligned EasySDK path, and Phases
19 through 24 published the maintained full-SDK platform groups and remaining
reference chapters. Those later publication changes do not broaden Phase 13's
JavaScript/Web executable-evidence boundary.

## SDK chooser publication boundary

The chooser preserves the legacy EasySDK and WuKongIMSDK names only as source
discovery aids. It must:

- link repositories under the current official WuKongIM organization;
- separate source availability, site tutorial publication, and executable
  exact-version verification;
- keep JavaScript/Web `wukongimjssdk@1.3.5` as the only client snapshot eligible
  for the existing receipt;
- send non-JavaScript readers to the current official repository, require them
  to select and record an exact tag, and link Common Guides without publishing
  platform method names;
- avoid unqualified integration-time, universal-platform, family-wide feature,
  current-version, system-requirement, or license claims;
- use capability requirements and a minimal acceptance loop instead of
  selecting only from an “IM app / non-IM app” label.

## Cross-SDK publication boundary

“Common” means a server- and wire-proven behavior contract, not one shared
client API. Every common guide must:

- identify itself as a cross-SDK behavior guide;
- link to the JavaScript/Web compatibility snapshot;
- avoid promising class, method, callback, package, OS, or background-runtime
  behavior for a planned platform;
- distinguish current server semantics from application recommendations;
- keep product accounts, authorization, device registration, push providers,
  business receipts, and local UI state application-owned;
- preserve the default v3 Beta stored-token verification warning.

The JavaScript/Web golden path remains the only executable client scenario and
the only client target eligible for its existing verification receipt.
Publishing common guides does not expand `compatibility.json`, its OpenAPI
subset, or browser support.

## Guide contracts

### Identity and token

- UID is a stable product identity; Device Flag is a category; `device_id` is
  one concrete installation; Device Level is same-category conflict policy.
- Credentials originate from a trusted backend, remain short-lived and
  revocable, and never grant Product HTTP management capability to a client.
- `/user/token` persists device-token metadata, but the default app composition
  still creates the Gateway Authenticator without stored-token verification.
- Revocation is complete only after durable invalidation, Session closure, and
  a proven reconnect rejection.

### Initialization and connection

- One application-owned connection manager serializes connect, disconnect,
  identity switching, and recovery.
- A recommended state model separates `connected` from `ready`.
- `/route` supplies configured ingress and is neither product authorization
  nor proof of one Session's owner node.
- CONNECT is the first WKProto packet. Session payload encryption does not
  replace TLS.

### Messaging and custom payloads

- Channel order is per Channel. `client_msg_no`, server message ID, and
  Channel message sequence remain separate identities.
- Durable SENDACK, online RECV, RECVACK, and business completion remain
  separate outcomes.
- Uncertain sends may retry with the same `client_msg_no`; overlapping wire
  attempts require distinct `client_seq` values.
- Product payloads use an explicit, forward-compatible versioned envelope and
  safe unknown-type fallback. Product HTTP Base64 is not an SDK payload API.

### Conversations and unread

- Conversations are transient UID-owned projections over membership and
  Channel committed state, not persisted message truth.
- `read_seq` is a badge floor, not a pull cursor or peer read receipt.
- Conversation opaque cursor, per-Channel pull cursor, local commit position,
  displayed position, and product read state remain distinct.
- Empty canonical list results do not imply completion; only `done=true` does.
  Temporary hydration failures remain `unresolved` rather than silent loss.

### Offline, push, and multi-device

- Durable Channel synchronization remains authoritative when no provider
  notification arrives.
- `msg.offline` reports UID candidates and is a bounded, in-memory,
  retry-limited, best-effort webhook effect. It is not a per-device list.
- The product owns provider credentials, device tokens, notification policy,
  outbox durability, provider retries, and provider receipts.
- Device Level is not a product role. Default CONNECT receives Slave level
  until a real token verifier returns trusted device metadata.
- Conversation and badge state are UID-owned; push token, drafts, downloads,
  and OS background state remain device-owned.

### Reconnect and errors

- Failures are classified by network, route HTTP, CONNACK, local SEND
  admission, SENDACK, synchronization, and product-handler phases.
- Authentication and policy rejection do not blind-retry. Stale route refreshes
  ingress, and temporary pressure uses bounded backoff with jitter and a total
  deadline.
- CONNECT success enters recovery before ready. A reconnect generation fence
  prevents an old attempt from replacing a newer Session.
- Metrics stay low-cardinality and exclude UID, Channel ID, client message ID,
  address, token, and complete payload.

## Protocol dictionaries

Shared TypeScript catalogs render the human tables and Markdown/LLM
supplements. Tests compare them with current Go authorities.

### Channel Type

- Publish exact current values `ChannelTypePerson=1` through
  `ChannelTypeAgentGroup=12`.
- Mark Person and Group as integration baselines, CustomerService as the
  source-deprecated compatibility type, and the remaining types as specialized.
- Enum presence never promises a complete public API or SDK implementation.

### Device Flag and Device Level

- Publish `APP=0`, `WEB=1`, `PC=2`, and server-reserved `SYSTEM=99`.
- Publish `DeviceLevelSlave=0` and `DeviceLevelMaster=1`.
- Explain that effective Master conflict policy depends on authenticated
  Device Level entering the Gateway Session.

### Message Flags

- Publish fixed-header bit positions for `NoPersist`, `RedDot`, `SyncOnce`,
  and `DUP` from `pkg/protocol/codec/common.go`.
- Publish Setting values for receipt, signal, no-encrypt, topic, and stream
  from `pkg/protocol/frame/setting.go`.
- Plain non-command `NoPersist` is compatibility success without authority,
  append, or realtime delivery. Only command-style `NoPersist` enters transient
  online delivery; neither branch has offline recovery.
- `RedDot`, receipt intent, SENDACK, RECVACK, and product read or execution
  receipts remain distinct.

## Drift repair

Phase 13 repairs two Phase 3 integration statements that described all
`NoPersist` sends as realtime. The aligned text now matches the already
published core-concept, tutorial, architecture, Phase 9, Phase 11, and current
channelappend runtime contracts.

## Machine-readable publication

Every new route enters locale-correct navigation, search, sitemap,
`llms-full.txt`, and per-page Markdown. Dictionary pages receive generated
Markdown tables from the same catalogs used by React components. Static-output
checks assert representative first and last values so rendered human pages and
LLM artifacts cannot silently diverge.

## Validation

The fast gate must cover:

- the exact 13-route publication set and bilingual MDX parity;
- official SDK source discovery without broadening executable compatibility;
- continued planned status for non-JavaScript platform tutorials;
- common-guide boundary text plus compatibility links;
- Channel Type names and values against `pkg/protocol/frame/common.go`;
- Device Flag and Device Level constants against `protocolmeta`;
- fixed-header bit ordering against `pkg/protocol/codec/common.go`;
- Setting names and values against `pkg/protocol/frame/setting.go`;
- generated dictionary facts in per-page Markdown;
- lint, typecheck, static export, internal links, search, SEO, sitemap,
  accessibility structure, and LLM outputs.

Source-aligned focused Go tests cover protocol frame/codec contracts,
channelappend `NoPersist` semantics, user device behavior, Gateway
authentication, conversation state, delivery, presence, and webhooks. This
content-only phase does not create a second full browser matrix for planned
SDK platforms.

## Excluded

- Runtime, authentication, SDK, protocol, configuration, or Product HTTP
  behavior changes.
- Claims that default v3 Beta token storage is production CONNECT validation.
- unqualified SDK rankings, current release matrices, or platform-specific API
  methods without versioned executable evidence.
- Publishing Android, iOS, Flutter, UniApp, HarmonyOS, JavaScript API reference,
  platform capabilities, or upgrade guides.
- Expanding the JavaScript golden-path OpenAPI subset or verification receipt.
- Complete client-protocol reference, Kubernetes, hosting, analytics, DNS, or
  production cutover.
