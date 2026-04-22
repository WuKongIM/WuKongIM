import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const stressReportPath = resolve(__dirname, '../../content/docs/resources/stress-reports.mdx');
const stressReport = readFileSync(stressReportPath, 'utf8');

test('stress report page matches the original single-node report structure', () => {
  assert.match(stressReport, /title:\s*"压力测试报告（单机）"/);
  assert.match(stressReport, /## 前序/);
  assert.match(stressReport, /## 测试环境/);
  assert.match(stressReport, /## 性能测试/);
  assert.match(stressReport, /### 发送速率测试/);
  assert.match(stressReport, /### 接收速率测试/);
  assert.match(stressReport, /## 稳定性测试/);
  assert.match(stressReport, /## 可靠性测试/);
  assert.match(stressReport, /## 有序性测试/);
  assert.match(stressReport, /WuKongIM测试版本/);
  assert.match(stressReport, /v2\.1\.2-20250120/);
  assert.match(stressReport, /20万条\/秒/);
});
