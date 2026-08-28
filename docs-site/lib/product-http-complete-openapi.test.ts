import { describe, expect, test } from 'bun:test';
import { fileURLToPath } from 'node:url';
import document from '../contracts/product-http.openapi.json';
import goldenProfile from '../contracts/javascript-web-quickstart.openapi.json';
import managementProfile from '../contracts/product-http-management.openapi.json';
import messagingProfile from '../contracts/product-http-messaging.openapi.json';

const httpMethods = ['delete', 'get', 'patch', 'post', 'put'] as const;
type HTTPMethod = (typeof httpMethods)[number];

interface Schema {
  type?: string | string[];
  default?: unknown;
  minimum?: number;
  maximum?: number;
  enum?: unknown[];
  additionalProperties?: boolean;
  required?: string[];
  properties?: Record<string, Schema>;
  oneOf?: Schema[];
}

interface Operation {
  operationId?: string;
  tags?: string[];
  security?: unknown[];
  responses?: Record<string, unknown>;
  parameters?: Parameter[];
  deprecated?: boolean;
  'x-wukongim-trust'?: string;
}

interface Parameter {
  name?: string;
  in?: string;
  $ref?: string;
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
    expect(document.paths['/conversation/sync'].post.deprecated).toBe(true);
    expect(document.paths['/user/systemuids_add_to_cache'].post['x-wukongim-trust']).toBe(
      'node-local-operator-only',
    );
  });
});
