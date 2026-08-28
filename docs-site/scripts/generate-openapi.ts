import { generateFilesOnly } from 'fumadocs-openapi';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  createProductHTTPManagementOpenAPI,
  createProductHTTPOpenAPI,
} from '../lib/openapi';
import { getOpenAPISearchStructuredData } from '../lib/openapi-markdown';
import {
  productHTTPOpenAPIReferenceGroups,
  type ProductHTTPOpenAPIContract,
  type ProductHTTPOpenAPILocale,
} from '../lib/product-http-openapi';

const mode = process.argv[2];
const docsRoot = fileURLToPath(new URL('..', import.meta.url));
const generatedRoot = resolve(docsRoot, 'content/docs/api/product-http');

if (mode !== '--check' && mode !== '--write') {
  throw new Error('usage: bun run scripts/generate-openapi.ts --check|--write');
}

function groupsFor(contract: ProductHTTPOpenAPIContract) {
  return productHTTPOpenAPIReferenceGroups.filter((group) => group.contract === contract);
}

function localizedSuffix(locale: ProductHTTPOpenAPILocale) {
  return locale === 'en' ? '.en' : '';
}

function routeForGeneratedFile(locale: ProductHTTPOpenAPILocale, filePath: string) {
  const stem = filePath.replace(/\.en\.mdx$|\.mdx$/, '');
  return `/${locale}/api/product-http/${stem}`;
}

function renderDeferrals(
  locale: ProductHTTPOpenAPILocale,
  group: (typeof productHTTPOpenAPIReferenceGroups)[number],
) {
  if (!group.deferrals) return '';
  return [
    `<Callout type="info" title="${group.deferrals.title[locale]}">`,
    '',
    ...group.deferrals.items.map(
      (item) =>
        `- ${item.routes.map((route) => `\`${route}\``).join(', ')} — ${item.reason[locale]}`,
    ),
    '',
    '</Callout>',
  ].join('\n');
}

async function generatedContractFiles(
  contract: ProductHTTPOpenAPIContract,
  locale: ProductHTTPOpenAPILocale,
) {
  const groups = groupsFor(contract);
  const suffix = localizedSuffix(locale);
  const operations = groups.flatMap((group) => group.operations);
  const titleToOperation = new Map(
    operations.map((operation) => [operation.title[locale], operation]),
  );
  if (titleToOperation.size !== operations.length) {
    throw new Error(`OpenAPI ${contract} operation titles must be unique in ${locale}`);
  }

  const files = await generateFilesOnly({
    input:
      contract === 'golden-path'
        ? createProductHTTPOpenAPI(locale)
        : createProductHTTPManagementOpenAPI(locale),
    per: 'operation',
    groupBy: 'tag',
    includeDescription: true,
    addGeneratedComment: true,
    name(output) {
      if (output.type !== 'operation') {
        throw new Error(`unexpected OpenAPI output type: ${output.type}`);
      }
      const operation = operations.find(
        (candidate) =>
          candidate.path === output.item.path && candidate.method === output.item.method,
      );
      if (!operation) {
        throw new Error(
          `OpenAPI operation is outside the published ${contract} subset: ${output.item.method.toUpperCase()} ${output.item.path}`,
        );
      }
      return `${operation.slug}${suffix}`;
    },
    frontmatter(title, _description, context) {
      if (context.type !== 'operation') return {};
      const operation = titleToOperation.get(title);
      if (!operation) {
        throw new Error(`missing route metadata for OpenAPI operation: ${title}`);
      }
      const structuredData = getOpenAPISearchStructuredData(locale, [
        'api',
        'product-http',
        operation.groupSlug,
        operation.slug,
      ]);
      if (!structuredData) {
        throw new Error(`missing searchable OpenAPI data for operation: ${title}`);
      }
      return { _openapi: { structuredData } };
    },
    index: {
      url: (filePath) => routeForGeneratedFile(locale, filePath),
      items: groups.map((group) => ({
        path: `${group.slug}/index${suffix}.mdx`,
        title: group.title[locale],
        description: group.description[locale],
        only: group.operations.map(
          (operation) => `${group.slug}/${operation.slug}${suffix}.mdx`,
        ),
      })),
    },
    beforeWrite(files) {
      for (const group of groups) {
        const deferrals = renderDeferrals(locale, group);
        if (!deferrals) continue;
        const indexPath = `${group.slug}/index${suffix}.mdx`;
        const index = files.find((file) => file.path === indexPath);
        if (!index) throw new Error(`missing generated OpenAPI index: ${indexPath}`);
        index.content = `${index.content.trimEnd()}\n\n${deferrals}\n`;
      }
    },
  });

  const expected = new Set(
    groups.flatMap((group) => [
      `${group.slug}/index${suffix}.mdx`,
      ...group.operations.map(
        (operation) => `${group.slug}/${operation.slug}${suffix}.mdx`,
      ),
    ]),
  );
  if (files.length !== expected.size || files.some((file) => !expected.has(file.path))) {
    throw new Error(`generated ${contract} OpenAPI files differ from the route registry`);
  }
  return files;
}

const files = (
  await Promise.all(
    (['zh', 'en'] as const).flatMap((locale) =>
      (['golden-path', 'management'] as const).map((contract) =>
        generatedContractFiles(contract, locale),
      ),
    ),
  )
)
  .flat()
  .sort((a, b) => a.path.localeCompare(b.path));

const expectedPaths = new Set(
  productHTTPOpenAPIReferenceGroups.flatMap((group) =>
    (['zh', 'en'] as const).flatMap((locale) => {
      const suffix = localizedSuffix(locale);
      return [
        `${group.slug}/index${suffix}.mdx`,
        ...group.operations.map(
          (operation) => `${group.slug}/${operation.slug}${suffix}.mdx`,
        ),
      ];
    }),
  ),
);

if (files.length !== expectedPaths.size || files.some((file) => !expectedPaths.has(file.path))) {
  throw new Error('generated OpenAPI pages differ from the bilingual route registry');
}

const drifted: string[] = [];
for (const file of files) {
  const target = resolve(generatedRoot, file.path);
  const expected = `${file.content.trimEnd()}\n`;
  if (mode === '--write') {
    await mkdir(dirname(target), { recursive: true });
    await writeFile(target, expected);
    continue;
  }

  let current: string | undefined;
  try {
    current = await readFile(target, 'utf8');
  } catch {
    current = undefined;
  }
  if (current !== expected) drifted.push(file.path);
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
