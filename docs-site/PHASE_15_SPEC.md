# WuKongIM v3 Documentation — Phase 15 Specification

## Goal

Publish a reader-first WuKongEasySDK path for iOS, Android, Flutter, and Web.
The legacy tutorials supply the learning sequence, while current official tags,
package registries, and public source define the API facts. The result gives an
integrator a pinned-source API walkthrough and a then-post-fix Alice/Bob
acceptance plan without presenting the path as executable at the Phase 15
revision or treating source review as runtime compatibility evidence.

The maintained platform labels now use “Quickstart” instead of the earlier
five-minute wording. They promise a task-oriented path, not an installation-time
guarantee, a platform-execution receipt, or production readiness.

## Superseding current state

A later server implementation supersedes Phase 15's runtime limitation.
Product Gateway now supports CONNECT-first authentication, request correlation,
Ping, online SEND/SENDACK, RECV/RECVACK, and reconnect for the four pinned
EasySDK wire profiles. Codec fixtures cover iOS, Android, Flutter, and Web; a
real `cmd/wukongim` 256-slot single-node cluster E2E runs the iOS and Android
profiles through an Alice/Bob bidirectional loop. A later cross-repository run
then compiled, tested, and executed the official Web, Android, iOS, and Flutter
examples against WuKongIM
`5676700d2dc966fa6fc9b2f0620a6ae429adad5a`. Web ran in Chrome, Android on an
API 34 emulator, and iOS plus Flutter on iOS Simulator. The exact example
revisions are recorded below. Physical devices, WSS/proxy deployment, offline
sync, push, subscriptions, batches, capacity, and production token validation
remain outside that receipt.
Logging-security fixes were later merged and released in the four official SDK
distributions. Current maintained pins are iOS `1.1.1`, Android `1.0.5`,
Flutter `1.1.0`, and Web `2.0.4`. They make diagnostics default-off, restrict
enabled output to sanitized operational metadata, and redact public model
string output. On 2026-09-01, a separate acceptance resolved these exact
registry artifacts and completed online bidirectional messaging plus disconnect
against the same WuKongIM source. That proves bounded platform execution of the
released packages, but not production readiness.

## Audience and completion outcome

The primary reader owns an existing application and trusted product backend.
After this phase, that reader can:

1. decide whether EasySDK's thin JSON-RPC model fits the application;
2. identify the exact released package for one of the four platforms;
3. model UID, token, and WebSocket routing material from a trusted backend;
4. follow how the pinned public API initializes, registers listeners, connects,
   sends, receives, and cleans up while distinguishing repository-source and
   registry-artifact evidence;
5. prepare the maintained acceptance flow with separate Alice and Bob
   identities; and
6. identify platform-specific blockers that remain after the released-package
   runtime receipt without treating it as physical-device or production proof.

## Published routes

Phase 15 publishes matching Chinese and English MDX for these routes:

- `/sdk/easy`
- `/sdk/easy/examples`
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
were completed, the five tutorial routes were republished as source-aligned
tutorials. The later implementation added the bounded server wire and
real-process receipt; the cross-repository run then added a sixth example
runbook and exact repository-source execution evidence without widening it to
older release packages, physical devices, or production readiness.

## Source snapshots

The overview records one exact released tag, source revision, and package
version for every platform. Each platform quickstart keeps the exact install
pin, official distribution, and matching release visible without repeating the
full provenance before the task:

| Platform | Repository tag | Source revision | Package |
| --- | --- | --- | --- |
| iOS | `v1.1.1` | `ca688fcac2c4cd8d6f8e8163faf165376b520ba9` | CocoaPods `WuKongEasySDK` `1.1.1` |
| Android | `v1.0.5` | `61ae6dc6d0077b15e47cda1fd530296b97a06a7a` | Maven `com.githubim:easysdk-android:1.0.5` |
| Flutter | `v1.1.0` | `98ab8f3d9a1ad53f40c32caef0979845a37ae9a6` | pub.dev `wukong_easy_sdk` `1.1.0` |
| Web | `v2.0.4` | `9c03c98c725982fac224cd1d3b52456eae983975` | npm `easyjssdk` `2.0.4` |

Install snippets pin these exact versions. They must not use `latest`, broad
version ranges, a default branch, or a legacy package version as evidence.

## Verified repository examples

