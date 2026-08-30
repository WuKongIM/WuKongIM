# WuKongIM v3 Documentation — Phase 15 Specification

## Goal

Publish a reader-first WuKongEasySDK path for iOS, Android, Flutter, and Web.
The legacy tutorials supply the learning sequence, while current official tags,
package registries, and public source define the API facts. The result gives an
integrator a pinned-source API walkthrough and a then-post-fix Alice/Bob
acceptance plan without presenting the path as executable at the Phase 15
revision or treating source review as runtime compatibility evidence.

The four platform labels retain the familiar five-minute tutorial shape. That
label describes a short source-reading path, not an executable quickstart,
installation-time promise, or production-readiness guarantee.

## Superseding current state

A later server implementation supersedes Phase 15's runtime limitation.
Product Gateway now supports CONNECT-first authentication, request correlation,
Ping, online SEND/SENDACK, RECV/RECVACK, and reconnect for the four pinned
EasySDK wire profiles. Codec fixtures cover iOS, Android, Flutter, and Web; a
real `cmd/wukongim` 256-slot single-node cluster E2E runs the iOS and Android
profiles through an Alice/Bob bidirectional loop. This remains server-side wire
evidence: no EasySDK platform artifact is compiled or executed, and offline
sync, push, subscriptions, batches, WSS/proxy deployment, sensitive logging,
platform lifecycle, and production token verification stay outside the receipt.

## Audience and completion outcome

The primary reader owns an existing application and trusted product backend.
After this phase, that reader can:

1. decide whether EasySDK's thin JSON-RPC model fits the application;
2. identify the exact released package for one of the four platforms;
3. model UID, token, and WebSocket routing material from a trusted backend;
4. follow how the pinned public API initializes, registers listeners, connects,
   sends, receives, and cleans up without claiming that it ran against the
   Phase 15 server revision;
5. prepare the then-post-fix validation with separate Alice and Bob identities; and
6. identify platform-specific blockers that must be closed before adoption.

## Published routes

Phase 15 publishes matching Chinese and English MDX for these routes:

- `/sdk/easy`
- `/sdk/easy/ios/getting-started`
- `/sdk/easy/android/getting-started`
- `/sdk/easy/flutter/getting-started`
- `/sdk/easy/javascript/getting-started`

The SDK landing page and chooser link these routes. The existing full
WuKongIMJSSDK golden path remains separate. At the Phase 15 boundary, full
WuKongIMSDK tutorials for iOS, Android, Flutter, HarmonyOS, and other runtimes
remained planned. Phases 19 through 24 later published the maintained full-SDK
platform paths and reference chapters without adding device/runtime receipts
or widening the JavaScript/Web executable-compatibility claim.

Phase 18 subsequently moved this group back to planned publication because the
Product Gateway at that revision did not support EasySDK JSON-RPC CONNECT as a
client integration path. After the bilingual content and source calibration
were completed, the five routes were republished as source-aligned tutorials.
The later implementation summarized above added the bounded server wire and
real-process receipt; it did not add platform-artifact or production-readiness
evidence.

## Source snapshots

Every platform tutorial identifies one exact released tag, source revision,
and package version:

| Platform | Repository tag | Source revision | Package |
| --- | --- | --- | --- |
| iOS | `v1.0.3` | `643848f85be70e3e3f2be22fceb86ae428b6cc38` | CocoaPods `WuKongEasySDK` `1.0.3` |
| Android | `v1.0.3` | `62084632cd8d1f26c751b053b0fb82d6aaa63892` | Maven `com.githubim:easysdk-android:1.0.3` |
| Flutter | `v1.0.4` | `6179251b49414401fe0eac4bfa3fec3f9b13a9fc` | pub.dev `wukong_easy_sdk` `1.0.4` |
| Web | `v2.0.2` | `c59c80551944c9e5d9b4a902ebd2629d3defb2e6` | npm `easyjssdk` `2.0.2` |

Install snippets pin these exact versions. They must not use `latest`, broad
version ranges, a default branch, or a legacy package version as evidence.

## Evidence boundary

At Phase 15 the tutorials were source-aligned publication, not executable
compatibility receipts. They proved that named APIs and examples existed in the
recorded source snapshots. The maintained pages now add the bounded server-side
receipt described above, but still do not prove execution of any released SDK
package on its platform.

Only the existing `wukongimjssdk@1.3.5` JavaScript/Web golden path may claim a
client-artifact/browser execution receipt. EasySDK pages may claim four wire
fixtures plus the iOS/Android-profile real-process E2E, but must not alter
`compatibility.json`, the golden-path receipt schema, or Phase 14 capability
statuses.

## Shared tutorial flow

