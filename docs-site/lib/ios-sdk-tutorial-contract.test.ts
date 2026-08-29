import { describe, expect, test } from 'bun:test';
import { getIndexedNavigationEntries, getNavigationEntry } from './navigation';

const docsRoot = new URL('../content/docs/sdk/', import.meta.url);

const snapshot = {
  version: '1.1.1',
  sourceRepository: 'https://github.com/WuKongIM/WuKongIMiOSSDK',
  sourceRevision: '89bf9a1b95ce374caabdd8031d69cc8844d825ae',
  frameworkRepository: 'https://github.com/WuKongIM/WuKongIMiOSSDK-Framework',
  frameworkRevision: '0cbfb99f18010fe76b7e13ed31b5d1ad4664b10c',
  distribution: 'https://cocoapods.org/pods/WuKongIMSDK',
} as const;

async function sdkDoc(fileName: string) {
  return Bun.file(new URL(fileName, docsRoot)).text();
}

describe('full iOS SDK tutorial contract', () => {
  test('pins the Phase 19 scope and evidence snapshot', async () => {
    const specification = await Bun.file(
      new URL('../PHASE_19_SPEC.md', import.meta.url),
    ).text();

    expect(specification).toContain("`WuKongIMSDK` `1.1.1`");
    expect(specification).toContain(snapshot.sourceRevision);
    expect(specification).toContain(snapshot.frameworkRevision);
    expect(specification).toContain('source/header aligned');
    expect(specification).toContain('no iOS build, simulator/device message loop');
    expect(specification).toContain('platform-capabilities');
    expect(specification).toContain('remain planned');
  });

  test('publishes only the bilingual iOS overview, installation, and quickstart', async () => {
    const published = getIndexedNavigationEntries('en').map((entry) => entry.url);

    expect(getNavigationEntry('en', 'sdk', ['ios'])?.status).toBe('published');
    expect(getNavigationEntry('en', 'sdk', ['ios', 'installation'])?.status).toBe(
      'published',
    );
    expect(getNavigationEntry('en', 'sdk', ['ios', 'quickstart'])?.status).toBe(
      'published',
    );
    expect(published).toEqual(
      expect.arrayContaining([
        '/en/sdk/ios',
        '/en/sdk/ios/installation',
        '/en/sdk/ios/quickstart',
      ]),
    );

    for (const slug of ['platform-capabilities', 'api-reference', 'upgrade']) {
      expect(getNavigationEntry('en', 'sdk', ['ios', slug])?.status).toBe('planned');
      expect(published).not.toContain(`/en/sdk/ios/${slug}`);
    }

    for (const fileName of [
      'ios/index.mdx',
      'ios/index.en.mdx',
      'ios/installation.mdx',
      'ios/installation.en.mdx',
      'ios/quickstart.mdx',
      'ios/quickstart.en.mdx',
    ]) {
      expect(await Bun.file(new URL(fileName, docsRoot)).exists()).toBe(true);
    }
  });

  test('installs the exact distributed artifact without inventing SPM support', async () => {
    const pages = await Promise.all([
      sdkDoc('ios/installation.mdx'),
      sdkDoc('ios/installation.en.mdx'),
    ]);

    for (const page of pages) {
      expect(page).toContain("pod 'WuKongIMSDK', '1.1.1'");
      expect(page).toContain(snapshot.distribution);
      expect(page).toContain(snapshot.sourceRepository);
      expect(page).toContain(snapshot.sourceRevision);
      expect(page).toContain(snapshot.frameworkRepository);
      expect(page).toContain(snapshot.frameworkRevision);
      expect(page).toContain('.xcworkspace');
      expect(page).toContain('iOS `11.0`');
      expect(page).toContain('Package.swift');
      expect(page).toContain('Swift Package Manager');
      expect(page).toContain('x86_64');
      expect(page).toContain('arm64');
      expect(page).toContain('EXCLUDED_ARCHS[sdk=iphonesimulator*]');
      expect(page).toContain('WKSDK.sdkVersion');
      expect(page).toContain('CFBundleShortVersionString');
      expect(page).toContain('`1.0.0`');
      expect(page).toContain('Podfile.lock');
      expect(page).not.toMatch(/@latest|~>\s*1\.1|\^1\.1/u);
      expect(page).not.toContain('NSAllowsArbitraryLoads');
    }

    expect(pages[0]).toContain('不支持 Swift Package Manager');
    expect(pages[1]).toContain('does not support Swift Package Manager');
  });

  test('maps the quickstart to exact Objective-C public headers and lifecycle', async () => {
    const pages = await Promise.all([
      sdkDoc('ios/quickstart.mdx'),
      sdkDoc('ios/quickstart.en.mdx'),
    ]);
    const requiredPublicAPI = [
      'WKOptions',
      'WKConnectInfo',
      'WKSDK.shared.options = options',
      'options.isDebug = NO',
      'addDelegate:self',
      'onConnectStatus:(WKConnectStatus)status reasonCode:(WKReason)reasonCode',
      'WKConnected',
      'WK_REASON_SUCCESS',
      'WKTextContent',
      'initWithContent:',
      'personWithChannelID:',
      'sendMessage:content channel:channel',
      'onMessageUpdate:(WKMessage *)message left:(NSInteger)left',
      'onRecvMessages:(WKMessage *)message left:(NSInteger)left',
      'removeDelegate:self',
      'disconnect:YES',
      'logout',
    ];

    for (const page of pages) {
      for (const api of requiredPublicAPI) expect(page).toContain(api);
      expect(page).toContain('15.0');
      expect(page).toContain('self.connectionAccepted = YES');
      expect(page).toContain('[self.connectionTimeoutTimer invalidate]');
      expect(page).toContain('dispatch_sync(dispatch_get_main_queue()');
      expect(page).toContain('[NSThread isMainThread]');
      expect(page).toContain('attempt != self.connectionAttempt || self.connectionAccepted');
      expect(page).toContain('self.pendingClientMsgNo = message.clientMsgNo');
      expect(page).toContain(
        '![message.clientMsgNo isEqualToString:self.pendingClientMsgNo]',
      );
      expect(page).toContain('@property (nonatomic, strong) WKIMClient *client;');
      expect(page).toContain('- (void)endSession');
      expect(page).toContain('- (void)dealloc');
      expect(page).toContain('Alice');
      expect(page).toContain('Bob');
      expect(page).toContain('uid');
      expect(page).toContain('token');
      expect(page).toContain('host');
      expect(page).toContain('port');
      expect(page).not.toContain('WKSDK.shared.setup()');
      expect(page).not.toContain('connectAddr');
      expect(page).not.toContain('apiURL');
      expect(page).not.toContain('uploadURL');
    }

    expect(pages[0]).toMatch(/本地[^\n]{0,30}(?:待发送|pending)/u);
    expect(pages[1]).toMatch(/local[^\n]{0,30}pending/iu);
    expect(pages[0]).toContain('受信业务后端');
    expect(pages[1]).toContain('trusted product backend');
    expect(pages[0]).toContain('不能拿来做关联键');
    expect(pages[1]).toContain('must not be used as correlation keys');
    expect(pages[0]).toContain('离线');
    expect(pages[1]).toContain('offline');
  });

  test('makes non-TLS transport, payload logging, local-data, and proof blockers explicit', async () => {
    const pages = await Promise.all([
      sdkDoc('ios/index.mdx'),
      sdkDoc('ios/index.en.mdx'),
      sdkDoc('ios/installation.mdx'),
      sdkDoc('ios/installation.en.mdx'),
      sdkDoc('ios/quickstart.mdx'),
      sdkDoc('ios/quickstart.en.mdx'),
    ]);

    for (const page of pages) {
      expect(page).toContain(snapshot.version);
      expect(page).toContain('GCDAsyncSocket');
      expect(page).toMatch(/TCP/u);
      expect(page).toMatch(/TLS/u);
      expect(page).toContain('isDebug');
      expect(page).toMatch(/RECV/u);
      expect(page).toMatch(/Payload|payload/u);
      expect(page).toContain('SQLCipher');
      expect(page).toMatch(/UID/u);
    }

    for (const page of [pages[0], pages[2], pages[4]]) {
      expect(page).toContain('不是本站运行验证');
      expect(page).toContain('生产阻断项');
    }
    for (const page of [pages[1], pages[3], pages[5]]) {
      expect(page).toContain('not runtime verification');
      expect(page).toContain('production blocker');
    }
  });

  test('keeps SDK discovery and compatibility pages aligned with the iOS evidence state', async () => {
    const pages = await Promise.all([
      sdkDoc('index.mdx'),
      sdkDoc('index.en.mdx'),
      sdkDoc('choose-sdk.mdx'),
      sdkDoc('choose-sdk.en.mdx'),
      sdkDoc('compatibility.mdx'),
      sdkDoc('compatibility.en.mdx'),
    ]);

    for (const page of pages) {
      expect(page).toContain('/sdk/ios');
      expect(page).toContain('1.1.1');
      expect(page).toMatch(/iOS/u);
      expect(page).toMatch(/receipt/u);
    }

    expect(pages[0]).toContain('源码与公开头文件已校对');
    expect(pages[1]).toContain('source and public headers are aligned');
    expect(pages[2]).toContain(snapshot.sourceRevision);
    expect(pages[3]).toContain(snapshot.sourceRevision);
    expect(pages[4]).toContain('不属于本页 receipt');
    expect(pages[5]).toContain('is not covered by this receipt');
  });
});
