import { fileURLToPath } from 'node:url';
import {
  domains,
  getAllNavigationEntries,
  getIndexedNavigationEntries,
  locales,
  type Locale,
} from '../lib/navigation';
import { getDomainPublicationCounts } from '../lib/navigation-tree';
import {
  productHTTPOpenAPIContractFiles,
  productHTTPManagementOpenAPIGroups,
  productHTTPMessagingOpenAPIGroups,
  productHTTPOpenAPIContracts,
  productHTTPOpenAPIReferenceGroups,
  productHTTPOpenAPIReferenceOperations,
  type ProductHTTPOpenAPIOperation,
} from '../lib/product-http-openapi';
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
  for (const group of productHTTPManagementOpenAPIGroups) {
    expectedManagementOpenAPIOperations.push(...group.operations);
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

  const messagingOpenAPI = JSON.parse(
    await text('contracts/product-http-messaging.openapi.json'),
  ) as {
    paths?: Record<string, Record<string, { security?: unknown[]; 'x-wukongim-trust'?: string }>>;
    'x-wukongim-scope'?: string;
  };
  const expectedMessagingOperations = productHTTPMessagingOpenAPIGroups.flatMap(
    (group) => group.operations,
  );
  if (
    messagingOpenAPI['x-wukongim-scope'] !==
      'non-exhaustive-trusted-message-sending-beta' ||
    expectedMessagingOperations.length !== 1 ||
    Object.keys(messagingOpenAPI.paths ?? {}).join('\n') !== '/message/send'
  ) {
    throw new Error('message-sending OpenAPI escaped its one-operation boundary');
  }
  for (const expected of expectedMessagingOperations) {
    const operation = messagingOpenAPI.paths?.[expected.path]?.[expected.method];
    if (
      operation?.['x-wukongim-trust'] !== 'trusted-backend-only' ||
      JSON.stringify(operation.security) !== '[]'
    ) {
      throw new Error('message-sending OpenAPI lost its trusted-backend boundary');
    }
  }

  const completeProductOpenAPI = JSON.parse(
    await text('contracts/product-http.openapi.json'),
  ) as {
    paths?: Record<
      string,
      Record<string, { security?: unknown[]; 'x-wukongim-trust'?: string }>
    >;
    'x-wukongim-scope'?: string;
  };
  const actualCompleteOperations = Object.entries(
    completeProductOpenAPI.paths ?? {},
  ).flatMap(([path, item]) => Object.keys(item).map((method) => `${method} ${path}`));
  const expectedCompleteOperations = productHTTPOpenAPIReferenceOperations.map(
    (operation) => `${operation.method} ${operation.path}`,
  );
  if (
    completeProductOpenAPI['x-wukongim-scope'] !==
      'complete-source-aligned-product-http-runtime' ||
    actualCompleteOperations.sort().join('\n') !==
      expectedCompleteOperations.sort().join('\n') ||
    expectedCompleteOperations.length !== 41
  ) {
    throw new Error('complete Product HTTP OpenAPI does not match the 41-operation registry');
  }
  for (const operation of productHTTPOpenAPIReferenceOperations) {
    const contracted = completeProductOpenAPI.paths?.[operation.path]?.[operation.method];
    if (
      JSON.stringify(contracted?.security) !== '[]' ||
      !['trusted-backend-only', 'operator-only', 'node-local-operator-only'].includes(
        contracted?.['x-wukongim-trust'] ?? '',
      )
    ) {
      throw new Error(
        `complete Product HTTP operation lost its trust boundary: ${operation.method.toUpperCase()} ${operation.path}`,
      );
    }
  }

  const operationsOpenAPI = JSON.parse(
    await text('contracts/operations-http.openapi.json'),
  ) as { paths?: Record<string, { get?: unknown }>; 'x-wukongim-scope'?: string };
  if (
    operationsOpenAPI['x-wukongim-scope'] !== 'stable-operations-http-beta' ||
    Object.keys(operationsOpenAPI.paths ?? {}).sort().join('\n') !==
      ['/healthz', '/metrics', '/readyz', '/top/v1/snapshot'].join('\n') ||
    Object.values(operationsOpenAPI.paths ?? {}).some((item) => !item.get)
  ) {
    throw new Error('Operations HTTP OpenAPI lost its exact four-entry boundary');
  }

  const webhooksOpenAPI = JSON.parse(
    await text('contracts/webhooks.openapi.json'),
  ) as {
    paths?: Record<string, unknown>;
    webhooks?: Record<string, { post?: unknown }>;
    'x-wukongim-scope'?: string;
  };
  if (
    webhooksOpenAPI['x-wukongim-scope'] !== 'outbound-webhooks-beta' ||
    Object.keys(webhooksOpenAPI.paths ?? {}).length !== 0 ||
    Object.keys(webhooksOpenAPI.webhooks ?? {}).sort().join('\n') !==
      ['msg.notify', 'msg.offline', 'user.onlinestatus'].join('\n') ||
    Object.values(webhooksOpenAPI.webhooks ?? {}).some((item) => !item.post)
  ) {
    throw new Error('Webhook OpenAPI must use exactly three top-level webhooks');
  }

  const jsonRPCSchema = JSON.parse(
    await text('contracts/json-rpc.experimental.schema.json'),
  ) as { '$schema'?: string; 'x-wukongim-stability'?: string; oneOf?: unknown[] };
  if (
    jsonRPCSchema.$schema !== 'https://json-schema.org/draft/2020-12/schema' ||
    jsonRPCSchema['x-wukongim-stability'] !== 'experimental-not-supported' ||
    jsonRPCSchema.oneOf?.length !== 14
  ) {
    throw new Error('JSON-RPC codec Schema lost its experimental unsupported boundary');
  }

  const operationFacts: Record<string, string[]> = {
    setQuickstartUserToken: ['device_flag'],
    getQuickstartGatewayRoute: ['wss_addr'],
    syncQuickstartChannelMessages: ['pull_mode', 'LegacyMessage', 'message_idstr', '10000'],
    addChannelSubscribers: ['subscribers', 'minItems: 1'],
    setTemporaryChannelSubscribers: ['uids'],
    listConversations: [
      'completed_coverage',
      'ConversationListResponse',
      'ConversationLastMessage',
      'message_idstr',
      'payload',
      'tombstones_retained_since',
    ],
    sendChannelMessage: [
      'SendMessageRequest',
      'client_msg_no',
      'payload',
      'reason',
    ],
  };
  function contractFacts(operation: ProductHTTPOpenAPIOperation) {
    return [
      ...(operationFacts[operation.slug] ?? []),
      'MaintenanceError',
      'restore maintenance is active',
    ];
  }
  for (const locale of locales) {
    for (const group of productHTTPOpenAPIReferenceGroups) {
      const indexHtml = visibleHtml(
        await text(`${locale}/api/product-http/${group.slug}/index.html`),
      );
      for (const operation of group.operations) {
        const href = `/${locale}/api/product-http/${group.slug}/${operation.slug}`;
        if (!indexHtml.includes(href)) {
          throw new Error(`${locale} Product HTTP ${group.slug} index is missing ${href}`);
        }
      }

      if (indexHtml.includes('Request Body') || indexHtml.includes('请求主体')) {
        throw new Error(`${locale} Product HTTP ${group.slug} index is not concise`);
      }
      const indexMarkdown = await text(
        `llms.mdx/${locale}/api/product-http/${group.slug}/content.md`,
      );
      const normalizedIndexMarkdown = indexMarkdown.replaceAll('\\_', '_');
      for (const deferral of group.deferrals?.items ?? []) {
        for (const fact of [...deferral.routes, deferral.reason[locale]]) {
          if (!indexHtml.includes(fact) || !normalizedIndexMarkdown.includes(fact)) {
            throw new Error(
              `${locale} Product HTTP ${group.slug} index is missing deferral: ${fact}`,
            );
          }
        }
      }
    }

    for (const operation of productHTTPOpenAPIReferenceOperations) {
      const route = `${locale}/api/product-http/${operation.groupSlug}/${operation.slug}`;
      const html = visibleHtml(await text(`${route}/index.html`));
      for (const fact of [operation.method.toUpperCase(), operation.path]) {
        if (!html.includes(fact)) {
          throw new Error(`${route} page is missing OpenAPI fact: ${fact}`);
        }
      }
      const requestBodyLabel = locale === 'zh' ? '请求主体' : 'Request Body';
      const responseBodyLabel = locale === 'zh' ? '响应主体' : 'Response Body';
      const contractPaths = productHTTPOpenAPIContracts[
        operation.contract
      ].document.paths as Record<
        string,
        Record<string, { requestBody?: unknown }>
      >;
      const contractPathItem = contractPaths[operation.path] as
        | Record<string, { requestBody?: unknown }>
        | undefined;
      const contractOperation = contractPathItem?.[operation.method];
      if (contractOperation?.requestBody && !html.includes(requestBodyLabel)) {
        throw new Error(`${route} is missing its request schema`);
      }
      if (!html.includes(responseBodyLabel)) {
        throw new Error(`${route} is missing its response schema`);
      }
      if (/<form\b/i.test(html) || /<button\b[^>]*\btype=["']submit["']/i.test(html)) {
        throw new Error(`${route} exposes an interactive playground`);
      }

      const markdown = await text(`llms.mdx/${route}/content.md`);
      for (const fact of [
        `\`${operation.method.toUpperCase()}\``,
        `\`${operation.path}\``,
        ...contractFacts(operation),
        '| `503` |',
      ]) {
        if (!markdown.includes(fact)) {
          throw new Error(`${route} Markdown is missing OpenAPI fact: ${fact}`);
        }
      }
    }

    const errorsHtml = visibleHtml(
      await text(`${locale}/api/product-http/errors/index.html`),
    );
    for (const fact of ['400', '503 maintenance', 'restore maintenance is active']) {
      if (!errorsHtml.includes(fact)) {
        throw new Error(`${locale} Product HTTP error guide is missing shared fact: ${fact}`);
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

    const clientProtocolFacts = {
      'connection-lifecycle': [
        'CONNECT',
        'CONNACK',
        'PING',
        'SENDACK',
        'RECVACK',
        'ReasonAuthFail',
        'ReasonClientKeyIsEmpty',
        'ReasonProtocolUpgradeRequired',
        'TLS',
      ],
      'packet-types': [
        'UNKNOWN',
        'CONNECT',
        'SENDACK',
        'RECVACK',
        'EVENT',
        'terminal-fence',
        'fail closed',
      ],
      'tcp-binary': [
        'remaining_length',
        'WebSocket v13',
        '1 MiB',
        'PING/PONG',
      ],
      'json-rpc': ['JSON-RPC', 'ping', 'connect', 'subscribe'],
      encryption: ['X25519', 'CBC', 'MD5', 'TLS'],
    } as const;
    for (const [page, facts] of Object.entries(clientProtocolFacts)) {
      const markdown = await text(
        `llms.mdx/${locale}/api/client-protocols/${page}/content.md`,
      );
      const html = visibleHtml(
        await text(`${locale}/api/client-protocols/${page}/index.html`),
      );
      for (const fact of facts) {
        if (!markdown.includes(fact) || !html.includes(fact)) {
          throw new Error(`${locale} ${page} output is missing protocol fact: ${fact}`);
        }
      }
    }
    const protocolIndexMarkdown = await text(
      `llms.mdx/${locale}/api/client-protocols/content.md`,
    );
    const protocolIndexHtml = visibleHtml(
      await text(`${locale}/api/client-protocols/index.html`),
    );
    for (const fact of ['JSON-RPC', 'DISCONNECT', 'EVENT']) {
      if (!protocolIndexMarkdown.includes(fact) || !protocolIndexHtml.includes(fact)) {
        throw new Error(`${locale} client-protocol index is missing boundary: ${fact}`);
      }
    }

    const alignedSurfaceFacts = {
      'operations-http/health-and-readiness': ['/healthz', '/readyz', '503'],
      'operations-http/metrics': ['/metrics', 'Prometheus'],
      'operations-http/read-only': ['/top/v1/snapshot', '/debug/', '/bench/v1/'],
      'operations-http/stability': ['auth_on=false', 'Manager', 'Node transport'],
      'webhooks/events': ['msg.notify', 'msg.offline', 'user.onlinestatus'],
      'webhooks/payloads': ['message_idstr', 'compress_to_uids', 'msg.notify'],
      'webhooks/reliability-and-security': ['HTTP `200`', 'Authorization'],
      'specifications/openapi': [
        '/contracts/product-http.openapi.json',
        '/contracts/operations-http.openapi.json',
        '/contracts/webhooks.openapi.json',
      ],
      'specifications/json-rpc-schema': [
        '/contracts/json-rpc.experimental.schema.json',
        'Experimental',
      ],
      'interface-inventory': ['108', '56', 'cluster_health', '/plugin/start'],
    } as const;
    for (const [page, facts] of Object.entries(alignedSurfaceFacts)) {
      const markdown = await text(`llms.mdx/${locale}/api/${page}/content.md`);
      for (const fact of facts) {
        if (!markdown.includes(fact)) {
          throw new Error(`${locale} ${page} Markdown is missing aligned fact: ${fact}`);
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
    'const: `200`',
    'Referenced schema — `LegacyMessage`',
    '`message_idstr`',
    '`/tmpchannel/subscriber_set`',
    '`completed_coverage`',
    'Referenced schema — `ConversationLastMessage`',
    '`payload`',
    '`tombstones_retained_since`',
    '`CompatibilityError`',
    '`MaintenanceError`',
    'restore maintenance is active',
    '`SendMessageRequest`',
    '`client_msg_no`',
    '`RetryRequiredError`',
    '| 0 | `UNKNOWN` |',
    '| 12 | `EVENT` |',
    '`ReasonClientKeyIsEmpty`',
    '`/user/systemuids_add_to_cache`',
    '`/channel/whitelist`',
    '`/conversation/sync`',
    'WebSocket v13',
    'AES-128-CBC',
    'msg.notify',
    '/contracts/operations-http.openapi.json',
    '/contracts/webhooks.openapi.json',
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
    for (const group of productHTTPOpenAPIReferenceGroups) {
      const pageId = `/${locale}/api/product-http/${group.slug}`;
      for (const deferral of group.deferrals?.items ?? []) {
        for (const fact of [...deferral.routes, deferral.reason[locale]]) {
          if (
            !indexedDocuments.some(
              (document) =>
                document.page_id === pageId &&
                document.content?.replaceAll('\\_', '_').includes(fact),
            )
          ) {
            throw new Error(
              `${locale} search index is missing deferral for ${pageId}: ${fact}`,
            );
          }
        }
      }
    }
    for (const operation of productHTTPOpenAPIReferenceOperations) {
      const pageId = `/${locale}/api/product-http/${operation.groupSlug}/${operation.slug}`;
      for (const fact of [operation.path, ...contractFacts(operation)]) {
        if (
          !indexedDocuments.some(
            (document) => document.page_id === pageId && document.content?.includes(fact),
          )
        ) {
          throw new Error(
            `${locale} search index is missing OpenAPI fact for ${pageId}: ${fact}`,
          );
        }
      }
    }
    for (const [page, facts] of Object.entries({
      'connection-lifecycle': [
        'CONNECT',
        'CONNACK',
        'RECVACK',
        'ReasonAuthFail',
        'ReasonClientKeyIsEmpty',
      ],
      'packet-types': ['UNKNOWN', 'EVENT', 'message_seq', 'terminal-fence', 'fail closed'],
      'tcp-binary': ['remaining_length', 'WebSocket v13', '1 MiB', 'PING/PONG'],
      'json-rpc': ['JSON-RPC', 'ping', 'connect', 'subscribe'],
      encryption: ['X25519', 'CBC', 'MD5', 'TLS'],
    })) {
      const pageId = `/${locale}/api/client-protocols/${page}`;
      for (const fact of facts) {
        if (
          !indexedDocuments.some(
            (document) => document.page_id === pageId && document.content?.includes(fact),
          )
        ) {
          throw new Error(
            `${locale} search index is missing protocol fact for ${pageId}: ${fact}`,
          );
        }
      }
    }
    const clientProtocolIndexId = `/${locale}/api/client-protocols`;
    for (const fact of ['JSON-RPC', 'DISCONNECT', 'EVENT']) {
      if (
        !indexedDocuments.some(
          (document) =>
            document.page_id === clientProtocolIndexId && document.content?.includes(fact),
        )
      ) {
        throw new Error(
          `${locale} search index is missing protocol boundary for ${clientProtocolIndexId}: ${fact}`,
        );
      }
    }
    for (const [page, facts] of Object.entries({
      'operations-http/health-and-readiness': ['/healthz', '/readyz'],
      'operations-http/read-only': ['/top/v1/snapshot', '/bench/v1/'],
      'webhooks/events': ['msg.notify', 'msg.offline', 'user.onlinestatus'],
      'webhooks/payloads': ['message_idstr', 'compress_to_uids'],
      'specifications/openapi': ['OpenAPI 3.1', '41', 'webhooks'],
      'interface-inventory': ['108', '56', '/plugin/start'],
    })) {
      const pageId = `/${locale}/api/${page}`;
      for (const fact of facts) {
        if (
          !indexedDocuments.some(
            (document) => document.page_id === pageId && document.content?.includes(fact),
          )
        ) {
          throw new Error(
            `${locale} search index is missing aligned fact for ${pageId}: ${fact}`,
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
    {
      path: `${locale}/api/product-http/users/setQuickstartUserToken/index.html`,
      locale,
    },
    { path: `${locale}/api/product-http/channels/index.html`, locale },
    { path: `${locale}/api/product-http/channels/upsertChannel/index.html`, locale },
    { path: `${locale}/api/product-http/conversations/index.html`, locale },
    {
      path: `${locale}/api/product-http/conversations/listConversations/index.html`,
      locale,
    },
    { path: `${locale}/api/product-http/message-send/index.html`, locale },
    {
      path: `${locale}/api/product-http/message-send/sendChannelMessage/index.html`,
      locale,
    },
    { path: `${locale}/api/client-protocols/index.html`, locale },
    { path: `${locale}/api/client-protocols/connection-lifecycle/index.html`, locale },
    { path: `${locale}/api/client-protocols/packet-types/index.html`, locale },
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
    const openAPIGroup = productHTTPOpenAPIReferenceGroups.find((group) =>
      critical.path.includes(`/api/product-http/${group.slug}/`),
    );
    const editSource = openAPIGroup
      ? `https://github.com/WuKongIM/WuKongIM/edit/main/${productHTTPOpenAPIContractFiles[openAPIGroup.contract].source}`
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
