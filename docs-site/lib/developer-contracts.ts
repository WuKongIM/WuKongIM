export const goldenPathHTTPPaths = [
  'POST /user/token',
  'GET /route',
  'POST /channel/messagesync',
] as const;

export const GOLDEN_PATH_VERIFICATION_RECEIPT_SCHEMA =
  'wukongim.docs.golden-path-verification/v1' as const;

const goldenPathScenario = 'javascript-web-quickstart/alice-bob-reconnect-sync/v1';
const sdkIdentity = { package: 'wukongimjssdk', version: '1.3.5' } as const;
const runtimeIdentity = {
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
} as const;

export type GoldenPathVerificationStatus =
  | 'verified'
  | 'missing'
  | 'malformed'
  | 'mismatch';

export interface CompatibilitySnapshotOptions {
  sourceRevision: string;
  sampleLockSha256?: string;
  verificationReceiptJson?: string;
}

function isExactRecord(value: unknown, keys: readonly string[]): value is Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return false;
  const actualKeys = Object.keys(value);
  return actualKeys.length === keys.length && keys.every((key) => actualKeys.includes(key));
}

function parseVerificationReceipt(receiptJson: string): Record<string, unknown> | undefined {
  if (receiptJson.length > 16 * 1024) return undefined;

  let receipt: unknown;
  try {
    receipt = JSON.parse(receiptJson);
  } catch {
    return undefined;
  }

  if (!isExactRecord(receipt, ['schema', 'result', 'source_revision', 'sample', 'sdk', 'runtime'])) {
    return undefined;
  }
  if (
    receipt.schema !== GOLDEN_PATH_VERIFICATION_RECEIPT_SCHEMA ||
    receipt.result !== 'passed' ||
    typeof receipt.source_revision !== 'string' ||
    !/^[a-f0-9]{40}([a-f0-9]{24})?$/.test(receipt.source_revision)
  ) {
    return undefined;
  }

  const sample = receipt.sample;
  const sdk = receipt.sdk;
  const runtime = receipt.runtime;
  if (
    !isExactRecord(sample, ['scenario', 'package_lock_sha256']) ||
    typeof sample.scenario !== 'string' ||
    typeof sample.package_lock_sha256 !== 'string' ||
    !/^[a-f0-9]{64}$/.test(sample.package_lock_sha256) ||
    !isExactRecord(sdk, ['package', 'version']) ||
    typeof sdk.package !== 'string' ||
    typeof sdk.version !== 'string' ||
    !isExactRecord(runtime, ['node', 'browser']) ||
    typeof runtime.node !== 'string'
  ) {
    return undefined;
  }

  const browser = runtime.browser;
  if (
    !isExactRecord(browser, [
      'engine',
      'playwright_package',
      'playwright_version',
      'revision',
      'browser_version',
    ]) ||
    typeof browser.engine !== 'string' ||
    typeof browser.playwright_package !== 'string' ||
    typeof browser.playwright_version !== 'string' ||
    typeof browser.revision !== 'string' ||
    typeof browser.browser_version !== 'string'
  ) {
    return undefined;
  }

  return receipt;
}

function resolveVerificationStatus({
  sourceRevision,
  sampleLockSha256,
  verificationReceiptJson,
}: CompatibilitySnapshotOptions): GoldenPathVerificationStatus {
  if (verificationReceiptJson === undefined || verificationReceiptJson.trim() === '') {
    return 'missing';
  }

  const receipt = parseVerificationReceipt(verificationReceiptJson);
  if (!receipt) return 'malformed';

  const sample = receipt.sample as Record<string, unknown>;
  const sdk = receipt.sdk as Record<string, unknown>;
  const runtime = receipt.runtime as Record<string, unknown>;
  const browser = runtime.browser as Record<string, unknown>;
  const immutableSourceRevision = /^[a-f0-9]{40}([a-f0-9]{24})?$/.test(sourceRevision);
  const immutableSampleLock = /^[a-f0-9]{64}$/.test(sampleLockSha256 ?? '');
  if (
    !immutableSourceRevision ||
    !immutableSampleLock ||
    receipt.source_revision !== sourceRevision ||
    sample.scenario !== goldenPathScenario ||
    sample.package_lock_sha256 !== sampleLockSha256 ||
    sdk.package !== sdkIdentity.package ||
    sdk.version !== sdkIdentity.version ||
    runtime.node !== runtimeIdentity.node ||
    browser.engine !== runtimeIdentity.browser.engine ||
    browser.playwright_package !== runtimeIdentity.browser.playwright_package ||
    browser.playwright_version !== runtimeIdentity.browser.playwright_version ||
    browser.revision !== runtimeIdentity.browser.revision ||
    browser.browser_version !== runtimeIdentity.browser.browser_version
  ) {
    return 'mismatch';
  }

  return 'verified';
}

