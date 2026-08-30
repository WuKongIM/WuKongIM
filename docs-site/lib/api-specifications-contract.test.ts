import { describe, expect, test } from 'bun:test';
import goldenPath from '../contracts/javascript-web-quickstart.openapi.json';
import operations from '../contracts/operations-http.openapi.json';
import management from '../contracts/product-http-management.openapi.json';
import messaging from '../contracts/product-http-messaging.openapi.json';
import productHTTP from '../contracts/product-http.openapi.json';
import webhooks from '../contracts/webhooks.openapi.json';

const specificationPages = [
  'index',
  'openapi',
  'json-rpc-schema',
  'protocol-changelog',
] as const;

const contractLinks = [
  '/contracts/product-http.openapi.json',
  '/contracts/operations-http.openapi.json',
  '/contracts/webhooks.openapi.json',
  '/contracts/javascript-web-quickstart.openapi.json',
  '/contracts/product-http-messaging.openapi.json',
  '/contracts/product-http-management.openapi.json',
] as const;

async function source(relativePath: string) {
  return Bun.file(new URL(relativePath, import.meta.url)).text();
}

async function page(slug: (typeof specificationPages)[number], locale: 'zh' | 'en') {
  const suffix = locale === 'en' ? '.en.mdx' : '.mdx';
  return source(`../content/docs/api/specifications/${slug}${suffix}`);
}

function operationCount(document: { paths: Record<string, Record<string, unknown>> }) {
  return Object.values(document.paths).reduce(
    (total, pathItem) =>
      total + Object.keys(pathItem).filter((key) => key !== 'parameters').length,
    0,
  );
}

describe('API specification pages', () => {
  test('provides every concise page in both locales', async () => {
    for (const slug of specificationPages) {
      const [zh, en] = await Promise.all([page(slug, 'zh'), page(slug, 'en')]);

      for (const content of [zh, en]) {
        expect(content).toMatch(/^---\ntitle: .+\ndescription: .+\n---/u);
        expect(content.trim().split('\n').length).toBeLessThanOrEqual(55);
      }
      expect(zh).not.toContain('/en/api/');
      expect(en).not.toContain('/zh/api/');
    }
  });

  test('keeps the Specifications indexes locale-correct', async () => {
    const [zh, en] = await Promise.all([page('index', 'zh'), page('index', 'en')]);

    for (const slug of ['openapi', 'json-rpc-schema', 'protocol-changelog']) {
      expect(zh).toContain(`href="/zh/api/specifications/${slug}"`);
      expect(en).toContain(`href="/en/api/specifications/${slug}"`);
    }
    expect(zh).toContain('/zh/api/client-protocols/tcp-binary');
    expect(en).toContain('/en/api/client-protocols/tcp-binary');
  });

  test('lists the three complete OpenAPI contracts and three narrow profiles', async () => {
    const [zh, en] = await Promise.all([page('openapi', 'zh'), page('openapi', 'en')]);

    for (const link of contractLinks) {
      expect(zh).toContain(`](${link})`);
      expect(en).toContain(`](${link})`);
      const artifact = await Bun.file(new URL(`..${link}`, import.meta.url)).exists();
      expect(artifact).toBeTrue();
    }

    expect(operationCount(productHTTP)).toBe(41);
    expect(operationCount(operations)).toBe(4);
    expect(Object.keys(webhooks.webhooks)).toEqual([
      'msg.notify',
      'msg.offline',
      'user.onlinestatus',
    ]);
    expect(operationCount(goldenPath)).toBe(3);
    expect(operationCount(messaging)).toBe(1);
    expect(operationCount(management)).toBe(16);

    for (const content of [zh, en]) {
      for (const count of ['41', '3', '1', '16']) expect(content).toContain(count);
      expect(content).toContain('OpenAPI 3.1');
      expect(content).toContain('webhooks');
      expect(content).toContain('/metrics');
    }
  });

  test('labels the bounded EasySDK core supported while keeping the wider JSON-RPC surface experimental', async () => {
    const [zh, en, rawSchema] = await Promise.all([
      page('json-rpc-schema', 'zh'),
      page('json-rpc-schema', 'en'),
      source('../contracts/json-rpc.experimental.schema.json'),
    ]);
    const schema = JSON.parse(rawSchema) as {
      $schema: string;
      'x-wukongim-stability': string;
      anyOf: unknown[];
    };

    for (const content of [zh, en]) {
      expect(content).toContain('/contracts/json-rpc.experimental.schema.json');
      expect(content).toContain('experimental-easysdk-core-supported');
      expect(content).toContain('ping');
      expect(content).toContain('connect');
      expect(content).toContain('send');
    }
    expect(zh).toContain('EasySDK 核心路径已支持');
    expect(en).toContain('EasySDK core path supported');
    expect(zh).toContain('/zh/api/client-protocols/json-rpc');
    expect(en).toContain('/en/api/client-protocols/json-rpc');
    expect(schema.$schema).toBe('https://json-schema.org/draft/2020-12/schema');
    expect(schema['x-wukongim-stability']).toBe('experimental-easysdk-core-supported');
    expect(schema.anyOf.length).toBeGreaterThan(1);
  });

  test('records the source-defined v5 to v6 sequence-width change', async () => {
    const [zh, en, frames] = await Promise.all([
      page('protocol-changelog', 'zh'),
      page('protocol-changelog', 'en'),
      source('../../pkg/protocol/frame/common.go'),
    ]);
    const legacyVersion = frames.match(/LegacyMessageSeqVersion\s*=\s*(\d+)/u)?.[1];
    const latestVersion = frames.match(/MessageSeqU64Version\s*=\s*(\d+)/u)?.[1];
    const clientSeqBytes = frames.match(/ClientSeqByteSize\s*=\s*(\d+)/u)?.[1];
    const legacyBytes = frames.match(/MessageSeqLegacyByteSize\s*=\s*(\d+)/u)?.[1];
    const latestBytes = frames.match(/MessageSeqU64ByteSize\s*=\s*(\d+)/u)?.[1];

    expect({ legacyVersion, latestVersion, clientSeqBytes, legacyBytes, latestBytes }).toEqual({
      legacyVersion: '5',
      latestVersion: '6',
      clientSeqBytes: '4',
      legacyBytes: '4',
      latestBytes: '8',
    });
    for (const content of [zh, en]) {
      expect(content).toContain('v5');
      expect(content).toContain('v6');
      expect(content).toContain('SENDACK.message_seq');
      expect(content).toContain('RECV.message_seq');
      expect(content).toContain('client_seq');
      expect(content).toContain('32');
      expect(content).toContain('64');
      expect(content).toContain('experimental-easysdk-core-supported');
    }
    expect(zh).toContain('破坏性变更');
    expect(en).toContain('Breaking changes');
  });
});
