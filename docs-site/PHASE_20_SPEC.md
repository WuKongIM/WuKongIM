# WuKongIM v3 Documentation — Phase 20 Specification

## Objective

Publish a reader-first integration path for the full WuKongIM Android SDK from
one pinned source tag and the matching JitPack AAR. Phase 20 publishes only the
Android overview, installation, and online-message quickstart. Source and AAR
inspection must not be presented as an Android build, emulator/device receipt,
or production-readiness claim.

The legacy Android documentation supplies the useful learning sequence—choose
the manager, install, initialize, provide a route and synchronization source,
connect, send, receive, and remove listeners—but is not an API authority. The
`1.5.5` source and distributed AAR override legacy snippets whenever they
disagree.

## Published routes

Phase 20 publishes bilingual content for:

- `/sdk/android`
- `/sdk/android/installation`
- `/sdk/android/quickstart`

The following routes remain planned and excluded from indexed output:

- `/sdk/android/platform-capabilities`
- `/sdk/android/api-reference`
- `/sdk/android/upgrade`

Flutter, UniApp, and HarmonyOS full-SDK tutorials remain planned in this phase.
EasySDK was also planned at this phase while its JSON-RPC CONNECT path was
unsupported by the Product Gateway; a later documentation-only change
published the source-aligned tutorials without claiming runtime compatibility.

This is the Phase 20 publication boundary. Phase 24 later publishes the three
Android chapters above from the same pinned AAR and source while retaining the
absence of an Android build, emulator, or device receipt.

## Exact source and artifact snapshot

| Component | Exact snapshot |
| --- | --- |
| Source repository | `WuKongIM/WuKongIMAndroidSDK`, tag `1.5.5`, revision `662a559a50d181540a0448454beb57e939b0c50e` |
| JitPack coordinate | `com.github.WuKongIM:WuKongIMAndroidSDK:1.5.5` |
| Observed AAR SHA-256 | `5a797f1fac53c4fbcf015afca2686ecbeebd24b5e64dea598881b814b1322792` |
| Android source build | compile SDK 34, target SDK 34, minimum SDK 21 |
| Java/Kotlin bytecode target | Java 17 / JVM 17; distributed `WKIM.class` major version 61 |

The JitPack build API reports status `ok`, the exact source revision, and
`isTag: true`. The AAR contains `classes.jar` plus
`libs/xSocket-2.8.15.jar`. Its POM imports
`org.jetbrains.kotlin:kotlin-bom:1.9.22` and declares runtime dependencies on
`kotlin-stdlib:1.9.22`, `kotlin-stdlib-jdk8:1.9.22`,
`com.android.support:multidex:1.0.3`,
`net.zetetic:sqlcipher-android:4.9.0`, `androidx.sqlite:sqlite-ktx:2.5.1`, and
`org.whispersystems:curve25519-java:0.5.0`. Legacy instructions that add older
SQLCipher or Curve25519 artifacts manually must not be copied.

The artifact has stale identity surfaces: `WKIM.getVersion()` returns
`V1.5.0`, while the tag and coordinate are `1.5.5`; the source's local Maven
publication also says `1.0.7`. Neither runtime nor local-publication value may
attest the installed package. Provenance comes from the dependency lock,
JitPack coordinate, exact revision, and reviewed AAR checksum.

The source's `consumer-rules.pro` and the distributed AAR `proguard.txt` are
empty even though source `proguard-rules.pro` contains SDK, xSocket,
Curve25519, and SQLCipher rules. A minified consumer must add reviewed rules
itself and validate a Release build; the tutorial must not imply they are
inherited automatically.

## Installation contract

The installation page must:

- add JitPack through modern `dependencyResolutionManagement` and pin the
  exact `1.5.5` coordinate;
- declare the exact minimum/compile/target SDK and Java 17 source facts without
  turning them into validation for every current Android Gradle Plugin or
  device tuple;
- inventory the AAR and POM dependency shape and tell readers not to duplicate
  the legacy dependency block;
- explain the empty consumer-rule artifact and require a minified Release
  smoke test with explicit app-owned R8 rules;
- record the exact tag, revision, checksum, lockfile, and stale version fields;
- explain that dependency locking pins versions rather than bytes, commit
  `gradle/verification-metadata.xml`, and enforce the reviewed AAR SHA in strict
  dependency-verification mode;
- keep Product HTTP management credentials out of the Android application.

## Quickstart contract

The quickstart must map the exact Java public API into this bounded lifecycle:

1. A trusted product backend authenticates the application user and returns
   `uid`, a short-lived `token`, and the current Gateway host/port. The client
   never receives Product HTTP management credentials.
2. An application-scoped integration registers the route and conversation-sync
   providers, disables debug/file logging, and calls `WKIM.init` with
   `context.getApplicationContext()`.
3. The default READ synchronization mode requires
   `addOnSyncConversationListener`: after CONNACK the SDK emits an initial
   `success`, then `syncMsg`, then `syncCompleted`, and finally the accepted
   `success`. The application must not enable sending on the first success.
   The bounded tutorial accepts only the first `connecting`; a second direct
   `connecting` has no public connection/sync generation, may arrive after the
   first acceptance timer was cancelled, and is terminal/process-restart-
   required rather than reusing the old provider epoch.
4. A fresh-user, online-only development acceptance may return an explicit
   empty `WKSyncChat`; existing users and production must use a real trusted
   backend mapping. An empty provider is not offline/conversation recovery.
   Provider completion must be single-shot and fenced by a monotonic activation
   epoch so a stale result cannot complete a newer attempt.
