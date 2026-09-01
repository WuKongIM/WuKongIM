import { describe, expect, test } from 'bun:test';

const deploymentRoot = new URL('../content/docs/server/deployment/', import.meta.url);

async function page(path: string) {
  return Bun.file(new URL(path, deploymentRoot)).text();
}

describe('native deployment publication contract', () => {
  test('documents executable Docker build, health, and persistence boundaries', async () => {
    const pages = await Promise.all([page('docker.mdx'), page('docker.en.mdx')]);

    for (const content of pages) {
      for (const contract of [
        'GO_IMAGE',
        'RUNTIME_IMAGE',
        'GOPROXY',
        'docker buildx version',
        'http://127.0.0.1:15001/readyz',
        'http://127.0.0.1:9091/-/ready',
        'http://127.0.0.1:3000/api/health',
        '/var/lib/wukongim/plugin-state',
        '/run/wukongim/plugin.sock',
        'docker compose down -v',
      ]) {
        expect(content).toContain(contract);
      }
    }
  });

  test('documents the secure native package and systemd lifecycle', async () => {
    const pages = await Promise.all([page('linux.mdx'), page('linux.en.mdx')]);

    for (const content of pages) {
      for (const contract of [
        '.goreleaser.packages.yaml',
        'wukongim config init',
        '--admin-password-stdin',
        'wukongim config validate',
        'RestartPreventExitStatus',
        '/var/lib/wukongim',
        '/var/log/wukongim',
        '/run/wukongim',
        'packages.githubim.com',
      ]) {
        expect(content).toContain(contract);
      }
      expect(content).not.toContain('deb [signed-by=');
      expect(content).not.toContain('baseurl=https://packages.githubim.com');
    }
  });

  test('states the three-node three-replica readiness boundary', async () => {
    const pages = await Promise.all([page('multi-node.mdx'), page('multi-node.en.mdx')]);

    for (const content of pages) {
      for (const contract of [
        'slot_replica_n = 3',
        'channel_replica_n = 3',
        '/readyz',
        '`503`',
      ]) {
        expect(content).toContain(contract);
      }
    }
  });
});
