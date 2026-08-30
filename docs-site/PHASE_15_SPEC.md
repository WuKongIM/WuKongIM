# WuKongIM v3 Documentation — Phase 15 Specification

## Goal

Publish a reader-first WuKongEasySDK path for iOS, Android, Flutter, and Web.
The legacy tutorials supply the learning sequence, while current official tags,
package registries, and public source define the API facts. The result helps an
integrator reach an Alice/Bob messaging proof without presenting source review
as runtime compatibility evidence.

The four platform labels retain the familiar five-minute tutorial shape. That
label describes a short happy-path walkthrough, not an installation-time or
production-readiness guarantee.

Every overview and platform page links its exact locale-matching legacy source
so the learning sequence is traceable. Legacy pages never override the pinned
release source, distribution metadata, current server contract, or evidence
boundary.

## Audience and completion outcome

The primary reader owns an existing application and trusted product backend.
After this phase, that reader can:

1. decide whether EasySDK's thin JSON-RPC model fits the application;
2. install an exact released package for one of the four platforms;
3. obtain UID, token, and WebSocket routing material from a trusted backend;
4. initialize, register listeners, connect, send, receive, and clean up with
   the public API of the pinned source snapshot;
5. validate the integration with separate Alice and Bob identities; and
6. identify platform-specific blockers that must be closed before adoption.

## Published routes

Phase 15 publishes matching Chinese and English MDX for these routes:

- `/sdk/easy`
- `/sdk/easy/ios/getting-started`
- `/sdk/easy/android/getting-started`
- `/sdk/easy/flutter/getting-started`
- `/sdk/easy/javascript/getting-started`

The SDK landing page and chooser link these routes. The existing full
WuKongIMJSSDK golden path remains separate. Full WuKongIMSDK tutorials for
iOS, Android, Flutter, HarmonyOS, and other runtimes remain planned.

Phase 18 subsequently moved this group back to planned publication because the
current Product Gateway does not implement the EasySDK JSON-RPC CONNECT path.
The files remain maintained source-aligned tutorials, but they must stay out of
public indexes until runtime support and executable acceptance exist.

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

The tutorials are source-aligned publication, not executable compatibility
receipts. They prove that named APIs and examples exist in the recorded source
snapshots. They do not prove that each package has completed this repository's
real server/browser acceptance scenario.

Only the existing `wukongimjssdk@1.3.5` JavaScript/Web golden path may claim the
site's executable compatibility evidence. EasySDK pages must link readers to
their own Alice/Bob acceptance and must not alter `compatibility.json`, the
golden-path receipt schema, or Phase 14 capability statuses.

## Shared tutorial flow

Each platform page must cover the same integration loop in native idiom:

1. prerequisites and exact installation;
2. trusted-backend ownership of UID, token, and WebSocket URL;
3. singleton or instance initialization constraints;
4. connection and message listener registration before connecting;
5. bounded connection handling;
6. persistent person-Channel sending;
7. listener removal, disconnect, and lifecycle cleanup;
8. an Alice/Bob acceptance checklist; and
9. a compact native-framework lifecycle handoff that shows who owns and cleans
   up the client without becoming a complete UI architecture; and
10. platform-specific troubleshooting and adoption gates.

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
`2`, while its object-shaped SEND/dictionary RECV payloads still do not match
the current Base64-byte contract. An unmodified tag therefore still cannot
pass the current v3 messaging loop.

### Android

The `v1.0.3` client contains underscore request/response names and
object-shaped SEND payloads that do not match the current camelCase and
Base64-byte Gateway JSON-RPC contracts. Its device values are now aligned as
APP `0`, WEB `1`, and PC `2`. The tutorial marks the remaining shapes as
adoption blockers and requires a fixed build plus a real Alice/Bob proof
against the exact target server. Unknown-message and parse-error paths also log
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
blocker. RECV exposes the server's Base64 payload string, so the tutorial must
decode it explicitly before message-type dispatch.

### Web

The `v2.0.2` source logs JSON-RPC request and response details. Because those
details can include tokens or message payloads, production adoption is blocked
until the integrator suppresses or sanitizes that logging in a reviewed build.
The page also preserves one SDK instance per identity/context and browser BFF
boundaries.

## Validation

The fast gate must cover:

- exact bilingual publication and navigation order for all five routes;
- exact locale-matching legacy overview and platform source links;
- exact source tag, revision, and package pin per platform;
- absence of floating install versions;
- explicit source-aligned-versus-runtime-evidence language;
- trusted-backend identity and Alice/Bob acceptance;
- listener removal, disconnect, and lifecycle cleanup;
- SwiftUI, Android Fragment, Flutter Provider, and React lifecycle handoffs;
- bounded connection waits with cleanup after timeout;
- the aligned device values and remaining iOS/Android payload contracts,
  Android JSON-field contract, iOS availability, Flutter receive
  decoding/lifecycle, and all four
  platforms' sensitive-logging boundaries;
- continued separation from the full JavaScript golden path and planned
  WuKongIMSDK platform groups; and
- navigation generation, lint, typecheck, static export, internal links,
  search, SEO, sitemap, accessibility structure, and LLM output.

## Excluded

- Changing any SDK or server implementation.
- Claiming a production-ready or universally compatible server/SDK tuple.
- Issuing a golden-path or local acceptance receipt for EasySDK.
- Publishing a complete API reference, migration guide, push guide, or
  platform UI architecture. Compact owner/cleanup adapters inside the
  getting-started pages remain in scope.
- Moving Product HTTP management calls into an untrusted client.
- Publishing the planned full WuKongIMSDK platform tutorials.
