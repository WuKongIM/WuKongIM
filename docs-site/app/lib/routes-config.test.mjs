import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const routesPath = resolve(__dirname, '../routes.ts');
const routes = readFileSync(routesPath, 'utf8');

test('docs routing defines a dedicated index route for the homepage and a splat route for docs pages', () => {
  assert.match(routes, /index\(\s*['"]routes\/home\.tsx['"]\s*\)/);
  assert.match(routes, /route\(\s*['"]\*['"]\s*,\s*['"]routes\/docs\.tsx['"]\s*\)/);
});
