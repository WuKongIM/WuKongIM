import openapiDocument from '../contracts/javascript-web-quickstart.openapi.json';
import { describe, expect, test } from 'bun:test';
import {
  createProductHTTPOpenAPI,
  createProductHTTPOpenAPIContract,
  productHTTPCompleteOpenAPIDocumentIds,
  productHTTPManagementOpenAPIDocumentIds,
  productHTTPMessagingOpenAPIDocumentIds,
  productHTTPOpenAPIDocumentId,
  productHTTPOpenAPIDocumentIds,
} from './openapi';
import { renderOpenAPIOperationMarkdown } from './openapi-markdown';
import {
  productHTTPOpenAPIContractFiles,
  productHTTPOpenAPIContractNames,
  productHTTPOpenAPIContracts,
} from './product-http-openapi';

const documentId = 'wukongim-product-http-beta';
interface TestedOperation {
  tags?: string[];
  'x-wukongim-trust'?: string;
  'x-codeSamples'?: Array<{ lang?: string; label?: string; source?: string }>;
}

const operationPages = [
  {
    groupSlug: 'users',
    slug: 'setQuickstartUserToken',
    method: 'post',
    path: '/user/token',
    tag: 'Users',
  },
  {
    groupSlug: 'routing',
    slug: 'getQuickstartGatewayRoute',
    method: 'get',
    path: '/route',
    tag: 'Routing',
  },
  {
    groupSlug: 'messages',
    slug: 'syncQuickstartChannelMessages',
    method: 'post',
    path: '/channel/messagesync',
    tag: 'Messages',
  },
] as const;

async function source(relativePath: string) {
  return Bun.file(new URL(relativePath, import.meta.url)).text();
}

