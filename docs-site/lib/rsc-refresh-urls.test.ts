import { describe, expect, test } from 'bun:test';
import { mkdtemp, mkdir, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import {
  RSC_REFRESH_ORIGIN,
  RSC_REFRESH_URLS_FILE,
  RSC_REFRESH_URL_LIMIT,
  createRSCRefreshURLs,
  findRSCRefreshURLInventoryIssues,
  generateRSCRefreshURLInventory,
  serializeRSCRefreshURLs,
} from '../scripts/generate-rsc-refresh-urls';

describe('RSC refresh URL inventory', () => {
  test('maps eligible static pages to one deterministic fixed-origin URL list', () => {
    const urls = createRSCRefreshURLs(
      [
        'zh\\guide\\integration\\index.html',
        '404/index.html',
        'index.html',
        '_not-found/index.html',
        'en/guide/index.html',
      ],
      new Set([
        'zh/guide/integration/index.txt',
        '404/index.txt',
        'index.txt',
        '_not-found/index.txt',
        'en/guide/index.txt',
      ]),
    );

    expect(urls).toEqual([
      `${RSC_REFRESH_ORIGIN}/_not-found/index.txt`,
      `${RSC_REFRESH_ORIGIN}/en/guide/index.txt`,
      `${RSC_REFRESH_ORIGIN}/index.txt`,
      `${RSC_REFRESH_ORIGIN}/zh/guide/integration/index.txt`,
    ]);
    expect(serializeRSCRefreshURLs(urls)).toBe(`${urls.join('\n')}\n`);
  });

  test('fails closed on unsafe, duplicate, missing, or unbounded route inventories', () => {
    expect(() =>
      createRSCRefreshURLs(['en/guide/index.html'], new Set()),
    ).toThrow('missing static RSC sibling');
    expect(() =>
      createRSCRefreshURLs(
        ['en/guide/index.html', 'en\\guide\\index.html'],
        new Set(['en/guide/index.txt']),
      ),
    ).toThrow('duplicate static RSC route');

    for (const unsafe of [
      '../index.html',
      'en//guide/index.html',
      'en/./guide/index.html',
      'en/%2e%2e/guide/index.html',
      'en/指南/index.html',
      'en/guide/page.html',
    ]) {
      expect(() =>
        createRSCRefreshURLs([unsafe], new Set([unsafe.replace(/\.html$/, '.txt')])),
      ).toThrow('unsafe static HTML route');
    }

    const excessivePages = Array.from(
      { length: RSC_REFRESH_URL_LIMIT + 1 },
      (_, index) => `route-${String(index).padStart(3, '0')}/index.html`,
    );
    const excessivePayloads = new Set(
      excessivePages.map((path) => path.replace(/\.html$/, '.txt')),
    );
    expect(() => createRSCRefreshURLs(excessivePages, excessivePayloads)).toThrow(
      `exceeds safety limit ${RSC_REFRESH_URL_LIMIT}`,
    );
  });

  test('validates exact formatting, origin, paths, ordering, uniqueness, and completeness', () => {
    const expected = [
      `${RSC_REFRESH_ORIGIN}/en/index.txt`,
      `${RSC_REFRESH_ORIGIN}/index.txt`,
    ];
    const valid = serializeRSCRefreshURLs(expected);
    expect(findRSCRefreshURLInventoryIssues(valid, expected)).toEqual([]);

    const invalidInventories = [
      valid.trimEnd(),
      `${valid}\n`,
      valid.replace('https://', 'http://'),
      valid.replace('docs.githubim.com', 'user:secret@docs.githubim.com'),
      valid.replace('docs.githubim.com', 'docs.githubim.com:8443'),
      valid.replace('/en/index.txt', '/en/index.txt?stale=1'),
      valid.replace('/en/index.txt', '/en/index.txt#fragment'),
      valid.replace('/en/index.txt', '/en//index.txt'),
      valid.replace('/en/index.txt', '/en/%2e%2e/index.txt'),
      serializeRSCRefreshURLs([...expected].reverse()),
      serializeRSCRefreshURLs([expected[0], expected[0], expected[1]]),
      serializeRSCRefreshURLs([expected[0]]),
    ];

    for (const inventory of invalidInventories) {
      expect(findRSCRefreshURLInventoryIssues(inventory, expected)).not.toEqual([]);
    }
  });

  test('scans a real export and rewrites the same inventory bytes', async () => {
    const outputDirectory = await mkdtemp(join(tmpdir(), 'docs-rsc-inventory-'));
    try {
      for (const directory of ['404', '_not-found', 'en/guide']) {
        await mkdir(join(outputDirectory, directory), { recursive: true });
      }
      await Promise.all([
        Bun.write(join(outputDirectory, 'index.html'), '<html></html>'),
        Bun.write(join(outputDirectory, 'index.txt'), 'root RSC'),
        Bun.write(join(outputDirectory, '404/index.html'), '<html></html>'),
        Bun.write(join(outputDirectory, '_not-found/index.html'), '<html></html>'),
        Bun.write(join(outputDirectory, '_not-found/index.txt'), 'not found RSC'),
        Bun.write(join(outputDirectory, 'en/guide/index.html'), '<html></html>'),
        Bun.write(join(outputDirectory, 'en/guide/index.txt'), 'guide RSC'),
        Bun.write(join(outputDirectory, 'en/guide/__next.sample.txt'), 'segment'),
      ]);

      const outputURLWithoutTrailingSlash = pathToFileURL(outputDirectory);
      const firstURLs = await generateRSCRefreshURLInventory(
        outputURLWithoutTrailingSlash,
      );
      const inventoryPath = join(outputDirectory, RSC_REFRESH_URLS_FILE);
      const firstContent = await readFile(inventoryPath, 'utf8');
      const secondURLs = await generateRSCRefreshURLInventory(
        outputURLWithoutTrailingSlash,
      );
      const secondContent = await readFile(inventoryPath, 'utf8');

      expect(firstURLs).toEqual([
        `${RSC_REFRESH_ORIGIN}/_not-found/index.txt`,
        `${RSC_REFRESH_ORIGIN}/en/guide/index.txt`,
        `${RSC_REFRESH_ORIGIN}/index.txt`,
      ]);
      expect(secondURLs).toEqual(firstURLs);
      expect(secondContent).toBe(firstContent);
      expect(firstContent).toBe(serializeRSCRefreshURLs(firstURLs));
    } finally {
      await rm(outputDirectory, { recursive: true, force: true });
    }
  });
});
