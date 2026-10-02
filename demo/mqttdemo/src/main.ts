import mqtt, { type MqttClient } from 'mqtt';
import { WKIM, WKIMEvent } from 'easyjssdk';
import { demoHomeURL } from '../../shared/home';
import '../../shared/home.css';
import './style.css';

type Identity = { uid: string; token: string; clientId: string };
type Session = { id: string; device: Identity; staff: Identity; colleague: Identity; groupId: string; mqttWsUrl: string; wsUrl: string };
type Role = 'device' | 'staff';
type Business = { kind: 'alert' | 'command' | 'receipt' | 'recovery' | 'note'; alertId?: string; commandId?: string; action?: string; temperature?: number; status?: string };
type Payload = { type: 1; content: string; mqtt_demo: Business };
type Row = { id: string; kind: string; text: string; from: string; commandId?: string; time: string };
type Command = { alertId: string; commandId: string; payload: Payload; key: string };
type DeviceAlert = { id: string; status: 'alarm' | 'cooling' | 'recovered'; alertSent?: boolean; recoverySent?: boolean; recoveryKey?: string };
// Keep one immutable, correlated receipt until its original MQTT publish is
// confirmed. Retrying this record must never repeat the device effect.
type PendingReceipt = { target: string; payload: Payload; key: string };
type State = {
  session?: Session; temperature: number; executions: number; deviceAlert?: DeviceAlert;
  staffAlert?: { id: string; state: 'waiting' | 'cooling' | 'recovered' };
  command?: Command; commandAck: string; execution: string; recovery: string;
  staffRows: Row[]; colleagueRows: Row[]; deviceSeen: string[]; staffSeen: string[]; colleagueSeen: string[];
  executed: string[]; pendingReceipt?: PendingReceipt; logs: { time: string; text: string }[];
};
const storageKey = 'wk-mqtt-demo-v1';
const $ = <T extends HTMLElement = HTMLElement>(selector: string) => document.querySelector<T>(selector)!;
const fresh = (): State => ({ temperature: -20, executions: 0, commandAck: '未发送', execution: '等待回执', recovery: '等待恢复', staffRows: [], colleagueRows: [], deviceSeen: [], staffSeen: [], colleagueSeen: [], executed: [], logs: [] });
let state = fresh();
let backend = document.querySelector('meta[name="wk-mqtt-backend"]') ? location.origin : 'http://127.0.0.1:5179';
let starting = false, commandBusy = false, triggerBusy = false, receiptBusy = false, coolingTimer: ReturnType<typeof setInterval> | undefined;
const runtime: Record<Role, { client?: MqttClient; online: boolean; connecting: boolean; generation: number; present: boolean }> = {
  device: { online: false, connecting: false, generation: 0, present: false },
  staff: { online: false, connecting: false, generation: 0, present: false },
};
let colleague: ReturnType<typeof WKIM.init> | undefined, colleagueOnline = false, colleagueConnecting = false, colleagueGeneration = 0;
const pendingDevice: { command: Business; sender: string }[] = [];
const icon = `<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="6" y="2" width="12" height="20" rx="2"/><path d="M6 10h12M9 6v2M9 13v4"/></svg>`;
$('#app').innerHTML = `
<div class="store-app"><header class="topbar"><a class="brand" href="${demoHomeURL()}"><span>W</span>WuKongIM <small>MQTT DEMO</small></a><div class="top-actions"><a class="demo-home-link" data-demo-home><span aria-hidden="true">←</span>返回首页</a><button id="settings-toggle">演示设置 <span>⚙</span></button></div></header>
<main><section class="intro"><div><span class="eyebrow">CONNECTED STORE · 智能门店</span><h1>让设备告警，<span>有始有终。</span></h1><p>冷柜发现温度异常，店员远程处置，直到收到执行回执与恢复消息。</p></div><span class="store-badge"><i></i> 小悟便利店 · 冷柜 01</span></section>
<div id="error" role="alert"></div><section id="start-card"><div><strong>两端连接，同一条真实消息链路</strong><p>自动准备独立演示账号与门店群，设备和店员通过 MQTT 连接。</p></div><button id="start" class="primary">开始演示 ↗</button></section>
<nav class="mobile-tabs" aria-label="演示面板"><button data-view="device" class="active">冷柜设备</button><button data-view="staff">店员工作台</button></nav>
<div class="workspace" data-current-view="device">
<section class="panel device-panel" id="device-panel"><div class="panel-heading"><div><span class="section-label">DEVICE SIMULATOR</span><h2>${icon}冷柜设备</h2></div><div class="device-controls"><span id="device-status" class="connection">未连接</span><button id="device-reconnect" hidden>重连设备</button></div></div>
<div class="freezer"><div class="freezer-top"><span>FRESH / 新鲜每一天</span><i id="freezer-light"></i></div><div class="freezer-glass"><div class="bottles"><b></b><b></b><b></b><b></b></div><div class="shelf"></div><div class="bottles lower"><b></b><b></b><b></b><b></b></div><span class="glass-mark">01</span></div><div class="freezer-controls"><div><small>当前温度</small><strong><span id="temperature-value">-20</span><em>°C</em></strong></div><span id="device-mode">正常保鲜</span></div><div class="freezer-vent"></div></div>
<div class="device-summary"><span>目标温度 <b>−18°C</b></span><span>设备执行 <b id="device-actions" data-executions="0">0 次</b></span></div>
<button id="trigger" class="alarm-button" disabled><span>↗</span> 触发温度异常</button><p class="quiet">模拟柜门未关好：温度升高至 −6°C，并向门店群发送告警。</p><div id="device-progress" role="status">等待开始演示</div><button id="device-retry" class="retry-publication" hidden>重试确认设备消息</button><button id="receipt-retry" class="retry-publication" hidden>补发执行回执</button><div class="device-footnote"><span>MQTT 5 · QoS 1</span><span>模拟设备 / 真实消息</span></div></section>
<section class="panel staff-panel" id="staff-panel"><div class="panel-heading"><div><span class="section-label">STAFF WORKSPACE</span><h2><span class="staff-icon">◈</span>店员工作台</h2></div><div class="staff-controls"><span id="staff-status" class="connection">未连接</span><button id="staff-disconnect" disabled>断开工作台</button></div></div>
<div class="staff-overview"><span class="avatar">悟</span><div><strong>小悟店员</strong><p>门店群告警 · 个人设备指令</p></div><span class="session-label" id="staff-session-status">会话未建立</span></div>
<div class="alert-card"><div><span class="alert-icon">!</span><div><strong>冷柜 01</strong><p id="alert-detail">设备正常，等待新的告警。</p></div><span id="alert-state">运行正常</span></div><button id="send-command" class="primary" disabled>开启强制制冷 <span>→</span></button></div>
<ol class="steps"><li><span>1</span><div><strong>指令提交</strong><small id="command-puback">未发送</small></div></li><li><span>2</span><div><strong>设备执行</strong><small id="execution-state">等待回执</small></div></li><li><span>3</span><div><strong>异常恢复</strong><small id="recovery-state">等待恢复</small></div></li></ol><div class="receipt-note">提交确认、设备执行回执和温度恢复分别呈现。<button id="repeat-command" disabled>重试同一指令</button></div>
<div class="timeline-heading"><h3>门店消息</h3><span>群告警 / 私聊回执</span></div><div id="staff-events" class="messages" aria-label="店员收到的消息"><p class="empty">告警与处置进度会出现在这里。</p></div><p id="offline-hint" class="offline-hint" hidden>工作台已离线。设备仍可发告警，重连后检查会话恢复和积压消息。</p></section>
</div>
<section class="colleague-section"><div class="colleague-intro"><div><span class="section-label">NEXT STEP · IM 互通</span><h2>把同事也接入这家门店</h2><p>同事使用 IM SDK 加入同一个门店群，接收设备告警，并发送巡检备注。</p></div><div><span id="colleague-status" class="connection">未连接</span><button id="colleague-start" class="secondary" disabled>接入 IM 同事 ↗</button></div></div><div class="colleague-content"><div id="colleague-messages" class="messages" aria-label="IM 同事收到的消息"><p class="empty">接入后，再触发一次告警，观察同事端收到的消息。</p></div><form id="colleague-form"><label for="colleague-note">同事备注</label><textarea id="colleague-note" placeholder="我已安排巡检，请继续观察冷柜。" maxlength="500" disabled></textarea><button id="colleague-send" class="primary" disabled>发送到门店群</button></form></div></section>
<details class="integration"><summary>接入说明与连接参数 <span>⌘</span></summary><div><p>设备和店员直接连接产品 MQTT WebSocket。可信演示后端仅准备用户凭据和门店群成员；收发、指令执行和回执全部发生在浏览器客户端。</p><dl id="parameters"></dl><pre id="connection-code"></pre><p class="quiet">固定 ClientID、clean=false、3600 秒会话有效期。工作台建立订阅后可断开；重连检查 Session Present，恢复时不重新订阅。MessageID 按十进制字符串去重，指令另按 commandId 去重。</p><p class="quiet">页面凭据与演示状态仅保存在当前标签页 sessionStorage；日志不含 Token。首版没有 Will 或实物设备，刷新后恢复当前演示；持久业务执行应由真实设备持久保存去重状态。</p></div></details>
<details class="event-log"><summary>连接与事件日志 <span id="log-count">0 / 120</span></summary><pre id="logs"></pre></details>
<footer>WuKongIM · 同一套用户、频道和消息，连接设备与人。</footer></main></div>
<dialog id="settings"><form id="settings-form"><div class="dialog-heading"><h2>演示连接</h2><button type="button" id="settings-close" aria-label="关闭演示设置">×</button></div><label for="backend-url">演示服务 URL</label><input id="backend-url" type="url" required><p class="quiet">使用内嵌页面时，填写本机 mqttdemo 的 Node 演示服务地址。该服务仅准备账号和群成员。</p><p id="backend-state" class="quiet"></p><button id="settings-save" class="primary">保存设置</button></form></dialog>`;
$<HTMLAnchorElement>('[data-demo-home]').href = demoHomeURL();

