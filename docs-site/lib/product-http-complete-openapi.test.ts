import { describe, expect, test } from 'bun:test';
import { fileURLToPath } from 'node:url';
import Ajv2020 from 'ajv/dist/2020';
import document from '../contracts/product-http.openapi.json';
import goldenProfile from '../contracts/javascript-web-quickstart.openapi.json';
import managementProfile from '../contracts/product-http-management.openapi.json';
import messagingProfile from '../contracts/product-http-messaging.openapi.json';
import { localizeOpenAPIDocument } from './product-http-openapi';

const httpMethods = ['delete', 'get', 'patch', 'post', 'put'] as const;
type HTTPMethod = (typeof httpMethods)[number];
const schemaValidator = new Ajv2020({ strict: false, allErrors: true });
for (const format of ['uint8', 'uint32', 'uint64', 'int32', 'int64']) {
  schemaValidator.addFormat(format, true);
}
const compiledSchemas = new Map<string, ReturnType<typeof schemaValidator.compile>>();

interface Schema {
  $ref?: string;
  type?: string | string[];
  description?: string;
  default?: unknown;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  minItems?: number;
  maxItems?: number;
  enum?: unknown[];
  pattern?: string;
  additionalProperties?: boolean;
  required?: string[];
  properties?: Record<string, Schema>;
  items?: Schema;
  oneOf?: Schema[];
  anyOf?: Schema[];
  allOf?: Schema[];
  not?: Schema;
  if?: Schema;
  then?: Schema;
  else?: Schema;
}

interface Operation {
  operationId?: string;
  tags?: string[];
  security?: unknown[];
  responses?: Record<string, unknown>;
  parameters?: Parameter[];
  requestBody?: { $ref?: string };
  deprecated?: boolean;
  'x-wukongim-trust'?: string;
  'x-wukongim-semantics'?: Record<string, string>;
  'x-codeSamples'?: unknown[];
}

interface Parameter {
  name?: string;
  in?: string;
  $ref?: string;
  description?: string;
}

interface ContractDocument {
  paths: Record<string, Partial<Record<HTTPMethod, Operation>>>;
}

const apiRoot = new URL('../../internal/access/api/', import.meta.url);

async function source(path: string) {
  return Bun.file(new URL(path, apiRoot)).text();
}

function isProductPath(path: string) {
  return (
    path === '/route' ||
    path === '/route/batch' ||
    /^\/(?:channel|tmpchannel|user|message|conversation|conversations)(?:\/|$)/.test(
      path,
    )
  );
}

function operationKeys() {
  return Object.entries(document.paths).flatMap(([path, pathItem]) =>
    Object.entries(pathItem)
      .filter(([method]) => httpMethods.includes(method as HTTPMethod))
      .map(([method]) => `${method.toUpperCase()} ${path}`),
  );
}

function operations(contract: ContractDocument) {
  const result = new Map<string, Operation>();
  for (const [path, pathItem] of Object.entries(contract.paths)) {
    for (const method of httpMethods) {
      const operation = pathItem[method];
      if (operation) result.set(`${method.toUpperCase()} ${path}`, operation);
    }
  }
  return result;
}

function parameterName(parameter: Parameter) {
  if (parameter.name) return parameter.name;
  const componentName = parameter.$ref?.split('/').at(-1);
  if (!componentName) return undefined;
  const parameters = document.components.parameters as Record<
    string,
    { name: string }
  >;
  return parameters[componentName]?.name;
}

async function registeredProductOperations() {
  const sourceFiles: string[] = [];
  for await (const path of new Bun.Glob('*.go').scan({
    cwd: fileURLToPath(apiRoot),
  })) {
    if (!path.endsWith('_test.go')) sourceFiles.push(path);
  }
  const files = await Promise.all(sourceFiles.map(source));
  const operations = new Set<string>();
  const registration = /s\.engine\.(DELETE|GET|PATCH|POST|PUT)\("([^"]+)"/g;

  for (const file of files) {
    for (const match of file.matchAll(registration)) {
      const method = match[1];
      const path = match[2];
      if (method && path && isProductPath(path)) operations.add(`${method} ${path}`);
    }
  }
  return [...operations].sort();
}

function schema(name: string): Schema {
  return document.components.schemas[
    name as keyof typeof document.components.schemas
  ] as Schema;
}

