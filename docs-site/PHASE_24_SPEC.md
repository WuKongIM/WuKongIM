# Phase 24: Complete the planned documentation backlog

## Goal

Publish every remaining maintained route in the bilingual navigation registry
without converting documentation availability into a runtime, device,
production, or compatibility claim. The legacy documentation supplies useful
topic coverage and reader order, while current source, pinned distribution
artifacts, public declarations, and executable repository contracts remain the
technical authority.

Phase 24 publishes 15 routes per locale:

- `/server/deployment/kubernetes`;
- `/sdk/android/{platform-capabilities,api-reference,upgrade}`;
- `/sdk/ios/{platform-capabilities,api-reference,upgrade}`;
- `/sdk/flutter/{platform-capabilities,api-reference,upgrade}`;
- `/sdk/harmonyos/{platform-capabilities,api-reference,upgrade}`;
- `/sdk/javascript/{api-reference,upgrade}`.

After this phase, the maintained registry has no `planned` entry. Both locale
variants must exist before a route is indexed, and an MDX file may not remain
hidden behind a planned or unknown route.

## Evidence hierarchy

Claims resolve in this order:

1. current WuKongIM server source, configuration schema, and tests;
2. the exact pinned SDK distribution plus its matching official source;
3. executable examples and contract tests in this repository;
4. official platform documentation for Kubernetes behavior;
5. legacy WuKongIM documentation for learning order and topic discovery only.

The legacy pages are useful for the sequence “install, configure, connect,
send, receive, recover, upgrade” and for identifying channel, conversation,
provider, listener, background, and troubleshooting topics. They must not
reintroduce floating versions, nonexistent packages or charts, obsolete
method signatures, stale ports, single-process topology claims, insecure
client-side Product HTTP calls, or unsupported compatibility promises.

The source audit is recorded in
`docs/superpowers/reports/2026-08-30-remaining-planned-docs-source-audit.md`.

## Fixed SDK snapshots

| Platform | Distribution identity | Matching source identity | Runtime evidence boundary |
| --- | --- | --- | --- |
| Android | JitPack `com.github.WuKongIM:WuKongIMAndroidSDK:1.5.5`, AAR SHA-256 `5a797f1fac53c4fbcf015afca2686ecbeebd24b5e64dea598881b814b1322792` | `662a559a50d181540a0448454beb57e939b0c50e` | No site Android build, emulator, or device receipt |
| iOS | CocoaPods `WuKongIMSDK` `1.1.1` and distributed framework revision `0cbfb99f18010fe76b7e13ed31b5d1ad4664b10c`; framework podspec conflicts between iOS platform `11.0` and deployment target `13.0` | `89bf9a1b95ce374caabdd8031d69cc8844d825ae` | No site Xcode, simulator, or device receipt; the podspec conflict does not establish one minimum iOS version |
| Flutter | pub.dev `wukongimfluttersdk` `1.7.9`, archive SHA-256 `b6191a86cd1e4caacaa4652e95709310eb1493f159fee65e1dd53c2a3ff9e80a` | `de1024276523119e38305c49a3a873caae4d5c59` | Analyze/macOS build evidence is not an Android, iOS, or macOS runtime receipt |
| HarmonyOS | OHPM `@wukong/wkim` `1.1.7`, HAR SHA-256 `d98d1523bc60ad204dd74d9cfa776935a5547fc3ab352322dfa17f5dbc7a3cd8` | `0c41810a1e0a5fc2936929d63ca32a50ffb11bec` | No site OHPM install, DevEco compile, HAP, emulator, or device receipt |
| JavaScript/Web | npm `wukongimjssdk@1.3.5`, archive SHA-256 `b053c9623ac36b7ce78dfd874240ac48abaee48e20dd78d824f28881c5504cfc` | tag/revision `3c507ea3ebc08eae9d74fc1f76b150c380752008` | The receipt contract is eligible only for the pinned Chromium golden scenario; without an exact attestation the generated compatibility state remains `verified: false` / `verification.status: missing` |

