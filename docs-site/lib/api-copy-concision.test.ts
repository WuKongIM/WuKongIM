import managementDocument from '../contracts/product-http-management.openapi.json';
import goldenPathDocument from '../contracts/javascript-web-quickstart.openapi.json';
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
] as const;

const supplementBudgets = {
  users: 24,
  routing: 24,
  messages: 28,
  channels: 34,
  conversations: 30,
} as const;

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
  paths: Record<string, Record<string, ConciseOperation>>;
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
      expect(page).not.toMatch(/Phase (12|16)/);
    }
  });

  test('keeps endpoint supplements short in both locales', async () => {
    for (const [slug, budget] of Object.entries(supplementBudgets)) {
      for (const suffix of ['.mdx', '.en.mdx']) {
        const supplement = await source(`../content/openapi/product-http/${slug}${suffix}`);
        expect(lineCount(supplement)).toBeLessThanOrEqual(budget);
        for (const heading of boilerplateHeadings) expect(supplement).not.toContain(heading);
        expect(supplement).not.toMatch(/Phase (12|16)/);
      }
    }
  });

  test('keeps OpenAPI operation descriptions scannable', () => {
    const documents = [goldenPathDocument, managementDocument] as unknown as ConciseDocument[];

    for (const document of documents) {
      expect(document.info.description.length).toBeLessThanOrEqual(180);

      for (const item of Object.values(document.paths)) {
        for (const operation of Object.values(item)) {
          expect(operation.description.length).toBeLessThanOrEqual(160);
          const zh = operation['x-i18n']?.zh;
          if (zh?.description) expect(zh.description.length).toBeLessThanOrEqual(90);
        }
      }
    }
  });
});
