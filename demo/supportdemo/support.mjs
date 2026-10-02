import { randomUUID, timingSafeEqual } from 'node:crypto';
import { setTimeout as pause } from 'node:timers/promises';
import { WKIM, WKIMEvent } from 'easyjssdk';
import { completionURL, streamCompletion } from './.runtime/model.js';

class PublicError extends Error {
  constructor(status, message) { super(message); this.status = status; }
}
const reject = (status, message) => { throw new PublicError(status, message); };
const decodePayload = payload => {
  if (typeof payload !== 'string') return payload || {};
  try { return JSON.parse(Buffer.from(payload, 'base64')); } catch { return {}; }
};
const roomView = room => ({id: room.id, channelId: room.channelId, visitorId: room.visitor.id, visitorName: room.visitor.name, status: room.status, revision: room.revision, generating: !!room.generation || !!room.pendingQuestion, unconfirmed: !!room.unconfirmed, error: room.error, preview: room.preview, updatedAt: room.updatedAt});

// This loopback Demo owns support business state. WuKongIM still owns all message
// persistence, cluster routing, SDK delivery and offline stream projections.
export function createSupport(api) {
  const workspaces = new Map();
  let activeModels = 0, creating = 0, closing = false;
  const record = (work, kind, room, detail = '') => {
    work.logs.push({time: new Date().toISOString(), kind, roomId: room?.id, detail});
    if (work.logs.length > 160) work.logs.shift();
  };
  async function product(route, body) {
    let response, value;
    try {
      response = await fetch(new URL(route, api), {method: body === undefined ? 'GET' : 'POST', headers: {'content-type': 'application/json'}, body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(10000), redirect: 'error'});
      value = await response.json();
    } catch { reject(503, 'WuKongIM 暂时不可用，请检查连接后重试。'); }
    if (!response.ok || value.status && value.status !== 200) reject(503, 'WuKongIM 未确认此次操作，请保留当前会话。');
    return value;
  }
  // One bounded lane per conversation orders writes and handoff fences.
  function serial(room, fn) {
    if (room.queued >= 24) return Promise.reject(new PublicError(429, '会话繁忙，请稍后重试。'));
    room.queued++;
    const next = room.lane.then(fn);
    room.lane = next.catch(() => {}).finally(() => { room.queued--; });
    return next;
  }
  async function send(client, room, payload, key = randomUUID(), stream = false) {
    let ack;
    try { ack = await client.send(room.channelId, 2, payload, {clientMsgNo: key, setting: {stream}}); }
    catch { reject(503, '消息结果尚未确认，请保留当前记录并检查服务。'); }
    if (ack.reasonCode !== 1) reject(503, 'WuKongIM 拒绝了消息，请检查演示账号与频道。');
    return ack;
  }
  // State changes are ordinary persisted SDK messages, so separate windows see
  // the same ownership transition and history can restore the system timeline.
  async function publish(room, status = room.status) {
    const next = {...roomView(room), status, revision: room.revision + 1, updatedAt: Date.now()};
    await send(room.work.agent.client, room, {type: 2001, support: next});
    room.status = status; room.revision = next.revision; room.updatedAt = next.updatedAt;
    record(room.work, '会话状态', room, status);
  }
  const projection = work => ({id: work.id, wsUrl: work.wsUrl, agent: {uid: work.agent.uid, token: work.agent.token}, visitors: work.visitors.map(v => ({id: v.id, uid: v.uid, token: v.token, name: v.name})), rooms: [...work.rooms.values()].map(roomView), config: {mode: work.config.mode, url: work.config.url, model: work.config.model, keyConfigured: !!work.config.apiKey, interval: work.config.interval, text: work.config.text}, logs: work.logs});
  async function connectIdentity(work, suffix, name) {
    const identity = {id: randomUUID(), uid: `support-${work.id.slice(0, 8)}-${suffix}`, token: randomUUID(), name};
    for (const flag of [1, 2]) await product('/user/token', {uid: identity.uid, token: identity.token, device_flag: flag, device_level: 0});
    const client = WKIM.init(work.wsUrl, {uid: identity.uid, token: identity.token, deviceFlag: 2}, {singleton: false});
    identity.client = client; work.clients.push(client);
    const timer = setTimeout(() => client.disconnect(), 10000);
    try { await client.connect(); } catch { reject(503, '演示账号连接失败，请检查 WebSocket 地址。'); } finally { clearTimeout(timer); }
    return identity;
  }
  async function createRoom(work, visitor) {
    if (work.rooms.size + (work.creatingRooms || 0) >= 16) reject(429, '已达到 16 个演示会话，请创建新的演示。');
    work.creatingRooms = (work.creatingRooms || 0) + 1;
    try {
      const id = randomUUID();
      const room = {id, channelId: `support-${id}`, visitor, work, status: 'ai', revision: 0, ownership: 0, lane: Promise.resolve(), queued: 0, context: [], questions: new Map(), generation: undefined, pendingQuestion: '', error: '', preview: '开始一次新的咨询', updatedAt: Date.now()};
      await product('/channel', {channel_id: room.channelId, channel_type: 2, subscribers: [visitor.uid, work.agent.uid, work.bot.uid]});
      work.rooms.set(id, room);
      await publish(room);
      await send(work.bot.client, room, {type: 1, content: '你好，我是小悟，先由我来帮你。你可以询问物流、退换货，也可以随时转人工。'});
      return room;
    } finally { work.creatingRooms--; }
  }
  const answer = question => question.includes('退')
    ? '可以的，我先帮你梳理退换货步骤。\n\n请保留商品与包装，在订单详情中选择申请售后。需要进一步处理时，点击“转人工”，客服会带着这段聊天记录继续协助你。'
    : question.includes('物流') || question.includes('订单')
      ? '我来帮你查看物流。\n\n你的演示订单 WK20260930 已发出，目前正在运输途中。物流更新后，可以在订单详情查看进展。还有其他问题，也可以直接转人工继续沟通。'
      : '收到，我会先帮你整理这个问题。\n\n这里可以演示 AI 流式回复、转人工和完整的聊天历史。需要人工协助时，点击“转人工”，我会停止回复，由客服接入后继续为你处理。';
  async function* simulated(text, interval, signal) {
    const chars = Array.from(text);
    for (let i = 0; i < chars.length; i += 4) {
      if (signal.aborted) return;
      yield chars.slice(i, i + 4).join('');
      await pause(interval, undefined, {signal});
    }
  }
  async function modelConfig(work, signal) {
    const config = work.config;
    if (config.model) return config;
    const url = new URL(config.url); url.pathname = url.pathname.replace(/\/chat\/completions$/, '/models');
    const response = await fetch(url, {headers: config.apiKey ? {Authorization: `Bearer ${config.apiKey}`} : {}, signal: AbortSignal.any([signal, AbortSignal.timeout(15000)]), redirect: 'error'});
    if (!response.ok || !response.body) throw Error('model catalog failed');
    let bytes = 0; const chunks = [];
    for await (const chunk of response.body) { bytes += chunk.byteLength; if (bytes > 65536) throw Error('catalog too large'); chunks.push(Buffer.from(chunk)); }
    const ids = JSON.parse(Buffer.concat(chunks)).data?.map(item => item.id).filter(id => typeof id === 'string' && id.length <= 256 && !/[\r\n]/.test(id) && !/embedding|tts|whisper|image|dall-e|moderation|transcrib|realtime|audio/i.test(id)) || [];
    config.model = ids.find(id => /chat/i.test(id)) || ids[0];
    if (!config.model) throw Error('model missing');
    return config;
  }
  // A generation lease is checked inside the same lane as each accepted delta.
  // Handoff revokes it before aborting, then joins terminal snapshot writes.
  async function generate(room, gen) {
    const work = room.work;
    const append = async (type, payload) => {
      const body = {channel_id: room.channelId, channel_type: 2, from_uid: work.bot.uid, client_msg_no: gen.key, event_id: `${gen.key}:${++gen.index}`, event_key: 'main', event_type: type, payload};
      await product('/message/event', body); record(work, type, room);
    };
    let opened = false;
    try {
      await serial(room, async () => {
        if (room.status !== 'ai' || room.ownership !== gen.ownership || gen.stop) return;
        await send(work.bot.client, room, {type: 1, content: ''}, gen.key, true);
        opened = true; await append('stream.open', {kind: 'text'});
      });
      if (!opened) return;
      const cfg = work.config;
      const context = room.context.slice(-39).filter(m => m.content.length <= 12000);
      while (context.reduce((n, m) => n + m.content.length, 0) > 24000) context.shift();
      const deltas = cfg.mode === 'model'
        ? streamCompletion(await modelConfig(work, gen.controller.signal), context, gen.controller.signal, () => {})
        : simulated(cfg.text || answer(context.at(-1)?.content || ''), cfg.interval, gen.controller.signal);
      for await (const delta of deltas) {
        const accepted = await serial(room, async () => {
          if (gen.stop || room.status !== 'ai' || room.ownership !== gen.ownership || room.generation !== gen) return false;
          await append('stream.delta', {kind: 'text', delta}); gen.text += delta; room.preview = gen.text.slice(-100); return true;
        });
        if (!accepted) break;
      }
    } catch {
      if (!gen.stop) { gen.stop = 'error'; room.error = 'AI 暂时无法继续回复，可以重试或转人工。'; record(work, 'AI 失败', room, '已保留部分回复'); }
    } finally {
      gen.controller.abort();
      try {
        if (opened) await serial(room, async () => {
          const snapshot = {kind: 'text', text: gen.text};
          if (gen.stop) await append(`stream.${gen.stop}`, {snapshot, error: gen.stop === 'error' ? 'AI 回复失败' : undefined});
          await append('stream.finish', {snapshot});
        });
        gen.confirmed = true;
      } catch { gen.confirmed = false; room.error = '回复结果尚未确认，暂时不能接入人工。请新建演示或检查服务。'; }
      await serial(room, async () => {
        if (!gen.stop && gen.text) { room.context.push({role: 'assistant', content: gen.text}); room.context = room.context.slice(-39); }
        room.generation = undefined; activeModels--;
        if (!gen.confirmed) room.unconfirmed = true;
        await publish(room).catch(() => { room.error = '会话状态结果未确认，请重新同步。'; });
      });
    }
  }
  async function receiveQuestion(work, message) {
    const room = [...work.rooms.values()].find(r => r.channelId === message.channelId);
    if (!room || message.fromUid !== room.visitor.uid) return;
    const key = message.clientMsgNo;
    await serial(room, async () => {
      if (!room.questions.has(key) || room.pendingQuestion !== key) return;
      room.pendingQuestion = '';
      const content = decodePayload(message.payload).content;
      if (typeof content !== 'string') return;
      room.context.push({role: 'user', content}); room.context = room.context.slice(-39);
      if (room.status !== 'ai' || room.generation || room.unconfirmed) return;
      const gen = {key: randomUUID(), ownership: room.ownership, index: 0, text: '', stop: '', controller: new AbortController(), confirmed: false};
      room.generation = gen; activeModels++;
      gen.done = generate(room, gen).catch(() => { room.error = 'AI 回复状态未确认，请重新同步。'; });
    });
  }
  // Retries share one result and one message key; changed bodies cannot reuse it.
  function operation(work, route, input, fn) {
    const id = input.requestId;
    if (typeof id !== 'string' || !id || id.length > 80 || /[\r\n]/.test(id)) reject(400, '需要稳定的 requestId。');
    const fingerprint = JSON.stringify(Object.fromEntries(Object.entries({...input, route}).sort(([a], [b]) => a.localeCompare(b))));
    const existing = work.operations.get(id);
    if (existing) { if (existing.fingerprint !== fingerprint) reject(409, '同一次请求不能修改内容，请使用新的请求编号。'); return existing.promise; }
    if (work.operations.size >= 256) {
      const old = [...work.operations].find(([, entry]) => entry.settled);
      if (!old) reject(429, '演示请求过多，请稍后重试。');
      work.operations.delete(old[0]);
    }
    const entry = {fingerprint, settled: false};
    entry.promise = Promise.resolve().then(fn).finally(() => { entry.settled = true; });
    work.operations.set(id, entry); return entry.promise;
  }
  function authorize(req) {
    const supplied = req.headers.authorization?.replace(/^Bearer /, '') || '';
    for (const work of workspaces.values()) {
      const a = Buffer.from(supplied), b = Buffer.from(work.token);
      if (a.length === b.length && timingSafeEqual(a, b)) { work.lastActive = Date.now(); return work; }
    }
    reject(401, '演示已过期，请重新开始。');
  }
  function findRoom(work, id) { const room = work.rooms.get(id); if (!room) reject(404, '未找到当前演示中的会话。'); return room; }
  async function dispatch(req, route, input) {
    if (closing) reject(503, '演示正在关闭。');
    if (route === '/health' && req.method === 'GET') return {ready: true};
    if (route === '/session' && req.method === 'POST') {
      if (workspaces.size + creating >= 8) reject(429, '已有 8 个演示，请稍后再创建。');
      creating++;
      const work = {id: randomUUID(), token: randomUUID(), visitors: [], rooms: new Map(), clients: [], operations: new Map(), logs: [], lastActive: Date.now(), config: {mode: 'simulation', url: '', apiKey: '', model: '', interval: 90, text: ''}};
      try {
        const routeInfo = await product('/route?uid=support-' + work.id.slice(0, 8));
        work.wsUrl = routeInfo.wss_addr || routeInfo.ws_addr;
        if (!work.wsUrl) reject(503, '服务未发布可用的 WebSocket 地址。');
        work.agent = await connectIdentity(work, 'agent', '小悟客服');
        work.bot = await connectIdentity(work, 'bot', '小悟 AI');
        work.bot.client.on(WKIMEvent.Message, m => { void receiveQuestion(work, m).catch(() => record(work, '处理失败', undefined, '请重新同步')); });
        for (const [suffix, name] of [['lin', '林同学'], ['chen', '陈同学']]) {
          const visitor = await connectIdentity(work, suffix, name); work.visitors.push(visitor); await createRoom(work, visitor);
        }
        workspaces.set(work.id, work); return {...projection(work), token: work.token};
      } catch (error) { for (const client of work.clients) client.destroy(); throw error; }
      finally { creating--; }
    }
    const work = authorize(req);
    if (route === '/state' && req.method === 'GET') return projection(work);
    if (req.method !== 'POST') reject(405, '请求方式不支持。');
    if (route === '/config') {
      if ([...work.rooms.values()].some(room => room.generation || room.pendingQuestion)) reject(409, '请等待当前 AI 回复结束后再修改配置。');
      if (!['simulation', 'model'].includes(input.mode)) reject(400, '回复来源无效。');
      const apiKey = input.apiKey === undefined ? work.config.apiKey : input.apiKey;
      if (typeof apiKey !== 'string' || apiKey.length > 16384 || /[\r\n]/.test(apiKey)) reject(400, 'API Key 格式无效。');
      if (typeof (input.model || '') !== 'string' || (input.model || '').length > 256 || /[\r\n]/.test(input.model || '')) reject(400, '模型名称无效。');
      if (typeof (input.text || '') !== 'string' || (input.text || '').length > 4096) reject(400, '模拟回复超过 4096 字符。');
      let url = input.url || work.config.url;
      if (input.mode === 'model') { try { url = completionURL(url); } catch { reject(400, '请输入有效的模型 URL。'); } }
      work.config = {mode: input.mode, url, apiKey, model: input.model?.trim() || '', interval: Math.max(40, Math.min(1000, Number(input.interval) || 90)), text: input.text || ''};
      record(work, '配置更新', undefined, input.mode); return projection(work);
    }
    if (route === '/history') {
      const room = findRoom(work, input.roomId);
      if (!['customer', 'agent'].includes(input.actor)) reject(400, '角色无效。');
      return product('/channel/messagesync', {login_uid: input.actor === 'agent' ? work.agent.uid : room.visitor.uid, channel_id: room.channelId, channel_type: 2, limit: 100, event_summary_mode: 'full'});
    }
    if (route === '/new') return operation(work, route, input, async () => {
      const visitor = work.visitors.find(v => v.id === input.visitorId);
      if (!visitor) reject(404, '访客不属于当前演示。');
      const previous = [...work.rooms.values()].filter(r => r.visitor === visitor).at(-1);
      await serial(previous, async () => {
        const latest = [...work.rooms.values()].filter(r => r.visitor === visitor).at(-1);
        if (latest !== previous || latest.status !== 'closed') reject(409, '请先结束当前会话。');
        await createRoom(work, visitor);
      }); return projection(work);
    });
    const room = findRoom(work, input.roomId);
    if (route === '/send') return operation(work, route, input, async () => {
      await serial(room, async () => {
        if (!['customer', 'agent'].includes(input.actor)) reject(400, '角色无效。');
        if (room.status === 'closed' || room.unconfirmed) reject(409, '当前会话不能发送消息。');
        if (input.actor === 'agent' && room.status !== 'human') reject(409, '请先接入该会话。');
        if (typeof input.content !== 'string' || !input.content.trim() || input.content.length > 1000) reject(400, '消息需要 1–1000 个字符。');
        if (input.actor === 'customer' && room.status === 'ai' && (room.generation || room.pendingQuestion)) reject(409, 'AI 正在回复，请稍候或转人工。');
        const pendingModels = [...workspaces.values()].reduce((total, w) => total + [...w.rooms.values()].filter(r => r.status === 'ai' && r.pendingQuestion).length, 0);
        if (input.actor === 'customer' && room.status === 'ai' && activeModels + pendingModels >= 4) reject(503, 'AI 正在处理其他咨询，请稍后重试。');
        room.error = ''; room.preview = input.content.trim(); room.updatedAt = Date.now();
        const visitorSend = input.actor === 'customer';
        if (visitorSend) { room.pendingQuestion = input.requestId; room.questions.set(input.requestId, true); if (room.questions.size > 128) room.questions.delete(room.questions.keys().next().value); }
        try { await send(visitorSend ? room.visitor.client : work.agent.client, room, {type: 1, content: input.content.trim()}, input.requestId); }
        catch (error) { room.pendingQuestion = ''; room.unconfirmed = true; room.error = '消息结果尚未确认，已暂停此会话。请检查服务或新建演示。'; throw error; }
        if (visitorSend && room.status !== 'ai') room.pendingQuestion = '';
        record(work, '发送消息', room, input.actor);
      });
      return projection(work);
    });
    if (route === '/handoff') return operation(work, route, input, async () => {
      let gen;
      await serial(room, async () => {
        if (!['ai', 'handoff'].includes(room.status) || room.unconfirmed) reject(409, '当前会话不能再次转人工。');
        room.ownership++; room.status = 'handoff'; room.pendingQuestion = '';
        gen = room.generation;
        if (gen) { gen.stop = 'cancel'; gen.controller.abort(); }
        record(work, '转人工', room, '已关闭 AI 写入');
      });
      if (gen) await gen.done;
      await serial(room, async () => {
        if (room.unconfirmed || gen && !gen.confirmed) reject(503, 'AI 终态未确认，暂时不能接入人工。');
        await publish(room, 'waiting');
      });
      return projection(work);
    });
    if (route === '/accept' || route === '/end') return operation(work, route, input, async () => {
      await serial(room, async () => {
        if (room.unconfirmed || room.generation || room.status !== (route === '/accept' ? 'waiting' : 'human')) reject(409, '会话状态已变化，请重新同步。');
        room.ownership++; await publish(room, route === '/accept' ? 'human' : 'closed');
      }); return projection(work);
    });
    reject(404, '接口不存在。');
  }
  const sweep = setInterval(() => {
    for (const [id, work] of workspaces) if (Date.now() - work.lastActive > 3600000 && ![...work.rooms.values()].some(room => room.generation)) {
      for (const client of work.clients) client.destroy(); work.config.apiKey = ''; workspaces.delete(id);
    }
  }, 60000); sweep.unref();
  return {
    async handle(req, res, route, input) {
      try { return {status: 200, body: await dispatch(req, route, input)}; }
      catch (error) { return {status: error instanceof PublicError ? error.status : 503, body: {error: error instanceof PublicError ? error.message : '演示服务暂时不可用，请稍后重试。'}}; }
    },
    async close() {
      closing = true; clearInterval(sweep);
      const generations = [];
      for (const work of workspaces.values()) for (const room of work.rooms.values()) if (room.generation) { room.generation.stop = 'cancel'; room.generation.controller.abort(); generations.push(room.generation.done); }
      await Promise.allSettled(generations);
      for (const work of workspaces.values()) { work.config.apiKey = ''; for (const client of work.clients) client.destroy(); }
      workspaces.clear();
    },
  };
}
