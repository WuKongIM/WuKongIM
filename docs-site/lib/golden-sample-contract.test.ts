import { describe, expect, test } from 'bun:test';
import openapi from '../contracts/javascript-web-quickstart.openapi.json';
import { MAX_PERSON_MESSAGE_SYNC_LIMIT } from '../examples/javascript-web-quickstart/src/server/bff';

const sampleRoot = new URL('../examples/javascript-web-quickstart/', import.meta.url);

async function sampleText(path: string) {
  return Bun.file(new URL(path, sampleRoot)).text();
}

describe('JavaScript Web golden sample contract', () => {
  test('pins the verified SDK and browser harness in npm metadata', async () => {
    const manifest = JSON.parse(await sampleText('package.json')) as {
      engines: { node: string };
      dependencies: Record<string, string>;
      devDependencies: Record<string, string>;
      scripts: Record<string, string>;
    };
    const lock = JSON.parse(await sampleText('package-lock.json')) as {
      lockfileVersion: number;
      packages: Record<string, { version?: string }>;
    };

    expect(manifest.engines.node).toBe('>=20.11');
    expect(manifest.dependencies.wukongimjssdk).toBe('1.3.5');
    expect(manifest.devDependencies['@axe-core/playwright']).toBe('4.13.0');
    expect(manifest.devDependencies['@playwright/test']).toBe('1.62.1');
    expect(manifest.scripts.check).toBeTruthy();
    expect(manifest.scripts['test:e2e']).toBeTruthy();
    expect(lock.lockfileVersion).toBe(3);
    expect(lock.packages['node_modules/wukongimjssdk']?.version).toBe('1.3.5');
    expect(lock.packages['node_modules/@axe-core/playwright']?.version).toBe('4.13.0');
    expect(lock.packages['node_modules/@playwright/test']?.version).toBe('1.62.1');
  });

  test('keeps every executable documentation anchor paired and MDX checkpoints present', async () => {
    const requiredRegions = [
      'bff-provision-identity',
      'bff-sync-messages',
      'browser-provision-identity',
      'browser-sync-messages',
      'product-http-token',
      'product-http-route',
      'product-http-message-sync',
      'sdk-configure-and-connect',
      'sdk-send-text',
      'sdk-reconnect-sync',
    ];
    const sourceFiles: string[] = [];
    for await (const path of new Bun.Glob('src/**/*.ts').scan({
      cwd: new URL('.', sampleRoot).pathname,
      onlyFiles: true,
    })) {
      sourceFiles.push(path);
    }

    const starts = new Map<string, number>();
    const ends = new Map<string, number>();
    for (const file of sourceFiles) {
      const source = await sampleText(file);
      for (const match of source.matchAll(/docs:start ([a-z0-9-]+)/g)) {
        starts.set(match[1], (starts.get(match[1]) ?? 0) + 1);
      }
      for (const match of source.matchAll(/docs:end ([a-z0-9-]+)/g)) {
        ends.set(match[1], (ends.get(match[1]) ?? 0) + 1);
      }
    }

    expect([...starts.keys()].sort()).toEqual(requiredRegions.sort());
    expect([...ends.keys()].sort()).toEqual(requiredRegions.sort());
    expect([...starts.values()].every((count) => count === 1)).toBe(true);
    expect([...ends.values()].every((count) => count === 1)).toBe(true);

    for (const path of [
      '../content/docs/sdk/javascript/installation.mdx',
      '../content/docs/sdk/javascript/installation.en.mdx',
    ]) {
      expect(await Bun.file(new URL(path, import.meta.url)).text()).toContain(
        'PHASE12:GOLDEN_SAMPLE_INSTALL_SNIPPET',
      );
    }
    for (const path of [
      '../content/docs/sdk/javascript/quickstart.mdx',
      '../content/docs/sdk/javascript/quickstart.en.mdx',
    ]) {
      const page = await Bun.file(new URL(path, import.meta.url)).text();
      expect(page).toContain('PHASE12:GOLDEN_SAMPLE_START_SNIPPET');
      expect(page).toContain('PHASE12:GOLDEN_SAMPLE_RECOVERY_SNIPPET');
    }
  });

  test('keeps Product HTTP behind the BFF and aligns its bounded sync contract', async () => {
    const browserSources = await Promise.all(
      ['src/client/browser-bff.ts', 'src/client/sdk-runtime.ts', 'src/client/session.ts'].map(
        sampleText,
      ),
    );
    for (const source of browserSources) {
      expect(source).not.toContain('/user/token');
      expect(source).not.toContain('/channel/messagesync');
      expect(source).not.toMatch(/["'`]\/route(?:[?"'`])/);
    }

    const productClient = await sampleText('src/server/product-http-client.ts');
    for (const path of ['/user/token', '/route', '/channel/messagesync']) {
      expect(productClient).toContain(path);
    }
    expect(
      openapi.components.schemas.ChannelMessageSyncRequest.properties.limit.maximum,
    ).toBe(MAX_PERSON_MESSAGE_SYNC_LIMIT);

    expect(Object.keys(openapi.paths).sort()).toEqual(
      ['/channel/messagesync', '/route', '/user/token'].sort(),
    );
    expect(openapi.paths['/route'].get).not.toHaveProperty('parameters');
    for (const operation of [
      openapi.paths['/user/token'].post,
      openapi.paths['/route'].get,
      openapi.paths['/channel/messagesync'].post,
    ]) {
      expect(Object.keys(operation.responses).sort()).toEqual(['200', '400', '503']);
    }
    expect(openapi.components.schemas.UpdateTokenRequest.required).toEqual([
      'uid',
      'token',
      'device_flag',
      'device_level',
    ]);
    expect(openapi.components.schemas.ChannelMessageSyncRequest.required).toEqual([
      'login_uid',
      'channel_id',
      'channel_type',
    ]);
    expect(openapi.components.schemas.SyncedMessage.required).toContain(
      'message_idstr',
    );
    expect(openapi.components.schemas.ErrorEnvelope.properties.status).toMatchObject({
      minimum: 400,
      maximum: 599,
    });

    const buildScript = await sampleText('scripts/build.mjs');
    expect(buildScript).toMatch(/drop:\s*\["console"\]/);
  });
});
