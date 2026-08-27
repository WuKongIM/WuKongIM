# WuKongIM v3 Documentation — Phase 14 Specification

## Goal

Turn the Phase 12 JavaScript/Web golden path and Phase 13 integration guidance
into an acceptance loop that an integrator can run, inspect, and hand to a
reviewer without overstating production readiness.

Phase 14 adds two distinct evidence layers:

1. an executable compatibility smoke proving the already-supported browser
   scenario against one reachable WuKongIM cluster; and
2. a fail-closed production-readiness checklist for identity, network,
   authorization, webhooks, capacity, operations, and rollback evidence that
   the development laboratory cannot prove.

The automated report must say that the compatibility smoke passed while
production readiness remains `not_assessed`. It is never a production
certificate and is not accepted as the protected publication attestation.

## Audience and completion outcome

The primary audience integrates an existing product backend and web client.
After this phase, that integrator can:

1. identify the exact JavaScript/Web capabilities currently backed by an
   executable test;
2. run one command against a reachable cluster and receive a bounded,
   redacted JSON report;
3. distinguish SENDACK, realtime receipt, offline absence, recovery, and
   deduplication evidence;
4. see which browser, SDK, and source identities produced the report;
5. stop a production release when connection authentication or another
   deployment-owned gate lacks evidence; and
6. prepare a review packet without copying tokens, UIDs, message bodies,
   Product HTTP addresses, WebSocket addresses, screenshots, or traces.

## Published routes

Phase 14 adds matching Chinese and English MDX for exactly these routes:

- `/guide/integration/acceptance`
- `/sdk/javascript/platform-capabilities`

The integration, SDK, and JavaScript indexes route readers into this material.
The complete JavaScript API reference and upgrade guide remain planned. Every
non-JavaScript platform remains planned and outside search, sitemap, LLM, and
compatibility claims.

## Evidence layers

### Automated compatibility smoke

`npm run verify:acceptance` in the existing JavaScript/Web laboratory must:

1. remove any stale acceptance report;
2. run the fast sample tests and typechecked build;
3. run the complete real Chromium scenario against the configured cluster;
4. write a report only after every command succeeds;
5. record no supplied endpoint, development token, UID, message body, DOM,
   screenshot, trace, video, console payload, or Product HTTP response;
6. write the report below the already-ignored `test-results/` directory; and
7. leave a failed run without a stale passing report.

The smoke continues to prove the existing scenario only:

- route discovery through the loopback BFF;
- CONNECT/CONNACK in two isolated Chromium contexts;
- fresh development UIDs and a new person Channel for every functional run;
- persistent person-Channel send in both directions;
- SENDACK separately from realtime receipt;
- absence of realtime receipt while one peer is disconnected;
- reconnect plus bounded Product HTTP message synchronization, including a
  fixed-window retry only for the exact asynchronous person-directory
  membership-not-ready response; and
- deduplication when realtime and synchronized observations overlap.

The accessibility checks remain laboratory and documentation quality evidence,
not JavaScript SDK capability claims.

### Production-readiness gates

The automated report must mark production readiness `not_assessed`. The
integrator guide requires separate evidence for:

- product account authentication and authoritative UID issuance;
- a trusted Gateway stored-token verifier, including rejection tests for
  invalid, expired, revoked, and cross-device credentials;
- TLS/WSS, controlled ingress, and private or authenticated Product HTTP;
- product authorization, membership, content policy, and abuse controls;
- bounded retries, capacity tests, backpressure, and rate limits;
- Webhook trust, durable admission, idempotency, retries, and reconciliation;
- metrics, alerts, backup/restore, incident diagnostics, and audit; and
- canary rollout, version pinning, rollback criteria, and rollback rehearsal.

The current default v3 Beta composition fails the stored-token verification
gate until the deployment wires a real verifier. Documentation must not turn a
successful `/user/token`, CONNECT, or compatibility smoke into contrary proof.

## Acceptance report contract

The local report schema is
`wukongim.docs.integration-acceptance/v1`. It has exact top-level sections for:

- generation time;
- source revision and clean/dirty state;
- sample lockfile, scenario, SDK, Node.js, Playwright, and Chromium identity;
- the fixed compatibility-smoke checks, all marked `passed`; and
- the fixed production gates, all marked `not_assessed`.

The report schema is intentionally different from
`wukongim.docs.golden-path-verification/v1`. Only the protected clean-HEAD
integration gate may issue the latter publication receipt. Renaming or feeding
the local report to the documentation build must not set
`compatibility.json.verified=true`.

The report builder validates bounded strings, a hexadecimal source revision,
a SHA-256 lock identity, an ISO timestamp, and the fixed check/gate vocabulary.
It rejects any serialized report containing URL schemes or the development
token prefix and caps output at 16 KiB.

## JavaScript/Web capability publication

The platform-capability page renders from one shared catalog used by both the
human table and Markdown/LLM supplement. Status vocabulary is:

- `verified`: exercised by the pinned real Chromium scenario;
- `boundary`: a current product or security limit the integrator must retain;
- `unverified`: enum/API presence or generic SDK guidance without executable
  evidence in this snapshot.

Verified entries are limited to route/connection, persistent person messaging,
SENDACK and realtime separation, reconnect/offline synchronization, and
deduplication in Chromium. Production authentication is a boundary. Other
browsers, groups, custom messages, conversation APIs, push, multi-device
policy, transient `NoPersist`, background lifecycle, complete API surface, and
upgrade behavior remain unverified by this snapshot.

The page always links to the compatibility snapshot, Quickstart, and production
acceptance guide. It does not infer support from npm exports or server enum
presence.

## Machine-readable publication

Both routes enter locale-correct navigation, static parameters, search,
sitemap, `llms-full.txt`, and per-page Markdown. The capability page receives
its generated Markdown matrix from the same catalog as the React table.
Static-output checks assert representative `verified`, `boundary`, and
`unverified` facts plus the production `not_assessed` boundary.

## Validation

The fast gate must cover:

- exact bilingual publication of the two Phase 14 routes;
- continued planned status for non-JavaScript platforms, JavaScript API
  reference, and JavaScript upgrade guide;
- exact capability status and evidence vocabulary;
- the separate compatibility-smoke and production-readiness layers;
- the acceptance report's exact shape, size, redaction, stale-report removal,
  and fail-closed write ordering;
- isolated acceptance identities plus bounded first-use person-directory
  convergence without retrying unrelated Product HTTP failures;
- sample tests, typecheck, navigation, lint, static export, internal links,
  search, SEO, sitemap, accessibility structure, and LLM output.

Because Phase 14 changes the executable sample, its E2E harness, and published
JavaScript contract pages, the protected `docs-integration` check must run the
real 256-Hash-Slot single-node-cluster/Chromium scenario before completion.

## Excluded

- Implementing production authentication, authorization, TLS termination,
  rate limiting, Webhook durability, monitoring, or rollback automation.
- Declaring the default v3 Beta composition production-ready.
- Publishing complete JavaScript APIs or upgrade instructions.
- Publishing Android, iOS, Flutter, UniApp, or HarmonyOS support.
- Expanding the Product HTTP OpenAPI subset.
- Adding groups, custom payloads, conversations, push, multi-device,
  `NoPersist`, or background-runtime behavior to the executable scenario.
- Replacing the protected golden-path publication attestation.
