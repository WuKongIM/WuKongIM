# Phase 22: Source-aligned HarmonyOS SDK tutorial

## Goal

Publish a bilingual, reader-first HarmonyOS NEXT path for the full
`@wukong/wkim` package. The path must let an application team pin the exact
observed HAR, understand the package's singleton and synchronization lifecycle,
map the documented snippets to the shipped ArkTS source, and run a bounded
Alice/Bob online-message acceptance in its own DevEco environment without
turning artifact inspection into a build, device, runtime, or production
receipt.

Phase 22 publishes only:

- `/{lang}/sdk/harmonyos`
- `/{lang}/sdk/harmonyos/installation`
- `/{lang}/sdk/harmonyos/quickstart`

HarmonyOS platform capabilities, a complete API reference, and an upgrade guide
remain planned. The deprecated UniApp repository is handled separately and must
not be represented as a working standalone SDK by this phase.

This is the Phase 22 publication boundary. Phase 24 later publishes those
three chapters from the same pinned HAR and source while retaining the absence
of OHPM install, DevEco compile, HAP, emulator, and device receipts.

## Exact package and source snapshot

The tutorial targets the OHPM package `@wukong/wkim` `1.1.7`, published on
2026-08-27. Its exact registry HAR is:

```text
https://repo.harmonyos.com/ohpm/@wukong/wkim/-/wkim-1.1.7.har
SHA-256 d98d1523bc60ad204dd74d9cfa776935a5547fc3ab352322dfa17f5dbc7a3cd8
integrity sha512-864btKpDkxGQ9ACUGur6LJ7gIsmFGDub6WdY+znWQTXFjyNoJziiaGby/7ZE9owwvHRwE10E4V9ZMfU0ZO2DFA==
```

The HAR's `src/main/ets` tree and `index.ets` match official repository revision
`0c41810a1e0a5fc2936929d63ca32a50ffb11bec` (`chore: prepare release
1.1.7`) after excluding packaged `.DS_Store` files. Repository HEAD
`42505190601967d6a9fc8f321692689917b13a91` changes only the README documentation
URL after that release-preparation revision. The repository exposes no git
tags, so documentation must not invent a `1.1.7` tag.

The package metadata declares:

- `compatibleSdkVersion: 20` and `compatibleSdkType: HarmonyOS`;
- no package dependencies;
- a release HAR built with compile SDK `6.1.1.125`, API 20, for `default` and
  `tablet` device types;
- `ohos.permission.INTERNET` and `ohos.permission.GET_NETWORK_INFO`;
- `obfuscated: false`.

The package root exports only `WKIM`. The shipped demo imports `WKChannel`,
`WKConnectStatus`, `WKMsg`, `WKSendOptions`, and other beans through
`src/main/ets/entity/Bean`, `WKTextContent` through
`src/main/ets/model/WKTextContent`, and `WKLogger` through
`src/main/ets/common/WKLogger`. The tutorial may reproduce those exact artifact
subpaths with the published package name, but must label them as deep imports
that are not a stable root API contract.

## Evidence boundary

The exact HAR was downloaded, hashed, extracted, and compared with the official
source revision. No DevEco Studio SDK, `ohpm`, or `hvigor` executable is
available on the documentation host. Therefore:

- the public package was not installed through OHPM by the site;
- the downloadable ArkTS acceptance skeleton was not compiled;
- no HAP was built or signed;
- no emulator/device Alice/Bob scenario was run;
- the repository's `ohosTest` contains only the generated `abc`-contains-`b`
  template assertion and does not exercise the SDK.

The published skeleton is `/examples/harmonyos/WKAcceptanceSession.ets`,
SHA-256 `589554efcb4667ba41930358a7708d828900d70d4090abb2082ea95e810c37f1`.
Its hash is a documentation contract so the download cannot drift unnoticed,
but a source hash is not a compile or runtime receipt. A reviewed user receipt
requires the exact DevEco/SDK/ohpm/hvigor tuple, application lockfile digest,
build mode, HAP/device/OS, server revision, and redacted Alice/Bob result. A bare
statement that the SDK was “verified” does not broaden this site's receipt.

## Reader outcomes

After the three pages, a reader can:

1. choose the full HarmonyOS SDK without confusing it with EasySDK or the
   deprecated UniApp wrapper;
2. pin `@wukong/wkim` to `1.1.7`, commit `oh-package-lock.json5`, and compare its
   resolved artifact and integrity with the observed registry snapshot;
3. build a HarmonyOS NEXT Stage-model application at API 20 or newer and verify
   the merged INTERNET and network-state permissions;
4. initialize `WKIM.shared` exactly once for one UID, pass an explicit
   application `Context`, disable the controllable SDK logger, set
   `deviceFlagApp = 0`, and supply a trusted DNS/IPv4 `host:port` route;
5. install the conversation provider and the unremovable local-insert callback
   once at application scope;
6. accept readiness only after the application's conversation provider has
   delivered and the SDK reports `syncCompleted` for the same one-shot
   activation;
7. send one durable personal text, distinguish local insertion from SENDACK,
   and observe Bob's online receive;