function validateSchema(name: string, value: unknown) {
  let validate = compiledSchemas.get(name);
  if (!validate) {
    validate = schemaValidator.compile({
      $schema: 'https://json-schema.org/draft/2020-12/schema',
      $ref: `#/components/schemas/${name}`,
      components: document.components,
    });
    compiledSchemas.set(name, validate);
  }
  return { valid: validate(value), errors: validate.errors };
}

function componentName(reference: string | undefined, component: string) {
  const prefix = `#/components/${component}/`;
  return reference?.startsWith(prefix) ? reference.slice(prefix.length) : undefined;
}

function jsonFieldsForStruct(file: string, typeName: string, seen = new Set<string>()) {
  if (seen.has(typeName)) return [];
  seen.add(typeName);
  const marker = `type ${typeName} struct {`;
  const start = file.indexOf(marker);
  if (start < 0) throw new Error(`missing Go request DTO: ${typeName}`);
  const bodyStart = start + marker.length;
  const bodyEnd = file.indexOf('\n}', bodyStart);
  if (bodyEnd < 0) throw new Error(`unterminated Go request DTO: ${typeName}`);
  const body = file.slice(bodyStart, bodyEnd);
  const fields = [...body.matchAll(/`json:"([^",]+)(?:,[^"]*)?"`/g)].map(
    (match) => match[1]!,
  );
  for (const line of body.split('\n')) {
    const embedded = line.match(/^\s*([a-z][A-Za-z0-9_]*)\s*$/)?.[1];
    if (embedded) fields.push(...jsonFieldsForStruct(file, embedded, seen));
  }
  return [...new Set(fields)].sort();
}