function save() {
  try { sessionStorage.setItem(storageKey, JSON.stringify({ backend, state })); } catch { /* A running Demo remains usable when tab storage is unavailable. */ }
}
function log(text: string) {
  state.logs.push({ time: new Date().toLocaleTimeString('zh-CN'), text });
  if (state.logs.length > 120) state.logs.shift();
  $('#logs').textContent = state.logs.map(item => `${item.time}  ${item.text}`).join('\n');
  $('#log-count').textContent = `${state.logs.length} / 120`; save();
}
function error(message: string) { $('#error').textContent = message; }
async function action(run: () => Promise<void>) { error(''); try { await run(); } catch (e) { error(e instanceof Error ? e.message : '操作未完成，请检查连接。'); } finally { render(); save(); } }
function bounded<T>(client: MqttClient, operation: Promise<T>, label: string): Promise<T> {
  return new Promise((resolve, reject) => {
    let settled = false;
    const timer = setTimeout(() => finish(Error(`${label}尚未确认，请保留原指令并检查连接。`)), 10000);
    const failed = () => finish(Error(`${label}连接中断，结果尚未确认。`));
    function finish(cause?: Error, value?: T) {
      if (settled) return; settled = true; clearTimeout(timer); client.off('close', failed); client.off('error', failed);
      cause ? reject(cause) : resolve(value!);
    }
    client.once('close', failed); client.once('error', failed);
    operation.then(value => finish(undefined, value), () => finish(Error(`${label}被拒绝，请检查权限或容量。`)));
  });
}
// Business IDs use UTF-8 canonical base64url; personal topics name the recipient.
function topic(kind: 'users' | 'groups', id: string) {
  const bytes = new TextEncoder().encode(id);
  return `wk/v1/${kind}/${btoa(String.fromCharCode(...bytes)).replaceAll('+', '-').replaceAll('/', '_').replaceAll('=', '')}/messages`;
}
function envelope(content: string, business: Business): Payload { return { type: 1, content, mqtt_demo: business }; }
async function publish(role: Role, destination: string, payload: Payload, key: string) {
  const client = runtime[role].client;
  if (!client || !runtime[role].online) throw Error(`${role === 'device' ? '设备' : '工作台'}已离线，请先连接。`);
  const packet = await bounded(client, client.publishAsync(destination, JSON.stringify(payload), { qos: 1, retain: false, properties: { userProperties: { 'wk.client_msg_no': key } } }), 'MQTT 发布');
  if (packet && 'reasonCode' in packet && Number(packet.reasonCode) >= 128) throw Error('MQTT 发布被拒绝，尚未成功确认。');
  log(`${role === 'device' ? '设备' : '店员'} → ${payload.mqtt_demo.kind} · PUBACK 成功`);
}
function seen(ids: string[], id: string) { if (ids.includes(id)) return true; ids.push(id); if (ids.length > 256) ids.shift(); return false; }
function row(rows: Row[], item: Row) { rows.push(item); if (rows.length > 60) rows.shift(); }
function decode(bytes: Uint8Array): Payload | undefined {
  if (bytes.length > 16384) return;
  try { const payload = JSON.parse(new TextDecoder().decode(bytes)); return payload?.type === 1 && typeof payload.content === 'string' && payload.content.length <= 2000 ? payload : undefined; } catch { return; }
}
function scalar(value: string | string[] | undefined) { return typeof value === 'string' ? value : ''; }
function correlationID(value: unknown): value is string { return typeof value === 'string' && /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/.test(value); }
function receive(role: Role, destination: string, bytes: Uint8Array, properties?: Record<string, string | string[]>) {
  const session = state.session; if (!session) return;
  const payload = decode(bytes), id = scalar(properties?.['wk.message_id']), from = scalar(properties?.['wk.from_uid']);
  if (!payload || !/^[1-9]\d*$/.test(id) || !from) { log('忽略格式不完整的消息'); return; }
  const business = payload.mqtt_demo;
  const group = destination === topic('groups', session.groupId) && scalar(properties?.['wk.channel_type']) === '2' && scalar(properties?.['wk.channel_id']) === session.groupId;
  if (role === 'device') {
    if (destination !== topic('users', session.device.uid) || scalar(properties?.['wk.channel_type']) !== '1' || business?.kind !== 'command' || from !== session.staff.uid || !correlationID(business.alertId) || !correlationID(business.commandId)) return;
    if (seen(state.deviceSeen, id)) { log('设备忽略重复 MessageID'); if (runtime.device.online && state.pendingReceipt?.payload.mqtt_demo.commandId === business.commandId) void action(publishPendingReceipt); return; }
    if (!runtime.device.online) {
      if (pendingDevice.length >= 32) { error('恢复期间设备指令过多，请重新连接并检查原指令。'); runtime.device.client?.end(true); return; }
      pendingDevice.push({ command: business, sender: from });
    } else void action(() => execute(business, from));
    return;
  }
  if (seen(state.staffSeen, id)) { log('工作台忽略重复 MessageID'); return; }
  let kind = business?.kind || 'note';
  if (group && from === session.device.uid && business?.kind === 'alert' && correlationID(business.alertId) && business.temperature === -6) {
    if (state.staffAlert?.id !== business.alertId) {
      state.staffAlert = { id: business.alertId, state: 'waiting' }; state.command = undefined;
      state.commandAck = '未发送'; state.execution = '等待回执'; state.recovery = '等待恢复';
    }
  } else if (group && from === session.device.uid && business?.kind === 'recovery' && state.staffAlert && correlationID(business.alertId) && business.alertId === state.staffAlert.id && typeof business.temperature === 'number' && business.temperature <= -18) {
    state.staffAlert!.state = 'recovered'; state.recovery = '已恢复';
  } else if (!group && destination === topic('users', session.staff.uid) && from === session.device.uid && business?.kind === 'receipt' && state.command && state.staffAlert && correlationID(business.commandId) && correlationID(business.alertId) && business.commandId === state.command.commandId && business.alertId === state.staffAlert.id && business.status === 'executed') {
    state.execution = '设备已执行'; if (state.staffAlert!.state !== 'recovered') state.staffAlert!.state = 'cooling';
  } else if (!(group && [session.staff.uid, session.colleague.uid].includes(from) && (kind === 'note' || !business))) return;
  row(state.staffRows, { id, kind, from, commandId: business?.commandId, text: payload.content, time: new Date().toLocaleTimeString('zh-CN') });
  log(`店员 ← ${kind} · MessageID ${id}`); render(); save();
}
async function connect(role: Role) {
  const session = state.session; if (!session || runtime[role].connecting) return;
  const identity = session[role], current = runtime[role], generation = ++current.generation;
  current.client?.end(true); current.online = false; current.connecting = true; render();
  const client = mqtt.connect(session.mqttWsUrl, {
    manualConnect: true, protocolVersion: 5, clientId: identity.clientId,
    username: identity.uid, password: identity.token, clean: false,
    reconnectPeriod: 0, resubscribe: false, queueQoSZero: false, connectTimeout: 10000,
    properties: { sessionExpiryInterval: 3600, receiveMaximum: 16, maximumPacketSize: 65536, userProperties: { 'wk.device_flag': '1' } },
  });
  current.client = client;
  client.on('message', (destination, bytes, packet) => { if (generation === current.generation) receive(role, destination, bytes, packet.properties?.userProperties); });
  client.on('error', () => { if (generation === current.generation) log(`${role === 'device' ? '设备' : '店员'} MQTT 连接异常，请检查服务或凭据`); });
  client.on('close', () => { if (generation === current.generation) { current.online = false; render(); } });
  try {
    const connack = await new Promise<{ sessionPresent: boolean }>((resolve, reject) => {
      const timer = setTimeout(() => finish(Error('MQTT 连接超时，请检查 WebSocket 地址。')), 11000);
      const onError = () => finish(Error('MQTT 连接失败，请检查凭据和 WebSocket 服务。'));
      const onClose = () => finish(Error('MQTT 连接已关闭，请检查 WebSocket 服务。'));
      const onConnect = (packet: { sessionPresent: boolean }) => finish(undefined, packet);
      function finish(cause?: Error, value?: { sessionPresent: boolean }) { clearTimeout(timer); client.off('error', onError); client.off('close', onClose); client.off('connect', onConnect); cause ? reject(cause) : resolve(value!); }
      client.once('error', onError); client.once('close', onClose); client.once('connect', onConnect); client.connect();
    });
    current.present = connack.sessionPresent;
    if (!connack.sessionPresent) {
      const targets = role === 'device' ? [topic('users', identity.uid)] : [topic('users', identity.uid), topic('groups', session.groupId)];
      const grants = await bounded(client, client.subscribeAsync(targets, { qos: 1 }), 'MQTT 订阅');
      if (grants.length !== targets.length || grants.some(grant => grant.qos !== 1)) throw Error('MQTT 订阅未获授权，请检查门店群成员。');
    }
    if (generation !== current.generation) return;
    current.online = true; log(`${role === 'device' ? '设备' : '店员'}已连接 · Session Present=${connack.sessionPresent}`);
    if (role === 'device') for (const item of pendingDevice.splice(0)) await action(() => execute(item.command, item.sender));
    if (role === 'device' && state.pendingReceipt) await action(publishPendingReceipt);
    if (role === 'device' && state.deviceAlert?.status === 'cooling') beginCooling();
  } catch (cause) { client.end(true); throw cause; }
  finally { if (generation === current.generation) { current.connecting = false; render(); } }
}
async function disconnectStaff() {
  const current = runtime.staff; if (!current.client) return;
  // Graceful DISCONNECT retains the nonzero expiry and established subscriptions.
  await new Promise<void>((resolve, reject) => {
    const timer = setTimeout(() => { current.client?.end(true); reject(Error('工作台断开尚未确认，请稍后重连。')); }, 5000);
    current.client!.end(false, {}, () => { clearTimeout(timer); resolve(); });
  });
  current.online = false; log('工作台已离线 · 保留 ClientID 与门店群订阅'); render();
}
async function trigger() {
  if (triggerBusy || state.pendingReceipt || state.deviceAlert && state.deviceAlert.status !== 'recovered') return;
  triggerBusy = true;
  try {
    const alertId = crypto.randomUUID(); state.deviceAlert = { id: alertId, status: 'alarm' }; state.temperature = -6; render(); save();
    await publishDeviceAlert();
    log('设备告警已提交，等待店员处置');
  } finally { triggerBusy = false; }
}
async function publishDeviceAlert() {
  const alert = state.deviceAlert!;
  await publish('device', topic('groups', state.session!.groupId), envelope('冷柜 01 温度异常：当前 −6°C，高于保鲜目标 −18°C，请及时处理。', { kind: 'alert', alertId: alert.id, temperature: -6 }), `${alert.id}-alert`);
  alert.alertSent = true; save();
}
async function publishRecovery() {
  const alert = state.deviceAlert!; alert.recoveryKey ||= `${alert.id}-recovery`; save();
  await publish('device', topic('groups', state.session!.groupId), envelope('冷柜 01 已恢复：温度降至 −18°C，本次告警关闭。', { kind: 'recovery', alertId: alert.id, temperature: -18 }), alert.recoveryKey);
  alert.recoverySent = true; save();
}
async function sendCommand(retry = false) {
  if (commandBusy || !state.staffAlert || !runtime.staff.online) return;
  commandBusy = true;
  try {
    if (!retry || !state.command) {
      const commandId = crypto.randomUUID(), alertId = state.staffAlert.id;
      state.command = { commandId, alertId, key: `${commandId}-command`, payload: envelope('请为冷柜 01 开启强制制冷，恢复至 −18°C。', { kind: 'command', alertId, commandId, action: 'force_cooling' }) };
      state.execution = '等待回执'; state.recovery = '等待恢复';
    }
    state.commandAck = '等待确认'; save(); render();
    await publish('staff', topic('users', state.session!.device.uid), state.command.payload, state.command.key);
    state.commandAck = '服务端已确认';
  } catch (cause) { state.commandAck = '结果待确认'; throw cause; }
  finally { commandBusy = false; }
}
async function execute(command: Business, authenticatedSender: string) {
  const session = state.session!;
  if (command.action !== 'force_cooling' || !correlationID(command.commandId) || !correlationID(command.alertId) || !state.deviceAlert || command.alertId !== state.deviceAlert.id) { log('设备拒绝不匹配的处置指令'); return; }
  const duplicate = state.executed.includes(command.commandId);
  if (!duplicate) {
    if (state.pendingReceipt) { log('设备保留前一条待确认回执，暂不执行新指令'); return; }
    state.executed.push(command.commandId); if (state.executed.length > 64) state.executed.shift();
    state.pendingReceipt = { target: authenticatedSender, payload: envelope('冷柜 01 已开启强制制冷，正在降低温度。', { kind: 'receipt', alertId: command.alertId, commandId: command.commandId, status: 'executed' }), key: `${command.commandId}-receipt` };
    state.executions++; state.deviceAlert!.status = 'cooling';
    // Save command deduplication before the simulated device effect or receipt.
    save(); render(); log('设备收到认证店员指令，已开启强制制冷'); beginCooling();
  } else log('设备忽略重复 commandId，复用原执行回执');
  // The reply target comes from the authenticated MQTT metadata, not the payload.
  if (state.pendingReceipt) await publishPendingReceipt();
  else await publish('device', topic('users', authenticatedSender), envelope('冷柜 01 已开启强制制冷，正在降低温度。', { kind: 'receipt', alertId: command.alertId, commandId: command.commandId, status: 'executed' }), `${command.commandId}-receipt`);
  if (session.device.uid !== state.session?.device.uid) return;
}
async function publishPendingReceipt() {
  if (!state.pendingReceipt || receiptBusy) return;
  receiptBusy = true; const receipt = state.pendingReceipt;
  try { await publish('device', topic('users', receipt.target), receipt.payload, receipt.key); if (state.pendingReceipt === receipt) state.pendingReceipt = undefined; save(); }
  finally { receiptBusy = false; render(); }
}
function beginCooling() {
  if (coolingTimer) clearInterval(coolingTimer);
  coolingTimer = setInterval(() => {
    if (!runtime.device.online || state.deviceAlert?.status !== 'cooling') return;
    state.temperature = Math.max(-18, state.temperature - 2); render(); save();
    if (state.temperature <= -18) {
      clearInterval(coolingTimer); coolingTimer = undefined;
      const alert = state.deviceAlert; alert.status = 'recovered'; alert.recoveryKey ||= `${alert.id}-recovery`; save(); render();
      void action(publishRecovery);
    }
  }, 600);
}
function renderRows(target: string, rows: Row[]) {
  const container = $(target), nearEnd = container.scrollHeight - container.scrollTop - container.clientHeight < 80;
  container.replaceChildren();
  if (!rows.length) { const empty = document.createElement('p'); empty.className = 'empty'; empty.textContent = target.includes('colleague') ? '接入后，再触发一次告警，观察同事端收到的消息。' : '告警与处置进度会出现在这里。'; container.append(empty); return; }
  for (const item of rows) {
    const message = document.createElement('article'); message.className = 'message'; message.dataset.kind = item.kind; message.dataset.messageId = item.id;
    if (item.commandId) message.dataset.commandId = item.commandId;
    const labels: Record<string, string> = { alert: '设备告警', receipt: '设备执行回执', recovery: '设备恢复', note: '同事备注' };
    const heading = document.createElement('div'), label = document.createElement('strong'), time = document.createElement('time'), content = document.createElement('p');
    label.textContent = labels[item.kind] || '门店消息'; time.textContent = item.time; content.textContent = item.text;
    heading.append(label, time); message.append(heading, content); container.append(message);
  }
  if (nearEnd) container.scrollTop = container.scrollHeight;
}
function render() {
  $('#start-card').hidden = !!state.session;
  $<HTMLButtonElement>('#start').disabled = starting;
  $('#start').textContent = starting ? '正在连接…' : '开始演示 ↗';
  for (const role of ['device', 'staff'] as Role[]) {
    const current = runtime[role]; $('#' + role + '-status').textContent = current.online ? '在线' : current.connecting ? '连接中' : state.session ? '离线' : '未连接';
    $('#' + role + '-status').classList.toggle('online', current.online);
  }
  $('#staff-session-status').textContent = runtime.staff.present ? '会话已恢复' : runtime.staff.online ? '已建立新会话' : '会话未建立';
  $('#temperature-value').textContent = String(state.temperature);
  $('.freezer').classList.toggle('alarm', state.deviceAlert?.status === 'alarm');
  $('.freezer').classList.toggle('cooling', state.deviceAlert?.status === 'cooling');
  $('#device-mode').textContent = state.deviceAlert?.status === 'alarm' ? '温度异常' : state.deviceAlert?.status === 'cooling' ? '强制制冷中' : '正常保鲜';
  $('#device-actions').textContent = `${state.executions} 次`; $('#device-actions').dataset.executions = String(state.executions);
  $<HTMLButtonElement>('#trigger').disabled = !runtime.device.online || triggerBusy || !!state.pendingReceipt || !!state.deviceAlert && state.deviceAlert.status !== 'recovered';
  $('#device-reconnect').hidden = !state.session || runtime.device.online;
  $<HTMLButtonElement>('#device-reconnect').disabled = runtime.device.connecting;
  $('#device-retry').hidden = !runtime.device.online || !state.deviceAlert || state.deviceAlert.status === 'cooling' || state.deviceAlert.status === 'alarm' && !!state.deviceAlert.alertSent || state.deviceAlert.status === 'recovered' && !!state.deviceAlert.recoverySent;
  $('#receipt-retry').hidden = !runtime.device.online || !state.pendingReceipt;
  $<HTMLButtonElement>('#receipt-retry').disabled = receiptBusy;
  $('#device-progress').textContent = !state.session ? '等待开始演示' : state.pendingReceipt ? '设备已执行，回执待确认；连接恢复后可补发原回执。' : state.deviceAlert?.status === 'alarm' ? '已触发异常，等待店员指令' : state.deviceAlert?.status === 'cooling' ? '已执行指令，温度逐渐下降中…' : state.deviceAlert?.status === 'recovered' ? '温度已达目标，本次告警关闭' : '设备就绪，可以触发温度异常';
  const alert = state.staffAlert;
  $('#alert-state').textContent = alert?.state === 'waiting' ? '等待处置' : alert?.state === 'cooling' ? '制冷中' : alert?.state === 'recovered' ? '已恢复' : '运行正常';
  $('#alert-state').dataset.state = alert?.state || 'normal';
  $('#alert-detail').textContent = alert?.state === 'waiting' ? '温度达到 −6°C，请远程开启强制制冷。' : alert?.state === 'cooling' ? '设备已收到指令，正在恢复目标温度。' : alert?.state === 'recovered' ? '目标温度已恢复，本次告警关闭。' : '设备正常，等待新的告警。';
  $<HTMLButtonElement>('#send-command').disabled = !runtime.staff.online || !alert || alert.state !== 'waiting' || !!state.command || commandBusy;
  $<HTMLButtonElement>('#repeat-command').disabled = !runtime.staff.online || !state.command || commandBusy;
  $('#command-puback').textContent = state.commandAck; $('#execution-state').textContent = state.execution; $('#recovery-state').textContent = state.recovery;
  $<HTMLButtonElement>('#staff-disconnect').disabled = !state.session || runtime.staff.connecting;
  $('#staff-disconnect').textContent = runtime.staff.online ? '断开工作台' : '重连工作台';
  $('#offline-hint').hidden = !state.session || runtime.staff.online || runtime.staff.connecting;
  $<HTMLButtonElement>('#colleague-start').disabled = !state.session || colleagueOnline || colleagueConnecting;
  $('#colleague-status').textContent = colleagueOnline ? '在线' : colleagueConnecting ? '连接中' : '未连接'; $('#colleague-status').classList.toggle('online', colleagueOnline);
  $<HTMLTextAreaElement>('#colleague-note').disabled = !colleagueOnline;
  $<HTMLButtonElement>('#colleague-send').disabled = !colleagueOnline;
  renderRows('#staff-events', state.staffRows); renderRows('#colleague-messages', state.colleagueRows);
  if (state.session) {
    const session = state.session;
    $('#parameters').replaceChildren();
    for (const [label, value] of [['MQTT WebSocket', session.mqttWsUrl], ['设备 UID', session.device.uid], ['店员 UID', session.staff.uid], ['设备 ClientID', session.device.clientId], ['工作台 ClientID', session.staff.clientId], ['群 Topic', topic('groups', session.groupId)], ['设备收件箱', topic('users', session.device.uid)]]) {
      const term = document.createElement('dt'), detail = document.createElement('dd'); term.textContent = label; detail.textContent = value; $('#parameters').append(term, detail);
    }
    $('#connection-code').textContent = `mqtt.connect(mqttWsUrl, {\n  protocolVersion: 5, clientId: stableClientId,\n  username: uid, password: token, clean: false,\n  reconnectPeriod: 0, resubscribe: false,\n  properties: { sessionExpiryInterval: 3600,\n    userProperties: { 'wk.device_flag': '1' } }\n});\n\nclient.publish(groupTopic, JSON.stringify({\n  type: 1, content: '冷柜温度异常',\n  mqtt_demo: { kind: 'alert', alertId }\n}), { qos: 1, retain: false, properties: {\n  userProperties: { 'wk.client_msg_no': stableMessageKey }\n}});`;
  }
}
async function startColleague() {
  if (!state.session || colleagueConnecting || colleagueOnline) return;
  colleagueConnecting = true; render();
  const session = state.session, generation = ++colleagueGeneration;
  colleague?.destroy();
  const client = WKIM.init(session.wsUrl, { uid: session.colleague.uid, token: session.colleague.token, deviceFlag: 1 }, { singleton: false }); colleague = client;
  client.on(WKIMEvent.Message, (message: any) => {
    if (generation !== colleagueGeneration || message.channelId !== session.groupId || message.channelType !== 2) return;
    const id = String(message.messageId || message.messageID || message.clientMsgNo || '');
    if (!id || seen(state.colleagueSeen, id)) return;
    let payload = message.payload;
    if (typeof payload === 'string') try { payload = JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(payload), c => c.charCodeAt(0)))); } catch { return; }
    if (payload?.type !== 1 || typeof payload.content !== 'string') return;
    row(state.colleagueRows, { id, from: message.fromUid, kind: payload.mqtt_demo?.kind || 'note', text: payload.content, time: new Date().toLocaleTimeString('zh-CN') }); log('IM 同事 ← 门店群消息'); render(); save();
  });
  client.on(WKIMEvent.Connect, () => { if (generation === colleagueGeneration) { colleagueOnline = true; render(); } });
  client.on(WKIMEvent.Disconnect, () => { if (generation === colleagueGeneration) { colleagueOnline = false; render(); } });
  client.on(WKIMEvent.Error, () => log('IM 同事连接异常，请检查 WebSocket 地址'));
  try { await client.connect(); log('IM 同事已通过 EasySDK 接入门店群'); }
  finally { colleagueConnecting = false; render(); }
}
$('#start').onclick = () => void action(async () => {
  starting = true; render();
  try {
    let response;
    try { response = await fetch(backend + '/mqttdemo/api/session', { method: 'POST', headers: { 'content-type': 'application/json' }, body: '{}', signal: AbortSignal.timeout(30000) }); }
    catch { throw Error('演示服务未连接，请在演示设置中检查服务 URL，并启动 mqttdemo 的 npm start。'); }
    const session = await response.json(); if (!response.ok) throw Error(session.error || '演示服务未完成账号准备。');
    state = { ...fresh(), session }; save(); log('已准备独立演示身份与门店群');
    await Promise.all([connect('device'), connect('staff')]);
  } finally { starting = false; }
});
$('#trigger').onclick = () => void action(trigger);
$('#device-reconnect').onclick = () => void action(() => connect('device'));
$('#device-retry').onclick = () => void action(() => state.deviceAlert?.status === 'recovered' ? publishRecovery() : publishDeviceAlert());
$('#receipt-retry').onclick = () => void action(publishPendingReceipt);
$('#send-command').onclick = () => void action(() => sendCommand());
$('#repeat-command').onclick = () => void action(() => sendCommand(true));
$('#staff-disconnect').onclick = () => void action(() => runtime.staff.online ? disconnectStaff() : connect('staff'));
$('#colleague-start').onclick = () => void action(startColleague);
$('#colleague-form').onsubmit = event => { event.preventDefault(); void action(async () => {
  const input = $<HTMLTextAreaElement>('#colleague-note'), content = input.value.trim(); if (!content || !colleagueOnline || !colleague) return;
  const ack = await colleague.send(state.session!.groupId, 2, envelope(content, { kind: 'note' }), { clientMsgNo: crypto.randomUUID() });
  if (ack.reasonCode !== 1) throw Error('IM 同事备注未提交成功，请保留内容。');
  input.value = ''; log('IM 同事 → 门店群备注 · SENDACK 成功');
}); };
document.querySelectorAll<HTMLButtonElement>('[data-view]').forEach(button => { button.onclick = () => { $('.workspace').dataset.currentView = button.dataset.view; document.querySelectorAll('[data-view]').forEach(item => item.classList.toggle('active', item === button)); }; });
$('#settings-toggle').onclick = () => { $<HTMLInputElement>('#backend-url').value = backend; $<HTMLInputElement>('#backend-url').disabled = !!state.session; $('#backend-state').textContent = state.session ? '当前演示已建立。如需其他部署，请在新标签页开启新的演示。' : ''; $<HTMLDialogElement>('#settings').showModal(); };
$('#settings-close').onclick = () => $<HTMLDialogElement>('#settings').close();
$('#settings-form').onsubmit = event => { event.preventDefault(); if (!state.session) {
  try { const value = new URL($<HTMLInputElement>('#backend-url').value); if (!['http:', 'https:'].includes(value.protocol) || value.username || value.password || value.search || value.hash) throw Error(); backend = value.origin; save(); }
  catch { error('演示服务 URL 无效。'); return; }
} $<HTMLDialogElement>('#settings').close(); };
window.addEventListener('pagehide', () => { save(); if (coolingTimer) clearInterval(coolingTimer); for (const current of Object.values(runtime)) current.client?.end(true); colleague?.disconnect(); });
try {
  const saved = sessionStorage.getItem(storageKey);
  if (saved) { const parsed = JSON.parse(saved); if (parsed.state?.session?.id && parsed.state.staffRows && parsed.state.executed) { state = parsed.state; backend = parsed.backend; } else if (parsed.backend) backend = parsed.backend; }
} catch { state = fresh(); }
$('#logs').textContent = state.logs.map(item => `${item.time}  ${item.text}`).join('\n'); $('#log-count').textContent = `${state.logs.length} / 120`;
render();
if (state.session) void action(async () => { await Promise.all([connect('device'), connect('staff')]); });
