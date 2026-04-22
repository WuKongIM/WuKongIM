import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const routePath = resolve(__dirname, '../routes/docs.tsx');
const routeModule = readFileSync(routePath, 'utf8');

test('docs route stays server-rendered without a client hydration loader', () => {
  assert.doesNotMatch(routeModule, /export\s+async\s+function\s+clientLoader/);
  assert.doesNotMatch(routeModule, /clientLoader\.hydrate\s*=\s*true/);
});

test('docs route does not define a HydrateFallback shell', () => {
  assert.doesNotMatch(routeModule, /export\s+function\s+HydrateFallback/);
});
