import { describe, expect, test } from 'bun:test';
import Ajv2020 from 'ajv/dist/2020';
import schema from '../contracts/json-rpc.experimental.schema.json';
import {
  jsonRPCInboundSurface,
  jsonRPCOutboundSurface,
} from './protocol-surface-contracts';

async function source(relativePath: string) {
  return Bun.file(new URL(relativePath, import.meta.url)).text();
}

describe('experimental JSON-RPC schema', () => {
  test('publishes the bounded EasySDK core while keeping other RPC methods experimental', () => {
    expect(schema.$schema).toBe('https://json-schema.org/draft/2020-12/schema');
    expect(schema['x-wukongim-stability']).toBe('experimental-easysdk-core-supported');
    expect(schema.description).toContain('supports the pinned EasySDK');
    expect(schema.$defs.PingRequest['x-wukongim-product-status']).toBe('works');
    expect(schema.$defs.ConnectRequest['x-wukongim-product-status']).toContain('supported');
    expect(schema.$defs.SendRequest['x-wukongim-product-status']).toContain('supported');
    expect(schema.$defs.RecvAckNotification['x-wukongim-product-status']).toContain('supported');
    expect(schema.$defs.SubscribeRequest['x-wukongim-product-status']).toBe(
      'decoded-to-sub-frame-but-product-handler-rejects',
    );
    expect(schema.$defs.UnsubscribeRequest['x-wukongim-product-status']).toBe(
      'decoded-to-sub-frame-but-product-handler-rejects',
    );
    expect(schema.$defs.SubscriptionResponse['x-wukongim-product-status']).toContain(
      'product-handler-rejects-sub',
    );
  });

  test('covers every decoded inbound method and every encoded outbound frame', () => {
    const requestOrNotificationDefinitions = [
      'ConnectRequest',
      'SendRequest',
      'PingRequest',
      'DisconnectRequest',
      'SubscribeRequest',
      'UnsubscribeRequest',
      'RecvAckNotification',
      'RecvNotification',
      'DisconnectNotification',
      'EventNotification',
    ];
    expect(requestOrNotificationDefinitions).toHaveLength(jsonRPCInboundSurface.length);
    for (const definition of requestOrNotificationDefinitions) {
      expect(schema.$defs).toHaveProperty(definition);
    }

    const outboundDefinitionByFrame = {
      CONNACK: 'ConnectResponse',
      SENDACK: 'SendResponse',
      SUBACK: 'SubscriptionResponse',
      RECV: 'RecvNotification',
      EVENT: 'EventNotification',
      DISCONNECT: 'DisconnectNotification',
      PONG: 'PongResponse',
    } as const;
    for (const { frame } of jsonRPCOutboundSurface) {
      expect(schema.$defs).toHaveProperty(
        outboundDefinitionByFrame[frame as keyof typeof outboundDefinitionByFrame],
      );
    }
  });

  test('matches the permissive Go parser instead of the stale checked-in schema', async () => {
    const types = await source('../../pkg/protocol/jsonrpc/types.go');
    expect(types).toContain('DeviceApp DeviceFlagEnum = 0');
    expect(types).toContain('func (p *SendParams) UnmarshalJSON');
    expect(schema.$defs.ConnectParams.properties.deviceFlag).toEqual({
      type: 'integer',
    });
    expect(
      (schema.$defs.ConnectParams as { required?: string[] }).required,
    ).toBeUndefined();
    expect(
      (schema.$defs.SendParams as { required?: string[] }).required,
    ).toBeUndefined();
    expect(schema.$defs.SendParams.properties.payload.oneOf).toEqual([
      {
        type: 'string',
        description: expect.stringContaining('JSON-text string'),
      },
      { type: 'object' },
      { type: 'null' },
    ]);
    expect(schema.$defs.ConnectParams.properties.device_flag).toEqual({ type: 'integer' });
    expect(schema.$defs.RecvParams.required).toContain('header');
    expect(schema.anyOf).toContainEqual({ $ref: '#/$defs/GenericResponse' });
    expect(schema).not.toHaveProperty('oneOf');
    expect(schema.$defs.ResponseBase.not).toEqual({ required: ['method'] });
    expect(schema.$defs.GenericResponse.allOf[1].oneOf).toEqual([
      {
        required: ['result'],
        not: { required: ['error'] },
      },
      {
        required: ['error'],
        not: { required: ['result'] },
      },
    ]);
    expect(types).toContain('type GenericResponse struct');
  });

  test('validates runtime response exclusivity and the fixed pong result', () => {
    const ajv = new Ajv2020({ strict: false, validateFormats: false });
    ajv.addSchema(schema);
    const validateEnvelope = ajv.getSchema(schema.$id);
    const validatePong = ajv.getSchema(`${schema.$id}#/$defs/PongResponse`);
    expect(validateEnvelope).toBeDefined();
    expect(validatePong).toBeDefined();

    expect(
      validateEnvelope?.({
        jsonrpc: '2.0',
        id: 'connect-1',
        result: { reasonCode: 1 },
        error: { code: -32000, message: 'must not coexist' },
      }),
    ).toBe(false);
    expect(
      validateEnvelope?.({
        jsonrpc: '2.0',
        id: 'connect-1',
        result: { reasonCode: 1 },
      }),
    ).toBe(true);
    expect(validatePong?.({ jsonrpc: '2.0', id: 'ping-1', result: null })).toBe(true);
    expect(validatePong?.({ jsonrpc: '2.0', id: 'ping-1', result: {} })).toBe(false);
  });

  test('validates the released Android JSON-text SEND payload shape', () => {
    const ajv = new Ajv2020({ strict: false, validateFormats: false });
    const validateEnvelope = ajv.compile(schema);
    expect(
      validateEnvelope({
        jsonrpc: '2.0',
        id: 'android-send',
        method: 'send',
        params: {
          client_msg_no: 'android-1',
          channel_id: 'bob',
          channel_type: 1,
          payload: '{"content":"hello","type":1}',
        },
      }),
    ).toBe(true);
  });

  test('publishes the schema from a deterministic static route', async () => {
    const route = await source(
      '../app/contracts/json-rpc.experimental.schema.json/route.ts',
    );
    expect(route).toContain("@/contracts/json-rpc.experimental.schema.json");
    expect(route).toContain("dynamic = 'force-static'");
  });
});