Publishing API and upgrade chapters does not broaden these evidence classes.
The compatibility page remains the authority for browser-tuple eligibility and
the actual attestation state; documentation publication cannot turn a missing
attestation into a passing receipt. Other platforms require their own exact
toolchain/device/server attestations.

## Platform-capability chapters

Each platform-capability page must:

- distinguish source/artifact-aligned capability from scenario-covered or
  runtime-verified behavior;
- assign identity, Product HTTP, push, background execution, local storage,
  lifecycle, and UI state to the correct application or platform owner;
- retain known raw-transport, payload-logging, local-data, singleton/provider,
  listener-cleanup, queue-generation, and account-isolation blockers from the
  earlier phase specification;
- state precisely which build and runtime targets were not exercised;
- give an adoption checklist that creates new evidence instead of changing a
  label based on exported symbols.

## API-reference chapters

Each API reference must use the pinned public distribution surface. Organize
the reader-facing entry points, configuration, providers/data sources,
connection lifecycle, messaging, channel, conversation, content/model,
result-code, listener-registration, and teardown APIs. Do not present private
helpers as stable contracts or infer a capability from a server enum.

Examples must preserve the exact language and callback semantics of the pinned
artifact. A local send return, local database insertion, SENDACK, peer online
receipt, and offline recovery remain distinct results. Every removable
listener uses the same function/object identity during cleanup; singleton
platforms must not imply safe same-process account switching when the pinned
source retains providers, queues, callbacks, or local state.

## Upgrade chapters

Upgrade guides target the fixed snapshots above. They must require:

- an inventory of the current dependency, lockfile, SDK call sites, custom
  content, providers, local schemas, and acceptance baseline;
- an exact dependency rather than `latest`, caret, or floating source branch;
- distribution-integrity and matching-source review;
- migration of changed APIs only when the source diff proves the change;
- compile/static checks followed by real Alice/Bob lifecycle scenarios;
- inspection of final artifacts and logs for sensitive payload leakage;
- a canary, observation thresholds, stop conditions, and rollback of the
  dependency and lockfile as one unit;
- explicit handling of local database or protocol compatibility before an old
  binary or bundle is called a valid rollback.

Each guide must bind its migration claims to these exact source intervals:

