import { describe, expect, test } from 'bun:test';
import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';

import {
  getIndexedNavigationEntries,
  getNavigationEntry,
} from './navigation';

const docsRoot = join(import.meta.dir, '..', 'content', 'docs', 'sdk');
const phaseSpec = join(import.meta.dir, '..', 'PHASE_21_SPEC.md');
const projectKnowledge = join(
  import.meta.dir,
  '..',
  '..',
  'docs',
  'development',
  'PROJECT_KNOWLEDGE.md',
);
const acceptanceExample = join(
  import.meta.dir,
  '..',
  'public',
  'examples',
  'flutter',
  'wk_acceptance.dart',
);
const acceptanceExampleSha =
  'b0a0b4aef4ddf2d77b9f928931ea6b60ab5cf0d53dd82d4b1abbec1c79a920e6';

const snapshot = {
  package: 'wukongimfluttersdk',
  version: '1.7.9',
  revision: 'de1024276523119e38305c49a3a873caae4d5c59',
  archiveSha:
    'b6191a86cd1e4caacaa4652e95709310eb1493f159fee65e1dd53c2a3ff9e80a',
  flutter: '3.41.4',
  dart: '3.11.1',
};

async function sdkDoc(path: string): Promise<string> {
  return readFile(join(docsRoot, path), 'utf8');
}

