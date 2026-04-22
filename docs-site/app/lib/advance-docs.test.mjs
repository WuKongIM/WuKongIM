import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const docsRoot = resolve(__dirname, '../../content/docs/server/advance');
const publicRoot = resolve(__dirname, '../../public/images/advance');

const offlineDoc = resolve(docsRoot, 'offlinemsg.mdx');
const integrationDoc = resolve(docsRoot, 'integration.mdx');
const stressDoc = resolve(docsRoot, 'stress.mdx');
const metaDoc = resolve(docsRoot, 'meta.json');

const requiredAssets = [
  'offlinemsg1.png',
  'offlinemsg2.png',
  'integration.png',
  'addTester.png',
  'runTester.png',
  'report.png',
];

test('advance docs pages exist with the expected structure', () => {
  for (const file of [offlineDoc, integrationDoc, stressDoc, metaDoc]) {
    assert.equal(existsSync(file), true, `${file} should exist`);
  }

  const offline = readFileSync(offlineDoc, 'utf8');
  const integration = readFileSync(integrationDoc, 'utf8');
  const stress = readFileSync(stressDoc, 'utf8');
  const meta = readFileSync(metaDoc, 'utf8');

  assert.match(offline, /title:\s*离线消息说明/);
  assert.match(offline, /## 离线消息同步流程/);
  assert.match(offline, /\/images\/advance\/offlinemsg1\.png/);
  assert.match(offline, /\/images\/advance\/offlinemsg2\.png/);

  assert.match(integration, /title:\s*集成到自己的系统/);
  assert.match(integration, /## 第一步：对接业务系统用户/);
  assert.match(integration, /## 第二步：提供 Webhook 接口/);
  assert.match(integration, /## 第三步：配置 Webhook 到 WuKongIM/);
  assert.match(integration, /\/images\/advance\/integration\.png/);

  assert.match(stress, /title:\s*压力测试部署/);
  assert.match(stress, /## 服务器开启压力配置/);
  assert.match(stress, /## 安装压测机/);
  assert.match(stress, /## 开始压测/);
  assert.match(stress, /## 查看压测报告/);
  assert.match(stress, /\/images\/advance\/addTester\.png/);
  assert.match(stress, /\/images\/advance\/runTester\.png/);
  assert.match(stress, /\/images\/advance\/report\.png/);
  assert.match(stress, /v2\.1\.2-20250120/);

  assert.match(meta, /"title":\s*"进阶"/);
  assert.match(meta, /"offlinemsg"/);
  assert.match(meta, /"integration"/);
  assert.match(meta, /"stress"/);
});

test('advance images are available under docs-site public', () => {
  for (const asset of requiredAssets) {
    assert.equal(existsSync(resolve(publicRoot, asset)), true, `${asset} should exist in docs-site/public/images/advance`);
  }
});
