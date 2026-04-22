import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const clientRoutePath = resolve(__dirname, './docs-route.tsx');
const clientRouteModule = readFileSync(clientRoutePath, 'utf8');

test('docs route view stays client-safe and does not import the server source loader', () => {
  assert.doesNotMatch(clientRouteModule, /from ['"]@\/lib\/source['"]/);
  assert.doesNotMatch(clientRouteModule, /export\s+async\s+function\s+loadDocsPage/);
});
