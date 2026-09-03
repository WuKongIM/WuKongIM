import { describe, expect, test } from 'bun:test';
import {
  jsonRPCInboundSurface,
  jsonRPCOutboundSurface,
  webhookDeliveryDefaults,
  webhookEventContracts,
  webSocketCarrier,
  wkprotoEncryptionProfile,
  wkprotoWireFormat,
  wkprotoWirePackets,
} from './protocol-surface-contracts';

const repositoryRoot = new URL('../../', import.meta.url);
const contentRoot = new URL('../content/docs/', import.meta.url);

async function source(path: string) {
  return Bun.file(new URL(path, repositoryRoot)).text();
}

async function page(path: string) {
  return Bun.file(new URL(path, contentRoot)).text();
}

function expectTokenOrder(text: string, tokens: readonly string[]) {
  let offset = -1;
  for (const token of tokens) {
    const next = text.indexOf(token, offset + 1);
    expect(next, `missing or out-of-order token: ${token}`).toBeGreaterThan(offset);
    offset = next;
  }
}

function functionBody(text: string, start: string, next: string) {
  const startOffset = text.indexOf(start);
  const endOffset = text.indexOf(next, startOffset + start.length);
  if (startOffset < 0 || endOffset < 0) {
    throw new Error(`unable to isolate ${start}`);
  }
  return text.slice(startOffset, endOffset);
}

function packetFields(name: string) {
  const packet = wkprotoWirePackets.find((item) => item.name === name);
  if (!packet) throw new Error(`missing WKProto packet ${name}`);
  return packet.fields.map((item) => item.name);
}

