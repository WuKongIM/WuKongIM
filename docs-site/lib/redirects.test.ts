import { describe, expect, test } from 'bun:test';
import manifest from '../redirects.json';

const legacyFullSDKRoutes = {
  ios: {
    '': '',
    intro: '',
    integration: 'quickstart',
    connection: 'connection',
    chat: 'messages',
    channel: 'channels',
    conversation: 'conversations',
    media: 'advanced/media-and-history',
    advance: 'advanced',
    advanced: 'advanced',
  },
  android: {
    '': '',
    intro: '',
    integration: 'quickstart',
    base: 'connection',
    message: 'messages',
    channel: 'channels',
    'channel-member': 'channels',
    conversation: 'conversations',
    cmd: 'advanced',
    datasource: 'advanced/media-and-history',
    reminder: 'conversations',
    advance: 'advanced',
  },
  flutter: {
    '': '',
    intro: '',
    integration: 'quickstart',
    base: 'connection',
    message: 'messages',
    channel: 'channels',
    channel_member: 'channels',
    conversation: 'conversations',
    cmd: 'advanced',
    datasource: 'advanced/media-and-history',
    reminder: 'conversations',
    advance: 'advanced',
  },
  harmonyos: {
    '': '',
    intro: '',
    integration: 'quickstart',
    base: 'connection',
    message: 'messages',
    channel: 'channels',
    channel_member: 'channels',
    conversation: 'conversations',
    cmd: 'advanced',
    datasource: 'advanced/media-and-history',
    reminder: 'conversations',
    advance: 'advanced',
  },
  javascript: {
    '': '',
    intro: '',
    integration: 'quickstart',
    base: 'connection',
    chat: 'messages',
    channel: 'channels',
    conversation: 'conversations',
    datasource: 'advanced/offline-and-uniapp',
    advance: 'advanced',
  },
} as const;

describe('legacy redirect seed', () => {
  test('uses permanent, unique, locale-preserving mappings', () => {
    expect(manifest.version).toBe(1);
    expect(manifest.status).toBe(308);
    expect(manifest.scope).toBe('public-route-migrations');

    const sources = manifest.mappings.map((mapping) => mapping.source);
    expect(new Set(sources).size).toBe(sources.length);

    for (const mapping of manifest.mappings) {
      expect(mapping.source).not.toBe(mapping.destination);
      expect(mapping.source).toMatch(/^\/(zh|en)\//);
      expect(mapping.destination).toMatch(/^\/(zh|en)\//);
      expect(mapping.destination.slice(1, 3)).toBe(mapping.source.slice(1, 3));
    }
  });

  test('preserves the routes replaced by the reader-first core concepts', () => {
    expect(manifest.mappings).toEqual(
      expect.arrayContaining([
        {
          source: '/zh/guide/core-concepts/cluster-and-nodes',
          destination: '/zh/server/architecture',
        },
        {
          source: '/en/guide/core-concepts/cluster-and-nodes',
          destination: '/en/server/architecture',
        },
        {
          source: '/zh/guide/core-concepts/users-and-devices',
          destination: '/zh/guide/core-concepts/users',
        },
        {
          source: '/en/guide/core-concepts/users-and-devices',
          destination: '/en/guide/core-concepts/users',
        },
      ]),
    );
  });

  test('routes the merged deployment chooser into the deployment entry', () => {
    expect(manifest.mappings).toEqual(
      expect.arrayContaining(
        ['zh', 'en'].map((locale) => ({
          source: `/${locale}/server/deployment/choosing`,
          destination: `/${locale}/server/deployment`,
        })),
      ),
    );
  });

  test('routes withdrawn Kubernetes deployment pages into the deployment entry', () => {
    expect(manifest.mappings).toEqual(
      expect.arrayContaining(
        ['zh', 'en'].flatMap((locale) =>
          ['/server/deployment/kubernetes', '/server/deployment/kubernetes-resources'].map(
            (source) => ({
              source: `/${locale}${source}`,
              destination: `/${locale}/server/deployment`,
            }),
          ),
        ),
      ),
    );
  });

  test('routes the merged product definition into the product overview', () => {
    expect(manifest.mappings).toEqual(
      expect.arrayContaining(
        ['zh', 'en'].map((locale) => ({
          source: `/${locale}/guide/product-overview/what-is-wukongim`,
          destination: `/${locale}/guide/product-overview`,
        })),
      ),
    );
  });

  test('routes legacy SDK overview and source discovery into the current SDK entry', () => {
    expect(manifest.mappings).toEqual(
      expect.arrayContaining(
        ['zh', 'en'].flatMap((locale) =>
          ['/sdk/overview', '/sdk/source-code'].map((source) => ({
            source: `/${locale}${source}`,
            destination: `/${locale}/sdk`,
          })),
        ),
      ),
    );
  });

  test('routes superseded SDK pages without retaining duplicate content', () => {
    for (const locale of ['zh', 'en']) {
      expect(manifest.mappings).toEqual(
        expect.arrayContaining([
          {
            source: `/${locale}/sdk/choose-sdk`,
            destination: `/${locale}/sdk`,
          },
          {
            source: `/${locale}/sdk/ios/installation`,
            destination: `/${locale}/sdk/ios/quickstart`,
          },
          {
            source: `/${locale}/sdk/android/platform-capabilities`,
            destination: `/${locale}/sdk/android/advanced`,
          },
          {
            source: `/${locale}/sdk/flutter/upgrade`,
            destination: `/${locale}/sdk/wukongim/upgrade`,
          },
          {
            source: `/${locale}/sdk/uniapp`,
            destination: `/${locale}/sdk/javascript/advanced/offline-and-uniapp`,
          },
        ]),
      );
    }
  });

  test('preserves every deep link from the former full-SDK documentation', () => {
    const expected = ['zh', 'en'].flatMap((locale) =>
      Object.entries(legacyFullSDKRoutes).flatMap(([platform, pages]) =>
        Object.entries(pages).map(([sourcePage, destinationPage]) => ({
          source: `/${locale}/sdk/wukongim/${platform}${sourcePage ? `/${sourcePage}` : ''}`,
          destination: `/${locale}/sdk/${platform}${destinationPage ? `/${destinationPage}` : ''}`,
        })),
      ),
    );

    expected.push(
      ...['zh', 'en'].flatMap((locale) =>
        ['uniapp', 'miniprogram'].flatMap((platform) =>
          ['', '/intro'].map((suffix) => ({
            source: `/${locale}/sdk/wukongim/${platform}${suffix}`,
            destination: `/${locale}/sdk/javascript/advanced/offline-and-uniapp`,
          })),
        ),
      ),
    );

    expect(manifest.mappings).toEqual(expect.arrayContaining(expected));
  });

  test('routes the legacy EasySDK overview to its published counterpart', () => {
    expect(manifest.mappings).toEqual(
      expect.arrayContaining(
        ['zh', 'en'].map((locale) => ({
          source: `/${locale}/sdk/easy/overview`,
          destination: `/${locale}/sdk/easy`,
        })),
      ),
    );
  });
});
