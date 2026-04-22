import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const docsRoot = resolve(__dirname, '../../content/docs/server/plugin');
const publicRoot = resolve(__dirname, '../../public/images/plugin');

const indexDoc = resolve(docsRoot, 'index.mdx');
const marketDoc = resolve(docsRoot, 'market.mdx');
const useDoc = resolve(docsRoot, 'use.mdx');
const devDoc = resolve(docsRoot, 'dev.mdx');
const metaDoc = resolve(docsRoot, 'meta.json');

const requiredAssets = [
  'ai-demo.png',
  'bind.png',
  'plugin-config.png',
  'plugin-config2.png',
  'plugin-connect.png',
  'plugin-dir.png',
  'uinstall.png',
];

test('plugin docs pages exist with the expected structure', () => {
  for (const file of [indexDoc, marketDoc, useDoc, devDoc, metaDoc]) {
    assert.equal(existsSync(file), true, `${file} should exist`);
  }

  const index = readFileSync(indexDoc, 'utf8');
  const market = readFileSync(marketDoc, 'utf8');
  const use = readFileSync(useDoc, 'utf8');
  const dev = readFileSync(devDoc, 'utf8');
  const meta = readFileSync(metaDoc, 'utf8');

  assert.match(index, /title:\s*说明/);
  assert.match(index, /## 什么是插件/);
  assert.match(index, /## 用户插件/);
  assert.match(index, /## 全局插件/);

  assert.match(market, /title:\s*市场/);
  assert.match(market, /Github/);
  assert.match(market, /Gitee/);
  assert.match(market, /\[插件名字\]-\[系统\]-\[CPU架构\]\.wkp/);

  assert.match(use, /title:\s*使用/);
  assert.match(use, /## 安装插件/);
  assert.match(use, /## 调用插件/);
  assert.match(use, /## 卸载插件/);
  assert.match(use, /\/images\/plugin\/plugin-dir\.png/);
  assert.match(use, /\/images\/plugin\/ai-demo\.png/);

  assert.match(dev, /title:\s*开发/);
  assert.match(dev, /Go PDK/);
  assert.match(dev, /## 开发插件/);
  assert.match(dev, /## 调试插件/);
  assert.match(dev, /## 函数说明/);
  assert.match(dev, /\/images\/plugin\/plugin-connect\.png/);

  assert.match(meta, /"title":\s*"插件"/);
  assert.match(meta, /"index"/);
  assert.match(meta, /"market"/);
  assert.match(meta, /"use"/);
  assert.match(meta, /"dev"/);
});

test('plugin images are available under docs-site public', () => {
  for (const asset of requiredAssets) {
    assert.equal(existsSync(resolve(publicRoot, asset)), true, `${asset} should exist in docs-site/public/images/plugin`);
  }
});
