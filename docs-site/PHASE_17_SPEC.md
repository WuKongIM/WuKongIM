# WuKongIM v3 Documentation — Phase 17 Specification

## Objective

Publish one bounded Product HTTP message-send operation and the first
source-aligned WKProto client-protocol baseline. Keep both surfaces concise,
bilingual, and explicitly non-exhaustive. Phase 17 is documentation and
contract work; it does not change runtime routes, wire behavior, authentication,
or storage semantics.

## Published routes

Phase 17 publishes matching Chinese and English content for:

- `/{lang}/api/product-http/message-send`
- `/{lang}/api/product-http/message-send/sendChannelMessage`
- `/{lang}/api/client-protocols`
- `/{lang}/api/client-protocols/connection-lifecycle`
- `/{lang}/api/client-protocols/packet-types`

The Product HTTP Message Sending tag index remains separate from the existing
bounded-sync Messages group. The client-protocol index introduces only the two
published WKProto pages and keeps the remaining protocol work visibly planned.

## Messaging OpenAPI boundary

Add `contracts/product-http-messaging.openapi.json` as the third bounded,
non-exhaustive OpenAPI 3.1 contract. It contains exactly
`POST /message/send`, with `sendChannelMessage` as its `operationId`, and
generates one independent Fumadocs operation page under the Message Sending
tag.

The documented request is the canonical, Channel-targeted trusted-backend
subset: required `from_uid`, `channel_id`, non-zero `channel_type`,
`client_msg_no`, and Base64 `payload`. The response exposes `message_id`,
`message_seq`, and wire `reason`. The contract must describe HTTP errors separately from a
successful HTTP response whose `reason` is not `ReasonSuccess`.

Legacy `sender_uid`, request-scoped `subscribers`, raw `setting`, `topic`,
`expire`, and `no_persist` / `sync_once` compatibility forms remain outside this
contract. Their runtime presence does not make them published API fields.

The operation declares `security: []` and
`x-wukongim-trust: trusted-backend-only`. This records the absence of built-in
Product HTTP authentication; it does not authorize anonymous or public access.
The static playground stays disabled, and any request example must state the
trusted-backend boundary.

This contract does not alter the three-operation
`javascript-web-quickstart.openapi.json` whitelist, compatibility tuple, browser
scenario, or Phase 12 verification receipt. The receipt does not attest
`POST /message/send` or the new protocol pages.

## Client-protocol baseline

### Connection lifecycle

The lifecycle page must distinguish transport open, CONNECT authentication,
successful CONNACK, application recovery, and ready state. It records these
current boundaries:

- CONNECT is the sole first decoded frame; another frame before authentication,
  an extra frame in the same initial batch, or traffic while authentication is
  pending is a protocol violation and closes the Session.
- Only a successful CONNACK opens the authenticated Session. A rejected
  CONNACK closes it; failure Reason Codes are handled by phase and are not
  generic retry signals.
- PING/PONG maintains connection liveness. Reconnect and message recovery are
  separate SDK/application responsibilities and must not be presented as one
  wire acknowledgement.
- Payload session encryption, when enabled, does not replace TLS, product
  identity, token verification, or Product HTTP protection.

### Packet types

The packet page publishes the exact current Frame Type number, direction, and
support classification for all values 0–12. It explains the public
CONNECT/CONNACK, SEND/SENDACK, RECV/RECVACK, and PING/PONG families while
marking DISCONNECT as codec-only. It links rather than duplicates the
Message Flags, Device Flag, Channel Type, and Reason Code dictionaries.

`SUB` and `SUBACK` remain enum/codec facts but are not published as supported
product integration flows. `EVENT` remains reserved for the controlled
benchmark terminal-fence path and is not a general client event API. Unknown or
unsupported frame types fail closed.

## Deferred protocol pages

These routes remain planned:

- `tcp-binary`: exact byte layouts, variable-length framing, limits, and
  versioned field ordering need a separate exhaustive codec contract.
- `json-rpc`: method and notification coverage must first be reconciled with
  the current JSON-RPC adapter and schema.
- `encryption`: handshake, key derivation, payload validation, opt-out flags,
  and TLS responsibilities need a dedicated security review.
- complete OpenAPI, JSON-RPC Schema, and protocol-changelog downloads under
  `specifications`: the three bounded HTTP contracts and this WKProto baseline
  are not a complete v3 specification.

WKProto, TCP frames, and JSON-RPC must remain protocol-specific documentation;
they must not be modeled as fake HTTP paths in an OpenAPI contract.

This is the Phase 17 boundary. Phase 18 later publishes the byte-layout,
encryption, explicitly unsupported experimental JSON-RPC Schema, and complete
specification routes after their separate source contracts were reconciled;
the deferred list above is not current publication status.

## Authoritative sources

- Product HTTP route, DTO mapping, compatibility errors, and tests:
  `internal/access/api/message_send.go`, `message_error_map.go`, and
  `message_legacy_test.go`.
- Durable-send behavior and reason mapping: `internal/usecase/message` plus its
  focused send tests.
- Frame numbers, protocol version, shared values, and packet fields:
  `pkg/protocol/frame`.
- Fixed header, body ordering, message-sequence versions, payload limits, and
  codec behavior: `pkg/protocol/codec`.
- First-frame authentication, CONNACK ordering, bounded admission, and Session
  close behavior: `pkg/gateway/core`, `pkg/gateway/auth.go`, and
  `pkg/gateway/FLOW.md`.
- Product frame handling and reserved EVENT boundary:
  `internal/access/gateway` and its `FLOW.md`.

Code, schemas, and focused tests remain authoritative over prose. Legacy
documentation and `resources/api/openapi.json` are inventory only.

## Verification

Phase 17 must verify:

- the messaging contract contains exactly one operation and preserves its
  canonical request, response, error, and trusted-backend boundaries;
- deterministic bilingual operation-per-page generation, concise Messages tag
  indexes, and a disabled playground;
- exact bilingual publication of the three client-protocol routes while
  `tcp-binary`, `json-rpc`, `encryption`, and complete specifications remain
  planned and `noindex`;
- Frame Type values, latest protocol version, public-versus-reserved packet
  classification, and lifecycle ordering against current Go authorities;
- representative request, response, packet, lifecycle, security, and deferral
  facts in rendered HTML, locale-isolated search, sitemap, per-page Markdown,
  and `llms-full.txt`;
- unchanged Phase 12 golden-path operation whitelist, compatibility output,
  executable scenario, and verification receipt scope;
- focused Product HTTP send, protocol codec/frame, Gateway lifecycle, and
  access-adapter tests, followed by the full documentation verification gate.

## Non-goals

- Runtime, SDK, protocol, authentication, authorization, or storage changes.
- A complete Product HTTP, OpenAPI, WKProto, TCP, or JSON-RPC reference.
- Publishing message batch, message event, message sync/syncack, CMD binding,
  request-scoped subscribers, transient send modes, or legacy request aliases.
- Claiming HTTP success as durable commit, realtime delivery, RECVACK, read
  state, or business completion.
- Expanding SDK/browser compatibility, production readiness, or the Phase 12
  verification receipt.
