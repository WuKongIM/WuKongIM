import { describe, expect, test } from 'bun:test';

const deploymentRoot = new URL('../content/docs/server/deployment/', import.meta.url);

async function page(path: string) {
  return Bun.file(new URL(path, deploymentRoot)).text();
}

describe('native deployment publication contract', () => {
  test('keeps the deployment entry focused on the supported decision path', async () => {
    const pages = await Promise.all([page('index.mdx'), page('index.en.mdx')]);

    for (const content of pages) {
      for (const contract of [
        '/guide/quick-start',
        '/server/deployment/docker',
        '/server/deployment/linux',
        '/server/deployment/multi-node',
        '/server/deployment/production-checklist',
        '/readyz',
      ]) {
        expect(content).toContain(contract);
      }
      expect(content).not.toContain('Kubernetes');
      expect(content).not.toContain('Helm');
    }
  });

  test('keeps the Docker deployment path to two direct docker run steps', async () => {
    const pages = await Promise.all([page('docker.mdx'), page('docker.en.mdx')]);

    for (const content of pages) {
      for (const contract of [
        'wukongim.toml',
        'data_dir = "/var/lib/wukongim"',
        'hash_slot_count = 256',
        'slot_replica_n = 1',
        'channel_replica_n = 1',
        'docker volume create wukongim-data',
        'docker run -d',
        '--mount type=bind,src="$PWD/wukongim.toml",dst=/etc/wukongim/wukongim.toml,readonly',
        '--mount type=volume,src=wukongim-data,dst=/var/lib/wukongim',
        'ghcr.io/wukongim/wukongim:3.0.0-beta.6',
        'wukongim-data',
        'http://127.0.0.1:5301',
        'http://127.0.0.1:5001/readyz',
        '/guide/quick-start',
        'docker volume rm wukongim-data',
      ]) {
        expect(content).toContain(contract);
      }
      expect(content.match(/^## \d+\./gm)).toHaveLength(2);
      expect(content).not.toContain('/install/docker.sh');
      expect(content).not.toContain('WK_VERSION');
      expect(content).not.toContain('一键');
      expect(content).not.toContain('one command');
      expect(content).not.toContain('docker compose');
      expect(content).not.toContain('node1.toml');
    }
  });

  test('does not publish the removed Docker installer', async () => {
    expect(
      await Bun.file(new URL('../public/install/docker.sh', import.meta.url)).exists(),
    ).toBe(false);
  });

  test('documents the verified release binary and systemd lifecycle', async () => {
    const pages = await Promise.all([page('linux.mdx'), page('linux.en.mdx')]);

    for (const content of pages) {
      for (const contract of [
        '3.0.0-beta.6',
        'https://packages.githubim.com/keys/apt-preview.asc',
        'https://packages.githubim.com/keys/rpm-preview.asc',
        'D4D5F12AD0FDCAE4D85B577E318ABB2BD40B6BB1',
        'A8FB9F660EC3B4F40B853C4A0FB64C9DD0801459',
        'deb [arch=amd64 signed-by=/etc/apt/keyrings/wukongim-preview.asc] https://packages.githubim.com/apt preview main',
        'sudo apt-get install -y wukongim=3.0.0~beta.6',
        'baseurl=https://packages.githubim.com/rpm/preview/el/9/x86_64',
        "sudo dnf -y --disablerepo='*' --enablerepo=wukongim-preview makecache --refresh",
        'sudo dnf -y --enablerepo=wukongim-preview install wukongim-3.0.0~beta.6-1.x86_64',
        'gpgcheck=1',
        'repo_gpgcheck=1',
        'sslverify=1',
        'skip_if_unavailable=0',
        'set -euo pipefail',
        'primary && $1 == "fpr"',
        'sudo dnf install -y curl-minimal',
        'RHEL',
        'build_source',
        '.goreleaser.packages.yaml',
        'wukongim config init',
        '--admin-password-stdin',
        'wukongim config validate',
        'systemctl enable --now wukongim',
        'RestartPreventExitStatus',
        '/var/lib/wukongim',
        '/var/log/wukongim',
        '/run/wukongim',
      ]) {
        expect(content).toContain(contract);
      }
      for (const unsafe of [
        'apt-key',
        'trusted=yes',
        'gpgcheck=0',
        'repo_gpgcheck=0',
        'curl | sudo',
        'sudo apt-get install -y wukongim\n',
        'sudo dnf install -y wukongim\n',
      ]) {
        expect(content).not.toContain(unsafe);
      }
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
