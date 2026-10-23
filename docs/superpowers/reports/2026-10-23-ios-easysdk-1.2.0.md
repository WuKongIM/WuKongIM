# iOS EasySDK 1.2.0 release and documentation validation

## Exact source and release identity

- SDK tag `v1.2.0`: `717bc038a730958db8588c83e2c90dff00e8431f` on `main`.
- [SDK PR #8](https://github.com/WuKongIM/WuKongEasySDK-iOS/pull/8) merged
  `e44f0a6dea9af005ca37bc2480ddabb42211326e`; its tree exactly matches the tag.
- [PR CI](https://github.com/WuKongIM/WuKongEasySDK-iOS/actions/runs/39927856967)
  passed Swift tests and CocoaPods lint. The local gates passed 36 SDK tests,
  four projection tests, iOS Simulator builds, a macOS Release build and
  iOS/macOS CocoaPods lint. Local all-platform lint was limited by a missing
  tvOS simulator; the repository CI retains the full gate.
- [Publisher](https://github.com/WuKongIM/WuKongEasySDK-iOS/actions/runs/39928780922)
  validated the tagged source before reporting a Trunk API timeout. The
  [GitHub Release](https://github.com/WuKongIM/WuKongEasySDK-iOS/releases/tag/v1.2.0)
  was finalized separately after public package verification.

## Public package consumers

SwiftPM resolved exact version `1.2.0` from the public repository to the tag SHA
above. A separate copy of the full native example replaced its local SDK path
with the remote exact dependency and built successfully. Documentation consumer
projects compiled the six Swift files extracted from both languages' quickstart
and streaming pages, on macOS and iOS Simulator. Quickstart fragments were
assembled with their documented `IMBootstrap`; integration-only business API
calls were excluded. Streaming helpers compiled unchanged.

CocoaPods installed 1.2.0 through the immutable public
[Specs commit](https://github.com/CocoaPods/Specs/commit/7cb1794f0739ad347904761cc9711288a925899d),
with spec checksum `f08f2897b6e6e83ddf7ff825111ded30f1285562`. All seven installed
SDK Swift files match the exact SDK tag. The independent native app built and
passed all 11 checks with this dependency; the public SwiftPM app also passed
the same 11 checks. Release assets retain the
[CocoaPods result](https://github.com/WuKongIM/WuKongEasySDK-iOS/releases/download/v1.2.0/cocoapods-native-acceptance.json),
[SwiftPM result](https://github.com/WuKongIM/WuKongEasySDK-iOS/releases/download/v1.2.0/spm-native-acceptance.json),
[package-source comparison](https://github.com/WuKongIM/WuKongEasySDK-iOS/releases/download/v1.2.0/cocoapods-source-verification.json)
and [documentation compile receipt](https://github.com/WuKongIM/WuKongEasySDK-iOS/releases/download/v1.2.0/documentation-compile.json).

The publisher returned HTTP 504 while Trunk called the GitHub commit API.
Read-only verification confirmed the Specs commit, Trunk version and CDN
podspec; no duplicate push was attempted. A GitHub Release was finalized
separately after both public package consumers passed. The CDN version index
subsequently included 1.2.0. An ordinary exact-version Podfile resolved from
the official CDN in an isolated CocoaPods home, built the native app and passed
all 11 checks. The lockfile lists WuKongEasySDK under `trunk`, with no external
source or local SDK override.

This host's CocoaPods/libcurl produced HTTP/2 framing errors and timeouts.
The final CDN verification used a temporary system-curl HTTPS/1.1 metadata
transport adapter; TLS checks, official registry URLs, CocoaPods resolution
and package contents were unchanged. The Release retains
`cocoapods-https-transport.rb` and the consumer receipt for reproduction. This
is a validation-host workaround, not an SDK requirement or a global tool change.

## Native acceptance scope

The final clean candidate passed 11 process-level native checks on iPhone SE
(3rd generation), iOS 18.3.1, Xcode 16.2 / Apple Swift 6.0.3 (Swift 5.7 package
mode). The server was built from WuKongIM `a804f4a57` and ran a 256-slot
single-node cluster with stored-device-token authentication enabled.

Coverage includes actual SDK SEND/EVENT, progressive UTF-8 text, stable terminal
snapshots for cancellation/failure, offline generation and reconnect recovery,
history snapshots merged with buffered live deltas, compatible model SSE,
unexpected EOF and SSE errors, upstream cancellation, multi-turn context and
model-key exclusion from transcript/logs. The fixture recorded exactly three
history reads for three authenticated connections, zero `/message/eventsync`
reads, no online polling, and one upstream cancellation.

The model was a local compatible fixture. This is not a new four-platform,
physical-device, cluster-fault or load/soak certification, nor evidence for
external model providers. Historical iOS 1.1.1 matrix receipts remain unchanged.

Reproduce with the exact SDK tag's
`Examples/WuKongIMExample-Unified/STREAMING.md` and
`Examples/tools/stream_acceptance.py`, a compatible server binary, and an
already booted simulator. Use `--app` to run the same harness against an app
built with the published CocoaPods dependency. A short directory such as
`/tmp/wk-ios-stream` avoids the server's Unix socket path limit.

## Documentation gate

The public tutorial selection lives in `docs-site/lib/easy-sdk-releases.json`.
Chinese/English `/sdk/easy/ios/streaming` pages use its tokens, retain listener
ownership, explain persisted parents, backend authorization, UTF-8 byte offsets,
terminal snapshots and reconnect recovery. Historical package records stay
literal. Source compilation, package consumption and native acceptance are
recorded separately.

`bun run verify` and the named `flow-doc-contracts` check pass. The documentation
suite has 212 library tests and validates examples, navigation/OpenAPI output,
ESLint, types, static export and machine-readable output. Local compilation
inputs and logs are retained under ignored `tmp/ios-easysdk-docs/`.
