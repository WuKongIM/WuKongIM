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

export type JavaScriptCapabilityStatus = 'verified' | 'boundary' | 'unverified';

export interface JavaScriptCapabilityDefinition {
  id: string;
  status: JavaScriptCapabilityStatus;
  capability: { zh: string; en: string };
  evidence: { zh: string; en: string };
}

function javascriptCapability(
  id: string,
  status: JavaScriptCapabilityStatus,
  capabilityZh: string,
  capabilityEn: string,
  evidenceZh: string,
  evidenceEn: string,
): JavaScriptCapabilityDefinition {
  return {
    id,
    status,
    capability: { zh: capabilityZh, en: capabilityEn },
    evidence: { zh: evidenceZh, en: evidenceEn },
  };
}

/** Capabilities and boundaries proven by the pinned JavaScript/Web snapshot. */
export const javascriptWebCapabilities: JavaScriptCapabilityDefinition[] = [
  javascriptCapability(
    'route-connect',
    'verified',
    '路由发现与 CONNECT/CONNACK',
    'Route discovery and CONNECT/CONNACK',
    '真实 Chromium 场景通过 loopback BFF 获取路由，并让两个隔离 Session 完成连接。',
    'The real Chromium scenario discovers routing through the loopback BFF and connects two isolated Sessions.',
  ),
  javascriptCapability(
    'persistent-person-messaging',
    'verified',
    '持久单聊双向发送',
    'Bidirectional persistent person messaging',
    'Alice 与 Bob 使用 ChannelTypePerson=1 双向发送持久文本。',
    'Alice and Bob exchange persistent text in both directions with ChannelTypePerson=1.',
  ),
  javascriptCapability(
    'sendack-realtime-separation',
    'verified',
    'SENDACK 与实时接收分离',
    'SENDACK separated from realtime receipt',
    '场景分别断言发送确认计数和对端 realtime 事件。',
    'The scenario asserts sender acknowledgement counts separately from peer realtime events.',
  ),
  javascriptCapability(
    'reconnect-offline-sync',
    'verified',
    '断线、重连与离线同步',
    'Disconnect, reconnect, and offline synchronization',
    '离线期间消息先提交，接收端重连后通过有界 person-message sync 恢复；首次个人频道目录异步投影只重试精确未就绪响应。',
    'A message commits while the peer is offline and is recovered through bounded person-message sync; first-use asynchronous directory projection retries only the exact not-ready response.',
  ),
  javascriptCapability(
    'realtime-sync-deduplication',
    'verified',
    '实时与同步结果去重',
    'Realtime/synchronization deduplication',
    '在线已观察消息不会被后续同步再次标记为 recovered，离线消息只出现一次。',
    'An already observed realtime message is not relabeled as recovered, and the offline message appears once.',
  ),
  javascriptCapability(
    'production-connection-authentication',
    'boundary',
    '生产连接身份校验',
    'Production connection authentication',
    '默认 v3 Beta 组合未启用已存 Token verifier；开发连接成功不是生产鉴权证据。',
    'The default v3 Beta composition has no stored-token verifier; a successful development connection is not production-authentication evidence.',
  ),
  javascriptCapability(
    'browser-product-http-access',
    'boundary',
    '浏览器与 Product HTTP 隔离',
    'Browser isolation from Product HTTP',
    '浏览器只访问 loopback BFF；Product HTTP 调用属于受信服务端边界。',
    'The browser calls only the loopback BFF; Product HTTP calls belong to a trusted server-side boundary.',
  ),
  javascriptCapability(
    'non-chromium-browsers',
    'unverified',
    'Firefox、Safari/WebKit 与其他浏览器',
    'Firefox, Safari/WebKit, and other browsers',
    '当前 receipt 只接受固定 Chromium 目标；没有第二浏览器矩阵。',
    'The current receipt accepts only the pinned Chromium target; no second browser matrix exists.',
  ),
  javascriptCapability(
    'groups-and-specialized-channels',
    'unverified',
    '群聊与专用 Channel Type',
    'Groups and specialized Channel Types',
    '服务端枚举存在，但当前 JavaScript 可执行场景只覆盖单聊。',
    'Server enums exist, but the executable JavaScript scenario covers person messaging only.',
  ),
  javascriptCapability(
    'custom-messages-and-conversations',
    'unverified',
    '自定义消息与会话 API',
    'Custom messages and conversation APIs',
    '公共指南定义行为边界，当前固定 SDK 场景未验证平台 API。',
    'Common guides define behavior boundaries; the pinned SDK scenario does not verify platform APIs.',
  ),
  javascriptCapability(
    'push-and-multi-device',
    'unverified',
    '推送与多设备产品策略',
    'Push and multi-device product policy',
    '这些能力依赖应用后端、设备登记和厂商服务，不在浏览器黄金路径中。',
    'These capabilities depend on the product backend, device registry, and providers and are outside the browser golden path.',
  ),
  javascriptCapability(
    'transient-and-background-behavior',
    'unverified',
    'NoPersist、后台生命周期与完整 SDK 面',
    'NoPersist, background lifecycle, and complete SDK surface',
    '当前场景不执行瞬时消息、OS 后台约束、完整 API Reference 或升级行为。',
    'The current scenario does not exercise transient messaging, OS background constraints, the complete API reference, or upgrades.',
  ),
];

