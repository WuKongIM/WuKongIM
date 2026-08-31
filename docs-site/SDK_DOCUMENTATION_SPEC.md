# WuKongIMSDK documentation specification

## Goal

Help an application developer who knows their platform but is new to WuKongIM
connect one user, send the first message, and then find the next task without
learning repository history or internal verification processes.

This specification covers the full WuKongIMSDK family. WuKongEasySDK has its
own section and examples.

## Maintained platforms

| Platform | Package | Documented version | Example language |
| --- | --- | --- | --- |
| Android | `WuKongIMAndroidSDK` | `1.5.5` | Java |
| iOS | `WuKongIMSDK` | `1.1.1` | Objective-C |
| JavaScript / Web | `wukongimjssdk` | `1.3.5` | TypeScript |
| Flutter | `wukongimfluttersdk` | `1.7.9` | Dart |
| HarmonyOS | `@wukong/wkim` | `1.1.7` | ArkTS |

The version appears on each platform entry and quickstart. API examples must be
checked against that released package and its matching official source.

## Information architecture

Every maintained platform publishes the same core tasks:

1. overview;
2. quickstart: install, connect, and send one online text message;
3. connection;
4. messages;
5. conversations;
6. channels;
7. advanced features that the platform actually exposes;
8. concise API reference.

Shared pages explain core concepts and upgrading once. Old installation,
platform-capability, per-platform upgrade, common-guide, chooser, compatibility,
and standalone UniApp pages exist only as redirects to current destinations.

## Writing contract

- Lead with the task and expected result.
- Explain `Channel`, `Conversation`, Provider, local insertion, and server send
  result before relying on those terms.
- Keep installation and first-message code together in the quickstart.
- Show listener cleanup and distinguish disconnect from logout.
- State which APIs the application must provide, especially identity, routing,
  conversations, history, channel metadata, and media upload.
- Keep advanced pages optional and end them with short production warnings.
- Do not publish source hashes, internal audit language, temporary phase notes,
  or device-validation scaffolds as reader documentation.
- Do not mix full-SDK and EasySDK method names on one integration path.

## Validation

`lib/wukongim-sdk-reader-contract.test.ts` is the focused content contract. The
normal `bun run verify` gate also checks navigation parity, internal links, MDX,
TypeScript, lint, static output, search, sitemap, and machine-readable exports.
