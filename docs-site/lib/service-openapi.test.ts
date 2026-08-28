import { describe, expect, test } from 'bun:test';
import operations from '../contracts/operations-http.openapi.json';
import webhooks from '../contracts/webhooks.openapi.json';
import { openapi } from './openapi';
import {
  localizedServiceOpenAPIDocumentId,
  serviceOpenAPIContracts,
} from './service-openapi';

async function source(relativePath: string) {
  return Bun.file(new URL(relativePath, import.meta.url)).text();
}

describe('service OpenAPI contracts', () => {
  test('keeps the Operations contract aligned with API registration and stability', async () => {
    const server = await source('../../internal/access/api/server.go');
    const contracted = Object.keys(operations.paths).sort();

    expect(contracted).toEqual([
      '/healthz',
      '/metrics',
      '/readyz',
      '/top/v1/snapshot',
    ]);
    expect(server).toContain('s.engine.GET("/healthz"');
    expect(server).toContain('s.engine.GET("/readyz"');
    expect(server).toContain('s.engine.Any("/metrics"');
    expect(server).toContain('s.engine.GET("/top/v1/snapshot"');
    expect(operations['x-wukongim-scope']).toBe('operations-http-beta');
    expect(operations.info.description).toContain('Top is unstable');

    for (const pathItem of Object.values(operations.paths)) {
      const operation = pathItem.get;
      expect(operation.security).toEqual([]);
      expect(operation['x-wukongim-trust']).toBe('operator-network-only');
    }
  });

  test('records exact Top query defaults and root response fields', () => {
    const operation = operations.paths['/top/v1/snapshot'].get;
    const parameters = Object.fromEntries(
      operation.parameters.map((parameter) => [parameter.name, parameter.schema]),
    );
    expect(parameters).toMatchObject({
      window: { default: '10s' },
      view: { default: 'overview' },
      limit: { default: 20, minimum: 1 },
    });
    expect(parameters.view.enum).toEqual([
      'overview',
      'runtime',
      'traffic',
      'channel',
      'storage',
      'delivery',
      'all',
    ]);
    expect(operations.components.schemas.TopSnapshot.required).toEqual([
      'version',
      'scope',
      'generated_at',
      'window_seconds',
      'node',
      'verdict',
      'sources',
    ]);
  });

  test('keeps the OpenAPI webhook set and payload branches source-aligned', async () => {
    const [types, mapper, sender] = await Promise.all([
      source('../../internal/runtime/webhook/types.go'),
      source('../../internal/runtime/webhook/mapper.go'),
      source('../../internal/runtime/webhook/sender.go'),
    ]);
    const names = Object.keys(webhooks.webhooks).sort();

    expect(names).toEqual(['msg.notify', 'msg.offline', 'user.onlinestatus']);
    for (const event of names) expect(types).toContain(`= "${event}"`);
    expect(mapper).toContain('json:"compress_to_uids,omitempty"');
    expect(mapper).toContain('json:"source_id,omitempty"');
    expect(sender).toContain('httpReq.Header.Set("Content-Type", "application/json")');
    expect(sender).toContain('resp.StatusCode != http.StatusOK');
    expect(sender).not.toContain('Authorization');

    const offline = webhooks.components.schemas.OfflineWebhook;
    expect(offline.unevaluatedProperties).toBe(false);
    expect(offline.allOf[1].oneOf).toHaveLength(2);
    expect(webhooks.webhooks['user.onlinestatus'].post.requestBody.content[
      'application/json'
    ].schema.items.description).toContain(
      'uid-deviceFlag-online-sessionID-deviceOnlineCount-totalOnlineCount',
    );
    expect(webhooks.webhooks['user.onlinestatus'].post.description).toContain(
      "UID owner's current node",
    );
  });

  test('loads localized Operations and Webhook schemas in Fumadocs', async () => {
    for (const contract of ['operations', 'webhooks'] as const) {
      for (const locale of ['zh', 'en'] as const) {
        const id = localizedServiceOpenAPIDocumentId(contract, locale);
        const schema = await openapi.getSchema(id);
        expect(schema.bundled.info?.title).toBeTruthy();
        expect(serviceOpenAPIContracts[contract].download).toStartWith('/contracts/');
      }
    }
  });

  test('renders Operations and Webhooks with the shared Fumadocs component', async () => {
    for (const locale of ['zh', 'en'] as const) {
      const suffix = locale === 'en' ? '.en' : '';
      const operationsId = localizedServiceOpenAPIDocumentId('operations', locale);
      const webhooksId = localizedServiceOpenAPIDocumentId('webhooks', locale);
      const [health, metrics, top, payloads] = await Promise.all([
        source(`../content/docs/api/operations-http/health-and-readiness${suffix}.mdx`),
        source(`../content/docs/api/operations-http/metrics${suffix}.mdx`),
        source(`../content/docs/api/operations-http/read-only${suffix}.mdx`),
        source(`../content/docs/api/webhooks/payloads${suffix}.mdx`),
      ]);

      for (const page of [health, metrics, top]) {
        expect(page).toContain(`- ${operationsId}`);
        expect(page).toContain(`<OpenAPIPage document="${operationsId}"`);
      }
      expect(health).toContain('"path":"/healthz","method":"get"');
      expect(health).toContain('"path":"/readyz","method":"get"');
      expect(metrics).toContain('"path":"/metrics","method":"get"');
      expect(top).toContain('"path":"/top/v1/snapshot","method":"get"');
      expect(payloads).toContain(`- ${webhooksId}`);
      expect(payloads).toContain(`<OpenAPIPage document="${webhooksId}"`);
      for (const name of ['msg.notify', 'msg.offline', 'user.onlinestatus']) {
        expect(payloads).toContain(`"name":"${name}","method":"post"`);
      }
    }
  });

  test('publishes both contracts as static JSON routes', async () => {
    const [operationsRoute, webhooksRoute] = await Promise.all([
      source('../app/contracts/operations-http.openapi.json/route.ts'),
      source('../app/contracts/webhooks.openapi.json/route.ts'),
    ]);
    expect(operationsRoute).toContain("@/contracts/operations-http.openapi.json");
    expect(webhooksRoute).toContain("@/contracts/webhooks.openapi.json");
    expect(operationsRoute).toContain("dynamic = 'force-static'");
    expect(webhooksRoute).toContain("dynamic = 'force-static'");
  });
});
