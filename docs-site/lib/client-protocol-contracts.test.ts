import { describe, expect, test } from 'bun:test';
import {
  clientProtocolFrames,
  clientProtocolLimits,
  renderClientProtocolPacketMarkdown,
} from './client-protocol-contracts';

function parseFrameTypes(source: string): Array<{ name: string; value: number }> {
  const block = source.match(/type FrameType uint8[\s\S]*?const \(([\s\S]*?)\n\)/u)?.[1];
  if (!block) throw new Error('Go FrameType const block is missing');

  const declarations = block
    .split('\n')
    .map((line) => line.replace(/\/\/.*$/u, '').trim())
    .filter(Boolean);

  return declarations.map((declaration, value) => {
    const match = declaration.match(/^([A-Z]+)(?:\s+FrameType)?(?:\s*=\s*(.+))?$/u);
    if (!match) throw new Error(`unsupported Go FrameType declaration: ${declaration}`);
    const [, name, expression] = match;
    if (value === 0 && expression?.replace(/\s+/gu, '') !== 'iota') {
      throw new Error('Go FrameType authority must begin with UNKNOWN = iota');
    }
    if (value > 0 && expression !== undefined) {
      throw new Error(`Go FrameType values must remain contiguous: ${name}`);
    }
    return { name, value };
  });
}

function parseIntegerExpression(source: string, name: string): number {
  const expression = source.match(new RegExp(`${name}\\s+(?:uint32\\s*)?=\\s*([^\\n]+)`))?.[1]
    ?.replace(/\/\/.*$/u, '')
    .trim();
  if (!expression) {
    throw new Error(`unsupported Go integer expression for ${name}: ${expression ?? 'missing'}`);
  }
  const product = expression.match(/^(\d+)\s*\*\s*(\d+)$/u);
  if (product) return Number(product[1]) * Number(product[2]);
  const shiftMinus = expression.match(/^(\d+)\s*<<\s*(\d+)\s*-\s*(\d+)$/u);
  if (shiftMinus) {
    return Number(shiftMinus[1]) * 2 ** Number(shiftMinus[2]) - Number(shiftMinus[3]);
  }
  if (/^\d+$/u.test(expression)) return Number(expression);
  throw new Error(`unsupported Go integer expression for ${name}: ${expression}`);
}

function parseDecodeMapTypes(source: string): string[] {
  const block = source.match(/var packetDecodeMap[\s\S]*?= map\[frame\.FrameType\]PacketDecodeFunc\{([\s\S]*?)\n\}/u)?.[1];
  if (!block) throw new Error('Go packetDecodeMap is missing');
  return [...block.matchAll(/frame\.([A-Z]+)\s*:/gu)].map((match) => match[1]);
}

function parseEncodeSwitchTypes(source: string): string[] {
  const method = source.match(/func \(l \*WKProto\) encodeFrameWithWriter[\s\S]*?\n\}\n\nfunc encodedFrameSize/u)?.[0];
  if (!method) throw new Error('Go encodeFrameWithWriter is missing');
  return [...method.matchAll(/case frame\.([A-Z]+):/gu)].map((match) => match[1]);
}

