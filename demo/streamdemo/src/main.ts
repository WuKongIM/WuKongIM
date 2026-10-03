import { WKIM, WKIMEvent } from 'easyjssdk';
import { completionURL, streamCompletion, type ChatMessage, type ModelConfig } from './model';
import './style.css';
import { demoHomeURL } from '../../shared/home';
import '../../shared/home.css';

type Credentials = { api: string; reader: string; writer: string; token: string; channel: string };
type StreamData = { channel_id: string; channel_type: number; client_msg_no: string; event_key: string; payload: any; text_offset?: number };
type Row = { key: string; text: string; status: string; finished: boolean; seen: Set<string>; element: HTMLElement };
type Run = { key: string; text: string; stop: '' | 'cancel' | 'error'; index: number; controller: AbortController };
const $ = <T extends HTMLElement = HTMLElement>(selector: string) => document.querySelector<T>(selector)!;
const storageKey = 'wk-streamdemo-session';
const modelStorageKey = 'wk-streamdemo-model';
const modelProxy = document.querySelector<HTMLMetaElement>('meta[name="wk-model-proxy"]')?.content || (import.meta.env.DEV ? '/streamdemo/api/chat' : undefined);
const encode = new TextEncoder();
const decode = new TextDecoder();
const delay = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));
const rows = new Map<string, Row>();
const userRows = new Map<string, HTMLElement>();
const logs: string[] = [];
let credentials: Credentials | undefined;
let writer: WKIM | undefined;
let reader: WKIM | undefined;
let readerOnline = false;
let writerOnline = false;
let restoring = false;
let connecting = false;
let buffered: any[] = [];
let run: Run | undefined;
let sendingQuestion = false;

