import goldenPathDocument from '../contracts/javascript-web-quickstart.openapi.json';
import completeDocument from '../contracts/product-http.openapi.json';
import { describe, expect, test } from 'bun:test';
import {
  createProductHTTPManagementOpenAPI,
  productHTTPCompleteOpenAPIDocumentIds,
  productHTTPManagementOpenAPIDocumentIds,
} from './openapi';
import {
  renderOpenAPIOperationMarkdown,
  renderOpenAPISearchText,
} from './openapi-markdown';
import { productHTTPManagementOpenAPIGroups } from './product-http-openapi';

interface TestedOperation {
  tags?: string[];
  security?: unknown[];
  'x-wukongim-trust'?: string;
  'x-codeSamples'?: Array<{ lang?: string; label?: string; source?: string }>;
  responses?: Record<string, unknown>;
}

interface TestedSchema {
  $ref?: string;
  type?: string | string[];
  const?: unknown;
  default?: unknown;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  minItems?: number;
  maxItems?: number;
  pattern?: string;
  additionalProperties?: boolean;
  required?: string[];
  properties?: Record<string, TestedSchema>;
  items?: TestedSchema;
}

const publishedOperations = [
  ['post', '/channel'],
  ['post', '/channel/subscriber_add'],
  ['post', '/channel/subscriber_remove_all'],
  ['post', '/tmpchannel/subscriber_set'],
  ['post', '/channel/blacklist_add'],
  ['post', '/channel/blacklist_remove'],
  ['post', '/channel/blacklist_remove_all'],
  ['post', '/channel/whitelist_add'],
  ['post', '/channel/whitelist_remove'],
  ['post', '/channel/whitelist_remove_all'],
  ['post', '/conversation/list'],
  ['post', '/conversation/retry'],
  ['post', '/conversations/clearUnread'],
  ['post', '/conversations/setUnread'],
  ['post', '/conversations/delete'],
  ['post', '/conversations/activate'],
] as const;

const deferredOperations = [
  '/channel/info',
  '/channel/delete',
  '/channel/subscriber_remove',
  '/channel/blacklist_set',
  '/channel/whitelist_set',
  '/channel/whitelist',
  '/conversation/sync',
] as const;

async function source(relativePath: string) {
  return Bun.file(new URL(relativePath, import.meta.url)).text();
}

async function managementDocument() {
  return JSON.parse(
    await source('../contracts/product-http-management.openapi.json'),
  ) as {
    paths: Record<string, Partial<Record<'get' | 'post', TestedOperation>>>;
    tags?: Array<{ name?: string }>;
    components?: { schemas?: Record<string, TestedSchema> };
    'x-wukongim-scope'?: string;
  };
}