describe('protocol surface contracts', () => {
  test('freezes every WKProto primary encoder layout', async () => {
    expect(wkprotoWirePackets.map(({ name, value }) => ({ name, value }))).toEqual([
      { name: 'UNKNOWN', value: 0 },
      { name: 'CONNECT', value: 1 },
      { name: 'CONNACK', value: 2 },
      { name: 'SEND', value: 3 },
      { name: 'SENDACK', value: 4 },
      { name: 'RECV', value: 5 },
      { name: 'RECVACK', value: 6 },
      { name: 'PING', value: 7 },
      { name: 'PONG', value: 8 },
      { name: 'DISCONNECT', value: 9 },
      { name: 'SUB', value: 10 },
      { name: 'SUBACK', value: 11 },
      { name: 'EVENT', value: 12 },
    ]);

    expect(packetFields('CONNECT')).toEqual([
      'version',
      'device_flag',
      'device_id',
      'uid',
      'token',
      'client_timestamp',
      'client_key',
    ]);
    expect(packetFields('CONNACK')).toEqual([
      'server_version',
      'time_diff',
      'reason_code',
      'server_key',
      'salt',
      'node_id',
    ]);
    expect(packetFields('SEND')).toEqual([
      'setting',
      'client_seq',
      'client_msg_no',
      'stream_no',
      'channel_id',
      'channel_type',
      'expire',
      'msg_key',
      'topic',
      'payload',
    ]);
    expect(packetFields('SENDACK')).toEqual([
      'message_id',
      'client_seq',
      'message_seq',
      'reason_code',
      'client_msg_no',
    ]);
    expect(packetFields('RECV')).toEqual([
      'setting',
      'msg_key',
      'from_uid',
      'channel_id',
      'channel_type',
      'expire',
      'client_msg_no',
      'stream_flag',
      'stream_no',
      'stream_id',
      'message_id',
      'message_seq',
      'timestamp',
      'topic',
      'payload',
    ]);
    expect(packetFields('RECVACK')).toEqual(['message_id', 'message_seq']);
    expect(packetFields('DISCONNECT')).toEqual(['reason_code', 'reason']);
    expect(packetFields('SUB')).toEqual([
      'setting',
      'sub_no',
      'channel_id',
      'channel_type',
      'action',
      'param',
    ]);
    expect(packetFields('SUBACK')).toEqual([
      'sub_no',
      'channel_id',
      'channel_type',
      'action',
      'reason_code',
    ]);
    expect(packetFields('EVENT')).toEqual(['id', 'type', 'timestamp', 'data']);
    expect(packetFields('PING')).toEqual([]);
    expect(packetFields('PONG')).toEqual([]);

    const frameCommon = await source('pkg/protocol/frame/common.go');
    const frameTypeBlock = functionBody(
      frameCommon,
      'const (\n\tUNKNOWN',
      ')\n\nfunc (p FrameType) String()',
    );
    const sourceFrameTypes = [...frameTypeBlock.matchAll(/^\s*([A-Z][A-Z0-9_]*)/gmu)].map(
      (match) => match[1],
    );
    expect(sourceFrameTypes).toEqual(wkprotoWirePackets.map((item) => item.name));

    const [connect, connack, send, sendack, recv, recvack, disconnect, sub, suback, event] =
      await Promise.all([
        source('pkg/protocol/codec/connect.go'),
        source('pkg/protocol/codec/connack.go'),
        source('pkg/protocol/codec/send.go'),
        source('pkg/protocol/codec/sendack.go'),
        source('pkg/protocol/codec/recv.go'),
        source('pkg/protocol/codec/recvack.go'),
        source('pkg/protocol/codec/disconnect.go'),
        source('pkg/protocol/codec/sub.go'),
        source('pkg/protocol/codec/suback.go'),
        source('pkg/protocol/codec/event.go'),
      ]);

    expectTokenOrder(connect, [
      'enc.WriteUint8(connectPacket.Version)',
      'enc.WriteUint8(connectPacket.DeviceFlag.ToUint8())',
      'enc.WriteString(connectPacket.DeviceID)',
      'enc.WriteString(connectPacket.UID)',
      'enc.WriteString(connectPacket.Token)',
      'enc.WriteInt64(connectPacket.ClientTimestamp)',
      'enc.WriteString(connectPacket.ClientKey)',
    ]);
    expectTokenOrder(connack, [
      'enc.WriteUint8(connack.ServerVersion)',
      'enc.WriteInt64(connack.TimeDiff)',
      'enc.WriteByte(connack.ReasonCode.Byte())',
      'enc.WriteString(connack.ServerKey)',
      'enc.WriteString(connack.Salt)',
      'enc.WriteUint64(connack.NodeId)',
    ]);
    expectTokenOrder(send, [
      'enc.WriteByte(sendPacket.Setting.Uint8())',
      'enc.WriteUint32(uint32(sendPacket.ClientSeq))',
      'enc.WriteString(sendPacket.ClientMsgNo)',
      'enc.WriteString(sendPacket.StreamNo)',
      'enc.WriteString(sendPacket.ChannelID)',
      'enc.WriteUint8(sendPacket.ChannelType)',
      'enc.WriteUint32(sendPacket.Expire)',
      'enc.WriteString(sendPacket.MsgKey)',
      'enc.WriteString(sendPacket.Topic)',
      'enc.WriteBytes(sendPacket.Payload)',
    ]);
    expectTokenOrder(sendack, [
      'enc.WriteInt64(sendackPacket.MessageID)',
      'enc.WriteUint32(uint32(sendackPacket.ClientSeq))',
      'encodeMessageSeq(enc, version, sendackPacket.MessageSeq)',
      'enc.WriteUint8(sendackPacket.ReasonCode.Byte())',
      'enc.WriteString(sendackPacket.ClientMsgNo)',
    ]);
    expectTokenOrder(recv, [
      'enc.WriteByte(recvPacket.Setting.Uint8())',
      'enc.WriteString(recvPacket.MsgKey)',
      'enc.WriteString(recvPacket.FromUID)',
      'enc.WriteString(recvPacket.ChannelID)',
      'enc.WriteUint8(recvPacket.ChannelType)',
      'enc.WriteUint32(recvPacket.Expire)',
      'enc.WriteString(recvPacket.ClientMsgNo)',
      'enc.WriteUint8(uint8(recvPacket.StreamFlag))',
      'enc.WriteString(recvPacket.StreamNo)',
      'enc.WriteUint64(recvPacket.StreamId)',
      'enc.WriteInt64(recvPacket.MessageID)',
      'encodeMessageSeq(enc, version, recvPacket.MessageSeq)',
      'enc.WriteInt32(recvPacket.Timestamp)',
      'enc.WriteString(recvPacket.Topic)',
      'enc.WriteBytes(recvPacket.Payload)',
    ]);
    expectTokenOrder(recvack, [
      'enc.WriteInt64(recvackPacket.MessageID)',
      'encodeMessageSeq(enc, version, recvackPacket.MessageSeq)',
    ]);
    expectTokenOrder(disconnect, [
      'enc.WriteUint8(disConnectPacket.ReasonCode.Byte())',
      'enc.WriteString(disConnectPacket.Reason)',
    ]);
    expectTokenOrder(sub, [
      'enc.WriteByte(subPacket.Setting.Uint8())',
      'enc.WriteString(subPacket.SubNo)',
      'enc.WriteString(subPacket.ChannelID)',
      'enc.WriteUint8(subPacket.ChannelType)',
      'enc.WriteUint8(subPacket.Action.Uint8())',
      'enc.WriteString(subPacket.Param)',
    ]);
    expectTokenOrder(suback, [
      'enc.WriteString(subackPacket.SubNo)',
      'enc.WriteString(subackPacket.ChannelID)',
      'enc.WriteUint8(subackPacket.ChannelType)',
      'enc.WriteUint8(subackPacket.Action.Uint8())',
      'enc.WriteUint8(subackPacket.ReasonCode.Byte())',
    ]);
    expectTokenOrder(event, [
      'enc.WriteString(eventPacket.Id)',
      'enc.WriteString(eventPacket.Type)',
      'enc.WriteInt64(eventPacket.Timestamp)',
      'enc.WriteBytes(eventPacket.Data)',
    ]);
    expect(sendack).toContain('decodeSendackBodyClientMsgNoFirst');
    expect(connack).toContain('if connack.GetHasServerVersion()');
    expect(connack).toContain('if version >= 4');
    for (const conditionalCodec of [send, recv]) {
      expect(conditionalCodec).toContain('if version < 5');
      expect(conditionalCodec).toContain('version >= 2');
      expect(conditionalCodec).toContain('SettingStream');
      expect(conditionalCodec).toContain('if version >= 3');
      expect(conditionalCodec).toContain('SettingTopic');
    }
    expect(sendack).toContain('if sendackPacket.ClientMsgNo != ""');
  });

  test('matches WKProto header, scalar, string, version, and size authorities', async () => {
    const [common, protocol, encoder, frameCommon, messageSeq] = await Promise.all([
      source('pkg/protocol/codec/common.go'),
      source('pkg/protocol/codec/protocol.go'),
      source('pkg/protocol/codec/encoder.go'),
      source('pkg/protocol/frame/common.go'),
      source('pkg/protocol/codec/message_seq.go'),
    ]);

    expect(common.replaceAll(/\s+/gu, '')).toContain(
      'encodeBool(f.GetDUP())<<3|encodeBool(f.GetsyncOnce())<<2|encodeBool(f.GetRedDot())<<1|encodeBool(f.GetNoPersist())',
    );
    expect(common).toContain('typeAndFlags = encodeBool(f.GetHasServerVersion())');
    expect(protocol).toContain('frameType == frame.PING || frameType == frame.PONG');
    expect(protocol).toContain('byte(int(frameType) << 4)');
    expect(protocol).toContain('digit |= 0x80');
    expect(protocol).toContain('digit&127');
    expect(encoder).toContain('if bl > math.MaxInt16');
    expect(encoder).toContain('e.WriteInt16(bl)');
    expect(encoder).toContain('byte(i >> 56)');
    expect(frameCommon).toContain('LegacyMessageSeqVersion = 5');
    expect(frameCommon).toContain('MessageSeqU64Version    = 6');
    expect(messageSeq).toContain('version <= frame.LegacyMessageSeqVersion');
    expect(wkprotoWireFormat).toMatchObject({
      fixedHeaderBytes: 1,
      maxEncodedStringBytes: 32767,
      maxRemainingLengthBytes: 1048576,
      maxEncodedSendPayloadBytes: 32767,
      latestVersion: 6,
      legacyMessageSeqVersion: 5,
    });
  });

  test('matches the WebSocket carrier handshake, framing, and control boundary', async () => {
    const [handshake, connection, gatewayOptions, writeTypes] = await Promise.all([
      source('pkg/gateway/transport/gnet/ws_handshake.go'),
      source('pkg/gateway/transport/gnet/conn.go'),
      source('pkg/gateway/types/options.go'),
      source('pkg/gateway/core/server_encode_write_test.go'),
    ]);

    expect(webSocketCarrier).toEqual({
      version: 13,
      emptyConfiguredPath: '/',
      clientFramesMasked: true,
      serverFramesMasked: false,
      fragmentedMessagesReassembled: true,
      maxReassembledBytes: 1048576,
      wkprotoMessageType: 'binary',
      controlPingHandledByCarrier: true,
    });
    expect(handshake).toContain('req.Method != http.MethodGet');
    expect(handshake).toContain('path = "/"');
    expect(handshake).toContain('version != "13"');
    expect(connection).toContain('client websocket frames must be masked');
    expect(connection).toContain('case wsOpcodeContinuation:');
    expect(connection).toContain('case wsOpcodePing:');
    expect(connection).toContain('opcode:  wsOpcodePong');
    expect(gatewayOptions).toContain('MaxInboundBytes:          1 << 20');
    expect(writeTypes).toContain('protocol: "wkproto", wantType: transport.WebSocketMessageBinary');
  });

  test('records the actual JSON-RPC bridge instead of the declared-only schema', async () => {
    expect(jsonRPCInboundSurface).toEqual([
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
    ]);
    expect(jsonRPCOutboundSurface.map((item) => item.frame)).toEqual([
      'CONNACK',
      'SENDACK',
      'SUBACK',
      'RECV',
      'EVENT',
      'DISCONNECT',
      'PONG',
    ]);

    const [codec, types, mux, server, handler] = await Promise.all([
      source('pkg/protocol/jsonrpc/codec.go'),
      source('pkg/protocol/jsonrpc/types.go'),
      source('pkg/gateway/protocol/wsmux/adapter.go'),
      source('pkg/gateway/core/server.go'),
      source('internal/access/gateway/handler.go'),
    ]);
    const toFrame = functionBody(codec, 'func ToFrame(', 'func FromFrame(');
    const fromFrame = functionBody(codec, 'func FromFrame(', 'func IsJSONObjectPrefix(');
    expect([...toFrame.matchAll(/case ([A-Za-z]+):/gu)].map((match) => match[1])).toEqual([
      'ConnectRequest',
      'SendRequest',
      'PingRequest',
      'DisconnectRequest',
      'RecvAckNotification',
      'SubscribeRequest',
      'UnsubscribeRequest',
    ]);
    expect([...fromFrame.matchAll(/case frame\.([A-Z]+):/gu)].map((match) => match[1])).toEqual(
      jsonRPCOutboundSurface.map((item) => item.frame),
    );
    const pongMapping = functionBody(fromFrame, 'case frame.PONG:', '\n\t}');
    expect(pongMapping).toContain('Result: json.RawMessage("null")');
    expect(types).toMatch(/type SendParams struct \{[\s\S]*?Payload\s+\[\]byte/u);
    expect(types.match(/type SendParams struct \{[\s\S]*?\n\}/u)?.[0]).not.toContain('ClientSeq');
    expect(mux).toContain("case '{', '[':");
    expect(server).toContain('adapter.(protocol.ConnectAuthenticationPolicy)');
    expect(server).toContain('state.listener.auth.ConnectAuthenticationRequired(state.session)');
    expect(server).toContain('state.setAuthRequired(false)');
    expect(server).toContain('state.setAuthenticated(true)');
    const onFrame = functionBody(
      handler,
      'func (h *Handler) OnFrame(',
      'func (h *Handler) handleTerminalFence(',
    );
    expect(onFrame).not.toContain('case *frame.SubPacket:');
    expect(onFrame).toContain('return ErrUnsupportedFrame');
  });

  test('keeps the compatibility encryption algorithm and runtime ordering source-aligned', async () => {
    const [crypto, recvFrame, adapter, clientReader] = await Promise.all([
      source('pkg/protocol/wkprotoenc/crypto.go'),
      source('pkg/protocol/frame/recv.go'),
      source('pkg/gateway/protocol/wkproto/adapter.go'),
      source('pkg/client/reader.go'),
    ]);

    expect(wkprotoEncryptionProfile).toMatchObject({
      keyAgreement: 'X25519',
      publicKeyEncoding: 'standard Base64 of 32 bytes',
    });
    expect(crypto).toContain('curve25519.X25519');
    expect(crypto).toContain('md5.Sum([]byte(base64.StdEncoding.EncodeToString(secret)))');
    expect(crypto).toContain('hexLower(sum[:])[:16]');
    expect(crypto).toContain('const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"');
    expectTokenOrder(crypto, [
      'strconv.AppendUint(buf, packet.ClientSeq, 10)',
      'packet.ClientMsgNo',
      'packet.ChannelID',
      'strconv.AppendInt(buf, int64(packet.ChannelType), 10)',
      'packet.Payload',
    ]);
    const recvVerification = functionBody(
      recvFrame,
      'func (r *RecvPacket) VerityBytes(',
      'func (r *RecvPacket) String()',
    );
    expectTokenOrder(recvVerification, [
      'r.MessageID',
      'r.MessageSeq',
      'r.ClientMsgNo',
      'r.Timestamp',
      'r.FromUID',
      'r.ChannelID',
      'r.ChannelType',
      'r.Payload',
    ]);
    const encryptPayload = functionBody(
      crypto,
      'func EncryptPayloadWithCrypto(',
      'func DecryptPayload(',
    );
    expectTokenOrder(encryptPayload, [
      'pkcs7PaddingSize',
      'encryptCBCBlocks',
      'base64.StdEncoding.Encode',
    ]);
    const msgKey = functionBody(crypto, 'func msgKeyWithCrypto(', 'func recvMsgKeyWithCrypto(');
    expectTokenOrder(msgKey, [
      'pkcs7PaddingSize',
      'encryptCBCBlocks',
      'base64.StdEncoding.Encode',
      'md5.Sum(encoded)',
    ]);
    expectTokenOrder(adapter, [
      'ValidateSendPacketWithCrypto(send, sessionCrypto)',
      'DecryptPayloadWithCrypto(send.Payload, sessionCrypto)',
    ]);
    expect(adapter).toContain('!send.Setting.IsSet(frame.SettingNoEncrypt)');
    expect(adapter).toContain('!recv.Setting.IsSet(frame.SettingNoEncrypt)');
    const decryptRecv = functionBody(clientReader, 'func (c *Client) decryptRecv(', 'func recvDecryptError(');
    expect(decryptRecv).toContain('DecryptPayloadWithCrypto');
    expect(decryptRecv).not.toContain('Validate');
  });

  test('matches webhook names, payload shapes, delivery semantics, and defaults', async () => {
    expect(webhookEventContracts.map(({ name, body }) => ({ name, body }))).toEqual([
      { name: 'msg.notify', body: 'message-array' },
      { name: 'msg.offline', body: 'offline-message-object' },
      { name: 'user.onlinestatus', body: 'status-string-array' },
    ]);
    expect(webhookDeliveryDefaults).toEqual({
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
    });

    const [types, mapper, sender, runtime, config, offline, presence] = await Promise.all([
      source('internal/runtime/webhook/types.go'),
      source('internal/runtime/webhook/mapper.go'),
      source('internal/runtime/webhook/sender.go'),
      source('internal/runtime/webhook/runtime.go'),
      source('internal/app/config.go'),
      source('internal/runtime/channelappend/offline.go'),
      source('internal/usecase/presence/app.go'),
    ]);
    expect([...types.matchAll(/Event[A-Za-z]+\s*=\s*"([^"]+)"/gu)].map((match) => match[1])).toEqual(
      webhookEventContracts.map((event) => event.name),
    );
    expect(mapper).toContain('json.Marshal(out)');
    expect(mapper).toContain('json.Marshal(resp)');
    expect(mapper).toContain('json.Marshal(values)');
    expect(mapper).toContain('json:"payload"');
    expect(mapper).toContain('json:"message_idstr"');
    expect(mapper).toContain('json:"to_uids,omitempty"');
    expect(mapper).toContain('json:"compress_to_uids,omitempty"');
    expect(mapper).toContain('resp.Compress = "gzip"');
    expect(sender).toContain('query.Set("event", req.Event)');
    expect(sender).toContain('query := target.Query()');
    expect(sender).toContain('httpReq.Header.Set("Content-Type", "application/json")');
    expect([...sender.matchAll(/httpReq\.Header\.Set\("([^"]+)"/gu)].map((match) => match[1])).toEqual([
      'Content-Type',
    ]);
    expect(sender).toContain('io.Copy(io.Discard, resp.Body)');
    expect(sender).toContain('resp.StatusCode != http.StatusOK');
    const retry = functionBody(runtime, 'func (r *Runtime) sendWithRetry(', 'func (r *Runtime) enabled(');
    expect(retry).toContain('for attempt := 1; attempt <= attempts; attempt++');
    expect(retry).not.toContain('time.Sleep');
    for (const value of [
      'cfg.QueueSize = 1024',
      'cfg.Workers = 16',
      'cfg.NotifyBatchMaxItems = 100',
      'cfg.NotifyBatchMaxWait = 500 * time.Millisecond',
      'cfg.OnlineBatchMaxItems = 512',
      'cfg.OnlineBatchMaxWait = 2 * time.Second',
      'cfg.OfflineUIDBatchSize = 512',
      'cfg.RequestTimeout = 5 * time.Second',
      'cfg.RetryMaxAttempts = 3',
    ]) {
      expect(config).toContain(value);
    }
    expect(runtime.replaceAll(/\s+/gu, '')).toContain('iflen(r.focus)==0{returntrue}');
    expect(mapper).toContain('len(message.ToUIDs) >= compressThreshold');
    expect(offline).toContain('event.MessageSeq > 0 && !event.SyncOnce && len(event.MessageScopedUIDs) == 0');
    expect(presence).toContain('fmt.Sprintf("%s-%d-%d-%d-%d-%d"');
  });

  test('publishes concise bilingual pages without pretending protocols are Product HTTP', async () => {
    const stems = [
      'api/client-protocols/tcp-binary',
      'api/client-protocols/json-rpc',
      'api/client-protocols/encryption',
      'api/webhooks/index',
      'api/webhooks/events',
      'api/webhooks/payloads',
      'api/webhooks/reliability-and-security',
    ] as const;

    for (const stem of stems) {
      const [zh, en] = await Promise.all([page(`${stem}.mdx`), page(`${stem}.en.mdx`)]);
      expect(zh).not.toBe(en);
      expect(zh.split('\n').length).toBeLessThan(180);
      expect(en.split('\n').length).toBeLessThan(180);
      expect(zh).not.toContain('<APIPage');
      expect(en).not.toContain('<APIPage');
    }

    const [tcp, jsonrpc, encryption, events, payloads, reliability, guideZh, guideEn] = await Promise.all([
      page('api/client-protocols/tcp-binary.en.mdx'),
      page('api/client-protocols/json-rpc.en.mdx'),
      page('api/client-protocols/encryption.en.mdx'),
      page('api/webhooks/events.en.mdx'),
      page('api/webhooks/payloads.en.mdx'),
      page('api/webhooks/reliability-and-security.en.mdx'),
      page('guide/integration/webhooks.mdx'),
      page('guide/integration/webhooks.en.mdx'),
    ]);
    expect(tcp).toContain('CONNECT');
    expect(tcp).toContain('device_flag');
    expect(tcp).toContain('bytes-rest');
    expect(tcp).toContain('WebSocket v13');
    expect(tcp).toContain('Client frames must be masked');
    expect(tcp).toContain('WebSocket control `PING/PONG`');
    expect(jsonrpc).toContain('EasySDK core path supported');
    expect(jsonrpc).toContain('CONNECT-first');
    expect(encryption).toContain('AES-128-CBC');
    expect(encryption).toContain('does not replace TLS');
    for (const event of webhookEventContracts) expect(events).toContain(event.name);
    expect(payloads).toContain('message_idstr');
    expect(payloads).toContain('compress_to_uids');
    expect(reliability).toContain('Only HTTP `200`');
    expect(reliability).toContain('no signature');
    expect(guideZh).toContain('UID Owner 当前节点');
    expect(guideZh).toContain('从右侧取最后五个数字段');
    expect(guideEn).toContain("UID owner's current node only");
    expect(guideEn).toContain('final five numeric fields from the right');
  });
});
