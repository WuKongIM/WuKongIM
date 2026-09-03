import { describe, expect, test } from 'bun:test';

const deploymentRoot = new URL('../content/docs/server/deployment/', import.meta.url);
const quickStartRoot = new URL('../content/docs/guide/quick-start/', import.meta.url);

async function page(path: string) {
  return Bun.file(new URL(path, deploymentRoot)).text();
}

async function quickStartPage(path: string) {
  return Bun.file(new URL(path, quickStartRoot)).text();
}

describe('native deployment publication contract', () => {
  test('keeps the single-node quick start on the Linux package and systemd path', async () => {
    const pages = await Promise.all([
      quickStartPage('single-node-cluster.mdx'),
      quickStartPage('single-node-cluster.en.mdx'),
    ]);

    for (const content of pages) {
      for (const contract of [
        'curl -fsSL https://packages.githubim.com/repo | sudo sh',
        'sudo apt install -y wukongim',
        'sudo dnf install -y wukongim',
        'wukongim config init',
        'wukongim config validate',
        'systemctl enable --now wukongim',
        'http://127.0.0.1:5001/readyz',
        'journalctl -u wukongim',
        'ssh -L 5001:127.0.0.1:5001',
        '/var/lib/wukongim',
      ]) {
        expect(content).toContain(contract);
      }

      for (const sourcePath of [
        'git clone',
        'go1.25.11',
        'GOWORK=off go run',
        'wukongim.toml.example',
        './data/wukongim-single-node-data',
      ]) {
        expect(content).not.toContain(sourcePath);
      }
    }
  });

  test('keeps the deployment entry focused on the supported decision path', async () => {
    const pages = await Promise.all([page('index.mdx'), page('index.en.mdx')]);

    for (const content of pages) {
      for (const contract of [
        '/guide/quick-start',
        '/server/deployment/docker',
        '/server/deployment/linux',
        '/server/deployment/multi-node',
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
        'ghcr.io/wukongim/wukongim:3.0.0-beta.7',
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
      expect(content).not.toContain('3.0.0-beta.6');
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

  test('keeps Linux deployment to a concise package, configuration, and systemd path', async () => {
    const pages = await Promise.all([page('linux.mdx'), page('linux.en.mdx')]);

    for (const content of pages) {
      for (const contract of [
        '3.0.0-beta.7',
        'curl -fsSL https://packages.githubim.com/repo | sudo sh',
        'sudo apt update',
        'sudo apt install -y wukongim\n',
        "sudo dnf -y --disablerepo='*' --enablerepo=wukongim-preview makecache --refresh",
        'sudo dnf install -y wukongim\n',
        'RHEL',
        'wukongim config init',
        'wukongim config validate',
        'systemctl enable --now wukongim',
        'http://127.0.0.1:5001/readyz',
        'http://127.0.0.1:5301',
        '0.0.0.0:5301',
        'auth_on = true',
        '/var/lib/wukongim',
        '/var/log/wukongim',
        '/server/deployment/multi-node',
        '/server/operations/upgrade-and-migration',
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
        'packages.githubim.com/bootstrap/wukongim-archive-keyring_',
        'packages.githubim.com/bootstrap/wukongim-release-',
        '/tmp/wukongim-archive-keyring_',
        '/tmp/wukongim-release-',
      ]) {
        expect(content).not.toContain(unsafe);
      }
      expect(content).not.toContain('3.0.0-beta.6');
      expect(content).toContain(
        'sudo wukongim init \\\n  --admin-password-stdin < /secure/path/manager-password',
      );

      expect(
        content.match(/^curl -fsSL https:\/\/packages\.githubim\.com\/repo \| sudo sh$/gm),
      ).toHaveLength(2);
      expect(content.match(/^## \d+\./gm)).toHaveLength(3);
      expect(content.trimEnd().split('\n').length).toBeLessThanOrEqual(70);
      expect(content).not.toContain('<details>');
      expect(content).not.toContain('.goreleaser.packages.yaml');

      const aptBootstrap = content.indexOf('curl -fsSL https://packages.githubim.com/repo | sudo sh');
      const aptUpdate = content.indexOf('sudo apt update', aptBootstrap);
      const aptInstall = content.indexOf('sudo apt install -y wukongim', aptUpdate);
      expect(aptBootstrap).toBeGreaterThan(-1);
      expect(aptUpdate).toBeGreaterThan(aptBootstrap);
      expect(aptInstall).toBeGreaterThan(aptUpdate);

      const rpmBootstrap = content.indexOf(
        'curl -fsSL https://packages.githubim.com/repo | sudo sh',
        aptInstall,
      );
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

  test('states the three-node readiness and Manager boundaries', async () => {
    const pages = await Promise.all([page('multi-node.mdx'), page('multi-node.en.mdx')]);

    for (const content of pages) {
      for (const contract of [
        'slot_replica_n = 3',
        'channel_replica_n = 3',
        '/readyz',
        '`503`',
        'http://manager.example.com:5301',
        'listen_addr = "0.0.0.0:5301"',
        'auth_on = true',
        'replace-with-the-same-random-64-character-secret',
        'replace-with-the-same-strong-password',
        'wukongim_manager',
      ]) {
        expect(content).toContain(contract);
      }
      expect(content.match(/server 10\.0\.0\.(?:11|12|13):5301/g)).toHaveLength(3);
    }
  });
});
