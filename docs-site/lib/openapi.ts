import openapiDocument from '../contracts/javascript-web-quickstart.openapi.json';
import managementOpenAPIDocument from '../contracts/product-http-management.openapi.json';
import { createOpenAPI } from 'fumadocs-openapi/server';

type OpenAPIOptions = NonNullable<Parameters<typeof createOpenAPI>[0]>;
type OpenAPISchemaRecord = Exclude<NonNullable<OpenAPIOptions['input']>, string[]>;

/** Stable schema ID embedded into generated Product HTTP reference pages. */
export const productHTTPOpenAPIDocumentId = 'wukongim-product-http-beta';

export const productHTTPOpenAPIDocumentIds = {
  zh: `${productHTTPOpenAPIDocumentId}-zh`,
  en: `${productHTTPOpenAPIDocumentId}-en`,
} as const;

/** Stable schema ID embedded into the generated trusted-management pages. */
export const productHTTPManagementOpenAPIDocumentId =
  'wukongim-product-http-management-beta';

export const productHTTPManagementOpenAPIDocumentIds = {
  zh: `${productHTTPManagementOpenAPIDocumentId}-zh`,
  en: `${productHTTPManagementOpenAPIDocumentId}-en`,
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

/** Locale metadata and exact operation whitelist for the management Beta pages. */
export const productHTTPManagementOpenAPIPages = [
  {
    slug: 'channels',
    tag: 'Channels',
    title: { zh: '频道（Beta 管理子集）', en: 'Channels (Beta Management Subset)' },
    description: {
      zh: '通过受信后端管理 Channel 元数据、持久或临时订阅者，以及允许和拒绝名单。',
      en: 'Manage Channel metadata, durable or temporary subscribers, and allow or deny lists from a trusted backend.',
    },
    operations: [
      { method: 'post', path: '/channel' },
      { method: 'post', path: '/channel/subscriber_add' },
      { method: 'post', path: '/channel/subscriber_remove_all' },
      { method: 'post', path: '/tmpchannel/subscriber_set' },
      { method: 'post', path: '/channel/blacklist_add' },
      { method: 'post', path: '/channel/blacklist_remove' },
      { method: 'post', path: '/channel/blacklist_remove_all' },
      { method: 'post', path: '/channel/whitelist_add' },
      { method: 'post', path: '/channel/whitelist_remove' },
      { method: 'post', path: '/channel/whitelist_remove_all' },
    ],
  },
  {
    slug: 'conversations',
    tag: 'Conversations',
    title: { zh: '会话（Canonical Beta 子集）', en: 'Conversations (Canonical Beta Subset)' },
    description: {
      zh: '以有界游标同步会话投影、重试未解析项，并单调维护未读、隐藏与激活状态。',
      en: 'Synchronize the Conversation projection with bounded cursors, retry unresolved keys, and monotonically maintain unread, hide, and activation state.',
    },
    operations: [
      { method: 'post', path: '/conversation/list' },
      { method: 'post', path: '/conversation/retry' },
      { method: 'post', path: '/conversations/clearUnread' },
      { method: 'post', path: '/conversations/setUnread' },
      { method: 'post', path: '/conversations/delete' },
      { method: 'post', path: '/conversations/activate' },
    ],
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

function localizedManagementDocument(locale: OpenAPILocale) {
  return localizeOpenAPIDocument(
    managementOpenAPIDocument,
    locale,
  ) as unknown as OpenAPISchemaRecord[string];
}

/** Creates the one-locale source used by deterministic MDX generation. */
export function createProductHTTPOpenAPI(locale: OpenAPILocale) {
  return createOpenAPI({
    input: {
      [productHTTPOpenAPIDocumentIds[locale]]: localizedDocument(locale),
    },
  });
}

/** Creates the one-locale source used by deterministic management-page generation. */
export function createProductHTTPManagementOpenAPI(locale: OpenAPILocale) {
  return createOpenAPI({
    input: {
      [productHTTPManagementOpenAPIDocumentIds[locale]]:
        localizedManagementDocument(locale),
    },
  });
}

/** Server-only loader for all published golden-path and management OpenAPI pages. */
export const openapi = createOpenAPI({
  input: {
    [productHTTPOpenAPIDocumentIds.zh]: localizedDocument('zh'),
    [productHTTPOpenAPIDocumentIds.en]: localizedDocument('en'),
    [productHTTPManagementOpenAPIDocumentIds.zh]: localizedManagementDocument('zh'),
    [productHTTPManagementOpenAPIDocumentIds.en]: localizedManagementDocument('en'),
  },
});