8. close Alice only after a successful terminal SENDACK, close Bob only after
   the application has verified the expected online receipt, and terminate each
   one-shot process after the bounded acceptance.

## Exact lifecycle contract

`WKIM.shared` and every manager are process singletons. `await WKIM.shared.init`
sets the identity and route, opens a UID-named relational store, and marks
earlier sending rows failed. `WKDBHelper.init` catches and only logs database
open failures, so a fulfilled `init` promise is not proof that storage opened.
The sample therefore claims activation in module scope before the first init
and never releases that claim: constructing another wrapper instance cannot
switch UID or replace the process-wide providers and listeners.

The documented first-connection sequence is:

```text
connecting -> success -> syncing -> syncCompleted
```

`success` is CONNACK success, not application readiness. The implementation
awaits `conversationManager().sync()` and then emits `syncCompleted`. If
`syncConversationCallback` is absent, `sync()` returns immediately and the SDK
still emits `syncCompleted`. A tutorial gate must therefore require both an
application-owned provider-delivered flag and the matching `syncCompleted`.
Provider rejection is not caught by `decodePacket`; the bounded wrapper treats
it as terminal rather than waiting indefinitely.

Connection listeners are arrays and must be removed with the same function
object. A second `connecting`, timeout, CONNACK failure, `noNetwork`, kick,
provider failure, SENDACK timeout, or teardown marks the one-shot process
restart-required.

`messageManager().sendWithOption(...)` returns `void`. For a durable text it
synchronously constructs and inserts `WKMsg`, assigns `clientSeq`, invokes the
single `addInsertedListener` callback, then sends the packet. The callback has
no remove API and later registration overwrites the previous callback; it must
be owned by one application-scoped dispatcher. Terminal SENDACK arrives through
`addSendStatusListener(clientMsgSeq, messageId, messageSeq, reasonCode)`.
`WKSendMsgResult.success` (`1`) is success; other reason codes are rejections.
The SDK still invokes the inserted callback and sends a packet when database
insertion returns `clientSeq === 0`. The skeleton does not report that callback
as durable insertion; it keeps the dispatch unresolved and preserves the send
listener so a later ACK with sequence zero is recorded only as late terminal
evidence. A rejecting observer cannot bypass teardown because the terminal
transition runs in `finally`. Bob's `addNewMsgListener` event is online receipt
only.

Send-status and new-message listeners are removable. Teardown calls
`disConnection(true)` only after no send is awaiting local insertion or
SENDACK. The skeleton tracks a successful ACK explicitly, so the sender cannot
report completion before sending. It also gives receiver-only Bob a separate
cleanup path that is enabled only after an online message event; the observer
must verify that event against the expected sender, channel type, and content
before calling it. If a SENDACK times out while a packet is still in flight,
the run becomes terminal but deliberately keeps the SDK connection and send
listener intact so a late SENDACK is classified separately through
`onLateSendTerminal`; process termination, not SDK logout, is then the required
cleanup. Because logout does not clear the connection manager's `sendingMsgMap`,
provider fields, inserted callback, or all manager listeners, the tutorial does
not permit switching UID or starting another acceptance in the same process.

## Security and adoption blockers

Exact `1.1.7` source:

- opens `@ohos.net.socket` `TCPSocket` over raw TCP and exposes no
  `SecureSocket` or authenticated TLS path;
- parses routes with `split(":")`, so the documented route is one DNS/IPv4 host
  and port, not a URI or IPv6 literal;
- defaults `WKLogger.showLog` to true and logs the complete outbound JSON
  payload; disabling `WKLogger` does not suppress two direct `hilog.info`
  send-trace calls containing channel and client identifiers;
- exposes `WKConfig.debug`, but exact source never reads it;
- persists decrypted message payloads in `${uid}.db` with relational-store
  `securityLevel: S1` and `encrypt: false`;
- stores the UID-scoped device identifier and migration version in Preferences;
- retains every sent packet in `sendingMsgMap`: SENDACK does not remove it,
  `isCanResend` starts false and is never set true, and logout does not clear
  the map;
- catches database-open failures and still lets `WKIM.init` fulfill;
- ships only generated template tests, is not obfuscated, and includes
  `.DS_Store` files in the HAR.

WKProto X25519/AES payload protection does not replace authenticated TLS.
Production adoption requires transport, logging, encrypted-storage, database
error propagation, queue cleanup/account isolation, stable root exports,
package hygiene, and real tests to be fixed or explicitly accepted and
validated. Offline, push, background, multi-device, upgrade, and scale behavior
need separate receipts.

## Publication rules

- Publish the three bilingual routes only after their contract test, sample
  hash check, independent Standards/Spec reviews, and `bun run verify` pass.
- Discovery and compatibility pages may call the HarmonyOS path
  source/artifact aligned, but must not call it installed, compiled, device
  tested, runtime verified, or production ready.
- EasySDK was planned at this phase because Product Gateway JSON-RPC CONNECT
  was unsupported. A later documentation-only change published the
  source-aligned tutorials without claiming runtime compatibility.
- Do not imply a standalone UniApp SDK remains supported; its official
  repository says it is deprecated and directs users to the JavaScript SDK.
