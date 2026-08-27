import { fileURLToPath } from 'node:url';
import {
  domains,
  getAllNavigationEntries,
  getIndexedNavigationEntries,
  locales,
  type Locale,
} from '../lib/navigation';
import { getDomainPublicationCounts } from '../lib/navigation-tree';
import { productHTTPManagementOpenAPIPages } from '../lib/openapi';
import { canonicalUrl, isPreviewBuild, siteUrl } from '../lib/shared';

const out = new URL('../out/', import.meta.url);

export interface StaticHtmlPage {
  filePath: string;
  html: string;
}

export interface BrokenInternalLink {
  from: string;
  href: string;
  resolvedPath: string;
}

function pageRoute(filePath: string): string {
  const normalized = filePath.replaceAll('\\', '/');
  if (normalized === 'index.html') return '/';
  if (normalized.endsWith('/index.html')) {
    return `/${normalized.slice(0, -'index.html'.length)}`;
  }
  return `/${normalized}`;
}

function outputCandidates(pathname: string): string[] {
  let decoded: string;
  try {
    decoded = decodeURIComponent(pathname);
  } catch {
    decoded = pathname;
  }

  const relative = decoded.replace(/^\/+/, '');
  if (!relative) return ['index.html'];
  if (relative.endsWith('/')) return [`${relative}index.html`];
  return [relative, `${relative}/index.html`];
}

/** Finds document links whose resolved path has no matching static-export artifact. */
export function findBrokenInternalLinks(
  pages: StaticHtmlPage[],
  outputPaths: ReadonlySet<string>,
): BrokenInternalLink[] {
  const broken = new Map<string, BrokenInternalLink>();

  for (const page of pages) {
    const base = new URL(pageRoute(page.filePath), 'https://static-output.invalid');
    const links = page.html.matchAll(
      /<a\b[^>]*\bhref=(?:"([^"]*)"|'([^']*)'|([^\s>]+))/gi,
    );

    for (const match of links) {
      const href = (match[1] ?? match[2] ?? match[3] ?? '').replaceAll('&amp;', '&');
      if (!href || href.startsWith('#') || href.startsWith('//')) continue;

      let resolved: URL;
      try {
        resolved = new URL(href, base);
      } catch {
        continue;
      }
      if (resolved.origin !== base.origin) continue;
      if (outputCandidates(resolved.pathname).some((candidate) => outputPaths.has(candidate))) {
        continue;
      }

      const finding = {
        from: page.filePath,
        href,
        resolvedPath: resolved.pathname,
      };
      broken.set(`${finding.from}\n${finding.href}\n${finding.resolvedPath}`, finding);
    }
  }

  return [...broken.values()].sort((a, b) =>
    `${a.from}\n${a.href}`.localeCompare(`${b.from}\n${b.href}`),
  );
}

/** Performs a small structural accessibility check; it is not a WCAG certification. */
export function getBasicAccessibilityIssues(html: string, locale: Locale): string[] {
  const issues: string[] = [];
  const htmlLanguage = html.match(
    /<html\b[^>]*\blang=(?:"([^"]+)"|'([^']+)'|([^\s>]+))/i,
  );
  const actualLanguage = htmlLanguage?.[1] ?? htmlLanguage?.[2] ?? htmlLanguage?.[3];
  if (actualLanguage !== locale) issues.push(`expected html lang=${locale}`);

  const mainCount = [...html.matchAll(/<main\b/gi)].length;
  if (mainCount !== 1) issues.push('expected exactly one main landmark');

  const headingCount = [...html.matchAll(/<h1\b/gi)].length;
  if (headingCount !== 1) issues.push('expected exactly one h1');

  for (const image of html.matchAll(/<img\b[^>]*>/gi)) {
    if (!/\balt\s*=/.test(image[0])) {
      issues.push('image is missing alt text');
      break;
    }
  }

  return issues;
}

async function text(path: string) {
  const file = Bun.file(new URL(path, out));
  if (!(await file.exists())) throw new Error(`missing static output: ${path}`);
  return file.text();
}

async function exists(path: string) {
  return Bun.file(new URL(path, out)).exists();
}

function visibleHtml(html: string) {
  return html.replace(/<script\b[\s\S]*?<\/script>/gi, '');
}

