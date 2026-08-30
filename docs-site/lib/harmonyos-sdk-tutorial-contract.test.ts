import { describe, expect, test } from 'bun:test';
import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';

import {
  getIndexedNavigationEntries,
  getNavigationEntry,
} from './navigation';

const docsRoot = join(import.meta.dir, '..', 'content', 'docs', 'sdk');
const phaseSpec = join(import.meta.dir, '..', 'PHASE_22_SPEC.md');
const acceptanceExample = join(
  import.meta.dir,
  '..',
  'public',
  'examples',
  'harmonyos',
  'WKAcceptanceSession.ets',
);

const snapshot = {
  package: '@wukong/wkim',
  version: '1.1.7',
  revision: '0c41810a1e0a5fc2936929d63ca32a50ffb11bec',
  archiveSha:
    'd98d1523bc60ad204dd74d9cfa776935a5547fc3ab352322dfa17f5dbc7a3cd8',
  integrity:
    'sha512-864btKpDkxGQ9ACUGur6LJ7gIsmFGDub6WdY+znWQTXFjyNoJziiaGby/7ZE9owwvHRwE10E4V9ZMfU0ZO2DFA==',
  api: '20',
  exampleSha:
    '589554efcb4667ba41930358a7708d828900d70d4090abb2082ea95e810c37f1',
};

async function sdkDoc(path: string): Promise<string> {
  return readFile(join(docsRoot, path), 'utf8');
}

