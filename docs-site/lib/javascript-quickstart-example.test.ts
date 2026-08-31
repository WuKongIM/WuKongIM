import { describe, expect, test } from 'bun:test';
import openapi from '../contracts/javascript-web-quickstart.openapi.json';
import { MAX_PERSON_MESSAGE_SYNC_LIMIT } from '../examples/javascript-web-quickstart/src/server/bff';

const sampleRoot = new URL('../examples/javascript-web-quickstart/', import.meta.url);

async function sampleText(path: string) {
  return Bun.file(new URL(path, sampleRoot)).text();
}

describe('JavaScript Web quickstart example', () => {
  test('stays small, pinned, and runnable with standard Node.js tooling', async () => {
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
    expect(manifest.scripts).toMatchObject({
      build: 'node scripts/build.mjs && tsc --noEmit',
      test: 'tsx --test test/*.test.ts',
      check: 'npm test && npm run build',
    });
    expect(manifest.scripts).not.toHaveProperty('test:e2e');
    expect(manifest.scripts).not.toHaveProperty('verify:acceptance');
    expect(manifest.devDependencies).not.toHaveProperty('@playwright/test');
    expect(manifest.devDependencies).not.toHaveProperty('@axe-core/playwright');
    expect(lock.lockfileVersion).toBe(3);
    expect(lock.packages['node_modules/wukongimjssdk']?.version).toBe('1.3.5');

    for (const removed of [
      'playwright.config.ts',
      'scripts/verify-acceptance.ts',
      'src/acceptance/report.ts',
      'test/acceptance-report.test.ts',
      'e2e/quickstart.spec.ts',
    ]) {
      expect(await Bun.file(new URL(removed, sampleRoot)).exists()).toBe(false);
    }

    const readme = await sampleText('README.md');
    expect(readme).toContain('npm run dev');
    expect(readme).toContain('npm run build');
    expect(readme).not.toMatch(/acceptance|golden-path|receipt|evidence report/iu);
  });

  test('keeps Product HTTP behind the local service', async () => {
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

    const buildScript = await sampleText('scripts/build.mjs');
    expect(buildScript).toMatch(/drop:\s*\["console"\]/);
  });
});
