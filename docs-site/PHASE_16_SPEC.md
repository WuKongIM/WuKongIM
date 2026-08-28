# WuKongIM v3 Documentation — Phase 16 Specification

## Objective

Publish the remaining Channel and Conversation operations inside the
already-published Product HTTP group as a source-calibrated, non-exhaustive
management Beta subset. Keep
the Phase 12 JavaScript/Web golden-path operation whitelist and receipt scope
frozen. Recalibrate only its shared restore-maintenance `503` schema from the
old compatibility envelope to the current runtime `{error,message}` body.

Phase 16 is documentation and contract work. It does not change runtime
routes, validation, authentication, or storage behavior.

## Published routes

Phase 16 keeps matching Chinese and English tag indexes at:

- `/{lang}/api/product-http/channels`
- `/{lang}/api/product-http/conversations`

Each index links to independent operation pages below its tag route, using the
OpenAPI `operationId` as the final path segment. The pages are generated from
`contracts/product-http-management.openapi.json` with Fumadocs
`per: 'operation'` and `groupBy: 'tag'`. An operation page contains exactly one
HTTP operation; tag indexes contain only concise cards.

## Operation whitelist

The Channel page publishes exactly these ten trusted-backend operations:

- `POST /channel`
- `POST /channel/subscriber_add`
- `POST /channel/subscriber_remove_all`
- `POST /tmpchannel/subscriber_set`
- `POST /channel/blacklist_add`
- `POST /channel/blacklist_remove`
- `POST /channel/blacklist_remove_all`
- `POST /channel/whitelist_add`
- `POST /channel/whitelist_remove`
- `POST /channel/whitelist_remove_all`

The Conversation page publishes exactly these six canonical operations:

- `POST /conversation/list`
- `POST /conversation/retry`
- `POST /conversations/clearUnread`
- `POST /conversations/setUnread`
- `POST /conversations/delete`
- `POST /conversations/activate`

Each operation declares `security: []` and
`x-wukongim-trust: trusted-backend-only`. This records the absence of built-in
authentication; it does not authorize anonymous access. The generated static
reference keeps its interactive playground disabled and contains only reviewed
trusted-backend cURL examples.

## Runtime-aligned HTTP behavior

- Successful mutations return `200 {"status":200}`.
- List and retry return the canonical bounded Conversation page shape.
- Input, validation, routing, use-case, and storage failures on this subset are
  exposed as `400 {"msg":"...","status":400}`. Exact message text is not a
  stable machine contract.
- Restore maintenance returns
  `503 {"error":"maintenance","message":"restore maintenance is active"}`.
- `uid` is a request field, not authenticated identity.
- Person-Channel Conversation IDs are projected to the peer UID at the HTTP
  boundary.
- Conversation cursors are opaque; an empty page is not complete unless
  `done=true`; coverage is persisted only after a complete traversal.
- `read_seq` is a monotonic badge floor, not a pull cursor or read receipt.

## Explicit deferrals

Phase 16 does not publish these existing Channel routes:

- `/channel/info`: no field validation and full replacement rather than patch;
- `/channel/delete`: weak key validation and terminal disband semantics;
- `/channel/subscriber_remove`: type-zero behavior differs from subscriber add;
- `/channel/blacklist_set` and `/channel/whitelist_set`: weak replacement
  validation;
- `GET /channel/whitelist`: unvalidated query and unbounded full-list response.

It also defers legacy `POST /conversation/sync`, whose delimited input, bare
array response, old message projection, and lack of completion indication need
a separate compatibility contract.

The legacy `resources/api/openapi.json` is inventory only. It must not supply
schemas, status claims, or production server URLs for Phase 16.

## Contract separation

`contracts/javascript-web-quickstart.openapi.json` remains exactly the three
Phase 12 operations used by the loopback BFF and real Chromium smoke. Phase 16
corrects its shared restore-maintenance response schema without changing that
operation set, the sample flow, compatibility tuple, or receipt. The receipt
does not attest the Phase 16 management contract.

The new contract is downloadable at
`/contracts/product-http-management.openapi.json`, but it is explicitly
non-exhaustive. The planned complete OpenAPI specification page remains
unpublished.

## Verification

Phase 16 must verify:

- the exact 16-operation whitelist and explicit deferrals;
- bilingual, operation-per-page deterministic Fumadocs generation grouped by tag;
- locale overlays with no residual `x-i18n` fields;
- disabled playground and trusted-backend-only examples;
- request, response, nested Schema, error, and maintenance facts in HTML,
  search, per-page Markdown, and `llms-full.txt`;
- published navigation, sitemap, canonical, edit, and feedback behavior;
- the frozen three-operation golden-path whitelist and receipt boundary, plus
  the runtime-aligned restore-maintenance response correction;
- current Channel and Conversation entry/use-case tests.

## Non-goals

- A complete Product HTTP or complete v3 OpenAPI specification.
- Runtime fixes for the deferred routes.
- Message send, user lifecycle, route batch, operations HTTP, Bench, Debug, or
  Manager APIs.
- Modeling Webhooks, WKProto, TCP frames, or JSON-RPC as fake inbound HTTP
  paths.
- Production authentication, authorization, quotas, audit, or rate limits.
