export type ProtocolDirection =
  | 'none'
  | 'client-to-server'
  | 'server-to-client'
  | 'bidirectional';

export type ProtocolPublication = 'public-core' | 'codec-only' | 'tooling-only' | 'reserved';

export interface WKProtoWireField {
  name: string;
  type: string;
  when?: string;
}

export interface WKProtoWirePacket {
  value: number;
  name: string;
  direction: ProtocolDirection;
  publication: ProtocolPublication;
  fields: readonly WKProtoWireField[];
  decoderCompatibility?: string;
}

function field(name: string, type: string, when?: string): WKProtoWireField {
  return when === undefined ? { name, type } : { name, type, when };
}

function packet(
  value: number,
  name: string,
  direction: ProtocolDirection,
  publication: ProtocolPublication,
  fields: readonly WKProtoWireField[],
  decoderCompatibility?: string,
): WKProtoWirePacket {
  return { value, name, direction, publication, fields, decoderCompatibility };
}

/** Exact WKProto fixed-header, length, scalar, and string encoding facts. */
export const wkprotoWireFormat = {
  fixedHeaderBytes: 1,
  frameTypeBits: '7..4',
  commonFlagBits: {
    DUP: 3,
    SyncOnce: 2,
    RedDot: 1,
    NoPersist: 0,
  },
  connackHasServerVersionBit: 0,
  remainingLength: 'base-128, least-significant 7-bit group first, continuation bit 0x80',
  scalarByteOrder: 'big-endian',
  stringEncoding: 'signed i16 big-endian byte length followed by raw bytes',
  maxEncodedStringBytes: (1 << 15) - 1,
  maxRemainingLengthBytes: 1 << 20,
  maxEncodedSendPayloadBytes: (1 << 15) - 1,
  latestVersion: 6,
  legacyMessageSeqVersion: 5,
} as const;

/** WebSocket carrier rules enforced before a WKProto or JSON-RPC adapter sees data. */
export const webSocketCarrier = {
  version: 13,
  emptyConfiguredPath: '/',
  clientFramesMasked: true,
  serverFramesMasked: false,
  fragmentedMessagesReassembled: true,
  maxReassembledBytes: 1 << 20,
  wkprotoMessageType: 'binary',
  controlPingHandledByCarrier: true,
} as const;