describe('client protocol contracts', () => {
  test('matches every current Go FrameType name and value', async () => {
    const source = await Bun.file(
      new URL('../../pkg/protocol/frame/common.go', import.meta.url),
    ).text();

    expect(clientProtocolFrames.map(({ name, value }) => ({ name, value }))).toEqual(
      parseFrameTypes(source),
    );
  });

  test('separates the public core from codec-only and reserved frames', () => {
    expect(
      clientProtocolFrames.filter((item) => item.scope === 'public-core').map((item) => item.name),
    ).toEqual(['CONNECT', 'CONNACK', 'SEND', 'SENDACK', 'RECV', 'RECVACK', 'PING', 'PONG']);
    expect(
      clientProtocolFrames.filter((item) => item.scope === 'codec-only').map((item) => item.name),
    ).toEqual(['DISCONNECT', 'SUB', 'SUBACK']);
    expect(
      clientProtocolFrames.filter((item) => item.scope === 'reserved').map((item) => item.name),
    ).toEqual(['UNKNOWN', 'EVENT']);
  });

  test('calibrates codec-only and reserved scopes against Go codec and terminal authorities', async () => {
    const codec = await Bun.file(
      new URL('../../pkg/protocol/codec/protocol.go', import.meta.url),
    ).text();
    const frameTypes = await Bun.file(
      new URL('../../pkg/protocol/frame/common.go', import.meta.url),
    ).text();
    const terminalFence = await Bun.file(
      new URL('../../pkg/protocol/frame/terminal_fence.go', import.meta.url),
    ).text();
    const handler = await Bun.file(
      new URL('../../internal/access/gateway/handler.go', import.meta.url),
    ).text();

    const decoded = new Set(parseDecodeMapTypes(codec));
    const encoded = new Set(parseEncodeSwitchTypes(codec));
    const codecOnly = clientProtocolFrames.filter((item) => item.scope === 'codec-only');

    expect(codecOnly.every((item) => decoded.has(item.name) && encoded.has(item.name))).toBeTrue();
    expect(frameTypes).toMatch(/UNKNOWN\s+FrameType\s*=\s*iota\s*\/\/\s*\u4fdd\u7559\u4f4d/u);
    expect(terminalFence).toContain(
      'TerminalFenceEventType is the reserved bench-only client request type.',
    );
    expect(handler).toMatch(/pkt == nil \|\| pkt\.Type != frame\.TerminalFenceEventType/u);
    expect(clientProtocolFrames.find((item) => item.name === 'EVENT')?.scope).toBe('reserved');
  });

  test('keeps DISCONNECT direction separate from its unpublished product scope', async () => {
    const [client, jsonrpc] = await Promise.all([
      Bun.file(new URL('../../pkg/client/reader.go', import.meta.url)).text(),
      Bun.file(new URL('../../pkg/protocol/jsonrpc/codec.go', import.meta.url)).text(),
    ]);
    const disconnect = clientProtocolFrames.find((item) => item.name === 'DISCONNECT');

    expect(client).toContain('case *frame.DisconnectPacket:');
    expect(jsonrpc).toContain('case DisconnectRequest:');
    expect(jsonrpc).toContain('case frame.DISCONNECT:');
    expect(disconnect?.direction).toBe('bidirectional');
    expect(disconnect?.scope).toBe('codec-only');
  });

  test('aligns fail-closed publication rules with decoder and Gateway defaults', async () => {
    const [codec, server, handler, page] = await Promise.all([
      Bun.file(new URL('../../pkg/protocol/codec/protocol.go', import.meta.url)).text(),
      Bun.file(new URL('../../pkg/gateway/core/server.go', import.meta.url)).text(),
      Bun.file(new URL('../../internal/access/gateway/handler.go', import.meta.url)).text(),
      Bun.file(
        new URL('../content/docs/api/client-protocols/packet-types.en.mdx', import.meta.url),
      ).text(),
    ]);

    expect(codec).toMatch(/if decodeFunc == nil \{[\s\S]*?return nil, 0, errors\.New/u);
    expect(server).toMatch(
      /listener\.adapter\.Decode\(state\.session, data\)[\s\S]*?CloseReasonProtocolError/u,
    );
    expect(server).toMatch(/CloseOnHandlerError[\s\S]*?state\.close\(reason, err\)/u);
    expect(handler).toContain('return ErrUnsupportedFrame');
    expect(page).toContain('UNKNOWN=0` never reaches product handling');
    expect(page).toContain('fail closed through');
    for (const dictionary of [
      'message-flags',
      'device-flags',
      'channel-types',
      'reason-codes',
    ]) {
      expect(page).toContain(`/en/api/dictionaries/${dictionary}`);
    }
  });

  test('matches current codec, version, and session limits', async () => {
    const codec = await Bun.file(
      new URL('../../pkg/protocol/codec/common.go', import.meta.url),
    ).text();
    const frames = await Bun.file(
      new URL('../../pkg/protocol/frame/common.go', import.meta.url),
    ).text();
    const session = await Bun.file(
      new URL('../../pkg/gateway/types/options.go', import.meta.url),
    ).text();

    const legacyVersion = Number(
      frames.match(/LegacyMessageSeqVersion\s*=\s*(\d+)/u)?.[1],
    );
    const latestVersion = Number(frames.match(/MessageSeqU64Version\s*=\s*(\d+)/u)?.[1]);
    const idleMinutes = Number(session.match(/IdleTimeout:\s*(\d+)\s*\*\s*time\.Minute/u)?.[1]);
    const clientSeqBytes = Number(frames.match(/ClientSeqByteSize\s*=\s*(\d+)/u)?.[1]);
    const legacyMessageSeqBytes = Number(
      frames.match(/MessageSeqLegacyByteSize\s*=\s*(\d+)/u)?.[1],
    );
    const latestMessageSeqBytes = Number(
      frames.match(/MessageSeqU64ByteSize\s*=\s*(\d+)/u)?.[1],
    );

    expect(Number(clientProtocolLimits.latestVersion)).toBe(latestVersion);
    expect(Number(clientProtocolLimits.legacyMessageSeqVersion)).toBe(legacyVersion);
    expect(Number(clientProtocolLimits.legacyMessageSeqBits)).toBe(legacyMessageSeqBytes * 8);
    expect(Number(clientProtocolLimits.latestMessageSeqBits)).toBe(latestMessageSeqBytes * 8);
    expect(Number(clientProtocolLimits.clientSeqWireBits)).toBe(clientSeqBytes * 8);
    expect(clientProtocolLimits.maxRemainingLengthBytes).toBe(
      parseIntegerExpression(codec, 'MaxRemaingLength'),
    );
    expect(clientProtocolLimits.maxEncodedSendPayloadBytes).toBe(
      parseIntegerExpression(codec, 'PayloadMaxSize'),
    );
    expect(clientProtocolLimits.defaultReadIdleTimeoutSeconds).toBe(idleMinutes * 60);
  });

  test('matches the authenticated product ingress frame switch', async () => {
    const handler = await Bun.file(
      new URL('../../internal/access/gateway/handler.go', import.meta.url),
    ).text();
    const onFrame = handler.match(/func \(h \*Handler\) OnFrame[\s\S]*?\n\}/u)?.[0];
    if (!onFrame) throw new Error('gateway Handler.OnFrame is missing');

    const inboundTypes = [...onFrame.matchAll(/case \*frame\.([A-Za-z]+)Packet:/gu)].map(
      (match) => match[1].toUpperCase(),
    );
    expect(inboundTypes).toEqual(['PING', 'SEND', 'RECVACK', 'EVENT']);
  });

  test('keeps connection lifecycle claims aligned with Gateway authentication', async () => {
    const [server, auth, wiring, lifecycleZh, lifecycleEn] = await Promise.all([
      Bun.file(new URL('../../pkg/gateway/core/server.go', import.meta.url)).text(),
      Bun.file(new URL('../../pkg/gateway/auth.go', import.meta.url)).text(),
      Bun.file(new URL('../../internal/app/wiring.go', import.meta.url)).text(),
      Bun.file(
        new URL('../content/docs/api/client-protocols/connection-lifecycle.mdx', import.meta.url),
      ).text(),
      Bun.file(
        new URL('../content/docs/api/client-protocols/connection-lifecycle.en.mdx', import.meta.url),
      ).text(),
    ]);
    const authGate = server.match(
      /func \(s \*Server\) handleAuthFrame[\s\S]*?\n\}/u,
    )?.[0];
    const authTask = server.match(/func \(s \*Server\) runAuthTask[\s\S]*?\n\}/u)?.[0];
    if (!authGate || !authTask) throw new Error('Gateway authentication flow is missing');

    expect(authGate).toContain('if state.isAuthPending()');
    expect(authGate).toContain('connect, ok := f.(*frame.ConnectPacket)');
    expect(authGate).toContain('if hasBatchTail');
    expect(authTask).toMatch(
      /OnSessionActivate[\s\S]*beginAuthenticatedOpen[\s\S]*writeImmediateFrame\(state, connack\)/u,
    );
    expect(authTask).toContain('rollbackActivatedSession');
    expect(server.match(/\.touchReadActivity\(\)/gu)).toHaveLength(2);

    expect(auth).toContain('encryptionEnabled := true');
    expect(auth).toContain('ReasonClientKeyIsEmpty');
    expect(auth).toMatch(
      /serverVersion == 0 \|\| serverVersion > frame\.LatestVersion/u,
    );
    expect(auth).toContain('connack.HasServerVersion = connect.Version > 3');
    expect(auth).toContain('if opts.TokenAuthOn && !isVisitor');
    expect(wiring).toContain(
      'gateway.NewWKProtoAuthenticator(gateway.WKProtoAuthOptions{NodeID: nodeID})',
    );
    for (const lifecycle of [lifecycleZh, lifecycleEn]) {
      for (const reason of [
        'ReasonAuthFail',
        'ReasonBan',
        'ReasonClientKeyIsEmpty',
        'ReasonProtocolUpgradeRequired',
        'ReasonRateLimit',
        'ReasonSystemError',
      ]) {
        expect(lifecycle).toContain(reason);
      }
      expect(lifecycle).toContain('fail closed');
      expect(lifecycle).toContain('Product HTTP');
    }
  });

  test('renders a bilingual LLM-friendly packet table', () => {
    const zh = renderClientProtocolPacketMarkdown('zh');
    const en = renderClientProtocolPacketMarkdown('en');

    expect(zh).toContain('| 0 | `UNKNOWN` | — | 保留 |');
    expect(zh).toContain('| 9 | `DISCONNECT` | 客户端 ↔ 服务端 | 仅编解码 |');
    expect(zh).toContain('| 12 | `EVENT` | 客户端 ↔ 服务端 | 保留 |');
    expect(en).toContain('| 1 | `CONNECT` | Client → Server | Public core |');
    expect(en).toContain('Protocol v6 uses a 64-bit `message_seq`');
  });
});