| Platform | Example revision | Relationship to the package snapshot |
| --- | --- | --- |
| iOS | `40014c16c0becd390c105098d359048901f4d87c` | Included in released `v1.1.1` |
| Android | `7134bbd0263fd01d9e7f71b7bd05b226f75b2292` | Included in released `v1.0.5` |
| Flutter | `98ab8f3d9a1ad53f40c32caef0979845a37ae9a6` | Exactly released `v1.1.0` |
| Web | `a055b3667247333b6b3183249f5d5929673dfd53` | Included in released `v2.0.4` |

On 2026-08-31 these exact sources passed their maintained unit/build gates and
completed online bidirectional messaging against the same current WuKongIM
revision. The Web run also covered manual disconnect and reconnect, Android
covered manual disconnect and heartbeat timeout, and the unified iOS example
rendered message content and timestamps. The runbook preserves source versus
package evidence even though every current release now includes its verified
source revision.

On 2026-09-01, npm `easyjssdk@2.0.4`, Maven
`com.githubim:easysdk-android:1.0.5`, CocoaPods `WuKongEasySDK 1.1.1`, and
pub.dev `wukong_easy_sdk 1.1.0` were resolved from their registries and run
against WuKongIM `5676700d2dc966fa6fc9b2f0620a6ae429adad5a`. Android used an
API 34 hosted emulator; iOS and Flutter used hosted iOS Simulators; the npm
package served as the peer in every hosted job and also passed separately in
Chrome 151. All four completed Alice/Bob online bidirectional messaging and
disconnect cleanup. GitHub Actions run `33466063708` is the retained hosted
receipt.

## Released logging fixes

| Platform | Fix pull request and merge revision | Fixed release | Release revision |
| --- | --- | --- | --- |
| iOS | `WuKongEasySDK-iOS#3` / `b7ec4440b940539bee213f95a3be74948f4b9fb8` | `v1.1.0` | `683c1519bfa19fd91a15ae092733e1efb1e75d5d` |
| Android | `WuKongEasySDK-Android#3` / `e984c7374a0e11f5d109ad3dbfdea599907735ff` | `v1.0.4` | `2ab2199a3eb91e6966c6a5d9b6098563e58e3203` |
| Flutter | `WuKongEasySDK-Flutter#3` / `d7758c301e5289ddfa09cd09b6976c2479584b1c` | `v1.1.0` | `98ab8f3d9a1ad53f40c32caef0979845a37ae9a6` |
| Web | `WuKongEasySDK-JS#6` / `3ebf505734c5b6764b30eac011f0b7a5024c89e8` | `v2.0.3` | `d29038e52aab5bce09f643fbe4daf11547379131` |

The maintained tutorials pin those fixed releases. Exact release revisions,
legacy calibration links, and earlier fix revisions are centralized in the
overview's evidence section instead of repeated before every quickstart.
Package and source tests verify inclusion of the default-off and redaction
controls; platform/log acceptance remains a separate consumer responsibility.

## Evidence boundary

At Phase 15 the tutorials were source-aligned publication, not executable
compatibility receipts. The maintained pages now add the bounded server-side
receipt, exact repository-example runs, and exact registry-package runs
described above. Source and package evidence remain separate, but current Web,
Android, and iOS patch releases now include the verified source revisions.
None of the runs alters `compatibility.json`, the full-SDK golden-path receipt
schema, or Phase 14 capability statuses.

## Shared tutorial flow

The overview first lets the reader choose a platform, decide whether EasySDK
fits, and understand the shared trusted-backend response. Each platform page
then covers the same post-fix integration loop in native idiom:

1. prerequisites and exact installation;
2. trusted-backend ownership of UID, token, and WebSocket URL;
3. singleton or instance initialization constraints;
4. connection and message listener registration before connecting;
5. bounded connection handling;
6. persistent person-Channel sending;
7. listener removal, disconnect, and lifecycle cleanup;
8. an Alice/Bob acceptance checklist;
9. symptom-oriented troubleshooting; and
10. a separate before-production checklist.

This sequence was not executable against the Product Gateway at Phase 15. The
maintained pages now point to the implemented JSON-RPC CONNECT core path and
clearly separate server wire/E2E evidence, exact repository-example execution,
released-package execution, and remaining production blockers. Full fix provenance
remains in the overview while each quickstart identifies its exact official
distribution and verified example revision.

The client never invents its own production UID or token. Browser clients do
not call Product HTTP management endpoints directly; a trusted backend or BFF
returns only the connection material the client needs.

## Platform boundaries

### iOS