export const javascriptCapabilityStatusLabels: Record<
  DeveloperContractLocale,
  Record<JavaScriptCapabilityStatus, string>
> = {
  zh: { verified: '已验证', boundary: '边界', unverified: '未验证' },
  en: { verified: 'Verified', boundary: 'Boundary', unverified: 'Unverified' },
};

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

export type ProtocolDictionaryName =
  | 'channel-types'
  | 'device-flags'
  | 'message-flags';

export type ProtocolValueScope =
  | 'baseline'
  | 'specialized'
  | 'legacy'
  | 'client'
  | 'internal'
  | 'wire';

export interface ProtocolValueDefinition {
  value: number;
  name: string;
  scope: ProtocolValueScope;
  summary: { zh: string; en: string };
}

function protocolValue(
  value: number,
  name: string,
  scope: ProtocolValueScope,
  zh: string,
  en: string,
): ProtocolValueDefinition {
  return { value, name, scope, summary: { zh, en } };
}

/** Channel Type values calibrated against pkg/protocol/frame/common.go. */
export const channelTypes: ProtocolValueDefinition[] = [
  protocolValue(1, 'ChannelTypePerson', 'baseline', '单聊；调用方使用对端 UID，服务端入口负责规范化双方 UID 的 Channel 身份。', 'Direct chat; callers use the peer UID and the server entry normalizes the two-UID Channel identity.'),
  protocolValue(2, 'ChannelTypeGroup', 'baseline', '群聊；业务服务必须先维护稳定 Channel ID、成员和发送策略。', 'Group chat; the product service must first maintain a stable Channel ID, membership, and send policy.'),
  protocolValue(3, 'ChannelTypeCustomerService', 'legacy', '旧客服频道类型；源码已标注过时，新访客流程使用 ChannelTypeVisitors。', 'Legacy customer-service type; source marks it deprecated in favor of ChannelTypeVisitors for new visitor flows.'),
  protocolValue(4, 'ChannelTypeCommunity', 'specialized', '社区容器类型；只在所选 SDK 与业务流程明确支持时使用。', 'Community container type; use only when the selected SDK and product flow explicitly support it.'),
  protocolValue(5, 'ChannelTypeCommunityTopic', 'specialized', '社区话题类型；不要把它与社区容器或普通群聊互换。', 'Community-topic type; do not interchange it with a community container or ordinary group chat.'),
  protocolValue(6, 'ChannelTypeInfo', 'specialized', '资讯频道，包含临时订阅者语义；接入前验证对应成员生命周期。', 'Information Channel with temporary-subscriber semantics; verify the matching membership lifecycle before integration.'),
  protocolValue(7, 'ChannelTypeData', 'specialized', '数据频道；枚举存在不等于当前平台已发布完整接入流程。', 'Data Channel; enum presence does not mean a complete platform integration flow is published.'),
  protocolValue(8, 'ChannelTypeTemp', 'specialized', '临时或请求级目标频道；不能当作持久业务群 ID。', 'Temporary or request-scoped target Channel; do not persist it as a product group ID.'),
  protocolValue(9, 'ChannelTypeLive', 'specialized', '直播频道；当前语义不保存最近会话数据。', 'Live Channel; current semantics do not retain recent-conversation data.'),
  protocolValue(10, 'ChannelTypeVisitors', 'specialized', '访客频道；Channel ID 是访客 UID，可对应一个访客和多个客服订阅者。', 'Visitor Channel; the Channel ID is the visitor UID and may represent one visitor with multiple support subscribers.'),
  protocolValue(11, 'ChannelTypeAgent', 'specialized', '单聊 Agent 频道；内部身份形如 UID@AgentID，业务服务仍负责 Agent 授权。', 'Direct Agent Channel; its internal identity is shaped like UID@AgentID while the product service still owns Agent authorization.'),
  protocolValue(12, 'ChannelTypeAgentGroup', 'specialized', '群聊 Agent 频道；用于多 Agent 协同，不是普通群聊的透明别名。', 'Group Agent Channel for multi-Agent collaboration; it is not a transparent alias for ordinary group chat.'),
];

