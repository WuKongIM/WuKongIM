import { describe, expect, test } from 'bun:test';

const deploymentRoot = new URL('../content/docs/server/deployment/', import.meta.url);

async function page(path: string) {
  return Bun.file(new URL(path, deploymentRoot)).text();
}

describe('native deployment publication contract', () => {
  test('documents a pinned Compose deployment with protected runtime boundaries', async () => {
    const pages = await Promise.all([page('docker.mdx'), page('docker.en.mdx')]);

    for (const content of pages) {
      for (const contract of [
        'ghcr.io/wukongim/wukongim:3.0.0-beta.4@sha256:98a4859e057746d2f3071810ad6eebcb073e3d5fb1ccbd6a97a51ce634ed0760',
        'docker compose config --quiet',
        'docker compose pull',
        'v3.0.0-beta.4/wukongim.toml.example',
        'WK_MANAGER_USERS=',
        'wukongim-node1-data',
        './node1.toml:/etc/wukongim/wukongim.toml:ro',
        '/etc/wukongim/wukongim.toml,readonly',
        '127.0.0.1:5001:5001',
        '0.0.0.0:5100:5100',
        '127.0.0.1:5301:5301',
        'healthcheck:',
        'wget',
        'uid=10001,gid=10001,mode=0750',
        'http://127.0.0.1:5001/readyz',
        '/run/wukongim/plugin.sock',
        '/guide/quick-start',
        'docker compose down -v',
        'Alpine 3.19',
      ]) {
        expect(content).toContain(contract);
      }
      expect(content).not.toContain('wukongim:latest');
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