Each platform page covers the same source walkthrough and the Phase 15
post-fix integration loop in native idiom:

1. prerequisites and exact installation;
2. trusted-backend ownership of UID, token, and WebSocket URL;
3. singleton or instance initialization constraints;
4. connection and message listener registration before connecting;
5. bounded connection handling;
6. persistent person-Channel sending;
7. listener removal, disconnect, and lifecycle cleanup;
8. an Alice/Bob acceptance checklist; and
9. platform-specific troubleshooting and adoption gates.

This sequence was not executable against the Product Gateway at Phase 15. The
maintained pages now point to the implemented JSON-RPC CONNECT core path and
clearly separate server wire/E2E evidence from platform execution and remaining
SDK adoption blockers.

The client never invents its own production UID or token. Browser clients do
not call Product HTTP management endpoints directly; a trusted backend or BFF
returns only the connection material the client needs.

## Platform boundaries

### iOS

The source package declares iOS 13 in `Package.swift`, while the public SDK
class is annotated for iOS 15. The tutorial therefore uses iOS 15 as the
conservative application deployment target until upstream reconciles the two.
It records the listener tokens and removes them during teardown. The tag's JSON
logger does not enforce its configuration guard and can expose payloads, so an
upstream upgrade or reviewed patch plus Release-log verification is a
production blocker. `v1.0.3` aligns device values as APP `0`, WEB `1`, and PC
`2`. At Phase 15 its object-shaped SEND/dictionary RECV payloads did not match
the server's Base64-byte contract. The current protocol boundary accepts object
SEND payloads and emits object RECV payloads for normal JSON messages; the
unmodified tag still requires iOS build/device, WSS, lifecycle, and logging
acceptance before production use. Its camelCase RECVACK includes both
`messageId` and `messageSeq`.

### Android

The `v1.0.3` client contains underscore request/response names, and its README
passes already-encoded JSON text as a `String` SEND payload rather than a JSON
object. Those shapes did not match the Phase 15 camelCase and Base64-byte
Gateway JSON-RPC contracts. The current protocol boundary accepts JSON text
strings, direct JSON objects, and Base64 payloads, and returns Android
snake_case aliases. Its device values are aligned as APP `0`, WEB `1`, and PC
`2`. Unknown-message and parse-error
paths still log
complete JSON-RPC messages or parameters without honoring `debugLogging`;
removing or redacting those paths and testing the built artifact is a
production blocker. The process-wide singleton cannot be silently
reinitialized for another UID or configuration.

### Flutter

The `v1.0.4` tutorial preserves the package's Dart 3 / Flutter 3 floor, listener-key
ownership, and widget lifecycle cleanup. Application code must not leave
connection or message callbacks attached after disposal. The tag logs complete
requests and responses without a public disable switch, so an upstream upgrade
or reviewed patch plus per-target Release-log verification is a production
blocker. The current server emits normal JSON message payloads as objects and
falls back to Base64 for non-object bytes; application parsing must handle the
pinned SDK's corresponding receive shape.

### Web

The `v2.0.2` source logs JSON-RPC request and response details. Because those
details can include tokens or message payloads, production adoption is blocked
until the integrator suppresses or sanitizes that logging in a reviewed build.
The page also preserves one SDK instance per identity/context and browser BFF
boundaries.

## Validation

The fast gate must cover:

- exact bilingual publication and navigation order for all five routes;
- exact source tag, revision, and package pin per platform;
- absence of floating install versions;
- explicit source-aligned-versus-runtime-evidence language;
- trusted-backend identity and the Phase 15 post-fix Alice/Bob acceptance plan;
- listener removal, disconnect, and lifecycle cleanup;
- bounded connection waits with cleanup after timeout;
- the aligned device values, the iOS two-field RECVACK, the Android README's
  JSON text-string SEND example, the current JSON text/object/Base64 compatibility
  mapping, iOS availability, Flutter receive
  decoding/lifecycle, and all four
  platforms' sensitive-logging boundaries;
- continued separation from the full JavaScript golden path and planned
  WuKongIMSDK platform groups; and
- navigation generation, lint, typecheck, static export, internal links,
  search, SEO, sitemap, accessibility structure, and LLM output.

## Phase 15 exclusions

- Changing any SDK or server implementation during Phase 15; later work added
  the bounded server implementation without modifying the pinned SDK releases.
- Claiming a production-ready or universally compatible server/SDK tuple.
- Issuing a client-artifact/platform receipt for EasySDK.
- Publishing a complete API reference, migration guide, push guide, or
  platform UI architecture.
- Moving Product HTTP management calls into an untrusted client.
- Publishing the planned full WuKongIMSDK platform tutorials.
