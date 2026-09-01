import { describe, expect, test } from 'bun:test';

const deploymentRoot = new URL('../content/docs/server/deployment/', import.meta.url);

async function page(path: string) {
  return Bun.file(new URL(path, deploymentRoot)).text();
}

describe('native deployment publication contract', () => {
  test('keeps the Docker deployment path to two steps with one-command install', async () => {
    const pages = await Promise.all([page('docker.mdx'), page('docker.en.mdx')]);

    for (const content of pages) {
      for (const contract of [
        'curl -fsSL https://docs.githubim.com/install/docker.sh | sh',
        'WK_PUBLIC_HOST=im.example.com',
        'wukongim-docker/',
        'wukongim-data',
        'http://127.0.0.1:5301',
        'http://127.0.0.1:5001/readyz',
        '/guide/quick-start',
        'docker volume rm wukongim-data',
      ]) {
        expect(content).toContain(contract);
      }
      expect(content.match(/^## \d+\./gm)).toHaveLength(2);
      expect(content).not.toContain('docker compose');
      expect(content).not.toContain('node1.toml');
    }
  });

  test('publishes a pinned idempotent installer with generated credentials', async () => {
    const installer = await Bun.file(
      new URL('../public/install/docker.sh', import.meta.url),
    ).text();

    for (const contract of [
      'ghcr.io/wukongim/wukongim:3.0.0-beta.6@sha256:d00b93c2d2e77bae83597eaea12191a1be88cfd458de5351e00c31ed49672786',
      'random_hex 32',
      'umask 077',
      'set -C',
      'WK_CLUSTER_NODES=',
      'WK_MANAGER_USERS=',
      'WK_PLUGIN_SOCKET_PATH=/run/wukongim/plugin.sock',
      '--env-file "$env_file"',
      '--mount "type=volume,src=$volume,dst=/var/lib/wukongim"',
      '--publish 127.0.0.1:5001:5001',
      '--publish 0.0.0.0:5100:5100',
      '--publish 127.0.0.1:5301:5301',
      '--entrypoint /usr/local/bin/wukongim',
      "installer_label='docs-one-click-v1'",
      'the container did not become healthy',
    ]) {
      expect(installer).toContain(contract);
    }
    expect(installer).not.toContain('wukongim:latest');
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