$('#app').innerHTML = `
  <main class="chat-shell">
    <header class="chat-header">
      <div class="brand"><a class="demo-home-link" data-demo-home><span aria-hidden="true">←</span>返回首页</a><div class="connection" data-testid="connection"><span id="dot"></span><span id="connection-state">未连接</span></div></div>
      <div class="assistant-profile">
        <svg class="avatar" viewBox="0 0 100 100" aria-hidden="true"><path d="M48 9C63 7 73 30 84 52C97 75 78 87 49 86C19 87 5 77 16 55C26 34 32 12 48 9Z" fill="#ffd35b"/><path d="M37 49h22m-37-4-7-1m58 1 7-1" stroke="#333126" stroke-width="2.4"/><ellipse cx="32" cy="44" rx="11" ry="15" fill="none" stroke="#333126" stroke-width="3"/><ellipse cx="66" cy="44" rx="11" ry="15" fill="none" stroke="#333126" stroke-width="3"/><path d="m28 43 6 2m29-2 6 2M44 67q6 5 12 0" fill="none" stroke="#333126" stroke-linecap="round" stroke-width="2.5"/><path d="m49 80-16-6v15l16-6 16 6V74l-16 6" fill="#333126"/></svg>
        <h1>流式助手</h1><p>每一句，都在抵达</p>
      </div>
      <details id="settings" class="settings"><summary>演示设置 <span aria-hidden="true">⚙</span></summary>
        <div class="settings-body">
          <h2>连接与模型</h2><p class="hint">EasySDK 真实通信，可使用模拟回复或真实聊天模型。</p>
          <label>API 地址<input id="api" aria-label="API 地址" spellcheck="false"></label>
          <button id="disconnect" class="secondary" disabled>断开接收端</button>
          <p id="identity" class="hint">演示凭据仅保存在当前标签页。</p>
          <label>回复来源<select id="mode" aria-label="回复来源"><option value="simulate">模拟回复</option><option value="model">真实模型</option></select></label>
          <div id="model-options" hidden><label>模型 URL<input id="model-url" aria-label="模型 URL" type="url" placeholder="https://your-provider.example/v1" spellcheck="false"></label>
            <label>API Key<input id="model-key" aria-label="API Key" type="password" autocomplete="off" placeholder="填写 API Key，本地模型可留空"></label>
            <label>模型名称 <span class="optional">可选</span><input id="model-name" aria-label="模型名称" placeholder="留空自动获取，可填写具体模型 ID" spellcheck="false"></label>
            <p class="hint">支持 OpenAI 兼容 Chat Completions。Key 仅用于当前页面，不保存。${modelProxy ? '模型通过本地代理调用。' : '浏览器直连；跨域受限时运行 npm start 使用本地代理。'}</p>
          </div>
          <div id="simulation-options"><label>模拟回复内容 <span class="optional">可选</span><textarea id="prompt" aria-label="模拟内容" maxlength="4096" placeholder="留空时，根据你的消息生成示例回复。填写后，助手会逐段发送这段内容。"></textarea></label>
          <div class="controls"><label>每段字符数<input id="chunk" aria-label="每段字符数" type="number" min="1" max="64" value="4"></label><label>间隔（毫秒）<input id="interval" aria-label="发送间隔（毫秒）" type="number" min="40" max="2000" value="100"></label></div>
          </div>
          <div class="actions"><button id="generate" class="secondary" disabled>开始生成</button><button id="fail" class="danger" disabled>模拟失败</button></div>
          <p class="hint">“开始生成”单独发送模拟回复，可验证离线恢复。</p>
          <details class="trace"><summary>请求与事件日志 <span id="generator-state">等待开始</span></summary><button id="clear" class="secondary">清空日志</button><pre id="logs"></pre><div class="hint">生成端预览</div><pre id="generated">等待生成。</pre></details>
          <p class="protocol">在线：SDK 实时事件<br>离线 / 重连：/channel/messagesync</p>
        </div>
      </details>
    </header>
    <section id="messages" role="log" aria-label="聊天消息" aria-live="polite">
      <div class="day-label">今天</div>
      <div class="greeting assistant-message"><div class="bubble">嗨，我是你的流式助手。</div></div>
      <div class="greeting assistant-message"><div class="bubble">给我发一条消息，看看回复如何一段一段出现。<br>想歇一下？随时可以停止生成。</div></div>
      <div id="suggestions"><button class="suggestion" data-question="你能帮我做什么？">你能帮我做什么？ <span>↗</span></button><button class="suggestion" data-question="介绍一下 WuKongIM 的流式消息">聊聊流式消息 <span>↗</span></button></div>
    </section>
    <div class="composer-area">
      <div id="error" role="alert"></div>
      <div id="connect-cta"><span id="connect-hint">连接后，开始这次对话。</span><button id="prepare">创建演示账号并连接</button><button id="retry-connect" class="secondary" hidden>重试连接</button><button id="reset-session" class="secondary" hidden>重新创建演示会话</button></div>
      <div class="composer-meta"><span id="chat-status">模拟回复 · 真实消息链路</span><button id="cancel" class="stop" disabled>取消生成</button></div>
      <form id="composer"><button id="more" type="button" aria-label="打开演示设置">＋</button><textarea id="question" aria-label="消息" rows="1" maxlength="1000" placeholder="发一条消息…" disabled></textarea><button id="send" type="submit" aria-label="发送消息" disabled><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 19V5m-6 6 6-6 6 6" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg></button></form>
    </div>
  </main>`;

$<HTMLAnchorElement>('[data-demo-home]').href = demoHomeURL(import.meta.env.DEV);

