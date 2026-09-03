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

  test('keeps the Docker deployment path to two direct steps with run or Compose', async () => {
    const pages = await Promise.all([page('docker.mdx'), page('docker.en.mdx')]);

    for (const content of pages) {
      for (const contract of [
        'wukongim.toml',
        'data_dir = "/var/lib/wukongim"',
        'listen_addr = "127.0.0.1:7001"',
        'listen_addr = "0.0.0.0:5001"',
        'listen_addr = "0.0.0.0:5301"',
        'auth_on = true',
        'jwt_secret = "replace-with-a-random-64-character-secret"',
        'dir = "/var/lib/wukongim/logs"',
        'docker run -d --name wukongim --restart unless-stopped',
        '-p 127.0.0.1:5001:5001 -p 5100:5100 -p 5200:5200 -p 127.0.0.1:5301:5301',
        '-v "$PWD/wukongim.toml:/etc/wukongim/wukongim.toml:ro"',
        '-v wukongim-data:/var/lib/wukongim',
        'compose.yaml',
        'docker compose up -d',
        'name: wukongim-data',
        'docker compose down --volumes',
        'ghcr.io/wukongim/wukongim:3.0.0-beta.6',
        'wukongim-data',
        'http://127.0.0.1:5301',
        'http://127.0.0.1:5001/readyz',
        '/guide/quick-start',
        '/server/configuration/reference',
        'docker volume rm wukongim-data',
      ]) {
        expect(content).toContain(contract);
      }
      expect(content.match(/^## \d+\./gm)).toHaveLength(2);
      expect(content).not.toContain('/install/docker.sh');
      expect(content).not.toContain('WK_VERSION');
      expect(content).not.toContain('一键');
      expect(content).not.toContain('one command');
      expect(content).not.toContain('docker volume create');
      expect(content).not.toContain('--mount');
      expect(content).not.toContain('node1.toml');
      for (const optional of [
        'cluster.id',
        'nodes =',
        'join_token =',
        'initial_slot_count =',
        'hash_slot_count =',
        'slot_replica_n =',
        'channel_replica_n =',
        'jwt_issuer =',
        'jwt_expire =',
        'external_tcp_addr =',
        'external_ws_addr =',
        'level =',
        'console =',
        '[diagnostics]',
        '[gateway]',
        '[plugin]',
      ]) {
        expect(content).not.toContain(optional);
      }
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
        'https://packages.githubim.com/bootstrap/wukongim-archive-keyring_1.0.0_all.deb',
        'https://packages.githubim.com/bootstrap/wukongim-release-1.0.0-1.noarch.rpm',
        'D4D5F12AD0FDCAE4D85B577E318ABB2BD40B6BB1',
        'A8FB9F660EC3B4F40B853C4A0FB64C9DD0801459',
        'wukongim-archive-keyring',
        'wukongim-release',
        'Deb822',
        '%config(noreplace)',
        'sudo apt update',
        'sudo apt install -y wukongim\n',
        "sudo dnf -y --disablerepo='*' --enablerepo=wukongim-preview makecache --refresh",
        'sudo dnf install -y wukongim\n',
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
        'apt-key add',
        'trusted=yes',
        'Trusted: yes',
        'gpgcheck=0',
        'repo_gpgcheck=0',
        'curl | sudo',
        'sudo apt install -y wukongim=',
        'sudo apt-get install -y wukongim=',
        'sudo dnf install -y wukongim-',
        'sudo dnf -y --enablerepo=wukongim-preview install wukongim-',
      ]) {
        expect(content).not.toContain(unsafe);
      }

      const aptBootstrap = content.indexOf('sudo apt install -y /tmp/wukongim-archive-keyring_1.0.0_all.deb');
      const aptUpdate = content.indexOf('sudo apt update', aptBootstrap);
      const aptInstall = content.indexOf('sudo apt install -y wukongim', aptUpdate);
      expect(aptBootstrap).toBeGreaterThan(-1);
      expect(aptUpdate).toBeGreaterThan(aptBootstrap);
      expect(aptInstall).toBeGreaterThan(aptUpdate);

      const rpmBootstrap = content.indexOf('sudo dnf install -y /tmp/wukongim-release-1.0.0-1.noarch.rpm');
      const dnfUpdate = content.indexOf(
        "sudo dnf -y --disablerepo='*' --enablerepo=wukongim-preview makecache --refresh",
        rpmBootstrap,
      );
      const dnfInstall = content.indexOf('sudo dnf install -y wukongim', dnfUpdate);
      expect(rpmBootstrap).toBeGreaterThan(-1);
      expect(dnfUpdate).toBeGreaterThan(rpmBootstrap);
      expect(dnfInstall).toBeGreaterThan(dnfUpdate);
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