/** Primary encoder order for every current FrameType. Payload/data fields consume the body tail. */
export const wkprotoWirePackets: readonly WKProtoWirePacket[] = [
  packet(0, 'UNKNOWN', 'none', 'reserved', []),
  packet(1, 'CONNECT', 'client-to-server', 'public-core', [
    field('version', 'u8'),
    field('device_flag', 'u8'),
    field('device_id', 'str16be'),
    field('uid', 'str16be'),
    field('token', 'str16be'),
    field('client_timestamp', 'i64be milliseconds'),
    field('client_key', 'str16be'),
  ]),
  packet(2, 'CONNACK', 'server-to-client', 'public-core', [
    field('server_version', 'u8', 'fixed-header bit 0 is set'),
    field('time_diff', 'i64be milliseconds'),
    field('reason_code', 'u8'),
    field('server_key', 'str16be'),
    field('salt', 'str16be'),
    field('node_id', 'u64be', 'negotiated version >= 4'),
  ]),
  packet(3, 'SEND', 'client-to-server', 'public-core', [
    field('setting', 'u8'),
    field('client_seq', 'u32be'),
    field('client_msg_no', 'str16be'),
    field('stream_no', 'str16be', '2 <= version < 5 and SettingStream'),
    field('channel_id', 'str16be'),
    field('channel_type', 'u8'),
    field('expire', 'u32be', 'version >= 3'),
    field('msg_key', 'str16be'),
    field('topic', 'str16be', 'SettingTopic'),
    field('payload', 'bytes-rest'),
  ]),
  packet(
    4,
    'SENDACK',
    'server-to-client',
    'public-core',
    [
      field('message_id', 'i64be'),
      field('client_seq', 'u32be'),
      field('message_seq', 'u32be when version <= 5; u64be when version >= 6'),
      field('reason_code', 'u8'),
      field('client_msg_no', 'str16be', 'non-empty'),
    ],
    'The decoder also accepts the compatibility order message_id, client_seq, client_msg_no, message_seq, reason_code.',
  ),
  packet(5, 'RECV', 'server-to-client', 'public-core', [
    field('setting', 'u8'),
    field('msg_key', 'str16be'),
    field('from_uid', 'str16be'),
    field('channel_id', 'str16be'),
    field('channel_type', 'u8'),
    field('expire', 'u32be', 'version >= 3'),
    field('client_msg_no', 'str16be'),
    field('stream_flag', 'u8', '2 <= version < 5 and SettingStream'),
    field('stream_no', 'str16be', '2 <= version < 5 and SettingStream'),
    field('stream_id', 'u64be', '2 <= version < 5 and SettingStream'),
    field('message_id', 'i64be'),
    field('message_seq', 'u32be when version <= 5; u64be when version >= 6'),
    field('timestamp', 'i32be seconds'),
    field('topic', 'str16be', 'SettingTopic'),
    field('payload', 'bytes-rest'),
  ]),
  packet(6, 'RECVACK', 'client-to-server', 'public-core', [
    field('message_id', 'i64be'),
    field('message_seq', 'u32be when version <= 5; u64be when version >= 6'),
  ]),
  packet(7, 'PING', 'client-to-server', 'public-core', []),
  packet(8, 'PONG', 'server-to-client', 'public-core', []),
  packet(9, 'DISCONNECT', 'bidirectional', 'codec-only', [
    field('reason_code', 'u8'),
    field('reason', 'str16be'),
  ]),
  packet(10, 'SUB', 'client-to-server', 'codec-only', [
    field('setting', 'u8'),
    field('sub_no', 'str16be'),
    field('channel_id', 'str16be'),
    field('channel_type', 'u8'),
    field('action', 'u8'),
    field('param', 'str16be'),
  ]),
  packet(11, 'SUBACK', 'server-to-client', 'codec-only', [
    field('sub_no', 'str16be'),
    field('channel_id', 'str16be'),
    field('channel_type', 'u8'),
    field('action', 'u8'),
    field('reason_code', 'u8'),
  ]),
  packet(12, 'EVENT', 'bidirectional', 'tooling-only', [
    field('id', 'str16be'),
    field('type', 'str16be'),
    field('timestamp', 'i64be'),
    field('data', 'bytes-rest'),
  ]),
];

export type JSONRPCProductStatus =
  | 'works'
  | 'rejected'
  | 'auth-fail'
  | 'ignored'
  | 'bridge-missing';

export interface JSONRPCInboundSurface {
  kind: 'request' | 'notification';
  method: string;
  decoded: boolean;
  bridgedFrame?: string;
  productStatus: JSONRPCProductStatus;
}

/** Decoder-to-frame behavior for data received from a JSON-RPC WebSocket client. */
export const jsonRPCInboundSurface: readonly JSONRPCInboundSurface[] = [
  { kind: 'request', method: 'connect', decoded: true, bridgedFrame: 'CONNECT', productStatus: 'works' },
  { kind: 'request', method: 'send', decoded: true, bridgedFrame: 'SEND', productStatus: 'works' },
  { kind: 'request', method: 'ping', decoded: true, bridgedFrame: 'PING', productStatus: 'works' },
  { kind: 'request', method: 'disconnect', decoded: true, bridgedFrame: 'DISCONNECT', productStatus: 'rejected' },
  { kind: 'request', method: 'subscribe', decoded: true, bridgedFrame: 'SUB', productStatus: 'rejected' },
  { kind: 'request', method: 'unsubscribe', decoded: true, bridgedFrame: 'SUB', productStatus: 'rejected' },
  { kind: 'notification', method: 'recvack', decoded: true, bridgedFrame: 'RECVACK', productStatus: 'works' },
  { kind: 'notification', method: 'recv', decoded: true, productStatus: 'bridge-missing' },
  { kind: 'notification', method: 'disconnect', decoded: true, productStatus: 'bridge-missing' },
  { kind: 'notification', method: 'event', decoded: true, productStatus: 'bridge-missing' },
];

