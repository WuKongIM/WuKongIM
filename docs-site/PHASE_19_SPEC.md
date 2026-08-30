# WuKongIM v3 Documentation — Phase 19 Specification

## Objective

Publish a reader-first integration path for the full WuKongIM iOS SDK using
the exact public API and installable artifact from one pinned release. Phase 19
publishes the iOS overview, installation, and quickstart only. It does not turn
source review into an executable compatibility receipt or a production-
readiness promise.

The legacy iOS documentation supplies the useful learning sequence—install,
configure identity, connect, send, receive, and clean up—but it is not an API
authority. Exact release source, distributed public headers, and package
metadata override legacy snippets whenever they disagree.

## Published routes

Phase 19 publishes bilingual content for:

- `/sdk/ios`
- `/sdk/ios/installation`
- `/sdk/ios/quickstart`

The following iOS routes remain planned and excluded from indexed output:

- `/sdk/ios/platform-capabilities`
- `/sdk/ios/api-reference`
- `/sdk/ios/upgrade`

Android, Flutter, UniApp, and HarmonyOS full-SDK tutorials remain planned in
this phase. EasySDK was also planned at this phase because its JSON-RPC CONNECT
path was unsupported by the Product Gateway; a later documentation-only change
published the source-aligned tutorials without claiming runtime compatibility.

This is the Phase 19 publication boundary. Phase 24 later publishes the three
iOS chapters above from the same pinned headers and source while retaining the
absence of an Xcode, simulator, or device receipt.

## Exact source and artifact snapshot

The tutorial is pinned to this auditable tuple:

| Component | Exact snapshot |
| --- | --- |
| CocoaPods package | `WuKongIMSDK` `1.1.1` |
| Source repository | `WuKongIM/WuKongIMiOSSDK`, tag `1.1.1`, revision `89bf9a1b95ce374caabdd8031d69cc8844d825ae` |
| Binary framework repository | `WuKongIM/WuKongIMiOSSDK-Framework`, tag `1.1.1`, revision `0cbfb99f18010fe76b7e13ed31b5d1ad4664b10c` |
| Deployment target | iOS `11.0` from the distributed podspec |

The source repository's tag-local podspec still declares `1.1.0`; it is not
the distribution authority for this release. CocoaPods trunk and the framework
repository identify the installable `1.1.1` artifact. The tutorial therefore
pins `pod 'WuKongIMSDK', '1.1.1'` and records both source and framework
revisions.

Two embedded version surfaces are also stale: the exact source implements
`WKSDK.sdkVersion` as `1.0.0`, and the distributed framework's
`CFBundleShortVersionString` is `1.0.0`. Neither can attest the installed
package version. The tutorial uses `Podfile.lock` plus immutable source and
framework revisions for provenance and records runtime version reporting as an
upstream observability gap.

The exact source and framework tags do not contain `Package.swift`. Phase 19
must not advertise Swift Package Manager installation. The Objective-C public
headers used by the tutorial are byte-identical between the source tag and the
distributed framework tag, which supports API alignment but does not prove
runtime behavior.

## Installation contract

The installation page must:

- use CocoaPods with the exact `1.1.1` pin and tell readers to open the
  generated `.xcworkspace`;
- declare iOS 11 as the package deployment target without projecting that fact
  into support for every current Xcode or device combination;
- record that the classic framework contains `arm64` device and `x86_64`
  simulator slices and excludes simulator `arm64`, so Apple Silicon simulator
  use requires an `x86_64`/Rosetta-compatible setup or an upstream XCFramework;
- explain that a text-only raw TCP quickstart does not require camera,
  microphone, photo-library, or global ATS exceptions; product features add
  their own permissions separately;
- keep Product HTTP credentials out of the application and pin all integration
  facts to the exact snapshot above.

## Quickstart contract

The quickstart uses the source-native Objective-C public API rather than
inventing a Swift wrapper. It must show this lifecycle in order:

1. A trusted product backend authenticates the application user and returns a
   bounded bootstrap containing `uid`, `token`, `host`, and `port`.
2. The application creates `WKOptions` and `WKConnectInfo`, sets
   `options.isDebug = NO`, assigns `WKSDK.shared.options`, and registers
   connection and chat delegates before connecting.
3. The application calls `connect`, enforces a 15-second application-level
   acceptance timeout, and handles `onConnectStatus:reasonCode:` instead of
   treating the void method call as success.
4. Alice sends `WKTextContent` through
   `sendMessage:channel:` and a `WKChannel personWithChannelID:` target. The
   immediately returned `WKMessage` is local/pending; send acknowledgement is
   observed through `onMessageUpdate:left:` and receipt through
   `onRecvMessages:left:`.
5. Alice and Bob independently prove connection, acknowledgement, and online
   receipt. Offline/conversation recovery remains outside this quickstart until
   its provider callbacks and an executable scenario are documented.
6. The owning object removes both delegates and disconnects on teardown. A
   deliberate account logout calls `logout`, which also clears connection
   identity and switches UID-scoped local storage on a subsequent login.

The tutorial must not use legacy or invented symbols such as
`WKSDK.shared.setup()`, `connectAddr`, `apiURL`, or `uploadURL`.

## Security and production-adoption boundaries

Exact `1.1.1` source connects with a raw `GCDAsyncSocket` TCP transport and has
no public TLS option or `startTLS` path. The tutorial must not claim native TLS.
Production adoption requires a controlled/private ingress or a reviewed SDK or
transport upgrade that provides authenticated TLS.

`WKOptions.isDebug` defaults to enabled and must be set to `NO`. That setting is
not sufficient to remove sensitive logging in `1.1.1`: the receive path has an
unconditional packet log, and the RECV packet description can include decoded
message payload. Production release remains blocked until an upstream or
reviewed patch removes or redacts that path and the Release artifact plus log
collection chain is verified.

The local SQLCipher database key is derived directly from the UID. UID scopes
the database file but is not a secret encryption key. Production adoption must
not describe this as standalone at-rest confidentiality; it requires an
explicit data-protection threat model and reviewed key management or SDK
changes in addition to platform file protection.

The product backend owns login, authorization, UID assignment, token creation,
route selection, expiry, rotation, and revocation. The client receives only the
minimum connection material; it never receives Product HTTP management
credentials.

## Evidence classification

Phase 19 keeps three states separate:

1. **Source availability:** the official source and framework repositories are
   reachable at the pinned tags.
2. **Tutorial publication:** the bilingual pages map the exact public headers
   and package metadata into a bounded integration path.
3. **Executable verification:** no iOS build, simulator/device message loop, or
   shared compatibility receipt is added by this phase.

The existing `/compatibility.json` receipt remains scoped to the JavaScript/Web
golden path. SDK index, chooser, and compatibility prose must say that iOS is
source/header aligned but not covered by that receipt.

## Verification

Phase 19 requires:

- a failing documentation contract before the published iOS seam is added;
- bilingual route and navigation parity;
- exact-snapshot, public-API, lifecycle, logging, TCP/TLS, and evidence-boundary
  assertions;
- deterministic navigation generation;
- the complete documentation verification gate and production static export;
- a two-axis Standards and Specification review before commit.

## Non-goals

- Publishing a complete iOS API reference, platform-capability matrix, upgrade
  guide, push guide, or offline/conversation synchronization implementation.
- Claiming Swift Package Manager, XCFramework, native TLS, complete log
  redaction, or Apple Silicon simulator support for the exact artifact.
- Adding iOS to the JavaScript/Web compatibility receipt.
- Changing WuKongIM server or SDK runtime behavior.
