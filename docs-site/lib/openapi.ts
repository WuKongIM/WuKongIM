import openapiDocument from '../contracts/javascript-web-quickstart.openapi.json';
import { createOpenAPI } from 'fumadocs-openapi/server';

type OpenAPIOptions = NonNullable<Parameters<typeof createOpenAPI>[0]>;
type OpenAPISchemaRecord = Exclude<NonNullable<OpenAPIOptions['input']>, string[]>;

/** Stable schema ID embedded into generated Product HTTP reference pages. */
export const productHTTPOpenAPIDocumentId = 'wukongim-product-http-beta';

export const productHTTPOpenAPIDocumentIds = {
  zh: `${productHTTPOpenAPIDocumentId}-zh`,
  en: `${productHTTPOpenAPIDocumentId}-en`,
} as const;

/** Locale metadata and route mapping for the three published OpenAPI operations. */
export const productHTTPOpenAPIPages = [
  {
    slug: 'users',
    method: 'post',
    path: '/user/token',
    title: { zh: '用户（Beta 子集）', en: 'Users (Beta Subset)' },
    description: {
      zh: '使用 POST /user/token 从受信 BFF 保存开发身份的设备 Token 元数据。',
      en: 'Use POST /user/token from a trusted BFF to store device-token metadata for a development identity.',
    },
  },
  {
    slug: 'routing',
    method: 'get',
    path: '/route',
    title: { zh: '路由发现（Beta 子集）', en: 'Route Discovery (Beta Subset)' },
    description: {
      zh: '使用 GET /route 从受信 BFF 获取当前配置的客户端接入地址。',
      en: 'Use GET /route from a trusted BFF to obtain the currently configured client-ingress addresses.',
    },
  },
  {
    slug: 'messages',
    method: 'post',
    path: '/channel/messagesync',
    title: { zh: '消息（Beta 同步子集）', en: 'Messages (Beta Sync Subset)' },
    description: {
      zh: '使用 POST /channel/messagesync 从已提交的个人 Channel 日志恢复离线消息。',
      en: 'Use POST /channel/messagesync to recover offline messages from a committed person-Channel log.',
    },
  },
] as const;

type OpenAPILocale = keyof typeof productHTTPOpenAPIDocumentIds;

/** Applies reviewed x-i18n text without duplicating the OpenAPI structure. */
export function localizeOpenAPIDocument<T>(document: T, locale: OpenAPILocale): T {
  const localized = structuredClone(document);

  function visit(value: unknown) {
    if (Array.isArray(value)) {
      for (const item of value) visit(item);
      return;
    }
    if (!value || typeof value !== 'object') return;

    const record = value as Record<string, unknown>;
    const translations = record['x-i18n'];
    if (translations && typeof translations === 'object' && !Array.isArray(translations)) {
      const selected = (translations as Record<string, unknown>)[locale];
      if (selected && typeof selected === 'object' && !Array.isArray(selected)) {
        Object.assign(record, selected);
      }
      delete record['x-i18n'];
    }
    for (const child of Object.values(record)) visit(child);
  }

  visit(localized);
  return localized;
}

function localizedDocument(locale: OpenAPILocale) {
  return localizeOpenAPIDocument(openapiDocument, locale) as unknown as OpenAPISchemaRecord[string];
}

/** Creates the one-locale source used by deterministic MDX generation. */
export function createProductHTTPOpenAPI(locale: OpenAPILocale) {
  return createOpenAPI({
    input: {
      [productHTTPOpenAPIDocumentIds[locale]]: localizedDocument(locale),
    },
  });
}

/** Server-only OpenAPI loader for the bounded JavaScript/Web golden-path contract. */
export const openapi = createOpenAPI({
  input: {
    [productHTTPOpenAPIDocumentIds.zh]: localizedDocument('zh'),
    [productHTTPOpenAPIDocumentIds.en]: localizedDocument('en'),
  },
});
