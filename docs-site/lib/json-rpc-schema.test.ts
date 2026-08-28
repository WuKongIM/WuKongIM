import { describe, expect, test } from 'bun:test';
import schema from '../contracts/json-rpc.experimental.schema.json';
import {
  jsonRPCInboundSurface,
  jsonRPCOutboundSurface,
} from './protocol-surface-contracts';

async function source(relativePath: string) {
  return Bun.file(new URL(relativePath, import.meta.url)).text();
}

describe('experimental JSON-RPC schema', () => {
  test('is a codec schema and never a supported-product claim', () => {
    expect(schema.$schema).toBe('https://json-schema.org/draft/2020-12/schema');
    expect(schema['x-wukongim-stability']).toBe('experimental-not-supported');
    expect(schema.description).toContain('does not support JSON-RPC');
    expect(schema.$defs.PingRequest['x-wukongim-product-status']).toBe('works');
    expect(schema.$defs.ConnectRequest['x-wukongim-product-status']).toContain(
      'rejected',
    );
    expect(schema.$defs.SubscribeRequest['x-wukongim-product-status']).toContain(
      'bridge-missing',
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
    expect(types).toContain('Payload     []byte');
    expect(schema.$defs.ConnectParams.properties.deviceFlag).toEqual({
      type: 'integer',
    });
    expect(
      (schema.$defs.ConnectParams as { required?: string[] }).required,
    ).toBeUndefined();
    expect(
      (schema.$defs.SendParams as { required?: string[] }).required,
    ).toBeUndefined();
    expect(schema.$defs.SendParams.properties.payload.type).toEqual([
      'string',
      'null',
    ]);
  });

  test('publishes the schema from a deterministic static route', async () => {
    const route = await source(
      '../app/contracts/json-rpc.experimental.schema.json/route.ts',
    );
    expect(route).toContain("@/contracts/json-rpc.experimental.schema.json");
    expect(route).toContain("dynamic = 'force-static'");
  });
});
