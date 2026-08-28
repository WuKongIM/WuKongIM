# WuKongIM v3 Documentation — Phase 12 Specification

## Goal

Publish the first reproducible application-developer integration path for the
v3 Beta source line. A developer must be able to start a real WuKongIM
single-node cluster, run one framework-neutral TypeScript sample, connect Alice
and Bob with the pinned JavaScript SDK, exchange persistent messages in both
directions, disconnect Bob, and recover one committed offline message after
reconnect.

This is a deliberately narrow golden-path subset. “Published” means the stated
scenario and contracts are complete, bilingual, source-aligned, and tested. It
does not mean every SDK, browser, Product HTTP route, or protocol has become a
stable v3 public API.

## Primary audience and outcome

The primary audience is an application integration developer who owns a web
client and trusted product backend. The path must teach the developer to keep
four results separate:

1. one SDK Session completed CONNECT;
2. a persistent SEND received a successful SENDACK at the Channel commit
   boundary;
3. one online Session received realtime delivery;
4. a reconnecting client recovered a committed message from synchronization.

The docs must not imply that connection is business authorization, SENDACK is
device delivery, or realtime delivery is durable offline recovery.

## Published routes

Phase 12 adds matching Chinese and English MDX for exactly these 14 routes:

- `/sdk/compatibility`
- `/sdk/javascript`
- `/sdk/javascript/installation`
- `/sdk/javascript/quickstart`
- `/api/conventions`
- `/api/authentication`
- `/api/compatibility`
- `/api/product-http`
- `/api/product-http/users`
- `/api/product-http/messages`
- `/api/product-http/routing`
- `/api/product-http/errors`
- `/api/dictionaries`
- `/api/dictionaries/reason-codes`

The domain indexes `/sdk` and `/api` are updated to route readers into this
published subset. All other SDK platforms, common SDK guides, complete SDK API
reference, upgrade guides, Product HTTP domains, Operations HTTP, Webhooks,
client-protocol details, and specification-download pages remain planned.

Every published Phase 12 page follows a common executable-contract structure:

- goal and completion signal;
- compatibility target and receipt status;
- prerequisites;
- ordered procedure or method/path/trust/request/response contract;
- expected result;
- failure diagnosis;
- security and responsibility boundary;
- next step.

## Golden sample

The executable source of truth lives in
`docs-site/examples/javascript-web-quickstart/` and has these properties:

- framework-neutral browser TypeScript; React and Vue are not required;
- exact `wukongimjssdk@1.3.5`, never `latest` or a version range;
- Node.js `>=20.11` and npm with a committed lockfile;
- one loopback-only Node.js BFF and one minimal browser laboratory;
- no account system, business database, durable token store, analytics,
  tracking script, avatar, conversation list, attachment, push, group chat, or
  production UI framework;
- one command, `npm run dev`, starts the built browser page and BFF after
  `npm ci`;
- named source regions form unique, linkable anchors in the executable sample;
  CI validates the anchor pairs and the corresponding MDX publication
  checkpoints without maintaining a second copy of the source.

The lab is an observable protocol experiment, not a chat-product mockup. It
shows Alice and Bob, connection state, selected node when available, outgoing
messages, SENDACK, realtime receipt, synchronized recovery, and a bounded event
log. It exposes explicit disconnect and reconnect-and-sync actions. Keyboard
operation must complete the entire scenario.

## Trust architecture

The browser never calls WuKongIM Product HTTP directly:

```text
browser
  -> same-origin localhost BFF
       -> POST /user/token
       -> GET /route
       -> POST /channel/messagesync

browser + pinned JavaScript SDK
  -> configured WebSocket Gateway
       -> CONNECT / SEND / RECV / RECVACK
```

The BFF must:

- bind only to loopback;
- reject non-loopback Host values and cross-origin requests;
- validate bounded development UIDs;
- generate high-entropy temporary development tokens server-side;
- return only the current UID, development token, and selected WebSocket URL;
- set non-cacheable responses;
- keep Product HTTP addresses and management capabilities out of the browser;
- proxy only the three declared golden-path capabilities;
- remain stateless across restarts.

The BFF is a development helper, not a reference implementation for product
accounts, authorization, token lifecycle, or production storage.

## End-to-end acceptance scenario

The single blocking scenario is:

1. Start the documented WuKongIM single-node cluster and pass `/readyz`.
2. Start the golden sample with one command.
3. Create Alice and Bob development identities in two isolated browser
   contexts and connect both.
4. Send one persistent Alice-to-Bob message and one persistent Bob-to-Alice
   message. For each direction, observe a successful SENDACK separately from
   realtime receipt.
5. Disconnect Bob.
6. Send one persistent Alice-to-Bob message while Bob is disconnected. Alice
   must receive a successful SENDACK and Bob must not record a realtime receipt.
7. Reconnect Bob and synchronize. Bob must recover that committed message as a
   `synced` event whose text contains `recovered`, not as `realtime` delivery.
8. Prove that deduplication tolerates overlap between realtime delivery and
   synchronization.

The test uses the real server process and real pinned SDK. Mock-only tests are
necessary for fast contract checks but cannot satisfy this acceptance scenario.

