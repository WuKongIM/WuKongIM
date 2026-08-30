import { describe, expect, test } from 'bun:test';
import { getIndexedNavigationEntries, getNavigationEntry } from './navigation';

const docsRoot = new URL('../content/docs/sdk/', import.meta.url);

const snapshot = {
  version: '1.5.5',
  repository: 'https://github.com/WuKongIM/WuKongIMAndroidSDK',
  revision: '662a559a50d181540a0448454beb57e939b0c50e',
  coordinate: 'com.github.WuKongIM:WuKongIMAndroidSDK:1.5.5',
  aarSha256: '5a797f1fac53c4fbcf015afca2686ecbeebd24b5e64dea598881b814b1322792',
} as const;

async function sdkDoc(fileName: string) {
  return Bun.file(new URL(fileName, docsRoot)).text();
}

describe('full Android SDK tutorial contract', () => {
  test('pins the Phase 20 source, artifact, and evidence boundary', async () => {
    const specification = await Bun.file(
      new URL('../PHASE_20_SPEC.md', import.meta.url),
    ).text();

    expect(specification).toContain(snapshot.coordinate);
    expect(specification).toContain(snapshot.revision);
    expect(specification).toContain(snapshot.aarSha256);
    expect(specification).toContain('source/AAR aligned');
    expect(specification).toContain('no Android SDK toolchain build, emulator/device');
    expect(specification).toContain('verification-metadata.xml');
    expect(specification).toContain('sendingMsgHashMap');
    expect(specification).toContain('monotonic activation');
    expect(specification).toContain('platform-capabilities');
    expect(specification).toContain('remain planned');
  });

  test('publishes only the bilingual Android overview, installation, and quickstart', async () => {
    const published = getIndexedNavigationEntries('en').map((entry) => entry.url);

    expect(getNavigationEntry('en', 'sdk', ['android'])?.status).toBe('published');
    expect(getNavigationEntry('en', 'sdk', ['android', 'installation'])?.status).toBe(
      'published',
    );
    expect(getNavigationEntry('en', 'sdk', ['android', 'quickstart'])?.status).toBe(
      'published',
    );
    expect(published).toEqual(
      expect.arrayContaining([
        '/en/sdk/android',
        '/en/sdk/android/installation',
        '/en/sdk/android/quickstart',
      ]),
    );

    for (const slug of ['platform-capabilities', 'api-reference', 'upgrade']) {
      expect(getNavigationEntry('en', 'sdk', ['android', slug])?.status).toBe('planned');
      expect(published).not.toContain(`/en/sdk/android/${slug}`);
    }

    for (const fileName of [
      'android/index.mdx',
      'android/index.en.mdx',
      'android/installation.mdx',
      'android/installation.en.mdx',
      'android/quickstart.mdx',
      'android/quickstart.en.mdx',
    ]) {
      expect(await Bun.file(new URL(fileName, docsRoot)).exists()).toBe(true);
    }
  });

  test('installs and audits the exact JitPack artifact without copying legacy dependencies', async () => {
    const pages = await Promise.all([
      sdkDoc('android/installation.mdx'),
      sdkDoc('android/installation.en.mdx'),
    ]);

    for (const page of pages) {
      expect(page).toContain(snapshot.coordinate);
      expect(page).toContain(snapshot.repository);
      expect(page).toContain(snapshot.revision);
      expect(page).toContain(snapshot.aarSha256);
      expect(page).toContain('https://jitpack.io');
      expect(page).toContain('minSdk = 21');
      expect(page).toContain('compileSdk = 34');
      expect(page).toContain('targetSdk = 34');
      expect(page).toContain('JavaVersion.VERSION_17');
      expect(page).toContain('xSocket-2.8.15.jar');
      expect(page).toContain('sqlcipher-android:4.9.0');
      expect(page).toContain('sqlite-ktx:2.5.1');
      expect(page).toContain('curve25519-java:0.5.0');
      expect(page).toContain('kotlin-bom:1.9.22');
      expect(page).toContain('kotlin-stdlib:1.9.22');
      expect(page).toContain('kotlin-stdlib-jdk8:1.9.22');
      expect(page).toContain('consumer-rules.pro');
      expect(page).toContain('proguard.txt');
      expect(page).toContain('V1.5.0');
      expect(page).toContain('1.0.7');
      expect(page).toContain('dependencyLocking');
      expect(page).toContain('verification-metadata.xml');
      expect(page).toContain('--write-verification-metadata sha256 :app:assembleRelease');
      expect(page).toContain('--dependency-verification strict');
      expect(page).toMatch(/解析依赖的 task|dependency-resolving task/u);
      expect(page).not.toContain('android-database-sqlcipher:4.5.3');
      expect(page).not.toMatch(/WuKongIMAndroidSDK:(?:version|latest)/u);
    }
  });

  test('maps the quickstart to the exact non-deprecated Java API and synchronization lifecycle', async () => {
    const pages = await Promise.all([
      sdkDoc('android/quickstart.mdx'),
      sdkDoc('android/quickstart.en.mdx'),
    ]);
    const requiredAPI = [
      'getApplicationContext()',
      'setDebug(false)',
      'setWriteLog(false)',
      'WKIM.getInstance().init',
      'addOnGetIpAndPortListener',
      'onGetSocketIpAndPort',
      'addOnSyncConversationListener',
      'WKSyncChat',
      'addOnConnectionStatusListener',
      'WKConnectStatus.syncMsg',
      'WKConnectStatus.syncCompleted',
      'WKConnectStatus.success',
      'WKConnectStatus.kicked',
      'WKSendMsgResult.send_success',
      'addOnSendMsgCallback',
      'addOnSendMsgAckListener',
      'addOnNewMsgListener',
      'new WKTextContent',
      'WKChannelType.PERSONAL',
      'sendMessage(message)',
      'clientMsgNO',
      'clientSeq',
      'removeOnConnectionStatusListener',
      'removeSendMsgCallBack',
      'removeSendMsgAckListener',
      'removeNewMsgListener',
      'disconnect(false)',
      'disconnect(true)',
      'activationEpoch',
      'AtomicBoolean',
      'didDeliver',
      'processUid',
      'restartBlocked',
      'requireProcessRestart',
      'onSendSucceeded',
      'onSendRejected',
      'onTerminalError',
      'stagedLocalInsert',
      'earlySendAck',
      'sendAckTimeout',
      'armSendAckTimeout',
      'cancelSendAckTimeout',
      'MessageSnapshot',
      'Collections.unmodifiableList',
      'notifyActiveObserver',
      'notifyTerminalObserver',
      'connectingCount',
      'reconnectWithoutFence',
      'automatic reconnect has no public generation fence',
      'requireNoInFlightSend',
      'resendMsg()',
    ];

    for (const page of pages) {
      for (const api of requiredAPI) expect(page).toContain(api);
      expect(page).toContain('15_000');
      expect(page).toContain('Alice');
      expect(page).toContain('Bob');
      expect(page).toContain('uid');
      expect(page).toContain('token');
      expect(page).toContain('host');
      expect(page).toContain('port');
      expect(page).not.toContain('WKConnectStatus.failed');
      expect(page).not.toContain('removeOnSendMsgCallback');
      expect(page).not.toContain('getConnectionManager().sendMessage');
      expect(page).not.toContain('.isBlank()');
      expect(page).not.toContain('The SDK refuses ordinary sends');
      expect(page).not.toMatch(/普通 `fail` \/ `noNetwork` 仍可能进入 SDK 重连|Ordinary `fail` \/ `noNetwork` can still enter SDK reconnect/u);
      expect(page).toMatch(/重启应用进程|Restart the application process/u);
      expect(page).toMatch(/没有取消句柄|no cancellation handle/u);
      expect(page).toMatch(/终止连接状态|terminal connection status/u);
    }

    expect(pages[0]).toContain('第一次 `success`');
    expect(pages[1]).toContain('first `success`');
    expect(pages[0]).toContain('仅限全新测试账号');
    expect(pages[1]).toContain('fresh test accounts only');
  });

  test('bounds uncertain local insertion and SENDACK paths before observer delivery', async () => {
    const pages = await Promise.all([
      sdkDoc('android/quickstart.mdx'),
      sdkDoc('android/quickstart.en.mdx'),
    ]);

    for (const page of pages) {
      expect(page).toContain('main.postDelayed(sendAckTimeout, 15_000);');
      expect(page).toContain('inserted == null || inserted.clientSeq <= 0');
      expect(page).toContain('stagedLocalInsert = message;');
      expect(page).toContain('earlySendAck = message;');
      expect(page.indexOf('armSendAckTimeout(')).toBeLessThan(
        page.indexOf('observer.onLocalInsert(insertedSnapshot)'),
      );
      expect(page).toMatch(
        /providers\.requireProcessRestart\(\);[\s\S]*finish\(false\);[\s\S]*observer\.onConnectionState/u,
      );
      expect(page).toMatch(/本地入库.*进程重启|local insertion.*process restart/iu);
      expect(page).toMatch(/SENDACK.*15.*(?:超时|timeout)/iu);
      expect(page).toContain('public MessageSnapshot sendText(');
      expect(page).toContain('currentAttempt == attempt && bootstrap != null');
      expect(page).toContain('terminalAttempt == attempt && bootstrap == null');
      expect(page).toContain('++connectingCount > 1');
      expect(page).not.toContain('void onLocalInsert(WKMsg message)');
      expect(page).not.toContain('void onNewMessages(List<WKMsg> messages)');
      expect(page).toMatch(
        /MessageSnapshot\.from\(inserted\)[\s\S]*notifyActiveObserver/u,
      );
      expect(page).toMatch(
        /List<MessageSnapshot>[\s\S]*Collections\.unmodifiableList/u,
      );
      expect(page).toMatch(
        /不可变快照.*旧会话|immutable snapshots?.*stale session/iu,
      );
      expect(page).toMatch(
        /第二次 `connecting`.*进程重启|second `connecting`.*process restart/iu,
      );
    }
  });

  test('documents failed-send initialization and the unsafe same-process account switch', async () => {
    const pages = await Promise.all([
      sdkDoc('android/index.mdx'),
      sdkDoc('android/index.en.mdx'),
      sdkDoc('android/quickstart.mdx'),
      sdkDoc('android/quickstart.en.mdx'),
    ]);

    for (const page of pages) {
      expect(page).toContain('sendingMsgHashMap');
      expect(page).toMatch(/进程隔离|process isolation/u);
      expect(page).toMatch(/终止 SENDACK|terminal SENDACK/u);
    }
    expect(pages[0]).toContain('updateLastSendingMsgFail()');
    expect(pages[1]).toContain('updateLastSendingMsgFail()');
  });

  test('publishes transport, credential, database, logging, device, and proof blockers', async () => {
    const pages = await Promise.all([
      sdkDoc('android/index.mdx'),
      sdkDoc('android/index.en.mdx'),
      sdkDoc('android/installation.mdx'),
      sdkDoc('android/installation.en.mdx'),
      sdkDoc('android/quickstart.mdx'),
      sdkDoc('android/quickstart.en.mdx'),
    ]);

    for (const page of pages) {
      expect(page).toContain(snapshot.version);
      expect(page).toContain('NonBlockingConnection');
      expect(page).toMatch(/TCP/u);
      expect(page).toMatch(/TLS/u);
      expect(page).toContain('SharedPreferences');
      expect(page).toContain('SQLCipher');
      expect(page).toMatch(/UID/u);
      expect(page).toContain('WKReceivedMsg.toString()');
      expect(page).toMatch(/Payload|payload/u);
      expect(page).toContain('0=APP');
      expect(page).toContain('1=WEB');
      expect(page).toContain('2=PC');
    }

    for (const page of [pages[0], pages[2], pages[4]]) {
      expect(page).toContain('不是本站 Android 运行验证');
      expect(page).toContain('生产阻断项');
    }
    for (const page of [pages[1], pages[3], pages[5]]) {
      expect(page).toContain('not Android runtime verification');
      expect(page).toContain('production blocker');
    }
  });

  test('keeps discovery and compatibility pages aligned with Android evidence', async () => {
    const pages = await Promise.all([
      sdkDoc('index.mdx'),
      sdkDoc('index.en.mdx'),
      sdkDoc('choose-sdk.mdx'),
      sdkDoc('choose-sdk.en.mdx'),
      sdkDoc('compatibility.mdx'),
      sdkDoc('compatibility.en.mdx'),
    ]);

    for (const page of pages) {
      expect(page).toContain('/sdk/android');
      expect(page).toContain(snapshot.version);
      expect(page).toMatch(/Android/u);
      expect(page).toMatch(/receipt/u);
    }

    expect(pages[0]).toContain('源码与 JitPack AAR 已校对');
    expect(pages[1]).toContain('source and JitPack AAR are aligned');
    expect(pages[2]).toContain(snapshot.revision);
    expect(pages[3]).toContain(snapshot.revision);
    expect(pages[4]).toContain('不属于本页 receipt');
    expect(pages[5]).toContain('is not covered by this receipt');
  });
});
