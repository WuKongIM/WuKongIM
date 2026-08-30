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
const previousSourceRevision = 'bb22a7659a7e5734d6dde5746aad71f85fb8ea59';

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

  test('publishes the complete bilingual iOS tutorial set', async () => {
    const published = getIndexedNavigationEntries('en').map((entry) => entry.url);
    const slugs = [
      '',
      'installation',
      'quickstart',
      'platform-capabilities',
      'api-reference',
      'upgrade',
    ];

    for (const slug of slugs) {
      const tail = slug === '' ? ['ios'] : ['ios', slug];
      expect(getNavigationEntry('en', 'sdk', tail)?.status).toBe('published');
    }
    expect(published).toEqual(
      expect.arrayContaining([
        '/en/sdk/ios',
        '/en/sdk/ios/installation',
        '/en/sdk/ios/quickstart',
        '/en/sdk/ios/platform-capabilities',
        '/en/sdk/ios/api-reference',
        '/en/sdk/ios/upgrade',
      ]),
    );

    for (const slug of slugs) {
      const stem = slug === '' ? 'index' : slug;
      for (const suffix of ['.mdx', '.en.mdx']) {
        expect(
          await Bun.file(new URL(`ios/${stem}${suffix}`, docsRoot)).exists(),
        ).toBe(true);
      }
    }
  });

  test('documents iOS capabilities without converting headers into runtime proof', async () => {
    const pages = await Promise.all([
      sdkDoc('ios/platform-capabilities.mdx'),
      sdkDoc('ios/platform-capabilities.en.mdx'),
    ]);

    for (const page of pages) {
      expect(page).toContain(snapshot.sourceRepository);
      expect(page).toContain(snapshot.sourceRevision);
      expect(page).toContain(snapshot.frameworkRepository);
      expect(page).toContain(snapshot.frameworkRevision);
      expect(page).toContain("pod 'WuKongIMSDK', '1.1.1'");
      expect(page).toContain('iOS `11.0`');
      expect(page).toContain('iOS `13.0`');
      expect(page).toMatch(/podspec.*(?:冲突|conflict)/iu);
      expect(page).toMatch(/不能.*最低|must not.*minimum/iu);
      expect(page).toContain('x86_64');
      expect(page).toContain('arm64');
      expect(page).toContain('Package.swift');
      expect(page).toContain('WKTextContent');
      expect(page).toContain('WKImageContent');
      expect(page).toContain('WKVoiceContent');
      expect(page).toContain('WKMultiMediaMessageContent');
      expect(page).toContain('registerMessageContent:');
      expect(page).toContain('personWithChannelID:');
      expect(page).toContain('groupWithChannelID:');
      expect(page).toContain('syncChannelMessageProvider');
      expect(page).toContain('uploadTaskProvider');
      expect(page).toContain('GCDAsyncSocket');
      expect(page).toContain('SQLCipher');
      expect(page).toContain('https://docs.githubim.com/zh/sdk/wukongim/ios/intro');
      expect(page).toContain(`blob/${snapshot.sourceRevision}`);
      expect(page).not.toMatch(/push (?:is|has been) verified|推送已验证/iu);
    }

    expect(pages[0]).toContain('旧站只用于学习顺序和主题覆盖');
    expect(pages[0]).toContain('不是本站运行验证');
    expect(pages[1]).toContain('legacy site is used only for learning order and topic coverage');
    expect(pages[1]).toContain('not runtime verification');
  });

  test('maps the iOS public manager, option, message, delegate, lifecycle, and error surface', async () => {
    const pages = await Promise.all([
      sdkDoc('ios/api-reference.mdx'),
      sdkDoc('ios/api-reference.en.mdx'),
    ]);
    const requiredAPI = [
      '[WKSDK shared]',
      'WKOptions',
      'WKConnectInfo',
      'connectInfoCallback',
      'hasLogin',
      'heartbeatInterval',
      'messageRetryInterval',
      'offlineMessageLimit',
      'protoVersion',
      'WKConnectionManager',
      'WKChatManager',
      'WKChannelManager',
      'WKConversationManager',
      'WKMediaManager',
      'WKReceiptManager',
      'WKReactionManager',
      'WKRobotManager',
      'WKPinnedMessageManager',
      'WKReminderManager',
      'WKCMDManager',
      'connect',
      'disconnect:',
      'logout',
      'addDelegate:',
      'removeDelegate:',
      'onConnectStatus:reasonCode:',
      'onKick:reason:',
      'WKConnectStatus',
      'WKMessage',
      'WKMessageContent',
      'WKTextContent',
      'WKChannel',
      'sendMessage:channel:',
      'resendMessage:',
      'onRecvMessages:left:',
      'onMessageUpdate:left:',
      'onSendack:left:',
      'clientMsgNo',
      'clientSeq',
      'messageId',
      'messageSeq',
      'WK_MESSAGE_WAITSEND',
      'WK_MESSAGE_SUCCESS',
      'WK_MESSAGE_FAIL',
      'WKReason',
      'WK_REASON_SUCCESS',
      'WK_REASON_AUTHFAIL',
      'WK_REASON_IN_BLACKLIST',
      'WK_REASON_KICK',
      'WK_REASON_NOT_IN_WHITELIST',
      'NSError',
      'fetchChannelInfo:completion:',
      'getMembersWithChannel:limit:',
      'getMember:uid:',
      'addOrUpdateMembers:',
      'deleteMembers:',
      'getConversationList',
      'getConversation:',
      'deleteConversation:',
      'clearConversationUnreadCount:',
      'getAllConversationUnreadCount',
      'setSyncConversationProviderAndAck:ack:',
      'upload:',
      'download:callback:',
      'addOrCancelReaction:messageID:complete:',
      'getPinnedMessagesByChannel:',
      'pullCMDMessages',
      'syncWithUsernames:complete:',
    ];

    for (const page of pages) {
      for (const api of requiredAPI) expect(page).toContain(api);
      expect(page).toContain(snapshot.sourceRevision);
      expect(page).toContain(`blob/${snapshot.sourceRevision}`);
      expect(page).toContain('Product HTTP');
      expect(page).toContain('GCDAsyncSocket');
      expect(page).toContain('RECV');
      expect(page).toContain('compatibility.json');
      expect(page).toContain('app-facing');
      expect(page).toContain('public-but-not-recommended');
      expect(page).toContain('PrivateHeaders');
      expect(page).toContain('WKDB.h');
      expect(page).toContain('WKMessageQueueManager.h');
      expect(page).toMatch(/PrivateHeaders.*(?:不进入|excluded)/iu);
      expect(page).toContain(
        `tree/${snapshot.frameworkRevision}/ios/WuKongIMSDK.framework/Headers`,
      );
      expect(page).not.toContain('WKSDK.shared.setup()');
      expect(page).not.toContain('connectAddr');
    }
  });

  test('defines an evidence-backed iOS upgrade and rollback boundary', async () => {
    const pages = await Promise.all([
      sdkDoc('ios/upgrade.mdx'),
      sdkDoc('ios/upgrade.en.mdx'),
    ]);

    for (const page of pages) {
      expect(page).toContain("pod 'WuKongIMSDK', '1.1.1'");
      expect(page).toContain(snapshot.sourceRevision);
      expect(page).toContain(snapshot.frameworkRevision);
      expect(page).toContain('Podfile.lock');
      expect(page).toContain('WKSDK.sdkVersion');
      expect(page).toContain('CFBundleShortVersionString');
      expect(page).toContain('`1.0.0`');
      expect(page).toContain('`1.1.0`');
      expect(page).toContain('iOS `11.0`');
      expect(page).toContain('iOS `13.0`');
      expect(page).toMatch(/podspec.*(?:冲突|conflict)/iu);
      expect(page).not.toContain("\nplatform :ios, '11.0'\n");
      expect(page).toContain('Package.swift');
      expect(page).toContain('WKDBMigrationManager');
      expect(page).toContain('sendMessage:channel:');
      expect(page).toContain('onMessageUpdate:left:');
      expect(page).toContain('compatibility.json');
      expect(page).toContain('https://github.com/WuKongIM/WuKongIMiOSSDK/tags');
      expect(page).toContain(
        'https://github.com/WuKongIM/WuKongIMiOSSDK/compare/1.1.0...1.1.1',
      );
      expect(page).toContain(previousSourceRevision);
      expect(page).toContain('filterNoCMDAndNoStreamMessages');
      expect(page).toContain('isDeleted != 0');
      expect(page).toContain('onRecvMessages:left:');
      expect(page).toMatch(/public headers.*unchanged|公开头文件.*未变/iu);
      expect(page).toMatch(/接收 delegate|receive delegate/iu);
      expect(page).toMatch(/额外探索性覆盖|additional exploratory coverage/iu);
      expect(page).not.toMatch(
        /可能重新出现在同步后的返回列表|may therefore reappear in lists returned after synchronization/iu,
      );
      expect(page).toContain(`blob/${snapshot.sourceRevision}`);
      expect(page).not.toMatch(/1\.1\.1 (?:fixes|adds|修复|新增)/u);
    }

    expect(pages[0]).toContain('没有发布说明就不推断变更');
    expect(pages[0]).toContain('没有公开的降级契约');
    expect(pages[0]).toContain('不是本站运行验证');
    expect(pages[1]).toContain('do not infer changes without release notes');
    expect(pages[1]).toContain('no public downgrade contract');
    expect(pages[1]).toContain('not runtime verification');
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
      expect(page).toContain('iOS `13.0`');
      expect(page).toMatch(/podspec.*(?:冲突|conflict)/iu);
      expect(page).not.toContain("\nplatform :ios, '11.0'\n");
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

    for (const page of pages.slice(0, 4)) {
      expect(page).toContain('iOS `11.0`');
      expect(page).toContain('iOS `13.0`');
      expect(page).toMatch(/podspec.*(?:冲突|conflict)/iu);
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