describe('Fumadocs OpenAPI integration', () => {
  test('pins a Fumadocs OpenAPI release compatible with the current UI runtime', async () => {
    const packageJson = JSON.parse(await source('../package.json')) as {
      dependencies?: Record<string, string>;
      scripts?: Record<string, string>;
    };

    expect(packageJson.dependencies?.['fumadocs-openapi']).toBe('11.2.4');
    expect(packageJson.dependencies?.shiki).toBe('4.4.3');
    expect(packageJson.dependencies?.['@fumadocs/language']).toBe('0.2.4');
    expect(packageJson.scripts?.['openapi:write']).toContain('--write');
    expect(packageJson.scripts?.['openapi:check']).toContain('--check');
    expect(packageJson.scripts?.verify).toContain('openapi:check');
  });

  test('keeps every published operation tagged and safe for generated examples', () => {
    const tagNames = openapiDocument.tags?.map((tag) => tag.name);
    const paths = openapiDocument.paths as Record<
      string,
      Partial<Record<'get' | 'post', TestedOperation>>
    >;

    for (const operationPage of operationPages) {
      const operation = paths[operationPage.path]?.[operationPage.method];

      expect(tagNames).toContain(operationPage.tag);
      expect(operation?.tags).toEqual([operationPage.tag]);
      expect(operation?.['x-wukongim-trust']).toBe('trusted-backend-only');
      expect(operation?.['x-codeSamples']).toHaveLength(1);
      expect(operation?.['x-codeSamples']?.[0]).toMatchObject({
        lang: 'bash',
        label: 'Trusted backend (cURL)',
      });
      expect(operation?.['x-codeSamples']?.[0]?.source).toContain('127.0.0.1:5001');
    }

    expect(
      openapiDocument.components.responses.MaintenanceUnavailable.content["application/json"]
        .schema.$ref,
    ).toBe('#/components/schemas/MaintenanceError');
    expect(openapiDocument.components.schemas.MaintenanceError.properties.error.const).toBe(
      'maintenance',
    );
    expect(openapiDocument.components.schemas.MaintenanceError.properties.message.const).toBe(
      'restore maintenance is active',
    );
  });

  test('wires the server preload, localized UI, stylesheet, and disabled playground', async () => {
    const [page, component, openapi, registry, layout, stylesheet] = await Promise.all([
      source('../app/[lang]/(docs)/[section]/[[...slug]]/page.tsx'),
      source('../components/openapi-page.tsx'),
      source('./openapi.ts'),
      source('./product-http-openapi.ts'),
      source('./layout.shared.tsx'),
      source('../app/global.css'),
    ]);

    expect(page).toContain('openapi.preloadOpenAPIPage(page)');
    expect(page).toContain('OpenAPIPage: async');
    expect(page).toContain('page.data._openapi');
    expect(page).toContain('productHTTPOpenAPIContractFiles');
    expect(component).toContain('createOpenAPIPage');
    expect(component).toContain('playground: { enabled: false }');
    expect(component).toContain('createCodeUsageGeneratorRegistry()');
    expect(productHTTPOpenAPIDocumentId).toBe(documentId);
    expect(openapi).toContain('localizeOpenAPIDocument');
    expect(openapi).toContain('productHTTPOpenAPIContracts');
    expect(registry).toContain('javascript-web-quickstart.openapi.json');
    expect(openapi).not.toContain('resources/api/openapi.json');
    expect(layout).toContain('.extend(openapiTranslations())');
    expect(layout).toContain(".preset('zh', zhCN())");
    expect(stylesheet).toContain("@import 'fumadocs-openapi/css/preset.css';");
  });

  test('keeps contract documents and publication metadata in one typed registry', async () => {
    const exactDocumentIds: [
      'wukongim-product-http-complete-beta-zh',
      'wukongim-product-http-beta-zh',
      'wukongim-product-http-management-beta-en',
      'wukongim-product-http-messaging-beta-zh',
    ] = [
      productHTTPCompleteOpenAPIDocumentIds.zh,
      productHTTPOpenAPIDocumentIds.zh,
      productHTTPManagementOpenAPIDocumentIds.en,
      productHTTPMessagingOpenAPIDocumentIds.zh,
    ];
    expect(exactDocumentIds).toEqual([
      'wukongim-product-http-complete-beta-zh',
      'wukongim-product-http-beta-zh',
      'wukongim-product-http-management-beta-en',
      'wukongim-product-http-messaging-beta-zh',
    ]);

    expect(Object.keys(productHTTPOpenAPIContracts)).toEqual(
      [...productHTTPOpenAPIContractNames],
    );

    for (const contract of productHTTPOpenAPIContractNames) {
      const descriptor = productHTTPOpenAPIContracts[contract];
      expect(descriptor.document).toHaveProperty('paths');
      expect(descriptor.source).toStartWith('docs-site/contracts/');
      expect(descriptor.download).toStartWith('/contracts/');
      expect(descriptor.documentId).toStartWith('wukongim-product-http');
      expect(descriptor.label.zh).not.toBe('');
      expect(descriptor.label.en).not.toBe('');
      expect(descriptor.llmScope.zh).toContain('Fumadocs');
      expect(descriptor.llmScope.en).toContain('Fumadocs');
      expect(productHTTPOpenAPIContractFiles[contract].source).toBe(descriptor.source);
      expect(productHTTPOpenAPIContractFiles[contract].download).toBe(
        descriptor.download,
      );

      const server = createProductHTTPOpenAPIContract(contract, 'en');
      const schema = await server.getSchema(`${descriptor.documentId}-en`);
      expect(schema.bundled.paths).toBeDefined();
    }
  });

  test('generates bilingual operation pages from the complete contract', async () => {
    for (const operationPage of operationPages) {
      for (const suffix of ['.mdx', '.en.mdx']) {
        const locale = suffix === '.mdx' ? 'zh' : 'en';
        const localizedDocumentId = productHTTPCompleteOpenAPIDocumentIds[locale];
        const page = await source(
          `../content/docs/api/product-http/${operationPage.groupSlug}/${operationPage.slug}${suffix}`,
        );

        expect(page).toContain('generated by Fumadocs');
        expect(page).toMatch(/full: true/);
        expect(page).toContain(`- ${localizedDocumentId}`);
        expect(page).toContain(`document=\"${localizedDocumentId}\"`);
        expect(page).toContain(
          `operations={[{\"path\":\"${operationPage.path}\",\"method\":\"${operationPage.method}\"}]}`,
        );
        expect(page).toContain('const Comp = OpenAPIPage ?? APIPage');
        expect(page).not.toMatch(/^## (Contract|合同)$/m);
      }
    }
  });

  test('applies the Chinese text overlay without duplicating operation structure', async () => {
    const zhServer = createProductHTTPOpenAPI('zh');
    const enServer = createProductHTTPOpenAPI('en');
    const zh = (await zhServer.getSchema(productHTTPOpenAPIDocumentIds.zh)).bundled;
    const en = (await enServer.getSchema(productHTTPOpenAPIDocumentIds.en)).bundled;

    expect(Object.keys(zh.paths ?? {})).toEqual(Object.keys(en.paths ?? {}));
    const zhDescription = zh.paths?.['/user/token']?.post?.description;
    const enDescription = en.paths?.['/user/token']?.post?.description;
    expect(zhDescription).toEqual(expect.any(String));
    expect(enDescription).toEqual(expect.any(String));
    expect(zhDescription).not.toBe(enDescription);
    expect(zhDescription).toMatch(/\p{Script=Han}/u);
    expect(enDescription).toMatch(/[A-Za-z]/);
    expect(JSON.stringify(zh)).not.toContain('"x-i18n"');
    expect(JSON.stringify(en)).not.toContain('"x-i18n"');
  });

  test('derives endpoint Markdown exports from the OpenAPI schemas', () => {
    const users = renderOpenAPIOperationMarkdown('en', [
      'api',
      'product-http',
      'users',
      'setQuickstartUserToken',
    ]);
    const routing = renderOpenAPIOperationMarkdown('en', [
      'api',
      'product-http',
      'routing',
      'getQuickstartGatewayRoute',
    ]);
    const messages = renderOpenAPIOperationMarkdown('en', [
      'api',
      'product-http',
      'messages',
      'syncQuickstartChannelMessages',
    ]);
    const usersZh = renderOpenAPIOperationMarkdown('zh', [
      'api',
      'product-http',
      'users',
      'setQuickstartUserToken',
    ]);

    expect(users).toContain('`POST`');
    expect(users).toContain('`device_flag`');
    expect(users).toContain('`uid`');
    expect(users).toContain('`token`');
    expect(users).toContain('const: `200`');
    expect(users).toContain('Additional properties: `false`');
    expect(users).toContain('| `503` |');
    expect(routing).toContain('`wss_addr`');
    expect(routing).toContain('`ws_addr`');
    expect(routing).toContain('| `503` |');
    expect(messages).toContain('`pull_mode`');
    expect(messages).toContain('`start_message_seq`');
    expect(messages).toContain('Referenced schema — `LegacyMessage`');
    expect(messages).toContain('`message_idstr`');
    expect(messages).toContain('10000');
    expect(messages).toContain('| `503` |');
    expect(usersZh).toContain('`uid`');
    expect(usersZh).toContain('`device_flag`');
    expect(renderOpenAPIOperationMarkdown('en', ['api', 'product-http'])).toBe('');
  });

  test('keeps generation deterministic and generates concise tag indexes', async () => {
    const generator = await source('../scripts/generate-openapi.ts');

    expect(generator).toContain('generateFilesOnly');
    expect(generator).toContain("'--check'");
    expect(generator).toContain("'--write'");
    expect(generator).toContain("groupBy: 'tag'");
    expect(generator).toContain('index:');

    for (const operationPage of operationPages) {
      await expect(
        Bun.file(
          new URL(
            `../content/docs/api/product-http/${operationPage.groupSlug}/${operationPage.slug}.mdx`,
            import.meta.url,
          ),
        ).exists(),
      ).resolves.toBe(true);
      await expect(
        Bun.file(
          new URL(
            `../content/docs/api/product-http/${operationPage.groupSlug}/${operationPage.slug}.en.mdx`,
            import.meta.url,
          ),
        ).exists(),
      ).resolves.toBe(true);
    }
  });
});
