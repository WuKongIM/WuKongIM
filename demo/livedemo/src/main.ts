import { WKIM, WKIMEvent, type RecvMessage } from 'easyjssdk';
import { demoHomeURL } from '../../shared/home';
import '../../shared/home.css';
import './style.css';
import { bootstrap, snapshot, notification, controlResult, decodePayload, uuid, type Bootstrap, type Snapshot, type Notification, type ControlIntent, type AudiencePayload } from './contracts';
import { LiveStage } from './stage';

type ChatRow = { id: string; fromUid: string; name: string; content: string; kind: 'barrage' | 'like'; source: 'local' | 'incoming'; time: string };
type SendIntent = { eventId: string; payload: AudiencePayload; status: 'sending' | 'unknown' | 'rejected'; reason?: string };
type Denied = { version: number; observedRestriction: boolean };
type HostState = { key: string; intent?: ControlIntent; operationStatus?: 'unknown' | 'pending'; notification?: Notification };
type Saved = { backend: string; session: Bootstrap; rows: ChatRow[]; seen: string[]; draft: string; joinNonce?: string; send?: SendIntent; denied?: Denied };
const $ = <T extends HTMLElement = HTMLElement>(selector: string) => document.querySelector<T>(selector)!;
const test = <T extends HTMLElement = HTMLElement>(id: string) => $<T>(`[data-testid="${id}"]`);
const storageKey = 'wk-live-demo-v1';
const query = new URLSearchParams(location.search), joinNonce = query.get('joinNonce') || undefined, inviteNonce = crypto.randomUUID();
const defaultBackend = () => {
  const value = document.querySelector<HTMLMetaElement>('meta[name="wk-live-backend"]')?.content;
  return value === 'same-origin' ? location.origin : value || 'http://127.0.0.1:5180';
};
function backendURL(value: string) { const url = new URL(value); if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash || !['', '/'].includes(url.pathname)) throw Error('请输入演示业务服务的 HTTP 地址。'); return url.origin; }
let backend = backendURL(defaultBackend()), session: Bootstrap | undefined, host: HostState | undefined;
let client: WKIM | undefined, generation = 0, recoveryEpoch = 0, online = false, ready = false, busy = false, controlling = false, recovering = false;
let connection = '尚未加入', sendIntent: SendIntent | undefined, denied: Denied | undefined, rows: ChatRow[] = [], seen: string[] = [], buffered: Snapshot[] = [], logs: string[] = [];
let notificationStatus = '', sendStatus = '', draft = '', visuals = true, scene = 0, lastSendAt = 0;

