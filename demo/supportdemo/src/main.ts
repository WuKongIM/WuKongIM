import { WKIM, WKIMEvent } from 'easyjssdk';
import './style.css';
import { demoHomeURL } from '../../shared/home';
import '../../shared/home.css';

type Room = {id: string; channelId: string; visitorId: string; visitorName: string; status: string; revision: number; generating: boolean; unconfirmed: boolean; error: string; preview: string; updatedAt: number};
type Identity = {id: string; uid: string; token: string; name: string};
type Config = {mode: string; url: string; model: string; keyConfigured: boolean; text: string; interval: number};
type Workspace = {id: string; token?: string; wsUrl: string; agent: {uid: string; token: string}; visitors: Identity[]; rooms: Room[]; config: Config; logs: {time: string; kind: string; roomId?: string; detail: string}[]};
type Row = {key: string; sequence: string; author: string; text: string; status: string; stream: boolean; terminal: boolean; notice: boolean; seen: Set<string>};
type Role = 'customer' | 'agent';
const $ = <T extends HTMLElement = HTMLElement>(selector: string) => document.querySelector<T>(selector)!;
const labels: Record<string, string> = {ai: 'AI 接待', handoff: '正在转接', waiting: '待接入', human: '人工接待', closed: '已结束'};
const storage = 'wk-supportdemo-session';
const messages = new Map<string, Map<string, Row>>();
const drafts = new Map<string, {text: string; retryId?: string}>();
const liveLogs: string[] = [];
const encoder = new TextEncoder(), decoder = new TextDecoder();
let work: Workspace | undefined, capability = '', visitorId = '', customerRoomId = '', agentRoomId = '';
let backend = document.querySelector('meta[name="wk-support-backend"]') ? location.origin : 'http://127.0.0.1:5177';
let starting = false, busy = false, filter = 'all';
let customer: WKIM | undefined, agent: WKIM | undefined;
let customerOnline = false, agentOnline = false;
let customerGeneration = 0, agentGeneration = 0;
const restoring: Record<Role, boolean> = {customer: false, agent: false};
const buffered: Record<Role, (() => void)[]> = {customer: [], agent: []};
const sendIcon = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m5 12 7-7 7 7M12 5v15"/></svg>';
const bot = '<svg viewBox="0 0 60 60" aria-hidden="true"><path d="M29 4C39 4 44 17 51 31C61 47 47 54 30 53C12 54 3 45 10 32C16 19 22 5 29 4Z" fill="#ffdc74"/><g fill="none" stroke="#292d2a" stroke-width="1.8"><ellipse cx="20" cy="26" rx="7" ry="9"/><ellipse cx="40" cy="26" rx="7" ry="9"/><path d="M27 26h6M17 26l5 1m16-1 4 1M25 40q5 5 10 0"/></g><path d="m29 47-9-4v9l10-4 9 4v-9l-10 4" fill="#292d2a"/></svg>';
$('#app').innerHTML = `
<div class="demo-app">
  <header class="app-header"><div class="wordmark"><span class="brand-icon">W</span> WuKongIM <span class="brand-divider"></span><span class="product-name">在线客服</span></div><div class="header-actions"><a class="demo-home-link" data-demo-home><span aria-hidden="true">←</span>返回首页</a><span class="chain"><i></i>真实消息链路</span><button id="settings-button" class="plain">演示设置 <span aria-hidden="true">⚙</span></button></div></header>
  <section class="intro"><div><span class="eyebrow">CUSTOMER SUPPORT DEMO</span><h1>从第一句问候，到问题解决。</h1><p>AI 先接待，人工随时接手。完整对话，一直都在。</p></div><div class="lifecycle" aria-label="接待流程"><span>AI 接待</span><i>→</i><span>转人工</span><i>→</i><span>问题解决</span></div></section>
  <div id="error" role="alert"></div>
  <div id="start-card"><div><strong>一起体验两侧的对话</strong><p>两个访客、一名客服，从这里开始。</p></div><button id="start" class="primary">开始演示 <span aria-hidden="true">↗</span></button></div>
  <nav class="mobile-tabs" aria-label="演示端切换"><button data-view="customer" class="active">访客端</button><button data-view="agent">客服工作台</button></nav>
  <main class="workspace" data-view="customer">
    <section class="visitor-panel" aria-label="访客端"><div class="panel-label"><span><i class="small-dot"></i>访客端</span><button id="customer-disconnect" class="subtle" disabled>断开访客端</button></div>
      <div class="phone"><div class="phone-camera"></div><div class="visitor-selector"><span class="person-avatar" id="visitor-avatar">林</span><div><label for="visitor-select">当前访客</label><select id="visitor-select" aria-label="切换访客" disabled><option>林同学</option><option>陈同学</option></select></div><span class="connection" id="customer-connection">未连接</span></div>
        <div class="customer-chat-header"><span class="bot-avatar">${bot}</span><div><h2 id="customer-title">小悟 AI 助手</h2><p id="customer-status">随时准备为你解答</p></div><button id="handoff" class="handoff" disabled>转人工</button></div>
        <div class="customer-history"><select id="customer-history" aria-label="访客会话历史" disabled><option>本次咨询</option></select></div>
        <div id="customer-messages" class="chat-messages" role="log" aria-label="访客聊天记录"><div class="welcome"><span class="welcome-bot">${bot}</span><h3>很高兴见到你</h3><p>物流、退换货、订单问题，<br>我们都可以聊聊。</p></div></div>
        <div id="customer-hint" class="chat-hint">开始演示后，即可发起咨询。</div><div class="question-chips"><button data-question="帮我查一下订单物流" disabled>查物流</button><button data-question="如何申请退换货？" disabled>退换货</button></div>
        <form id="customer-composer" class="composer"><textarea id="customer-question" aria-label="访客消息" rows="1" maxlength="1000" placeholder="说说你遇到的问题…" disabled></textarea><button id="customer-send" aria-label="发送访客消息" disabled>${sendIcon}</button></form><button id="new" class="new-consultation" hidden>重新咨询</button>
        <div class="phone-home"></div>
      </div>
    </section>
    <section class="agent-panel" aria-label="客服工作台"><div class="panel-label"><span><i class="small-dot agent-dot"></i>客服工作台</span><div><span class="agent-name">小悟客服</span><span id="agent-connection" class="connection">未连接</span><button id="agent-disconnect" class="subtle" disabled>断开客服端</button></div></div>
      <div class="workbench"><aside class="inbox"><div class="inbox-heading"><h2>会话</h2><span id="room-count">0</span></div><nav class="inbox-tabs" aria-label="会话筛选"><button data-filter="all" class="active">全部</button><button data-filter="waiting">待接入 <b id="waiting-count">0</b></button><button data-filter="human">接待中</button></nav><div id="room-list" role="list" aria-label="会话列表"><div class="list-placeholder"><svg viewBox="0 0 48 48" aria-hidden="true"><path d="M10 9h28v25H25l-9 6v-6h-6Z"/></svg><p>新咨询会出现在这里</p></div></div><div class="inbox-footer"><i></i>消息与记录实时同步</div></aside>
        <section class="agent-conversation"><header class="agent-chat-header"><span class="person-avatar" id="agent-visitor-avatar">林</span><div><h2 id="agent-title">选择一个会话</h2><p id="agent-room-status">AI 与你协作接待每位访客</p></div><div class="agent-controls"><button id="accept" class="primary" hidden>接入会话</button><button id="end" class="secondary" hidden>结束会话</button></div></header><div id="agent-messages" class="chat-messages" role="log" aria-label="客服聊天记录"><div class="agent-welcome"><div class="support-illustration"><span>✦</span><i></i><b>↗</b></div><h3>每一次咨询，都值得被认真回应。</h3><p>查看 AI 接待的对话，<br>在需要时接手，继续帮助访客。</p></div></div><div id="agent-hint" class="chat-hint">接入会话后，你可以在这里回复。</div><div class="quick-replies"><span>快捷回复</span><button data-reply="您好，我已了解前面的情况，接下来由我为您处理。" disabled>接待问候</button><button data-reply="您的问题已记录，我会为您进一步核实，请稍等。" disabled>处理中</button></div><form id="agent-composer" class="composer"><textarea id="agent-question" aria-label="客服消息" rows="2" maxlength="1000" placeholder="输入回复，Enter 发送…" disabled></textarea><button id="agent-send" class="primary" disabled>发送 ${sendIcon}</button></form></section>
      </div>
    </section>
  </main><footer class="app-footer"><span>Powered by WuKongIM · EasySDK</span><span>访客和客服看到的是同一段真实对话</span></footer>
</div>
<dialog id="settings"><form id="config-form"><header><div><span class="eyebrow">DEMO SETTINGS</span><h2>连接与 AI 接待</h2></div><button id="settings-close" type="button" class="plain" aria-label="关闭演示设置">×</button></header><label>客服业务服务 URL<input id="backend-url" aria-label="客服业务服务 URL" type="url" spellcheck="false"></label><p class="hint">独立业务服务管理接管与模型调用。内嵌页面可连接本机的客服 Demo 服务。</p><label>回复来源<select id="mode" aria-label="回复来源"><option value="simulation">模拟回复</option><option value="model">真实模型</option></select></label><div id="model-options" hidden><label>模型 URL<input id="model-url" aria-label="模型 URL" type="url" placeholder="https://your-provider.example/v1" spellcheck="false"></label><label>API Key<input id="model-key" aria-label="API Key" type="password" autocomplete="off" placeholder="仅保留在本次演示业务进程内存"></label><label>模型名称 <span class="hint">可选</span><input id="model-name" aria-label="模型名称" placeholder="留空自动获取聊天模型" spellcheck="false"></label></div><div id="simulation-options"><label>模拟回复 <span class="hint">可选</span><textarea id="simulation-text" aria-label="模拟回复" rows="3" maxlength="4096" placeholder="留空时，根据物流、退换货等问题回复。"></textarea></label><label>发送间隔（毫秒）<input id="interval" aria-label="发送间隔（毫秒）" type="number" min="40" max="1000" value="90"></label></div><button id="save-config" class="primary" type="submit" disabled>保存设置</button><p id="config-result" role="status"></p><details id="trace"><summary>请求与事件日志</summary><button id="resync" class="subtle" type="button" disabled>重新同步</button><pre id="logs"></pre></details></form></dialog>`;