| Platform | Required comparison | Minimum proved delta |
| --- | --- | --- |
| Android | [`1.5.4 → 1.5.5`](https://github.com/WuKongIM/WuKongIMAndroidSDK/compare/1.5.4...1.5.5) | Android 8/8.1 VPN capability failure gains the `NetworkInfo` fallback; public Java signatures do not change |
| iOS | [`1.1.0 → 1.1.1`](https://github.com/WuKongIM/WuKongIMiOSSDK/compare/1.1.0...1.1.1) | `filterNoCMDAndNoStreamMessages` stops filtering `isDeleted != 0`; public headers do not change |
| Flutter | [`d99990f41ecb31166af82b9d20c121f33ff8385d → de1024276523119e38305c49a3a873caae4d5c59`](https://github.com/WuKongIM/WuKongIMFlutterSDK/compare/d99990f41ecb31166af82b9d20c121f33ff8385d...de1024276523119e38305c49a3a873caae4d5c59) | async sender/member lookup, awaited reaction persistence, maximum reaction sequence, and populated conversation results |
| HarmonyOS | [`a79df83f2794c581096850f0f77d34b95566a9ae → 0c41810a1e0a5fc2936929d63ca32a50ffb11bec`](https://github.com/WuKongIM/WuKongIMHarmonyOSSDK/compare/a79df83f2794c581096850f0f77d34b95566a9ae...0c41810a1e0a5fc2936929d63ca32a50ffb11bec) | new channel/message/conversation queries, connection-generation changes, failed-sending initialization, and extra/reaction persistence |
| JavaScript direct | [`533a60cdd1b9229fc4a87d7d22b5b860eb4aa43c → 3c507ea3ebc08eae9d74fc1f76b150c380752008`](https://github.com/WuKongIM/WuKongIMJSSDK/compare/533a60cdd1b9229fc4a87d7d22b5b860eb4aa43c...3c507ea3ebc08eae9d74fc1f76b150c380752008) | `WKEvent.dataText → dataJson` with JSON parsing |
| JavaScript wide | [`3747f4477829cf87d9003725038506aa5591b1ab → 3c507ea3ebc08eae9d74fc1f76b150c380752008`](https://github.com/WuKongIM/WuKongIMJSSDK/compare/3747f4477829cf87d9003725038506aa5591b1ab...3c507ea3ebc08eae9d74fc1f76b150c380752008) | protocol-version, stream-removal, event-manager, and build-version changes in addition to the direct delta |

Generic deprecation cleanup belongs in a call-site audit, not in the proved
version-delta table. A missing release note does not override an exact source
comparison.

For JavaScript, the `1.3.0 → 1.3.5` source comparison must call out the default
`protoVersion` change from `4` to `5`, removal of `streamManager` and stream
fields, addition of `eventManager`, and `WKEvent.dataText → dataJson`. It must
also retain the unconditional plaintext Payload logging boundary in the
unmodified `1.3.5` source.

## Kubernetes Beta contract

The Kubernetes page is a source-aligned reference architecture, not an
official chart or production manifest. It must:

- treat one Pod as a single-node cluster and preserve full cluster semantics;
- build an immutable image from reviewed source and deploy by digest;
- use a StatefulSet, Headless Service, stable DNS, deterministic unique node
  IDs, the same complete static member list, and an independent PVC per node;
- keep `hash_slot_count = 256` and make replica counts fit the member count and
  real failure domains;
- use `/healthz` for process liveness/startup and `/readyz` for traffic
  admission;
- keep node transport, Manager, metrics, diagnostics, and client entry points
  in separate network boundaries;
- disable Kubernetes service-link injection or otherwise prove that no unknown
  `WK_*` environment variable reaches the fail-closed configuration loader;
- explain that PDBs cover only some voluntary evictions, topology spread does
  not prove replica health, and scaling a StatefulSet replica count is not a
  WuKongIM membership operation;
- use a controlled node-by-node upgrade and retain backup, data-compatibility,
  stop, and rollback evidence.

The page may link official Kubernetes StatefulSet, probe, disruption, and
topology-spread documentation. It must not copy the legacy Helm repository,
chart version, `latest` image, stale health path, or direct PVC deletion and tar
commands as a production procedure.

## Shared publication integration

- Change all 15 registry entries to `published` only with both MDX variants in
  place.
- Regenerate `NAVIGATION.md` from `lib/navigation.ts`.
- Update SDK/platform/deployment landing pages, chooser, compatibility wording,
  README, phase-history notes, and stable project knowledge so none describes
  a newly published chapter as planned.
- Preserve Beta and runtime-evidence warnings after publication.
- Keep public search, Sitemap, and LLM indexes derived from the shared registry.
- Retain a zero-planned assertion and add the reverse invariant: every tracked
  MDX page must resolve to a published navigation entry. This prevents a page
  from existing on disk while still showing a planning badge or disappearing
  from public output.

## Validation

Focused content contracts must cover the fixed SDK identity, public entry
points, evidence boundary, listener cleanup, upgrade gates, and Kubernetes
stateful deployment invariants. The phase is complete only when:

1. all focused contracts pass;
2. navigation reports zero planned maintained routes and no missing or hidden
   bilingual MDX;
3. generated navigation and OpenAPI outputs are clean;
4. lint, MDX/TypeScript checks, static export, localized search, Sitemap, and
   LLM output tests pass;
5. independent Standards and Spec reviews have no unresolved findings.
