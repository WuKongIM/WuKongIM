// Failure-first process/browser acceptance. See failure-inventory.md.
// Recipient evidence comes from real wsmux frames, never local ACK animation.
import assert from 'node:assert/strict';
import { spawn, execFileSync } from 'node:child_process';
import { createServer } from 'node:net';
import { createServer as createHTTPServer, request as httpRequest } from 'node:http';
import { once } from 'node:events';
import { mkdir, mkdtemp, writeFile, readFile, access } from 'node:fs/promises';
import { createHash, randomUUID } from 'node:crypto';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';

const root = fileURLToPath(new URL('../../../', import.meta.url));
const playwrightPath = process.env.WK_DEMO_PLAYWRIGHT || join(root, 'tmp/live-demo-playwright/node_modules/playwright');
const require = createRequire(import.meta.url), { chromium } = require(playwrightPath);
const playwrightVersion = require(join(dirname(require.resolve(playwrightPath)), 'package.json')).version;
const artifactsRoot = join(root, 'tmp/live-demo-acceptance');
await mkdir(artifactsRoot, { recursive: true });
const evidence = process.env.WK_LIVE_DEMO_REPORT_DIR || await mkdtemp(join(artifactsRoot, 'run-'));
await mkdir(evidence, { recursive: true });
const children = [], logs = {}, checks = [], browserErrors = [], incoming = [], sent = [], responses = [], bootstraps = new Map();
const fixtureClients = [], fixtureResponses = [], relayObservations = [], httpObservations = [], historyRequests = [];
const fault = { denylist: false, publication: false, sdkAck: false, sdkUid: '', sdkCut: false };
let browser, page, relay, runMetadata, phase = 'setup';
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const sha256 = bytes => createHash('sha256').update(bytes).digest('hex');
const testid = (p, name) => p.getByTestId(name);
const check = (name, value) => { assert(value, name); checks.push(name); };
async function until(condition, label, timeout = 20000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) { if (await condition()) return; await pause(80); }
  throw Error('Timed out: ' + label);
}
async function reservePort() {
  const server = createServer(); server.listen(0, '127.0.0.1'); await once(server, 'listening');
  const value = server.address().port; await new Promise(resolve => server.close(resolve)); return value;
}
function start(name, command, args, env, cwd = root) {
  const child = spawn(command, args, { cwd, env, stdio: ['ignore', 'pipe', 'pipe'] });
  children.push(child); logs[name] = '';
  for (const stream of [child.stdout, child.stderr]) stream.on('data', bytes => { logs[name] = (logs[name] + bytes).slice(-262144); });
  child.on('error', error => { logs[name] += error.message; });
  return child;
}
async function ready(url, child) {
  await until(async () => {
    if (child.exitCode !== null) throw Error('Owned service exited before readiness: ' + url);
    try { return (await fetch(url, { signal: AbortSignal.timeout(1000) })).ok; } catch { return false; }
  }, 'service readiness ' + url, 45000);
}
function decodePayload(value) {
  if (typeof value !== 'string') return value;
  try { return JSON.parse(Buffer.from(value, 'base64').toString('utf8')); } catch { return null; }
}
function wireMessageID(raw) { return raw.match(/"(?:messageId|message_id)"\s*:\s*"?(-?\d+)/)?.[1] || ''; }
function observe(p, label) {
  p.setDefaultTimeout(20000);
  p.on('pageerror', error => browserErrors.push({ label, error: error.message.slice(0, 1000) }));
  p.on('request', request => { if (/\/(channel\/messagesync|message\/eventsync|conversation\/)/.test(request.url())) historyRequests.push({ label, path: new URL(request.url()).pathname }); });
  p.on('response', async response => {
    if (!/\/livedemo\/api\/(rooms|join|resume)$/.test(response.url()) || !response.ok()) return;
    try { const data = await response.json(); if (data.viewer?.uid) bootstraps.set(p, data); } catch {}
  });
  p.on('websocket', socket => {
    const requests = new Map();
    socket.on('framesent', frame => {
      let packet; try { packet = JSON.parse(frame.payload.toString()); } catch { return; }
      if (packet.method !== 'send') return;
      const payload = decodePayload(packet.params?.payload), event = payload?.live_demo;
      if (!event) return;
      requests.set(packet.id, event.eventId);
      sent.push({ label, eventId: event.eventId, kind: event.kind, roomId: event.roomId, content: payload.content, header: packet.params.header });
    });
    socket.on('framereceived', frame => {
      let packet; try { packet = JSON.parse(frame.payload.toString()); } catch { return; }
      if (packet.method === 'recv') {
        const params = packet.params || {}, payload = decodePayload(params.payload), event = payload?.live_demo;
        if (!event) return;
        incoming.push({ label, fromUid: params.fromUid ?? params.from_uid, channelId: params.channelId ?? params.channel_id, channelType: params.channelType ?? params.channel_type, messageId: wireMessageID(frame.payload.toString()), messageSeq: params.messageSeq ?? params.message_seq, header: params.header, content: payload.content, event });
        if (incoming.length > 2000) incoming.shift();
      } else if (requests.has(packet.id)) responses.push({ label, eventId: requests.get(packet.id), result: packet.result, error: packet.error });
    });
  });
}
// This relay forwards every request to the real product. A fault consumes the
// real response before dropping it, so pending state follows an actual mutation.
async function productRelay(api) {
  const sockets = new Set();
  const server = createHTTPServer(async (request, response) => {
    try {
      const chunks = []; for await (const chunk of request) chunks.push(chunk);
      const body = Buffer.concat(chunks), headers = { ...request.headers };
      delete headers.host; delete headers.connection; delete headers['transfer-encoding']; delete headers['content-length'];
      const upstream = await fetch(api + request.url, { method: request.method, headers, body: ['GET', 'HEAD'].includes(request.method) ? undefined : body, signal: AbortSignal.timeout(10000) });
      const bytes = Buffer.from(await upstream.arrayBuffer());
      let input; try { input = JSON.parse(body.toString()); } catch {}
      const event = decodePayload(input?.payload)?.live_demo;
      const dropped = (fault.denylist && /\/channel\/blacklist_(add|remove)$/.test(request.url)) || (fault.publication && request.url === '/message/send' && event?.kind === 'room_state');
      if (dropped) {
        let result; try { result = JSON.parse(bytes.toString()); } catch {}
        relayObservations.push({ path: request.url, upstreamStatus: upstream.status, status: result?.status, reason: result?.reason, roomVersion: event?.roomVersion, eventId: event?.eventId, dropped: true });
        response.destroy(); return;
      }
      response.writeHead(upstream.status, Object.fromEntries([...upstream.headers].filter(([key]) => !['transfer-encoding', 'content-encoding', 'content-length', 'connection'].includes(key))));
      response.end(bytes);
    } catch { if (!response.destroyed) { response.writeHead(502); response.end('Real product request failed'); } }
  });
  server.on('connection', socket => { sockets.add(socket); socket.on('close', () => sockets.delete(socket)); });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  return { url: `http://127.0.0.1:${server.address().port}`, async close() { for (const socket of sockets) socket.destroy(); await new Promise(resolve => server.close(resolve)); } };
}
// Raw protocol clients are only fault/burst sources. Both primary viewers run
// the released browser SDK and their actual incoming frames prove fanout.
async function realFixture(session, label) {
  const socket = new WebSocket(session.wsUrl), pending = new Map(), received = [];
  const request = (method, params) => new Promise((resolve, reject) => {
    const id = randomUUID(), timer = setTimeout(() => { pending.delete(id); reject(Error(label + ' gateway request deadline')); }, 10000);
    pending.set(id, { resolve, reject, timer, method, channelId: params.channelId, eventId: params.clientMsgNo }); socket.send(JSON.stringify({ method, params, id }));
  });
  socket.addEventListener('message', event => {
    let packet; try { packet = JSON.parse(String(event.data)); } catch { return; }
    const waiter = pending.get(packet.id);
    if (waiter) {
      clearTimeout(waiter.timer); pending.delete(packet.id);
      if (waiter.method === 'send') fixtureResponses.push({ label, fromUid: session.viewer.uid, channelId: waiter.channelId, eventId: waiter.eventId, reasonCode: packet.error?.code ?? packet.result?.reasonCode, error: Boolean(packet.error), messageId: wireMessageID(String(event.data)) });
      if (packet.error && waiter.method === 'send') waiter.resolve({ rpcError: { code: packet.error.code } });
      else if (packet.error) waiter.reject(Error(label + ' gateway rejected request with reason ' + packet.error.code));
      else waiter.resolve(packet.result);
    }
    if (packet.method === 'recv') {
      const p = packet.params, payload = decodePayload(p.payload);
      received.push({ fromUid: p.fromUid, channelId: p.channelId, payload });
      if (received.length > 200) received.shift();
      if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ method: 'recvack', params: { header: p.header, messageId: p.messageId, messageSeq: p.messageSeq } }));
    }
  });
  socket.addEventListener('error', () => {});
  socket.addEventListener('close', () => { for (const waiter of pending.values()) { clearTimeout(waiter.timer); waiter.reject(Error(label + ' gateway connection closed')); } pending.clear(); });
  await Promise.race([once(socket, 'open'), pause(10000).then(() => { throw Error(label + ' real WebSocket handshake deadline'); })]);
  await request('connect', { uid: session.viewer.uid, token: session.viewer.token, deviceId: 'live-fixture-' + randomUUID(), deviceFlag: 1, clientTimestamp: Date.now() });
  const client = {
    received,
    send(payload, roomId = session.roomId) { return request('send', { channelId: roomId, channelType: 2, payload: Buffer.from(JSON.stringify(payload)).toString('base64'), clientMsgNo: payload.live_demo?.eventId || randomUUID(), header: { noPersist: true, syncOnce: false, redDot: true } }); },
    async close() { if (socket.readyState === WebSocket.CLOSED) return; const closed = once(socket, 'close'); socket.close(1000); await Promise.race([closed, pause(1000)]); }
  };
  fixtureClients.push(client); return client;
}
async function enter(p) {
  await until(async () => bootstraps.has(p) || await testid(p, 'start').isEnabled().catch(() => false), 'page bootstrap action');
  if (!bootstraps.has(p) && await testid(p, 'start').isEnabled()) await testid(p, 'start').click();
  await until(() => bootstraps.has(p), 'page bootstrap response');
}
async function screenshot(p, name) { await p.screenshot({ path: join(evidence, name + '.png'), fullPage: true }); }
async function metadata() {
  const digests = {};
  for (const path of ['AGENTS.md', 'test/e2e/AGENTS.md', 'internal/access/api/FLOW.md', 'demo/livedemo/test/failure-inventory.md', 'demo/livedemo/test/live.integration.mjs', 'demo/livedemo/server.mjs', 'demo/livedemo/src/main.ts', 'demo/livedemo/src/style.css', 'demo/livedemo/src/contracts.ts', 'demo/livedemo/src/stage.ts', 'demo/livedemo/package.json', 'demo/livedemo/package-lock.json', 'internal/access/api/demoui/livedist/index.html']) {
    try { digests[path] = sha256(await readFile(join(root, path))); } catch { digests[path] = null; }
  }
  try {
    const index = await readFile(join(root, 'internal/access/api/demoui/livedist/index.html'), 'utf8');
    for (const match of index.matchAll(/(?:src|href)="\/livedemo\/([^"\s]+\.(?:js|css))"/g)) {
      const path = 'internal/access/api/demoui/livedist/' + match[1]; digests[path] = sha256(await readFile(join(root, path)));
    }
  } catch {}
  let frozenInstructions; try { frozenInstructions = JSON.parse(await readFile(join(root, 'tmp/live-demo-evidence/test-frozen.json'), 'utf8')); } catch {}
  return { revision: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim(), worktreeStatus: execFileSync('git', ['status', '--short'], { cwd: root, encoding: 'utf8' }), binarySha256: process.env.WK_DEMO_SERVER_BIN ? sha256(await readFile(process.env.WK_DEMO_SERVER_BIN)) : null, sourceDigests: digests, frozenInstructions, hashSlots: 256, topology: 'single-node cluster', sdk: 'easyjssdk@2.0.5', playwright: playwrightVersion };
}
try {
  assert(process.env.WK_DEMO_SERVER_BIN, 'Supply WK_DEMO_SERVER_BIN built from this worktree');
  assert.equal(playwrightVersion, '1.62.1', 'Use the pinned Playwright 1.62.1');
  runMetadata = await metadata();
  const [httpPort, raftPort, wsPort, demoPort] = await Promise.all(Array.from({ length: 4 }, reservePort));
  const api = `http://127.0.0.1:${httpPort}`, base = `http://127.0.0.1:${demoPort}`;
  const cleanEnv = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('WK_')));
  const config = `[node]\nid=1\ndata_dir=${JSON.stringify(join(evidence, 'data'))}\n[cluster]\nid="live-demo-acceptance"\nlisten_addr="127.0.0.1:${raftPort}"\nnodes=[{id=1,addr="127.0.0.1:${raftPort}"}]\ninitial_slot_count=8\nhash_slot_count=256\nslot_replica_n=1\n[api]\nlisten_addr="127.0.0.1:${httpPort}"\nexternal_ws_addr="ws://127.0.0.1:${wsPort}"\n[manager]\nlisten_addr="127.0.0.1:0"\n[gateway]\ntoken_auth_on=true\nlisteners=[{name="ws",network="websocket",address="127.0.0.1:${wsPort}",transport="gnet",protocol="wsmux"}]\n[plugin]\nenable=false\n[log]\nlevel="warn"\ndir=${JSON.stringify(join(evidence, 'app-logs'))}\n`;
  await writeFile(join(evidence, 'wukongim.toml'), config);
  const product = start('wukongim', process.env.WK_DEMO_SERVER_BIN, ['-config', join(evidence, 'wukongim.toml')], cleanEnv, evidence);
  await ready(api + '/readyz', product);
  phase = 'live-backend';
  await access(join(root, 'demo/livedemo/server.mjs'));
  relay = await productRelay(api);
  const backend = start('live-demo', process.execPath, ['server.mjs'], { ...cleanEnv, WK_DEMO_PORT: String(demoPort), WK_DEMO_API_URL: relay.url }, join(root, 'demo/livedemo'));
  await ready(base + '/livedemo/api/health', backend);
  const bff = async (path, body, key, extraHeaders = {}) => {
    const response = await fetch(base + '/livedemo/api/' + path, { method: body === undefined ? 'GET' : 'POST', headers: { 'content-type': 'application/json', ...(key ? { authorization: 'Bearer ' + key } : {}), ...extraHeaders }, ...(body === undefined ? {} : { body: typeof body === 'string' ? body : JSON.stringify(body) }), signal: AbortSignal.timeout(20000) });
    let data; try { data = await response.json(); } catch {}
    httpObservations.push({ path: path.split('?')[0], status: response.status, code: data?.code });
    return { status: response.status, data };
  };
  check('foreign_origin_rejected', (await bff('rooms', {}, null, { origin: 'https://foreign.invalid' })).status === 403);
  const foreignHostStatus = await new Promise((resolve, reject) => {
    const request = httpRequest(base + '/livedemo/api/rooms', { method: 'POST', headers: { host: 'foreign.invalid', 'content-type': 'application/json', 'content-length': '2' } }, response => { response.resume(); response.once('end', () => resolve(response.statusCode)); });
    request.on('error', reject); request.end('{}');
  });
  check('foreign_host_rejected', foreignHostStatus === 403);
  check('malformed_json_rejected', (await bff('rooms', '{')).status === 400);
  check('oversized_body_rejected', (await bff('rooms', JSON.stringify({ text: 'x'.repeat(5000) }))).status === 413);
  check('unsupported_method_rejected', (await bff('rooms')).status === 405);
  check('unicode_resume_capability_rejected_cleanly', [403, 404, 410].includes((await bff('resume', { roomId: 'livedemo-' + randomUUID(), viewerKey: '😈' })).status));
  phase = 'two-real-viewers';
  browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1024 } });
  await context.routeWebSocket(`ws://127.0.0.1:${wsPort}/**`, socket => {
    let uid, cutId;
    const upstream = socket.connectToServer();
    socket.onMessage(message => {
      let packet; try { packet = JSON.parse(String(message)); } catch {}
      if (packet?.method === 'connect') uid = packet.params.uid;
      if (fault.sdkAck && uid === fault.sdkUid && packet?.method === 'send') cutId = packet.id;
      upstream.send(message);
    });
    upstream.onMessage(message => {
      let packet; try { packet = JSON.parse(String(message)); } catch {}
      if (cutId && packet?.id === cutId) { fault.sdkCut = true; socket.close({ code: 4001, reason: 'Controlled lost real SEND response' }); upstream.close({ code: 4001 }); return; }
      socket.send(message);
    });
  });
  let pageNumber = 0;
  context.on('page', p => observe(p, 'viewer-' + (++pageNumber)));
  page = await context.newPage();
  await page.goto(base + '/livedemo/?home=' + encodeURIComponent(api + '/demos/'));
  await testid(page, 'start').click();
  await until(() => bootstraps.has(page), 'creator bootstrap');
  await until(() => testid(page, 'send').isEnabled(), 'creator SDK and snapshot ready');
  const popup = context.waitForEvent('page'); await testid(page, 'open-viewer').click();
  const other = await popup;
  await enter(other);
  await until(() => testid(other, 'send').isEnabled(), 'second viewer SDK and snapshot ready');
  const firstSession = bootstraps.get(page), secondSession = bootstraps.get(other);
  check('distinct_viewer_UIDs', firstSession.viewer.uid !== secondSession.viewer.uid);
  check('viewer_invitation_omits_host_credentials', !secondSession.ownerKey && !other.url().includes(firstSession.ownerKey) && !other.url().includes(firstSession.viewer.token));
  const content = '真实观众互通-' + randomUUID();
  await testid(page, 'send-input').fill(content); await testid(page, 'send').click();
  await until(() => incoming.some(item => item.label === 'viewer-2' && item.content === content && item.fromUid === firstSession.viewer.uid), 'actual incoming frame at other viewer');
  await testid(other, 'chat').getByText(content, { exact: true }).waitFor();
  const received = incoming.find(item => item.label === 'viewer-2' && item.content === content);
  check('recipient_frame_has_room_and_message_identity', received.channelId === firstSession.roomId && received.channelType === 2 && received.messageId !== '' && received.event.roomId === firstSession.roomId);
  check('direct_SDK_send_is_online_only', sent.some(item => item.content === content && item.header?.noPersist === true && item.header?.syncOnce === false));
  check('actual_send_ACK_and_recipient_are_separate', responses.some(item => item.eventId === received.event.eventId && (item.result?.reasonCode ?? item.result?.reason) === 1));
  check('one_rendered_row_per_remote_event', await testid(other, 'chat').getByText(content, { exact: true }).count() === 1);
  await screenshot(page, '01-creator'); await screenshot(other, '02-real-recipient');
  const roomId = firstSession.roomId, ownerKey = firstSession.ownerKey;
  const state = async () => (await bff('state?roomId=' + encodeURIComponent(roomId), undefined, ownerKey)).data;
  const control = body => bff('control', { roomId, requestId: randomUUID(), ...body }, ownerKey);
  const accepted = result => (result?.reasonCode ?? result?.reason) === 1;
  const denied = (result, reasons) => reasons.includes(result?.rpcError?.code);
  const payload = (kind, content, extra = {}) => ({ type: 1, content, live_demo: { kind, roomId, eventId: randomUUID(), ...extra } });

  phase = 'like-notice-and-authority';
  await page.bringToFront();
  await testid(other, 'like').click();
  await until(() => incoming.some(item => item.label === 'viewer-1' && item.fromUid === secondSession.viewer.uid && item.event.kind === 'like'), 'real other-viewer like reception');
  await testid(page, 'like-pulse').first().waitFor();
  check('likes_are_real_online_interactions', incoming.some(item => item.label === 'viewer-1' && item.event.kind === 'like' && item.messageSeq === 0));
  const notice = '现场公告-' + randomUUID();
  await testid(page, 'notice-text').fill(notice); await testid(page, 'notice-publish').click();
  await until(() => incoming.some(item => item.label === 'viewer-2' && item.fromUid === firstSession.hostUid && item.event.kind === 'room_state' && item.event.snapshot?.notice === notice), 'authorized real host announcement');
  await until(async () => (await testid(other, 'notice-current').textContent()).includes(notice), 'remote current notice');
  check('viewer_control_denied', [401, 403].includes((await bff('control', { roomId, requestId: randomUUID(), kind: 'notice', text: 'Unauthorized' }, secondSession.viewerKey)).status));
  check('anonymous_state_denied', [401, 403].includes((await bff('state?roomId=' + roomId)).status));
  check('unicode_join_capability_rejected_cleanly', (await bff('join', { roomId, invite: '😈' })).status === 403);
  const invalidResume = await bff('resume', { roomId, viewerKey: '😈' });
  check('unicode_live_resume_capability_rejected_cleanly', [403, 410].includes(invalidResume.status) && ['unauthorized', 'recovery_invalid'].includes(invalidResume.data.code));
  check('foreign_target_denied', [400, 403, 404].includes((await control({ kind: 'mute', targetUid: 'foreign-viewer-' + randomUUID(), muted: true })).status));
  const beforeMalformed = (await state()).snapshot;
  const joinedFixture = await bff('join', { roomId, invite: firstSession.invite });
  check('fixture_is_real_authorized_audience', joinedFixture.status === 200 && !joinedFixture.data.ownerKey);
  const fixture = await realFixture(joinedFixture.data, 'malformed/burst audience');
  const forged = payload('room_state', 'Viewer-forged announcement', { roomVersion: 999999, snapshot: { ...beforeMalformed, version: 999999, notice: 'FORGED HOST ANNOUNCEMENT' } });
  check('forged_state_is_sent_through_real_product', accepted(await fixture.send(forged)));
  const malformed = { type: 1, content: 'Missing event ID', live_demo: { kind: 'barrage', roomId } };
  check('malformed_payload_is_sent_through_real_product', accepted(await fixture.send(malformed)));
  await until(() => incoming.some(item => item.label === 'viewer-2' && item.content === forged.content) && incoming.some(item => item.label === 'viewer-2' && item.content === malformed.content), 'malformed and forged actual incoming frames');
  check('viewer_cannot_forge_host_state', !(await testid(other, 'notice-current').textContent()).includes('FORGED HOST ANNOUNCEMENT'));
  check('missing_event_ID_not_rendered', await testid(other, 'chat').getByText(malformed.content, { exact: true }).count() === 0);
  const unsafeText = '<img src=x onerror="window.__liveXSS=1">';
  await fixture.send(payload('barrage', unsafeText));
  await testid(other, 'chat').getByText(unsafeText, { exact: true }).waitFor();
  check('barrage_text_is_literal', await other.evaluate(() => window.__liveXSS === undefined));
  await screenshot(other, '03-real-notice-and-untrusted-payload');

  phase = 'room-mute-refresh-and-receive';
  await testid(page, 'viewer-select').selectOption(secondSession.viewer.uid); await testid(page, 'mute').click();
  await until(async () => (await state()).snapshot.viewers.find(v => v.uid === secondSession.viewer.uid)?.muted === true, 'confirmed room mute');
  await until(() => testid(other, 'send').isDisabled(), 'target UI muted');
  const victim = await realFixture(secondSession, 'muted audience');
  check('product_rejects_muted_barrage', denied(await victim.send(payload('barrage', 'Muted text must be rejected')), [4]));
  check('product_rejects_muted_like', denied(await victim.send(payload('like', 'Muted like must be rejected')), [4]));
  const deliveredWhileMuted = '仍可接收-' + randomUUID();
  await testid(page, 'send-input').fill(deliveredWhileMuted); await testid(page, 'send').click();
  await testid(other, 'chat').getByText(deliveredWhileMuted, { exact: true }).waitFor();
  await until(() => victim.received.some(item => item.payload?.content === deliveredWhileMuted), 'muted real device still receives');
  check('room_mute_preserves_actual_receive', true);
  bootstraps.delete(other); await other.reload(); await enter(other);
  await until(async () => /在线|已连接/.test(await testid(other, 'connection').textContent()), 'muted viewer reconnect completes');
  check('reload_preserves_viewer_identity', bootstraps.get(other).viewer.uid === secondSession.viewer.uid);
  check('reload_preserves_mute', await testid(other, 'send').isDisabled() && await testid(other, 'like').isDisabled());
  await screenshot(other, '04-mute-survives-refresh');
  await testid(page, 'unmute').click(); await until(() => testid(other, 'send').isEnabled(), 'unmute restores target');
  check('actual_product_permission_recovers', accepted(await victim.send(payload('barrage', 'Actual unmuted send'))));

  phase = 'actual-SDK-rejection-with-stale-business-state';
  const externalMute = await fetch(api + '/channel/blacklist_add', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ channel_id: roomId, channel_type: 2, uids: [secondSession.viewer.uid] }) });
  check('external_real_product_mute_applied', externalMute.status === 200 && (await externalMute.json()).status === 200);
  check('business_snapshot_intentionally_still_allows', (await state()).snapshot.viewers.find(v => v.uid === secondSession.viewer.uid)?.muted === false);
  const rejectedText = '真实SDK权限拒绝-' + randomUUID();
  const staleResync = other.waitForResponse(response => response.url().includes('/livedemo/api/state?') && response.status() === 200);
  await testid(other, 'send-input').fill(rejectedText); await testid(other, 'send').click();
  await until(() => responses.some(item => item.label === 'viewer-2' && item.error?.code === 4), 'actual SDK SEND error reason4');
  await staleResync;
  await until(async () => (await other.locator('body').textContent()).includes('拒绝'), 'explicit server denial UI');
  check('actual_denial_is_not_unknown_or_false_success', !(await other.locator('body').textContent()).includes('发送结果未知') && !incoming.some(item => item.content === rejectedText));
  check('stale_snapshot_cannot_reopen_denied_UI', await testid(other, 'send').isDisabled() && await testid(other, 'like').isDisabled());
  await control({ kind: 'mute', targetUid: secondSession.viewer.uid, muted: true });
  await until(async () => /已(?:被)?禁言/.test(await testid(other, 'permission').textContent()), 'denied viewer observes confirmed restriction');
  const observedRestriction = (await state()).snapshot.viewers.find(v => v.uid === secondSession.viewer.uid);
  check('previous_denial_converges_to_confirmed_mute', observedRestriction?.muted === true && observedRestriction.pending === false);
  await testid(other, 'disconnect').click();
  await control({ kind: 'mute', targetUid: secondSession.viewer.uid, muted: false });
  await testid(other, 'reconnect').click();
  await until(() => testid(other, 'send').isEnabled(), 'offline unmute restores current policy after previously observed denial');
  check('offline_unmute_clears_observed_denial_after_resume', await testid(other, 'like').isEnabled());

  phase = 'snapshot-race-and-live-only-reconnect';
  await testid(other, 'disconnect').click(); await until(() => testid(other, 'send').isDisabled(), 'viewer disconnected');
  const missed = '离线旧弹幕-' + randomUUID();
  await testid(page, 'send-input').fill(missed); await testid(page, 'send').click();
  let heldSnapshot, releaseSnapshot, hold = true;
  const released = new Promise(resolve => { releaseSnapshot = resolve; });
  await other.route('**/livedemo/api/state?**', async route => {
    if (!hold) { await route.continue(); return; }
    hold = false;
    const response = await route.fetch(); heldSnapshot = (await response.json()).snapshot;
    await released; await route.fulfill({ response });
  });
  await testid(other, 'reconnect').click(); await until(() => !!heldSnapshot, 'real old snapshot held');
  check('input_gated_during_state_restore', await testid(other, 'send').isDisabled());
  const newerNotice = '竞态中的新公告-' + randomUUID();
  const changed = await control({ kind: 'notice', text: newerNotice });
  check('newer_state_committed_during_snapshot_fetch', changed.status === 200 && changed.data.snapshot.version > heldSnapshot.version);
  await until(() => incoming.some(item => item.label === 'viewer-2' && item.event.snapshot?.notice === newerNotice), 'live newer state while snapshot held');
  releaseSnapshot();
  await until(() => testid(other, 'send').isEnabled(), 'snapshot and buffer merge completes');
  check('late_snapshot_cannot_roll_back_notice', (await testid(other, 'notice-current').textContent()).includes(newerNotice));
  check('reconnect_does_not_replay_offline_barrage', await testid(other, 'chat').getByText(missed, { exact: true }).count() === 0);
  check('no_history_or_conversation_polling', historyRequests.length === 0);
  await other.unroute('**/livedemo/api/state?**');

  phase = 'old-real-notification-cannot-evict-newcomer';
  fault.publication = true;
  const oldNotice = await control({ kind: 'notice', text: '新观众加入前的旧公告-' + randomUUID() });
  check('old_notification_retained_with_real_unknown_SEND', oldNotice.status === 200 && oldNotice.data.notification.status === 'pending');
  fault.publication = false;
  const newcomer = await context.newPage();
  await newcomer.goto(await testid(page, 'invite-link').getAttribute('href')); await enter(newcomer);
  await until(() => testid(newcomer, 'send').isEnabled(), 'new audience joined after retained old snapshot');
  const newestNotice = '新观众的当前公告-' + randomUUID();
  const newest = await control({ kind: 'notice', text: newestNotice });
  check('newer_notice_confirmed_after_newcomer_join', newest.status === 200 && newest.data.snapshot.version > oldNotice.data.snapshot.version);
  await until(async () => (await testid(newcomer, 'notice-current').textContent()).includes(newestNotice), 'newcomer sees current snapshot');
  const oldRetry = await bff('retry', { roomId, requestId: oldNotice.data.notification.requestId, phase: 'notification' }, ownerKey);
  check('retained_old_notification_real_retry_confirmed', oldRetry.status === 200 && oldRetry.data.notification.status === 'confirmed');
  await until(() => incoming.some(item => item.label === 'viewer-3' && item.event.eventId === oldNotice.data.notification.requestId), 'newcomer actually receives older roster that excludes it');
  await pause(200);
  check('old_roster_cannot_evict_new_viewer', await testid(newcomer, 'send').isEnabled() && (await testid(newcomer, 'notice-current').textContent()).includes(newestNotice));
  await screenshot(newcomer, '05-old-state-retry-newcomer');
  const newcomerUID = bootstraps.get(newcomer).viewer.uid;
  await testid(newcomer, 'leave').click();
  await until(async () => !(await state()).snapshot.viewers.some(v => v.uid === newcomerUID), 'newcomer explicit UI leave');
  await newcomer.close();

  phase = 'real-product-pending-control-and-publication';
  fault.denylist = true;
  const uncertainID = randomUUID();
  const uncertain = await bff('control', { roomId, requestId: uncertainID, kind: 'mute', targetUid: secondSession.viewer.uid, muted: true }, ownerKey);
  check('real_product_mutation_response_was_dropped', relayObservations.some(item => item.path === '/channel/blacklist_add' && item.upstreamStatus === 200 && item.dropped));
  check('uncertain_mute_does_not_claim_confirmed', uncertain.status === 200 && uncertain.data.operation.status === 'pending' && uncertain.data.snapshot.viewers.find(v => v.uid === secondSession.viewer.uid)?.muted === false && uncertain.data.snapshot.pending?.requestId === uncertainID);
  check('opposite_control_blocked_by_pending', (await control({ kind: 'mute', targetUid: secondSession.viewer.uid, muted: false })).status === 409);
  check('pending_target_cannot_leave_with_stale_snapshot_reference', (await bff('leave', { roomId }, secondSession.viewerKey)).status === 409);
  check('product_really_applied_lost_response_mute', denied(await victim.send(payload('barrage', 'Lost response mute is enforced')), [4]));
  fault.denylist = false;
  const resolved = await bff('retry', { roomId, requestId: uncertainID, phase: 'operation' }, ownerKey);
  check('immutable_control_retry_converges', resolved.status === 200 && resolved.data.operation.status === 'confirmed' && resolved.data.snapshot.viewers.find(v => v.uid === secondSession.viewer.uid)?.muted === true && !resolved.data.snapshot.pending);
  await control({ kind: 'mute', targetUid: secondSession.viewer.uid, muted: false });
  await until(() => testid(other, 'send').isEnabled(), 'target available after pending convergence');
  fault.publication = true;
  const lostNotice = '广播响应丢失-' + randomUUID(), published = await control({ kind: 'notice', text: lostNotice });
  check('real_notification_SEND_response_was_dropped', relayObservations.some(item => item.path === '/message/send' && item.reason === 1 && item.dropped));
  check('saved_state_separate_from_publication', published.status === 200 && published.data.snapshot.notice === lostNotice && published.data.notification.status === 'pending' && (await state()).snapshot.notice === lostNotice);
  const originalNotification = published.data.notification;
  await testid(page, 'disconnect').click(); await testid(page, 'reconnect').click();
  await until(() => testid(page, 'send').isEnabled(), 'owner reconnect restores authoritative notification metadata');
  check('reconnect_pending_notification_overrides_cached_confirmed_prose', /通知.*(?:待确认|未确认)/.test(await testid(page, 'control-status').textContent()));
  fault.publication = false;
  const recovered = await bff('retry', { roomId, requestId: originalNotification.requestId, phase: 'notification' }, ownerKey);
  check('notification_retry_confirms_real_SEND', recovered.status === 200 && recovered.data.notification.status === 'confirmed' && recovered.data.snapshot.version === published.data.snapshot.version);
  await until(async () => (await testid(other, 'notice-current').textContent()).includes(lostNotice), 'real notification is visible');
  await screenshot(page, '05-control-faults-converged');

  phase = 'lost-real-SDK-response';
  fault.sdkUid = firstSession.viewer.uid; fault.sdkAck = true;
  const unknownText = '真实ACK丢失-' + randomUUID();
  await testid(page, 'send-input').fill(unknownText); await testid(page, 'send').click();
  await until(() => fault.sdkCut, 'real SEND reply deliberately lost');
  await testid(other, 'chat').getByText(unknownText, { exact: true }).waitFor();
  await until(async () => (await page.locator('body').textContent()).includes('未知'), 'truthful unknown send outcome');
  const unknownEvents = sent.filter(item => item.content === unknownText);
  check('unknown_send_not_automatically_retried', unknownEvents.length === 1);
  fault.sdkAck = false;
  if (await testid(page, 'reconnect').isEnabled()) await testid(page, 'reconnect').click();
  await until(() => testid(page, 'retry-send').isEnabled(), 'manual immutable retry available');
  await testid(page, 'retry-send').click();
  await until(() => sent.filter(item => item.content === unknownText).length === 2, 'manual retry uses real network');
  const retried = sent.filter(item => item.content === unknownText);
  check('unknown_retry_keeps_original_event_ID', retried[0].eventId === retried[1].eventId);
  check('unknown_retry_does_not_duplicate_remote_row', await testid(other, 'chat').getByText(unknownText, { exact: true }).count() === 1);

  phase = 'bounds-mobile-and-independent-room';
  await other.bringToFront();
  for (let index = 0; index < 112; index++) {
    check('burst_send_' + index, accepted(await fixture.send(payload('barrage', '真实突发消息-' + index))));
  }
  await until(() => incoming.some(item => item.label === 'viewer-2' && item.content === '真实突发消息-111'), 'bounded burst reaches real audience');
  const chatRows = await testid(other, 'chat-row').count(), animationRows = await testid(other, 'barrage-item').count();
  check('chat_DOM_bounded', chatRows >= 90 && chatRows <= 100);
  check('stage_animation_DOM_bounded', animationRows > 0 && animationRows <= 12);
  const queued = await other.locator('#live-visuals').getAttribute('data-queue-size');
  check('animation_queue_bounded', /^\d+$/.test(queued || '') && Number(queued) <= 30);
  await testid(other, 'barrage-toggle').click(); await testid(other, 'barrage-toggle').click();
  for (let index = 0; index < 8; index++) await fixture.send(payload('like', '真实点赞突发-' + index));
  await testid(other, 'like-pulse').first().waitFor();
  check('like_pulse_DOM_bounded', await testid(other, 'like-pulse').count() <= 4);
  await other.emulateMedia({ reducedMotion: 'reduce' }); await other.setViewportSize({ width: 390, height: 844 });
  check('mobile_has_no_horizontal_overflow', await other.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1));
  check('mobile_composer_visible', await testid(other, 'send-input').isVisible());
  await screenshot(other, '06-mobile-reduced-motion');
  const separate = await bff('rooms', {});
  check('independent_room_created', separate.status === 200 && separate.data.roomId !== roomId);
  const isolated = await realFixture(separate.data, 'second room audience');
  const separatePayload = { type: 1, content: '第二房间隔离-' + randomUUID(), live_demo: { kind: 'barrage', roomId: separate.data.roomId, eventId: randomUUID() } };
  check('nonmember_cannot_send_to_other_room', denied(await fixture.send(separatePayload, separate.data.roomId), [3]));
  check('own_room_send_accepted', accepted(await isolated.send(separatePayload, separate.data.roomId)));
  await pause(200);
  check('second_room_message_absent_in_first_room', !incoming.some(item => item.content === separatePayload.content));
  const wrongEnvelope = { ...separatePayload, content: 'Forged other room envelope', live_demo: { ...separatePayload.live_demo, eventId: randomUUID() } };
  await fixture.send(wrongEnvelope);
  await until(() => incoming.some(item => item.label === 'viewer-2' && item.content === wrongEnvelope.content), 'foreign-room envelope actually arrives on current channel');
  check('wrong_room_envelope_not_rendered', await testid(other, 'chat').getByText(wrongEnvelope.content, { exact: true }).count() === 0);

  phase = 'assets-auth-failure-and-lifecycle';
  const embedded = await fetch(api + '/livedemo/'), embeddedHTML = await embedded.text();
  check('embedded_bundle_is_served', embedded.status === 200 && embeddedHTML.includes('/livedemo/assets/'));
  const asset = embeddedHTML.match(/(?:src|href)="([^"\s]+\.(?:js|css))"/)?.[1];
  check('real_hashed_asset_served', !!asset && (await fetch(new URL(asset, api))).status === 200);
  check('missing_asset_is_404', (await fetch(api + '/livedemo/assets/does-not-exist.js')).status === 404);
  check('embedded_HEAD_works', (await fetch(api + '/livedemo/', { method: 'HEAD' })).status === 200);
  check('cross_port_home_link_preserved', (await page.locator('[data-demo-home]').evaluateAll(nodes => nodes.map(node => node.getAttribute('href')))).every(href => href === api + '/demos/'));
  bootstraps.delete(other);
  await other.getByRole('link', { name: /返回首页/ }).click();
  await until(() => other.url().startsWith(api + '/demos/'), 'actual navigation to cross-port catalog');
  await other.goBack();
  await until(async () => bootstraps.has(other) && await testid(other, 'send').isEnabled(), 'back navigation restores current viewer connection');
  check('navigation_back_preserves_viewer_identity', bootstraps.get(other).viewer.uid === secondSession.viewer.uid);
  const afterNavigation = '返回直播后的真实消息-' + randomUUID();
  await testid(other, 'send-input').fill(afterNavigation); await testid(other, 'send').click();
  await testid(page, 'chat').getByText(afterNavigation, { exact: true }).waitFor();
  check('navigation_back_reestablishes_real_message_path', incoming.some(item => item.label === 'viewer-1' && item.content === afterNavigation && item.fromUid === secondSession.viewer.uid));
  const authPage = await context.newPage();
  await authPage.route('**/livedemo/api/rooms', async route => { const response = await route.fetch(), bootstrap = await response.json(); bootstrap.viewer.token = 'invalid-fixture-token'; await route.fulfill({ response, json: bootstrap }); });
  await authPage.goto(base + '/livedemo/'); await testid(authPage, 'start').click();
  await until(async () => /失败|拒绝|错误/.test(await authPage.locator('body').textContent()), 'real invalid credential error');
  check('invalid_gateway_credentials_keep_input_disabled', await testid(authPage, 'send').isDisabled());
  await screenshot(authPage, '07-invalid-real-gateway-auth'); await authPage.close();
  const unavailable = await context.newPage();
  await unavailable.goto(base + '/livedemo/'); await unavailable.getByRole('button', { name: /演示设置/ }).click();
  const unavailablePort = await reservePort();
  await testid(unavailable, 'settings-backend').fill('http://127.0.0.1:' + unavailablePort); await testid(unavailable, 'backend-save').click();
  await testid(unavailable, 'start').click();
  await until(async () => /演示业务服务未确认请求，请检查连接后重试/.test(await unavailable.getByRole('alert').textContent()), 'actionable unavailable backend');
  check('unavailable_backend_keeps_input_disabled_and_retryable', await testid(unavailable, 'send').isDisabled() && await testid(unavailable, 'start').isEnabled());
  await screenshot(unavailable, '08-unavailable-backend'); await unavailable.close();
  const left = await bff('leave', { roomId }, secondSession.viewerKey);
  check('explicit_leave_confirmed', left.status === 200 && left.data.left === true);
  const leftResume = await bff('resume', { roomId, viewerKey: secondSession.viewerKey });
  check('left_identity_resume_invalidated', [403, 410].includes(leftResume.status) && leftResume.data.code === 'recovery_invalid');
  check('actual_product_denies_left_member', denied(await victim.send(payload('barrage', 'Left audience must be rejected')), [3]));
  phase = 'late-real-heartbeat-cannot-retire-replacement-session';
  await page.bringToFront();
  let heldHeartbeat = false, oldHeartbeatStatus, forwardHeartbeat, releaseHeartbeat;
  const forwardGate = new Promise(resolve => { forwardHeartbeat = resolve; });
  const releaseGate = new Promise(resolve => { releaseHeartbeat = resolve; });
  await page.route('**/livedemo/api/heartbeat', async route => {
    if (heldHeartbeat) { await route.continue(); return; }
    heldHeartbeat = true;
    await forwardGate;
    const response = await route.fetch(); oldHeartbeatStatus = response.status();
    await releaseGate; await route.fulfill({ response });
  });
  // Wait for the application's actual low-frequency heartbeat, without
  // manipulating its timers or calling private application state.
  await until(() => heldHeartbeat, 'actual application heartbeat captured', 55000);
  const closeResponse = page.waitForResponse(response => response.url().endsWith('/livedemo/api/close') && response.request().method() === 'POST');
  await testid(page, 'close').click();
  const closeResult = await closeResponse, closed = { status: closeResult.status(), data: await closeResult.json() };
  check('room_close_confirmed', closed.status === 200 && closed.data.closed === true);
  await until(async () => (await testid(page, 'connection').textContent()).includes('已结束'), 'old room closed via actual UI');
  bootstraps.delete(page); await testid(page, 'start').click();
  await until(async () => bootstraps.has(page) && await testid(page, 'send').isEnabled(), 'replacement room ready');
  const replacementSession = bootstraps.get(page);
  check('replacement_session_has_new_identity', replacementSession.viewer.uid !== firstSession.viewer.uid && replacementSession.roomId !== roomId);
  forwardHeartbeat(); await until(() => oldHeartbeatStatus !== undefined, 'old heartbeat reaches real BFF after old close');
  check('old_heartbeat_gets_real_recovery_invalid', oldHeartbeatStatus === 410);
  releaseHeartbeat(); await pause(200);
  check('late_old_heartbeat_cannot_retire_new_session', await testid(page, 'send').isEnabled() && (await page.locator('#parameters').textContent()).includes(replacementSession.viewer.uid));
  await screenshot(page, '09-late-old-heartbeat-new-room');
  await page.unroute('**/livedemo/api/heartbeat');
  const closedResume = await bff('resume', { roomId, viewerKey: firstSession.viewerKey });
  check('closed_room_resume_invalidated', [403, 410].includes(closedResume.status) && closedResume.data.code === 'recovery_invalid');
  await bff('close', { roomId: replacementSession.roomId }, replacementSession.ownerKey);
  await bff('close', { roomId: separate.data.roomId }, separate.data.ownerKey);
  const restartRoom = await bff('rooms', {});
  check('restart_recovery_fixture_is_real', restartRoom.status === 200);
  const backendExited = once(backend, 'exit'); backend.kill('SIGTERM'); await backendExited;
  const replacement = start('live-demo-restarted', process.execPath, ['server.mjs'], { ...cleanEnv, WK_DEMO_PORT: String(demoPort), WK_DEMO_API_URL: relay.url }, join(root, 'demo/livedemo'));
  await ready(base + '/livedemo/api/health', replacement);
  const restartResume = await bff('resume', { roomId: restartRoom.data.roomId, viewerKey: restartRoom.data.viewerKey });
  check('backend_restart_invalidates_old_recovery', [403, 410].includes(restartResume.status) && restartResume.data.code === 'recovery_invalid');
  check('no_browser_errors', browserErrors.length === 0);
  check('all_live_messages_used_actual_transport', incoming.length > 0 && historyRequests.length === 0);
  const finishedMetadata = await metadata();
  check('candidate_binary_unchanged_during_run', finishedMetadata.binarySha256 === runMetadata.binarySha256);
  await writeFile(join(evidence, 'report.json'), JSON.stringify({ passed: true, phase, ...runMetadata, finishedSourceDigests: finishedMetadata.sourceDigests, checks, incoming, sent, responses, fixtureResponses, relayObservations, httpObservations, historyRequests, browserErrors }, null, 2));
  console.log(JSON.stringify({ passed: true, checks: checks.length, evidence }));
} catch (error) {
  const failureUI = [];
  for (const [index, activePage] of (browser?.contexts().flatMap(context => context.pages()) || []).entries()) {
    await screenshot(activePage, 'failure-page-' + (index + 1)).catch(() => {});
    try { failureUI.push({ page: index + 1, path: new URL(activePage.url()).pathname, ...await activePage.evaluate(() => ({ alert: document.querySelector('[role="alert"]')?.textContent, connection: document.querySelector('[data-testid="connection"]')?.textContent, permission: document.querySelector('[data-testid="permission"]')?.textContent, sendDisabled: document.querySelector('[data-testid="send"]')?.disabled })) }); } catch {}
  }
  await writeFile(join(evidence, 'failure.json'), JSON.stringify({ passed: false, phase, ...(runMetadata || await metadata()), checks, incoming, sent, responses, fixtureResponses, relayObservations, httpObservations, historyRequests, browserErrors, failureUI, error: error.message }, null, 2));
  console.error('Evidence: ' + evidence); process.exitCode = 1;
} finally {
  for (const client of fixtureClients) await client.close().catch(() => {});
  await browser?.close();
  const cleanup = [];
  for (const child of children.reverse()) {
    const exited = child.exitCode !== null || child.signalCode !== null ? Promise.resolve() : once(child, 'exit');
    if (child.exitCode === null && child.signalCode === null) child.kill('SIGTERM');
    await Promise.race([exited, pause(5000)]);
    if (child.exitCode === null && child.signalCode === null) { child.kill('SIGKILL'); await exited; }
    cleanup.push({ pid: child.pid, exitCode: child.exitCode, signal: child.signalCode, stopped: child.exitCode !== null || child.signalCode !== null });
  }
  await writeFile(join(evidence, 'cleanup.json'), JSON.stringify(cleanup, null, 2));
  await relay?.close();
  for (const [name, log] of Object.entries(logs)) await writeFile(join(evidence, name + '.log'), log);
}
