import { describe, expect, test } from 'bun:test';
import manifest from '../redirects.json';

describe('legacy redirect seed', () => {
  test('uses permanent, unique, locale-preserving mappings', () => {
    expect(manifest.version).toBe(1);
    expect(manifest.status).toBe(308);
    expect(manifest.scope).toBe('phase-one-seed');

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

  test('routes legacy SDK overview and source discovery into the published chooser', () => {
    expect(manifest.mappings).toEqual(
      expect.arrayContaining(
        ['zh', 'en'].flatMap((locale) =>
          ['/sdk/overview', '/sdk/source-code'].map((source) => ({
            source: `/${locale}${source}`,
            destination: `/${locale}/sdk/choose-sdk`,
          })),
        ),
      ),
    );
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