The source package declares iOS 13 in `Package.swift`, while the public SDK
class is annotated for iOS 15. The tutorial therefore uses iOS 15 as the
conservative application deployment target until upstream reconciles the two.
It records the listener tokens and removes them during teardown. Release
`v1.1.1` retains the `enableDebugLogging` master gate, Builder support for
`enableJsonLogging(_:)`, and redacts diagnostics and public model strings;
diagnostics remain disabled unless the application opts in. It aligns device
values as APP `0`, WEB `1`, and PC `2`. At Phase 15 its object-shaped
SEND/dictionary RECV payloads did not match
the server's Base64-byte contract. The current protocol boundary accepts object
SEND payloads and emits object RECV payloads for normal JSON messages; the
unmodified release still requires iOS build/device, WSS, lifecycle, and logging
acceptance before production use. Its camelCase RECVACK includes both
`messageId` and `messageSeq`.

### Android

The `v1.0.5` client retains underscore request/response names and the same wire
shape as `v1.0.4` and `v1.0.3`: its README passes already-encoded JSON text as a `String`
SEND payload rather than a JSON object. Those shapes did not match the Phase 15
camelCase and Base64-byte
Gateway JSON-RPC contracts. The current protocol boundary accepts JSON text
strings, direct JSON objects, and Base64 payloads, and returns Android
snake_case aliases. Its device values are aligned as APP `0`, WEB `1`, and PC
`2`. Maven `1.0.5` includes the centralized default-silent logger and model
redaction from `e984c7374a0e11f5d109ad3dbfdea599907735ff`; enabled diagnostics
emit only sanitized operational metadata. The process-wide singleton still
cannot be silently reinitialized for another UID or configuration.

### Flutter

The `v1.1.0` tutorial preserves the package's Dart 3 / Flutter 3 floor,
listener-key ownership, and widget lifecycle cleanup. Application code must not
leave connection or message callbacks attached after disposal. This release
includes default-off `debugLogging`, an optional `logHandler`, and redacted
diagnostics/model strings from
`d7758c301e5289ddfa09cd09b6976c2479584b1c`. The current server emits normal
JSON message payloads as objects and falls back to Base64 for non-object bytes;
application parsing must handle the pinned SDK's corresponding receive shape.

### Web

The `v2.0.4` release includes default-off `debugLogging` from
`3ebf505734c5b6764b30eac011f0b7a5024c89e8` and routes all supported adapters
through a sanitized logger. Applications should leave diagnostics disabled in
production unless their own log review permits the sanitized operational
metadata. The page also preserves one SDK instance per identity/context and
browser BFF boundaries.

## Validation

The fast gate must cover:

- exact bilingual publication and navigation order for all six routes;
- task-first platform discovery and Quickstart labels without an unverified
  completion-time claim;
- exact source tag, revision, and package pin per platform;
- centralized source/fix provenance in the overview rather than repeated
  legacy history at the start of every platform page;
- absence of floating install versions;
- exact verified example revisions, runnable commands, explicit
  source-versus-release evidence boundaries, and the exact registry-package
  acceptance receipt;
- trusted-backend identity and the Phase 15 post-fix Alice/Bob acceptance plan;
- listener removal, disconnect, and lifecycle cleanup;
- bounded connection waits with cleanup after timeout;
- the aligned device values, the iOS two-field RECVACK, the Android README's
  JSON text-string SEND example, the current JSON text/object/Base64 compatibility
  mapping, iOS availability, Flutter receive
  decoding/lifecycle, all four platforms' sensitive-logging controls, exact
  logging-fix provenance, fixed release revisions and package inclusion, and
  application examples that never log raw models, payloads, credentials,
  identifiers, reasons, or error details;
- continued separation from the full JavaScript golden path and planned
  WuKongIMSDK platform groups; and
- navigation generation, lint, typecheck, static export, internal links,
  search, SEO, sitemap, accessibility structure, and LLM output.

## Phase 15 exclusions

- Changing any SDK or server implementation during Phase 15; later work added
  the bounded server implementation and released the logging-security fixes.
  The maintained pins record that superseding state without rewriting the
  original Phase 15 runtime-evidence boundary.
- Claiming a production-ready or universally compatible server/SDK tuple.
- Issuing a physical-device or production receipt from either the narrower
  repository-example runs or the later released-package simulator runs.
- Publishing a complete API reference, migration guide, push guide, or
  platform UI architecture.
- Moving Product HTTP management calls into an untrusted client.
- Publishing the planned full WuKongIMSDK platform tutorials.
