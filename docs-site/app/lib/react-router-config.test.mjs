import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const configPath = resolve(__dirname, '../../react-router.config.ts');
const config = readFileSync(configPath, 'utf8');

test('react-router config keeps SSR enabled outside production builds', () => {
  assert.match(config, /const\s+isProduction\s*=\s*process\.env\.NODE_ENV\s*===\s*['"]production['"]/);
  assert.match(config, /ssr:\s*!isProduction/);
});