/** Builds the public identity of one reproducible documentation snapshot. */
export function buildCompatibilitySnapshot({
  sourceRevision,
  sampleLockSha256 = 'unavailable',
  verificationReceiptJson,
}: CompatibilitySnapshotOptions) {
  const verificationStatus = resolveVerificationStatus({
    sourceRevision,
    sampleLockSha256,
    verificationReceiptJson,
  });

  return {
    schema: 'wukongim.docs.compatibility/v1',
    channel: 'v3-beta-snapshot',
    source_revision: sourceRevision,
    verified: verificationStatus === 'verified',
    verification: {
      status: verificationStatus,
      receipt_schema: GOLDEN_PATH_VERIFICATION_RECEIPT_SCHEMA,
    },
    topology: 'single-node cluster',
    hash_slot_count: 256,
    sdk: sdkIdentity,
    sample: {
      scenario: goldenPathScenario,
      node_requirement: '>=20.11',
      package_lock_sha256: sampleLockSha256,
    },
    runtime: runtimeIdentity,
    contracts: {
      openapi: '/contracts/javascript-web-quickstart.openapi.json',
      compatibility: '/compatibility.json',
    },
  } as const;
}

type SourceRevisionEnvironment = Partial<
  Record<
    | 'WK_DOCS_SOURCE_REVISION'
    | 'VERCEL_GIT_COMMIT_SHA'
    | 'GITHUB_SHA'
    | 'CF_PAGES_COMMIT_SHA',
    string | undefined
  >
>;

export function resolveSourceRevision(
  environment: SourceRevisionEnvironment = process.env as SourceRevisionEnvironment,
): string {
  return (
    environment.WK_DOCS_SOURCE_REVISION ??
    environment.VERCEL_GIT_COMMIT_SHA ??
    environment.GITHUB_SHA ??
    environment.CF_PAGES_COMMIT_SHA ??
    'working-tree'
  );
}

export const compatibilitySnapshot = buildCompatibilitySnapshot({
  sourceRevision: resolveSourceRevision(),
  sampleLockSha256: process.env.WK_DOCS_SAMPLE_LOCK_SHA256,
  verificationReceiptJson: process.env.WK_DOCS_GOLDEN_PATH_RECEIPT_JSON,
});

export type ReasonRetryGuidance =
  | 'not-applicable'
  | 'do-not-retry'
  | 'refresh-route'
  | 'retry-with-backoff'
  | 'application-policy'
  | 'upgrade-client';

export type ReasonReachability = 'active' | 'compatibility' | 'reserved';

export interface ReasonCodeDefinition {
  value: number;
  name: string;
  stage: string;
  retry: ReasonRetryGuidance;
  reachability: ReasonReachability;
  summary: { zh: string; en: string };
}

function reason(
  value: number,
  name: string,
  stage: string,
  retry: ReasonRetryGuidance,
  reachability: ReasonReachability,
  zh: string,
  en: string,
): ReasonCodeDefinition {
  return { value, name, stage, retry, reachability, summary: { zh, en } };
}

