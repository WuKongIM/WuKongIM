# Android EasySDK 1.1.0 release and documentation validation

## Exact release identity

- Maven coordinate: `com.githubim:easysdk-android:1.1.0`, resolved from
  [Maven Central](https://repo.maven.apache.org/maven2/com/githubim/easysdk-android/1.1.0/).
- SDK tag `v1.1.0` points to `2e4c29c1ca58c73793c3cb8269853cae9fbdc91e` on `main`,
  merged by [SDK PR #7](https://github.com/WuKongIM/WuKongEasySDK-Android/pull/7).
- [Release assets](https://github.com/WuKongIM/WuKongEasySDK-Android/releases/tag/v1.1.0)
  contain the signed packages, example APK, checksums and machine-readable receipts.
- [Publish run](https://github.com/WuKongIM/WuKongEasySDK-Android/actions/runs/39919132722)
  succeeded. Sonatype deployment: `61a04f6b-75b2-44cf-ab84-f286c3f27fd7`.
  Completion was confirmed by public Maven resolution, beyond upload acceptance.

## Public artifact verification

The [registry receipt](https://github.com/WuKongIM/WuKongEasySDK-Android/releases/download/v1.1.0/registry-verification.json)
records verified signatures for the AAR, POM, sources and Javadoc. Their signer,
`226F51D759B24DAE71B91780A8AF9E0BEB26E224`, matches the previous release.

| Artifact | SHA-256 |
| --- | --- |
| AAR | `afcbea39cc1cf751f510b83a8537ee3248c56936f5c3c7156fc235ac0f527040` |
| POM | `8fff944ac4f3b1dc35f99d34eab947ddbbad701c624b95437eb38c3d0414f461` |
| Sources | `f9cb905ab9c3305bdb3d510baad086f3b3abd1f53213ef5282b5ce4b7f50a250` |
| Javadoc | `87831e68126ebaa4a2627ea2c6615efe1b25fd90c0e47ef27c54e8ceb8b55248` |

The [Maven consumer receipt](https://github.com/WuKongIM/WuKongEasySDK-Android/releases/download/v1.1.0/maven-client-report.json)
confirms the example's local SDK project dependency was removed, 67 transitive
artifacts resolved, and the example compiled against that exact public AAR.
The release verification also passed 50 SDK/example debug and release test cases.

## Native streaming acceptance

The [published-package native receipt](https://github.com/WuKongIM/WuKongEasySDK-Android/releases/download/v1.1.0/published-native-acceptance.json)
records 11 passing checks on an Android API 34 emulator using the public AAR and
SDK source above. The test server was built from WuKongIM revision `a804f4a57`
and ran as a 256-slot single-node cluster. The build used JDK 17 and Gradle 8.4.

Coverage includes actual SDK SEND/EVENT with progressive UTF-8 text, cancellation
and failure snapshots, offline generation and history recovery, Activity
recreation, snapshot/live-event merging, OpenAI-compatible SSE, unexpected EOF,
upstream cancellation, model-key exclusion from transcript/logs, and multi-turn
context. The fixture observed three history reads, zero `/message/eventsync`
reads, no online history polling, and one upstream cancellation.

Reproduce from the exact SDK tag using `example/STREAMING.md` and
`example/tools/stream_acceptance.py`, with a compatible server binary and a
running API 34 emulator. Use a short output directory such as `/tmp/wk-stream`
for the server's Unix socket paths. The Release includes the tested APK and
screenshots. The model was a local compatible fixture; this does not certify
external model providers, all devices, or a new four-platform/fault/soak matrix.

## Documentation validation

Current public versions come from `docs-site/lib/easy-sdk-releases.json`.
The bilingual Android quickstart and new `/sdk/easy/android/streaming` pages use
its tokens. Historical Android 1.0.5 receipts remain unchanged.

The Kotlin blocks in both languages were extracted from all four MDX files.
The stream reader and producer functions compiled unchanged; quickstart blocks
were assembled by inserting `sendText` into `ChatActivity` as the text instructs.
Six Kotlin files compiled in a separate Android library against Maven 1.1.0,
with no local SDK project dependency. Resolution verified the AAR checksum above.
The environment used Kotlin 1.9.0, AGP 8.1.4, Gradle 8.4, JDK 17, compileSdk 34,
and minSdk 21. This establishes API compatibility of the literal examples;
the native runtime result is the separate published-package acceptance above.

`bun run verify` passes the documentation examples, 212 library tests,
navigation/OpenAPI generation checks, ESLint, type checks, static build and
static-output contract. The named `flow-doc-contracts` check passes. Local
compilation inputs and logs are retained under ignored
`tmp/android-easysdk-docs/`; release receipts remain public in the SDK Release.