## Compatibility source of truth

Phase 12 is a reproducible `v3 Beta snapshot`, not a final v3 release. The
shared compatibility source must record:

- server source revision, injected at build time rather than hand-copied into
  prose;
- exact JavaScript SDK version;
- sample Node.js requirement and package-lock identity;
- the Playwright Chromium version used by the real smoke test;
- verification status and the golden scenario identity.

The same facts render through `CompatibilitySnapshot` on human pages and are
published as `/compatibility.json` for the sample, CI, and external tooling.
Changing any member creates a new unverified combination until the complete
scenario passes.

Verification is fail-closed. A normal build has `verified: false` and
`verification.status: "missing"`. A publishing build accepts only a bounded
JSON receipt file through `WK_DOCS_GOLDEN_PATH_ATTESTATION_PATH`; it has no
boolean verification override. The receipt uses the exact-key schema
`wukongim.docs.golden-path-verification/v1`, records `result: "passed"`, and
binds the source revision, sample lock SHA-256, scenario, SDK package and
version, Node.js version, Playwright package and version, Chromium engine,
revision, and browser version. Only an exact tuple match may set
`verified: true`. Missing, unreadable, malformed, oversized, extra-field, or
drifted receipts remain unverified.

The named Chromium snapshot is the only browser target eligible for a Phase 12
verification receipt. Firefox and WebKit/Safari are explicitly unverified. The
docs must not claim support for “all modern browsers.”

## Product HTTP contract subset

A slice-level OpenAPI contract is the machine-readable source for exactly:

| Method | Path | Golden-path purpose |
| --- | --- | --- |
| `POST` | `/user/token` | Create missing UID metadata and upsert development device-token metadata |
| `GET` | `/route` | Obtain configured TCP/WebSocket ingress addresses |
| `POST` | `/channel/messagesync` | Read committed person-Channel messages after reconnect |

The shared contract renders through `GoldenPathContract` and the generated
Fumadocs OpenAPI operation pages, supplies BFF types or validation, and is
checked against the runnable sample. Fumadocs groups the three independent
operation pages by tag and generates a concise card index for each tag. The
operation pages render request, response, and Schema content directly from the
localized contract. The
static request playground stays disabled because Product HTTP belongs behind a
trusted BFF. The contract is visibly labeled “Beta golden-path subset” and must
never be offered as the complete v3 OpenAPI.

Important semantics are frozen as follows:

- `POST /user/token` is a trusted metadata mutation. Success does not prove
  that a later CONNECT validated the token.
- The default v3 Beta app composition does not enable stored-token validation
  in the Gateway Authenticator. This gap must appear prominently in SDK, API,
  and Quickstart pages.
- `GET /route` currently returns configured ingress addresses. The sample does
  not put a UID in the query string; route success is neither business
  authorization nor proof of one Session's owner node.
- The BFF prefers `wss_addr`, falling back to `ws_addr`; a browser does not use
  `tcp_addr` in this sample.
- `POST /channel/messagesync` uses `login_uid`, peer UID as `channel_id`,
  `channel_type=1`, inclusive start sequence, exclusive end sequence, bounded
  limit, and pull mode. Reads contain committed data and respect membership
  visibility.
- The sample BFF exposes only a `1–100` sync limit even though the compatible
  runtime has a broader capped range.
- JavaScript retains `message_idstr` rather than relying on a potentially
  unsafe large JSON number.
- An empty sync page is valid. Online and sync results may overlap and require
  deduplication.
- Realtime send uses the JavaScript SDK and Gateway. `/message/send` is not part
  of this HTTP subset.

Compatible HTTP routes have endpoint-specific success and error envelopes.
Pages must tell developers to inspect HTTP status first and then parse the
endpoint shape; they must not invent one global `{data,error}` wrapper.

## Reason Code source of truth

The Reason Code page covers the complete current
`pkg/protocol/frame.ReasonCode` range 0–29. `ReasonCodeTable` is generated from
the Go name/value authority plus shared metadata for:

- applicable protocol phase;
- safe default retry classification;
- current reachability or conditionality;
- concise developer meaning.

CI must fail when the enum changes without regenerating and reviewing that
metadata. Presence in the enum must not be described as proof that every
current path emits the value. Unknown values fail closed and preserve their raw
number.

## Bilingual and machine-readable publishing

Chinese and English share technical facts but maintain separately written
narrative. Commands, executable source anchors, endpoint coverage,
compatibility values, and Reason Code rows have one generated or validated
source. MDX prose links to the executable anchors instead of claiming copied
snippets are byte-for-byte synchronized.

Every published Phase 12 route enters:

- locale-correct navigation;
- search;
- sitemap and canonical metadata;
- per-page Markdown;
- `llms-full.txt`.

Quickstart steps, developer-facing snippets, endpoint contracts, Reason Codes,
and compatibility facts belong in Markdown/LLM outputs. Playwright
implementation, internal build configuration, and diagnostic harness source are
linked rather than expanded in full.