/** Wire-level ReasonCode catalog calibrated against pkg/protocol/frame/common.go. */
export const reasonCodes: ReasonCodeDefinition[] = [
  reason(0, 'ReasonUnknown', 'CONNECT / SEND', 'application-policy', 'compatibility', '未分类或未知结果；先保留上下文并停止盲目重试。', 'Unclassified or unknown result; retain context and avoid blind retries.'),
  reason(1, 'ReasonSuccess', 'CONNECT / SEND', 'not-applicable', 'active', '请求成功；具体完成边界取决于数据包类型。', 'The request succeeded; the completion boundary depends on the packet type.'),
  reason(2, 'ReasonAuthFail', 'CONNECT / SEND', 'do-not-retry', 'active', '认证或发送身份被拒绝；先修复凭据或服务端策略。', 'Authentication or sender identity was rejected; repair credentials or server policy first.'),
  reason(3, 'ReasonSubscriberNotExist', 'SEND', 'do-not-retry', 'active', '发送者不在要求的频道成员集合中。', 'The sender is absent from the channel membership required for this send.'),
  reason(4, 'ReasonInBlacklist', 'SEND', 'do-not-retry', 'active', '发送者命中频道黑名单。', 'The sender is present in the channel denylist.'),
  reason(5, 'ReasonChannelNotExist', 'SEND', 'application-policy', 'active', '频道不存在或当前不能接收该发送。', 'The channel does not exist or cannot currently accept the send.'),
  reason(6, 'ReasonUserNotOnNode', 'CONNECT / delivery', 'refresh-route', 'compatibility', '目标用户不在当前节点；刷新路由后再决定是否重试。', 'The target user is not on this node; refresh routing before deciding whether to retry.'),
  reason(7, 'ReasonSenderOffline', 'SEND', 'application-policy', 'reserved', '保留的发送者离线结果；当前产品路径未发出该值。', 'Reserved sender-offline result; no current product path emits this value.'),
  reason(8, 'ReasonMsgKeyError', 'SEND', 'do-not-retry', 'reserved', '保留的消息密钥或完整性错误；当前产品路径未发出该值。', 'Reserved message-key or integrity error; no current product path emits this value.'),
  reason(9, 'ReasonPayloadDecodeError', 'SEND', 'do-not-retry', 'active', 'SEND 请求（字段或载荷）格式错误或不受支持，包括解码失败。', 'Malformed or unsupported SEND request (fields or payload), including decode failures.'),
  reason(10, 'ReasonForwardSendPacketError', 'SEND', 'retry-with-backoff', 'compatibility', '兼容转发路径未能转发发送包。', 'A compatibility forwarding path could not forward the send packet.'),
  reason(11, 'ReasonNotAllowSend', 'SEND', 'do-not-retry', 'active', '频道策略不允许该发送者发送。', 'Channel policy does not allow this sender to send.'),
  reason(12, 'ReasonConnectKick', 'CONNECT / disconnect', 'application-policy', 'reserved', '保留的连接踢下线结果；当前产品路径未发出该值。', 'Reserved connection-kick result; no current product path emits this value.'),
  reason(13, 'ReasonNotInWhitelist', 'SEND', 'do-not-retry', 'active', '频道启用白名单，而发送者不在其中。', 'The channel requires an allowlist and the sender is absent from it.'),
  reason(14, 'ReasonQueryTokenError', 'CONNECT', 'retry-with-backoff', 'reserved', '保留的 Token 查询错误；当前默认组合未发出该值。', 'Reserved token-query error; the current default composition does not emit this value.'),
  reason(15, 'ReasonSystemError', 'CONNECT / SEND', 'retry-with-backoff', 'active', '服务端压力或内部错误；使用同一幂等键进行有界退避。', 'Server pressure or internal error; retry with bounded backoff and the same idempotency key.'),
  reason(16, 'ReasonChannelIDError', 'SEND', 'do-not-retry', 'reserved', '保留的频道标识错误；当前产品路径使用其他错误映射。', 'Reserved channel-identifier error; current product paths use other error mappings.'),
  reason(17, 'ReasonNodeMatchError', 'CONNECT / SEND', 'refresh-route', 'compatibility', '兼容节点匹配失败；刷新路由。', 'Compatibility node matching failed; refresh routing.'),
  reason(18, 'ReasonNodeNotMatch', 'SEND', 'refresh-route', 'active', '当前节点不是新鲜权威目标；刷新路由并复用原幂等键。', 'The current node is not the fresh authority target; refresh routing and reuse the original idempotency key.'),
  reason(19, 'ReasonBan', 'CONNECT / SEND', 'do-not-retry', 'active', '用户连接或频道发送已被封禁。', 'The user connection or channel send is banned.'),
  reason(20, 'ReasonNotSupportHeader', 'CONNECT / SEND', 'do-not-retry', 'reserved', '保留的 Header 不支持结果；当前产品路径未发出该值。', 'Reserved unsupported-header result; no current product path emits this value.'),
  reason(21, 'ReasonClientKeyIsEmpty', 'CONNECT', 'do-not-retry', 'active', '需要客户端密钥的握手未提供密钥。', 'A handshake that requires a client key did not provide one.'),
  reason(22, 'ReasonRateLimit', 'CONNECT / SEND', 'retry-with-backoff', 'compatibility', '客户端与压测器保留的限流结果；当前默认产品路径未发出该值。', 'Rate-limit result retained by clients and workload tooling; the current default product path does not emit it.'),
  reason(23, 'ReasonNotSupportChannelType', 'SEND', 'do-not-retry', 'reserved', '保留的频道类型不支持结果；当前产品路径未发出该值。', 'Reserved unsupported-channel-type result; no current product path emits this value.'),
  reason(24, 'ReasonDisband', 'SEND', 'do-not-retry', 'active', '频道已解散，不能继续发送。', 'The channel is disbanded and cannot accept further sends.'),
  reason(25, 'ReasonSendBan', 'SEND', 'do-not-retry', 'active', '发送者被禁止发送。', 'The sender is banned from sending.'),
  reason(26, 'ReasonChannelDeleting', 'SEND', 'retry-with-backoff', 'reserved', '保留的频道删除中结果；当前产品路径未发出该值。', 'Reserved channel-deleting result; no current product path emits this value.'),
  reason(27, 'ReasonProtocolUpgradeRequired', 'CONNECT', 'upgrade-client', 'compatibility', '网关保留该连接失败分类；当前默认认证路径未发出该值。', 'The gateway retains this connection-failure classification; the current default authenticator does not emit it.'),
  reason(28, 'ReasonIdempotencyConflict', 'SEND', 'do-not-retry', 'reserved', '保留的幂等冲突结果；当前产品路径未发出该值。', 'Reserved idempotency-conflict result; no current product path emits this value.'),
  reason(29, 'ReasonMessageSeqExhausted', 'SEND', 'do-not-retry', 'reserved', '保留的消息序号耗尽结果；当前产品路径未发出该值。', 'Reserved message-sequence-exhausted result; no current product path emits this value.'),
];

