# JavaScript / Web quickstart laboratory

This is the executable source for the Phase 12 JavaScript / Web quickstart and
the Phase 14 local acceptance report. It
uses framework-independent TypeScript, a minimal HTML interface, a
localhost-only Node.js BFF, and exactly `wukongimjssdk@1.3.5`.

> Development boundary: this helper has no account system, authorization
> policy, business database, or durable token lifecycle. A real product must
> provide those capabilities. Do not expose this BFF or the WuKongIM Product
> HTTP listener directly to the public internet.

## Prerequisites

- Node.js `22.12.0` or another supported Node.js 20+ release, with npm.
- A ready WuKongIM single-node cluster whose Product HTTP listener is reachable
  from this Node.js process.
- A `/route` result whose `ws_addr` or `wss_addr` is reachable from Chromium.

## Run

```bash
npm ci
npm run dev
```

Open <http://127.0.0.1:5173>. Connect Alice and Bob, send one persistent text
message each way, disconnect Bob, send another message from Alice, and choose
**Reconnect + sync** in Bob's panel. The event log labels live delivery as
`realtime`, durable recovery as `recovered`, and protocol acknowledgement as
`SENDACK`.

The development server accepts these variables:

| Variable | Default | Purpose |
| --- | --- | --- |
| `WK_DOCS_QUICKSTART_HOST` | `127.0.0.1` | BFF/UI bind address. Only `127.0.0.1`, `localhost`, and `::1` are accepted. |
| `WK_DOCS_QUICKSTART_PORT` | `5173` | BFF/UI port. |
| `WK_DOCS_QUICKSTART_PRODUCT_HTTP_URL` | `http://127.0.0.1:5001` | Trusted WuKongIM Product HTTP base URL. This value is never sent to the browser. |

## Trust boundary

The browser calls only these same-origin BFF endpoints:

- `POST /api/development/identity`
- `POST /api/messages/sync`

The Node.js process maps those requests to the Beta snapshot's trusted Product
HTTP subset: `POST /user/token`, `GET /route`, and
`POST /channel/messagesync`. The BFF validates loopback Host and browser Origin,
bounds request bodies and sync pages, and keeps Product HTTP addresses out of
the client bundle. The browser receives only its development UID, ephemeral
development token, and discovered WebSocket address.

Before append submission for the first persistent ordinary person message, the
server durably admits a source-owned directory projection task. Both UID-owned
memberships materialize asynchronously and can trail SENDACK. Message recovery
retries only the exact membership-not-ready Product HTTP response, at 250ms
intervals for at most 20 attempts; unrelated failures are never retried.

The pinned SDK logs decoded packets and retry payloads unconditionally in its
published browser module. This sample's browser build therefore removes all
`console` calls; use the bounded, text-only event panel for diagnostics. Do not
remove that build guard or enable raw browser-console capture around real data.

The current default server composition stores `/user/token` metadata but does
not, by itself, guarantee CONNECT token validation. This sample demonstrates a
reproducible development path, not production authentication.

## Verify

```bash
npm test
npm run build
```

The real Chromium scenario is opt-in and expects a running WuKongIM cluster:

```bash
WK_DOCS_QUICKSTART_E2E_PRODUCT_HTTP_URL=http://127.0.0.1:5001 \
WK_DOCS_QUICKSTART_E2E_UI_URL=http://127.0.0.1:5173 \
npm run test:e2e
```

To run the fast checks and complete Chromium scenario as one fail-closed flow,
then write a bounded local evidence report:

```bash
WK_DOCS_QUICKSTART_E2E_PRODUCT_HTTP_URL=http://127.0.0.1:5001 \
WK_DOCS_QUICKSTART_E2E_UI_URL=http://127.0.0.1:5173 \
npm run verify:acceptance
```

The command removes any stale report before starting and writes
`test-results/integration-acceptance.json` only after both gates pass. The
report records the acceptance-harness revision, lock identity, the actual
installed SDK version after validating it against the shared target, and
runtime identity. It marks the tested cluster's source identity
`not_assessed`, because an arbitrary Product HTTP endpoint does not prove its
build revision. It records no configured endpoint, token, UID, message body,
DOM, screenshot, trace, video, or server response. Its schema is
`wukongim.docs.integration-acceptance/v1`; it deliberately reports production
readiness as `not_assessed` and publication attestation as `not_issued`. It is
not accepted in place of the protected golden-path publication receipt.

The sample accessibility baseline always runs. Documentation quality is a
separate report section: it remains `not_assessed` unless
`WK_DOCS_SITE_E2E_URL` points to a loopback static documentation server, in
which case the bilingual route matrix joins the E2E and can report `passed`.

`test:e2e` builds and starts the BFF/UI itself. It optionally accepts
`WK_DOCS_QUICKSTART_E2E_OUTPUT_DIR` for bounded failure screenshots. Automatic
Playwright screenshots, trace, and video capture remain disabled because the
identity response contains an ephemeral development token. On failure, the
suite first replaces event and error details, clears message inputs, removes
session frames, redacts development UIDs and sent message bodies, and asserts
that neither the token prefix nor development UIDs remain in the DOM. Only then
does it write a viewport PNG capped at 2 MiB; CI must not retain an unredacted
network trace or screenshot.

The functional scenario uses isolated development UIDs for the Alice and Bob
roles on every run, opens them in two independent Chromium contexts, and drives
connect, send, disconnect, and reconnect controls with `Tab` and `Enter`. It
also proves that an online message is not duplicated by sync and that the
offline message appears exactly once as `recovered`.
Separate checks run axe on the lab and both session pages, fail on serious or
critical findings, and check horizontal overflow at 1440×900 and 390×844. This
scenario also asserts the real computed `:focus-visible` indicator used by its
keyboard-operated controls. When `WK_DOCS_SITE_E2E_URL` points at the loopback
static documentation server, the same axe and overflow checks cover the
bilingual home, Quickstart, and Product HTTP pages at both viewports and require
at least one visible keyboard focus indicator on every page. This is a
repeatable accessibility baseline, not a conformance certification.

Stable smoke-test selectors are:

- top-level frames: `alice-frame`, `bob-frame`
- identity and connection: `identity`, `peer`, `connection-status`, `node-id`
- controls: `connect-button`, `disconnect-button`, `reconnect-sync-button`
- messaging: `message-input`, `send-button`
- events: `event-log`, `event-status`, `event-outgoing`, `event-sendack`,
  `event-received`, `event-synced`, `event-error`

## Documentation source anchors

The named `docs:start` / `docs:end` pairs provide stable links from the
documentation into executable code. The docs gate verifies that each anchor is
unique and paired, and that the corresponding MDX publication checkpoints are
present; it does not claim that independently copied MDX code is synchronized:

- `bff-provision-identity`, `bff-sync-messages`
- `product-http-token`, `product-http-route`, `product-http-message-sync`
- `browser-provision-identity`, `browser-sync-messages`
- `sdk-configure-and-connect`, `sdk-send-text`, `sdk-reconnect-sync`

This laboratory intentionally excludes production authentication, groups,
custom messages, conversation lists, push notifications, attachments, and
framework-specific state management.