Every new page provides an “Edit this page” link and a prefilled “Report a
documentation issue” link carrying route, locale, and compatibility snapshot.
Phase 12 adds no cookies, analytics provider, tracking pixel, or behavioral
telemetry.

## Site integration

The Phase 12 change also fixes site behavior that would otherwise invalidate
the developer path:

- the home page no longer reports the Phase 1 state;
- the application-developer primary action enters
  `/{lang}/sdk/javascript/quickstart`;
- previous/next navigation skips planned routes;
- published Phase 12 pages receive locale-correct feedback links;
- site package metadata, lockfile, README, and CI agree on Bun `1.3.11`;
- `https://docs.githubim.com` remains the default production canonical but is
  configurable for builds;
- preview builds keep production canonicals and may be globally `noindex`;
- trailing-slash and source-based last-modified behavior stay consistent.

Phase 12 keeps the current unversioned URL structure. It does not migrate all
pages under `/v3/` or build a multi-version documentation system.

## Validation

### Fast site gate

Every `docs-site` change runs:

- navigation and bilingual publication contracts;
- unique executable source-anchor pairing and MDX publication checkpoints;
- slice-level OpenAPI and BFF contract alignment;
- deterministic bilingual, operation-per-page Fumadocs OpenAPI generation and disabled
  playground boundaries;
- complete Reason Code 0–29 alignment;
- compatibility-manifest generation and render checks;
- default unverified output plus exact-receipt verified-output checks;
- golden-sample unit tests, typecheck, and build;
- site lint, typecheck, static export, link, search, SEO, sitemap, and LLM-output
  checks;
- published inclusion and planned exclusion assertions;
- static accessibility-structure and landmark checks.

### Real integration gate

The real single-node cluster + BFF + browser scenario is selected only when the
golden sample, published SDK/API content, shared contract facts, shared
developer-page presentation sources, or their narrow runtime source
dependencies change. Selection includes the relevant token, route, sync,
Reason Code, Gateway protocol/authentication, and SDK-version sources rather
than every documentation or Go change.

It is a protected docs integration named check, separate from the default Go
unit tier and the repository-wide E2E suite. It reruns the source-alignment
contracts before the exact acceptance scenario in Playwright Chromium. Each
selected run also checks the bilingual home entry, JavaScript Quickstart, and
Product HTTP overview, users, messages, routing, and errors pages at desktop
and mobile viewports for horizontal overflow, serious accessibility findings,
and visible keyboard focus (14 documentation URLs in total).

### Accessibility

The home-page entry, Quickstart, Product HTTP pages, and golden sample must:

- have no serious or critical automated accessibility findings;
- complete the sample flow by keyboard;
- preserve visible focus and programmatic control names;
- avoid horizontal overflow at tested desktop and mobile viewports.

This is a verified baseline, not a claim of full WCAG 2.2 AA certification.

### Failure evidence

A failing Playwright run may retain at most three capped PNG failure
screenshots under the repository-ignored `tmp/docs-site-e2e/` directory.
Successful runs remove only their unique run directory. The Go harness bounds
browser subprocess output in memory but publishes only its byte counts, never
the raw browser, BFF, or server log tail.
Playwright automatic screenshots are disabled. Before the E2E suite creates its
one viewport PNG for that failed test, it replaces the event/error log, clears
inputs, removes session frames, redacts the development UIDs and sent message
bodies, and asserts that neither a development-token prefix nor a development
UID remains in the DOM. Each retained PNG must be no larger than 2 MiB.
It does not retain a copied compatibility manifest, video, or network trace
because those channels can contain the development token, UID, or message
body. Native Playwright trace recording stays disabled because the identity
response cannot be selectively redacted before capture.

## Security invariants

- Browsers and mobile apps never call Product HTTP directly.
- CORS compatibility is not authentication.
- Product HTTP must be private or protected by authenticated service ingress.
- The default Gateway stored-token gap is always disclosed and never called
  production-ready.
- The sample BFF is loopback-only and development-only.
- Token, headers, and message body never enter ordinary logs, screenshots,
  metrics labels, or success artifacts.
- Version compatibility does not imply production security, capacity, TLS,
  disaster recovery, or tail-latency fitness.
- A single-node deployment remains a single-node cluster and never bypasses
  Controller, Slot, Channel, routing, or durability semantics.

## Excluded

- Production authentication implementation, runtime refactors, or Gateway
  stored-token wiring.
- Product account, authorization, database, friendship, moderation, tenant, or
  token-revocation systems.
- Group chat, custom messages, conversation list, read receipts, push,
  attachments, background browser behavior, and product visual design.
- Complete JavaScript SDK API reference or upgrade guide.
- Android, iOS, Flutter, UniApp, HarmonyOS, Firefox, or WebKit/Safari support
  promises.
- Full Product HTTP reference, `/message/send`, Operations HTTP, Webhooks,
  binary protocol, JSON-RPC details, complete OpenAPI, or downloadable complete
  protocol schemas.
- Versioned URL migration or multi-version documentation.
- Analytics, cookies, session replay, or third-party tracking.
- Hosting, DNS, redirects, CDN policy, production cutover, and external
  deployment mutations.
- Complete WCAG certification or production-capacity certification.