/** Device category values calibrated against protocolmeta and the frame authority. */
export const deviceFlags: ProtocolValueDefinition[] = [
  protocolValue(0, 'APP', 'client', '原生移动应用设备类别。', 'Native mobile-application device category.'),
  protocolValue(1, 'WEB', 'client', 'Web 浏览器或 Web 应用设备类别。', 'Web browser or web-application device category.'),
  protocolValue(2, 'PC', 'client', '桌面客户端设备类别。', 'Desktop-client device category.'),
  protocolValue(99, 'SYSTEM', 'internal', '服务端保留的系统设备类别；终端应用不能冒充。', 'Server-reserved system device category; end-user clients must not impersonate it.'),
];

/** Same-category connection-conflict policies calibrated against protocolmeta. */
export const deviceLevels: ProtocolValueDefinition[] = [
  protocolValue(0, 'DeviceLevelSlave', 'client', '允许同一设备类别的多个端点共存。', 'Allows multiple endpoints in the same device category to coexist.'),
  protocolValue(1, 'DeviceLevelMaster', 'client', '声明同一设备类别的单活冲突策略；是否生效仍取决于已验证的连接鉴权与组合。', 'Declares single-active conflict policy within one device category; enforcement still depends on verified connection authentication and composition.'),
];

export interface MessageHeaderFlagDefinition extends ProtocolValueDefinition {
  bit: number;
}

function messageHeaderFlag(
  bit: number,
  name: string,
  zh: string,
  en: string,
): MessageHeaderFlagDefinition {
  return {
    ...protocolValue(bit, name, 'wire', zh, en),
    bit,
  };
}

/** WKProto fixed-header flag bits calibrated against pkg/protocol/codec/common.go. */
export const messageHeaderFlags: MessageHeaderFlagDefinition[] = [
  messageHeaderFlag(0, 'NoPersist', '普通非命令分支只返回兼容成功且不投递；只有命令式分支进入瞬时在线投递。两者都没有持久序号或离线恢复。', 'The plain non-command branch returns compatibility success without delivery; only the command-style branch enters transient online delivery. Neither has a durable sequence or offline recovery.'),
  messageHeaderFlag(1, 'RedDot', '携带红点展示意图；它不是消息已读回执，也不单独证明服务端未读数发生变化。', 'Carries red-dot display intent; it is not a read receipt and does not by itself prove a server unread-count change.'),
  messageHeaderFlag(2, 'SyncOnce', '把命令式消息路由到独立 CMD Channel；可恢复命令还需要绑定与 CMD 同步流程。', 'Routes command-style messages through a separate CMD Channel; recoverable commands additionally require binding and CMD synchronization.'),
  messageHeaderFlag(3, 'DUP', '协议重发标记；业务幂等仍以稳定 client_msg_no 和结果关联为准。', 'Protocol retransmission marker; product idempotency still relies on a stable client_msg_no and result correlation.'),
];

/** Message Setting bits calibrated against pkg/protocol/frame/setting.go. */
export const messageSettings: ProtocolValueDefinition[] = [
  protocolValue(128, 'SettingReceiptEnabled', 'wire', '开启协议回执意图；不能把它等同于 Channel 提交、设备业务执行或最终用户已读。', 'Enables protocol receipt intent; do not equate it with Channel commit, device-side business execution, or end-user read state.'),
  protocolValue(32, 'SettingSignal', 'wire', '标记兼容 signal 模式；只在所选 SDK 和协议版本明确支持时使用。', 'Marks compatible signal mode; use only when the selected SDK and protocol version explicitly support it.'),
  protocolValue(16, 'SettingNoEncrypt', 'wire', '跳过已协商的会话 Payload 加密；它不替代 TLS，敏感消息不应启用。', 'Skips negotiated session payload encryption; it does not replace TLS and should not be enabled for sensitive messages.'),
  protocolValue(8, 'SettingTopic', 'wire', '表示数据包携带 Topic 字段；Topic 生命周期仍由兼容客户端与业务约定。', 'Indicates that the packet carries a Topic field; Topic lifecycle remains a compatible-client and product contract.'),
  protocolValue(2, 'SettingStream', 'wire', '表示兼容流消息字段；流式 AI 的持久投影与实时增量仍是不同路径。', 'Indicates compatible stream-message fields; durable AI stream projection and realtime deltas remain separate paths.'),
];

export const protocolScopeLabels: Record<
  DeveloperContractLocale,
  Record<ProtocolValueScope, string>
