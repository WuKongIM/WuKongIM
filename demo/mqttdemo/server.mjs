import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { resolve, extname, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import { randomUUID } from 'node:crypto';

// This loopback helper only provisions identities/membership. All messages are
// sent by real browser MQTT or IM clients, never by this process.
const port = Number(process.env.WK_DEMO_PORT || 5179);
const api = new URL(process.env.WK_DEMO_API_URL || 'http://127.0.0.1:5001');
const mqttURL = new URL(process.env.WK_DEMO_MQTT_WS_URL || 'ws://127.0.0.1:1884/mqtt');
if (!Number.isInteger(port) || port < 1 || port > 65535 || !['http:', 'https:'].includes(api.protocol) || api.username || api.password || !['ws:', 'wss:'].includes(mqttURL.protocol) || mqttURL.username || mqttURL.password || mqttURL.search || mqttURL.hash) throw Error('Invalid MQTT Demo configuration');
const root = resolve(fileURLToPath(new URL('../../internal/access/api/demoui/mqttdist/', import.meta.url)));
const provisions = new Map();
let creating = false, active = 0;
async function product(path, body) {
  const response = await fetch(new URL(path, api), { method: body === undefined ? 'GET' : 'POST', headers: { 'content-type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(10000), redirect: 'error' });
  const value = await response.json();
  if (!response.ok || value.status && value.status !== 200) throw Error('Product request not confirmed');
  return value;
}
const server = createServer((req, res) => { void serve(req, res).catch(() => {
  if (res.destroyed) return;
  if (res.headersSent) { res.destroy(); return; }
  res.writeHead(503, { 'content-type': 'application/json', 'cache-control': 'no-store' });
  res.end(JSON.stringify({ error: '演示服务无法准备账号，请检查 WuKongIM 配置后重试。' }));
}); });
async function serve(req, res) {
  res.setHeader('X-Content-Type-Options', 'nosniff');
  const json = (status, body) => { if (!res.destroyed) { res.writeHead(status, { 'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store' }); res.end(JSON.stringify(body)); } };
  const url = new URL(req.url, `http://127.0.0.1:${port}`);
  if (url.pathname.startsWith('/mqttdemo/api/')) {
    let host;
    try { host = new URL('http://' + req.headers.host); } catch { return json(403, { error: '仅允许本机请求。' }); }
    if (!['127.0.0.1', 'localhost', '[::1]'].includes(host.hostname)) return json(403, { error: '仅允许本机请求。' });
    const origins = new Set([`http://${req.headers.host}`, `https://${req.headers.host}`, api.origin]);
    if (req.headers.origin ? !origins.has(req.headers.origin) : req.headers['sec-fetch-site'] === 'cross-site') return json(403, { error: '仅允许演示页面访问。' });
    if (req.headers.origin) { res.setHeader('Access-Control-Allow-Origin', req.headers.origin); res.setHeader('Vary', 'Origin'); }
    if (req.method === 'OPTIONS') { res.writeHead(204, { 'Access-Control-Allow-Headers': 'content-type', 'Access-Control-Allow-Methods': 'GET,POST,OPTIONS' }); res.end(); return; }
    if (url.pathname === '/mqttdemo/api/health' && req.method === 'GET') return json(200, { ready: true });
    if (url.pathname !== '/mqttdemo/api/session') return json(404, { error: '演示接口不存在。' });
    if (req.method !== 'POST') return json(405, { error: '创建演示需使用 POST。' });
    if (!req.headers['content-type']?.startsWith('application/json')) return json(415, { error: '需要 JSON 请求。' });
    if (active >= 8) return json(429, { error: '演示请求过多，请稍后重试。' });
    active++;
    const timer = setTimeout(() => { json(408, { error: '请求正文读取超时。' }); req.destroy(); }, 10000);
    try {
      let bytes = 0; const chunks = [];
      for await (const chunk of req) { bytes += chunk.length; if (bytes > 4096) return json(413, { error: '请求不能超过 4 KiB。' }); chunks.push(chunk); }
      clearTimeout(timer);
      let input;
      try { input = JSON.parse(Buffer.concat(chunks).toString() || '{}'); if (!input || typeof input !== 'object' || Array.isArray(input) || Object.keys(input).length) throw Error(); } catch { return json(400, { error: '创建演示仅接受空 JSON 对象。' }); }
      for (const [id, time] of provisions) if (Date.now() - time > 3600000) provisions.delete(id);
      if (creating || provisions.size >= 8) return json(429, { error: '最多同时准备 8 个演示，请稍后重试。' });
      creating = true;
      try {
        const id = randomUUID();
        const identity = role => ({ uid: `mqttdemo-${id}-${role}`, token: randomUUID(), clientId: `mqttdemo-${id}-${role}` });
        const device = identity('device'), staff = identity('staff'), colleague = identity('colleague'), groupId = `mqttdemo-store-${id}`;
        // Tokens are Web credentials for both MQTT and the later EasySDK step.
        for (const person of [device, staff, colleague]) await product('/user/token', { uid: person.uid, token: person.token, device_flag: 1, device_level: 0 });
        await product('/channel', { channel_id: groupId, channel_type: 2, subscribers: [device.uid, staff.uid, colleague.uid] });
        const route = await product('/route?uid=' + encodeURIComponent(colleague.uid));
        const wsUrl = route.wss_addr || route.ws_addr;
        if (!wsUrl || !['ws:', 'wss:'].includes(new URL(wsUrl).protocol)) throw Error('Missing IM WebSocket address');
        provisions.set(id, Date.now());
        json(200, { id, device, staff, colleague, groupId, mqttWsUrl: mqttURL.href, wsUrl });
      } finally { creating = false; }
    } finally { clearTimeout(timer); active--; }
    return;
  }
  if (!['GET', 'HEAD'].includes(req.method)) { res.writeHead(405); res.end(); return; }
  if (url.pathname === '/' || url.pathname === '/mqttdemo') { res.writeHead(302, { location: '/mqttdemo/' + url.search }); res.end(); return; }
  let file;
  try { file = url.pathname === '/mqttdemo/' ? 'index.html' : url.pathname.startsWith('/mqttdemo/assets/') ? decodeURIComponent(url.pathname.slice('/mqttdemo/'.length)) : ''; } catch { res.writeHead(400); res.end(); return; }
  const filename = resolve(root, file);
  if (!file || !filename.startsWith(root + sep)) { res.writeHead(404); res.end(); return; }
  try {
    let body = await readFile(filename);
    if (file === 'index.html') body = Buffer.from(body.toString().replace('</head>', `<meta name="wk-mqtt-backend" content="same-origin"><meta name="wk-demo-home" content="${api.origin.replaceAll('&', '&amp;').replaceAll('"', '&quot;')}/demos/"></head>`));
    res.writeHead(200, { 'content-type': { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8' }[extname(file)] || 'application/octet-stream', 'cache-control': file === 'index.html' ? 'no-store' : 'public, max-age=31536000, immutable' });
    res.end(req.method === 'HEAD' ? undefined : body);
  } catch { res.writeHead(404); res.end('Build the MQTT Demo with npm run build.'); }
}
server.listen(port, '127.0.0.1', () => console.log(`MQTT Demo: http://127.0.0.1:${port}/mqttdemo/`));
for (const signal of ['SIGTERM', 'SIGINT']) process.once(signal, () => { server.close(); server.closeAllConnections(); });
