import { describe, expect, test } from 'bun:test';
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';

import {
  getIndexedNavigationEntries,
  getNavigationEntry,
} from './navigation';

const docsRoot = join(import.meta.dir, '..', 'content', 'docs', 'sdk');
const phaseSpec = join(import.meta.dir, '..', 'PHASE_23_SPEC.md');
const projectKnowledge = join(
  import.meta.dir,
  '..',
  '..',
  'docs',
  'development',
  'PROJECT_KNOWLEDGE.md',
);

const snapshot = {
  retiredRevision: '582cacb5ed7a559b66ed4f66fe71dd1a3608e21d',
  retirementRevision: '88da7bff68046bd4f2299b511e0dcb91a705c8de',
  retiredPackage: 'wukongimuniappsdk',
  retiredVersion: '1.0.3',
  retiredArchiveSha:
    'a2dfcb7a2317ea6f123ac4fbd8f92a2ecee6f48eaa10d6629e77abc1a1540db7',
  retiredIntegrity:
    'sha512-3IYWWKqRAVloLn7MVkoPJO0diF16UIPWxnLgO8/SaqTE06dxUkynVD0kxRoOzsnBM4UX+su5IoHpxWYH5wEwWA==',
  targetRevision: '3c507ea3ebc08eae9d74fc1f76b150c380752008',
  targetPackage: 'wukongimjssdk',
  targetVersion: '1.3.5',
  targetArchiveSha:
    'b053c9623ac36b7ce78dfd874240ac48abaee48e20dd78d824f28881c5504cfc',
  targetIntegrity:
    'sha512-Y3RY4IdkLfCB2MCJFQlamSe5EQ6SU3PGphdoV9MJjJTSUAzZTTw5gBxmMi2jbwLRDqM+cSFaIb1vhQ+Rl0ftnQ==',
};

async function sdkDoc(path: string): Promise<string> {
  return readFile(join(docsRoot, path), 'utf8');
}