$('#app').innerHTML = `<div class="live-app"><header class="topbar"><a class="brand" data-demo-home><b>W</b>WuKongIM <small>LIVE DEMO</small></a><div class="top-actions"><a class="demo-home-link" data-demo-home><span aria-hidden="true">←</span>返回首页</a><button id="settings-open">演示设置 <span>⚙</span></button></div></header>
<section class="intro"><div><span class="eyebrow">A MOMENT, SHARED TOGETHER</span><h1>小悟新品发布会</h1><p>发一条弹幕，加入现场。把第一声期待，分享给正在观看的人。</p></div><span class="chain"><i></i>真实消息链路</span></section>
<div id="error" role="alert"></div><div id="start-card"><div><strong>欢迎来到发布会现场</strong><p id="start-description">进入直播间，准备你的观众身份和小悟的管理面板。</p></div><button class="primary" data-testid="start">进入直播间 <span>↗</span></button></div>
<div class="session-line"><span data-testid="connection">尚未加入</span><span data-testid="identity">等待观众入场</span></div>
<nav class="mobile-tabs" aria-label="直播间角色"><button data-testid="role-viewer" class="active">观众</button><button data-testid="role-host">主播 / 房管</button></nav>
<main class="workspace" data-view="viewer"><section class="audience-panel"><div class="stage" data-testid="stage"><div class="stage-header"><span><i></i>小悟新品发布会</span><span>演示画面</span></div><div class="stage-notice"><span>主播公告</span><p data-testid="notice-current">进入后获取当前公告</p></div>
<div class="product-scene"><div class="product-visual"><svg viewBox="0 0 300 260" role="img" aria-label="Studio One 新品示意图"><defs><linearGradient id="front" x2=".7" y2="1"><stop stop-color="#dce3ca"/><stop offset="1" stop-color="#a8bba0"/></linearGradient><linearGradient id="side" x2="1" y2="1"><stop stop-color="#6c956e"/><stop offset="1" stop-color="#315740"/></linearGradient></defs><ellipse cx="155" cy="228" rx="119" ry="17" fill="#0b2117"/><path d="m62 67 133-30 46 30-137 33Z" fill="#8aab8b"/><path d="m62 67 42 33v113l-42-37Z" fill="url(#side)"/><path d="m104 100 137-33v126l-137 20Z" fill="url(#front)"/><path d="m122 113 101-23v64l-101 19Z" fill="#163a29"/><path d="m137 129 69-16M137 142l44-10M137 156l56-13" stroke="#b9d19e" stroke-width="5" stroke-linecap="round"/><circle cx="214" cy="173" r="9" fill="#58805c"/><circle cx="213" cy="172" r="3" fill="#c9dcaf"/><path d="m121 188 60-11" stroke="#6a896b" stroke-width="3"/></svg></div><div class="product-copy"><span>STUDIO ONE</span><h2 id="scene-title">灵感，即刻发生。</h2><p id="scene-description">看见设计的每一面，让日常多一点惊喜。</p><div id="scene-chips"><span>轻巧轮廓</span><span>自然触感</span></div></div></div><div id="live-visuals" aria-hidden="true"></div><div class="stage-bottom"><span>内置新品介绍 · <b id="scene-index">01 / 03</b></span><div class="scene-tabs"><button data-scene="0" class="active" aria-label="介绍外观">外观</button><button data-scene="1" aria-label="介绍特点">特点</button><button data-scene="2" aria-label="现场提问画面">现场提问</button></div></div></div>
<div class="audience-toolbar"><button data-testid="barrage-toggle" aria-pressed="true">弹幕已开启</button><div><a data-testid="invite-link" target="_blank" rel="noopener" hidden>邀请链接 ↗</a><button data-testid="open-viewer">打开第二位观众 ↗</button></div></div><p class="invitation-note">第二个标签页是一位新的观众。彼此发一条消息，看看另一端真正收到的内容。</p>
<div class="scene-note"><span>01 入场互动</span><i>→</i><span>02 主播公告</span><i>→</i><span>03 房间管理</span></div>
</section><aside class="side-panel"><section class="chat-panel"><header class="panel-header"><div><span class="eyebrow">LIVE CHAT</span><h2>现场聊天</h2></div><span id="viewer-name" class="viewer-badge">观众</span></header><div class="chat" data-testid="chat" role="log" aria-label="现场聊天"></div><p class="permission" data-testid="permission">加入后即可发言与点赞。</p><form id="composer"><textarea data-testid="send-input" maxlength="240" rows="2" aria-label="弹幕正文" placeholder="分享此刻的期待…" disabled></textarea><div class="composer-actions"><button type="button" data-testid="like">♡ 点赞</button><span id="input-length">0 / 120</span><button class="primary" data-testid="send" disabled>发送 ↑</button></div></form><p id="send-status" role="status"></p><button class="retry" data-testid="retry-send" hidden>重试原消息</button><div class="connection-actions"><button data-testid="disconnect">断开连接</button><button data-testid="reconnect">重新连接</button><button data-testid="leave">离开直播间</button></div></section>
<section class="host-panel" id="host-panel" hidden><header class="panel-header"><div><span class="eyebrow">HOST DESK</span><h2>小悟 · 主播管理</h2></div><span class="host-badge">创建者</span></header><form id="notice-form"><label for="notice-text">置顶公告</label><textarea id="notice-text" data-testid="notice-text" maxlength="240" rows="2" placeholder="演示结束后开放提问"></textarea><button class="primary" data-testid="notice-publish">发布公告</button></form><label for="viewer-select">演示观众 / 已加入</label><select id="viewer-select" data-testid="viewer-select" aria-label="选择管理的观众"></select><div class="manage-actions"><button data-testid="mute">禁言本观众</button><button data-testid="unmute">解除禁言</button></div><p data-testid="control-status" role="status">加入后可管理本房间。</p><div class="host-retries"><button class="retry" data-testid="retry-operation" hidden>重试同一管理操作</button><button class="retry" data-testid="retry-notification" hidden>重试状态通知</button></div><p class="host-hint">禁言限制该观众在本房间发言和点赞，仍能接收消息。临时断线后恢复当前公告与权限。</p><button class="close-room" data-testid="close">结束这场演示</button></section></aside></main>
<details class="integration"><summary>接入示例与实际连接参数 <span>⌘</span></summary><p>观众使用 EasySDK 直接连接 WuKongIM。演示业务服务准备身份、管理本房间权限并保存当前快照；弹幕与点赞不存历史、不补离线消息。</p><dl id="parameters"></dl><pre id="integration-code"></pre><p>本人提交成功与对方实际收到分别呈现。未知发送可用原 UUID 手动重试；当前快照只接受真实主播和更新版本。</p></details>
<details class="event-record"><summary>事件记录 <span>有界 · 不包含凭据</span></summary><pre id="logs"></pre></details><footer>Powered by WuKongIM · EasySDK <span>连接此刻，也连接每一个人。</span></footer></div>
<dialog id="settings"><form id="settings-form"><header><h2>演示设置</h2><button id="settings-close" type="button" aria-label="关闭设置">×</button></header><label for="backend-url">直播演示业务服务 URL</label><input id="backend-url" data-testid="settings-backend" type="url" spellcheck="false"><p>连接提供身份、当前房间状态和管理操作的演示业务服务。</p><button class="primary" data-testid="backend-save">保存设置</button></form></dialog>`;
document.querySelectorAll<HTMLAnchorElement>('[data-demo-home]').forEach(link => { link.href = demoHomeURL(); });
const stage = new LiveStage($('#live-visuals'));

