import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const homepagePath = resolve(__dirname, '../../content/docs/index.mdx');
const homepage = readFileSync(homepagePath, 'utf8');

test('homepage includes the approved primary entry cards', () => {
  assert.match(homepage, /WuKongIM 文档/);
  assert.match(homepage, /认识 WuKongIM/);
  assert.match(homepage, /快速开始/);
  assert.match(homepage, /客户端接入/);
  assert.match(homepage, /服务端集成/);
});

test('homepage includes the approved capability summary', () => {
  assert.match(homepage, /分布式架构/);
  assert.match(homepage, /高并发长连接/);
  assert.match(homepage, /多端消息同步/);
  assert.match(homepage, /可扩展业务集成/);
});

test('homepage includes the approved secondary entries and reading path', () => {
  assert.match(homepage, /部署与运维/);
  assert.match(homepage, /参考与资源/);
  assert.match(homepage, /AI Agent 场景/);
  assert.match(homepage, /推荐阅读路径/);
  assert.match(homepage, /跑通单机/);
});

test('homepage hero avoids paragraph tags inside the custom intro block', () => {
  assert.doesNotMatch(homepage, /<p className="text-sm font-medium tracking-wide text-fd-muted-foreground">/);
  assert.doesNotMatch(homepage, /<p className="mt-3 max-w-3xl text-lg leading-8 text-fd-foreground md:text-xl">/);
  assert.doesNotMatch(homepage, /<p className="mt-3 max-w-3xl text-sm leading-7 text-fd-muted-foreground md:text-base">/);
});
