import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { resolve, extname, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import { randomUUID, timingSafeEqual } from 'node:crypto';

// This loopback BFF owns Demo capabilities and current room state. Audience
// messages use browser SDK connections; no audience send proxy exists here.
const port = Number(process.env.WK_DEMO_PORT || 5180);
const api = new URL(process.env.WK_DEMO_API_URL || 'http://127.0.0.1:5001');
if (!Number.isInteger(port) || port < 1 || port > 65535 || !['http:', 'https:'].includes(api.protocol) || api.username || api.password || api.search || api.hash) throw Error('Invalid Live Demo configuration');
const root = resolve(fileURLToPath(new URL('../../internal/access/api/demoui/livedist/', import.meta.url)));
const ROOM_LIMIT = 8, VIEWER_LIMIT = 16, QUEUE_LIMIT = 8, RESULT_LIMIT = 64;
const IDLE_MS = 3600000, HEARTBEAT_MS = 30000;
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
// Closed rooms with unconfirmed removal retain a quota slot. Cleanup records
// contain only bounded registered UID sets, never caller-supplied identities.
const rooms = new Map(), cleanup = new Map();
let active = 0, productActive = 0, maintenanceActive = false, stopping = false;

class HTTPError extends Error {
  constructor(status, code, message) { super(message); this.status = status; this.code = code; }
}
const fail = (status, code, message) => { throw new HTTPError(status, code, message); };
function equal(left, right) {
  if (typeof left !== 'string' || typeof right !== 'string' || !uuid.test(right)) return false;
  const expected = Buffer.from(left), supplied = Buffer.from(right);
  return expected.length === supplied.length && timingSafeEqual(expected, supplied);
}
const pause = ms => new Promise(done => setTimeout(done, ms));

// A successful Product HTTP transport response is insufficient for SEND;
// callers also validate its business reason. Raw product errors never escape.
async function product(path, body, timeout = 8000) {
  if (productActive >= 8) throw Error('Product request not confirmed');
  productActive++;
  try {
    const response = await fetch(new URL(path, api), {
      method: body === undefined ? 'GET' : 'POST',
      headers: { 'content-type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.timeout(timeout), redirect: 'error',
    });
    const value = await response.json();
    if (!response.ok || !value || typeof value !== 'object' || value.status !== undefined && value.status !== 200) throw Error('Product request not confirmed');
    return value;
  } finally { productActive--; }
}
async function mutation(path, value, timeout = 8000) {
  const result = await product(path, value, timeout);
  if (result.status !== 200) throw Error('Product mutation not confirmed');
}

function snapshot(room) {
  return {
    roomId: room.id, version: room.version, hostUid: room.hostUid, notice: room.notice,
    viewers: [...room.viewers.values()].map(viewer => ({ uid: viewer.uid, name: viewer.name, muted: viewer.muted, pending: room.pending?.targetUid === viewer.uid })),
    pending: room.pending ? { requestId: room.pending.requestId, targetUid: room.pending.targetUid, desiredMuted: room.pending.muted } : null,
  };
}
function notificationMeta(notification) {
  return notification ? { status: notification.status, requestId: notification.id, roomVersion: notification.version } : undefined;
}
function requireLive(room) {
  if (room.closed || rooms.get(room.id) !== room) fail(410, 'recovery_invalid', '这场演示已结束，请重新创建直播间。');
}
function touch(room) { room.lastActivity = Date.now(); }

// Room mutations share one bounded serial lane. State reads copy a complete
// snapshot synchronously and never wait for an unrelated Product request.
function serial(room, task) {
  if (room.queued >= QUEUE_LIMIT) return Promise.reject(new HTTPError(429, 'room_busy', '直播间操作较多，请稍后重试。'));
  room.queued++;
  const result = room.tail.then(() => { requireLive(room); return task(); });
  room.tail = result.catch(() => {});
  return result.finally(() => { room.queued--; });
}
function newNotification(room) {
  const state = snapshot(room), id = randomUUID();
  const payload = { type: 1, content: '主播更新直播间状态', live_demo: { kind: 'room_state', roomId: room.id, eventId: id, roomVersion: state.version, snapshot: state } };
  const bytes = Buffer.from(JSON.stringify(payload));
  if (bytes.length > 4096) throw Error('Room notification exceeds its bound');
  // Payload and UUID stay immutable through uncertain-response retries.
  const notification = { id, version: state.version, payload: bytes.toString('base64'), status: 'pending' };
  room.notification = notification;
  return notification;
}
async function sendNotification(room, notification) {
  if (!notification || notification.status === 'confirmed') return;
  requireLive(room);
  try {
    const result = await product('/message/send', {
      from_uid: room.hostUid, channel_id: room.id, channel_type: 2,
      client_msg_no: notification.id, payload: notification.payload,
      header: { no_persist: 1, sync_once: 0, red_dot: 0 },
    }, 4000);
    requireLive(room);
    if (result.reason === 1) notification.status = 'confirmed';
  } catch { /* Unknown delivery outcome remains pending; no version/broadcast loop. */ }
}
async function changed(room) {
  room.version++;
  const notification = newNotification(room);
  await sendNotification(room, notification);
  return notification;
}
function bootstrap(room, viewer, creator = false) {
  const value = { roomId: room.id, invite: room.invite, hostUid: room.hostUid, wsUrl: room.wsUrl, viewer: { uid: viewer.uid, token: viewer.token, name: viewer.name }, viewerKey: viewer.key, snapshot: snapshot(room) };
  if (creator) value.ownerKey = room.ownerKey;
  return value;
}
function viewerIdentity(index) {
  return { uid: `livedemo-v-${randomUUID()}`, token: randomUUID(), key: randomUUID(), name: index === 1 ? '小林' : index === 2 ? '小陈' : `观众 ${index}`, muted: false, lastHeartbeat: 0 };
}
async function prepareToken(person) {
  await mutation('/user/token', { uid: person.uid, token: person.token, device_flag: 1, device_level: 0 });
}
async function createRoom() {
  if (rooms.size + cleanup.size >= ROOM_LIMIT) fail(429, 'room_limit', '最多同时准备 8 场演示，请结束已有直播间后重试。');
  const viewer = viewerIdentity(1);
  const room = {
    id: `livedemo-${randomUUID()}`, hostUid: `livedemo-h-${randomUUID()}`,
    ownerKey: randomUUID(), invite: randomUUID(), notice: '', version: 0,
    viewers: new Map(), cleanupUIDs: new Set(), reservations: new Set([viewer.uid]),
    pending: null, notification: null, operations: new Map(), nextViewer: 2,
    tail: Promise.resolve(), queued: 0, lastActivity: Date.now(), closed: false,
  };
  rooms.set(room.id, room);
  let groupAttempted = false;
  try {
    await prepareToken({ uid: room.hostUid, token: randomUUID() });
    requireLive(room);
    await prepareToken(viewer); requireLive(room);
    groupAttempted = true;
    await mutation('/channel', { channel_id: room.id, channel_type: 2, subscribers: [room.hostUid, viewer.uid] });
    requireLive(room);
    const route = await product('/route?uid=' + encodeURIComponent(viewer.uid));
    requireLive(room);
    const address = new URL(route.wss_addr || route.ws_addr);
    if (!['ws:', 'wss:'].includes(address.protocol) || address.username || address.password) throw Error('Missing WebSocket address');
    room.wsUrl = address.href;
    room.viewers.set(viewer.uid, viewer); room.reservations.clear();
    await changed(room); requireLive(room);
    return bootstrap(room, viewer, true);
  } catch (error) {
    room.closed = true; rooms.delete(room.id);
    if (groupAttempted) {
      const job = { id: room.id, uids: new Set([room.hostUid, viewer.uid]) };
      cleanup.set(room.id, job); await removeMembers(job);
    }
    throw error;
  }
}
async function joinRoom(room) {
  return serial(room, async () => {
    if (room.viewers.size + room.cleanupUIDs.size + room.reservations.size >= VIEWER_LIMIT) fail(429, 'viewer_limit', '本场演示最多加入 16 位观众。');
    const viewer = viewerIdentity(room.nextViewer++);
    room.reservations.add(viewer.uid);
    let memberAttempted = false;
    try {
      await prepareToken(viewer); requireLive(room);
      memberAttempted = true;
      await mutation('/channel/subscriber_add', { channel_id: room.id, channel_type: 2, subscribers: [viewer.uid] });
      requireLive(room);
      room.viewers.set(viewer.uid, viewer); room.reservations.delete(viewer.uid); touch(room);
      await changed(room);
      return bootstrap(room, viewer);
    } catch (error) {
      room.reservations.delete(viewer.uid);
      if (memberAttempted) room.cleanupUIDs.add(viewer.uid);
      throw error;
    }
  });
}

// One pending membership mutation retains its original desired intent. Product
// timeouts cannot justify applying an opposing operation or confirming old state.
async function applyMute(room, operation) {
  for (let attempt = 0; attempt < 3; attempt++) {
    requireLive(room);
    try {
      await mutation(operation.muted ? '/channel/blacklist_add' : '/channel/blacklist_remove', { channel_id: room.id, channel_type: 2, uids: [operation.targetUid] }, 2000);
      requireLive(room);
      const viewer = room.viewers.get(operation.targetUid);
      if (viewer) viewer.muted = operation.muted;
      operation.status = 'confirmed'; room.pending = null;
      operation.notification = await changed(room);
      return;
    } catch (error) {
      if (error instanceof HTTPError) throw error;
      if (attempt < 2) await pause(100);
    }
  }
}
function remember(room, operation) {
  if (room.operations.size >= RESULT_LIMIT) room.operations.delete(room.operations.keys().next().value);
  room.operations.set(operation.requestId, operation);
}
function controlResult(room, operation, notification = operation.notification) {
  return { snapshot: snapshot(room), operation: { requestId: operation.requestId, status: operation.status }, notification: notificationMeta(notification) };
}
async function control(room, command) {
  return serial(room, async () => {
    const old = room.operations.get(command.requestId);
    if (old) {
      if (old.signature !== command.signature) fail(409, 'idempotency_conflict', '同一操作编号不能改变操作内容。');
      return controlResult(room, old);
    }
    if (room.pending) fail(409, 'operation_pending', '上一项权限操作待确认，请先重试原操作。');
    if (command.kind === 'mute' && !room.viewers.has(command.targetUid)) fail(404, 'viewer_missing', '该观众已离开本场演示。');
    const operation = { ...command, status: command.kind === 'notice' ? 'confirmed' : 'pending', notification: null };
    remember(room, operation); touch(room);
    if (operation.kind === 'notice') {
      room.notice = operation.text;
      operation.notification = await changed(room);
    } else {
      room.pending = operation;
      operation.notification = await changed(room);
      await applyMute(room, operation);
    }
    return controlResult(room, operation);
  });
}
async function retry(room, input) {
  return serial(room, async () => {
    const operation = room.operations.get(input.requestId);
    if (input.phase === 'operation') {
      if (!operation) fail(404, 'operation_missing', '这项操作已不在演示保留范围内。');
      if (operation.status === 'pending') {
        if (room.pending !== operation) fail(409, 'operation_pending', '只能重试当前待确认的原操作。');
        await applyMute(room, operation);
      }
      touch(room); return controlResult(room, operation);
    }
    const notification = room.notification?.id === input.requestId ? room.notification : operation?.notification || [...room.operations.values()].find(item => item.notification?.id === input.requestId)?.notification;
    if (!notification) fail(404, 'notification_missing', '这份通知已不在演示保留范围内，请恢复当前快照。');
    await sendNotification(room, notification); touch(room);
    return { snapshot: snapshot(room), operation: { requestId: operation?.requestId || input.requestId, status: operation?.status || 'confirmed' }, notification: notificationMeta(notification) };
  });
}

async function removeMembers(job) {
  if (!job.uids.size) { cleanup.delete(job.id); return true; }
  try {
    await mutation('/channel/subscriber_remove', { channel_id: job.id, channel_type: 2, subscribers: [...job.uids] }, 2000);
    job.uids.clear(); cleanup.delete(job.id); return true;
  } catch { return false; }
}
async function leave(room, viewer) {
  return serial(room, async () => {
    if (room.viewers.get(viewer.uid) !== viewer) fail(410, 'recovery_invalid', '这份观众恢复凭据已失效。');
    if (room.pending?.targetUid === viewer.uid) fail(409, 'operation_pending', '该观众的权限操作待确认，请先重试原操作或结束直播间。');
    room.viewers.delete(viewer.uid); room.cleanupUIDs.add(viewer.uid); touch(room);
    const removed = await removeMembers({ id: room.id, uids: new Set([viewer.uid]) });
    if (removed) room.cleanupUIDs.delete(viewer.uid);
    await changed(room);
    return { left: true, cleanup: removed ? 'confirmed' : 'pending' };
  });
}
async function closeRoom(room) {
  return serial(room, async () => {
    room.closed = true; rooms.delete(room.id);
    const job = { id: room.id, uids: new Set([room.hostUid, ...room.viewers.keys(), ...room.cleanupUIDs, ...room.reservations]) };
    room.viewers.clear(); room.operations.clear(); room.pending = null;
    cleanup.set(room.id, job);
    const removed = await removeMembers(job);
    return { closed: true, cleanup: removed ? 'confirmed' : 'pending' };
  });
}
async function maintenance() {
  if (maintenanceActive || stopping) return;
  maintenanceActive = true;
  try {
    for (const room of [...rooms.values()]) {
      if (Date.now() - room.lastActivity >= IDLE_MS) { await closeRoom(room).catch(() => {}); continue; }
      if (room.cleanupUIDs.size) await serial(room, async () => {
        const job = { id: room.id, uids: new Set(room.cleanupUIDs) };
        if (await removeMembers(job)) for (const uid of [...room.cleanupUIDs]) room.cleanupUIDs.delete(uid);
      }).catch(() => {});
    }
    for (const job of [...cleanup.values()]) await removeMembers(job);
  } finally { maintenanceActive = false; }
}

function shape(input, required, optional = []) {
  if (!input || typeof input !== 'object' || Array.isArray(input) || required.some(key => !(key in input)) || Object.keys(input).some(key => !required.includes(key) && !optional.includes(key))) fail(400, 'invalid_request', '请求格式有误。');
}
function roomID(value) { if (typeof value !== 'string' || !/^livedemo-[0-9a-f-]{36}$/i.test(value)) fail(400, 'invalid_request', '直播间编号无效。'); return value; }
function requestID(value) { if (typeof value !== 'string' || !uuid.test(value)) fail(400, 'invalid_request', '操作编号需使用 UUID。'); return value; }
function bearer(req) {
  const match = /^Bearer ([0-9a-f-]{36})$/i.exec(req.headers.authorization || '');
  if (!match) fail(403, 'unauthorized', '需要有效的演示会话。');
  return match[1];
}
async function findRoom(id) {
  const room = rooms.get(roomID(id));
  if (!room) fail(410, 'recovery_invalid', '这场演示已结束或服务已重启，请重新进入。');
  if (Date.now() - room.lastActivity >= IDLE_MS) { await closeRoom(room).catch(() => {}); fail(410, 'recovery_invalid', '这场演示已过期，请重新创建直播间。'); }
  requireLive(room); return room;
}
function owner(room, key) { if (!equal(room.ownerKey, key)) fail(403, 'unauthorized', '此操作仅限本场演示的主播。'); }
function viewerFor(room, key) {
  const viewer = [...room.viewers.values()].find(value => equal(value.key, key));
  if (!viewer) fail(410, 'recovery_invalid', '这份观众恢复凭据已失效。');
  return viewer;
}
async function body(req) {
  if (!/^application\/json(?:\s*;|$)/i.test(req.headers['content-type'] || '')) fail(415, 'json_required', '需要 JSON 请求。');
  if (Number(req.headers['content-length']) > 4096) fail(413, 'body_limit', '请求不能超过 4 KiB。');
  const chunks = []; let length = 0;
  await new Promise((done, reject) => {
    const timer = setTimeout(() => finish(new HTTPError(408, 'body_timeout', '请求正文读取超时。')), 8000);
    const finish = error => {
      clearTimeout(timer); req.off('data', data); req.off('end', end); req.off('error', errorHandler); req.off('aborted', aborted);
      if (error) { req.resume(); reject(error); } else done();
    };
    const data = chunk => { length += chunk.length; if (length > 4096) finish(new HTTPError(413, 'body_limit', '请求不能超过 4 KiB。')); else chunks.push(chunk); };
    const end = () => finish();
    const errorHandler = () => finish(new HTTPError(400, 'invalid_request', '请求未完整送达。'));
    const aborted = errorHandler;
    req.on('data', data); req.once('end', end); req.once('error', errorHandler); req.once('aborted', aborted);
  });
  try { return JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(Buffer.concat(chunks)) || '{}'); }
  catch { fail(400, 'invalid_request', '请求需为有效的 JSON。'); }
}
function controlCommand(input) {
  if (input.kind === 'notice') {
    shape(input, ['roomId', 'requestId', 'kind', 'text']);
    if (typeof input.text !== 'string' || [...input.text].length > 120) fail(400, 'invalid_request', '公告最多 120 字。');
    return { requestId: requestID(input.requestId), kind: 'notice', text: input.text, signature: JSON.stringify(['notice', input.text]) };
  }
  shape(input, ['roomId', 'requestId', 'kind', 'targetUid', 'muted']);
  if (input.kind !== 'mute' || typeof input.targetUid !== 'string' || input.targetUid.length > 100 || typeof input.muted !== 'boolean') fail(400, 'invalid_request', '权限操作格式有误。');
  return { requestId: requestID(input.requestId), kind: 'mute', targetUid: input.targetUid, muted: input.muted, signature: JSON.stringify(['mute', input.targetUid, input.muted]) };
}
function guard(req, res) {
  let host;
  try { host = new URL('http://' + req.headers.host); } catch { fail(403, 'loopback_only', '仅允许本机演示请求。'); }
  if (!['127.0.0.1', 'localhost', '[::1]'].includes(host.hostname) || Number(host.port || 80) !== port || host.username || host.password || host.pathname !== '/' || host.search || host.hash) fail(403, 'loopback_only', '仅允许本机演示请求。');
  const origins = new Set([host.origin, api.origin]);
  if (req.headers.origin ? !origins.has(req.headers.origin) : req.headers['sec-fetch-site'] === 'cross-site') fail(403, 'origin_denied', '仅允许演示页面访问。');
  if (req.headers.origin) { res.setHeader('Access-Control-Allow-Origin', req.headers.origin); res.setHeader('Vary', 'Origin'); }
}
function json(res, status, value) {
  if (res.destroyed || res.writableEnded) return;
  res.writeHead(status, { 'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store' });
  res.end(JSON.stringify(value));
}
async function serveAPI(req, res, url) {
  guard(req, res);
  if (req.method === 'OPTIONS') { res.writeHead(204, { 'Access-Control-Allow-Headers': 'content-type,authorization', 'Access-Control-Allow-Methods': 'GET,POST,OPTIONS', 'Access-Control-Max-Age': '600' }); res.end(); return; }
  if (url.pathname === '/livedemo/api/health' && req.method === 'GET') return json(res, stopping ? 503 : 200, { ready: !stopping });
  if (stopping) fail(503, 'shutting_down', '演示服务正在结束。');
  if (active >= 8) fail(429, 'busy', '演示请求过多，请稍后重试。');
  active++;
  try {
    const path = url.pathname.slice('/livedemo/api/'.length);
    if (path === 'state') {
      if (req.method !== 'GET') fail(405, 'method_not_allowed', '状态读取需使用 GET。');
      const room = await findRoom(url.searchParams.get('roomId')), key = bearer(req);
      const isOwner = equal(room.ownerKey, key);
      if (!isOwner) viewerFor(room, key);
      touch(room); return json(res, 200, { snapshot: snapshot(room), ...(isOwner ? { notification: notificationMeta(room.notification) } : {}) });
    }
    if (!['rooms', 'join', 'resume', 'control', 'retry', 'heartbeat', 'leave', 'close'].includes(path)) fail(404, 'not_found', '演示接口不存在。');
    if (req.method !== 'POST') fail(405, 'method_not_allowed', '此操作需使用 POST。');
    const input = await body(req);
    if (path === 'rooms') { shape(input, []); return json(res, 200, await createRoom()); }
    if (path === 'join') {
      shape(input, ['roomId', 'invite']); const room = await findRoom(input.roomId);
      if (!equal(room.invite, input.invite)) fail(403, 'invite_invalid', '观众邀请无效。');
      return json(res, 200, await joinRoom(room));
    }
    if (path === 'resume') {
      shape(input, ['roomId', 'viewerKey']); const room = await findRoom(input.roomId), viewer = viewerFor(room, input.viewerKey);
      touch(room); return json(res, 200, bootstrap(room, viewer));
    }
    const room = await findRoom(input.roomId), key = bearer(req);
    if (['control', 'retry', 'close'].includes(path)) owner(room, key);
    if (path === 'control') return json(res, 200, await control(room, controlCommand(input)));
    if (path === 'retry') {
      shape(input, ['roomId', 'requestId', 'phase']); requestID(input.requestId);
      if (!['operation', 'notification'].includes(input.phase)) fail(400, 'invalid_request', '重试阶段无效。');
      return json(res, 200, await retry(room, input));
    }
    shape(input, ['roomId']);
    if (path === 'close') return json(res, 200, await closeRoom(room));
    const viewer = viewerFor(room, key);
    if (path === 'leave') return json(res, 200, await leave(room, viewer));
    if (Date.now() - viewer.lastHeartbeat >= HEARTBEAT_MS) { viewer.lastHeartbeat = Date.now(); touch(room); }
    return json(res, 200, { ready: true });
  } finally { active--; }
}
async function serve(req, res) {
  res.setHeader('X-Content-Type-Options', 'nosniff');
  const url = new URL(req.url, `http://127.0.0.1:${port}`);
  if (url.pathname.startsWith('/livedemo/api/')) return serveAPI(req, res, url);
  if (!['GET', 'HEAD'].includes(req.method)) { res.writeHead(405); res.end(); return; }
  if (url.pathname === '/' || url.pathname === '/livedemo') { res.writeHead(302, { location: '/livedemo/' + url.search }); res.end(); return; }
  let file;
  try { file = url.pathname === '/livedemo/' ? 'index.html' : url.pathname.startsWith('/livedemo/assets/') ? decodeURIComponent(url.pathname.slice('/livedemo/'.length)) : ''; }
  catch { res.writeHead(400); res.end(); return; }
  const filename = resolve(root, file);
  if (!file || !filename.startsWith(root + sep)) { res.writeHead(404); res.end(); return; }
  try {
    let content = await readFile(filename);
    if (file === 'index.html') content = Buffer.from(content.toString().replace('</head>', `<meta name="wk-live-backend" content="same-origin"><meta name="wk-demo-home" content="${api.origin.replaceAll('&', '&amp;').replaceAll('"', '&quot;')}/demos/"></head>`));
    res.writeHead(200, { 'content-type': { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8' }[extname(file)] || 'application/octet-stream', 'cache-control': file === 'index.html' ? 'no-store' : 'public, max-age=31536000, immutable' });
    res.end(req.method === 'HEAD' ? undefined : content);
  } catch { res.writeHead(404); res.end('Build the Live Demo with npm run build.'); }
}
const server = createServer((req, res) => {
  // A body cancellation can emit a later error after its parser has settled.
  req.on('error', () => {});
  void serve(req, res).catch(error => {
  if (res.destroyed || res.writableEnded) return;
  if (res.headersSent) { res.destroy(); return; }
  json(res, error instanceof HTTPError ? error.status : 503, { code: error instanceof HTTPError ? error.code : 'product_unavailable', error: error instanceof HTTPError ? error.message : '演示服务无法确认产品请求，请检查 WuKongIM 后重试。' });
  });
});
server.requestTimeout = 10000; server.headersTimeout = 10000;
const maintenanceTimer = setInterval(() => { void maintenance(); }, 60000);
maintenanceTimer.unref();
server.on('error', error => { console.error(`Live Demo listen failed: ${error.code || 'unknown'}`); process.exitCode = 1; clearInterval(maintenanceTimer); });
server.listen(port, '127.0.0.1', () => console.log(`Live Demo: http://127.0.0.1:${port}/livedemo/`));
for (const signal of ['SIGTERM', 'SIGINT']) process.once(signal, () => {
  if (stopping) return;
  stopping = true; clearInterval(maintenanceTimer); server.close(); server.closeAllConnections();
  // Shutdown removal is best effort with a hard bound; recovery capabilities
  // never survive a backend restart, even when Product HTTP is unavailable.
  const timeout = setTimeout(() => process.exit(0), 4500);
  void Promise.allSettled([...rooms.values()].map(closeRoom)).then(() => Promise.allSettled([...cleanup.values()].map(removeMembers))).finally(() => { clearTimeout(timeout); process.exit(0); });
});