describe('full Flutter SDK tutorial contract', () => {
  test('pins the exact package, source payload, and evidence boundary', async () => {
    const spec = await readFile(phaseSpec, 'utf8');

    for (const value of Object.values(snapshot)) expect(spec).toContain(value);
    expect(spec).toContain('93');
    expect(spec).toContain('RangeError');
    expect(spec).toContain('macOS Release');
    expect(spec).toMatch(/macOS.*`2=PC`/u);
    expect(spec).not.toMatch(/other desktop targets/u);
    expect(spec).toMatch(/No Alice\/Bob server scenario|没有运行 Alice\/Bob/u);
    expect(spec).toMatch(/does not tag `1\.7\.9`|没有.*`1\.7\.9`.*tag/u);
  });

  test('publishes only the bilingual Flutter overview, installation, and quickstart', async () => {
    const published = getIndexedNavigationEntries('en').map((entry) => entry.url);

    expect(published).toEqual(
      expect.arrayContaining([
        '/en/sdk/flutter',
        '/en/sdk/flutter/installation',
        '/en/sdk/flutter/quickstart',
      ]),
    );
    for (const slug of ['platform-capabilities', 'api-reference', 'upgrade']) {
      expect(getNavigationEntry('en', 'sdk', ['flutter', slug])?.status).toBe(
        'planned',
      );
    }

    for (const route of [
      'flutter/index.mdx',
      'flutter/index.en.mdx',
      'flutter/installation.mdx',
      'flutter/installation.en.mdx',
      'flutter/quickstart.mdx',
      'flutter/quickstart.en.mdx',
    ]) {
      const page = await sdkDoc(route);
      expect(page).toContain(snapshot.version);
    }
  });

  test('installs the exact hosted archive and enforces the application lockfile', async () => {
    const pages = await Promise.all([
      sdkDoc('flutter/installation.mdx'),
      sdkDoc('flutter/installation.en.mdx'),
    ]);

    const dependencies = [
      'path',
      'encrypt',
      'cupertino_icons',
      'x25519',
      'hex',
      'crypto',
      'uuid',
      'dio',
      'shared_preferences',
      'sqflite',
      'connectivity_plus',
    ];
    for (const page of pages) {
      expect(page).toContain(`${snapshot.package}: ${snapshot.version}`);
      expect(page).not.toMatch(
        /^\s{2}wukongimfluttersdk: \^1\.7\.9$/mu,
      );
      expect(page).toContain(snapshot.archiveSha);
      expect(page).toContain(snapshot.revision);
      expect(page).toContain('pubspec.lock');
      expect(page).toContain('flutter pub get --enforce-lockfile');
      expect(page).toContain('>=2.17.0 <4.0.0');
      for (const dependency of dependencies) expect(page).toContain(dependency);
    }
  });

  test('maps the quickstart to the exact Dart API and synchronization lifecycle', async () => {
    const pages = await Promise.all([
      sdkDoc('flutter/quickstart.mdx'),
      sdkDoc('flutter/quickstart.en.mdx'),
    ]);
    const example = await readFile(acceptanceExample, 'utf8');
    expect(createHash('sha256').update(example).digest('hex')).toBe(
      acceptanceExampleSha,
    );
    const requiredApi = [
      'WKIM.shared.setup',
      'Options.newDefault',
      '..debug = false',
      'required this.deviceFlag',
      'final int deviceFlag',
      'next.deviceFlag != 0 && next.deviceFlag != 2',
      '..deviceFlag = next.deviceFlag',
      'addOnSyncConversationListener',
      'WKSyncConversation',
      'addOnConnectionStatus',
      'WKConnectStatus.connecting',
      'WKConnectStatus.success',
      'WKConnectStatus.syncMsg',
      'WKConnectStatus.syncCompleted',
      'WKConnectStatus.kicked',
      'WKConnectStatus.noNetwork',
      'sendWithOption',
      'WKTextContent',
      'WKChannelType.personal',
      'addOnMsgInsertedListener',
      'addOnRefreshMsgListener',
      'addOnNewMsgListener',
      'removeOnConnectionStatus',
      'removeOnRefreshMsgListener',
      'removeNewMsgListener',
      'clientMsgNO',
      'clientSeq',
      'WKSendMsgResult.sendSuccess',
      'connectionEpoch',
      'requireProcessRestart',
    ];

    for (const api of requiredApi) expect(example).toContain(api);
    expect(example.indexOf('_sendTimer = Timer(sendTimeout')).toBeLessThan(
      example.indexOf('_observer.onLocalInsert(inserted)'),
    );
    for (const page of pages) {
      expect(page).toContain('/examples/flutter/wk_acceptance.dart');
      expect(page).toContain(acceptanceExampleSha);
      expect(page).toContain('WidgetsFlutterBinding.ensureInitialized()');
      expect(page).toContain('WKIM.shared.setup');
      expect(page).toContain('sendWithOption');
      expect(page).toContain('15');
      expect(page).toContain('Alice');
      expect(page).toContain('Bob');
      expect(page).toMatch(/fail\(null\).*connecting.*success.*syncMsg.*syncCompleted/su);
      expect(page).toMatch(/全新测试账号|brand-new test accounts/u);
      expect(page).toMatch(/不等待|does not await/u);
      expect(page).toMatch(/Android\/iOS.*`0`|Android\/iOS.*0=APP/u);
      expect(page).toMatch(/macOS.*`2`|desktop.*2=PC/u);
      expect(page).not.toMatch(/其他桌面应用|other desktop applications/u);
      expect(page).not.toMatch(
        /移动\/桌面.*使用 `0`|mobile\/desktop.*uses `0`/u,
      );
      expect(page).not.toMatch(/syncCompleted\s*(?:→|->)\s*success/u);
    }
  });

  test('publishes transport, storage, logging, retry, parser, and proof blockers', async () => {
    const pages = await Promise.all([
      sdkDoc('flutter/index.mdx'),
      sdkDoc('flutter/index.en.mdx'),
      sdkDoc('flutter/installation.mdx'),
      sdkDoc('flutter/installation.en.mdx'),
      sdkDoc('flutter/quickstart.mdx'),
      sdkDoc('flutter/quickstart.en.mdx'),
    ]);

    for (const page of pages) {
      expect(page).toContain('Socket.connect');
      expect(page).toMatch(/原始.*TCP|raw.*TCP/u);
      expect(page).toContain('sqflite');
      expect(page).toContain('SharedPreferences');
      expect(page).toContain('Payload');
      expect(page).toContain('_sendingMsgMap');
      expect(page).toMatch(/进程重启|process restart/u);
      expect(page).toMatch(/运行 receipt|runtime receipt/u);
    }
  });

  test('keeps discovery and compatibility pages aligned with Flutter evidence', async () => {
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
      expect(page).toMatch(/\/zh\/sdk\/flutter|\/en\/sdk\/flutter/u);
      expect(page).toMatch(/没有.*运行 receipt|no.*runtime receipt/u);
    }

    const knowledge = await readFile(projectKnowledge, 'utf8');
    expect(knowledge).toMatch(/macOS.*`2=PC`/u);
    expect(knowledge).not.toMatch(/`2=PC` for desktop targets/u);
  });
});