$('#api').setAttribute('value', new URLSearchParams(location.search).get('apiurl') || document.querySelector<HTMLMetaElement>('meta[name="wk-api-base"]')?.content || (import.meta.env.DEV ? 'http://127.0.0.1:5001' : location.origin));
function log(source: string, type: string, body: unknown) {
  logs.push(`${new Date().toLocaleTimeString('zh-CN')}  ${source}  ${type}\n${JSON.stringify(body, (key,value)=>key === "token" ? "[redacted]" : value, 2)}`);
  if (logs.length > 160) logs.shift();
  $('#logs').textContent = logs.join('\n\n');
  $('#logs').scrollTop = $('#logs').scrollHeight;
}
function errorText(error: unknown): string {
  if (error instanceof Error) return error.message;
  if (error && typeof error === 'object' && 'message' in error && typeof error.message === 'string') return error.message;
  return typeof error === 'string' ? error : '请求失败，请重试。';
}
function showError(error: unknown) { $('#error').textContent = errorText(error); }
function state() {
  const ready = readerOnline && writerOnline;
  $('#connection-state').textContent = connecting ? '连接中' : restoring ? '恢复中' : ready ? '在线' : readerOnline ? '生成端未连接' : credentials ? '离线' : '未连接';
  $('#dot').classList.toggle('online', ready);
  $('#disconnect').textContent = readerOnline ? '断开接收端' : '重连接收端';
  $('#disconnect').toggleAttribute('disabled', !credentials || connecting);
  $('#prepare').toggleAttribute('disabled', connecting || !!credentials);
  $('#prepare').hidden = !!credentials;
  $('#retry-connect').hidden = !credentials;
  $('#reset-session').hidden = !credentials;
  $('#retry-connect').toggleAttribute('disabled', connecting || restoring || !!run || sendingQuestion);
  $('#reset-session').toggleAttribute('disabled', connecting || restoring || !!run || sendingQuestion);
  $('#connect-hint').textContent = connecting ? '正在连接接收端与生成端…' : credentials ? '连接未就绪，可重试；凭据失效时重新创建演示会话。' : '连接后，开始这次对话。';
  $('#api').toggleAttribute('disabled', !!credentials || connecting);
  $('#generate').toggleAttribute('disabled', !writerOnline || !!run);
  $('#send').toggleAttribute('disabled', !readerOnline || !writerOnline || !!run || sendingQuestion || restoring);
  $('#question').toggleAttribute('disabled', !readerOnline || !writerOnline || sendingQuestion);
  $('#connect-cta').hidden = ready;
  $('#chat-status').textContent = run ? run.stop ? '正在结束回复…' : '助手正在回复…' : `${modelMode() ? '真实模型' : '模拟回复'} · 真实消息链路`;
  $('#composer').classList.toggle('generating', !!run);
  $('#model-options').hidden = !modelMode(); $('#simulation-options').hidden = modelMode();
  for (const id of ['mode', 'model-url', 'model-key', 'model-name']) $(`#${id}`).toggleAttribute('disabled', !!run || sendingQuestion);
  for (const id of ['cancel', 'fail']) $(`#${id}`).toggleAttribute('disabled', !run || !!run.stop);
}
async function post(route: string, body: any) {
  if (!credentials) throw Error('请先连接演示账号。');
  log('HTTP →', route, body);
  const response = await fetch(credentials.api + route, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body), signal: AbortSignal.timeout(10000) });
  const value = await response.json();
  log('HTTP ←', route, value);
  if (!response.ok || value.status && value.status !== 200) throw Error(`${route}: ${value.msg || value.error || response.status}`);
  return value;
}
function placeMessage(element: HTMLElement, sequence?: string | number) {
  if (sequence) element.dataset.sequence = String(sequence);
  const list = $('#messages');
  const ordered = [...list.querySelectorAll<HTMLElement>('[data-message-key]')];
  const next = ordered.find(other => other !== element && element.dataset.sequence && other.dataset.sequence && BigInt(other.dataset.sequence) > BigInt(element.dataset.sequence));
  list.insertBefore(element, next || null);
  // Bound both ordinary and stream message DOM/state to the recovered page size.
  while (list.querySelectorAll('[data-message-key]').length > 40) {
    const oldest = list.querySelector<HTMLElement>('[data-message-key]')!;
    rows.delete(oldest.dataset.messageKey!); userRows.delete(oldest.dataset.messageKey!); oldest.remove();
  }
}
function messagePayload(payload: any): any {
  if (typeof payload !== 'string') return payload || {};
  try { return JSON.parse(decode.decode(Uint8Array.from(atob(payload), character => character.charCodeAt(0)))); }
  catch { return {}; }
}
function showUserMessage(key: string, payload: any, sequence?: number) {
  const content = messagePayload(payload).content;
  if (typeof content !== 'string' || !key) return;
  $('#suggestions').hidden = true;
  let element = userRows.get(key);
  if (!element) { element = document.createElement('div'); element.className = 'user-message'; element.dataset.messageKey = key; element.innerHTML = '<div class="bubble body"></div>'; userRows.set(key, element); }
  element.querySelector('.body')!.textContent = content;
  placeMessage(element, sequence);
  $('#messages').scrollTop = $('#messages').scrollHeight;
}
function ensureRow(key: string, sequence?: string | number): Row {
  let row = rows.get(key);
  if (row) { if (sequence) placeMessage(row.element, sequence); return row; }
  const element = document.createElement('div'); element.className = 'stream-message assistant-message'; element.dataset.messageKey = key;
  element.innerHTML = '<div class="bubble"><div class="body"></div><span class="typing" aria-hidden="true"><i></i><i></i><i></i></span></div><small class="status">生成中</small>';
  placeMessage(element, sequence);
  row = { key, text: '', status: '生成中', finished: false, seen: new Set(), element };
  rows.set(key, row);
  return row;
}
function render(row: Row) {
  row.element.querySelector('.body')!.textContent = row.text;
  row.element.querySelector('.status')!.textContent = row.status;
  row.element.dataset.status = row.status;
  $('#messages').scrollTop = $('#messages').scrollHeight;
}
function receiveEvent(event: any) {
  const data = event.data as StreamData;
  if (!credentials || !event.type?.startsWith('stream.') || data?.channel_id !== credentials.channel || data.channel_type !== 2 || !data.client_msg_no) return;
  if (data.event_key !== 'main' && event.type !== 'stream.finish') return;
  const row = ensureRow(data.client_msg_no, event.data.message_seq);
  if (row.seen.has(event.id)) return;
  if (row.seen.size >= 2048) row.seen.delete(row.seen.values().next().value!);
  row.seen.add(event.id);
  const payload = data.payload || {};
  if (event.type === 'stream.delta' && !row.finished) {
    const delta = encode.encode(payload.delta || ''); const bytes = encode.encode(row.text);
    const offset = data.text_offset ?? bytes.length;
    if (offset > bytes.length) row.status = '增量中断，等待终态快照';
    else if (offset + delta.length > bytes.length) row.text += decode.decode(delta.slice(bytes.length - offset));
  }
  if (['stream.close', 'stream.cancel', 'stream.error', 'stream.finish', 'stream.snapshot'].includes(event.type)) {
    const snapshot = payload.snapshot || payload;
    if (snapshot.kind === 'text' && typeof snapshot.text === 'string') row.text = snapshot.text;
  }
  if (event.type === 'stream.cancel') { row.status = '已取消'; row.finished = true; }
  if (event.type === 'stream.error') { row.status = '失败'; row.finished = true; }
  if (['stream.close', 'stream.finish'].includes(event.type)) { if (!['已取消', '失败'].includes(row.status)) row.status = '完成'; row.finished = true; }
  render(row);
}
// Recovery runs once per successful connection, never as an online polling loop.
// Buffer arriving events until history has been applied; UTF-8 offsets prevent
// deltas already covered by that snapshot from being appended a second time.
async function recover() {
  if (!credentials) return;
  restoring = true; buffered = []; state();
  try {
    const history = await post('/channel/messagesync', { login_uid: credentials.reader, channel_id: credentials.channel, channel_type: 2, limit: 40, event_summary_mode: 'full' });
    for (const message of history.messages || []) {
      if (!(message.setting & 2)) { if (message.from_uid === credentials.reader) showUserMessage(message.client_msg_no, message.payload, message.message_seq); continue; }
      const row = ensureRow(message.client_msg_no, message.message_seq);
      const lane = message.event_meta?.events?.find((e: any) => e.event_key === 'main');
      if (lane?.snapshot?.kind === 'text') row.text = lane.snapshot.text || '';
      row.status = lane?.status === 'cancelled' ? '已取消' : lane?.status === 'error' ? '失败' : message.event_meta?.completed || lane?.status === 'closed' ? '完成' : '生成中';
      row.finished = row.status !== '生成中'; render(row);
    }
  } finally {
    restoring = false;
    const pending = buffered; buffered = [];
    for (const event of pending) receiveEvent(event);
    state();
  }
}
async function connect() {
  if (!credentials || connecting) return;
  connecting = true; state(); $('#error').textContent = '';
  try {
    log('HTTP →','GET /route',{uid:credentials.reader});
    const response=await fetch(credentials.api+'/route?uid='+encodeURIComponent(credentials.reader),{signal:AbortSignal.timeout(10000)});
    if(!response.ok)throw Error(`/route: ${response.status}`);
    const route=await response.json();log('HTTP ←','GET /route',route);
    const ws = route.wss_addr || route.ws_addr;
    if (!ws) throw Error('/route 未返回 WebSocket 地址，请配置外部 WebSocket 地址。');
    reader?.destroy();
    reader = WKIM.init(ws, { uid: credentials.reader, token: credentials.token, deviceFlag: 1 }, { singleton: false });
    reader.on(WKIMEvent.Message, (message: any) => {
      log('SDK ←', 'Message', { clientMsgNo: message.clientMsgNo, channelId: message.channelId, setting: message.setting });
      if (message.channelId !== credentials?.channel || !message.clientMsgNo) return;
      if (message.setting?.stream) render(ensureRow(message.clientMsgNo, message.messageSeq));
      else if (message.fromUid === credentials?.reader) showUserMessage(message.clientMsgNo, message.payload, message.messageSeq);
    });
    reader.on(WKIMEvent.CustomEvent, (event: any) => { log('SDK ←', event.type, event.data); if (restoring) { if (buffered.length < 2048) buffered.push(event); else showError('恢复期间事件过多，请重连接收端。'); } else receiveEvent(event); });
    reader.on(WKIMEvent.Disconnect, () => { readerOnline = false; state(); });
    reader.on(WKIMEvent.Connect, () => { readerOnline = true; void recover().catch(showError); });
    reader.on(WKIMEvent.Error, (error: unknown) => { log('SDK ←', 'Error', String(error)); showError(error); });
    await connectPeer(reader);
    if (!writerOnline) {
      writer?.destroy();
      writer = WKIM.init(ws, { uid: credentials.writer, token: credentials.token, deviceFlag: 1 }, { singleton: false });
      writer.on(WKIMEvent.Connect, () => { writerOnline = true; state(); });
      writer.on(WKIMEvent.Disconnect, () => { writerOnline = false; state(); });
      writer.on(WKIMEvent.Error, (error: unknown) => { log('SDK ←', 'Writer Error', errorText(error)); showError(error); });
      await connectPeer(writer);
    }
    $('#identity').textContent = `接收账号 ${credentials.reader}　·　生成账号 ${credentials.writer}　·　群频道 ${credentials.channel}`;
  } catch (error) {
    reader?.destroy(); writer?.destroy(); reader = writer = undefined;
    readerOnline = writerOnline = false;
    showError(`连接失败：${errorText(error)} 可重试连接，或重新创建演示会话。`);
  } finally { connecting = false; state(); }
}
// Bound an SDK handshake even when a WebSocket never opens; failed peers are destroyed by connect().
async function connectPeer(peer: WKIM) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    await Promise.race([peer.connect(), new Promise<never>((_, reject) => {
      timer = setTimeout(() => reject(Error('WebSocket 连接超时，请检查 API 与外部 WebSocket 地址。')), 15000);
    })]);
  } finally { if (timer) clearTimeout(timer); }
}
$('#prepare').onclick = async () => {
  const api = $('#api') as HTMLInputElement;
  try {
    const parsed = new URL(api.value); if (!['http:', 'https:'].includes(parsed.protocol)) throw Error('请输入 HTTP API 地址。');
    const id = crypto.randomUUID().slice(0, 8);
    credentials = { api: parsed.href.replace(/\/$/, ''), reader: `streamdemo-reader-${id}`, writer: `streamdemo-writer-${id}`, channel: `streamdemo-room-${id}`, token: crypto.randomUUID() };
    connecting = true; state();
    for (const uid of [credentials.reader, credentials.writer]) await post('/user/token', { uid, token: credentials.token, device_flag: 1, device_level: 0 });
    await post('/channel', { channel_id: credentials.channel, channel_type: 2, subscribers: [credentials.reader, credentials.writer] });
    sessionStorage.setItem(storageKey, JSON.stringify(credentials));
    connecting = false; await connect();
  } catch (error) { if (!readerOnline) credentials = undefined; connecting = false; state(); showError(error); }
};
$('#disconnect').onclick = () => { if (readerOnline) reader?.disconnect(); else void connect(); };
$('#retry-connect').onclick = () => { void connect(); };
$('#reset-session').onclick = () => {
  if (connecting || restoring || run || sendingQuestion) return;
  reader?.destroy(); writer?.destroy(); reader = writer = undefined;
  readerOnline = writerOnline = false; credentials = undefined;
  sessionStorage.removeItem(storageKey);
  rows.clear(); userRows.clear(); buffered = [];
  for (const message of $('#messages').querySelectorAll('[data-message-key]')) message.remove();
  $('#suggestions').hidden = false;
  $('#error').textContent = ''; $('#identity').textContent = '演示凭据仅保存在当前标签页。';
  state();
};
$('#clear').onclick = () => { logs.length = 0; $('#logs').textContent = ''; };
for (const [id, stop] of [['cancel', 'cancel'], ['fail', 'error']] as const) $(`#${id}`).onclick = () => { if (run) { run.stop = stop; run.controller.abort(); state(); } };
function modelMode() { return $<HTMLSelectElement>('#mode').value === 'model'; }
function modelConfig(): ModelConfig { return { url: completionURL($<HTMLInputElement>('#model-url').value.trim()), apiKey: $<HTMLInputElement>('#model-key').value.trim(), model: $<HTMLInputElement>('#model-name').value.trim(), proxy: modelProxy }; }
// Context follows committed message order. Failed/cancelled answers are omitted,
// and the latest 39 messages fit inside the demo proxy's bounded JSON request.
function conversation(): ChatMessage[] {
  const messages: ChatMessage[] = [];
  for (const element of $('#messages').querySelectorAll<HTMLElement>('[data-message-key]')) {
    const key = element.dataset.messageKey!; const row = rows.get(key);
    if (userRows.has(key)) messages.push({ role: 'user', content: element.querySelector('.body')!.textContent! });
    else if (row?.status === '完成' && row.text) messages.push({ role: 'assistant', content: row.text });
  }
  let chars = 0;
  return messages.slice(-39).reverse().filter(message => { chars += message.content.length; return chars <= 24000; }).reverse();
}
async function* simulate(text: string) {
  const chunks = Array.from(text);
  const size = Math.max(1, Math.min(64, Number(($<HTMLInputElement>('#chunk')).value) || 4));
  const interval = Math.max(40, Math.min(2000, Number(($<HTMLInputElement>('#interval')).value) || 180));
  for (let offset = 0; offset < chunks.length; offset += size) { yield chunks.slice(offset, offset + size).join(''); await delay(interval); }
}
async function generate(question: string, simulated = false) {
  if (!writerOnline || !writer || !credentials || run) return;
  $('#error').textContent = '';
  const config = !simulated && modelMode() ? modelConfig() : undefined;
  const active: Run = { key: crypto.randomUUID(), text: '', stop: '', index: 0, controller: new AbortController() }; run = active; state();
  const append = async (type: string, payload: any) => {
    const body = { channel_id: credentials!.channel, channel_type: 2, from_uid: credentials!.writer, client_msg_no: active.key, event_id: `${active.key}:${++active.index}`, event_type: type, event_key: 'main', payload };
    if (type === 'stream.delta') $('#logs').dataset.lastDelta = JSON.stringify(body);
    await post('/message/event', body);
  };
  try {
    log('SDK →', 'send(stream)', { channel: credentials.channel, clientMsgNo: active.key });
    const ack = await writer.send(credentials.channel, 2, { type: 1, content: '' }, { clientMsgNo: active.key, setting: { stream: true } });
    if (ack.reasonCode !== 1) throw Error(`基础消息被拒绝：${ack.reasonCode}`);
    log('SDK ←', 'SENDACK', ack);
    await append('stream.open', { kind: 'text' });
    $('#generated').textContent = ''; $('#generator-state').textContent = '生成中';
    const deltas = config ? streamCompletion(config, conversation(), active.controller.signal, model => { $<HTMLInputElement>('#model-name').value = model; saveModelSettings(); log('模型 ←', '开始回复', { model }); }) : simulate(reply(question));
    const iterator = deltas[Symbol.asyncIterator]();
    try {
      while (!active.stop) {
        let result: IteratorResult<string>;
        // Separate model/producer failures from uncertain IM event writes.
        try { result = await iterator.next(); }
        catch (error) { if (!active.stop) { active.stop = 'error'; showError(error); log('生成器', '失败', { message: '模型请求或流异常，已保存部分回复。' }); } break; }
        if (result.done || active.stop) break;
        await append('stream.delta', { kind: 'text', delta: result.value });
        active.text += result.value; $('#generated').textContent = active.text;
      }
    } finally { active.controller.abort(); await iterator.return?.(undefined).catch(() => {}); }
    const snapshot = { kind: 'text', text: active.text };
    if (active.stop) await append(`stream.${active.stop}`, { snapshot, error: active.stop === 'error' ? '生成器失败' : undefined });
    await append('stream.finish', { snapshot });
    $('#generator-state').textContent = active.stop === 'cancel' ? '已取消' : active.stop === 'error' ? '失败' : '完成';
  } catch (error) { showError(error); $('#generator-state').textContent = '写入失败，结果未确认'; }
  finally { run = undefined; state(); }
}
function reply(question: string) {
  return $<HTMLTextAreaElement>('#prompt').value || (question.includes('流式') || question.includes('WuKongIM')
    ? 'WuKongIM 的流式消息会先创建一条消息，再把回复逐段送达。\n\n你现在看到的文字，是通过 EasySDK 实时接收的。即使中途断开，重新连接后也能从消息历史恢复完整回复。\n\n试试底部的停止按钮，或者在演示设置里模拟一次失败。'
    : `收到，你想了解「${question}」。\n\n我可以帮你梳理思路、整理资料，也能把一个大问题拆成清晰的步骤。先告诉我你想推进的事情，我们就从那里开始。\n\n这是一段模拟回复。你可以在演示设置里调整回复内容和输出速度，观察消息逐段抵达。`);
}
$('#generate').onclick = () => void generate('流式消息', true).catch(showError);
$('#composer').onsubmit = async event => {
  event.preventDefault();
  const question = $<HTMLTextAreaElement>('#question'); const content = question.value.trim();
  if (!content || !credentials || !reader || !readerOnline || !writerOnline || run || sendingQuestion || restoring) return;
  sendingQuestion = true; state(); $('#error').textContent = '';
  try {
    if (modelMode()) modelConfig();
    const key = crypto.randomUUID();
    log('SDK →', 'send(question)', { channel: credentials.channel, clientMsgNo: key });
    const ack = await reader.send(credentials.channel, 2, { type: 1, content }, { clientMsgNo: key });
    if (ack.reasonCode !== 1) throw Error(`消息被拒绝：${ack.reasonCode}`);
    log('SDK ←', 'SENDACK', ack); showUserMessage(key, { content }, ack.messageSeq);
    question.value = ''; question.style.height = ''; $('#suggestions').hidden = true;
    await generate(content);
  } catch (error) { showError(error); }
  finally { sendingQuestion = false; state(); question.focus(); }
};
$('#question').onkeydown = event => { if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) { event.preventDefault(); $<HTMLFormElement>('#composer').requestSubmit(); } };
$('#question').oninput = () => { const question = $('#question'); question.style.height = 'auto'; question.style.height = `${Math.min(question.scrollHeight, 130)}px`; };
$('#more').onclick = () => { $<HTMLDetailsElement>('#settings').open = !$<HTMLDetailsElement>('#settings').open; };
for (const button of document.querySelectorAll<HTMLButtonElement>('[data-question]')) button.onclick = () => { $<HTMLTextAreaElement>('#question').value = button.dataset.question!; $('#question').focus(); };
function saveModelSettings() { sessionStorage.setItem(modelStorageKey, JSON.stringify({ mode: $<HTMLSelectElement>('#mode').value, url: $<HTMLInputElement>('#model-url').value, model: $<HTMLInputElement>('#model-name').value })); }
for (const id of ['mode', 'model-url', 'model-name']) $(`#${id}`).onchange = () => { saveModelSettings(); state(); };
try { const saved = JSON.parse(sessionStorage.getItem(modelStorageKey) || 'null'); if (saved) { $<HTMLSelectElement>('#mode').value = saved.mode === 'model' ? 'model' : 'simulate'; $<HTMLInputElement>('#model-url').value = saved.url || ''; $<HTMLInputElement>('#model-name').value = saved.model || ''; } } catch { sessionStorage.removeItem(modelStorageKey); }
state();
try {
  const saved = sessionStorage.getItem(storageKey);
  if (saved) {
    const value = JSON.parse(saved);
    if (!value || !['api','reader','writer','token','channel'].every(key => typeof value[key] === 'string' && value[key].trim())) throw Error('invalid saved session');
    const api = new URL(value.api);
    if (!['http:', 'https:'].includes(api.protocol) || api.username || api.password || api.search || api.hash) throw Error('invalid saved API');
    credentials = value; $<HTMLInputElement>('#api').value = value.api; void connect();
  }
} catch {
  credentials = undefined; sessionStorage.removeItem(storageKey);
  showError('保存的演示会话无效，请重新创建演示账号并连接。'); state();
}
window.addEventListener('pagehide', () => { if (run) { run.stop = 'cancel'; run.controller.abort(); } $<HTMLInputElement>('#model-key').value = ''; reader?.destroy(); writer?.destroy(); });
