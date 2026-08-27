import { describe, expect, test } from 'bun:test';
import {
  javascriptWebCapabilities,
  renderJavaScriptCapabilityMarkdown,
} from './developer-contracts';
import {
  ACCEPTANCE_CHECK_IDS,
  DOCUMENTATION_QUALITY_CHECK_IDS,
  PRODUCTION_GATE_IDS,
} from '../examples/javascript-web-quickstart/src/acceptance/report';

const contentRoot = new URL('../content/docs/', import.meta.url);
const sampleRoot = new URL('../examples/javascript-web-quickstart/', import.meta.url);

async function text(root: URL, path: string) {
  return Bun.file(new URL(path, root)).text();
}

describe('Phase 14 integration acceptance contracts', () => {
  test('publishes only evidence-backed JavaScript capability statuses', () => {
    expect(javascriptWebCapabilities.map(({ id, status }) => [id, status])).toEqual([
      ['route-connect', 'scenario-covered'],
      ['persistent-person-messaging', 'scenario-covered'],
      ['sendack-realtime-separation', 'scenario-covered'],
      ['reconnect-offline-sync', 'scenario-covered'],
      ['realtime-sync-deduplication', 'scenario-covered'],
      ['production-connection-authentication', 'boundary'],
      ['browser-product-http-access', 'boundary'],
      ['non-chromium-browsers', 'unverified'],
      ['groups-and-specialized-channels', 'unverified'],
      ['custom-messages-and-conversations', 'unverified'],
      ['push-and-multi-device', 'unverified'],
      ['transient-and-background-behavior', 'unverified'],
    ]);
  });

  test('renders one capability catalog into both locales for machine readers', () => {
    const zh = renderJavaScriptCapabilityMarkdown('zh');
    const en = renderJavaScriptCapabilityMarkdown('en');

    for (const fact of [
      '`route-connect`',
      '`production-connection-authentication`',
      '`transient-and-background-behavior`',
    ]) {
      expect(zh).toContain(fact);
      expect(en).toContain(fact);
    }
    expect(zh).toContain('场景覆盖');
    expect(zh).toContain('边界');
    expect(zh).toContain('未验证');
    expect(en).toContain('Scenario-covered');
    expect(en).toContain('Boundary');
    expect(en).toContain('Unverified');
  });

  test('keeps compatibility smoke separate from production acceptance', async () => {
    const [zhAcceptance, enAcceptance, zhCapabilities, enCapabilities] = await Promise.all([
      text(contentRoot, 'guide/integration/acceptance.mdx'),
      text(contentRoot, 'guide/integration/acceptance.en.mdx'),
      text(contentRoot, 'sdk/javascript/platform-capabilities.mdx'),
      text(contentRoot, 'sdk/javascript/platform-capabilities.en.mdx'),
    ]);

    for (const page of [zhAcceptance, enAcceptance]) {
      expect(page).toContain('npm run verify:acceptance');
      expect(page).toContain('wukongim.docs.integration-acceptance/v1');
      expect(page).toContain('not_assessed');
      expect(page).toContain('/readyz');
      expect(page).toContain('cluster.source_identity');
      expect(page).toContain('documentation_quality.result');
    }
    expect(zhAcceptance).toContain('默认 v3 Beta');
    expect(enAcceptance).toContain('default v3 Beta');
    expect(zhAcceptance).toContain('事故诊断');
    expect(zhAcceptance).toContain('版本固定');
    expect(enAcceptance).toContain('incident diagnostics');
    expect(enAcceptance).toContain('version pinning');
    expect(zhCapabilities).toContain('<JavaScriptCapabilityMatrix locale="zh" />');
    expect(enCapabilities).toContain('<JavaScriptCapabilityMatrix locale="en" />');
    expect(zhCapabilities).toContain('/zh/guide/integration/acceptance');
    expect(enCapabilities).toContain('/en/guide/integration/acceptance');
  });

  test('keeps the executable command and E2E page coverage wired', async () => {
    const manifest = JSON.parse(await text(sampleRoot, 'package.json')) as {
      scripts: Record<string, string>;
    };
    const e2e = await text(sampleRoot, 'e2e/quickstart.spec.ts');
    const verifier = await text(sampleRoot, 'scripts/verify-acceptance.ts');

    expect(manifest.scripts['verify:acceptance']).toBe('tsx scripts/verify-acceptance.ts');
    expect(verifier).toContain('runIntegrationAcceptanceVerification');
    expect(e2e).toContain('guide/integration/acceptance/');
    expect(e2e).toContain('sdk/javascript/platform-capabilities/');
  });

  test('documents isolated participants and bounded person-directory convergence', async () => {
    const [
      e2e,
      productClient,
      verifier,
      sampleReadme,
      zhQuickstart,
      enQuickstart,
      zhAcceptance,
      enAcceptance,
    ] = await Promise.all([
      text(sampleRoot, 'e2e/quickstart.spec.ts'),
      text(sampleRoot, 'src/server/product-http-client.ts'),
      text(sampleRoot, 'scripts/verify-acceptance.ts'),
      text(sampleRoot, 'README.md'),
      text(contentRoot, 'sdk/javascript/quickstart.mdx'),
      text(contentRoot, 'sdk/javascript/quickstart.en.mdx'),
      text(contentRoot, 'guide/integration/acceptance.mdx'),
      text(contentRoot, 'guide/integration/acceptance.en.mdx'),
    ]);

    expect(e2e).toContain('acceptanceParticipantUids');
    expect(productClient).toContain('maxAttempts: 20');
    expect(productClient).toContain('delayMs: 250');
    expect(productClient).toContain('valid channel membership required');
    expect(verifier).toContain('JAVASCRIPT_WEB_QUICKSTART_TARGET.sdk.package');
    expect(sampleReadme).toContain('isolated development UIDs');
    expect(zhQuickstart).toContain('异步建立');
    expect(enQuickstart).toContain('asynchronously');
    expect(zhAcceptance).toContain('精确成员关系未就绪');
    expect(enAcceptance).toContain('exact membership-not-ready');
  });

  test('binds report checks and scenario-covered capability claims to the real scenario', async () => {
    expect(ACCEPTANCE_CHECK_IDS).toEqual([
      'sample-contracts',
      'route-connect',
      'bidirectional-persistent-send',
      'sendack-realtime-separation',
      'offline-realtime-absence',
      'reconnect-sync-recovery',
      'realtime-sync-deduplication',
      'sample-accessibility-baseline',
    ]);
    expect(DOCUMENTATION_QUALITY_CHECK_IDS).toEqual([
      'bilingual-documentation-accessibility',
    ]);
    expect(PRODUCTION_GATE_IDS).toContain('gateway-stored-token-verification');

    const [e2e, playwright] = await Promise.all([
      text(sampleRoot, 'e2e/quickstart.spec.ts'),
      text(sampleRoot, 'playwright.config.ts'),
    ]);
    for (const executableFact of [
      'Alice and Bob exchange persistent messages and recover one after reconnect',
      'event-sendack',
      'event-received',
      'disconnect-button',
      'reconnect-sync-button',
      'event-synced',
      'recovered',
    ]) {
      expect(e2e).toContain(executableFact);
    }
    expect(playwright).toContain('browserName: "chromium"');
    expect(playwright).not.toContain('browserName: "firefox"');
    expect(playwright).not.toContain('browserName: "webkit"');
  });
});