type DeveloperContractLocale = 'zh' | 'en';

/** Renders the compatibility snapshot into Markdown exports and LLM indexes. */
export function renderCompatibilityMarkdown(
  locale: DeveloperContractLocale,
  options?: CompatibilitySnapshotOptions,
): string {
  const snapshot = options ? buildCompatibilitySnapshot(options) : compatibilitySnapshot;
  const title = locale === 'zh' ? '兼容性快照（共享契约）' : 'Compatibility snapshot (shared contract)';
  const verificationStatus = {
    verified: {
      zh: '已验证（精确 receipt 与当前构建匹配）',
      en: 'verified (exact receipt matches this build)',
    },
    missing: {
      zh: '未验证（未提供验证 receipt）',
      en: 'unverified (no verification receipt supplied)',
    },
    malformed: {
      zh: '未验证（验证 receipt 无法读取或格式错误）',
      en: 'unverified (verification receipt is unreadable or malformed)',
    },
    mismatch: {
      zh: '未验证（验证 receipt 与当前构建不匹配）',
      en: 'unverified (verification receipt does not match this build)',
    },
  }[snapshot.verification.status][locale];

  return [
    `## ${title}`,
    '',
    `- ${locale === 'zh' ? '文档频道' : 'Documentation channel'}: \`${snapshot.channel}\``,
    `- ${locale === 'zh' ? '服务端源码修订' : 'Server source revision'}: \`${snapshot.source_revision}\``,
    `- ${locale === 'zh' ? '验证状态' : 'Verification status'}: ${verificationStatus}`,
    `- ${locale === 'zh' ? 'Receipt schema' : 'Receipt schema'}: \`${snapshot.verification.receipt_schema}\``,
    `- ${locale === 'zh' ? '拓扑' : 'Topology'}: ${snapshot.topology}`,
    `- ${locale === 'zh' ? '物理哈希槽' : 'Physical hash slots'}: ${snapshot.hash_slot_count}`,
    `- JavaScript SDK: \`${snapshot.sdk.package}@${snapshot.sdk.version}\``,
    `- ${locale === 'zh' ? '黄金场景' : 'Golden scenario'}: \`${snapshot.sample.scenario}\``,
    `- ${locale === 'zh' ? '样例 Node.js 要求' : 'Sample Node.js requirement'}: \`${snapshot.sample.node_requirement}\``,
    `- ${locale === 'zh' ? '样例锁文件 SHA-256' : 'Sample lockfile SHA-256'}: \`${snapshot.sample.package_lock_sha256}\``,
    `- ${locale === 'zh' ? '测试运行时目标' : 'Test runtime target'}: Node.js ${snapshot.runtime.node}, ${snapshot.runtime.package_manager}`,
    `- ${locale === 'zh' ? 'Chromium 测试目标' : 'Chromium test target'}: ${snapshot.runtime.browser.engine} ${snapshot.runtime.browser.browser_version} (revision ${snapshot.runtime.browser.revision}) via \`${snapshot.runtime.browser.playwright_package}@${snapshot.runtime.browser.playwright_version}\``,
    `- ${locale === 'zh' ? '其他浏览器' : 'Other browsers'}: ${snapshot.runtime.browser.other_browsers}`,
    `- [compatibility.json](${snapshot.contracts.compatibility})`,
    `- [OpenAPI ${locale === 'zh' ? '子集' : 'subset'}](${snapshot.contracts.openapi})`,
  ].join('\n');
}

