import { expect, test } from 'bun:test';
import Ajv2020 from 'ajv/dist/2020';
import document from '../contracts/product-http.openapi.json';

const validator = new Ajv2020({ strict: false });
const schemas = document.components.schemas as Record<string, unknown>;
const paths = document.paths as unknown as Record<string, Record<string, {
  security?: unknown[];
  requestBody?: { $ref?: string };
  parameters?: { name?: string; required?: boolean }[];
  responses: Record<string, unknown>;
}>>;

function validate(name: string, value: unknown) {
  expect(schemas[name]).toBeDefined();
  return validator.compile({ $ref: `#/components/schemas/${name}`, components: document.components })(value);
}

test('publishes both policy scopes with typed CAS and unavailable outcomes', () => {
  for (const scope of ['user', 'channel']) {
    const path = paths[`/${scope}/send_ban`];
    expect(path).toBeDefined();
    for (const method of ['get', 'post']) {
      expect(path![method]!.security).toEqual([]);
      const expected = ['200', '400', '503'];
      if (scope === 'channel') expected.push('404');
      if (method === 'post') expected.push('409');
      expect(Object.keys(path![method]!.responses).sort()).toEqual(expected.sort());
    }
    expect(path!.post!.requestBody?.$ref).toBe(`#/components/requestBodies/${scope === 'user' ? 'User' : 'Channel'}SendBan`);
    expect(path!.get!.parameters?.filter((p) => p.required).map((p) => p.name).sort()).toEqual(scope === 'user' ? ['uid'] : ['channel_id', 'channel_type']);
  }
});

test('send-ban schemas preserve strict flags, unknown-field rejection and decimal uint64 CAS', () => {
  for (const [name, identity] of [
    ['UserSendBanRequest', { uid: 'alice' }],
    ['ChannelSendBanRequest', { channel_id: 'team', channel_type: 2 }],
  ] as const) {
    const valid = { ...identity, send_ban: 1, expected_version: '18446744073709551615' };
    expect(validate(name, valid)).toBe(true);
    expect(validate(name, { ...identity, send_ban: 0, expected_version: null })).toBe(true);
    for (const invalid of [
      { ...identity }, { ...identity, send_ban: null }, { ...valid, send_ban: 2 },
      { ...valid, send_ban: -1 }, { ...valid, unknown: 1 },
      { ...valid, expected_version: 1 }, { ...valid, expected_version: '01' },
      { ...valid, expected_version: '+1' }, { ...valid, expected_version: '18446744073709551616' },
    ]) expect(validate(name, invalid)).toBe(false);
  }
});

test('documents precise send-ban response fields without numeric policy versions', () => {
  for (const [name, identity] of [
    ['UserSendBanResponse', { uid: 'alice' }],
    ['ChannelSendBanResponse', { channel_id: 'team', channel_type: 2 }],
  ] as const) {
    const data = { ...identity, send_ban: 1, send_ban_version: '18446744073709551615' };
    expect(validate(name, { status: 200, data })).toBe(true);
    expect(validate(name, { status: 200, data: { ...data, send_ban_version: 1 } })).toBe(false);
  }
  expect(validate('SendBanError', { status: 409, code: 'version_conflict', msg: 'version_conflict' })).toBe(true);
});

test('legacy channel metadata describes omitted versus explicit strict send-ban', () => {
  for (const name of ['ChannelUpsertRequest', 'ChannelInfoRequest']) {
    const schema = schemas[name] as { properties: Record<string, { enum?: (number | null)[]; default?: number }> };
    expect(schema.properties.send_ban?.enum).toEqual([0, 1, null]);
    expect(schema.properties.send_ban).not.toHaveProperty('default');
  }
});
