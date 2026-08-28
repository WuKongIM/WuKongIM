import managementDocument from '../contracts/product-http-management.openapi.json';
import messagingDocument from '../contracts/product-http-messaging.openapi.json';
import goldenPathDocument from '../contracts/javascript-web-quickstart.openapi.json';
import completeDocument from '../contracts/product-http.openapi.json';
import operationsDocument from '../contracts/operations-http.openapi.json';
import webhooksDocument from '../contracts/webhooks.openapi.json';
import { describe, expect, test } from 'bun:test';

const readerPages = [
  '../content/docs/api/index.mdx',
  '../content/docs/api/index.en.mdx',
  '../content/docs/api/authentication.mdx',
  '../content/docs/api/authentication.en.mdx',
  '../content/docs/api/conventions.mdx',
  '../content/docs/api/conventions.en.mdx',
  '../content/docs/api/compatibility.mdx',
  '../content/docs/api/compatibility.en.mdx',
  '../content/docs/api/product-http/index.mdx',
  '../content/docs/api/product-http/index.en.mdx',
  '../content/docs/api/product-http/errors.mdx',
  '../content/docs/api/product-http/errors.en.mdx',
  '../content/docs/api/client-protocols/index.mdx',
  '../content/docs/api/client-protocols/index.en.mdx',
  '../content/docs/api/client-protocols/connection-lifecycle.mdx',
  '../content/docs/api/client-protocols/connection-lifecycle.en.mdx',
  '../content/docs/api/client-protocols/packet-types.mdx',
  '../content/docs/api/client-protocols/packet-types.en.mdx',
] as const;

const tagIndexes = [
  'users',
  'routing',
  'messages',
  'message-send',
  'channels',
  'conversations',
] as const;

const boilerplateHeadings = [
  '## 目标与完成标准',
  '## 兼容目标',
  '## 前置条件',
  '## 预期结果',
  '## 下一步',
  '## Goal and completion criteria',
  '## Compatibility target',
  '## Prerequisites',
  '## Expected result',
  '## Next step',
] as const;

interface ConciseOperation {
  description: string;
  'x-i18n'?: { zh?: { description?: string } };
}

interface ConciseDocument {
  info: { description: string };
  paths?: Record<string, Record<string, ConciseOperation>>;
  webhooks?: Record<string, Record<string, ConciseOperation>>;
}

async function source(relativePath: string) {
  return Bun.file(new URL(relativePath, import.meta.url)).text();
}

function lineCount(value: string) {
  return value.trim().split('\n').length;
}

describe('API copy concision', () => {
  test('removes process-oriented boilerplate from reader-facing API pages', async () => {
    const pages = await Promise.all(readerPages.map(source));

    for (const page of pages) {
      for (const heading of boilerplateHeadings) expect(page).not.toContain(heading);
      expect(page).not.toMatch(/Phase (12|16|17)/);
    }
  });

  test('keeps generated tag indexes short in both locales', async () => {
    for (const slug of tagIndexes) {
      for (const suffix of ['.mdx', '.en.mdx']) {
        const index = await source(
          `../content/docs/api/product-http/${slug}/index${suffix}`,
        );
        expect(lineCount(index)).toBeLessThanOrEqual(32);
        expect(index).toContain('<Cards>');
        expect(index).not.toContain('operations={[');
        for (const heading of boilerplateHeadings) expect(index).not.toContain(heading);
        expect(index).not.toMatch(/Phase (12|16|17)/);
      }
    }
  });

  test('keeps client-protocol pages concise in both locales', async () => {
    const pages = [
      ['index', 30],
      ['connection-lifecycle', 45],
      ['packet-types', 45],
      ['tcp-binary', 65],
      ['json-rpc', 40],
      ['encryption', 55],
    ] as const;

    for (const [page, maximum] of pages) {
      for (const suffix of ['.mdx', '.en.mdx']) {
        const content = await source(
          `../content/docs/api/client-protocols/${page}${suffix}`,
        );
        expect(lineCount(content)).toBeLessThanOrEqual(maximum);
      }
    }
  });

  test('keeps OpenAPI operation descriptions scannable', () => {
    const documents = [
      goldenPathDocument,
      managementDocument,
      messagingDocument,
      completeDocument,
      operationsDocument,
      webhooksDocument,
    ] as unknown as ConciseDocument[];

    for (const document of documents) {
      expect(document.info.description.length).toBeLessThanOrEqual(180);

      const pathItems = [
        ...Object.values(document.paths ?? {}),
        ...Object.values(document.webhooks ?? {}),
      ];
      for (const item of pathItems) {
        for (const operation of Object.values(item)) {
          expect(operation.description.length).toBeLessThanOrEqual(160);
          const zh = operation['x-i18n']?.zh;
          if (zh?.description) expect(zh.description.length).toBeLessThanOrEqual(90);
        }
      }
    }
  });

  test('uses the concise default schema presentation', async () => {
    const component = await source('../components/openapi-page.tsx');

    expect(component).not.toContain('showExample: true');
  });
});