export interface JSONRPCOutboundSurface {
  frame: string;
  shape: string;
  productBoundary: string;
}

/** Frame-to-JSON mappings present in the codec; mapping does not imply a reachable product flow. */
export const jsonRPCOutboundSurface: readonly JSONRPCOutboundSurface[] = [
  { frame: 'CONNACK', shape: 'correlated connect result or error', productBoundary: 'reachable after JSON-RPC CONNECT authentication and activation' },
  { frame: 'SENDACK', shape: 'correlated send result or error', productBoundary: 'reachable on an authenticated JSON-RPC session' },
  { frame: 'SUBACK', shape: 'correlated subscription result or error', productBoundary: 'codec mapping only; the product Gateway rejects inbound SUB frames' },
  { frame: 'RECV', shape: 'recv notification with header object and object payload', productBoundary: 'online delivery only; offline sync is outside EasySDK' },
  { frame: 'EVENT', shape: 'event notification', productBoundary: 'tooling-only EVENT scope' },
  { frame: 'DISCONNECT', shape: 'disconnect notification', productBoundary: 'no published product emission contract' },
  {
    frame: 'PONG',
    shape: 'correlated response with result null',
    productBoundary: 'reachable after CONNECT as a strict JSON-RPC success response',
  },
];

/** Compatibility cryptography implemented by pkg/protocol/wkprotoenc. */
export const wkprotoEncryptionProfile = {
  keyAgreement: 'X25519',
  publicKeyEncoding: 'standard Base64 of 32 bytes',
  keyDerivation: 'lowercase hex(MD5(Base64(shared_secret)))[0:16] as AES-128 key bytes',
  iv: '16 random alphanumeric bytes returned as CONNACK salt and reused for the session',
  payload: 'PKCS#7 -> AES-128-CBC -> standard Base64',
  msgKey: 'lowercase hex(MD5(Base64(AES-128-CBC(PKCS#7(verification_bytes)))))',
  sendVerificationFields: [
    'client_seq',
    'client_msg_no',
    'channel_id',
    'channel_type',
    'encrypted_payload',
  ],
  recvVerificationFields: [
    'message_id',
    'message_seq',
    'client_msg_no',
    'timestamp',
    'from_uid',
    'channel_id',
    'channel_type',
    'encrypted_payload',
  ],
} as const;

export type WebhookBodyShape = 'message-array' | 'offline-message-object' | 'status-string-array';

export interface WebhookEventContract {
  name: string;
  body: WebhookBodyShape;
  boundary: string;
}

/** Exact event names and top-level body shapes accepted by the current webhook runtime. */
export const webhookEventContracts: readonly WebhookEventContract[] = [
  { name: 'msg.notify', body: 'message-array', boundary: 'durable committed messages' },
  { name: 'msg.offline', body: 'offline-message-object', boundary: 'eligible durable offline-recipient candidates' },
  { name: 'user.onlinestatus', body: 'status-string-array', boundary: 'legacy-compatible presence records' },
];

/** Defaults applied by internal/app.NormalizeWebhookConfig. */
export const webhookDeliveryDefaults = {
  queueSizePerEvent: 1024,
  workersPerEvent: 16,
  notifyBatchMaxItems: 100,
  notifyBatchMaxWaitMilliseconds: 500,
  onlineBatchMaxItems: 512,
  onlineBatchMaxWaitMilliseconds: 2000,
  offlineUIDBatchSize: 512,
  requestTimeoutMilliseconds: 5000,
  retryMaxAttempts: 3,
  successStatus: 200,
} as const;
