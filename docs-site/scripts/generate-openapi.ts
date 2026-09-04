import { generateFilesOnly } from 'fumadocs-openapi';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createProductHTTPOpenAPIContract } from '../lib/openapi';
import { getOpenAPISearchStructuredData } from '../lib/openapi-markdown';
import {
  productHTTPOpenAPIReferenceContractNames,
  productHTTPOpenAPIReferenceGroups,
  productHTTPOpenAPIContracts,
  type ProductHTTPOpenAPIContract,
  type ProductHTTPOpenAPILocale,
} from '../lib/product-http-openapi';
import { localizeProductHTTPOperationSemantics } from '../lib/product-http-operation-semantics';

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

function operationToc(
  locale: ProductHTTPOpenAPILocale,
  operation: (typeof productHTTPOpenAPIReferenceGroups)[number]['operations'][number],
) {
  const document = productHTTPOpenAPIContracts[operation.contract].document as {
    paths: Record<string, Record<string, {
      parameters?: Array<{ in?: string; $ref?: string }>;
      requestBody?: unknown;
      responses?: unknown;
    }>>;
    components?: { parameters?: Record<string, { in?: string }> };
  };
  const candidate = document.paths[operation.path]?.[operation.method];
  if (!candidate) throw new Error(`missing OpenAPI operation: ${operation.method} ${operation.path}`);
  const labels = locale === 'zh'
    ? {
        path: '路径参数',
        query: '查询参数',
        header: '请求头参数',
        cookie: 'Cookie 参数',
        request: '请求体',
        response: '响应体',
      }
    : {
        path: 'Path Parameters',
        query: 'Query Parameters',
        header: 'Header Parameters',
        cookie: 'Cookie Parameters',
        request: 'Request Body',
        response: 'Response Body',
      };
  const parameterTypes = new Set(
    (candidate.parameters ?? []).flatMap((parameter) => {
      if (parameter.in) return [parameter.in];
      const name = parameter.$ref?.split('/').at(-1);
      const location = name ? document.components?.parameters?.[name]?.in : undefined;
      return location ? [location] : [];
    }),
  );
  const toc = [...parameterTypes]
    .filter((type): type is keyof Pick<typeof labels, 'path' | 'query' | 'header' | 'cookie'> =>
      type === 'path' || type === 'query' || type === 'header' || type === 'cookie')
    .map((type) => ({ depth: 2, title: labels[type], url: `#parameters-${type}` }));
  if (candidate.requestBody) {
    toc.push({ depth: 2, title: labels.request, url: '#request-body' });
  }
  if (candidate.responses) {
    toc.push({ depth: 2, title: labels.response, url: '#response-body' });
  }
  return toc;
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

const trustLabels = {
  'trusted-backend-only': {
    zh: '仅限受信业务后端',
    en: 'Trusted backend only',
  },
  'operator-only': {
    zh: '仅限受保护的运维边界',
    en: 'Protected operator boundary only',
  },
  'node-local-operator-only': {
    zh: '仅限当前节点的受保护运维边界',
    en: 'Protected current-node operator boundary only',
  },
} as const;

function renderOperationBoundary(
  locale: ProductHTTPOpenAPILocale,
  operation: (typeof productHTTPOpenAPIReferenceGroups)[number]['operations'][number],
) {
  const trust = trustLabels[operation.trust as keyof typeof trustLabels];
  if (!trust) throw new Error(`unknown Product HTTP trust tier: ${operation.trust}`);
  const title = locale === 'zh' ? '接口边界' : 'Operation boundary';
  const labels =
    locale === 'zh'
      ? { trust: '调用方', scope: '作用域', atomicity: '原子性', success: '成功判定', recovery: '恢复' }
      : { trust: 'Caller', scope: 'Scope', atomicity: 'Atomicity', success: 'Success', recovery: 'Recovery' };
  const separator = locale === 'zh' ? '：' : ': ';
  const lines = [`- **${labels.trust}**${separator}${trust[locale]}`];
  if (operation.semantics) {
    const semantics = localizeProductHTTPOperationSemantics(
      operation.semantics,
      locale,
    );
    for (const field of ['scope', 'atomicity', 'success', 'recovery'] as const) {
      if (semantics[field]) lines.push(`- **${labels[field]}**${separator}${semantics[field]}`);
    }
  }
  return [`<Callout type="warn" title="${title}">`, '', ...lines, '', '</Callout>'].join('\n');
}

function appendAfterFrontmatter(content: string, addition: string) {
  const end = content.indexOf('\n---', 4);
  if (end < 0) throw new Error('generated OpenAPI page has no closing frontmatter');
  const insertAt = end + '\n---'.length;
  return `${content.slice(0, insertAt)}\n\n${addition}${content.slice(insertAt)}`;
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
    input: createProductHTTPOpenAPIContract(contract, locale),
    per: 'operation',
    groupBy: 'tag',
    slugify(name) {
      return groups.find((group) => group.tag === name)?.slug ?? name.replace(/\s+/g, '-').toLowerCase();
    },
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
      return { _openapi: { toc: operationToc(locale, operation), structuredData } };
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
      for (const operation of operations) {
        const operationPath = `${operation.groupSlug}/${operation.slug}${suffix}.mdx`;
        const page = files.find((file) => file.path === operationPath);
        if (!page) throw new Error(`missing generated OpenAPI operation: ${operationPath}`);
        page.content = appendAfterFrontmatter(
          page.content,
          renderOperationBoundary(locale, operation),
        );
      }
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
      productHTTPOpenAPIReferenceContractNames.map((contract) =>
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