function hostStorageKey() { return session ? `wk-live-host-owner-cap-v1:${session.roomId}:${session.viewerKey}` : ''; }
// Viewer recovery never includes the isolated ownership capability.
function persist() {
  if (!session) return;
  const { ownerKey: _, ...viewerSession } = session;
  const saved: Saved = { backend, session: viewerSession, rows, seen, draft: test<HTMLTextAreaElement>('send-input').value, joinNonce, send: sendIntent, denied };
  try { sessionStorage.setItem(storageKey, JSON.stringify(saved)); if (host) sessionStorage.setItem(hostStorageKey(), JSON.stringify(host)); } catch { /* Storage is optional for the live page, but refresh may require a new room. */ }
}
function log(value: string) { logs.push(`${new Date().toLocaleTimeString('zh-CN')}  ${value}`); logs = logs.slice(-100); $('#logs').textContent = logs.join('\n'); }
function error(value: unknown) { $('#error').textContent = value instanceof Error ? value.message : '操作未完成，请稍后重试。'; }
function forget() { try { if (session) sessionStorage.removeItem(hostStorageKey()); sessionStorage.removeItem(storageKey); } catch {} session = undefined; host = undefined; rows = []; seen = []; sendIntent = undefined; denied = undefined; draft = ''; }
class ApiError extends Error { constructor(message: string, readonly status: number, readonly code: string) { super(message); } }
async function request(path: string, body?: unknown, credential?: string) {
  let response: Response;
  try { response = await fetch(backend + '/livedemo/api/' + path, { method: body === undefined ? 'GET' : 'POST', headers: { 'content-type': 'application/json', ...(credential ? { Authorization: 'Bearer ' + credential } : {}) }, body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(30000) }); }
  catch { throw Error('演示业务服务未确认请求，请检查连接后重试。'); }
  let value: any; try { value = await response.json(); } catch { throw Error('演示业务服务返回了无法识别的结果。'); }
  if (!response.ok) throw new ApiError(typeof value.error === 'string' ? value.error : '演示业务请求未完成。', response.status, typeof value.code === 'string' ? value.code : '');
  return value;
}
function markSeen(keys: string[]) { if (keys.some(key => seen.includes(key))) return false; seen.push(...keys); seen = seen.slice(-512); return true; }
function self() { return session?.snapshot.viewers.find(viewer => viewer.uid === session?.viewer.uid); }
function canSend() { const viewer = self(); return !!session && online && ready && !!viewer && !viewer.muted && !viewer.pending && !denied && sendIntent?.status !== 'sending' && sendIntent?.status !== 'unknown'; }
function append(row: ChatRow) { if (rows.some(item => item.id === row.id && item.fromUid === row.fromUid && item.kind === row.kind)) return; rows.push(row); rows = rows.slice(-100); renderChat(); if (online && ready) stage.enqueue(row.kind === 'like' ? `${row.name} ♡` : `${row.name}：${row.content}`, row.kind); persist(); }
// A server denial clears only after a known restriction advances to confirmed permission.
function reconcilePermission(value: Snapshot) {
  const viewer = value.viewers.find(item => item.uid === session?.viewer.uid);
  if (!denied || !viewer) return;
  if (viewer.muted || viewer.pending) { denied.observedRestriction = true; denied.version = Math.max(denied.version, value.version); }
  else if (denied.observedRestriction && value.version > denied.version) denied = undefined;
}
function applySnapshot(value: Snapshot) {
  if (!session || value.version <= session.snapshot.version) return;
  if (!value.viewers.some(viewer => viewer.uid === session?.viewer.uid)) { retire(); forget(); connection = '本页已离开房间'; log('更新的主播快照确认本页已不在房间，停止发送'); render(); return; }
  session.snapshot = value;
  reconcilePermission(value);
  log(`当前房间快照 v${value.version} · 公告 / 权限 / 观众名单已同步`); persist(); render();
}
function receive(message: RecvMessage, activeGeneration: number) {
  if (activeGeneration !== generation || !session || message.channelId !== session.roomId || message.channelType !== 2 || typeof message.fromUid !== 'string') return;
  const payload = decodePayload(message.payload), event = payload?.live_demo;
  if (!payload || payload.type !== 1 || typeof payload.content !== 'string' || !event || event.roomId !== session.roomId || !uuid(event.eventId)) return;
  const keys = [`event:${session.roomId}:${message.fromUid}:${event.kind}:${event.eventId}`];
  if (typeof message.messageId === 'string' && /^\d+$/.test(message.messageId)) keys.push('message:' + message.messageId);
  if (event.kind === 'room_state') {
    if (message.fromUid !== session.hostUid || !snapshot(event.snapshot, session.roomId, session.hostUid) || event.roomVersion !== event.snapshot.version || event.snapshot.version <= session.snapshot.version || !markSeen(keys)) return;
    if (recovering || !ready) { buffered.push(event.snapshot); buffered = buffered.sort((a, b) => b.version - a.version).slice(0, 32); }
    else applySnapshot(event.snapshot);
    log(`SDK ← 主播状态 v${event.snapshot.version}`); return;
  }
  if (!ready || !online || !['barrage', 'like'].includes(event.kind) || message.fromUid === session.viewer.uid || !payload.content.trim() || [...payload.content].length > 120) return;
  const viewer = session.snapshot.viewers.find(person => person.uid === message.fromUid);
  if (!viewer || !markSeen(keys)) return;
  append({ id: event.eventId, fromUid: message.fromUid, name: viewer.name, content: event.kind === 'like' ? `${viewer.name} 点赞了` : payload.content, kind: event.kind, source: 'incoming', time: new Date().toLocaleTimeString('zh-CN') });
  log(`SDK ← ${event.kind} · ${message.fromUid} · eventId ${event.eventId}`);
}
// Merge buffered, authenticated state versions before opening the composer.
async function recover(activeGeneration = generation) {
  if (!session) return;
  const epoch = ++recoveryEpoch; ready = false; recovering = true; connection = '已连接 · 同步当前状态'; render();
  try {
    const value = await request('state?roomId=' + encodeURIComponent(session.roomId), undefined, host?.key || session.viewerKey);
    if (activeGeneration !== generation || epoch !== recoveryEpoch || !online || !session) return;
    if (!snapshot(value.snapshot, session.roomId, session.hostUid, session.viewer.uid)) throw Error('当前房间快照无效，输入保持暂停。');
    applySnapshot(value.snapshot);
    for (const item of buffered.sort((a, b) => a.version - b.version)) applySnapshot(item);
    if (!session || !online || activeGeneration !== generation || epoch !== recoveryEpoch) return;
    buffered = []; if (host && notification(value.notification) && (!host.notification || value.notification.roomVersion >= host.notification.roomVersion)) host.notification = value.notification;
    ready = true; connection = '已连接 · 当前直播'; log(`快照恢复完成 v${session.snapshot.version} · 继续当前直播，不补播弹幕`);
  } catch (cause) { if (activeGeneration === generation && epoch === recoveryEpoch) { ready = false; if (cause instanceof ApiError && [403, 404, 410].includes(cause.status)) { retire(); forget(); connection = '旧演示已失效 · 可创建新房间'; render(); } else connection = '状态恢复失败 · 请重新连接'; error(cause); } }
  finally { if (activeGeneration === generation && epoch === recoveryEpoch) { recovering = false; persist(); render(); } }
}
function retire() { if (sendIntent?.status === 'sending') { sendIntent.status = 'unknown'; sendStatus = '发送结果未知，保留原内容与 UUID；可手动重试。'; } generation++; recoveryEpoch++; client?.destroy(); client = undefined; online = false; ready = false; recovering = false; buffered = []; stage.clear(); }
async function connect() {
  if (!session || busy) return;
  retire(); busy = true; connection = '连接中'; render();
  const activeGeneration = generation, active = WKIM.init(session.wsUrl, { uid: session.viewer.uid, token: session.viewer.token, deviceFlag: 1, deviceId: session.viewer.uid }, { singleton: false }); client = active;
  active.on(WKIMEvent.Message, message => receive(message, activeGeneration));
  active.on(WKIMEvent.Connect, () => { if (activeGeneration !== generation) return; online = true; ready = false; buffered = []; void recover(activeGeneration); });
  active.on(WKIMEvent.Disconnect, () => { if (activeGeneration !== generation) return; online = false; ready = false; recovering = false; recoveryEpoch++; stage.clear(); connection = '连接中断'; log('SDK 连接中断 · 暂停输入'); render(); });
  active.on(WKIMEvent.Reconnecting, () => { if (activeGeneration !== generation) return; ready = false; connection = '正在尝试重连'; render(); });
  active.on(WKIMEvent.Error, () => { if (activeGeneration !== generation) return; if (!online) connection = '连接失败 · 可手动重连'; log('SDK 连接异常 · 请检查服务与凭据'); render(); });
  try { await active.connect(); }
  catch { if (activeGeneration === generation) { connection = '连接失败 · 可手动重连'; error(Error('直播连接未建立，请检查 WuKongIM 与演示业务服务。')); } }
  finally { if (activeGeneration === generation) { busy = false; render(); } }
}
async function prepare(mode: 'rooms' | 'join' | 'resume', input: object) {
  if (busy) return; busy = true; render();
  try {
    const value = await request(mode, input); if (!bootstrap(value)) throw Error('演示身份或房间快照无效，无法入场。');
    const previousHost = host, previousSession = session;
    const { ownerKey, ...viewerBootstrap } = value; session = viewerBootstrap;
    if (mode === 'resume' && previousSession?.roomId === session.roomId && previousSession.viewer.uid === session.viewer.uid && previousSession.snapshot.version > session.snapshot.version) session.snapshot = previousSession.snapshot;
    if (mode === 'rooms' && ownerKey) host = { key: ownerKey };
    else if (mode === 'join') host = undefined;
    else host = previousHost;
    if (!host && mode === 'resume') try { const stored = sessionStorage.getItem(hostStorageKey()); if (stored) { const saved = JSON.parse(stored); if (typeof saved.key === 'string' && saved.key.length <= 1024) host = saved; } } catch {}
    if (mode !== 'resume') { rows = []; seen = []; sendIntent = undefined; denied = undefined; draft = ''; }
    else reconcilePermission(session.snapshot);
    persist(); log(`${mode === 'rooms' ? '创建直播间' : mode === 'join' ? '新观众入场' : '恢复本标签页身份'} · ${session.viewer.name}`);
    busy = false; render(); await connect();
  } catch (cause) { busy = false; if (mode === 'resume' && cause instanceof ApiError && [403, 404, 410].includes(cause.status)) { retire(); forget(); connection = '旧演示已失效'; } error(cause); render(); }
}
async function transmit(intent: SendIntent) {
  if (!session || !client || !online || !ready || self()?.muted || self()?.pending || denied) return;
  intent.status = 'sending'; sendIntent = intent; persist(); sendStatus = '正在提交，等待服务端确认…'; render();
  const activeGeneration = generation, viewer = session.viewer;
  try {
    const ack = await client.send(session.roomId, 2, intent.payload, { clientMsgNo: intent.eventId, header: { noPersist: true, syncOnce: false } });
    if (activeGeneration !== generation || session?.viewer.uid !== viewer.uid) return;
    if (!Number.isInteger(ack.reasonCode)) throw Error('Unrecognized send result');
    if (ack.reasonCode !== 1) {
      intent.status = 'rejected'; intent.reason = `服务端拒绝（原因 ${ack.reasonCode}）`; sendStatus = intent.reason + '，原内容已保留。';
      if ([3, 4, 5, 11, 13, 19, 24, 25].includes(ack.reasonCode)) { denied = { version: session.snapshot.version, observedRestriction: !!self()?.muted || !!self()?.pending }; persist(); await recover(); }
      log(`SDK SENDACK 拒绝 · reason ${ack.reasonCode}`); return;
    }
    sendIntent = undefined; sendStatus = '本人已提交 · 服务端确认；对方收到以其实际消息为准。';
    markSeen([`event:${session.roomId}:${viewer.uid}:${intent.payload.live_demo.kind}:${intent.eventId}`]);
    append({ id: intent.eventId, fromUid: viewer.uid, name: viewer.name, content: intent.payload.content, kind: intent.payload.live_demo.kind, source: 'local', time: new Date().toLocaleTimeString('zh-CN') });
    if (intent.payload.live_demo.kind === 'barrage' && test<HTMLTextAreaElement>('send-input').value.trim() === intent.payload.content) { test<HTMLTextAreaElement>('send-input').value = ''; draft = ''; }
    log(`SDK SENDACK 成功 · eventId ${intent.eventId} · MessageID ${typeof ack.messageId === 'string' ? ack.messageId : '以业务 UUID 关联'}`);
  } catch (cause) {
    if (session?.viewer.uid !== viewer.uid) return;
    const code = cause && typeof cause === 'object' && 'code' in cause ? cause.code : undefined;
    if (typeof code === 'number' && Number.isInteger(code) && code > 1 && code <= 255) {
      intent.status = 'rejected'; intent.reason = `服务端拒绝（原因 ${code}）`; sendStatus = intent.reason + '，原内容已保留。'; log(`SDK JSON-RPC 拒绝 · reason ${code}`);
      if ([3, 4, 5, 11, 13, 19, 24, 25].includes(code)) { denied = { version: session.snapshot.version, observedRestriction: !!self()?.muted || !!self()?.pending }; persist(); await recover(); }
    } else { intent.status = 'unknown'; sendStatus = '发送结果未知，原内容已保留。可重试同一条消息；不会自动重发。'; log(`发送结果未知 · eventId ${intent.eventId}`); }
  } finally { persist(); render(); }
}
async function send(kind: 'barrage' | 'like') {
  if (!session || !canSend()) return;
  const content = kind === 'like' ? `${session.viewer.name} 点赞了` : test<HTMLTextAreaElement>('send-input').value.trim();
  if (!content || [...content].length > 120) { error(Error('弹幕请输入 1–120 个字。')); return; }
  if (Date.now() - lastSendAt < 500) { sendStatus = '稍等片刻，让大家看清这条互动。'; render(); return; } lastSendAt = Date.now();
  const eventId = crypto.randomUUID();
  const intent: SendIntent = { eventId, status: 'sending', payload: { type: 1, content, live_demo: { kind, roomId: session.roomId, eventId } } }; await transmit(intent);
}
async function manage(intent?: ControlIntent, phase?: 'operation' | 'notification') {
  if (!session || !host || controlling || !ready) return; controlling = true; $('#error').textContent = ''; render();
  const currentSession = session, currentHost = host;
  if (intent) { host.intent = intent; host.operationStatus = 'unknown'; persist(); }
  try {
    const original = host.intent;
    const value = phase === 'notification' ? await request('retry', { roomId: session.roomId, requestId: host.notification?.requestId, phase }, host.key)
      : phase === 'operation' && host.operationStatus === 'pending' ? await request('retry', { roomId: session.roomId, requestId: original?.requestId, phase }, host.key)
      : await request('control', original, host.key);
    if (session !== currentSession || host !== currentHost) return;
    if (!controlResult(value, session)) throw Error('管理结果无法确认，请重试原操作。');
    if (phase !== 'notification' && value.operation.requestId !== original?.requestId) throw Error('管理操作身份不一致，请重试原操作。');
    applySnapshot(value.snapshot);
    if (!host.notification || value.notification.roomVersion >= host.notification.roomVersion) host.notification = value.notification;
    if (phase !== 'notification') { if (value.operation.status === 'pending') host.operationStatus = 'pending'; else { host.intent = undefined; host.operationStatus = undefined; } }
    notificationStatus = host.notification.status === 'pending' ? '状态已更新，通知待确认。' : '管理结果已确认；状态通知已被服务端接受。'; log(notificationStatus);
  } catch (cause) { if (session !== currentSession || host !== currentHost) return; error(cause); if (cause instanceof ApiError && cause.status >= 400 && cause.status < 500 && phase !== 'notification') { host.intent = undefined; host.operationStatus = undefined; notificationStatus = '管理请求被拒绝，已保留原确认状态。'; if (online) await recover(); } else notificationStatus = phase === 'notification' ? '通知尚未确认，可继续重试原通知。' : '管理结果未知，保留原操作；请重试，不执行相反动作。'; }
  finally { controlling = false; persist(); render(); }
}
function invitation() {
  if (!session) return '';
  const url = new URL(location.href); url.search = ''; url.hash = '';
  url.searchParams.set('roomId', session.roomId); url.searchParams.set('invite', session.invite); url.searchParams.set('joinNonce', inviteNonce); url.searchParams.set('backend', backend); url.searchParams.set('home', demoHomeURL()); return url.href;
}
function renderChat() {
  const list = test('chat'); list.replaceChildren();
  if (!rows.length) { const empty = document.createElement('div'); empty.className = 'chat-empty'; empty.textContent = '期待你的第一条弹幕。打开第二位观众，一起分享此刻。'; list.append(empty); }
  for (const row of rows) {
    const item = document.createElement('article'); item.className = `chat-row ${row.source}`; item.dataset.testid = 'chat-row'; item.dataset.eventId = row.id; item.dataset.fromUid = row.fromUid; item.dataset.origin = row.source;
    const heading = document.createElement('div'), name = document.createElement('strong'), time = document.createElement('small'), content = document.createElement('p'); name.textContent = row.name; time.textContent = row.source === 'local' ? '本人已提交' : row.time; content.textContent = row.content;
    heading.append(name, time); item.append(heading, content); list.append(item);
  }
  list.scrollTop = list.scrollHeight;
}
function render() {
  const member = self(); test('connection').textContent = connection; test('connection').dataset.online = String(online && ready);
  test('identity').textContent = session ? `${session.viewer.name} · ${session.viewer.uid}` : '等待观众入场'; $('#viewer-name').textContent = session?.viewer.name || '观众';
  $('#start-card').hidden = !!session; test<HTMLButtonElement>('start').disabled = busy;
  test('notice-current').textContent = session?.snapshot.notice || (session ? '暂无公告' : '进入后获取当前公告');
  test('permission').textContent = !session ? '加入后即可发言与点赞。' : !online ? '连接已暂停，原内容保留。重连后继续当前直播。' : !ready ? '正在同步当前公告与权限，输入暂时暂停。' : member?.pending ? '本房间权限待确认，暂时无法发言或点赞，仍可接收消息。' : member?.muted ? '本房间已被禁言，无法发言或点赞，仍可接收消息。' : denied ? '服务端拒绝，权限待确认。输入继续暂停，仍可接收消息。' : '可发言与点赞 · 弹幕仅在当前直播送达。';
  test<HTMLTextAreaElement>('send-input').disabled = !canSend(); test<HTMLButtonElement>('send').disabled = !canSend(); test<HTMLButtonElement>('like').disabled = !canSend();
  $('#input-length').textContent = `${[...test<HTMLTextAreaElement>('send-input').value].length} / 120`;
  $('#send-status').textContent = sendStatus; test('retry-send').hidden = sendIntent?.status !== 'unknown'; test<HTMLButtonElement>('retry-send').disabled = !ready || !online || !!member?.muted || !!member?.pending || !!denied;
  test<HTMLButtonElement>('disconnect').disabled = !session || !online; test<HTMLButtonElement>('reconnect').disabled = !session || busy || recovering; test<HTMLButtonElement>('leave').disabled = !session || busy;
  test<HTMLButtonElement>('open-viewer').disabled = !session || !ready; const invite = test<HTMLAnchorElement>('invite-link'); invite.href = invitation(); invite.hidden = !session;
  $('#host-panel').hidden = !host; test('role-host').hidden = !host; if (!host) $('.workspace').dataset.view = 'viewer';
  const select = test<HTMLSelectElement>('viewer-select'), selected = select.value; select.replaceChildren();
  for (const viewer of session?.snapshot.viewers || []) { const option = document.createElement('option'); option.value = viewer.uid; option.textContent = `${viewer.name}${viewer.uid === session?.viewer.uid ? '（本人）' : ''} · ${viewer.pending ? '权限待确认' : viewer.muted ? '已禁言' : '可发言'}`; select.append(option); }
  if ([...select.options].some(option => option.value === selected)) select.value = selected; else if (session) select.value = session.snapshot.viewers.find(viewer => viewer.uid !== session?.viewer.uid)?.uid || session.viewer.uid;
  const blocked = !session || !host || !ready || busy || controlling || !!session.snapshot.pending || !!host.operationStatus;
  for (const id of ['notice-publish', 'mute', 'unmute']) test<HTMLButtonElement>(id).disabled = blocked;
  test<HTMLButtonElement>('close').disabled = !session || !host || busy || controlling;
  test('control-status').textContent = controlling ? '管理请求进行中…' : host?.operationStatus || session?.snapshot.pending ? '管理操作待确认，保留上一确认值。可重试同一操作。' : host?.notification?.status === 'pending' ? '状态已更新，通知待确认。' : notificationStatus || (host ? '管理当前房间的公告与演示观众。' : '本页是独立观众。');
  test('retry-operation').hidden = !host?.intent && !session?.snapshot.pending; test<HTMLButtonElement>('retry-operation').disabled = !host || !ready || controlling;
  test('retry-notification').hidden = host?.notification?.status !== 'pending'; test<HTMLButtonElement>('retry-notification').disabled = !host || !ready || controlling;
  const parameters = $('#parameters'); parameters.replaceChildren();
  if (session) for (const [label, value] of [['WebSocket', session.wsUrl], ['房间 / 群频道', session.roomId], ['观众 UID', session.viewer.uid], ['主播 UID', session.hostUid], ['当前业务版本', String(session.snapshot.version)]]) { const dt = document.createElement('dt'), dd = document.createElement('dd'); dt.textContent = label; dd.textContent = value; parameters.append(dt, dd); }
  $('#integration-code').textContent = `const im = WKIM.init(wsUrl, { uid, token, deviceFlag: 1 }, { singleton: false });\nim.on(WKIMEvent.Message, receive); // 先监听，再连接和恢复快照\nawait im.connect();\nconst eventId = crypto.randomUUID();\nawait im.send(roomId, 2, {\n  type: 1, content: '期待实机演示',\n  live_demo: { kind: 'barrage', roomId, eventId }\n}, { clientMsgNo: eventId, header: { noPersist: true, syncOnce: false } });`;
  renderChat();
}
async function action(run: () => Promise<void>) { $('#error').textContent = ''; try { await run(); } catch (cause) { error(cause); } finally { render(); } }
const invited = { roomId: query.get('roomId') || '', invite: query.get('invite') || '' };
test('start').onclick = () => void action(() => invited.roomId && invited.invite ? prepare('join', invited) : prepare('rooms', {}));
$('#composer').onsubmit = event => { event.preventDefault(); void action(() => send('barrage')); };
test('like').onclick = () => void action(() => send('like'));
test('retry-send').onclick = () => void action(async () => { if (sendIntent?.status === 'unknown') await transmit(sendIntent); });
test<HTMLTextAreaElement>('send-input').oninput = () => { const input = test<HTMLTextAreaElement>('send-input'); input.value = [...input.value].slice(0, 120).join(''); draft = input.value; persist(); $('#input-length').textContent = `${[...draft].length} / 120`; };
test<HTMLTextAreaElement>('notice-text').oninput = () => { const input = test<HTMLTextAreaElement>('notice-text'); input.value = [...input.value].slice(0, 120).join(''); };
test('disconnect').onclick = () => { client?.disconnect(); online = false; ready = false; recoveryEpoch++; stage.clear(); connection = '已主动断开 · 可重新连接'; render(); };
test('reconnect').onclick = () => void action(async () => { if (session) await prepare('resume', { roomId: session.roomId, viewerKey: session.viewerKey }); });
test('open-viewer').onclick = () => { const url = invitation(); if (url) window.open(url, '_blank', 'noopener'); };
test('barrage-toggle').onclick = () => { visuals = !visuals; stage.toggle(visuals); test('barrage-toggle').textContent = visuals ? '弹幕已开启' : '弹幕已关闭'; test('barrage-toggle').setAttribute('aria-pressed', String(visuals)); };
$('#notice-form').onsubmit = event => { event.preventDefault(); const text = test<HTMLTextAreaElement>('notice-text').value.trim(); if (!text || [...text].length > 120) { error(Error('公告请输入 1–120 个字。')); return; } if (session) void action(() => manage({ roomId: session!.roomId, requestId: crypto.randomUUID(), kind: 'notice', text })); };
for (const [id, muted] of [['mute', true], ['unmute', false]] as const) test(id).onclick = () => { if (session) void action(() => manage({ roomId: session!.roomId, requestId: crypto.randomUUID(), kind: 'mute', targetUid: test<HTMLSelectElement>('viewer-select').value, muted })); };
test('retry-operation').onclick = () => void action(async () => {
  if (session && host && !host.intent && session.snapshot.pending) { const pending = session.snapshot.pending; host.intent = { roomId: session.roomId, requestId: pending.requestId, kind: 'mute', targetUid: pending.targetUid, muted: pending.desiredMuted }; host.operationStatus = 'pending'; }
  await manage(undefined, 'operation');
});
test('retry-notification').onclick = () => void action(() => manage(undefined, 'notification'));
async function leave(close = false) {
  if (!session) return; const current = session; busy = true; render();
  try { await request(close ? 'close' : 'leave', { roomId: current.roomId }, close ? host?.key : current.viewerKey); retire(); forget(); connection = close ? '发布会已结束' : '已离开直播间'; log(connection); }
  finally { busy = false; render(); }
}
test('leave').onclick = () => void action(() => leave()); test('close').onclick = () => void action(() => leave(true));
for (const [id, view] of [['role-viewer', 'viewer'], ['role-host', 'host']] as const) test(id).onclick = () => { $('.workspace').dataset.view = view; test('role-viewer').classList.toggle('active', view === 'viewer'); test('role-host').classList.toggle('active', view === 'host'); };
$('#settings-open').onclick = () => { test<HTMLInputElement>('settings-backend').value = backend; test<HTMLInputElement>('settings-backend').disabled = !!session; $<HTMLDialogElement>('#settings').showModal(); };
$('#settings-close').onclick = () => $<HTMLDialogElement>('#settings').close();
$('#settings-form').onsubmit = event => { event.preventDefault(); try { if (!session) backend = backendURL(test<HTMLInputElement>('settings-backend').value); $<HTMLDialogElement>('#settings').close(); } catch (cause) { error(cause); } };
const scenes = [ ['灵感，即刻发生。', '看见设计的每一面，让日常多一点惊喜。', ['轻巧轮廓', '自然触感']], ['为灵感留出空间。', '从细节到体验，每一个改变都值得分享。', ['舒适交互', '从容协作']], ['期待你的第一个问题。', '新品介绍进行中，现场互动由你开启。', ['分享期待', '一起提问']] ] as const;
function showScene(index: number) { scene = index; $('#scene-title').textContent = scenes[index][0]; $('#scene-description').textContent = scenes[index][1]; $('#scene-index').textContent = `0${index + 1} / 03`; $('#scene-chips').replaceChildren(...scenes[index][2].map(value => { const chip = document.createElement('span'); chip.textContent = value; return chip; })); document.querySelectorAll<HTMLButtonElement>('[data-scene]').forEach(button => { button.classList.toggle('active', Number(button.dataset.scene) === index); }); }
document.querySelectorAll<HTMLButtonElement>('[data-scene]').forEach(button => { button.onclick = () => showScene(Number(button.dataset.scene)); });
const sceneTimer = setInterval(() => { if (!document.hidden) showScene((scene + 1) % 3); }, 14000);
const heartbeat = setInterval(() => {
  if (!session || document.hidden) return;
  const renewingSession = session, activeGeneration = generation;
  void request('heartbeat', { roomId: renewingSession.roomId }, renewingSession.viewerKey).catch(cause => {
    if (session !== renewingSession || generation !== activeGeneration) return;
    if (cause instanceof ApiError && [403, 404, 410].includes(cause.status)) { retire(); forget(); connection = '旧演示已失效 · 可创建新房间'; error(cause); render(); }
    else log('业务续期未确认 · 可手动重新连接');
  });
}, 45000);
window.addEventListener('pagehide', () => { persist(); retire(); stage.destroy(); clearInterval(sceneTimer); clearInterval(heartbeat); });
// BFCache restores a shell whose connections and timers were intentionally retired.
window.addEventListener('pageshow', event => { if (event.persisted) location.reload(); });
try {
  const savedText = sessionStorage.getItem(storageKey);
  if (savedText) { const saved: Saved = JSON.parse(savedText); if (bootstrap(saved.session) && (!invited.roomId || invited.roomId === saved.session.roomId) && saved.joinNonce === joinNonce) { backend = backendURL(saved.backend); session = saved.session; rows = Array.isArray(saved.rows) ? saved.rows.filter(row => row && uuid(row.id) && typeof row.fromUid === 'string' && typeof row.name === 'string' && typeof row.content === 'string' && ['barrage', 'like'].includes(row.kind) && ['local', 'incoming'].includes(row.source)).slice(-100) : []; seen = Array.isArray(saved.seen) ? saved.seen.filter(item => typeof item === 'string').slice(-512) : []; draft = typeof saved.draft === 'string' ? saved.draft.slice(0, 240) : ''; const savedSend = saved.send; if (savedSend && uuid(savedSend.eventId) && savedSend.payload?.live_demo?.eventId === savedSend.eventId && savedSend.payload.live_demo.roomId === session.roomId && ['barrage', 'like'].includes(savedSend.payload.live_demo.kind) && typeof savedSend.payload.content === 'string' && [...savedSend.payload.content].length <= 120 && ['sending', 'unknown', 'rejected'].includes(savedSend.status)) sendIntent = savedSend; if (sendIntent?.status === 'sending') sendIntent.status = 'unknown'; if (saved.denied && Number.isSafeInteger(saved.denied.version) && typeof saved.denied.observedRestriction === 'boolean') denied = saved.denied; } }
} catch { /* Invalid tab state starts a new Demo instead of acquiring a role. */ }
if (!session && query.get('backend')) try { backend = backendURL(query.get('backend')!); } catch {}
test<HTMLTextAreaElement>('send-input').value = draft;
if (sendIntent?.status === 'unknown') sendStatus = '上次发送结果未知，保留原内容与 UUID；连接后可手动重试。';
if (invited.roomId && invited.invite) $('#start-description').textContent = '你将作为一位新的观众加入同一场发布会。';
render();
if (session) void action(() => prepare('resume', { roomId: session!.roomId, viewerKey: session!.viewerKey }));
else if (invited.roomId && invited.invite) void action(() => prepare('join', invited));
