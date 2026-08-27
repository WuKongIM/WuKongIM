import { describe, expect, test } from 'bun:test';
import openapi from '../contracts/javascript-web-quickstart.openapi.json';
import {
  GOLDEN_PATH_VERIFICATION_RECEIPT_SCHEMA,
  buildCompatibilitySnapshot,
  goldenPathHTTPPaths,
  reasonCodes,
  renderCompatibilityMarkdown,
  renderDeveloperContractSupplement,
  renderGoldenPathContractMarkdown,
  renderReasonCodeMarkdown,
  resolveSourceRevision,
} from './developer-contracts';

const sourceRevision = '0123456789abcdef0123456789abcdef01234567';
const sampleLockSha256 = 'a'.repeat(64);
const validVerificationReceipt = JSON.stringify({
  schema: 'wukongim.docs.golden-path-verification/v1',
  result: 'passed',
  source_revision: sourceRevision,
  sample: {
    scenario: 'javascript-web-quickstart/alice-bob-reconnect-sync/v1',
    package_lock_sha256: sampleLockSha256,
  },
  sdk: { package: 'wukongimjssdk', version: '1.3.5' },
  runtime: {
    node: '22.12.0',
    browser: {
      engine: 'chromium',
      playwright_package: '@playwright/test',
      playwright_version: '1.62.1',
      revision: '1234',
      browser_version: '151.0.7922.34',
    },
  },
});

const expectedReasonCodeNames = [
  'ReasonUnknown',
  'ReasonSuccess',
  'ReasonAuthFail',
  'ReasonSubscriberNotExist',
  'ReasonInBlacklist',
  'ReasonChannelNotExist',
  'ReasonUserNotOnNode',
  'ReasonSenderOffline',
  'ReasonMsgKeyError',
  'ReasonPayloadDecodeError',
  'ReasonForwardSendPacketError',
  'ReasonNotAllowSend',
  'ReasonConnectKick',
  'ReasonNotInWhitelist',
  'ReasonQueryTokenError',
  'ReasonSystemError',
  'ReasonChannelIDError',
  'ReasonNodeMatchError',
  'ReasonNodeNotMatch',
  'ReasonBan',
  'ReasonNotSupportHeader',
  'ReasonClientKeyIsEmpty',
  'ReasonRateLimit',
  'ReasonNotSupportChannelType',
  'ReasonDisband',
  'ReasonSendBan',
  'ReasonChannelDeleting',
  'ReasonProtocolUpgradeRequired',
  'ReasonIdempotencyConflict',
  'ReasonMessageSeqExhausted',
];

