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

  test('publishes only the bilingual HarmonyOS overview, installation, and quickstart', async () => {
    const published = getIndexedNavigationEntries('en').map((entry) => entry.url);

    expect(published).toEqual(
      expect.arrayContaining([
        '/en/sdk/harmonyos',
        '/en/sdk/harmonyos/installation',
        '/en/sdk/harmonyos/quickstart',
      ]),
    );
    for (const slug of ['platform-capabilities', 'api-reference', 'upgrade']) {
      expect(getNavigationEntry('en', 'sdk', ['harmonyos', slug])?.status).toBe(
        'planned',
      );
    }

    for (const route of [
      'harmonyos/index.mdx',
      'harmonyos/index.en.mdx',
      'harmonyos/installation.mdx',
      'harmonyos/installation.en.mdx',
      'harmonyos/quickstart.mdx',
      'harmonyos/quickstart.en.mdx',
    ]) {
      const page = await sdkDoc(route);
      expect(page).toContain(snapshot.version);
      expect(page).toContain(snapshot.package);
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
