# Phase 21: Source-aligned Flutter SDK tutorial

## Goal

Publish a bilingual, reader-first Flutter path for the full
`wukongimfluttersdk` package. The path must let an application team install the
exact observed package, understand its singleton and synchronization lifecycle,
compile the documented API, and run a bounded Alice/Bob online-message
acceptance without turning source or desktop-build evidence into a device or
production receipt.

Phase 21 publishes only:

- `/{lang}/sdk/flutter`
- `/{lang}/sdk/flutter/installation`
- `/{lang}/sdk/flutter/quickstart`

Flutter platform capabilities, a complete API reference, and an upgrade guide
remain planned.

## Exact package and source snapshot

The tutorial targets the pub.dev package `wukongimfluttersdk` `1.7.9`, published
on 2026-04-28. Its archive is:

```text
https://pub.dev/api/archives/wukongimfluttersdk-1.7.9.tar.gz
SHA-256 b6191a86cd1e4caacaa4652e95709310eb1493f159fee65e1dd53c2a3ff9e80a
```

The archive's `lib`, `assets`, `pubspec.yaml`, and `CHANGELOG.md` match official
repository revision `de1024276523119e38305c49a3a873caae4d5c59`. The repository
does not tag `1.7.9`; its only observed tag is the stale `v1.0.0`. The pub.dev
uploader is not a verified publisher. Documentation must therefore pin the
hosted package version and application lockfile hash, and record the matching
source revision separately rather than inventing a release tag.

The package declares Dart `>=2.17.0 <4.0.0` but no explicit Flutter minimum.
Its direct hosted dependency constraints are `path ^1.8.3`, `encrypt ^5.0.1`,
`cupertino_icons ^1.0.2`, `x25519 ^0.1.1`, `hex ^0.2.0`, `crypto ^3.0.6`,
`uuid ^4.3.3`, `dio ^5.3.2`, `shared_preferences ^2.2.0`, `sqflite ^2.4.1`, and
`connectivity_plus ^6.1.0`. Applications must commit `pubspec.lock` and run
`flutter pub get --enforce-lockfile` in CI; the `wukongimfluttersdk` lock entry
must contain the archive SHA above.

## Evidence boundary

The exact public package was consumed by a temporary application using Flutter
`3.41.4`, Dart `3.11.1`, and macOS `15.1` arm64. The documented wrapper passed
`flutter analyze`, `flutter pub get --enforce-lockfile`, and a macOS Release
build. The published wrapper is `/examples/flutter/wk_acceptance.dart`, SHA-256
`c2a6f4a2c39029b945ad8e251420f18afef871fb574fc3ccf3cbe474f7d8050c`.
Its hash is a documentation contract so the download cannot drift from the
compile-checked source without failing verification.

This is a compile/build receipt only:

- Android was not built because the host lacks Android command-line tools.
- iOS device Release did not complete because the temporary application had no
  selected Development Team/provisioning profile.
- No Alice/Bob server scenario was run by the site.
- The package's own `flutter analyze` reports 93 warnings/info findings, and its
  own `flutter test` fails `test/wksdk_test.dart` (`cust data`) with a
  `RangeError` in `ReadData.readString` through the packet cut/decode path.

A user statement that the SDK was “verified” is not enough to broaden this
receipt. A reviewed runtime receipt needs the exact Flutter/Dart versions,
platform/toolchain, build mode, device/OS, server revision, package lock hash,
and redacted Alice/Bob result.

## Reader outcomes

After the three pages, a reader can:

1. choose the full Flutter SDK without confusing it with EasySDK or Web;
2. pin `wukongimfluttersdk: 1.7.9`, commit the content-hash lockfile, and run a
   lock-enforced build;
3. initialize the process singleton exactly once with `await
   WKIM.shared.setup(...)`, `debug = false`, `deviceFlag = 0`, and a trusted
   DNS/IPv4 `host:port` route;
4. install the unkeyed conversation-sync and local-insert providers once at
   application scope;