5. The session registers keyed connection, local-insert, SENDACK, and new-
   message listeners before `connection()`, enforces independent 15-second
   application bounds for connection acceptance and each terminal SENDACK, and
   removes the same keys on teardown. Internal terminal state, cleanup, and
   send timers must be committed before observer notifications. Every queued
   notification is fenced by the active attempt; message events cross that
   asynchronous boundary only as application-owned immutable snapshots, and
   teardown invalidates already-queued callbacks before same-UID restart.
6. Alice creates a durable `WKMsg` with `WKTextContent`, a personal channel,
   and the SDK-created `clientMsgNO`, then calls the non-deprecated
   `sendMessage(WKMsg)`. Local insertion, SENDACK, and Bob's online receive are
   separate events; ACK correlation uses `clientMsgNO`/`clientSeq`, not a
   pending server `messageID` or `messageSeq`. Because the local-insert callback
   can occur inline, it must be staged until `sendMessage` returns and accepted
   only with a positive `clientSeq`; a missing/non-positive insert or mismatched
   early ACK is terminal and process-restart-required because a sequence-zero
   send may already have entered the network path. The SENDACK timer is armed
   before `onLocalInsert` delivery, and the SENDACK path must branch
   `WKSendMsgResult.send_success` from rejection statuses instead of naming
   every response successful.
7. `disconnect(false)` stops the current connection and reconnect loop while
   retaining identity. Account logout uses `disconnect(true)`, which clears the
   token, stops the connection, clears caches, and asynchronously closes the
   UID-scoped database. Neither teardown path may run with the tutorial's send
   still in flight, and timeout/kick paths must invalidate the attempt, remove
   keyed listeners, and deactivate providers. Because the provider API has no
   cancellation handle or SDK-side attempt identity after callback handoff, a
   timeout, kick, `fail`, `noNetwork`, unresolved-send timeout, uncertain local
   insertion, or explicit teardown before final ready must also block further
   connection attempts until the application process restarts. The tutorial
   treats `fail` and `noNetwork` as terminal instead of permitting an
   unidentifiable automatic-reconnect generation.

Calling the low-level send API during `syncMsg` still performs local insertion
and adds the item to `sendingMsgHashMap`; only network transmission is deferred
until final success invokes `resendMsg()`. The tutorial wrapper blocks the call
before final success rather than claiming that the SDK rejects local queuing.

The tutorial must not use stale symbols or shapes such as
`WKConnectStatus.failed`, `ConnectionManager.sendMessage`,
`removeOnSendMsgCallback`, or deprecated content/channel scalar send overloads.

## Security and production-adoption boundaries

Exact `1.5.5` source opens `org.xsocket.connection.NonBlockingConnection` over
raw TCP and contains no TLS socket, TLS option, or `startTLS` path. WKProto's
Curve25519/AES-CBC payload compatibility encryption does not authenticate the
server and is not TLS. Production requires controlled/private ingress or a
reviewed authenticated-transport upgrade.

`WKIM.init` stores UID and token in ordinary `MODE_PRIVATE` SharedPreferences.
The SQLCipher database is opened with the UID itself as the password. An app-
sandbox file and a UID are not independently managed secrets; production
adoption requires reviewed credential storage, database key management, backup,
account-switch, logout, and erasure behavior or an SDK patch.

`disconnect(true)` does not clear `WKConnection.sendingMsgHashMap`, while a
later successful connection invokes `resendMsg()`. Logout therefore is not a
safe same-process account-switch boundary with unacknowledged sends. The
tutorial must require terminal SENDACK before teardown and classify production
account switching as requiring an SDK queue-isolation/cleanup fix or process
isolation.

Debug logging must remain disabled: the exact decode path logs
`WKReceivedMsg.toString()` when debug is enabled, and that string includes the
decrypted payload. `setWriteLog(false)` must also remain disabled, and Release
artifacts plus the complete logging pipeline require inspection. Current warning
logging still emits selected lifecycle warnings even with debug disabled, so a
production review cannot treat the flag as a universal no-log switch.

The exact CONNECT packet uses device flag `0`, matching the server contract
`0=APP`, `1=WEB`, `2=PC`. This source fact does not by itself prove multi-device
or push behavior.

## Evidence classification

Phase 20 separates:

1. **Source availability:** the official `1.5.5` tag and revision are
   reachable.
2. **Artifact/API alignment:** the matching JitPack AAR is downloadable, its
   checksum and public class signatures were inspected, and JitPack records the
   exact tag revision.
3. **Tutorial publication:** bilingual pages map those facts to a bounded
   integration path.
4. **Executable verification:** no Android SDK toolchain build, emulator/device
   Alice/Bob loop, or shared compatibility receipt is added by this phase.

`/compatibility.json` remains scoped to the JavaScript/Web golden path. SDK
discovery and compatibility prose must describe Android as source/AAR aligned,
not runtime verified.

## Verification

Phase 20 requires:

- a failing Android documentation contract before publication;
- bilingual route and navigation parity;
- exact snapshot, AAR API, non-deprecated lifecycle, synchronization gate,
  generation fencing, SENDACK success/rejection branching, listener cleanup,
  account-switch blocking, checksum enforcement, transport, logging, storage,
  and evidence assertions;
- deterministic navigation generation;
- the complete documentation verification gate and production static export;
- a two-axis Standards and Specification review before the final commit.

## Non-goals

- Publishing the complete Android manager/API catalog, platform-capability
  matrix, upgrade guide, push guide, or production offline data sources.
- Claiming native TLS, encrypted credential storage, independent SQLCipher key
  management, inherited consumer R8 rules, or runtime version attestation.
- Adding Android to the JavaScript/Web compatibility receipt.
- Changing server or SDK runtime behavior.