describe('complete Product HTTP OpenAPI contract', () => {
  test('matches all and only the 41 runtime Product HTTP registrations', async () => {
    const registered = await registeredProductOperations();
    const contracted = operationKeys().sort();

    expect(registered).toHaveLength(41);
    expect(contracted).toHaveLength(41);
    expect(contracted).toEqual(registered);
  });

  test('records the actual unauthenticated and maintenance-fenced boundary', () => {
    for (const pathItem of Object.values(document.paths)) {
      for (const [method, candidate] of Object.entries(pathItem)) {
        if (method !== 'get' && method !== 'post') continue;
        const operation = candidate as Operation;
        expect(operation.security).toEqual([]);
        expect(operation['x-wukongim-trust']).toMatch(
          /^(trusted-backend-only|operator-only|node-local-operator-only)$/,
        );
        expect(operation.responses).toHaveProperty('503');
      }
    }
    expect(schema('MaintenanceError').properties?.error).toMatchObject({
      type: 'string',
    });
  });

  test('preserves profile operation IDs, tags, and generated documentation URLs', () => {
    const complete = operations(document as ContractDocument);
    const profiled = new Map<string, Operation>();

    for (const profile of [goldenProfile, managementProfile, messagingProfile]) {
      for (const [key, operation] of operations(profile as ContractDocument)) {
        expect(profiled.has(key)).toBe(false);
        profiled.set(key, operation);
      }
    }

    expect(profiled.size).toBe(20);
    for (const [key, expected] of profiled) {
      expect(complete.get(key)).toMatchObject({
        operationId: expected.operationId,
        tags: expected.tags,
        'x-wukongim-trust': expected['x-wukongim-trust'],
      });
    }
    expect(document.tags.map((tag) => tag.name)).toEqual([
      'Users',
      'Routing',
      'Messages',
      'Message Sending',
      'Channels',
      'Conversations',
    ]);
  });

  test('adds reviewed examples and runtime semantics to the localized complete contract', () => {
    const localized = localizeOpenAPIDocument(document, 'en') as typeof document;
    const localizedOperations = operations(localized as ContractDocument);
    expect(
      [...localizedOperations.values()].filter((operation) => operation['x-codeSamples'])
        .length,
    ).toBe(20);
    expect(localizedOperations.get('POST /message/send')?.['x-wukongim-semantics']).toMatchObject({
      success: expect.stringContaining('reason'),
    });
    expect(localizedOperations.get('POST /message/syncack')?.['x-wukongim-semantics']).toMatchObject({
      scope: expect.stringContaining('last_message_seq'),
    });
    expect(localizedOperations.get('POST /channel')?.['x-wukongim-semantics']).toMatchObject({
      atomicity: expect.stringContaining('not one transaction'),
    });
  });

  test('encodes runtime request branches as executable JSON Schema', () => {
    expect(
      validateSchema('SendMessageRequest', {
        from_uid: '',
        sender_uid: 'alice',
        channel_id: 'team-42',
        channel_type: 2,
        payload: 'e30=',
      }).valid,
    ).toBe(true);
    expect(
      validateSchema('SendMessageRequest', {
        from_uid: 'alice',
        payload: 'e30=',
      }).valid,
    ).toBe(false);
    expect(
      validateSchema('SendMessageRequest', {
        from_uid: 'alice',
        channel_id: '',
        subscribers: ['bob'],
        payload: 'e30=',
      }).valid,
    ).toBe(false);
    expect(
      validateSchema('SendMessageRequest', {
        from_uid: 'alice',
        channel_id: '',
        subscribers: ['bob'],
        header: { sync_once: 1 },
        payload: 'e30=',
      }).valid,
    ).toBe(true);

    expect(
      validateSchema('ChannelUpsertRequest', {
        channel_id: 'a#b',
        channel_type: 2,
      }).valid,
    ).toBe(false);
    expect(
      validateSchema('ChannelUpsertRequest', {
        channel_id: 'alice',
        channel_type: 1,
        subscribers: ['bob'],
      }).valid,
    ).toBe(false);
    expect(
      validateSchema('ChannelSubscriberAddRequest', {
        channel_id: 'team-42',
        channel_type: 1,
        subscribers: ['bob'],
      }).valid,
    ).toBe(false);
    expect(
      validateSchema('ChannelSubscriberAddRequest', {
        channel_id: 'team-42',
        channel_type: 2,
        temp_subscriber: 1,
        subscribers: ['bob'],
      }).valid,
    ).toBe(false);

    expect(
      validateSchema('AppendMessageEventRequest', {
        channel_id: 'team-42',
        channel_type: 2,
        client_msg_no: 'm-1',
        event_id: 'e-1',
        event_type: ' STREAM.FINISH ',
      }).valid,
    ).toBe(true);
    expect(
      validateSchema('AppendMessageEventRequest', {
        channel_id: 'team-42',
        channel_type: 2,
        client_msg_no: 'm-1',
        event_id: 'e-1',
        event_type: 'custom.event',
      }).valid,
    ).toBe(false);
  });

  test('corrects the quickstart sync profile drift in the complete contract', async () => {
    const syncSource = await source('channel_messagesync.go');
    const usecaseSource = await Bun.file(
      new URL('../../internal/usecase/message/sync.go', import.meta.url),
    ).text();
    const request = schema('ChannelMessageSyncRequest');

    expect(syncSource).toContain('ChannelType      uint8');
    expect(usecaseSource).toContain('if query.ChannelType == 0');
    expect(request.properties?.channel_type).toMatchObject({ minimum: 1, maximum: 255 });
    expect(request.properties?.pull_mode).toMatchObject({
      default: 0,
      minimum: 0,
      maximum: 255,
    });
    expect(request.properties?.pull_mode?.enum).toBeUndefined();
    expect(schema('LegacyMessage').properties?.payload?.type).toEqual([
      'string',
      'null',
    ]);
  });

  test('models the full parser instead of repeating the three narrow profiles', () => {
    expect(schema('UpdateTokenRequest').required).toEqual(['uid', 'token']);
    expect(schema('UpdateTokenRequest').additionalProperties).toBe(true);

    const route = document.paths['/route'].get as Operation;
    expect(route.parameters?.map(parameterName)).toEqual([
      'node_id',
      'nodeId',
      'nodeID',
      'intranet',
    ]);

    const send = schema('SendMessageRequest');
    for (const field of [
      'from_uid',
      'sender_uid',
      'device_id',
      'setting',
      'topic',
      'expire',
      'subscribers',
      'header',
      'no_persist',
      'sync_once',
    ]) {
      expect(send.properties).toHaveProperty(field);
    }
    expect(send.required).not.toContain('client_msg_no');
    expect(send.additionalProperties).toBe(true);
  });

  test('matches every documented JSON request field to the runtime request DTO', async () => {
    const dtoContracts = [
      ['UpdateTokenRequest', 'user_token.go', 'updateTokenRequest'],
      ['DeviceQuitRequest', 'user_legacy.go', 'deviceQuitRequest'],
      ['SystemUIDsRequest', 'user_legacy.go', 'systemUIDsRequest'],
      ['ChannelInfoRequest', 'channel_management.go', 'channelInfoRequest'],
      ['ChannelUpsertRequest', 'channel_management.go', 'channelUpsertRequest'],
      ['WeakChannelKeyRequest', 'channel_management.go', 'channelKeyRequest'],
      ['ChannelSubscriberAddRequest', 'channel_management.go', 'channelSubscriberRequest'],
      ['ChannelSubscriberRemoveRequest', 'channel_management.go', 'channelSubscriberRequest'],
      ['NonPersonChannelKeyRequest', 'channel_management.go', 'channelKeyRequest'],
      ['TemporarySubscriberSetRequest', 'channel_management.go', 'tmpChannelSubscriberRequest'],
      ['ChannelMemberMutationRequest', 'channel_management.go', 'channelMemberRequest'],
      ['ChannelMemberSetRequest', 'channel_management.go', 'channelMemberRequest'],
      ['ChannelAllowlistMutationRequest', 'channel_management.go', 'channelMemberRequest'],
      ['ChannelKeyRequest', 'channel_management.go', 'channelKeyRequest'],
      ['SendMessageHeaderRequest', 'message_send.go', 'sendMessageHeaderRequest'],
      ['SendMessageRequest', 'message_send.go', 'sendMessageRequest'],
      ['AppendMessageEventRequest', 'message_event.go', 'appendMessageEventRequest'],
      ['MessageSyncRequest', 'message_sync.go', 'messageSyncRequest'],
      ['MessageSyncAckRequest', 'message_sync.go', 'messageSyncAckRequest'],
      ['MessageCMDBindingRequest', 'message_sync.go', 'messageCMDBindingRequest'],
      ['ChannelMessageSyncRequest', 'channel_messagesync.go', 'syncChannelMessagesRequest'],
      ['ChannelMessageSyncBatchItemRequest', 'channel_messagesync.go', 'syncChannelMessagesRequest'],
      ['ChannelMessageSyncBatchRequest', 'channel_messagesync.go', 'syncChannelMessagesBatchRequest'],
      ['ConversationListRequest', 'conversation_list.go', 'conversationListRequest'],
      ['ConversationKey', 'conversation_list.go', 'conversationListKey'],
      ['ConversationRetryRequest', 'conversation_list.go', 'conversationRetryRequest'],
      ['ConversationMutationRequest', 'conversation_mutation.go', 'clearConversationUnreadRequest'],
      ['ConversationSetUnreadRequest', 'conversation_mutation.go', 'setConversationUnreadRequest'],
      ['ConversationSyncLegacyRequest', 'conversation_sync_legacy.go', 'conversationSyncLegacyRequest'],
    ] as const;
    const files = new Map<string, string>();

    for (const [schemaName, sourceFile, typeName] of dtoContracts) {
      let file = files.get(sourceFile);
      if (!file) {
        file = await source(sourceFile);
        files.set(sourceFile, file);
      }
      expect(Object.keys(schema(schemaName).properties ?? {}).sort()).toEqual(
        jsonFieldsForStruct(file, typeName),
      );
    }
  });

  test('explains every query and JSON-body parameter in both published locales', () => {
    for (const locale of ['zh', 'en'] as const) {
      const localized = localizeOpenAPIDocument(document, locale) as typeof document;
      const descriptions: string[] = [];
      const visited = new Set<string>();

      function visitInputSchema(candidate: Schema | undefined) {
        if (!candidate) return;
        const referencedName = componentName(candidate.$ref, 'schemas');
        if (referencedName) {
          if (visited.has(referencedName)) return;
          visited.add(referencedName);
        }
        const resolved = referencedName
          ? (localized.components.schemas as unknown as Record<string, Schema>)[referencedName]
          : candidate;
        expect(resolved, referencedName).toBeDefined();
        if (!resolved) return;
        const isNamedOrComposite = Boolean(
          referencedName ||
            resolved.properties ||
            resolved.items ||
            resolved.oneOf ||
            resolved.anyOf,
        );
        if (isNamedOrComposite) {
          expect(resolved.description, referencedName ?? 'inline input schema').not.toBe(
            '',
          );
          if (resolved.description) descriptions.push(resolved.description);
        }
        for (const [fieldName, field] of Object.entries(resolved.properties ?? {})) {
          expect(field.description, `${referencedName}.${fieldName}`).not.toBe('');
          if (field.description) descriptions.push(field.description);
          visitInputSchema(field);
          visitInputSchema(field.items);
          for (const alternative of [...(field.oneOf ?? []), ...(field.anyOf ?? [])]) {
            visitInputSchema(alternative);
          }
        }
        visitInputSchema(resolved.items);
      }

      for (const pathItem of Object.values(localized.paths)) {
        for (const [method, candidate] of Object.entries(pathItem)) {
          if (method !== 'get' && method !== 'post') continue;
          const operation = candidate as Operation;
          for (const rawParameter of operation.parameters ?? []) {
            const name = componentName(rawParameter.$ref, 'parameters');
            const parameter = name
              ? (localized.components.parameters as Record<string, Parameter>)[name]
              : rawParameter;
            expect(parameter?.description, name ?? parameter?.name).not.toBe('');
            if (parameter?.description) descriptions.push(parameter.description);
          }
          const bodyName = componentName(operation.requestBody?.$ref, 'requestBodies');
          if (!bodyName) continue;
          const body = localized.components.requestBodies[
            bodyName as keyof typeof localized.components.requestBodies
          ] as { description?: string; content?: { 'application/json'?: { schema?: Schema } } };
          expect(body.description, bodyName).not.toBe('');
          if (body.description) descriptions.push(body.description);
          visitInputSchema(body.content?.['application/json']?.schema);
        }
      }

      expect(descriptions.length).toBeGreaterThan(100);
      for (const description of descriptions) {
        expect(description).toMatch(locale === 'zh' ? /\p{Script=Han}/u : /[A-Za-z]/);
      }
    }
  });

  test('explains every structured response field in both published locales', () => {
    const responseSchemas = [
      'StatusEnvelope',
      'CompatibilityError',
      'MaintenanceError',
      'RouteResponse',
      'RouteBatchItem',
      'UserOnlineStatus',
      'ChannelMember',
      'SendMessageResponse',
      'SendError',
      'RetryRequiredError',
      'AppendMessageEventData',
      'AppendMessageEventResponse',
      'LegacyMessageHeader',
      'LegacyMessageEventKeyMeta',
      'LegacyMessageEventMeta',
      'LegacyMessageEventSyncHint',
      'LegacyMessage',
      'ChannelMessageSyncResponse',
      'ChannelMessageSyncBatchItemResponse',
      'ChannelMessageSyncBatchResponse',
      'ConversationListResponse',
      'ConversationListItem',
      'ConversationLastMessage',
      'ConversationSyncLegacyItem',
    ];

    for (const locale of ['zh', 'en'] as const) {
      const localized = localizeOpenAPIDocument(document, locale) as typeof document;
      for (const name of responseSchemas) {
        const candidate = (localized.components.schemas as unknown as Record<string, Schema>)[name]!;
        expect(candidate.description, name).not.toBe('');
        for (const [field, value] of Object.entries(candidate.properties ?? {})) {
          expect(value.description, `${name}.${field}`).not.toBe('');
          expect(value.description).toMatch(
            locale === 'zh' ? /\p{Script=Han}/u : /[A-Za-z]/,
          );
        }
      }
    }
  });

  test('keeps compatibility-only parser behavior visible instead of normalizing it away', () => {
    expect(schema('RouteBatchItem').required).not.toContain('uids');
    expect(schema('SystemUIDListResponse').type).toEqual(['array', 'null']);
    expect(schema('ChannelInfoRequest').required ?? []).toEqual([]);
    expect(schema('ChannelMemberSetRequest').required).toEqual(['channel_id']);
    expect(schema('DeviceQuitRequest').properties?.device_flag?.enum).toBeUndefined();
    expect(schema('OnlineStatusResponse').oneOf).toHaveLength(2);
    expect(schema('MessageSyncRequest').properties?.message_seq).toBeDefined();
    expect(schema('MessageSyncAckRequest').properties?.last_message_seq).toMatchObject({
      minimum: 1,
    });
    expect(schema('RouteBatchRequest').type).toEqual(['array', 'null']);
    expect(schema('OnlineStatusRequest').type).toEqual(['array', 'null']);
    expect(schema('SystemUIDsRequest').properties?.uids?.type).toEqual([
      'array',
      'null',
    ]);
    expect(schema('SendMessageRequest').properties?.header?.oneOf).toEqual([
      { $ref: '#/components/schemas/SendMessageHeaderRequest' },
      { type: 'null' },
    ]);
    expect(schema('ChannelUpsertRequest').properties?.channel_id?.pattern).toContain(
      '\\S',
    );
    expect(schema('AppendMessageEventRequest').properties?.event_id?.pattern).toBe(
      '\\S',
    );
    expect(schema('MessageSyncRequest').properties?.uid?.pattern).toBe('\\S');
    expect(document.paths['/conversation/sync'].post.deprecated).toBe(true);
    expect(document.paths['/user/systemuids_add_to_cache'].post['x-wukongim-trust']).toBe(
      'node-local-operator-only',
    );
  });
});
