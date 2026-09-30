// Process-level black-box acceptance against a real 256-slot single-node cluster.
// Failure cases are specified before the support Demo implementation.
import assert from 'node:assert/strict';
import { mkdtemp, writeFile, access } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createServer as netServer } from 'node:net';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { WKIM, WKIMEvent } from 'easyjssdk';

const cwd = fileURLToPath(new URL('../', import.meta.url));
const delay = ms => new Promise(r => setTimeout(r, ms));
async function until(fn, label, ms = 30000) {
  const deadline = Date.now() + ms;
  while (Date.now() < deadline) { if (await fn()) return; await delay(40); }
  throw Error(`Timed out: ${label}`);
}
async function port() {
  const socket = netServer(); socket.listen(0, '127.0.0.1'); await once(socket, 'listening');
  const value = socket.address().port; await new Promise(r => socket.close(r)); return value;
}
const evidence = process.env.WK_SUPPORT_REPORT_DIR || await mkdtemp(join(tmpdir(), 'wk-support-demo-'));
const checks = [];
function check(name, result) { assert(result, name); checks.push(name); }
const children = [], clients = [];
let fixture, logs = {};
try {
  assert(process.env.WK_DEMO_SERVER_BIN, 'Set WK_DEMO_SERVER_BIN to a freshly built WuKongIM binary');
  await access(resolve(cwd, 'server.mjs'));
  const apiPort = await port(), raft = await port(), ws = await port(), demo = await port();
  const api = `http://127.0.0.1:${apiPort}`, base = `http://127.0.0.1:${demo}`;
  await writeFile(join(evidence, 'wukongim.toml'), `[node]\nid = 1\ndata_dir = "${evidence}/data"\n[cluster]\nid = "support-demo-validation"\nlisten_addr = "127.0.0.1:${raft}"\nnodes = [{id = 1, addr = "127.0.0.1:${raft}"}]\ninitial_slot_count = 8\nhash_slot_count = 256\nslot_replica_n = 1\n[api]\nlisten_addr = "127.0.0.1:${apiPort}"\nexternal_ws_addr = "ws://127.0.0.1:${ws}"\n[manager]\nlisten_addr = "127.0.0.1:0"\n[gateway]\ntoken_auth_on = true\nlisteners = [{name = "ws", network = "websocket", address = "127.0.0.1:${ws}", transport = "gnet", protocol = "wsmux"}]\n[plugin]\nenable = false\n[log]\nlevel = "warn"\ndir = "${evidence}/logs"\n`);
  function start(name, command, args, env) {
    const child = spawn(command, args, {cwd, env, stdio: ['ignore', 'pipe', 'pipe']});
    logs[name] = ''; for (const stream of [child.stdout, child.stderr]) stream.on('data', b => { logs[name] = (logs[name] + b).slice(-262144); });
    children.push(child); return child;
  }
  const cleanEnv = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('WK_')));
  start('cluster', process.env.WK_DEMO_SERVER_BIN, ['-config', join(evidence, 'wukongim.toml')], cleanEnv);
  await until(async () => { try { return (await fetch(api + '/readyz')).ok; } catch { return false; } }, 'cluster readiness');
  start('demo', process.execPath, ['server.mjs'], {...cleanEnv, WK_DEMO_API_URL: api, WK_DEMO_PORT: String(demo)});
  await until(async () => { try { return (await fetch(base + '/supportdemo/api/health')).ok; } catch { return false; } }, 'Demo readiness');
  check('standalone_page', (await fetch(base + '/supportdemo/')).ok);
  const embedded = await fetch(api + '/supportdemo/');
  check('embedded_page', embedded.ok);
  const references = [...(await embedded.text()).matchAll(/(?:src|href)="(\/supportdemo\/assets\/[^\"]+)"/g)];
  assert(references.length > 0, 'Embedded UI must reference its production assets');
  for (const [, path] of references) {
    const asset = await fetch(api + path);
    assert(asset.ok && asset.headers.get('cache-control')?.includes('immutable'), path);
  }
  check('embedded_assets_use_immutable_caching', embedded.headers.get('cache-control') === 'no-cache');
  const embeddedMutation = await fetch(api + '/supportdemo/api/session', {method: 'POST', headers: {'content-type': 'application/json'}, body: '{}'});
  assert.equal(embeddedMutation.status, 405, 'embedded business mutation: ' + await embeddedMutation.text());
  check('embedded_UI_rejects_business_mutation', true);
  check('malformed_asset_is_rejected_without_crashing', (await fetch(base + '/supportdemo/assets/%XX.js')).status === 400 && (await fetch(base + '/supportdemo/api/health')).ok);
  const denied = await fetch(base + '/supportdemo/api/session', {method: 'POST', headers: {'content-type': 'application/json', origin: 'https://untrusted.example'}, body: '{}'});
  check('reject_cross_origin_mutation', denied.status === 403);
  const setup = await fetch(base + '/supportdemo/api/session', {method: 'POST', headers: {'content-type': 'application/json'}, body: '{}'}).then(r => r.json());
  const auth = {authorization: `Bearer ${setup.token}`, 'content-type': 'application/json'};
  async function request(route, body, status = 200) {
    const response = await fetch(base + '/supportdemo/api' + route, {method: body === undefined ? 'GET' : 'POST', headers: auth, body: body === undefined ? undefined : JSON.stringify(body)});
    assert.equal(response.status, status, route + ' HTTP status');
    return response.json();
  }
  const state = () => request('/state');
  const visitor = setup.visitors[0], other = setup.visitors[1];
  let room = setup.rooms.find(r => r.visitorId === visitor.id);
  const received = [], events = [];
  const receiver = WKIM.init(setup.wsUrl, {uid: visitor.uid, token: visitor.token, deviceFlag: 1}, {singleton: false});
  clients.push(receiver);
  receiver.on(WKIMEvent.Message, m => received.push(m));
  receiver.on(WKIMEvent.CustomEvent, e => events.push(e));
  await receiver.connect();
  const send = (actor, content, target = room.id, requestId = crypto.randomUUID()) => request('/send', {roomId: target, actor, content, requestId});
  await request('/config', {mode: 'simulation', interval: 50, text: '你好，订单已发出。快递正在运输，请耐心等待。'});
  await send('customer', '查询我的物流');
  await until(() => events.some(e => e.type === 'stream.finish'), 'normal AI finish');
  check('normal_question_received_over_SDK', received.some(m => m.payload?.content === '查询我的物流'));
  check('normal_AI_received_real_deltas', events.some(e => e.type === 'stream.delta' && e.data?.channel_id === room.channelId));
  const normalFinish = events.find(e => e.type === 'stream.finish');
  check('normal_snapshot', normalFinish.data.payload.snapshot.text === '你好，订单已发出。快递正在运输，请耐心等待。');
  await request('/send', {roomId: room.id, actor: 'agent', content: '不能抢答', requestId: crypto.randomUUID()}, 409);
  check('server_rejects_agent_during_AI', true);
  await until(async () => !(await state()).rooms.find(r => r.id === room.id).generating, 'generation released');
  await request('/config', {mode: 'simulation', interval: 150, text: '这是一段较长的订单说明，用来验证接管过程中已经开始的 AI 回复会及时停止，并且不会在人工接入后继续追加任何文字。'.repeat(3)});
  const before = events.length;
  await send('customer', '我要退货，先说明步骤');
  await until(() => events.slice(before).some(e => e.type === 'stream.delta'), 'handoff first delta');
  const waiting = await request('/handoff', {roomId: room.id, requestId: crypto.randomUUID()});
  check('manual_handoff_waiting', waiting.rooms.find(r => r.id === room.id).status === 'waiting');
  check('handoff_saves_cancelled_snapshot', events.slice(before).some(e => e.type === 'stream.cancel'));
  await request('/send', {roomId: room.id, actor: 'agent', content: '还未接入', requestId: crypto.randomUUID()}, 409);
  const countAfterFence = events.filter(e => e.type === 'stream.delta' && e.data?.channel_id === room.channelId).length;
  await request('/accept', {roomId: room.id, requestId: crypto.randomUUID()});
  const humanKey = crypto.randomUUID();
  await send('agent', '您好，我是人工客服，继续为您处理。', room.id, humanKey);
  await send('agent', '您好，我是人工客服，继续为您处理。', room.id, humanKey);
  await until(() => received.some(m => m.payload?.content === '您好，我是人工客服，继续为您处理。'), 'human SDK reply');
  await delay(350);
  check('no_late_AI_delta_after_handoff', events.filter(e => e.type === 'stream.delta' && e.data?.channel_id === room.channelId).length === countAfterFence);
  check('idempotent_human_send', received.filter(m => m.clientMsgNo === humanKey).length === 1);
  await request('/send', {roomId: room.id, actor: 'agent', content: 'changed payload', requestId: humanKey}, 409);
  check('reject_idempotency_payload_conflict', true);
  const secondRoom = setup.rooms.find(r => r.visitorId === other.id);
  await request('/send', {roomId: secondRoom.id, actor: 'customer', content: '第二个访客的独立咨询', requestId: crypto.randomUUID()});
  await until(async () => !(await state()).rooms.find(r => r.id === secondRoom.id).generating, 'second visitor complete');
  check('visitor_channel_isolation', !received.some(m => m.payload?.content === '第二个访客的独立咨询'));
  await request('/end', {roomId: room.id, requestId: crypto.randomUUID()});
  check('ended_room_read_only', (await state()).rooms.find(r => r.id === room.id).status === 'closed');
  for (const actor of ['customer', 'agent']) await request('/send', {roomId: room.id, actor, content: '结束后不能发言', requestId: crypto.randomUUID()}, 409);
  check('server_rejects_closed_room_send', true);
  const reopenAttempts = await Promise.all(Array.from({length: 2}, () => fetch(base + '/supportdemo/api/new', {method: 'POST', headers: auth, body: JSON.stringify({visitorId: visitor.id, requestId: crypto.randomUUID()})})));
  check('concurrent_new_consultation_creates_one_channel', reopenAttempts.filter(r => r.ok).length === 1 && reopenAttempts.filter(r => r.status === 409).length === 1);
  const reopened = await reopenAttempts.find(r => r.ok).json();
  const newRoom = reopened.rooms.filter(r => r.visitorId === visitor.id).at(-1);
  check('new_consultation_new_channel', newRoom.channelId !== room.channelId && newRoom.status === 'ai');
  check('old_conversation_retained', reopened.rooms.find(r => r.id === room.id).status === 'closed');
  room = newRoom;
  // A real upstream fixture exercises the same SSE/model path without paid requests.
  let modelCalls = 0, modelAborts = 0;
  const contexts = [];
  fixture = createServer(async (req, res) => {
    if (req.url === '/v1/models') { res.setHeader('content-type', 'application/json'); res.end(JSON.stringify({data: [{id: 'test-chat'}]})); return; }
    let raw = ''; for await (const chunk of req) raw += chunk;
    const input = JSON.parse(raw); contexts.push(input.messages); modelCalls++;
    res.writeHead(200, {'content-type': 'text/event-stream'});
    let finished = false; res.on('close', () => { if (!finished) modelAborts++; });
    const last = input.messages.at(-1).content;
    res.write('data: ' + JSON.stringify({choices: [{index: 0, delta: {content: '真实模型夹具：'}}]}) + '\n\n');
    await delay(250);
    if (res.destroyed) return;
    if (last.includes('失败')) { finished = true; res.end('data: {"error":{"message":"fixture-key-must-not-leak"}}\n\n'); return; }
    if (last.includes('中断')) { res.end(); return; }
    for (const text of ['订单正在运输。', '我会继续协助。']) { if (res.destroyed) return; res.write('data: ' + JSON.stringify({choices: [{index: 0, delta: {content: text}}]}) + '\n\n'); await delay(150); }
    finished = true; res.end('data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n');
  });
  fixture.listen(0, '127.0.0.1'); await once(fixture, 'listening');
  const modelURL = `http://127.0.0.1:${fixture.address().port}/v1`;
  await request('/config', {mode: 'model', url: modelURL, apiKey: 'fixture-key-must-not-leak', model: ''});
  await send('customer', '通过模型回答物流问题');
  await until(async () => !(await state()).rooms.find(r => r.id === room.id).generating, 'real SSE complete');
  check('model_auto_discovery', (await state()).config.model === 'test-chat');
  await send('customer', '请基于上次回答继续解释');
  await until(async () => !(await state()).rooms.find(r => r.id === room.id).generating, 'model context complete');
  check('multi_turn_model_context', contexts.at(-1).some(m => m.role === 'assistant' && m.content.includes('真实模型夹具')));
  const failureBefore = events.length;
  await send('customer', '失败测试');
  await until(() => events.slice(failureBefore).some(e => e.type === 'stream.error' && e.data.channel_id === room.channelId), 'SSE failure');
  await until(async () => !(await state()).rooms.find(r => r.id === room.id).generating, 'failure finalized');
  check('failed_model_exposes_handoff', !!(await state()).rooms.find(r => r.id === room.id).error);
  const truncatedBefore = events.length;
  await send('customer', '中断测试');
  await until(() => events.slice(truncatedBefore).some(e => e.type === 'stream.error' && e.data.channel_id === room.channelId), 'truncated model stream failure');
  await until(async () => !(await state()).rooms.find(r => r.id === room.id).generating, 'truncated finalized');
  check('truncated_model_is_failure', true);
  const abortBefore = events.length;
  await send('customer', '请给出更长的回答供转人工测试');
  await until(() => events.slice(abortBefore).some(e => e.type === 'stream.delta' && e.data.channel_id === room.channelId), 'real model delta');
  await request('/handoff', {roomId: room.id, requestId: crypto.randomUUID()});
  await until(() => modelAborts > 0, 'upstream request aborted');
  check('handoff_aborts_model_request', true);
  receiver.disconnect();
  await request('/accept', {roomId: room.id, requestId: crypto.randomUUID()});
  await send('agent', '断线期间仍然保留的人工答复');
  await receiver.connect();
  const history = await request('/history', {roomId: room.id, actor: 'customer'});
  check('offline_history_recovers_human_message', history.messages.some(m => JSON.parse(Buffer.from(m.payload, 'base64')).content === '断线期间仍然保留的人工答复'));
  check('offline_history_recovers_cancelled_projection', history.messages.some(m => m.event_meta?.events?.some(e => e.status === 'cancelled' && e.snapshot?.text)));
  const safe = JSON.stringify(await state());
  check('key_not_in_state_or_logs', !safe.includes('fixture-key-must-not-leak') && !JSON.stringify(logs).includes('fixture-key-must-not-leak'));
  await request('/history', {roomId: 'unknown-room', actor: 'customer'}, 404);
  check('reject_foreign_room', true);
  const creations = await Promise.all(Array.from({length: 12}, () => fetch(base + '/supportdemo/api/session', {method: 'POST', headers: {'content-type': 'application/json'}, body: '{}'})));
  check('concurrent_workspace_creation_respects_capacity', creations.filter(r => r.status === 200).length === 7 && creations.filter(r => r.status === 429).length === 5);
  await writeFile(join(evidence, 'history.json'), JSON.stringify(history, null, 2));
  await writeFile(join(evidence, 'report.json'), JSON.stringify({passed: true, checks, modelCalls, modelAborts, sdk: 'easyjssdk@2.0.5', topology: 'single-node cluster', hashSlots: 256}, null, 2));
  console.log(JSON.stringify({passed: true, checks: checks.length, evidence}));
} catch (error) {
  await writeFile(join(evidence, 'failure.json'), JSON.stringify({passed: false, completedChecks: checks, error: error.message}, null, 2));
  console.error('Evidence:', evidence); throw error;
} finally {
  for (const client of clients) client.disconnect();
  fixture?.closeAllConnections(); fixture?.close();
  for (const child of children.reverse()) {
    const exited = child.exitCode === null ? once(child, 'exit') : Promise.resolve();
    child.kill('SIGTERM'); await Promise.race([exited, delay(4000)]);
    if (child.exitCode === null) { child.kill('SIGKILL'); await exited; }
  }
  for (const [name, content] of Object.entries(logs)) await writeFile(join(evidence, name + '.log'), content);
}
