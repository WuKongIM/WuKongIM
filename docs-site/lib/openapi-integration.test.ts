import openapiDocument from '../contracts/javascript-web-quickstart.openapi.json';
import { describe, expect, test } from 'bun:test';
import {
  createProductHTTPOpenAPI,
  productHTTPOpenAPIDocumentIds,
} from './openapi';
import { renderOpenAPIOperationMarkdown } from './openapi-markdown';

const documentId = 'wukongim-product-http-beta';
interface TestedOperation {
  tags?: string[];
  'x-wukongim-trust'?: string;
  'x-codeSamples'?: Array<{ lang?: string; label?: string; source?: string }>;
}

const operationPages = [
  {
    slug: 'users',
    method: 'post',
    path: '/user/token',
    tag: 'Users',
  },
  {
    slug: 'routing',
    method: 'get',
    path: '/route',
    tag: 'Routing',
  },
  {
    slug: 'messages',
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
    const [page, component, openapi, layout, stylesheet] = await Promise.all([
      source('../app/[lang]/(docs)/[section]/[[...slug]]/page.tsx'),
      source('../components/openapi-page.tsx'),
      source('./openapi.ts'),
      source('./layout.shared.tsx'),
      source('../app/global.css'),
    ]);

    expect(page).toContain('openapi.preloadOpenAPIPage(page)');
    expect(page).toContain('OpenAPIPage: async');
    expect(page).toContain('page.data._openapi');
    expect(page).toContain('docs-site/content/openapi/');
    expect(component).toContain('createOpenAPIPage');
    expect(component).toContain('playground: { enabled: false }');
    expect(component).toContain('createCodeUsageGeneratorRegistry()');
    expect(openapi).toContain(`export const productHTTPOpenAPIDocumentId = '${documentId}'`);
    expect(openapi).toContain('localizeOpenAPIDocument');
    expect(openapi).toContain('javascript-web-quickstart.openapi.json');
    expect(openapi).not.toContain('resources/api/openapi.json');
    expect(layout).toContain('.extend(openapiTranslations())');
    expect(layout).toContain(".preset('zh', zhCN())");
    expect(stylesheet).toContain("@import 'fumadocs-openapi/css/preset.css';");
  });

  test('generates bilingual operation pages from one bounded contract', async () => {
    for (const operationPage of operationPages) {
      for (const suffix of ['.mdx', '.en.mdx']) {
        const locale = suffix === '.mdx' ? 'zh' : 'en';
        const localizedDocumentId = productHTTPOpenAPIDocumentIds[locale];
        const page = await source(
          `../content/docs/api/product-http/${operationPage.slug}${suffix}`,
        );

        expect(page).toContain('This file is generated from the bounded Product HTTP OpenAPI contract');
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
    expect(zh.paths?.['/user/token']?.post?.description).toContain('浏览器连接前');
    expect(en.paths?.['/user/token']?.post?.description).toContain(
      'Compatibility endpoint used by',
    );
    expect(JSON.stringify(zh)).not.toContain('"x-i18n"');
    expect(JSON.stringify(en)).not.toContain('"x-i18n"');
  });

  test('derives endpoint Markdown exports from the OpenAPI schemas', () => {
    const users = renderOpenAPIOperationMarkdown('en', [
      'api',
      'product-http',
      'users',
    ]);
    const routing = renderOpenAPIOperationMarkdown('en', [
      'api',
      'product-http',
      'routing',
    ]);
    const messages = renderOpenAPIOperationMarkdown('en', [
      'api',
      'product-http',
      'messages',
    ]);
    const usersZh = renderOpenAPIOperationMarkdown('zh', [
      'api',
      'product-http',
      'users',
    ]);

    expect(users).toContain('`POST`');
    expect(users).toContain('`device_flag`');
    expect(users).toContain('Stable product identity');
    expect(users).toContain('Trusted backend (cURL)');
    expect(users).toContain('server-generated-development-secret');
    expect(users).toContain('const: `200`');
    expect(users).toContain('Additional properties: `false`');
    expect(users).toContain('| `503` |');
    expect(routing).toContain('`wss_addr`');
    expect(routing).toContain('TLS WebSocket ingress');
    expect(routing).toContain('"wss_addr": ""');
    expect(routing).toContain('| `503` |');
    expect(messages).toContain('`pull_mode`');
    expect(messages).toContain('Direction selector: 0 pulls older messages');
    expect(messages).toContain('Referenced schema — `SyncedMessage`');
    expect(messages).toContain('`message_idstr`');
    expect(messages).toContain('"login_uid": "bob"');
    expect(messages).toContain('1–100');
    expect(messages).toContain('| `503` |');
    expect(usersZh).toContain('浏览器连接前');
    expect(usersZh).toContain('`device_flag`');
    expect(renderOpenAPIOperationMarkdown('en', ['api', 'product-http'])).toBe('');
  });

  test('keeps generation deterministic and preserves narrative supplements', async () => {
    const generator = await source('../scripts/generate-openapi.ts');

    expect(generator).toContain('generateFilesOnly');
    expect(generator).toContain("'--check'");
    expect(generator).toContain("'--write'");
    expect(generator).toContain('content/openapi/product-http');

    for (const operationPage of operationPages) {
      await expect(
        Bun.file(
          new URL(
            `../content/openapi/product-http/${operationPage.slug}.mdx`,
            import.meta.url,
          ),
        ).exists(),
      ).resolves.toBe(true);
      await expect(
        Bun.file(
          new URL(
            `../content/openapi/product-http/${operationPage.slug}.en.mdx`,
            import.meta.url,
          ),
        ).exists(),
      ).resolves.toBe(true);
    }
  });
});
