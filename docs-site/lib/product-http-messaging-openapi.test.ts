import { describe, expect, test } from 'bun:test';
import {
  createProductHTTPMessagingOpenAPI,
  productHTTPCompleteOpenAPIDocumentIds,
  productHTTPMessagingOpenAPIDocumentIds,
} from './openapi';
import {
  renderOpenAPIOperationMarkdown,
  renderOpenAPISearchText,
} from './openapi-markdown';
import { productHTTPMessagingOpenAPIGroups } from './product-http-openapi';

interface TestedSchema {
  $ref?: string;
  type?: string;
  format?: string;
  description?: string;
  'x-i18n'?: { zh?: { description?: string } };
  const?: unknown;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  pattern?: string;
  contentEncoding?: string;
  additionalProperties?: boolean;
  required?: string[];
  enum?: unknown[];
  properties?: Record<string, TestedSchema>;
  oneOf?: TestedSchema[];
}

interface TestedResponse {
  $ref?: string;
  description?: string;
  'x-i18n'?: { zh?: { description?: string } };
  content?: {
    'application/json'?: {
      schema?: TestedSchema;
    };
  };
}

interface TestedOperation {
  operationId?: string;
  summary?: string;
  description?: string;
  tags?: string[];
  security?: unknown[];
  'x-i18n'?: { zh?: { summary?: string; description?: string } };
  'x-wukongim-trust'?: string;
  'x-codeSamples'?: Array<{
    lang?: string;
    label?: string;
    source?: string;
    'x-i18n'?: { zh?: { label?: string; source?: string } };
  }>;
  requestBody?: {
    content?: { 'application/json'?: { schema?: TestedSchema } };
  };
  responses?: Record<string, TestedResponse>;
}

interface MessagingDocument {
  openapi?: string;
  info?: {
    title?: string;
    description?: string;
    'x-i18n'?: { zh?: { title?: string; description?: string } };
  };
  tags?: Array<{
    name?: string;
    description?: string;
    'x-i18n'?: { zh?: { description?: string } };
  }>;
  paths: Record<string, Partial<Record<'post', TestedOperation>>>;
  components?: {
    schemas?: Record<string, TestedSchema & {
      description?: string;
    }>;
    responses?: Record<string, TestedResponse>;
  };
  'x-wukongim-scope'?: string;
}

async function source(relativePath: string) {
  return Bun.file(new URL(relativePath, import.meta.url)).text();
}

async function messagingDocument() {
  return JSON.parse(
    await source('../contracts/product-http-messaging.openapi.json'),
  ) as MessagingDocument;
}

function responseSchema(document: MessagingDocument, status: string) {
  const operation = document.paths['/message/send']?.post;
  const response = operation?.responses?.[status];
  const name = response?.$ref?.split('/').at(-1);
  const resolved = name ? document.components?.responses?.[name] : response;
  return resolved?.content?.['application/json']?.schema;
}