5. accept readiness only after `success -> syncMsg -> syncCompleted`, with the
   synchronization callback fenced to the active connection generation;
6. send one durable personal text with `await sendWithOption(...)`, then
   distinguish local insertion, successful/rejected SENDACK refresh, and Bob's
   online receive;
7. remove every keyed listener and terminate the one-shot process safely.

## Exact lifecycle contract

`WKIM.shared` and every manager are process singletons. `setup` initializes the
UID-scoped sqflite database and calls `updateSendingMsgFail()`, which marks
earlier sending rows failed; it does not recover an earlier send.

`connect()` synchronously calls `disconnect(false)` first, so a listener sees a
synthetic `WKConnectStatus.fail` with a null reason before the first
`connecting`. The accepted first-connection sequence is:

```text
fail(null) -> connecting -> success -> syncMsg -> syncCompleted
```

There is no final second `success` after Flutter synchronization. Sending is
enabled only at a `syncCompleted` tied to the latest application provider
completion. A production provider must map a trusted backend response into
`WKSyncConversation`; an empty result is permitted only for brand-new test
accounts in a bounded online acceptance.

Socket-connect failures schedule an internal delayed reconnect without a public
generation or cancellation handle. `disconnect(false)` also cannot cancel a
retry already scheduled by `_connectFail`. The tutorial therefore permits only
one `connecting` generation. A second generation, timeout, non-null CONNACK
failure, `noNetwork`, kick, provider failure, SENDACK timeout, or teardown marks
the process restart-required and clears identity with `disconnect(true)`.

The README's `sendMessage(...)` wrapper invokes `sendWithOption(...)` without
awaiting it. The tutorial calls and awaits `sendWithOption(...)` directly. Its
durable local insertion is observed through the unkeyed
`addOnMsgInsertedListener`; terminal SENDACK state arrives through keyed
`addOnRefreshMsgListener`. Correlation uses both `clientMsgNO` and `clientSeq`.
`WKSendMsgResult.sendSuccess` is success; every other terminal reason is a
rejection. Bob's keyed `addOnNewMsgListener` is online receipt only.

The unkeyed sync and insert providers have no remove API and must remain owned
by one application-scoped dispatcher. Keyed connection, refresh, and new-message
listeners must be removed with the exact same key. Teardown is forbidden while
a send is awaiting insertion or terminal SENDACK.

## Security and adoption blockers

Exact `1.7.9` source:

- opens `dart:io Socket.connect` over raw TCP and exposes no `SecureSocket` or
  authenticated TLS path;
- defaults `Options.debug` to `true` and logs decrypted `RecvPacket.toString()`,
  including message Payload, when debug is enabled;
- stores messages and conversation data in a plain UID-named sqflite database;
- stores a UID-scoped device identifier and schema version in
  `SharedPreferences`;
- retains `_sendingMsgMap` across `disconnect(true)`, allowing unresolved work
  to survive logout inside the process;
- exposes a `ConnectPacket.toString()` containing UID and Token;
- parses routes by `addr.split(":")`, so the documented route is one DNS/IPv4
  host and port, not a URI or IPv6 literal;
- imports `dart:io` and uses sqflite, while pub.dev advertises Android, iOS, and
  macOS rather than Web.

WKProto payload encryption does not replace authenticated TLS. Production
adoption requires an SDK transport fix, encrypted local storage and credential
policy, log review, retry/generation cancellation, queue isolation on logout,
parser hardening, and separate offline/push/background/device receipts.

## Publication rules

- Publish the three bilingual routes only after their contract test, exact Dart
  sample analysis, independent Standards/Spec reviews, and `bun run verify`
  pass.
- Discovery and compatibility pages may call the Flutter path source/archive
  aligned and macOS-build checked, but must not call it Android/iOS/runtime or
  production verified.
- EasySDK remains planned because Product Gateway JSON-RPC CONNECT is still
  unsupported.
