export type ClientProtocolLocale = 'zh' | 'en';
export type ClientProtocolFrameScope = 'public-core' | 'codec-only' | 'reserved';
export type ClientProtocolDirection =
  | 'none'
  | 'client-to-server'
  | 'server-to-client'
  | 'codec-defined'
  | 'internal-bidirectional';

export interface ClientProtocolFrameDefinition {
  value: number;
  name: string;
  direction: ClientProtocolDirection;
  scope: ClientProtocolFrameScope;
  summary: Record<ClientProtocolLocale, string>;
}

function protocolFrame(
  value: number,
  name: string,
  direction: ClientProtocolDirection,
  scope: ClientProtocolFrameScope,
  zh: string,
  en: string,
): ClientProtocolFrameDefinition {
  return { value, name, direction, scope, summary: { zh, en } };
}

/** FrameType catalog calibrated against pkg/protocol/frame/common.go and Gateway ingress. */
export const clientProtocolFrames: readonly ClientProtocolFrameDefinition[] = [
  protocolFrame(0, 'UNKNOWN', 'none', 'reserved', 'Wire 保留值；不能发送。', 'Wire-reserved value; do not send.'),
  protocolFrame(1, 'CONNECT', 'client-to-server', 'public-core', '必须是连接上的唯一首包。', 'Must be the sole first packet on a connection.'),
  protocolFrame(2, 'CONNACK', 'server-to-client', 'public-core', '返回连接结果和会话材料；v4+ 请求另带协商版本。', 'Returns connection result and session material; v4+ requests also receive the negotiated version.'),
  protocolFrame(3, 'SEND', 'client-to-server', 'public-core', '提交一条 Channel 消息。', 'Submits one Channel message.'),
  protocolFrame(4, 'SENDACK', 'server-to-client', 'public-core', '关联 SEND 并返回协议结果。', 'Correlates a SEND with its protocol result.'),
  protocolFrame(5, 'RECV', 'server-to-client', 'public-core', '向当前 Session 投递一条消息。', 'Delivers one message to the current Session.'),
  protocolFrame(6, 'RECVACK', 'client-to-server', 'public-core', '确认客户端处理了指定 RECV。', 'Acknowledges client handling of a specific RECV.'),
  protocolFrame(7, 'PING', 'client-to-server', 'public-core', '刷新入站活动并请求 PONG。', 'Refreshes inbound activity and requests PONG.'),
  protocolFrame(8, 'PONG', 'server-to-client', 'public-core', '无正文的 PING 响应。', 'Bodyless response to PING.'),
  protocolFrame(9, 'DISCONNECT', 'codec-defined', 'codec-only', '存在编解码器；当前产品 Gateway 入站未发布。', 'Codec exists; current product Gateway ingress is unpublished.'),
  protocolFrame(10, 'SUB', 'client-to-server', 'codec-only', '存在编解码器；当前产品 Gateway 入站未支持。', 'Codec exists; current product Gateway ingress does not support it.'),
  protocolFrame(11, 'SUBACK', 'server-to-client', 'codec-only', 'SUB 的编解码响应；当前产品入口未发布。', 'Codec response for SUB; current product entry is unpublished.'),
  protocolFrame(12, 'EVENT', 'internal-bidirectional', 'reserved', '仅保留给受控内部协议；不是应用事件 API。', 'Reserved for controlled internal protocols; not an application event API.'),
];

/** Source-aligned limits used by the concise client-protocol reference. */
export const clientProtocolLimits = {
  latestVersion: 6,
  legacyMessageSeqVersion: 5,
  legacyMessageSeqBits: 32,
  latestMessageSeqBits: 64,
  clientSeqWireBits: 32,
  maxRemainingLengthBytes: 1024 * 1024,
  maxEncodedSendPayloadBytes: (1 << 15) - 1,
  defaultReadIdleTimeoutSeconds: 3 * 60,
} as const;

export const clientProtocolScopeLabels: Record<
  ClientProtocolLocale,
  Record<ClientProtocolFrameScope, string>
> = {
  zh: {
    'public-core': '公共核心',
    'codec-only': '仅编解码',
    reserved: '保留',
  },
  en: {
    'public-core': 'Public core',
    'codec-only': 'Codec only',
    reserved: 'Reserved',
  },
};

export const clientProtocolDirectionLabels: Record<
  ClientProtocolLocale,
  Record<ClientProtocolDirection, string>
> = {
  zh: {
    none: '—',
    'client-to-server': '客户端 → 服务端',
    'server-to-client': '服务端 → 客户端',
    'codec-defined': '编解码定义',
    'internal-bidirectional': '内部双向',
  },
  en: {
    none: '—',
    'client-to-server': 'Client → Server',
    'server-to-client': 'Server → Client',
    'codec-defined': 'Codec-defined',
    'internal-bidirectional': 'Internal bidirectional',
  },
};

/** Renders the source-checked frame catalog for Markdown and future LLM supplements. */
export function renderClientProtocolPacketMarkdown(locale: ClientProtocolLocale): string {
  const isZh = locale === 'zh';
  const rows = clientProtocolFrames.map(
    (item) =>
      `| ${item.value} | \`${item.name}\` | ${clientProtocolDirectionLabels[locale][item.direction]} | ${clientProtocolScopeLabels[locale][item.scope]} | ${item.summary[locale]} |`,
  );

  return [
    `## ${isZh ? 'WKProto 数据包目录' : 'WKProto packet catalog'}`,
    '',
    `| ${isZh ? '值' : 'Value'} | ${isZh ? '名称' : 'Name'} | ${isZh ? '方向' : 'Direction'} | ${isZh ? '范围' : 'Scope'} | ${isZh ? '说明' : 'Meaning'} |`,
    '| --- | --- | --- | --- | --- |',
    ...rows,
    '',
    isZh
      ? '协议 v6 使用 64 位 `message_seq`；v5 及以下使用 32 位。`client_seq` 的 Wire 宽度始终为 32 位。'
      : 'Protocol v6 uses a 64-bit `message_seq`; v5 and earlier use 32 bits. The wire width of `client_seq` is always 32 bits.',
  ].join('\n');
}
