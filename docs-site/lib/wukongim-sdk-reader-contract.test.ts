import { describe, expect, test } from 'bun:test';
import { readdir } from 'node:fs/promises';
import { domains, getNavigationEntry, isNavigationGroup } from './navigation';

const docsRoot = new URL('../content/docs/sdk/', import.meta.url);
const platforms = ['android', 'ios', 'javascript', 'flutter', 'harmonyos'] as const;
const platformVersions = {
  android: '1.5.5',
  ios: '1.1.1',
  javascript: '1.3.5',
  flutter: '1.7.9',
  harmonyos: '1.1.7',
} as const;
const corePages = [
  'quickstart',
  'connection',
  'messages',
  'conversations',
  'channels',
  'advanced',
  'api-reference',
] as const;

async function page(path: string) {
  return Bun.file(new URL(path, docsRoot)).text();
}

async function publicFullSDKPages() {
  const paths: string[] = [];
  for (const platform of platforms) {
    const visit = async (directory: string) => {
      for (const entry of await readdir(new URL(`${directory}/`, docsRoot), {
        withFileTypes: true,
      })) {
        const relative = `${directory}/${entry.name}`;
        if (entry.isDirectory()) await visit(relative);
        if (entry.isFile() && entry.name.endsWith('.mdx')) paths.push(relative);
      }
    };
    await visit(platform);
  }
  paths.push(
    'index.mdx',
    'index.en.mdx',
    'wukongim/index.mdx',
    'wukongim/index.en.mdx',
    'wukongim/concepts.mdx',
    'wukongim/concepts.en.mdx',
    'wukongim/upgrade.mdx',
    'wukongim/upgrade.en.mdx',
  );
  return Promise.all(paths.map(async (path) => [path, await page(path)] as const));
}

describe('reader-first WuKongIMSDK documentation', () => {
  test('publishes one task-oriented path for every maintained platform', () => {
    for (const platform of platforms) {
      const platformEntry = getNavigationEntry('zh', 'sdk', [platform]);
      expect(platformEntry?.status).toBe('published');

      for (const slug of corePages) {
        expect(getNavigationEntry('zh', 'sdk', [platform, slug])?.status).toBe('published');
        expect(getNavigationEntry('en', 'sdk', [platform, slug])?.status).toBe('published');
      }
    }

    expect(getNavigationEntry('zh', 'sdk', ['wukongim', 'concepts'])?.status).toBe(
      'published',
    );
    expect(getNavigationEntry('zh', 'sdk', ['wukongim', 'upgrade'])?.status).toBe(
      'published',
    );
  });

  test('uses advanced subpages only for features documented on that platform', () => {
    const expected = {
      android: ['custom-messages', 'media-and-history'],
      ios: ['custom-messages', 'media-and-history'],
      javascript: ['custom-messages', 'offline-and-uniapp'],
      flutter: ['custom-messages', 'media-and-history'],
      harmonyos: ['custom-messages', 'media-and-history'],
    } as const;

    for (const platform of platforms) {
      const advanced = getNavigationEntry('zh', 'sdk', [platform, 'advanced']);
      expect(advanced?.status).toBe('published');
      const group = getNavigationEntry('zh', 'sdk', [platform]);
      expect(group).toBeDefined();

      const domain = domains.find((item) => item.key === 'sdk');
      const platformGroup = domain?.groups
        .flatMap((item) => (isNavigationGroup(item) ? item.children : []))
        .find((item) => isNavigationGroup(item) && item.slug === platform);
      const advancedGroup =
        platformGroup && isNavigationGroup(platformGroup)
          ? platformGroup.children.find(
              (item) => isNavigationGroup(item) && item.slug === 'advanced',
            )
          : undefined;
      expect(
        advancedGroup && isNavigationGroup(advancedGroup)
          ? advancedGroup.children.map((item) => item.slug)
          : [],
      ).toEqual([...expected[platform]]);
    }
  });

  test('removes superseded tutorial routes instead of keeping duplicate pages', async () => {
    for (const platform of platforms) {
      for (const slug of ['installation', 'platform-capabilities', 'upgrade']) {
        expect(getNavigationEntry('zh', 'sdk', [platform, slug])).toBeUndefined();
        expect(await Bun.file(new URL(`${platform}/${slug}.mdx`, docsRoot)).exists()).toBe(
          false,
        );
        expect(
          await Bun.file(new URL(`${platform}/${slug}.en.mdx`, docsRoot)).exists(),
        ).toBe(false);
      }
    }
    expect(getNavigationEntry('zh', 'sdk', ['choose-sdk'])).toBeUndefined();
    expect(getNavigationEntry('zh', 'sdk', ['compatibility'])).toBeUndefined();
    expect(getNavigationEntry('zh', 'sdk', ['common-guides'])).toBeUndefined();
  });

  test('pins the documented stable version once at each platform entry and quickstart', async () => {
    for (const platform of platforms) {
      for (const suffix of ['', '.en']) {
        const index = await page(`${platform}/index${suffix}.mdx`);
        const quickstart = await page(`${platform}/quickstart${suffix}.mdx`);
        expect(index).toContain(platformVersions[platform]);
        expect(quickstart).toContain(platformVersions[platform]);
      }
    }
  });

  test('keeps the first-message examples aligned with platform readiness and syntax', async () => {
    for (const suffix of ['', '.en']) {
      for (const route of ['quickstart', 'connection']) {
        const android = await page(`android/${route}${suffix}.mdx`);
        expect(android).toContain('status == WKConnectStatus.success && syncInProgress');
        expect(android).toContain('WKConnectStatus.syncMsg');
        expect(android).toContain('WKConnectStatus.syncCompleted');
      }

      const ios = await page(`ios/quickstart${suffix}.mdx`);
      expect(ios.match(/^@end$/gmu)).toHaveLength(2);
    }
  });

  test('keeps internal audit vocabulary out of the public full-SDK path', async () => {
    const banned = [
      /\breceipt\b/iu,
      /evidence boundary/iu,
      /source revision/iu,
      /SHA-?256/iu,
      /证据边界/u,
      /源码\s*revision/iu,
      /归档哈希/u,
      /运行凭据/u,
    ];

    for (const [path, content] of await publicFullSDKPages()) {
      for (const expression of banned) {
        expect(content, `${path} contains ${expression}`).not.toMatch(expression);
      }
    }
  });

  test('explains unfamiliar terms in plain language and keeps EasySDK examples separate', async () => {
    const conceptsZh = await page('wukongim/concepts.mdx');
    expect(conceptsZh).toContain('消息要发给谁');
    expect(conceptsZh).toContain('聊天列表中的一项');
    expect(conceptsZh).toContain('服务端已接收');

    for (const [path, content] of await publicFullSDKPages()) {
      if (path === 'index.mdx' || path === 'index.en.mdx') continue;
      expect(content, `${path} mixes the lightweight SDK into the full SDK path`).not.toContain(
        'WuKongEasySDK',
      );
    }
  });
});