describe('Product HTTP management OpenAPI integration', () => {
  test('keeps the golden-path operation whitelist fixed at exactly three operations', () => {
    expect(Object.keys(goldenPathDocument.paths)).toEqual([
      '/user/token',
      '/route',
      '/channel/messagesync',
    ]);
  });

  test('publishes the reviewed channel and canonical-conversation whitelist only', async () => {
    const document = await managementDocument();
    const actual = Object.entries(document.paths).flatMap(([path, item]) =>
      Object.keys(item).map((method) => [method, path]),
    );

    expect(document['x-wukongim-scope']).toBe(
      'non-exhaustive-trusted-product-management-beta',
    );
    expect(document.tags?.map((tag) => tag.name)).toEqual(['Channels', 'Conversations']);
    const expected = publishedOperations.map(([method, path]) => [method, path]);
    expect(actual).toEqual(expected);
    for (const path of deferredOperations) expect(document.paths[path]).toBeUndefined();
  });

  test('keeps the published whitelist attached to current Product HTTP registrations', async () => {
    const [channels, conversations, server] = await Promise.all([
      source('../../internal/access/api/channel_management.go'),
      source('../../internal/access/api/conversation_list.go'),
      source('../../internal/access/api/server.go'),
    ]);

    for (const [, path] of publishedOperations) {
      const registration = `s.engine.POST("${path}"`;
      expect(path.startsWith('/conversation') ? conversations : channels).toContain(
        registration,
      );
    }
    expect(server).toContain('"error":   "maintenance"');
    expect(server).toContain('"message": "restore maintenance is active"');
  });

  test('marks every operation trusted-backend-only with reviewed non-browser examples', async () => {
    const document = await managementDocument();

    for (const [method, path] of publishedOperations) {
      const operation = document.paths[path]?.[method];
      const expectedTag = path.startsWith('/conversation') ? 'Conversations' : 'Channels';

      expect(operation?.tags).toEqual([expectedTag]);
      expect(operation?.security).toEqual([]);
      expect(operation?.['x-wukongim-trust']).toBe('trusted-backend-only');
      expect(operation?.['x-codeSamples']).toHaveLength(1);
      expect(operation?.['x-codeSamples']?.[0]).toMatchObject({
        lang: 'bash',
        label: 'Trusted backend (cURL)',
      });
      expect(operation?.['x-codeSamples']?.[0]?.source).toContain('127.0.0.1:5001');
      expect(Object.keys(operation?.responses ?? {})).toEqual(['200', '400', '503']);
    }
  });

  test('pins bounded Conversation fields and the real compatibility error shapes', async () => {
    const document = await managementDocument();
    const schemas = document.components?.schemas ?? {};

    expect(schemas.ConversationListRequest?.required).toEqual(['uid']);
    expect(schemas.ConversationListRequest?.properties?.limit).toMatchObject({
      type: 'integer',
      minimum: 0,
      maximum: 200,
      default: 50,
    });
    expect(schemas.ConversationRetryRequest?.properties?.channels).toMatchObject({
      minItems: 1,
      maxItems: 200,
    });
    expect(schemas.ConversationMutationRequest?.additionalProperties).toBe(false);
    expect(schemas.ConversationMutationRequest?.properties?.message_seq).toBeUndefined();
    expect(schemas.ConversationSetUnreadRequest?.required).toContain('unread');
    expect(schemas.ConversationLastMessage?.properties?.message_idstr?.type).toBe('string');
    expect(schemas.ConversationLastMessage?.properties?.payload?.type).toEqual([
      'string',
      'null',
    ]);
    expect(schemas.CompatibilityError?.properties?.status?.const).toBe(400);
    expect(schemas.MaintenanceError?.properties?.error?.const).toBe('maintenance');
    expect(schemas.MaintenanceError?.properties?.message?.const).toBe(
      'restore maintenance is active',
    );
    expect(schemas.ChannelSubscriberAddRequest?.properties?.subscribers?.items?.$ref).toBe(
      '#/components/schemas/NonBlankUID',
    );
    expect(schemas.ChannelAllowlistMutationRequest?.properties?.uids?.items?.$ref).toBe(
      '#/components/schemas/NonBlankUID',
    );
    expect(schemas.ChannelMemberMutationRequest?.properties?.uids?.items?.$ref).toBe(
      '#/components/schemas/UID',
    );
    expect(schemas.NonBlankUID).toMatchObject({
      type: 'string',
      minLength: 1,
      pattern: '\\S',
    });
  });

  test('localizes the two operation groups from one management contract', async () => {
    expect(productHTTPManagementOpenAPIGroups.map((group) => group.slug)).toEqual([
      'channels',
      'conversations',
    ]);

    const zhServer = createProductHTTPManagementOpenAPI('zh');
    const enServer = createProductHTTPManagementOpenAPI('en');
    const zh = (
      await zhServer.getSchema(productHTTPManagementOpenAPIDocumentIds.zh)
    ).bundled;
    const en = (
      await enServer.getSchema(productHTTPManagementOpenAPIDocumentIds.en)
    ).bundled;

    expect(Object.keys(zh.paths ?? {})).toEqual(Object.keys(en.paths ?? {}));
    const zhDescription = zh.paths?.['/conversation/list']?.post?.description;
    const enDescription = en.paths?.['/conversation/list']?.post?.description;
    expect(zhDescription).toEqual(expect.any(String));
    expect(enDescription).toEqual(expect.any(String));
    expect(zhDescription).not.toBe(enDescription);
    expect(zhDescription).toMatch(/\p{Script=Han}/u);
    expect(enDescription).toMatch(/[A-Za-z]/);
    expect(zh.paths?.['/channel/subscriber_add']?.post?.description).toContain(
      'Channel 不存在时创建元数据',
    );
    expect(en.paths?.['/channel/subscriber_add']?.post?.description).toContain(
      'creating Channel metadata when absent',
    );
    expect(zh.paths?.['/conversations/clearUnread']?.post?.description).toContain(
      '运行时会忽略',
    );
    expect(en.paths?.['/conversations/clearUnread']?.post?.description).toContain(
      'runtime ignores it',
    );
    expect(JSON.stringify(zh)).not.toContain('"x-i18n"');
    expect(JSON.stringify(en)).not.toContain('"x-i18n"');
  });

  test('renders every narrow-profile operation from the complete Fumadocs contract', async () => {
    for (const group of productHTTPManagementOpenAPIGroups) {
      for (const suffix of ['.mdx', '.en.mdx']) {
        const locale = suffix === '.mdx' ? 'zh' : 'en';
        const index = await source(
          `../content/docs/api/product-http/${group.slug}/index${suffix}`,
        );
        expect(index).toContain('<Cards>');
        expect(index).not.toContain('operations={[');
        for (const deferral of group.deferrals?.items ?? []) {
          for (const route of deferral.routes) expect(index).not.toContain(`\`${route}\``);
          expect(index).not.toContain(deferral.reason[locale]);
        }

        for (const operation of group.operations) {
          const generated = await source(
            `../content/docs/api/product-http/${group.slug}/${operation.slug}${suffix}`,
          );
          expect(generated).toContain('generated by Fumadocs');
          expect(generated).toContain(productHTTPCompleteOpenAPIDocumentIds[locale]);
          expect(generated).toContain(`\"path\":\"${operation.path}\"`);
          expect(generated).toContain(`\"method\":\"${operation.method}\"`);
          expect(generated.match(/\"path\":/g)).toHaveLength(1);
          expect(generated).toContain('CompatibilityError');
          expect(generated).toContain('MaintenanceError');
          expect(generated).toContain('restore maintenance is active');
        }
      }
    }
  });

  test('exports one operation and its nested schemas to each LLM page', () => {
    const channel = renderOpenAPIOperationMarkdown('en', [
      'api',
      'product-http',
      'channels',
      'setTemporaryChannelSubscribers',
    ]);
    const conversation = renderOpenAPIOperationMarkdown('en', [
      'api',
      'product-http',
      'conversations',
      'listConversations',
    ]);

    expect(channel).toContain('`POST` `/tmpchannel/subscriber_set`');
    expect(channel).toContain('`uids`');
    expect(channel).toContain('| `503` |');
    expect(channel).not.toContain('`POST` `/channel`');
    expect(conversation).toContain('`POST` `/conversation/list`');
    expect(conversation).toContain('`completed_coverage`');
    expect(conversation).toContain('Referenced schema — `ConversationLastMessage`');
    expect(conversation).toContain('`message_idstr`');
    expect(conversation).not.toContain('`POST` `/conversations/activate`');
    expect(conversation).toContain('| `503` |');

    const searchableConversations = renderOpenAPISearchText('en', [
      'api',
      'product-http',
      'conversations',
      'listConversations',
    ]);
    for (const fact of [
      'ConversationLastMessage',
      'message_idstr',
      'payload',
      'tombstones_retained_since',
      'CompatibilityError',
      'MaintenanceError',
      'restore maintenance is active',
    ]) {
      expect(searchableConversations).toContain(fact);
    }
  });

  test('retains the narrow management profile while the complete reference publishes its exclusions', async () => {
    const [route, specification, channels, conversations] = await Promise.all([
      source('../app/contracts/product-http-management.openapi.json/route.ts'),
      source('../PHASE_16_SPEC.md'),
      source('../content/docs/api/product-http/channels/index.mdx'),
      source('../content/docs/api/product-http/conversations/index.mdx'),
    ]);

    expect(route).toContain('product-http-management.openapi.json');
    for (const path of deferredOperations) {
      expect(specification).toContain(path);
      expect(completeDocument.paths[path]).toBeDefined();
    }
    for (const slug of [
      'updateChannelInfo',
      'disbandChannel',
      'removeChannelSubscribers',
      'setChannelDenylistMembers',
      'setChannelAllowlistMembers',
      'listChannelAllowlistMembers',
    ]) {
      expect(channels).toContain(`/zh/api/product-http/channels/${slug}`);
    }
    expect(conversations).toContain(
      '/zh/api/product-http/conversations/syncConversationsLegacy',
    );
  });
});
