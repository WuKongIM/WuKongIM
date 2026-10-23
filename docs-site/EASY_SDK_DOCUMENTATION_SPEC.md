# WuKongEasySDK documentation specification

## Reader and outcome

An application developer familiar with their platform but new to WuKongIM can
prepare credentials, install one exact SDK version, connect two users, observe
both message directions, and release the connection. Explain Channel, Payload,
and send results before relying on them. The application supplies a trusted
backend and UI; EasySDK handles online messaging, not offline synchronization.

## Published structure

Keep the existing `/sdk/easy` routes and Chinese/English parity. The overview
helps choose a platform. `examples` prepares a development single-node cluster,
credentials with matching device categories, and eight runnable entry points.
Each platform quickstart follows prepare, install, connect/listen, exchange,
cleanup, and troubleshooting, in that order. Native streaming follows each
quickstart at `/sdk/easy/android/streaming`, `/sdk/easy/ios/streaming` and `/sdk/easy/flutter/streaming`, keeping advanced projection and
backend event responsibilities out of the first-message path.

Use one default installation path. Put alternative installations after these
tasks. Preserve each platform's real singleton, callback, timeout, and cleanup
semantics. Runnable official examples may complement application integration
snippets; identify application-supplied functions and where snippets belong.
Show expected output and wait for both peers before sending. Bound any display
buffers; they are not persistent history or durable deduplication.

## Tutorial versions

The sole current-version manifest is `lib/easy-sdk-releases.json`. It selects
one exact released version per platform, with independent source and vcpkg pins
where required. Public MDX uses `WK_EASYSDK_<PLATFORM>_VERSION`, `_TAG`, or
`_SOURCE_REF`; the build resolves prose, code, links and literal MDX attributes.
Frontmatter stays version-neutral; navigation reads the same manifest.

Update a selection only after checking the released package against its tutorial,
including installation, APIs, cleanup, platform requirements and known limits.
Compatible releases need only a manifest edit. C++ registry, tool, CMake and
source pins must be reviewed independently. This manifest does not attest that a
new package passed an older live matrix. The Web pilot discovers compatible npm
releases, verifies the literal tutorial in Chromium and proposes a human-reviewed
PR; other platforms remain manual. See the README for the maintenance workflow.

Check examples against their released package or pinned source. Source examples
are not package-consumer verification. Web 2.0.5 includes the handshake-failure
reconnect repair; keep its independent package validation separate from
historical Web 2.0.4 four-platform results. Android 1.1.0 adds independent peers,
stream SEND options and custom EVENT callbacks; its Maven artifact and native
API 34 streaming acceptance are distinct from the historical Android 1.0.5
four-platform matrix. iOS 1.2.0 adds typed stream SEND options and custom EVENT
callbacks; its SwiftPM/CocoaPods consumer and native streaming evidence is
separate from the historical iOS 1.1.1 matrix.

## Validation reference

The original client, server, harness, artifact checksums, environment, failed
observations and bounded outcomes are preserved in
`docs/superpowers/reports/2026-09-08-easysdk-validation-history.md`, with links to
immutable originals. Link that engineering reference from public pages rather
than embedding CI commands or retrospective reports in the first-message path.
Retain actionable version requirements and known recovery limitations in the
platform tutorial, including C# cluster recovery and Python membership fixes.
Installation pins needed by vcpkg or optional source builds remain explicit.

## Checks

`lib/easy-sdk-tutorial-contract.test.ts` checks paired learning order, platform
APIs, installs, cleanup, bounded examples, and provenance separation.
`lib/easy-sdk-cpp-contract.test.ts` checks JSON manifests, CMake wiring,
archive prerequisites, and callback constraints. Run the normal documentation
verification gate for navigation, links, lint, MDX/types, static export and
machine-readable outputs. Record compilation separately from a live message run.

Flutter 1.2.0 adds independent peers, typed stream settings, JSON-string EVENT decoding and a native streaming demo. Its exact pub.dev consumer, native iOS acceptance and Web build are distinct from historical Flutter 1.1.0 matrix results.

Flutter 1.2.1 repairs the acceptance runner's executable discovery; its SDK and Dart demo code are unchanged from 1.2.0. Current selection requires independent exact 1.2.1 public-package verification.