describe('Phase 12 developer contracts', () => {
  test('builds a reproducible compatibility snapshot from build identity', () => {
    expect(
      buildCompatibilitySnapshot({
        sourceRevision,
        sampleLockSha256,
        verificationReceiptJson: validVerificationReceipt,
      }),
    ).toEqual({
      schema: 'wukongim.docs.compatibility/v1',
      channel: 'v3-beta-snapshot',
      source_revision: sourceRevision,
      verified: true,
      verification: {
        status: 'verified',
        receipt_schema: GOLDEN_PATH_VERIFICATION_RECEIPT_SCHEMA,
      },
      topology: 'single-node cluster',
      hash_slot_count: 256,
      sdk: { package: 'wukongimjssdk', version: '1.3.5' },
      sample: {
        scenario: 'javascript-web-quickstart/alice-bob-reconnect-sync/v1',
        node_requirement: '>=20.11',
        package_lock_sha256: sampleLockSha256,
      },
      runtime: {
        node: '22.12.0',
        package_manager: 'npm',
        browser: {
          engine: 'chromium',
          playwright_package: '@playwright/test',
          playwright_version: '1.62.1',
          revision: '1234',
          browser_version: '151.0.7922.34',
          other_browsers: 'unverified',
        },
      },
      contracts: {
        openapi: '/contracts/javascript-web-quickstart.openapi.json',
        compatibility: '/compatibility.json',
      },
    });
  });

  test('fails closed when the verification receipt tuple drifts', () => {
    const snapshot = buildCompatibilitySnapshot({
      sourceRevision,
      sampleLockSha256: 'b'.repeat(64),
      verificationReceiptJson: validVerificationReceipt,
    });

    expect(snapshot.verified).toBe(false);
    expect(snapshot.verification.status).toBe('mismatch');
  });

  test('binds every receipt identity field to the current build tuple', () => {
    const drifts = [
      ['source revision', sourceRevision, 'f'.repeat(40)],
      ['sample lock', sampleLockSha256, 'b'.repeat(64)],
      [
        'scenario',
        'javascript-web-quickstart/alice-bob-reconnect-sync/v1',
        'javascript-web-quickstart/alice-bob-reconnect-sync/v2',
      ],
      ['SDK package', 'wukongimjssdk', 'other-sdk'],
      ['SDK version', '1.3.5', '1.3.6'],
      ['Node.js', '22.12.0', '22.13.0'],
      ['browser engine', 'chromium', 'firefox'],
      ['Playwright package', '@playwright/test', 'playwright'],
      ['Playwright version', '1.62.1', '1.63.0'],
      ['Chromium revision', '1234', '1235'],
      ['Chromium version', '151.0.7922.34', '151.0.7922.35'],
    ] as const;

    for (const [field, currentValue, driftedValue] of drifts) {
      const receipt = validVerificationReceipt.replace(currentValue, driftedValue);
      const snapshot = buildCompatibilitySnapshot({
        sourceRevision,
        sampleLockSha256,
        verificationReceiptJson: receipt,
      });
      expect({ field, status: snapshot.verification.status }).toEqual({
        field,
        status: 'mismatch',
      });
    }
  });

  test('fails closed when the verification receipt is malformed', () => {
    const snapshot = buildCompatibilitySnapshot({
      sourceRevision,
      sampleLockSha256,
      verificationReceiptJson: '{not-json',
    });

    expect(snapshot.verified).toBe(false);
    expect(snapshot.verification.status).toBe('malformed');
  });

  test('rejects extra receipt fields instead of partially trusting them', () => {
    const receipt = JSON.parse(validVerificationReceipt) as Record<string, unknown>;
    receipt.generated_at = '2026-08-27T00:00:00Z';
    const snapshot = buildCompatibilitySnapshot({
      sourceRevision,
      sampleLockSha256,
      verificationReceiptJson: JSON.stringify(receipt),
    });

    expect(snapshot.verified).toBe(false);
    expect(snapshot.verification.status).toBe('malformed');
  });

  test('defaults to unverified when no verification receipt is supplied', () => {
    const snapshot = buildCompatibilitySnapshot({ sourceRevision, sampleLockSha256 });

    expect(snapshot.verified).toBe(false);
    expect(snapshot.verification.status).toBe('missing');
  });

  test('resolves source identity from explicit and common static-host build variables', () => {
    expect(resolveSourceRevision({ WK_DOCS_SOURCE_REVISION: 'explicit' })).toBe('explicit');
    expect(resolveSourceRevision({ CF_PAGES_COMMIT_SHA: 'cloudflare' })).toBe('cloudflare');
    expect(resolveSourceRevision({ GITHUB_SHA: 'github' })).toBe('github');
  });

  test('keeps the public HTTP fragment intentionally limited to the golden path', () => {
    expect(goldenPathHTTPPaths).toEqual([
      'POST /user/token',
      'GET /route',
      'POST /channel/messagesync',
    ]);
    expect(Object.keys(openapi.paths)).toEqual([
      '/user/token',
      '/route',
      '/channel/messagesync',
    ]);
    expect(openapi.info.title).toContain('Golden Path Subset');
    expect(openapi['x-wukongim-scope']).toBe('non-exhaustive-v3-beta-snapshot');
    expect(
      openapi.components.schemas.ChannelMessageSyncRequest.properties.limit.maximum,
    ).toBe(100);
  });

  test('documents every wire ReasonCode in exact numeric order', async () => {
    expect(reasonCodes.map((reason) => reason.name)).toEqual(expectedReasonCodeNames);
    expect(reasonCodes.map((reason) => reason.value)).toEqual(
      expectedReasonCodeNames.map((_, index) => index),
    );
    expect(reasonCodes.every((reason) => reason.stage.length > 0)).toBe(true);
    expect(reasonCodes.every((reason) => Boolean(reason.retry))).toBe(true);
    expect(reasonCodes.every((reason) => Boolean(reason.reachability))).toBe(true);

    const goSource = await Bun.file(
      new URL('../../pkg/protocol/frame/common.go', import.meta.url),
    ).text();
    const reasonBlock = goSource.match(/type ReasonCode uint8[\s\S]*?const \(([\s\S]*?)\n\)/)?.[1];
    const goNames = [...(reasonBlock ?? '').matchAll(/^\s*(Reason[A-Za-z0-9]+)(?:\s|$)/gm)].map(
      (match) => match[1],
    );

    expect(goNames).toEqual(expectedReasonCodeNames);
    expect(
      reasonCodes.filter((reason) => reason.reachability === 'reserved').map((reason) => reason.name),
    ).toEqual([
      'ReasonSenderOffline',
      'ReasonMsgKeyError',
      'ReasonConnectKick',
      'ReasonQueryTokenError',
      'ReasonChannelIDError',
      'ReasonNotSupportHeader',
      'ReasonNotSupportChannelType',
      'ReasonChannelDeleting',
      'ReasonIdempotencyConflict',
      'ReasonMessageSeqExhausted',
    ]);
    expect(reasonCodes.find((reason) => reason.name === 'ReasonBan')?.stage).toBe(
      'CONNECT / SEND',
    );
    expect(
      reasonCodes.find((reason) => reason.name === 'ReasonPayloadDecodeError')?.summary,
    ).toEqual({
      zh: 'SEND 请求（字段或载荷）格式错误或不受支持，包括解码失败。',
      en: 'Malformed or unsupported SEND request (fields or payload), including decode failures.',
    });
  });

  test('renders shared facts into LLM-friendly Markdown without test internals', () => {
    const snapshot = renderCompatibilityMarkdown('en', {
      sourceRevision,
      sampleLockSha256,
      verificationReceiptJson: validVerificationReceipt,
    });
    expect(snapshot).toContain('wukongimjssdk@1.3.5');
    expect(snapshot).toContain(sourceRevision);
    expect(snapshot).toContain('exact receipt matches this build');
    expect(snapshot).not.toContain('bun test');

    const contract = renderGoldenPathContractMarkdown('zh');
    for (const path of goldenPathHTTPPaths) expect(contract).toContain(path);
    expect(contract).toContain('非完整');

    const reasons = renderReasonCodeMarkdown('en');
    expect(reasons).toContain('| 0 | `ReasonUnknown` |');
    expect(reasons).toContain('| 29 | `ReasonMessageSeqExhausted` |');
    expect(reasons.match(/\n\| \d+ \|/g)).toHaveLength(30);

    const quickstart = renderDeveloperContractSupplement('en', [
      'sdk',
      'javascript',
      'quickstart',
    ]);
    expect(quickstart).toContain('wukongimjssdk@1.3.5');
    expect(quickstart).toContain('POST /channel/messagesync');
    expect(renderDeveloperContractSupplement('en', ['guide', 'index'])).toBe('');
  });

  test('accepts only the attestation file input at the public build boundary', async () => {
    const buildScript = await Bun.file(
      new URL('../scripts/build-site.mjs', import.meta.url),
    ).text();
    const contractSource = await Bun.file(new URL('./developer-contracts.ts', import.meta.url)).text();

    expect(buildScript).toContain('WK_DOCS_GOLDEN_PATH_ATTESTATION_PATH');
    expect(buildScript).toContain('WK_DOCS_GOLDEN_PATH_RECEIPT_JSON cannot both be set');
    expect(`${buildScript}\n${contractSource}`).not.toContain(
      ['WK_DOCS_GOLDEN_PATH', 'VERIFIED'].join('_'),
    );
  });
});