describe('UniApp retirement and JSSDK migration contract', () => {
  test('pins the deprecated repository, stale package artifact, and target JSSDK', async () => {
    const spec = await readFile(phaseSpec, 'utf8');

    for (const value of Object.values(snapshot)) expect(spec).toContain(value);
    expect(spec).toContain('no git tags');
    expect(spec).toContain('2023-06-26');
    expect(spec).toContain('2023-07-13');
    expect(spec).toContain('no `deprecated` field');
    expect(spec).toContain(`"${snapshot.retiredPackage}": "^1.0.1"`);
    expect(spec).toContain('wx.connectSocket');
    expect(spec).toContain('config.debug = false');
    expect(spec).toContain('WKSDK.shared().register');
    expect(spec).toContain('second lockfile');
    expect(spec).toContain('exit code `1`');
    expect(spec).toContain('stale adapter');
  });

  test('publishes only the bilingual retirement overview and migration route', async () => {
    const published = getIndexedNavigationEntries('en').map((entry) => entry.url);

    expect(published).toEqual(
      expect.arrayContaining([
        '/en/sdk/uniapp',
        '/en/sdk/uniapp/migrate-to-jssdk',
      ]),
    );
    expect(getNavigationEntry('en', 'sdk', ['uniapp'])?.status).toBe('published');
    expect(
      getNavigationEntry('en', 'sdk', ['uniapp', 'migrate-to-jssdk'])?.status,
    ).toBe('published');
    for (const slug of [
      'installation',
      'quickstart',
      'platform-capabilities',
      'api-reference',
      'upgrade',
    ]) {
      expect(getNavigationEntry('en', 'sdk', ['uniapp', slug])).toBeUndefined();
    }

    for (const route of [
      'uniapp/index.mdx',
      'uniapp/index.en.mdx',
      'uniapp/migrate-to-jssdk.mdx',
      'uniapp/migrate-to-jssdk.en.mdx',
    ]) {
      const page = await sdkDoc(route);
      expect(page).toContain(snapshot.retiredPackage);
      expect(page).toContain(snapshot.targetPackage);
      expect(page).toMatch(/弃用|deprecated/iu);
      expect(page).toMatch(/没有.*运行 receipt|no.*runtime receipt/iu);
    }
  });

  test('provides a bounded removal and exact-version migration procedure', async () => {
    const pages = await Promise.all([
      sdkDoc('uniapp/migrate-to-jssdk.mdx'),
      sdkDoc('uniapp/migrate-to-jssdk.en.mdx'),
    ]);

    for (const page of pages) {
      expect(page).toContain(`npm uninstall ${snapshot.retiredPackage}`);
      expect(page).toContain(
        `npm install --save-exact ${snapshot.targetPackage}@${snapshot.targetVersion}`,
      );
      expect(page).toContain(`npm ls ${snapshot.retiredPackage}`);
      expect(page).toContain(`npm ls ${snapshot.targetPackage}`);
      expect(page).toContain(`yarn remove ${snapshot.retiredPackage}`);
      expect(page).toContain(
        `yarn add --exact ${snapshot.targetPackage}@${snapshot.targetVersion}`,
      );
      expect(page).toContain(`pnpm remove ${snapshot.retiredPackage}`);
      expect(page).toContain(
        `pnpm add --save-exact ${snapshot.targetPackage}@${snapshot.targetVersion}`,
      );
      expect(page).toMatch(/退出码.*`1`|exit code.*`1`/iu);
      expect(page).toMatch(/第二份锁文件|second lockfile/iu);
      expect(page).toContain(snapshot.retiredArchiveSha);
      expect(page).toContain(snapshot.retiredIntegrity);
      expect(page).toContain(snapshot.targetArchiveSha);
      expect(page).toContain(snapshot.targetIntegrity);
      expect(page).toContain(snapshot.targetRevision);
      expect(page).toContain("from 'wukongimjssdk'");
      expect(page).toMatch(/深路径|deep import/iu);
    }
  });

  test('documents the exact UniApp adapter and Device Flag boundary', async () => {
    const pages = await Promise.all([
      sdkDoc('uniapp/migrate-to-jssdk.mdx'),
      sdkDoc('uniapp/migrate-to-jssdk.en.mdx'),
    ]);

    for (const page of pages) {
      expect(page).toContain('uni.connectSocket');
      expect(page).toContain('wx.connectSocket');
      expect(page).toMatch(/uni\.connectSocket.*wx\.connectSocket/isu);
      expect(page).toContain('runtime.uni !== undefined');
      expect(page).toContain('runtime.wx !== undefined');
      expect(page).toContain('config.platform');
      expect(page).toContain('wkconnectSocket');
      expect(page).toMatch(/不要.*config\.platform|do not.*config\.platform/iu);
      expect(page).toMatch(/陈旧.*adapter|stale.*adapter/iu);
      expect(page).toContain('0 = APP');
      expect(page).toContain('1 = WEB');
      expect(page).toContain('2 = PC');
      expect(page).toContain('deviceFlag');
      expect(page).toContain('WSS');
    }
  });

  test('separates custom-content factories from runtime listeners', async () => {
    const pages = await Promise.all([
      sdkDoc('uniapp/migrate-to-jssdk.mdx'),
      sdkDoc('uniapp/migrate-to-jssdk.en.mdx'),
    ]);

    for (const page of pages) {
      expect(page).toContain('WKSDK.shared().register(');
      expect(page).toContain('addConnectStatusListener');
      expect(page).toContain('addMessageListener');
      expect(page).toContain('addMessageStatusListener');
      expect(page).toContain('UnknownContent');
    }
  });

  test('blocks release on the unconditional plaintext payload logs', async () => {
    const pages = await Promise.all([
      sdkDoc('uniapp/migrate-to-jssdk.mdx'),
      sdkDoc('uniapp/migrate-to-jssdk.en.mdx'),
    ]);

    for (const page of pages) {
      const releaseGate = page.match(/## 8\.[\s\S]*$/u)?.[0] ?? '';

      expect(releaseGate).toContain('config.debug = false');
      expect(releaseGate).toMatch(/明文.*Payload|plaintext.*Payload/iu);
      expect(releaseGate).toMatch(/补丁|patch/iu);
      expect(releaseGate).toMatch(/脱敏|redaction/iu);
      expect(releaseGate).toMatch(/构建期移除|build-time stripping/iu);
      expect(releaseGate).toMatch(/不能.*发布|cannot.*release/iu);
    }
  });

  test('keeps every UniApp target outside the Chromium receipt', async () => {
    const pages = await Promise.all([
      sdkDoc('uniapp/index.mdx'),
      sdkDoc('uniapp/index.en.mdx'),
      sdkDoc('uniapp/migrate-to-jssdk.mdx'),
      sdkDoc('uniapp/migrate-to-jssdk.en.mdx'),
    ]);

    for (const page of pages) {
      expect(page).toContain('Chromium');
      expect(page).toContain('HBuilderX');
      expect(page).toMatch(/App.*H5.*小程序|App.*H5.*mini-program/isu);
      expect(page).toMatch(/独立.*验收|separate.*acceptance/iu);
      expect(page).toMatch(/受信.*后端|trusted.*backend/iu);
    }
  });

  test('aligns discovery, compatibility, and project knowledge with retirement', async () => {
    const pages = await Promise.all([
      sdkDoc('index.mdx'),
      sdkDoc('index.en.mdx'),
      sdkDoc('choose-sdk.mdx'),
      sdkDoc('choose-sdk.en.mdx'),
      sdkDoc('compatibility.mdx'),
      sdkDoc('compatibility.en.mdx'),
    ]);
    const knowledge = await readFile(projectKnowledge, 'utf8');

    for (const page of pages) {
      expect(page).toContain(snapshot.targetVersion);
      expect(page).toMatch(/\/zh\/sdk\/uniapp|\/en\/sdk\/uniapp/u);
      expect(page).toMatch(/弃用|deprecated/iu);
    }
    for (const value of [
      snapshot.retiredRevision,
      snapshot.retiredArchiveSha,
      snapshot.targetRevision,
      snapshot.targetArchiveSha,
      'uni.connectSocket',
      'wx.connectSocket',
      'config.debug = false',
      'stale adapter',
      '0 = APP',
      '1 = WEB',
      '2 = PC',
    ]) {
      expect(knowledge).toContain(value);
    }
  });
});
