# WuKongIM v3 Documentation — Phase 18 Specification

## Objective

Make the API & Protocols domain match the current repository snapshot without
turning private or experimental interfaces into public integration promises.
Phase 18 changes documentation, schemas, generation, and drift checks only. It
does not change runtime routes, authentication, wire behavior, or storage.

## Surface classification

Every discovered interface belongs to one documented class:

| Class | Publication form | Stability and trust |
| --- | --- | --- |
| Product HTTP | Complete OpenAPI 3.1 plus one Fumadocs page per operation | 41 runtime operations; no built-in authentication; trusted backend only |
| Operations HTTP | OpenAPI 3.1 plus concise Fumadocs reference pages | Health, readiness, metrics, and node-local Top; protect with the operator network |
| Outbound Webhook | OpenAPI 3.1 `webhooks` plus protocol guidance | Three callback events; bounded, best effort, and unsigned |
| WKProto | Protocol pages and source-checked tables | Public binary core, codec-only packets, and reserved packets are distinct |
| WebSocket JSON-RPC | Experimental schema and support matrix | Codec exists, but the current Product Gateway path is not a supported client integration |
| Manager, Debug, Bench, MCP, node transport, and plugin RPC | Exhaustive inventory and boundary page | Operator-only, conditional, tool-specific, or cluster-internal; not Product HTTP |

The inventory closes documentation gaps by naming private interfaces and their
authority, not by granting public compatibility to them.

## Product HTTP contract

Add `contracts/product-http.openapi.json` as the complete current Product HTTP
snapshot. Its method/path set must equal the routes registered by the Channel,
User, Message, Conversation, and Routing adapters: 41 operations, no more and
no fewer. The existing golden-path, management, and message-send contracts stay
available as narrower adoption profiles and keep their receipt boundaries.

The complete contract records the runtime parser, response shapes, maintenance
`503`, weak or legacy validation, aliases, polymorphic responses, and caller-
supplied identity exactly. It may recommend a safer input profile, but it must
not claim that Gin rejects unknown fields or that the listener authenticates a
caller. Existing operation IDs remain stable so published URLs do not move.

Fumadocs generates one bilingual page per operation, grouped by the six current
tags: Users, Routing, Messages, Message Sending, Channels, and Conversations.
Tag indexes remain short; detailed request and response schemas stay on the
operation pages.

## Operations and Webhook contracts

`contracts/operations-http.openapi.json` owns `GET /healthz`, `GET /readyz`,
Prometheus `GET /metrics`, and `GET /top/v1/snapshot`. The docs distinguish
liveness from traffic admission, describe Top as a node-local observation, and
state that none of these endpoints has built-in authentication.

`contracts/webhooks.openapi.json` uses the OpenAPI 3.1 `webhooks` object for
`msg.notify`, `msg.offline`, and `user.onlinestatus`. It records the exact query
event selector, payload alternatives, HTTP-200-only success rule, finite retry,
bounded in-memory queues, lack of signing headers, and lack of crash replay.

Debug and Bench endpoints are not mixed into the stable Operations contract.
Their exact route inventory, enablement, bearer behavior, and instability are
published on the interface-inventory and stability pages.

## Client and internal protocols

The TCP binary page publishes fixed-header bits, base-128 remaining length,
exact packet body order, conditional fields, versioned sequence width, and
current limits. Its WebSocket carrier boundary records v13, exact-path upgrade,
client masking, bounded fragment reassembly, and the separation between
WebSocket control Ping/Pong and WKProto heartbeat. It preserves the distinction
between public Gateway handling, codec-only packets, and the benchmark-reserved
EVENT packet.

The encryption page documents the compatibility algorithm as implemented,
including X25519, Base64/MD5 key derivation, AES-CBC with PKCS#7, `msg_key`, and
`NoEncrypt`. It must explicitly state that this is not TLS, authenticated
encryption, or a modern AEAD construction.

The JSON-RPC page and downloadable schema describe codec capability separately
from Product Gateway support. They must not advertise connect, send,
subscribe, or unsubscribe as supported until runtime authentication,
correlation, bridge mappings, and end-to-end tests are fixed.

The interface inventory records Manager HTTP, Operations MCP, Cloud Analysis
MCP, Review Check MCP, node transport, and plugin RPC as private or tool
contracts. It includes exact counts and source anchors and corrects the Manager
`auth_on=false` behavior: most routes lose the permission middleware; only
explicit fail-closed categories retain their own gates.

## Machine-readable outputs

Published contracts are available from `/contracts/`. The Specifications group
links the complete Product HTTP, Operations HTTP, Webhook, JSON-RPC, and narrow
profile artifacts and labels their stability. Search, per-page Markdown,
`llms.txt`, `llms-full.txt`, sitemap, and static output include every published
bilingual route.

Source drift tests compare:

- Product HTTP method/path pairs with the 41-operation OpenAPI contract;
- base, Debug, and Bench registrations with their documented inventory;
- Manager registrations and permission resources with the private inventory;
- WKProto frame numbers, limits, and field order with frame/codec sources;
- JSON-RPC method conversion support with codec and bridge sources;
- Webhook events and payload fields with the runtime mapper and sender.

## Verification

Phase 18 requires focused API, Gateway protocol, JSON-RPC, webhook, Manager,
node-transport, and documentation tests; deterministic navigation/OpenAPI
generation; the complete documentation verification gate; a production static
export; and two-axis standards/spec review.

## Non-goals

- Runtime fixes for JSON-RPC, authentication, weak legacy validation, or
  Webhook durability/signing.
- Public support promises for Manager, Debug, Bench, MCP, plugin, node RPC, or
  agent-control interfaces.
- Expanding the protected JavaScript/Web golden-path receipt.
- Modeling binary, JSON-RPC, MCP, or node transport as fake HTTP paths.
