import { describe, expect, test } from 'bun:test';

const deploymentRoot = new URL('../content/docs/server/deployment/', import.meta.url);

async function page(path: string) {
  return Bun.file(new URL(path, deploymentRoot)).text();
}

describe('Kubernetes deployment publication contract', () => {
  test('publishes a concise bilingual deployment path', async () => {
    const pages = await Promise.all([page('kubernetes.mdx'), page('kubernetes.en.mdx')]);

    for (const content of pages) {
      for (const contract of [
        'StatefulSet',
        'Headless Service',
        'enableServiceLinks: false',
        'WK_NODE_ID',
        'WK_CLUSTER_NODES',
        'hash_slot_count = 256',
        '/readyz',
        '/healthz',
        'wukongim@sha256:',
        'kubernetes-resources',
      ]) {
        expect(content).toContain(contract);
      }
      expect(content).toContain('https://kubernetes.io/docs/concepts/workloads/controllers/statefulset/');
      expect(content).toContain('https://kubernetes.io/docs/concepts/workloads/pods/probes/');
      expect(content).not.toContain('wukongim.github.io/helm');
      expect(content).not.toMatch(/image(?:\.tag)?[^\n]*\blatest\b/iu);
      expect(content).not.toContain('Kubernetes 1.19');
    }
  });

  test('publishes the complete resource contract in a separate bilingual reference', async () => {
    const pages = await Promise.all([
      page('kubernetes-resources.mdx'),
      page('kubernetes-resources.en.mdx'),
    ]);

    for (const content of pages) {
      for (const contract of [
        'StatefulSet',
        'Headless Service',
        'enableServiceLinks: false',
        'WK_NODE_ID',
        'WK_CLUSTER_NODES',
        'hash_slot_count = 256',
        'slot_replica_n = 3',
        '/readyz',
        '/healthz',
        'wukongim@sha256:',
        'volumeClaimTemplates',
        'PodDisruptionBudget',
        'podAntiAffinity',
        'topologySpreadConstraints',
      ]) {
        expect(content).toContain(contract);
      }
      expect(content).toContain('https://kubernetes.io/docs/concepts/workloads/controllers/statefulset/');
      expect(content).toContain('https://kubernetes.io/docs/concepts/workloads/pods/probes/');
      expect(content).not.toContain('wukongim.github.io/helm');
      expect(content).not.toMatch(/image(?:\.tag)?[^\n]*\blatest\b/iu);
      expect(content).not.toContain('Kubernetes 1.19');
    }
  });
});
