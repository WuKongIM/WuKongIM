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
});
