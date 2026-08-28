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
    title: { zh: '用户 Token API（Beta）', en: 'User Token API (Beta)' },
    description: {
      zh: '保存开发身份的设备 Token 元数据。',
      en: 'Store device-token metadata for a development identity.',
    },
  },
  {
    slug: 'routing',
    method: 'get',
    path: '/route',
    title: { zh: 'Gateway 路由 API（Beta）', en: 'Gateway Route API (Beta)' },
    description: {
      zh: '获取当前配置的客户端接入地址。',
      en: 'Get the configured client-ingress addresses.',
    },
  },
  {
    slug: 'messages',
    method: 'post',
    path: '/channel/messagesync',
    title: { zh: '消息同步 API（Beta）', en: 'Message Sync API (Beta)' },
    description: {
      zh: '从已提交的 Channel 日志恢复消息。',
      en: 'Recover messages from a committed Channel log.',
    },
  },
] as const;

/** Locale metadata and exact operation whitelist for the management Beta pages. */
export const productHTTPManagementOpenAPIPages = [
  {
    slug: 'channels',
    tag: 'Channels',
    title: { zh: 'Channel 管理 API（Beta）', en: 'Channel Management API (Beta)' },
    description: {
      zh: '管理 Channel、订阅者及允许或拒绝名单。',
      en: 'Manage Channels, subscribers, and allow or deny lists.',
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
    title: { zh: '会话 API（Beta）', en: 'Conversation API (Beta)' },
    description: {
      zh: '同步会话，并管理未读、隐藏与激活状态。',
      en: 'Synchronize Conversations and manage unread, hide, and activation state.',
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
