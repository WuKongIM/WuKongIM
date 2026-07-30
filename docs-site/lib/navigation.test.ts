import { describe, expect, test } from 'bun:test';
import {
  domains,
  getAllNavigationEntries,
  getIndexedNavigationEntries,
  getNavigationEntry,
  isPublishedContentPath,
  locales,
  parseLocale,
} from './navigation';
import { buildLayoutTabs, buildPageTree } from './navigation-tree';

describe('documentation navigation contract', () => {
  test('exposes the agreed locales and documentation domains in order', () => {
    expect(locales).toEqual(['zh', 'en']);
    expect(domains.map((domain) => domain.key)).toEqual(['guide', 'server', 'sdk', 'api']);
    expect(domains.map((domain) => domain.label.zh)).toEqual([
      '指南',
      '服务端',
      'SDK',
      'API 与协议',
    ]);
    expect(domains.map((domain) => domain.label.en)).toEqual([
      'Guides',
      'Server',
      'SDK',
      'API & Protocols',
    ]);
  });

  test('keeps the agreed first-level menu groups for every domain', () => {
    const byKey = Object.fromEntries(domains.map((domain) => [domain.key, domain]));

    expect(byKey.guide.groups.map((group) => group.slug)).toEqual([
      'product-overview',
      'quick-start',
      'core-concepts',
      'integration',
      'tutorials',
    ]);
    expect(byKey.server.groups.map((group) => group.slug)).toEqual([
      'deployment',
      'configuration',
      'operations',
      'tools',
      'architecture',
    ]);
    expect(byKey.sdk.groups.map((group) => group.slug)).toEqual([
      'common-guides',
      'android',
      'ios',
      'javascript',
      'flutter',
      'uniapp',
      'harmonyos',
    ]);
    expect(byKey.api.groups.map((group) => group.slug)).toEqual([
      'product-http',
      'operations-http',
      'webhooks',
      'client-protocols',
      'dictionaries',
      'specifications',
    ]);
    expect(byKey.sdk.pages.map((page) => page.slug)).toEqual(['choose-sdk', 'compatibility']);
    expect(byKey.api.pages.map((page) => page.slug)).toEqual([
      'conventions',
      'authentication',
      'compatibility',
    ]);
  });

  test('gives every bilingual menu item a unique canonical route', () => {
    for (const locale of locales) {
      const entries = getAllNavigationEntries(locale);
      const urls = entries.map((entry) => entry.url);

      expect(new Set(urls).size).toBe(urls.length);
      expect(urls.every((url) => url.startsWith(`/${locale}/`))).toBe(true);
      expect(getNavigationEntry(locale, 'guide', ['quick-start', 'first-message'])?.label).toBe(
        locale === 'zh' ? '发送第一条消息' : 'Send the First Message',
      );
    }
  });

  test('keeps planned pages out of public indexes', () => {
    for (const locale of locales) {
      const indexed = getIndexedNavigationEntries(locale);

      expect(indexed.map((entry) => entry.url)).toEqual([
        `/${locale}/guide`,
        `/${locale}/guide/product-overview`,
        `/${locale}/guide/product-overview/what-is-wukongim`,
        `/${locale}/guide/quick-start`,
        `/${locale}/guide/quick-start/prerequisites`,
        `/${locale}/guide/quick-start/single-node-cluster`,
        `/${locale}/guide/quick-start/first-message`,
        `/${locale}/guide/quick-start/chat-demo`,
        `/${locale}/guide/quick-start/next-steps`,
        `/${locale}/guide/core-concepts`,
        `/${locale}/guide/integration`,
        `/${locale}/guide/integration/architecture`,
        `/${locale}/guide/integration/authentication`,
        `/${locale}/guide/integration/messaging`,
        `/${locale}/guide/integration/webhooks`,
        `/${locale}/server`,
        `/${locale}/server/configuration`,
        `/${locale}/sdk`,
        `/${locale}/api`,
      ]);
      expect(indexed.every((entry) => entry.status === 'published')).toBe(true);
    }
  });

  test('backs every published route with matching Chinese and English MDX', async () => {
    for (const entry of getIndexedNavigationEntries('zh')) {
      const segments = [entry.domain, ...entry.slugs];
      if (entry.kind !== 'page') segments.push('index');
      const stem = segments.join('/');

      expect(await Bun.file(new URL(`../content/docs/${stem}.mdx`, import.meta.url)).exists()).toBe(
        true,
      );
      expect(
        await Bun.file(new URL(`../content/docs/${stem}.en.mdx`, import.meta.url)).exists(),
      ).toBe(true);
    }
  });

  test('builds a Fumadocs tree and top tabs from the same registry', () => {
    const tree = buildPageTree('zh', 'guide');
    const overview = tree.children[0];
    const folders = tree.children.filter((node) => node.type === 'folder');

    expect(overview.type).toBe('page');
    if (overview.type === 'page') expect(overview.url).toBe('/zh/guide');
    expect(folders.map((folder) => folder.index?.url)).toEqual([
      '/zh/guide/product-overview',
      '/zh/guide/quick-start',
      '/zh/guide/core-concepts',
      '/zh/guide/integration',
      '/zh/guide/tutorials',
    ]);
    expect(buildLayoutTabs('en').map((tab) => tab.url)).toEqual([
      '/en/guide',
      '/en/server',
      '/en/sdk',
      '/en/api',
    ]);
  });

  test('fails closed when MDX content is not marked as published', () => {
    expect(parseLocale('zh')).toBe('zh');
    expect(parseLocale('fr')).toBeUndefined();
    expect(isPublishedContentPath('guide/index.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/index.en.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/quick-start/index.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/quick-start/index.en.mdx')).toBe(true);
    expect(isPublishedContentPath('guide/integration/plugins.mdx')).toBe(false);
    expect(isPublishedContentPath('guide/integration/plugins.en.mdx')).toBe(false);
    expect(isPublishedContentPath('guide/tutorials/direct-chat.mdx')).toBe(false);
    expect(isPublishedContentPath('guide/tutorials/direct-chat.en.mdx')).toBe(false);
    expect(isPublishedContentPath('unknown/index.mdx')).toBe(false);
  });
});
