# Flutter EasySDK 1.2.1 release and documentation validation

## Exact identities

- `wukong_easy_sdk 1.2.1` was published through the repository's tag-triggered OIDC publisher: [run 39937570376](https://github.com/WuKongIM/WuKongEasySDK-Flutter/actions/runs/39937570376).
- Tag `v1.2.1` and clean `main`: `f9c4d971f900c40782a063f452184ea7e32aabef`.
- [Feature PR #5](https://github.com/WuKongIM/WuKongEasySDK-Flutter/pull/5) merged `abb54e8c11e97b7067b95a2abe600f6a2a3af247`; [portable-runner PR #6](https://github.com/WuKongIM/WuKongEasySDK-Flutter/pull/6) merged `ce6fc31b94357abb4f09c2da7100066cd8f179f4`.
- PR CI [39936671436](https://github.com/WuKongIM/WuKongEasySDK-Flutter/actions/runs/39936671436) and [39937326511](https://github.com/WuKongIM/WuKongEasySDK-Flutter/actions/runs/39937326511), plus main CI [39937562182](https://github.com/WuKongIM/WuKongEasySDK-Flutter/actions/runs/39937562182), passed analysis, formatting and SDK tests.
- Public [pub.dev archive](https://pub.dev/api/archives/wukong_easy_sdk-1.2.1.tar.gz) SHA-256: `881ac28e434860766e0bda63025d27b2415d15d4eb1a02a0a596a07062bf8bb7` (167793 bytes). All 32 SDK and Dart example source files match the tag byte for byte.
- Server binary: WuKongIM `a804f4a57`, the same committed implementation used for the Android/iOS streaming qualification; single-node cluster, 256 hash slots, eight initial physical slots and stored-token authentication enabled.

## Version and source boundaries

1.2.0 introduced independent `createInstance()` peers, typed `SendOptions` / `MessageSetting`, receive-side stream settings and JSON-string EVENT decoding. Both `reason` and `reasonCode` SENDACK fields are supported, with legacy success defaulting to 1. Existing singleton and individual `send` arguments remain supported.

1.2.1 fixes the acceptance runner's executable discovery: Flutter comes from PATH or `--flutter`, and the caller's environment is inherited. No tag was moved. SDK library, Dart demo and projection test sources are identical between 1.2.0 and 1.2.1. The source acceptance records identify their original exact revision; they are not relabeled as public-package runs.

## Public package consumers

An independent example copy replaced its path dependency with exact `wukong_easy_sdk: 1.2.1`. A fresh `/tmp/wk-flutter-pub-121-cache` resolved the official `https://pub.dev` hosted package; its lock-file digest matches the public archive above. No dependency override, Git dependency or metadata substitution was used. Analysis passed.

Six Dart files extracted from both languages' quickstart and streaming pages analyzed against the same exact public package. Quickstart fragments were assembled with their documented `IMBootstrap` and state methods. Literal analysis is separate from live messaging proof.

## Native process-level acceptance

The real Flutter screen controller and SDK ran on iPhone SE (3rd generation), iOS 18.3.1 Simulator, using Flutter 3.41.4 / Dart 3.11.1 and Xcode 16.2. Both the clean source candidate and exact public-package consumer passed all 11 checks:

- SEND, live EVENT and progressive UTF-8 replies, including Chinese and emoji.
- Cancellation and simulated failure preserve acknowledged partial snapshots.
- Generation while the receiver is offline; one full-history recovery at reconnect.
- Concurrent recovery snapshots and buffered deltas merge without repetition.
- Compatible model SSE split across UTF-8 bytes, CRLF delimiters and multiline JSON.
- Upstream error and missing DONE preserve failure; cancellation aborts the upstream connection.
- Model keys stay out of transcript/logs; the fixture confirms multi-turn context.
- Exactly three history reads for three reader connections, zero `/message/eventsync` reads and one observed upstream cancellation.

The projection regression first reproduced a real EVENT decimal-string `message_seq` failure, then verified numeric ordering after the fix. There are 29 SDK tests and five projection tests. Evidence includes JSON reports and partial/completed/recovered/model screenshots. The native driver invokes the actual screen controller and transport; it does not claim end-to-end coverage of every settings gesture.

## Build scope and artifacts

- Exact public 1.2.1 consumer: Android debug APK built successfully; SHA-256 `3bf8d410d3880a0379099935f38f0a9e2459d0a3972cfc93d6add07e41468c7a`. Android runtime was not exercised in this qualification.
- Source Web release build passed with `--no-tree-shake-icons` because the local Flutter cache lacked `const_finder.dart.snapshot`. Wasm dry-run analysis also passed. This is compilation evidence, not browser runtime acceptance.
- SDK minimum constraints remain unchanged. The shipped generated native example requires Flutter 3.41+ / Dart 3.11+.
- Controlled model fixtures are repeatable evidence; they do not certify all model providers, operating systems or physical devices.
- Persistent local artifacts: `WuKongEasySDK-Flutter/tmp/release-1.2.1/`, with registry verification, public lock file, literal analysis, JSON runtime reports, safe screenshots and build receipts. [GitHub Release](https://github.com/WuKongIM/WuKongEasySDK-Flutter/releases/tag/v1.2.1) publishes the final receipts and artifacts.

The historical Flutter 1.1.0 four-platform matrix remains literal and unchanged. Current documentation selection is centralized in `docs-site/lib/easy-sdk-releases.json`; this qualification does not transfer newer results onto old records.

## Documentation checks

`bun run verify` passed all 212 documentation tests, navigation/OpenAPI checks, lint, MDX/types, static export and the 450-URL RSC output contract. The named `flow-doc-contracts` check passed with 88 compliant / zero invalid FLOW files; 20 advisory length warnings predate this task. Both languages' generated HTML and Markdown select 1.2.1. Documentation delivery is a local commit; no site deployment is claimed.