describe('full HarmonyOS SDK tutorial contract', () => {
  test('pins the exact HAR, matching source, and evidence boundary', async () => {
    const spec = await readFile(phaseSpec, 'utf8');

    for (const value of Object.values(snapshot)) expect(spec).toContain(value);
    expect(spec).toContain('6.1.1.125');
    expect(spec).toContain('42505190601967d6a9fc8f321692689917b13a91');
    expect(spec).toMatch(/no git\s+tags|没有 git tag/iu);
    expect(spec).toMatch(/not compiled|未编译/iu);
    expect(spec).toMatch(/template assertion|模板断言/iu);
  });

  test('publishes the complete bilingual HarmonyOS documentation path', async () => {
    const published = getIndexedNavigationEntries('en').map((entry) => entry.url);

    expect(published).toEqual(
      expect.arrayContaining([
        '/en/sdk/harmonyos',
        '/en/sdk/harmonyos/installation',
        '/en/sdk/harmonyos/quickstart',
        '/en/sdk/harmonyos/platform-capabilities',
        '/en/sdk/harmonyos/api-reference',
        '/en/sdk/harmonyos/upgrade',
      ]),
    );
    for (const slug of ['platform-capabilities', 'api-reference', 'upgrade']) {
      expect(getNavigationEntry('en', 'sdk', ['harmonyos', slug])?.status).toBe(
        'published',
      );
    }

    for (const route of [
      'harmonyos/index.mdx',
      'harmonyos/index.en.mdx',
      'harmonyos/installation.mdx',
      'harmonyos/installation.en.mdx',
      'harmonyos/quickstart.mdx',
      'harmonyos/quickstart.en.mdx',
      'harmonyos/platform-capabilities.mdx',
      'harmonyos/platform-capabilities.en.mdx',
      'harmonyos/api-reference.mdx',
      'harmonyos/api-reference.en.mdx',
      'harmonyos/upgrade.mdx',
      'harmonyos/upgrade.en.mdx',
    ]) {
      const page = await sdkDoc(route);
      expect(page).toContain(snapshot.version);
      expect(page).toContain(snapshot.package);
    }
  });

  test('documents source-aligned HarmonyOS capabilities without inventing a device receipt', async () => {
    const pages = await Promise.all([
      sdkDoc('harmonyos/platform-capabilities.mdx'),
      sdkDoc('harmonyos/platform-capabilities.en.mdx'),
    ]);
    const required = [
      snapshot.package,
      snapshot.version,
      snapshot.revision,
      snapshot.archiveSha,
      snapshot.integrity,
      'compatibleSdkVersion',
      'default',
      'tablet',
      'ohos.permission.INTERNET',
      'ohos.permission.GET_NETWORK_INFO',
      'TCPSocket',
      'RelationalStore',
      'encrypt: false',
      'WKTextContent',
      'WKImageContent',
      'WKVoiceContent',
      'WKVideoContent',
      'ChannelMemberManager',
      'ConversationManager',
      'ReminderManager',
      'CMDManager',
      'sendingMsgMap',
    ];
    for (const page of pages) {
      for (const value of required) expect(page).toContain(value);
      expect(page).toMatch(/根.*只导出.*WKIM|root.*exports only.*WKIM/iu);
      expect(page).toMatch(/深路径|deep import/iu);
      expect(page).toMatch(/没有.*运行 receipt|no.*runtime receipt/iu);
      expect(page).toMatch(/没有.*编译|not.*compiled/iu);
    }
  });

  test('maps the HarmonyOS API reference to the exact root export, providers, listeners, and results', async () => {
    const pages = await Promise.all([
      sdkDoc('harmonyos/api-reference.mdx'),
      sdkDoc('harmonyos/api-reference.en.mdx'),
    ]);
    const required = [
      snapshot.package,
      snapshot.version,
      snapshot.revision,
      "from '@wukong/wkim'",
      'WKIM.shared.init',
      'channelManager()',
      'channelMemberManager()',
      'messageManager()',
      'cmdManager()',
      'connectionManager()',
      'conversationManager()',
      'reminderManager()',
      'WKProvider',
      'connectAddrCallback',
      'syncConversationCallback',
      'syncMessageCallback',
      'uploadAttachmentCallback',
      'addConnectStatusListener',
      'removeConnectStatusListener',
      'disConnection(isLogout: boolean)',
      'sendWithOption',
      'getMaxOrderSeqWithChannel',
      'getMaxMessageSeqWithChannel',
      'getMinMessageSeqWithChannel',
      'getMaxReactionSeqWithChannel',
      'getMessageOrderSeq',
      'updateContent',
      'updateViewedAt',
      'updateLocalExtra',
      'updateEdit',
      'addInsertedListener',
      'addSendStatusListener',
      'addNewMsgListener',
      'WKSendMsgResult.success',
      'WKConnectStatus.syncCompleted',
      'clientMsgNo',
      'clientSeq',
      'updateMsgExtra',
      'getMsgExtraWithChannel',
      'syncExtra',
      'addRefreshExtrasListener',
      'updateName',
      'updateRemark',
      'getWithPageOrSearch',
      'getMaxVersion',
      'addCmdListener',
      'setRefresh',
      'setRefreshExtras',
      'setDeleted',
      'setSendStatus',
      'setRefreshReactions',
      'pushNewMsgs',
      'generateClientMsgNo',
      'parsingMsg',
      'updateMsgSendStatus',
    ];
    for (const page of pages) {
      for (const value of required) expect(page).toContain(value);
      expect(page).toMatch(/根.*只导出.*WKIM|root.*exports only.*WKIM/iu);
      expect(page).toMatch(
        /这些路径.*artifact.*不是稳定 root API|Those are.*artifact facts, not a stable root API/iu,
      );
      expect(page).toMatch(
        /源码公开但不推荐由应用调用的方法|Source-public methods that applications should not call directly/iu,
      );
      expect(page).toMatch(
        /manager deep path.*不是稳定 package-root 产品合同|manager deep-path artifact facts, not stable package-root product contracts/iu,
      );
      expect(page).not.toMatch(
        /import\s*\{[^}]*\b(?:MessageManager|ConversationManager|WKChannel)\b[^}]*\}\s*from\s*['"]@wukong\/wkim['"]/u,
      );
      expect(page).toMatch(/相同.*函数对象|same function object/iu);
      expect(page).toMatch(/单槽位|single slot/iu);
      expect(page).toMatch(/不是.*运行验证|not.*runtime verification/iu);
    }
  });

  test('upgrades to HarmonyOS 1.1.7 through a pinned HAR, deep-import audit, and database rollback boundary', async () => {
    const pages = await Promise.all([
      sdkDoc('harmonyos/upgrade.mdx'),
      sdkDoc('harmonyos/upgrade.en.mdx'),
    ]);
    const comparison =
      'https://github.com/WuKongIM/WuKongIMHarmonyOSSDK/compare/a79df83f2794c581096850f0f77d34b95566a9ae...0c41810a1e0a5fc2936929d63ca32a50ffb11bec';
    for (const page of pages) {
      expect(page).toContain(`"${snapshot.package}": "${snapshot.version}"`);
      expect(page).toContain(snapshot.archiveSha);
      expect(page).toContain(snapshot.integrity);
      expect(page).toContain(snapshot.revision);
      expect(page).toContain('oh-package-lock.json5');
      expect(page).toContain('ohpm install');
      expect(page).toContain('ohpm list @wukong/wkim@1.1.7');
      expect(page).toContain('getWithFollowAndStatus');
      expect(page).toContain('getMaxReactionSeqWithChannel');
      expect(page).toContain('a79df83f2794c581096850f0f77d34b95566a9ae');
      expect(page).toContain(comparison);
      expect(page).toContain('getMinMessageSeqWithChannel');
      expect(page).toContain('getMessageOrderSeq');
      expect(page).toContain('updateMsgExtra');
      expect(page).toContain('getWithChannel');
      expect(page).toContain('getMsgExtraWithChannel');
      expect(page).toContain('updateSendingToFail');
      expect(page).toContain('1.1.2');
      expect(page).toContain('1.1.7');
      expect(page).toMatch(/没有.*tag|no.*tag/iu);
      expect(page).toMatch(/深路径|deep import/iu);
      expect(page).toMatch(/数据库.*快照|database.*snapshot/iu);
      expect(page).toMatch(/降级契约|downgrade contract/iu);
      expect(page).toMatch(/进程重启|process restart/iu);
      expect(page).toMatch(/运行 receipt|runtime receipt/u);

      const diffStart = page.indexOf('## 3.');
      const diffEnd = page.indexOf('## 4.');
      expect(diffStart).toBeGreaterThanOrEqual(0);
      expect(diffEnd).toBeGreaterThan(diffStart);
      const exactDiff = page.slice(diffStart, diffEnd);
      expect(exactDiff).toContain(comparison);
      expect(exactDiff).toMatch(/1\.1\.6[\s\S]*1\.1\.7/u);
      expect(exactDiff).toMatch(
        /连接停滞恢复.*attempt\/timer|stalled connection recovery.*attempt\/timer/iu,
      );
      expect(exactDiff).toMatch(
        /路由超时[\s\S]*陈旧地址[\s\S]*重复 reconnect|route timeout[\s\S]*stale addresses[\s\S]*duplicate reconnect/iu,
      );
      expect(exactDiff).toMatch(
        /同步消息持久化调整[\s\S]*clientMsgNo[\s\S]*reaction|Changes synchronized-message persistence[\s\S]*clientMsgNo[\s\S]*reactions/iu,
      );
      expect(exactDiff).toMatch(
        /SENDACK 本地更新调整[\s\S]*messageId[\s\S]*messageSeq[\s\S]*orderSeq[\s\S]*clientSeq|Changes local SENDACK update[\s\S]*messageId[\s\S]*messageSeq[\s\S]*orderSeq[\s\S]*clientSeq/iu,
      );
      expect(exactDiff).toMatch(
        /没有移除旧调用签名的证据|no source evidence.*remove an old call signature/iu,
      );
    }
  });

  test('pins the exact OHPM dependency, lockfile, permissions, and package boundary', async () => {
    const pages = await Promise.all([
      sdkDoc('harmonyos/installation.mdx'),
      sdkDoc('harmonyos/installation.en.mdx'),
    ]);

    for (const page of pages) {
      expect(page).toContain(`"${snapshot.package}": "${snapshot.version}"`);
      expect(page).toContain('oh-package-lock.json5');
      expect(page).toContain(snapshot.archiveSha);
      expect(page).toContain(snapshot.integrity);
      expect(page).toContain(snapshot.revision);
      expect(page).toContain('compatibleSdkVersion');
      expect(page).toContain('ohos.permission.INTERNET');
      expect(page).toContain('ohos.permission.GET_NETWORK_INFO');
      expect(page).toContain('obfuscated: false');
      expect(page).toContain('index.ets');
      expect(page).toMatch(/深路径|deep import/iu);
    }
  });

  test('maps the quickstart to the shipped ArkTS API and exact readiness gate', async () => {
    const pages = await Promise.all([
      sdkDoc('harmonyos/quickstart.mdx'),
      sdkDoc('harmonyos/quickstart.en.mdx'),
    ]);
    const example = await readFile(acceptanceExample, 'utf8');
    const exampleSha = createHash('sha256').update(example).digest('hex');

    expect(exampleSha).toBe(snapshot.exampleSha);

    for (const page of pages) {
      expect(page).toContain('/examples/harmonyos/WKAcceptanceSession.ets');
      expect(page).toContain(snapshot.exampleSha);
      expect(page).toMatch(/connecting.*success.*syncing.*syncCompleted/su);
      expect(page).toMatch(/全新测试账号|brand-new test accounts/u);
      expect(page).toMatch(/返回 `void`|returns `void`/u);
      expect(page).toContain('15');
      expect(page).toContain('Alice');
      expect(page).toContain('Bob');
    }

    const requiredApi = [
      "from '@wukong/wkim'",
      "@wukong/wkim/src/main/ets/entity/Bean",
      "@wukong/wkim/src/main/ets/model/WKTextContent",
      "@wukong/wkim/src/main/ets/common/WKLogger",
      'WKLogger.setShowLog(false)',
      'WKIM.shared.init',
      'deviceFlagApp = 0',
      'syncConversationCallback',
      'addConnectStatusListener',
      'WKConnectStatus.connecting',
      'WKConnectStatus.success',
      'WKConnectStatus.syncing',
      'WKConnectStatus.syncCompleted',
      'sendWithOption',
      'WKTextContent',
      'WKChannelType.personal',
      'addInsertedListener',
      'addSendStatusListener',
      'addNewMsgListener',
      'removeConnectStatusListener',
      'removeSendStatusListener',
      'removeNewMsgListener',
      'WKSendMsgResult.success',
      'clientSeq',
      'requireProcessRestart',
    ];
    for (const api of requiredApi) expect(example).toContain(api);
  });

  test('keeps sender and receiver teardown safe at the terminal boundary', async () => {
    const pages = await Promise.all([
      sdkDoc('harmonyos/quickstart.mdx'),
      sdkDoc('harmonyos/quickstart.en.mdx'),
    ]);
    const example = await readFile(acceptanceExample, 'utf8');

    expect(example).toContain('private successfulAckSeen = false');
    expect(example).toContain('private onlineMessageSeen = false');
    expect(example).toContain('let processActivationClaimed = false');
    expect(example).toContain('private sendMayBeUnresolved = false');
    expect(example).toContain('onLateSendTerminal');
    expect(example).toContain('closeAfterTerminalAck');
    expect(example).toContain('closeReceiverAfterVerifiedOnlineReceipt');
    expect(example).toMatch(
      /if \(this\.expectingInsert \|\| this\.sendMayBeUnresolved \|\|[\s\S]*requireProcessRestart[\s\S]*return/u,
    );
    expect(example).toMatch(
      /if \(msg\.clientSeq <= 0\)[\s\S]*return[\s\S]*observer\.onLocalInsert/u,
    );
    expect(example).toMatch(
      /try \{[\s\S]*observer\.onSendRejected[\s\S]*\} finally \{[\s\S]*terminate/u,
    );
    expect(example).toMatch(
      /try \{[\s\S]*observer\.onConnectionStatus[\s\S]*\} catch \(error\) \{[\s\S]*terminate/u,
    );

    for (const page of pages) {
      expect(page).toContain('closeAfterTerminalAck');
      expect(page).toContain('closeReceiverAfterVerifiedOnlineReceipt');
      expect(page).toMatch(/late.*SENDACK|迟到.*SENDACK/iu);
      expect(page).toMatch(/clientSeq.*0|`0`.*clientSeq/iu);
    }
  });

  test('publishes source-proven transport, logging, storage, queue, and proof blockers', async () => {
    const pages = await Promise.all([
      sdkDoc('harmonyos/index.mdx'),
      sdkDoc('harmonyos/index.en.mdx'),
      sdkDoc('harmonyos/installation.mdx'),
      sdkDoc('harmonyos/installation.en.mdx'),
      sdkDoc('harmonyos/quickstart.mdx'),
      sdkDoc('harmonyos/quickstart.en.mdx'),
    ]);

    for (const page of pages) {
      expect(page).toContain('TCPSocket');
      expect(page).toMatch(/原始.*TCP|raw.*TCP/u);
      expect(page).toContain('WKLogger');
      expect(page).toContain('hilog.info');
      expect(page).toContain('encrypt: false');
      expect(page).toContain('sendingMsgMap');
      expect(page).toMatch(/进程重启|process restart|restart the process/u);
      expect(page).toMatch(/运行 receipt|runtime receipt/u);
    }
  });

  test('keeps discovery and compatibility pages aligned with HarmonyOS evidence', async () => {
    const pages = await Promise.all([
      sdkDoc('index.mdx'),
      sdkDoc('index.en.mdx'),
      sdkDoc('choose-sdk.mdx'),
      sdkDoc('choose-sdk.en.mdx'),
      sdkDoc('compatibility.mdx'),
      sdkDoc('compatibility.en.mdx'),
    ]);

    for (const page of pages) {
      expect(page).toContain(snapshot.version);
      expect(page).toMatch(/\/zh\/sdk\/harmonyos|\/en\/sdk\/harmonyos/u);
      expect(page).toMatch(/没有.*运行 receipt|no.*runtime receipt/u);
    }

    const choosers = await Promise.all([
      sdkDoc('choose-sdk.mdx'),
      sdkDoc('choose-sdk.en.mdx'),
    ]);
    expect(choosers[0]).toContain('tags 或 releases（如果仓库提供）');
    expect(choosers[1]).toContain('tags or releases when the repository provides them');
  });
});