> = {
  zh: {
    baseline: '基础接入',
    specialized: '专用类型',
    legacy: '兼容 / 旧类型',
    client: '客户端',
    internal: '服务端保留',
    wire: 'Wire 标志',
  },
  en: {
    baseline: 'Integration baseline',
    specialized: 'Specialized',
    legacy: 'Compatibility / legacy',
    client: 'Client',
    internal: 'Server-reserved',
    wire: 'Wire flag',
  },
};

function renderProtocolValueTable(
  locale: DeveloperContractLocale,
  values: readonly ProtocolValueDefinition[],
  firstHeading: string,
): string[] {
  const scopeHeading = locale === 'zh' ? '范围' : 'Scope';
  const meaningHeading = locale === 'zh' ? '集成说明' : 'Integrator guidance';
  return [
    `| ${firstHeading} | ${locale === 'zh' ? '名称' : 'Name'} | ${scopeHeading} | ${meaningHeading} |`,
    '| --- | --- | --- | --- |',
    ...values.map(
      (item) =>
        `| ${item.value} | \`${item.name}\` | ${protocolScopeLabels[locale][item.scope]} | ${item.summary[locale]} |`,
    ),
  ];
}

/** Renders source-checked protocol dictionaries into Markdown and LLM exports. */
export function renderProtocolDictionaryMarkdown(
  locale: DeveloperContractLocale,
  dictionary: ProtocolDictionaryName,
): string {
  if (dictionary === 'channel-types') {
    return [
      `## ${locale === 'zh' ? 'Channel Type（共享契约）' : 'Channel Types (shared contract)'}`,
      '',
      ...renderProtocolValueTable(locale, channelTypes, locale === 'zh' ? '值' : 'Value'),
    ].join('\n');
  }
  if (dictionary === 'device-flags') {
    return [
      `## ${locale === 'zh' ? 'Device Flag（共享契约）' : 'Device Flags (shared contract)'}`,
      '',
      ...renderProtocolValueTable(locale, deviceFlags, locale === 'zh' ? '值' : 'Value'),
      '',
      `### ${locale === 'zh' ? 'Device Level' : 'Device Levels'}`,
      '',
      ...renderProtocolValueTable(locale, deviceLevels, locale === 'zh' ? '值' : 'Value'),
    ].join('\n');
  }

  const headerRows = messageHeaderFlags.map((item) => ({ ...item, value: item.bit }));
  return [
    `## ${locale === 'zh' ? '消息标志（共享契约）' : 'Message flags (shared contract)'}`,
    '',
    `### ${locale === 'zh' ? '固定 Header 位' : 'Fixed-header bits'}`,
    '',
    ...renderProtocolValueTable(locale, headerRows, locale === 'zh' ? 'Bit' : 'Bit'),
    '',
    `### ${locale === 'zh' ? 'Setting 位值' : 'Setting bit values'}`,
    '',
    ...renderProtocolValueTable(locale, messageSettings, locale === 'zh' ? '值' : 'Value'),
  ].join('\n');
}

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

/** Renders the JavaScript/Web evidence matrix into Markdown exports. */
export function renderJavaScriptCapabilityMarkdown(
  locale: DeveloperContractLocale,
): string {
  const headings =
    locale === 'zh'
      ? ['ID', '能力', '状态', '证据或边界']
      : ['ID', 'Capability', 'Status', 'Evidence or boundary'];
  return [
    `## ${locale === 'zh' ? 'JavaScript / Web 能力证据矩阵（共享契约）' : 'JavaScript / Web capability evidence matrix (shared contract)'}`,
    '',
    `| ${headings.join(' | ')} |`,
    `| ${headings.map(() => '---').join(' | ')} |`,
    ...javascriptWebCapabilities.map(
      (item) =>
        `| \`${item.id}\` | ${item.capability[locale]} | ${javascriptCapabilityStatusLabels[locale][item.status]} | ${item.evidence[locale]} |`,
    ),
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
  if (route === 'sdk/javascript/platform-capabilities') {
    sections.push(renderJavaScriptCapabilityMarkdown(locale));
  }
  if (route === 'api/dictionaries/reason-codes') {
    sections.push(renderReasonCodeMarkdown(locale));
  }
  const protocolDictionary = route.match(
    /^api\/dictionaries\/(channel-types|device-flags|message-flags)$/,
  )?.[1] as ProtocolDictionaryName | undefined;
  if (protocolDictionary) {
    sections.push(renderProtocolDictionaryMarkdown(locale, protocolDictionary));
  }
  return sections.join('\n\n');
}