$<HTMLAnchorElement>('[data-demo-home]').href = demoHomeURL();

const selected = (role: Role) => work?.rooms.find(r => r.id === (role === 'customer' ? customerRoomId : agentRoomId));
const actorOnline = (role: Role) => role === 'customer' ? customerOnline : agentOnline;
function log(kind: string, room?: Room, detail = '') {
  liveLogs.push(`${new Date().toLocaleTimeString('zh-CN')}  ${kind}  ${room?.visitorName || ''}  ${detail}`);
  if (liveLogs.length > 160) liveLogs.shift();
  $('#logs').textContent = [...(work?.logs || []).slice(-40).map(l => `${l.time.slice(11, 19)}  后端 ${l.kind}  ${l.detail}`), ...liveLogs].join('\n');
}
function showError(error: unknown) { $('#error').textContent = error instanceof Error ? error.message : '操作失败，请稍后重试。'; }
async function api(route: string, body?: unknown): Promise<any> {
  const headers: Record<string, string> = {'content-type': 'application/json'};
  if (capability) headers.Authorization = `Bearer ${capability}`;
  let response: Response;
  try { response = await fetch(backend + '/supportdemo/api' + route, {method: body === undefined ? 'GET' : 'POST', headers, body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(30000)}); }
  catch { throw Error('客服业务服务未连接。请在演示设置中检查服务 URL，并运行 supportdemo 的 npm start。'); }
  let result: any;
  try { result = await response.json(); } catch { throw Error('请先运行客服业务服务，再开始演示。'); }
  if (!response.ok) { if (response.status === 401 && capability) resetSession(); throw Error(result.error || '操作失败，请重新同步。'); }
  if (route !== '/config') log(`HTTP ${route}`);
  return result;
}
function saveSelection() {
  if (work) sessionStorage.setItem(storage, JSON.stringify({backend, capability, visitorId, customerRoomId, agentRoomId}));
}
function update(next: Workspace) {
  if (work?.id === next.id) next.rooms = next.rooms.map(room => {
    const current = work!.rooms.find(r => r.id === room.id);
    return current && current.revision > room.revision ? current : room;
  });
  work = next;
  visitorId ||= next.visitors[0].id;
  customerRoomId ||= next.rooms.filter(r => r.visitorId === visitorId).at(-1)?.id || '';
  agentRoomId ||= next.rooms[0]?.id || '';
  saveSelection(); render();
}
function resetSession() {
  customerGeneration++; agentGeneration++; customer?.destroy(); agent?.destroy();
  customer = agent = undefined; customerOnline = agentOnline = false;
  work = undefined; capability = visitorId = customerRoomId = agentRoomId = '';
  messages.clear(); drafts.clear(); liveLogs.length = 0;
  buffered.customer.length = buffered.agent.length = 0;
  sessionStorage.removeItem(storage);
  $('#room-list').replaceChildren(); $('#room-count').textContent = $('#waiting-count').textContent = '0';
  $<HTMLSelectElement>('#visitor-select').replaceChildren(new Option('林同学'));
  $<HTMLSelectElement>('#customer-history').replaceChildren(new Option('本次咨询'));
  $<HTMLInputElement>('#model-key').value = ''; $('#logs').textContent = '';
  for (const role of ['customer', 'agent'] as const) {
    const input = $<HTMLTextAreaElement>(`#${role}-question`); input.value = ''; delete input.dataset.retryId;
    $(`#${role}-messages`).replaceChildren();
  }
  render();
}
// Each role keeps drafts and retry identity with the selected conversation.
function switchRoom(role: Role, roomId: string) {
  const input = $<HTMLTextAreaElement>(`#${role}-question`);
  const oldId = role === 'customer' ? customerRoomId : agentRoomId;
  drafts.set(`${role}:${oldId}`, {text: input.value, retryId: input.dataset.retryId});
  if (role === 'customer') customerRoomId = roomId; else agentRoomId = roomId;
  const saved = drafts.get(`${role}:${roomId}`); input.value = saved?.text || '';
  if (saved?.retryId) input.dataset.retryId = saved.retryId; else delete input.dataset.retryId;
  saveSelection();
}
function render() {
  const c = selected('customer'), a = selected('agent');
  $('#start-card').hidden = !!work;
  $('#start').toggleAttribute('disabled', starting); $('#start').textContent = starting ? '正在连接…' : '开始演示 ↗';
  $('#customer-connection').textContent = customerOnline ? '在线' : work ? '离线' : '未连接';
  $('#agent-connection').textContent = agentOnline ? '在线' : work ? '离线' : '未连接';
  for (const role of ['customer', 'agent'] as const) {
    const el = $(`#${role}-disconnect`); el.toggleAttribute('disabled', !work || busy); el.textContent = actorOnline(role) ? `断开${role === 'customer' ? '访客' : '客服'}端` : `重连${role === 'customer' ? '访客' : '客服'}端`;
    $(`#${role}-connection`).classList.toggle('online', actorOnline(role));
  }
  $('#visitor-select').toggleAttribute('disabled', !work || busy);
  $('#customer-history').toggleAttribute('disabled', !work || busy);
  const customerCanSend = !!c && !c.unconfirmed && customerOnline && !restoring.customer && !busy && c.status !== 'closed' && !(c.status === 'ai' && c.generating);
  const agentCanSend = !!a && !a.unconfirmed && agentOnline && !restoring.agent && !busy && a.status === 'human';
  for (const id of ['customer-question', 'customer-send']) $(`#${id}`).toggleAttribute('disabled', !customerCanSend);
  for (const id of ['agent-question', 'agent-send']) $(`#${id}`).toggleAttribute('disabled', !agentCanSend);
  $('#handoff').toggleAttribute('disabled', !c || c.unconfirmed || !customerOnline || busy || c.status !== 'ai');
  $('#handoff').hidden = !!c && c.status !== 'ai';
  $('#new').hidden = c?.status !== 'closed'; $('#new').toggleAttribute('disabled', busy || !customerOnline);
  $('#customer-composer').hidden = c?.status === 'closed'; $('.question-chips').hidden = c?.status === 'closed';
  document.querySelectorAll<HTMLButtonElement>('[data-question]').forEach(e => { e.disabled = !customerCanSend; });
  document.querySelectorAll<HTMLButtonElement>('[data-reply]').forEach(e => { e.disabled = !agentCanSend; });
  $('#accept').hidden = a?.status !== 'waiting'; $('#end').hidden = a?.status !== 'human';
  for (const id of ['accept', 'end']) $(`#${id}`).toggleAttribute('disabled', busy || !agentOnline);
  $('#customer-title').textContent = c?.status === 'human' ? '小悟客服' : '小悟 AI 助手';
  $('#customer-status').textContent = c ? labels[c.status] : '随时准备为你解答';
  $('#customer-hint').textContent = c?.error || (!customerOnline && work ? '访客端已断开，重连后可恢复消息。' : c?.status === 'closed' ? '本次咨询已结束，历史记录仍然保留。' : c?.status === 'waiting' ? '正在等待客服接入，你可以补充问题。' : c?.status === 'handoff' ? 'AI 已停止，正在为你转接…' : c?.generating ? '小悟正在回复，随时可以转人工。' : c?.status === 'human' ? '人工客服正在为你服务。' : 'AI 接待中 · 支持随时转人工');
  $('#agent-title').textContent = a?.visitorName || '选择一个会话';
  $('#agent-room-status').textContent = a ? labels[a.status] : 'AI 与你协作接待每位访客';
  $('#agent-hint').textContent = a?.error || (!agentOnline && work ? '客服端已断开，重连后可恢复消息。' : a?.status === 'human' ? '你已接入，AI 将保持停止。' : a?.status === 'waiting' ? '访客正在等待，点击“接入会话”开始回复。' : a?.status === 'closed' ? '会话已结束，聊天记录只读。' : 'AI 正在接待；访客转人工后，你可以接入。');
  const v = work?.visitors.find(v => v.id === visitorId);
  $('#visitor-avatar').textContent = v?.name.slice(0, 1) || '林'; $('#agent-visitor-avatar').textContent = a?.visitorName.slice(0, 1) || '林';
  if (work) {
    const visitorSelect = $<HTMLSelectElement>('#visitor-select');
    if (visitorSelect.options.length !== work.visitors.length || visitorSelect.options[0]?.value !== work.visitors[0].id) {
      visitorSelect.replaceChildren(...work.visitors.map(v => new Option(v.name, v.id)));
    }
    visitorSelect.value = visitorId;
    const history = $<HTMLSelectElement>('#customer-history');
    const historyRooms = work.rooms.filter(r => r.visitorId === visitorId);
    history.replaceChildren(...historyRooms.map((r, index) => new Option(`咨询 ${index + 1} · ${labels[r.status]}`, r.id)));
    history.value = customerRoomId;
    $('#room-count').textContent = String(work.rooms.length);
    $('#waiting-count').textContent = String(work.rooms.filter(r => r.status === 'waiting').length);
    renderRoomList(); renderChat('customer'); renderChat('agent');
  }
  $('#backend-url').toggleAttribute('disabled', !!work || starting);
  $('#save-config').toggleAttribute('disabled', !work || busy || work.rooms.some(r => r.generating));
  $('#resync').toggleAttribute('disabled', !work || busy);
}
function renderRoomList() {
  if (!work) return;
  const list = $('#room-list'); list.replaceChildren();
  for (const room of [...work.rooms].sort((a, b) => b.updatedAt - a.updatedAt)) {
    if (filter !== 'all' && room.status !== filter) continue;
    const button = document.createElement('button'); button.className = `room-item${room.id === agentRoomId ? ' selected' : ''}`; button.dataset.roomId = room.id; button.setAttribute('role', 'listitem');
    button.innerHTML = '<span class="person-avatar"></span><span class="room-copy"><span class="room-top"><strong></strong><small></small></span><span class="room-preview"></span><span class="room-badge"></span></span>';
    button.querySelector('.person-avatar')!.textContent = room.visitorName.slice(0, 1);
    button.querySelector('strong')!.textContent = room.visitorName;
    button.querySelector('small')!.textContent = new Date(room.updatedAt).toLocaleTimeString('zh-CN', {hour: '2-digit', minute: '2-digit'});
    button.querySelector('.room-preview')!.textContent = room.preview;
    const badge = button.querySelector<HTMLElement>('.room-badge')!; badge.textContent = labels[room.status]; badge.dataset.status = room.status;
    button.onclick = () => void action(async () => { if (busy) return; switchRoom('agent', room.id); await recover('agent', room); });
    list.append(button);
  }
  if (!list.childNodes.length) { const empty = document.createElement('p'); empty.className = 'empty-list'; empty.textContent = '暂时没有会话'; list.append(empty); }
}
function rows(role: Role, room: Room) { const cacheKey = `${role}:${room.id}`; let value = messages.get(cacheKey); if (!value) { value = new Map(); messages.set(cacheKey, value); } return value; }
function row(role: Role, room: Room, key: string): Row {
  const cache = rows(role, room); let value = cache.get(key);
  if (!value) { value = {key, sequence: '0', author: work!.agent.uid, text: '', status: '', stream: false, terminal: false, notice: false, seen: new Set()}; cache.set(key, value); }
  // Bound per-room message state to the recovery page size.
  if (cache.size > 100) { const oldest = [...cache.values()].sort((a, b) => Number(BigInt(a.sequence) - BigInt(b.sequence)))[0]; if (oldest.key !== key) cache.delete(oldest.key); }
  return value;
}
function payload(value: any) { if (typeof value !== 'string') return value || {}; try { return JSON.parse(decoder.decode(Uint8Array.from(atob(value), c => c.charCodeAt(0)))); } catch { return {}; } }
function ordinary(role: Role, message: any, historical = false) {
  if (!work) return;
  const room = work.rooms.find(r => r.channelId === (message.channelId || message.channel_id));
  if (!room) return;
  const body = payload(message.payload);
  const key = message.clientMsgNo || message.client_msg_no;
  if (!key) return;
  const value = row(role, room, key);
  value.sequence = String(message.messageSeq || message.message_seq || value.sequence); value.author = message.fromUid || message.from_uid;
  if (body.type === 2001 && body.support?.id === room.id) {
    const next = body.support as Room;
    if (next.revision >= room.revision) Object.assign(room, next);
    value.notice = true; value.text = labels[next.status]; value.status = ''; value.terminal = true;
  } else {
    value.stream = typeof message.setting === 'number' ? !!(message.setting & 2) : !!message.setting?.stream;
    if (!value.stream) value.text = typeof body.content === 'string' ? body.content : '';
    else { if (!value.status) value.status = '生成中'; if (!historical) room.generating = !value.terminal; }
    if (!historical && !value.stream && value.text) { room.preview = value.text; room.updatedAt = Date.now(); }
    const projection = message.event_meta?.events?.find((e: any) => e.event_key === 'main');
    if (projection && (!value.terminal || ['closed', 'cancelled', 'error'].includes(projection.status))) {
      value.stream = true; value.text = projection.snapshot?.text || value.text; value.status = {closed: '完成', cancelled: '已取消', error: '失败'}[projection.status as 'closed' | 'cancelled' | 'error'] || '生成中';
      value.terminal = !!message.event_meta.completed || ['closed', 'cancelled', 'error'].includes(projection.status); if (!historical) room.generating = !value.terminal;
    }
  }
}
function event(role: Role, value: any) {
  if (!work || !value.type?.startsWith('stream.')) return;
  const data = value.data;
  const room = work.rooms.find(r => r.channelId === data?.channel_id);
  // stream.finish uses the reserved message-level lane, rather than main.
  if (!room || data.channel_type !== 2 || !data.client_msg_no || data.event_key !== 'main' && value.type !== 'stream.finish') return;
  const item = row(role, room, data.client_msg_no);
  if (item.seen.has(value.id)) return;
  if (item.seen.size >= 2048) item.seen.delete(item.seen.values().next().value!);
  item.seen.add(value.id); item.stream = true; item.author = data.from_uid; item.sequence = String(data.message_seq || item.sequence);
  const p = data.payload || {};
  if (value.type === 'stream.delta' && !item.terminal) {
    const bytes = encoder.encode(item.text), delta = encoder.encode(p.delta || ''); const offset = data.text_offset ?? bytes.length;
    if (offset > bytes.length) item.status = '等待终态快照';
    else if (offset + delta.length > bytes.length) item.text += decoder.decode(delta.slice(bytes.length - offset));
    room.generating = true;
  }
  if (value.type === 'stream.cancel' || value.type === 'stream.error' || value.type === 'stream.finish') {
    if (typeof p.snapshot?.text === 'string') item.text = p.snapshot.text;
    if (value.type === 'stream.cancel') item.status = '已取消';
    else if (value.type === 'stream.error') item.status = '失败';
    else if (!['已取消', '失败'].includes(item.status)) item.status = '完成';
    item.terminal = true; room.generating = false;
  } else if (!item.status) item.status = '生成中';
  log(`SDK ${value.type}`, room); render();
}
function renderChat(role: Role) {
  const room = selected(role); if (!room) return;
  const container = $(`#${role}-messages`);
  const atEnd = container.scrollHeight - container.scrollTop - container.clientHeight < 100;
  const oldScroll = container.scrollTop;
  const values = [...rows(role, room).values()].sort((a, b) => Number(BigInt(a.sequence) - BigInt(b.sequence)));
  container.replaceChildren();
  for (const item of values) {
    const el = document.createElement('div'); el.dataset.messageKey = item.key; el.dataset.status = item.status;
    if (item.notice) { el.className = 'system-notice'; el.textContent = item.text; }
    else {
      const own = role === 'customer' ? item.author === room.visitorId || item.author === work!.visitors.find(v => v.id === room.visitorId)?.uid : item.author === work!.agent.uid;
      el.className = `message${own ? ' own' : ''}${item.stream ? ' stream-message' : ''}`;
      el.innerHTML = '<span class="message-author"></span><div class="bubble"></div><small class="message-status"></small>';
      el.querySelector('.message-author')!.textContent = item.author === work!.agent.uid ? '小悟客服' : item.author === work!.visitors.find(v => v.id === room.visitorId)?.uid ? room.visitorName : '小悟 AI';
      const text = el.querySelector<HTMLElement>('.bubble')!; text.textContent = item.text;
      if (!item.text && item.stream && !item.terminal) { text.classList.add('typing'); text.textContent = '···'; }
      el.querySelector('.message-status')!.textContent = item.status;
    }
    container.append(el);
  }
  if (atEnd) container.scrollTop = container.scrollHeight; else container.scrollTop = oldScroll;
}
async function recover(role: Role, room = selected(role)) {
  if (!room || !work) return;
  restoring[role] = true; render();
  try {
    const history = await api('/history', {roomId: room.id, actor: role});
    for (const message of history.messages || []) ordinary(role, message, true);
    // An in-progress snapshot may omit its base message: buffered live events
    // follow the snapshot using UTF-8 offsets and Event ID deduplication.
    for (const apply of buffered[role].splice(0)) apply();
  } finally { restoring[role] = false; render(); }
}
async function connect(role: Role) {
  if (!work) return;
  const generation = role === 'customer' ? ++customerGeneration : ++agentGeneration;
  const current = () => generation === (role === 'customer' ? customerGeneration : agentGeneration);
  const old = role === 'customer' ? customer : agent; old?.destroy(); buffered[role].length = 0;
  if (role === 'customer') customerOnline = false; else agentOnline = false;
  const identity = role === 'customer' ? work.visitors.find(v => v.id === visitorId)! : work.agent;
  const client = WKIM.init(work.wsUrl, {uid: identity.uid, token: identity.token, deviceFlag: 1}, {singleton: false});
  if (role === 'customer') customer = client; else agent = client;
  const receive = (apply: () => void) => {
    if (!current()) return;
    if (!restoring[role]) { apply(); return; }
    if (buffered[role].length < 512) buffered[role].push(() => { if (current()) apply(); });
    else { client.disconnect(); buffered[role].length = 0; showError(Error('历史恢复期间消息过多，已断开此端。请重连后恢复。')); }
  };
  client.on(WKIMEvent.Message, message => receive(() => { ordinary(role, message); render(); }));
  client.on(WKIMEvent.CustomEvent, value => receive(() => event(role, value)));
  client.on(WKIMEvent.Connect, () => {
    if (!current()) return;
    if (role === 'customer') customerOnline = true; else agentOnline = true;
    void action(async () => { update(await api('/state')); await recover(role); });
  });
  client.on(WKIMEvent.Disconnect, () => { if (!current()) return; if (role === 'customer') customerOnline = false; else agentOnline = false; render(); });
  client.on(WKIMEvent.Error, () => { if (current()) log('SDK 连接异常'); });
  await client.connect(); render();
}
async function action(fn: () => Promise<void>) {
  $('#error').textContent = '';
  try { await fn(); } catch (error) { showError(error); } finally { render(); }
}
async function control(route: string, role: Role, extra = {}) {
  const room = selected(role); if (!room || busy) return;
  busy = true; render();
  await action(async () => { update(await api(route, {roomId: room.id, requestId: crypto.randomUUID(), ...extra})); });
  busy = false; render();
}
async function submit(role: Role) {
  const input = $<HTMLTextAreaElement>(`#${role}-question`), room = selected(role);
  const content = input.value.trim(); if (!content || !room || busy || !actorOnline(role)) return;
  const requestId = input.dataset.retryId || crypto.randomUUID();
  input.dataset.retryId = requestId;
  busy = true; render();
  await action(async () => { update(await api('/send', {roomId: room.id, actor: role, content, requestId})); input.value = ''; delete input.dataset.retryId; });
  busy = false; render();
}
$('#start').onclick = () => void action(async () => {
  starting = true; render();
  try { const next = await api('/session', {}); capability = next.token; update(next); await Promise.all([connect('customer'), connect('agent')]); }
  finally { starting = false; render(); }
});
$('#handoff').onclick = () => void control('/handoff', 'customer');
$('#accept').onclick = () => void control('/accept', 'agent');
$('#end').onclick = () => void control('/end', 'agent');
$('#new').onclick = () => void action(async () => {
  busy = true; render();
  try { const next = await api('/new', {visitorId, requestId: crypto.randomUUID()}); const roomId = next.rooms.filter((r: Room) => r.visitorId === visitorId).at(-1).id; switchRoom('customer', roomId); switchRoom('agent', roomId); update(next); await Promise.all([recover('customer'), recover('agent')]); }
  finally { busy = false; render(); }
});
for (const role of ['customer', 'agent'] as const) {
  $(`#${role}-composer`).onsubmit = e => { e.preventDefault(); void submit(role); };
  $(`#${role}-question`).onkeydown = e => { if (e.key === 'Enter' && !e.shiftKey && !e.isComposing && e.keyCode !== 229) { e.preventDefault(); void submit(role); } };
  $(`#${role}-question`).oninput = () => { delete $(`#${role}-question`).dataset.retryId; };
  $(`#${role}-disconnect`).onclick = () => void action(async () => { if (actorOnline(role)) (role === 'customer' ? customer : agent)?.disconnect(); else await connect(role); });
}
$<HTMLSelectElement>('#visitor-select').onchange = () => void action(async () => {
  visitorId = $<HTMLSelectElement>('#visitor-select').value;
  switchRoom('customer', work!.rooms.filter(r => r.visitorId === visitorId).at(-1)!.id);
  render(); saveSelection(); await connect('customer');
});
$<HTMLSelectElement>('#customer-history').onchange = () => void action(async () => { switchRoom('customer', $<HTMLSelectElement>('#customer-history').value); await recover('customer'); });
document.querySelectorAll<HTMLButtonElement>('[data-question]').forEach(e => { e.onclick = () => { $<HTMLTextAreaElement>('#customer-question').value = e.dataset.question!; void submit('customer'); }; });
document.querySelectorAll<HTMLButtonElement>('[data-reply]').forEach(e => { e.onclick = () => { $<HTMLTextAreaElement>('#agent-question').value = e.dataset.reply!; $('#agent-question').focus(); }; });
document.querySelectorAll<HTMLButtonElement>('[data-filter]').forEach(e => { e.onclick = () => { filter = e.dataset.filter!; document.querySelectorAll('[data-filter]').forEach(b => b.classList.toggle('active', b === e)); renderRoomList(); }; });
document.querySelectorAll<HTMLButtonElement>('[data-view]').forEach(e => { e.onclick = () => { $('.workspace').dataset.view = e.dataset.view; document.querySelectorAll('[data-view].active').forEach(b => b.classList.remove('active')); e.classList.add('active'); }; });
function settings() {
  const cfg = work?.config;
  $<HTMLInputElement>('#backend-url').value = backend;
  if (cfg) {
    $<HTMLSelectElement>('#mode').value = cfg.mode; $<HTMLInputElement>('#model-url').value = cfg.url; $<HTMLInputElement>('#model-name').value = cfg.model;
    $<HTMLInputElement>('#model-key').placeholder = cfg.keyConfigured ? '已配置；留空保留，刷新也不会写入浏览器存储' : '仅保留在演示业务进程内存';
    $<HTMLTextAreaElement>('#simulation-text').value = cfg.text; $<HTMLInputElement>('#interval').value = String(cfg.interval);
  }
  mode(); $<HTMLDialogElement>('#settings').showModal();
}
function mode() { const real = $<HTMLSelectElement>('#mode').value === 'model'; $('#model-options').hidden = !real; $('#simulation-options').hidden = real; }
$('#settings-button').onclick = settings;
$('#settings').onclose = () => { $<HTMLInputElement>('#model-key').value = ''; };
$('#settings-close').onclick = () => { $<HTMLInputElement>('#model-key').value = ''; $<HTMLDialogElement>('#settings').close(); };
$<HTMLSelectElement>('#mode').onchange = mode;
$('#backend-url').onchange = () => { if (work) return; try { const url = new URL($<HTMLInputElement>('#backend-url').value); if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password) throw Error(); backend = url.origin; } catch { showError(Error('业务服务 URL 无效。')); } };
$('#config-form').onsubmit = e => { e.preventDefault(); void action(async () => {
  const apiKey = $<HTMLInputElement>('#model-key').value;
  update(await api('/config', {mode: $<HTMLSelectElement>('#mode').value, url: $<HTMLInputElement>('#model-url').value, model: $<HTMLInputElement>('#model-name').value, ...(apiKey ? {apiKey} : {}), text: $<HTMLTextAreaElement>('#simulation-text').value, interval: Number($<HTMLInputElement>('#interval').value)}));
  $<HTMLInputElement>('#model-key').value = ''; $('#config-result').textContent = '已保存，下一次 AI 回复使用新配置。';
}); };
$('#resync').onclick = () => void action(async () => { update(await api('/state')); await Promise.all([recover('customer'), recover('agent')]); });
window.addEventListener('pagehide', () => { customer?.disconnect(); agent?.disconnect(); $<HTMLInputElement>('#model-key').value = ''; });
render();
try {
  const stored = sessionStorage.getItem(storage);
  if (stored) {
    const saved = JSON.parse(stored); backend = saved.backend; capability = saved.capability; visitorId = saved.visitorId; customerRoomId = saved.customerRoomId; agentRoomId = saved.agentRoomId;
    void action(async () => { update(await api('/state')); await Promise.all([connect('customer'), connect('agent')]); });
  }
} catch { sessionStorage.removeItem(storage); }