async function loadOutputInventory() {
  const outputPaths: string[] = [];
  const htmlPages: StaticHtmlPage[] = [];
  const cwd = fileURLToPath(out);

  for await (const filePath of new Bun.Glob('**/*').scan({ cwd, onlyFiles: true })) {
    const normalized = filePath.replaceAll('\\', '/');
    outputPaths.push(normalized);
    if (normalized.endsWith('.html')) {
      htmlPages.push({ filePath: normalized, html: await text(normalized) });
    }
  }

  return {
    outputPaths: new Set(outputPaths),
    htmlPages,
  };
}

function normalizedRoute(pathname: string) {
  if (pathname === '/') return pathname;
  return pathname.replace(/\/+$/, '');
}

export async function checkStaticOutput() {
  const preview = isPreviewBuild();

  for (const locale of locales) {
    const home = await text(`${locale}/index.html`);
    const normalizedHome = home.replace(/<!--[\s\S]*?-->/g, '');
    if (!home.includes(`href="/${locale}/sdk/javascript/quickstart/"`)) {
      throw new Error(`${locale} home is missing the JavaScript application-developer entry`);
    }
    if (
      home.includes('phase-one') ||
      home.includes('Navigation ready · detailed content planned') ||
      home.includes('菜单骨架已就绪，正文规划中')
    ) {
      throw new Error(`${locale} home still contains obsolete phase-one publication copy`);
    }
    for (const domain of domains) {
      const counts = getDomainPublicationCounts(locale, domain.key);
      const publishedLabel = locale === 'zh' ? '已发布' : 'published';
      const plannedLabel = locale === 'zh' ? '规划中' : 'planned';
      if (!normalizedHome.includes(`${counts.published} ${publishedLabel}`)) {
        throw new Error(`${locale} home is missing the ${domain.key} published count`);
      }
      if (!normalizedHome.includes(`${counts.planned} ${plannedLabel}`)) {
        throw new Error(`${locale} home is missing the ${domain.key} planned count`);
      }
    }

    for (const domain of domains) {
      await text(`${locale}/${domain.key}/index.html`);
    }
  }

  const plannedRoutes = locales.flatMap((locale) =>
    getAllNavigationEntries(locale)
      .filter((entry) => entry.status === 'planned')
      .map((entry) => entry.url),
  );
  for (const route of plannedRoutes) {
    const planned = await text(`${route.slice(1)}/index.html`);
    if (!/<meta name="robots" content="[^"]*noindex/.test(planned)) {
      throw new Error(`planned page must carry a noindex directive: ${route}`);
    }
  }

  const compatibility = JSON.parse(await text('compatibility.json')) as {
    schema?: string;
    channel?: string;
    source_revision?: string;
    verified?: boolean;
    verification?: {
      status?: 'verified' | 'missing' | 'malformed' | 'mismatch';
      receipt_schema?: string;
    } | null;
    topology?: string;
    hash_slot_count?: number;
    sdk?: { package?: string; version?: string };
    sample?: {
      scenario?: string;
      node_requirement?: string;
      package_lock_sha256?: string;
    };
    runtime?: {
      node?: string;
      package_manager?: string;
      browser?: {
        engine?: string;
        playwright_package?: string;
        playwright_version?: string;
        revision?: string;
        browser_version?: string;
        other_browsers?: string;
      };
    };
    contracts?: { openapi?: string; compatibility?: string };
  };
  const requireVerified = process.env.WK_DOCS_REQUIRE_VERIFIED === '1';
  const verificationHasExactShape =
    compatibility.verification !== undefined &&
    compatibility.verification !== null &&
    typeof compatibility.verification === 'object' &&
    !Array.isArray(compatibility.verification) &&
    Object.keys(compatibility.verification).sort().join(',') === 'receipt_schema,status';
  const verificationIsConsistent =
    verificationHasExactShape &&
    typeof compatibility.verified === 'boolean' &&
    ['verified', 'missing', 'malformed', 'mismatch'].includes(
      compatibility.verification?.status ?? '',
    ) &&
    compatibility.verification?.receipt_schema ===
      'wukongim.docs.golden-path-verification/v1' &&
    compatibility.verified === (compatibility.verification?.status === 'verified');
  const verificationMeetsBuildMode = requireVerified
    ? compatibility.verified === true && compatibility.verification?.status === 'verified'
    : compatibility.verified === false && compatibility.verification?.status === 'missing';
  if (
    compatibility.schema !== 'wukongim.docs.compatibility/v1' ||
    compatibility.channel !== 'v3-beta-snapshot' ||
    !/^[a-f0-9]{40}([a-f0-9]{24})?$/.test(compatibility.source_revision ?? '') ||
    !verificationIsConsistent ||
    !verificationMeetsBuildMode ||
    compatibility.topology !== 'single-node cluster' ||
    compatibility.hash_slot_count !== 256 ||
    compatibility.sdk?.package !== 'wukongimjssdk' ||
    compatibility.sdk.version !== '1.3.5' ||
    compatibility.sample?.scenario !==
      'javascript-web-quickstart/alice-bob-reconnect-sync/v1' ||
    compatibility.sample.node_requirement !== '>=20.11' ||
    !/^[a-f0-9]{64}$/.test(compatibility.sample.package_lock_sha256 ?? '') ||
    compatibility.runtime?.node !== '22.12.0' ||
    compatibility.runtime.package_manager !== 'npm' ||
    compatibility.runtime.browser?.engine !== 'chromium' ||
    compatibility.runtime.browser.playwright_package !== '@playwright/test' ||
    compatibility.runtime.browser.playwright_version !== '1.62.1' ||
    compatibility.runtime.browser.revision !== '1234' ||
    compatibility.runtime.browser.browser_version !== '151.0.7922.34' ||
    compatibility.runtime.browser.other_browsers !== 'unverified' ||
    compatibility.contracts?.openapi !==
      '/contracts/javascript-web-quickstart.openapi.json' ||
    compatibility.contracts.compatibility !== '/compatibility.json'
  ) {
    throw new Error('compatibility.json does not identify the reproducible golden-path snapshot');
  }

  const openapi = JSON.parse(
    await text('contracts/javascript-web-quickstart.openapi.json'),
  ) as {
    paths?: Record<string, unknown>;
    'x-wukongim-scope'?: string;
  };
  const openapiPaths = Object.keys(openapi.paths ?? {});
  if (
    openapi['x-wukongim-scope'] !== 'non-exhaustive-v3-beta-snapshot' ||
    openapiPaths.join('\n') !== ['/user/token', '/route', '/channel/messagesync'].join('\n')
  ) {
    throw new Error('golden-path OpenAPI artifact escaped its three-endpoint Beta boundary');
  }

  const managementOpenAPI = JSON.parse(
    await text('contracts/product-http-management.openapi.json'),
  ) as {
    paths?: Record<string, Record<string, { security?: unknown[]; 'x-wukongim-trust'?: string }>>;
    'x-wukongim-scope'?: string;
  };
  const managementOpenAPIPaths = Object.keys(managementOpenAPI.paths ?? {});
  const expectedManagementOpenAPIOperations: Array<{ method: string; path: string }> = [];
  for (const page of productHTTPManagementOpenAPIPages) {
    expectedManagementOpenAPIOperations.push(...page.operations);
  }
  const expectedManagementOpenAPIPaths = expectedManagementOpenAPIOperations.map(
    (operation) => operation.path,
  );
  if (
    managementOpenAPI['x-wukongim-scope'] !==
      'non-exhaustive-trusted-product-management-beta' ||
    managementOpenAPIPaths.join('\n') !== expectedManagementOpenAPIPaths.join('\n')
  ) {
    throw new Error('management OpenAPI artifact escaped its reviewed 16-operation boundary');
  }
  for (const expected of expectedManagementOpenAPIOperations) {
    const operation = managementOpenAPI.paths?.[expected.path]?.[expected.method];
    if (
      operation?.['x-wukongim-trust'] !== 'trusted-backend-only' ||
      JSON.stringify(operation.security) !== '[]'
    ) {
      throw new Error(
        `management OpenAPI operation lost its trust boundary: ${expected.method.toUpperCase()} ${expected.path}`,
      );
    }
  }

  const openAPIPages = [
    {
      slug: 'users',
      method: 'POST',
      path: '/user/token',
      requestBody: true,
      field: '`device_flag`',
      enDescription: 'Compatibility endpoint used by the localhost BFF before a browser connects',
      zhDescription: '浏览器连接前由 localhost BFF 调用的兼容入口',
    },
    {
      slug: 'routing',
      method: 'GET',
      path: '/route',
      requestBody: false,
      field: '`wss_addr`',
      enDescription: 'Returns configured gateway addresses',
      zhDescription: '返回已配置的 Gateway 地址',
    },
    {
      slug: 'messages',
      method: 'POST',
      path: '/channel/messagesync',
      requestBody: true,
      field: '`pull_mode`',
      enDescription: 'Compatibility endpoint used by the localhost BFF after reconnect',
      zhDescription: '重连后由 localhost BFF 使用的兼容入口',
    },
  ] as const;
  for (const locale of locales) {
    for (const page of openAPIPages) {
      const html = visibleHtml(
        await text(`${locale}/api/product-http/${page.slug}/index.html`),
      );
      const description = locale === 'zh' ? page.zhDescription : page.enDescription;
      for (const fact of [page.method, page.path, page.field.slice(1, -1), description]) {
        if (!html.includes(fact)) {
          throw new Error(
            `${locale} Product HTTP ${page.slug} page is missing OpenAPI fact: ${fact}`,
          );
        }
      }
      const requestBodyLabel = locale === 'zh' ? '请求主体' : 'Request Body';
      const responseBodyLabel = locale === 'zh' ? '响应主体' : 'Response Body';
      if (page.requestBody && !html.includes(requestBodyLabel)) {
        throw new Error(`${locale} Product HTTP ${page.slug} is missing its request schema`);
      }
      if (!html.includes(responseBodyLabel)) {
        throw new Error(`${locale} Product HTTP ${page.slug} is missing its response schema`);
      }
      if (/<form\b/i.test(html) || /<button\b[^>]*\btype=["']submit["']/i.test(html)) {
        throw new Error(`${locale} Product HTTP ${page.slug} exposes an interactive playground`);
      }

      const markdown = await text(
        `llms.mdx/${locale}/api/product-http/${page.slug}/content.md`,
      );
      for (const fact of [`\`${page.method}\``, `\`${page.path}\``, page.field, '| `503` |']) {
        if (!markdown.includes(fact)) {
          throw new Error(
            `${locale} Product HTTP ${page.slug} Markdown is missing OpenAPI fact: ${fact}`,
          );
        }
      }
      if (page.slug === 'messages' && !markdown.includes('1–100')) {
        throw new Error(`${locale} Product HTTP messages Markdown lost the limit constraint`);
      }
    }
  }

  const managementOpenAPIPages = [
    {
      slug: 'channels',
      facts: ['/channel', '/tmpchannel/subscriber_set', 'temp_subscriber'],
      schemaFacts: [
        'NonBlankUID',
        'CompatibilityError',
        'MaintenanceError',
        'restore maintenance is active',
      ],
      enDescription: 'Creates or updates one Channel',
      zhDescription: '创建或更新一个 Channel',
    },
    {
      slug: 'conversations',
      facts: ['/conversation/list', '/conversations/activate', 'completed_coverage'],
      schemaFacts: [
        'ConversationListResponse',
        'ConversationLastMessage',
        'message_idstr',
        'payload',
        'tombstones_retained_since',
        'CompatibilityError',
        'MaintenanceError',
        'restore maintenance is active',
      ],
      enDescription: 'Canonical endpoint that incrementally scans',
      zhDescription: 'Canonical 入口会增量扫描',
    },
  ] as const;
  for (const locale of locales) {
    for (const page of managementOpenAPIPages) {
      const html = visibleHtml(
        await text(`${locale}/api/product-http/${page.slug}/index.html`),
      );
      const description = locale === 'zh' ? page.zhDescription : page.enDescription;
      for (const fact of ['POST', ...page.facts, ...page.schemaFacts, description]) {
        if (!html.includes(fact)) {
          throw new Error(
            `${locale} Product HTTP ${page.slug} page is missing management OpenAPI fact: ${fact}`,
          );
        }
      }
      const requestBodyLabel = locale === 'zh' ? '请求主体' : 'Request Body';
      const responseBodyLabel = locale === 'zh' ? '响应主体' : 'Response Body';
      if (!html.includes(requestBodyLabel) || !html.includes(responseBodyLabel)) {
        throw new Error(`${locale} Product HTTP ${page.slug} lost its management schemas`);
      }
      if (/<form\b/i.test(html) || /<button\b[^>]*\btype=["']submit["']/i.test(html)) {
        throw new Error(`${locale} Product HTTP ${page.slug} exposes an interactive playground`);
      }

      const markdown = await text(
        `llms.mdx/${locale}/api/product-http/${page.slug}/content.md`,
      );
      for (const fact of [...page.facts, ...page.schemaFacts]) {
        if (!markdown.includes(fact)) {
          throw new Error(
            `${locale} Product HTTP ${page.slug} Markdown is missing management OpenAPI fact: ${fact}`,
          );
        }
      }
      if (!markdown.includes('| `503` |')) {
        throw new Error(`${locale} Product HTTP ${page.slug} Markdown lost maintenance`);
      }
    }
  }

  for (const locale of locales) {
    for (const entry of getIndexedNavigationEntries(locale)) {
      const published = await text(`${entry.url.slice(1)}/index.html`);
      const noindex = /<meta name="robots" content="[^"]*noindex/.test(published);
      if (preview ? !noindex : noindex) {
        throw new Error(
          preview
            ? `preview page must carry a noindex directive: ${entry.url}`
            : `published page must be indexable: ${entry.url}`,
        );
      }
      if (!published.includes(canonicalUrl(entry.url))) {
        throw new Error(`published page is missing its trailing-slash canonical: ${entry.url}`);
      }
    }
  }

  const sitemap = await text('sitemap.xml');
  const sitemapUrls = [...sitemap.matchAll(/<loc>(.*?)<\/loc>/g)].map((match) => match[1]);
  const expectedSitemapPaths = locales.flatMap((locale) => [
    `/${locale}`,
    ...getIndexedNavigationEntries(locale).map((entry) => entry.url),
  ]);
  const actualSitemapPaths = sitemapUrls.map((url) => normalizedRoute(new URL(url).pathname));
  if (actualSitemapPaths.sort().join('\n') !== expectedSitemapPaths.sort().join('\n')) {
    throw new Error(
      `sitemap routes differ from the publication registry:\n${actualSitemapPaths.join('\n')}`,
    );
  }
  for (const url of sitemapUrls) {
    const parsed = new URL(url);
    if (parsed.origin !== siteUrl || !parsed.pathname.endsWith('/')) {
      throw new Error(`sitemap URL is not a normalized canonical page URL: ${url}`);
    }
  }
  if (sitemap.includes('<lastmod>')) {
    throw new Error('sitemap must not synthesize lastModified from the build time');
  }
  for (const route of plannedRoutes) {
    if (sitemap.includes(route)) {
      throw new Error(`planned page must not appear in sitemap.xml: ${route}`);
    }
  }

  for (const locale of locales) {
    const quickstartMarkdown = await text(
      `llms.mdx/${locale}/sdk/javascript/quickstart/content.md`,
    );
    for (const fact of [
      'wukongimjssdk@1.3.5',
      'POST /user/token',
      'GET /route',
      'POST /channel/messagesync',
    ]) {
      if (!quickstartMarkdown.includes(fact)) {
        throw new Error(`${locale} quickstart Markdown is missing shared fact: ${fact}`);
      }
    }
    const reasonMarkdown = await text(
      `llms.mdx/${locale}/api/dictionaries/reason-codes/content.md`,
    );
    for (const fact of ['ReasonUnknown', 'ReasonMessageSeqExhausted']) {
      if (!reasonMarkdown.includes(fact)) {
        throw new Error(`${locale} ReasonCode Markdown is missing shared fact: ${fact}`);
      }
    }
    const protocolDictionaryFacts = {
      'channel-types': ['| 1 | `ChannelTypePerson` |', '| 12 | `ChannelTypeAgentGroup` |'],
      'device-flags': [
        '| 0 | `APP` |',
        '| 99 | `SYSTEM` |',
        '| 0 | `DeviceLevelSlave` |',
        '| 1 | `DeviceLevelMaster` |',
      ],
      'message-flags': [
        '| 0 | `NoPersist` |',
        '| 3 | `DUP` |',
        '| 128 | `SettingReceiptEnabled` |',
        '| 2 | `SettingStream` |',
      ],
    } as const;
    for (const [dictionary, facts] of Object.entries(protocolDictionaryFacts)) {
      const markdown = await text(
        `llms.mdx/${locale}/api/dictionaries/${dictionary}/content.md`,
      );
      for (const fact of facts) {
        if (!markdown.includes(fact)) {
          throw new Error(`${locale} ${dictionary} Markdown is missing shared fact: ${fact}`);
        }
      }
    }

    const capabilityMarkdown = await text(
      `llms.mdx/${locale}/sdk/javascript/platform-capabilities/content.md`,
    );
    const capabilityFacts = [
      '`route-connect`',
      locale === 'zh' ? '场景覆盖' : 'Scenario-covered',
      '`production-connection-authentication`',
      locale === 'zh' ? '边界' : 'Boundary',
      '`transient-and-background-behavior`',
      locale === 'zh' ? '未验证' : 'Unverified',
    ];
    for (const fact of capabilityFacts) {
      if (!capabilityMarkdown.includes(fact)) {
        throw new Error(`${locale} capability Markdown is missing shared fact: ${fact}`);
      }
    }

    const acceptanceMarkdown = await text(
      `llms.mdx/${locale}/guide/integration/acceptance/content.md`,
    );
    for (const fact of [
      'wukongim.docs.integration-acceptance/v1',
      'not_assessed',
      'publication_attestation',
    ]) {
      if (!acceptanceMarkdown.includes(fact)) {
        throw new Error(`${locale} acceptance Markdown is missing boundary: ${fact}`);
      }
    }
  }

  const llmsIndex = await text('llms.txt');
  const llmsFull = await text('llms-full.txt');
  for (const fact of [
    'server-generated-development-secret',
    'const: `200`',
    'Referenced schema — `SyncedMessage`',
    '`message_idstr`',
    '`/tmpchannel/subscriber_set`',
    '`completed_coverage`',
    'Referenced schema — `ConversationLastMessage`',
    '`payload`',
    '`tombstones_retained_since`',
    '`CompatibilityError`',
    '`MaintenanceError`',
    'restore maintenance is active',
  ]) {
    if (!llmsFull.includes(fact)) {
      throw new Error(`llms-full.txt is missing OpenAPI fact: ${fact}`);
    }
  }
  const llms = `${llmsIndex}\n${llmsFull}`;
  for (const locale of locales) {
    for (const entry of getIndexedNavigationEntries(locale)) {
      if (!llms.includes(entry.url)) {
        throw new Error(`missing published LLM route: ${entry.url}`);
      }
      await text(`llms.mdx${entry.url}/content.md`);
    }
  }
  for (const route of plannedRoutes) {
    if (llmsIndex.includes(route)) {
      throw new Error(`planned page must not appear in llms.txt: ${route}`);
    }
    if (await exists(`llms.mdx${route}/content.md`)) {
      throw new Error(`planned page has per-page Markdown output: ${route}`);
    }
  }

  const searchPayload = await text('api/search');
  const search = JSON.parse(searchPayload) as {
    type: string;
    data: Record<
      string,
      {
        internalDocumentIDStore: { internalIdToId: string[] };
        docs?: { docs?: Record<string, { page_id?: string; content?: string }> };
      }
    >;
  };
  if (search.type !== 'i18n') throw new Error('search index must be locale-aware');
  if (Object.keys(search.data).sort().join(',') !== 'en,zh') {
    throw new Error('search index must contain exactly the en and zh locales');
  }
  for (const locale of locales) {
    const ids = search.data[locale]?.internalDocumentIDStore.internalIdToId ?? [];
    if (ids.length === 0 || ids.some((id) => !id.startsWith(`/${locale}/`))) {
      throw new Error(`${locale} search index contains cross-language documents`);
    }
    for (const entry of getIndexedNavigationEntries(locale)) {
      if (!ids.includes(entry.url)) {
        throw new Error(`${locale} search index is missing ${entry.url}`);
      }
    }
    const localePlannedRoutes = plannedRoutes.filter((route) => route.startsWith(`/${locale}/`));
    if (ids.some((id) => localePlannedRoutes.includes(id))) {
      throw new Error(`${locale} search index contains a planned page`);
    }
    const indexedDocuments = Object.values(search.data[locale]?.docs?.docs ?? {});
    for (const page of openAPIPages) {
      const pageId = `/${locale}/api/product-http/${page.slug}`;
      const description = locale === 'zh' ? page.zhDescription : page.enDescription;
      if (
        !indexedDocuments.some(
          (document) =>
            document.page_id === pageId && document.content?.includes(description),
        )
      ) {
        throw new Error(`${locale} search index is missing OpenAPI text for ${pageId}`);
      }
    }
    for (const page of managementOpenAPIPages) {
      const pageId = `/${locale}/api/product-http/${page.slug}`;
      const description = locale === 'zh' ? page.zhDescription : page.enDescription;
      for (const fact of [description, ...page.facts, ...page.schemaFacts]) {
        if (
          !indexedDocuments.some(
            (document) => document.page_id === pageId && document.content?.includes(fact),
          )
        ) {
          throw new Error(
            `${locale} search index is missing management OpenAPI fact for ${pageId}: ${fact}`,
          );
        }
      }
    }
  }

  const robots = await text('robots.txt');
  if (preview ? !robots.includes('Disallow: /') : !robots.includes('Allow: /')) {
    throw new Error(`robots.txt does not match the ${preview ? 'preview' : 'production'} policy`);
  }

  const criticalPages = locales.flatMap((locale) => [
    { path: `${locale}/index.html`, locale },
    { path: `${locale}/sdk/common-guides/index.html`, locale },
    { path: `${locale}/sdk/javascript/quickstart/index.html`, locale },
    { path: `${locale}/sdk/javascript/platform-capabilities/index.html`, locale },
    { path: `${locale}/guide/integration/acceptance/index.html`, locale },
    { path: `${locale}/api/dictionaries/message-flags/index.html`, locale },
    { path: `${locale}/api/product-http/users/index.html`, locale },
    { path: `${locale}/api/product-http/channels/index.html`, locale },
    { path: `${locale}/api/product-http/conversations/index.html`, locale },
  ]);
  for (const critical of criticalPages) {
    const html = await text(critical.path);
    const issues = getBasicAccessibilityIssues(html, critical.locale);
    if (issues.length > 0) {
      throw new Error(`basic accessibility structure failed for ${critical.path}: ${issues.join(', ')}`);
    }
    if (critical.path === `${critical.locale}/index.html`) continue;
    const feedbackLabel = critical.locale === 'zh' ? '报告文档问题' : 'Report a docs issue';
    const editLabel = critical.locale === 'zh' ? '编辑此页' : 'Edit this page';
    const editSource = critical.path.includes('/api/product-http/')
      ? 'https://github.com/WuKongIM/WuKongIM/edit/main/docs-site/content/openapi/product-http/'
      : 'https://github.com/WuKongIM/WuKongIM/edit/main/docs-site/content/docs/';
    if (
      !html.includes(feedbackLabel) ||
      !html.includes('https://github.com/WuKongIM/WuKongIM/issues/new?')
    ) {
      throw new Error(`published page is missing prefilled documentation feedback: ${critical.path}`);
    }
    if (
      !html.includes(editLabel) ||
      !html.includes(editSource)
    ) {
      throw new Error(`published page is missing its edit link: ${critical.path}`);
    }
  }

  const { htmlPages, outputPaths } = await loadOutputInventory();
  const brokenLinks = findBrokenInternalLinks(htmlPages, outputPaths);
  if (brokenLinks.length > 0) {
    const summary = brokenLinks
      .slice(0, 20)
      .map((link) => `${link.from}: ${link.href} -> ${link.resolvedPath}`)
      .join('\n');
    throw new Error(`broken internal routes found in static output:\n${summary}`);
  }

  console.log('static output contract passed');
}

if (import.meta.main) await checkStaticOutput();