/** Renders the intentionally narrow Product HTTP boundary into Markdown exports. */
export function renderGoldenPathContractMarkdown(locale: DeveloperContractLocale): string {
  return [
    `## ${locale === 'zh' ? 'JavaScript Web 黄金路径契约' : 'JavaScript Web golden-path contract'}`,
    '',
    locale === 'zh'
      ? '这是非完整的 v3 Beta 子集；三个调用只能出现在受信任的 localhost BFF 中，浏览器不能直接调用 Product HTTP API。'
      : 'This is a non-exhaustive v3 Beta subset. These three calls belong only in the trusted localhost BFF; the browser must not call the Product HTTP API directly.',
    '',
    ...goldenPathHTTPPaths.map((path) => `- \`${path}\``),
    '',
    `[OpenAPI 3.1 ${locale === 'zh' ? '子集' : 'subset'}](${compatibilitySnapshot.contracts.openapi})`,
  ].join('\n');
}

/** Renders the complete source-checked wire ReasonCode table into Markdown exports. */
export function renderReasonCodeMarkdown(locale: DeveloperContractLocale): string {
  const headings =
    locale === 'zh'
      ? ['值', '名称', '阶段', '重试指引', '可达性', '说明']
      : ['Value', 'Name', 'Stage', 'Retry guidance', 'Reachability', 'Meaning'];
  const rows = reasonCodes.map(
    (item) =>
      `| ${item.value} | \`${item.name}\` | ${item.stage} | ${item.retry} | ${item.reachability} | ${item.summary[locale]} |`,
  );

  return [
    `## ${locale === 'zh' ? 'Wire ReasonCode 完整枚举（共享契约）' : 'Complete wire ReasonCode enum (shared contract)'}`,
    '',
    `| ${headings.join(' | ')} |`,
    `| ${headings.map(() => '---').join(' | ')} |`,
    ...rows,
  ].join('\n');
}

/** Selects only the shared facts relevant to one exported developer page. */
export function renderDeveloperContractSupplement(
  locale: DeveloperContractLocale,
  slugs: readonly string[],
): string {
  const route = slugs.join('/');
  const sections: string[] = [];
  if (
    route === 'sdk/compatibility' ||
    route === 'api/compatibility' ||
    route === 'sdk/javascript/quickstart'
  ) {
    sections.push(renderCompatibilityMarkdown(locale));
  }
  if (
    route === 'api/conventions' ||
    route === 'api/product-http' ||
    route === 'sdk/javascript/quickstart'
  ) {
    sections.push(renderGoldenPathContractMarkdown(locale));
  }
  if (route === 'api/dictionaries/reason-codes') {
    sections.push(renderReasonCodeMarkdown(locale));
  }
  return sections.join('\n\n');
}
