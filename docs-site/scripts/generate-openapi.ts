import { generateFilesOnly } from 'fumadocs-openapi';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import openapiDocument from '../contracts/javascript-web-quickstart.openapi.json';
import {
  createProductHTTPManagementOpenAPI,
  createProductHTTPOpenAPI,
  localizeOpenAPIDocument,
  productHTTPManagementOpenAPIPages,
  productHTTPOpenAPIPages,
} from '../lib/openapi';
import { getOpenAPISearchStructuredData } from '../lib/openapi-markdown';

type Locale = 'zh' | 'en';

const mode = process.argv[2];
const docsRoot = fileURLToPath(new URL('..', import.meta.url));
const generatedRoot = resolve(docsRoot, 'content/docs/api/product-http');
const supplementRoot = resolve(docsRoot, 'content/openapi/product-http');
const generatedComment =
  'This file is generated from the bounded Product HTTP OpenAPI contract and its locale supplement. Run `bun run openapi:write` instead of editing it directly.';
const managementGeneratedComment =
  'This file is generated from the Product HTTP management OpenAPI contract and its locale supplement. Run `bun run openapi:write` instead of editing it directly.';

if (mode !== '--check' && mode !== '--write') {
  throw new Error('usage: bun run scripts/generate-openapi.ts --check|--write');
}

function pageForOperation(path: string, method: string) {
  return productHTTPOpenAPIPages.find(
    (page) => page.path === path && page.method === method,
  );
}

async function generatedGoldenPathFiles(locale: Locale) {
  const localized = localizeOpenAPIDocument(openapiDocument, locale) as unknown as {
    paths: Record<string, Record<string, { summary?: string }>>;
  };
  const pageByOperationTitle = new Map<
    string,
    (typeof productHTTPOpenAPIPages)[number]
  >();
  for (const page of productHTTPOpenAPIPages) {
    const title = localized.paths[page.path]?.[page.method]?.summary;
    if (!title) {
      throw new Error(`OpenAPI operation is missing its summary: ${page.method} ${page.path}`);
    }
    pageByOperationTitle.set(title, page);
  }
  const files = await generateFilesOnly({
    input: createProductHTTPOpenAPI(locale),
    per: 'operation',
    includeDescription: true,
    addGeneratedComment: generatedComment,
    name(output) {
      if (output.type !== 'operation') {
        throw new Error(`unexpected OpenAPI output type: ${output.type}`);
      }
      const page = pageForOperation(output.item.path, output.item.method);
      if (!page) {
        throw new Error(
          `OpenAPI operation is outside the published Beta subset: ${output.item.method.toUpperCase()} ${output.item.path}`,
        );
      }
      return `${page.slug}${locale === 'en' ? '.en' : ''}`;
    },
    frontmatter(title) {
      const page = pageByOperationTitle.get(title);
      if (!page) throw new Error(`missing localized metadata for OpenAPI page: ${title}`);
      return {
        title: page.title[locale],
        description: page.description[locale],
        full: true,
      };
    },
  });

  return Promise.all(
    files.map(async (file) => {
      const page = productHTTPOpenAPIPages.find((candidate) =>
        file.path.startsWith(candidate.slug),
      );
      if (!page) throw new Error(`unexpected generated OpenAPI file: ${file.path}`);
      const supplementName = `${page.slug}${locale === 'en' ? '.en' : ''}.mdx`;
      const supplement = await readFile(resolve(supplementRoot, supplementName), 'utf8');

      return {
        path: file.path,
        content: `${file.content.trimEnd()}\n\n${supplement.trim()}\n`,
      };
    }),
  );
}

async function generatedManagementFiles(locale: Locale) {
  const files = await generateFilesOnly({
    input: createProductHTTPManagementOpenAPI(locale),
    per: 'tag',
    includeDescription: true,
    addGeneratedComment: managementGeneratedComment,
    name(output) {
      if (output.type !== 'page' || !output.tag) {
        throw new Error(`unexpected management OpenAPI output type: ${output.type}`);
      }
      const tagName = output.tag.name;
      const page = productHTTPManagementOpenAPIPages.find(
        (candidate) => candidate.tag === tagName,
      );
      if (!page) {
        throw new Error(`OpenAPI tag is outside the published management subset: ${tagName}`);
      }
      const generatedOperations = output.operations.map((operation) => ({
        method: operation.method,
        path: operation.path,
      }));
      if (JSON.stringify(generatedOperations) !== JSON.stringify(page.operations)) {
        throw new Error(`OpenAPI tag operation list drifted: ${tagName}`);
      }
      return `${page.slug}${locale === 'en' ? '.en' : ''}`;
    },
    frontmatter(title) {
      const page = productHTTPManagementOpenAPIPages.find(
        (candidate) => candidate.tag === title,
      );
      if (!page) throw new Error(`missing localized metadata for management page: ${title}`);
      const structuredData = getOpenAPISearchStructuredData(locale, [
        'api',
        'product-http',
        page.slug,
      ]);
      if (!structuredData) {
        throw new Error(`missing searchable OpenAPI data for management page: ${title}`);
      }
      return {
        title: page.title[locale],
        description: page.description[locale],
        full: true,
        _openapi: { structuredData },
      };
    },
  });

  return Promise.all(
    files.map(async (file) => {
      const page = productHTTPManagementOpenAPIPages.find((candidate) =>
        file.path.startsWith(candidate.slug),
      );
      if (!page) throw new Error(`unexpected generated management file: ${file.path}`);
      const supplementName = `${page.slug}${locale === 'en' ? '.en' : ''}.mdx`;
      const supplement = await readFile(resolve(supplementRoot, supplementName), 'utf8');

      return {
        path: file.path,
        content: `${file.content.trimEnd()}\n\n${supplement.trim()}\n`,
      };
    }),
  );
}

const files = [
  ...(await generatedGoldenPathFiles('zh')),
  ...(await generatedGoldenPathFiles('en')),
  ...(await generatedManagementFiles('zh')),
  ...(await generatedManagementFiles('en')),
].sort((a, b) => a.path.localeCompare(b.path));
const expectedPaths = new Set(
  [...productHTTPOpenAPIPages, ...productHTTPManagementOpenAPIPages].flatMap((page) => [
    `${page.slug}.mdx`,
    `${page.slug}.en.mdx`,
  ]),
);

if (files.length !== expectedPaths.size || files.some((file) => !expectedPaths.has(file.path))) {
  throw new Error('generated OpenAPI pages differ from the bilingual page registries');
}

const drifted: string[] = [];
for (const file of files) {
  const target = resolve(generatedRoot, file.path);
  if (mode === '--write') {
    await mkdir(dirname(target), { recursive: true });
    await writeFile(target, file.content);
    continue;
  }

  let current: string | undefined;
  try {
    current = await readFile(target, 'utf8');
  } catch {
    current = undefined;
  }
  if (current !== file.content) drifted.push(file.path);
}

if (drifted.length > 0) {
  throw new Error(
    `generated OpenAPI pages are stale: ${drifted.join(', ')}; run bun run openapi:write`,
  );
}

console.log(
  mode === '--write'
    ? `generated ${files.length} bilingual OpenAPI pages`
    : `verified ${files.length} bilingual OpenAPI pages`,
);