describe('Product HTTP message-sending OpenAPI contract', () => {
  test('publishes exactly one trusted-backend message-sending operation', async () => {
    const document = await messagingDocument();
    const operation = document.paths['/message/send']?.post;

    expect(document.openapi).toBe('3.1.0');
    expect(document['x-wukongim-scope']).toBe(
      'non-exhaustive-trusted-message-sending-beta',
    );
    expect(Object.keys(document.paths)).toEqual(['/message/send']);
    expect(document.tags?.map((tag) => tag.name)).toEqual(['Message Sending']);
    expect(operation).toMatchObject({
      operationId: 'sendChannelMessage',
      tags: ['Message Sending'],
      security: [],
      'x-wukongim-trust': 'trusted-backend-only',
    });
    expect(operation?.description).toContain('trusted backend');
    expect(operation?.['x-codeSamples']).toHaveLength(1);
    expect(operation?.['x-codeSamples']?.[0]).toMatchObject({
      lang: 'bash',
      label: 'Trusted backend (cURL)',
    });
    const sample = operation?.['x-codeSamples']?.[0]?.source ?? '';
    expect(sample).toContain('trusted backend');
    expect(sample).toContain('http://127.0.0.1:5001/message/send');
    expect(sample).not.toContain('\n+');
    for (const hidden of [
      'sender_uid',
      'device_id',
      'subscribers',
      'no_persist',
      'sync_once',
    ]) {
      expect(sample).not.toContain(hidden);
    }
  });

  test('contracts only the ordinary durable request shape', async () => {
    const document = await messagingDocument();
    const schemas = document.components?.schemas ?? {};
    const request = schemas.SendChannelMessageRequest;
    const requestRef =
      document.paths['/message/send']?.post?.requestBody?.content?.['application/json']
        ?.schema?.$ref;

    expect(requestRef).toBe('#/components/schemas/SendChannelMessageRequest');
    expect(request?.additionalProperties).toBe(false);
    expect(request?.required).toEqual([
      'from_uid',
      'channel_id',
      'channel_type',
      'client_msg_no',
      'payload',
    ]);
    expect(Object.keys(request?.properties ?? {})).toEqual(request?.required ?? []);
    expect(request?.properties?.from_uid).toMatchObject({
      type: 'string',
      minLength: 1,
      pattern: '\\S',
    });
    expect(request?.properties?.channel_id).toMatchObject({
      type: 'string',
      minLength: 1,
      pattern: '\\S',
    });
    expect(request?.properties?.channel_type).toMatchObject({
      type: 'integer',
      format: 'uint8',
      minimum: 1,
      maximum: 255,
    });
    expect(request?.properties?.client_msg_no).toMatchObject({
      type: 'string',
      minLength: 1,
      pattern: '\\S',
    });
    expect(request?.properties?.payload).toMatchObject({
      type: 'string',
      minLength: 1,
      contentEncoding: 'base64',
    });

    for (const hidden of [
      'sender_uid',
      'device_id',
      'subscribers',
      'header',
      'no_persist',
      'sync_once',
    ]) {
      expect(request?.properties?.[hidden]).toBeUndefined();
    }
  });

  test('models the HTTP-200 protocol result and all runtime HTTP failures', async () => {
    const document = await messagingDocument();
    const operation = document.paths['/message/send']?.post;
    const schemas = document.components?.schemas ?? {};
    const result = schemas.SendChannelMessageResponse;

    expect(Object.keys(operation?.responses ?? {})).toEqual([
      '200',
      '400',
      '404',
      '408',
      '500',
      '503',
    ]);
    expect(responseSchema(document, '200')?.$ref).toBe(
      '#/components/schemas/SendChannelMessageResponse',
    );
    expect(result?.required).toEqual(['message_id', 'message_seq', 'reason']);
    expect(result?.properties?.message_id).toMatchObject({
      type: 'integer',
      format: 'int64',
    });
    expect(result?.properties?.message_seq).toMatchObject({
      type: 'integer',
      format: 'uint64',
      minimum: 0,
    });
    expect(result?.properties?.reason).toMatchObject({
      type: 'integer',
      format: 'uint8',
      minimum: 0,
      maximum: 255,
    });
    expect(result?.properties?.reason?.description).toContain(
      'HTTP 200 does not mean business success',
    );

    expect(responseSchema(document, '400')?.$ref).toBe(
      '#/components/schemas/SendError',
    );
    expect(responseSchema(document, '404')?.$ref).toBe(
      '#/components/schemas/SendError',
    );
    expect(responseSchema(document, '408')?.$ref).toBe(
      '#/components/schemas/SendError',
    );
    expect(responseSchema(document, '500')?.$ref).toBe(
      '#/components/schemas/SendError',
    );
    expect(responseSchema(document, '503')?.oneOf).toEqual([
      { $ref: '#/components/schemas/RetryRequiredError' },
      { $ref: '#/components/schemas/MaintenanceError' },
    ]);
    expect(schemas.RetryRequiredError?.properties?.error?.const).toBe('retry required');
    expect(schemas.MaintenanceError?.properties?.error?.const).toBe('maintenance');
    expect(schemas.MaintenanceError?.properties?.message?.const).toBe(
      'restore maintenance is active',
    );
  });

  test('keeps the contract aligned with route, DTO, and error mapping source', async () => {
    const [handler, errorMap, server] = await Promise.all([
      source('../../internal/access/api/message_send.go'),
      source('../../internal/access/api/message_error_map.go'),
      source('../../internal/access/api/server.go'),
    ]);

    expect(handler).toContain('s.engine.POST("/message/send", s.handleSendMessage)');
    for (const field of [
      '`json:"from_uid"`',
      '`json:"channel_id"`',
      '`json:"channel_type"`',
      '`json:"client_msg_no"`',
      '`json:"payload"`',
    ]) {
      expect(handler).toContain(field);
    }
    expect(handler).toContain('base64.StdEncoding.DecodeString(req.Payload)');
    expect(handler).toContain('http.StatusInternalServerError');
    expect(handler).toContain('MessageID  int64');
    expect(handler).toContain('MessageSeq uint64');
    expect(handler).toContain('Reason     uint8');

    for (const mapping of [
      'http.StatusBadRequest',
      'http.StatusNotFound',
      'http.StatusRequestTimeout',
      'http.StatusServiceUnavailable',
      '"retry required"',
    ]) {
      expect(errorMap).toContain(mapping);
    }
    expect(server).toContain('"error":   "maintenance"');
    expect(server).toContain('"message": "restore maintenance is active"');
  });

  test('provides concise Chinese overlays for the public contract', async () => {
    const document = await messagingDocument();
    const operation = document.paths['/message/send']?.post;
    const schemas = document.components?.schemas ?? {};

    expect(document.info?.['x-i18n']?.zh?.title).toContain('消息发送');
    expect(document.info?.['x-i18n']?.zh?.description).toMatch(/受信后端/);
    expect(document.tags?.[0]?.['x-i18n']?.zh?.description).toMatch(/持久/);
    expect(operation?.['x-i18n']?.zh?.summary).toMatch(/发送/);
    expect(operation?.['x-i18n']?.zh?.description).toMatch(/HTTP 200/);
    expect(operation?.['x-codeSamples']?.[0]?.['x-i18n']?.zh?.source).toMatch(
      /受信后端/,
    );
    expect(schemas.SendChannelMessageRequest?.['x-i18n']?.zh?.description).toMatch(
      /持久/,
    );
    expect(schemas.SendChannelMessageResponse?.['x-i18n']?.zh?.description).toMatch(
      /Reason Code/,
    );
  });

  test('publishes the exact contract through a static route', async () => {
    const route = await source(
      '../app/contracts/product-http-messaging.openapi.json/route.ts',
    );

    expect(route).toContain("@/contracts/product-http-messaging.openapi.json");
    expect(route).toContain('export const revalidate = false');
    expect(route).toContain('return Response.json(openapi)');
  });

  test('generates and localizes one Fumadocs operation page', async () => {
    expect(productHTTPMessagingOpenAPIGroups).toHaveLength(1);
    expect(productHTTPMessagingOpenAPIGroups[0]?.slug).toBe('message-send');
    expect(productHTTPMessagingOpenAPIGroups[0]?.operations.map((item) => item.slug)).toEqual([
      'sendChannelMessage',
    ]);

    for (const locale of ['zh', 'en'] as const) {
      const server = createProductHTTPMessagingOpenAPI(locale);
      const bundled = (
        await server.getSchema(productHTTPMessagingOpenAPIDocumentIds[locale])
      ).bundled;
      expect(JSON.stringify(bundled)).not.toContain('"x-i18n"');
      expect(bundled.paths?.['/message/send']?.post?.description).toMatch(
        locale === 'zh' ? /受信后端/ : /trusted backend/i,
      );

      const suffix = locale === 'en' ? '.en' : '';
      const generated = await source(
        `../content/docs/api/product-http/message-send/sendChannelMessage${suffix}.mdx`,
      );
      expect(generated).toContain('generated by Fumadocs');
      expect(generated).toContain(productHTTPCompleteOpenAPIDocumentIds[locale]);
      expect(generated).toContain(
        'operations={[{"path":"/message/send","method":"post"}]}',
      );
      const index = await source(
        `../content/docs/api/product-http/message-send/index${suffix}.mdx`,
      );
      expect(index).toContain('<Cards>');
      expect(index).not.toContain('operations={[');
    }
  });

  test('exports the request, result, and error boundary to Markdown and search', () => {
    const slugs = [
      'api',
      'product-http',
      'message-send',
      'sendChannelMessage',
    ] as const;
    const markdown = renderOpenAPIOperationMarkdown('en', slugs);
    const search = renderOpenAPISearchText('en', slugs);

    for (const fact of [
      '`POST` `/message/send`',
      '`client_msg_no`',
      '`payload`',
      '`reason`',
      '`RetryRequiredError`',
      '`MaintenanceError`',
      '| `503` |',
    ]) {
      expect(markdown).toContain(fact);
    }
    for (const fact of [
      '/message/send',
      'client_msg_no',
      'reason',
      'RetryRequiredError',
      'MaintenanceError',
    ]) {
      expect(search).toContain(fact);
    }
  });
});
