import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const publicDir = resolve(__dirname, '../../public');

const requiredAssets = [
  'images/stress-report/send_rate_bench.png',
  'images/stress-report/send_rate_bench_btop.png',
  'images/stress-report/收消息性能压测.png',
  'images/stress-report/收消息btop.png',
  'images/stress-report/混合测试.png',
  'images/stress-report/混合测试btop.png',
  'images/stress-report/稳定性测试.png',
  'images/stress-report/稳定性测试btop.png',
  'images/stress-report/可靠性测试结果.png',
  'images/stress-report/可靠性测试结果2.png',
  'videos/stress-report/send.mp4',
  'videos/stress-report/收消息性能压测.mp4',
  'videos/stress-report/混合测试.mp4',
  'videos/stress-report/稳定性测试2.mp4',
  'videos/stress-report/顺序性测试.mp4',
];

test('stress report assets are available under docs-site public', () => {
  for (const asset of requiredAssets) {
    assert.equal(existsSync(resolve(publicDir, asset)), true, `${asset} should exist in docs-site/public`);
  }
});
