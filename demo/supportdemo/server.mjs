import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { resolve, extname, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createSupport } from './support.mjs';

// Demo configuration belongs to this loopback business process, not WuKongIM.
const port = Number(process.env.WK_DEMO_PORT || 5177);
const api = new URL(process.env.WK_DEMO_API_URL || 'http://127.0.0.1:5001');
if (!Number.isInteger(port) || port < 1 || port > 65535 || !['http:', 'https:'].includes(api.protocol) || api.username || api.password) throw Error('Invalid Demo configuration');
const root = resolve(fileURLToPath(new URL('../../internal/access/api/demoui/supportdist/', import.meta.url)));
const support = createSupport(api);
let active = 0;
const server = createServer((req, res) => {
  void serve(req, res).catch(() => {
    if (res.destroyed) return;
    if (res.headersSent) { res.destroy(); return; }
    res.writeHead(503, {'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store'});
    res.end(JSON.stringify({error: '请求未完成，请检查连接后重试。'}));
  });
});
async function serve(req, res) {
  res.setHeader('X-Content-Type-Options', 'nosniff');
  const json = (status, value) => { if (!res.destroyed) { res.writeHead(status, {'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store'}); res.end(JSON.stringify(value)); } };
  let url;
  try { url = new URL(req.url, `http://127.0.0.1:${port}`); } catch { return json(400, {error: '地址无效。'}); }
  if (url.pathname.startsWith('/supportdemo/api/')) {
    let host;
    try { host = new URL(`http://${req.headers.host}`); } catch { return json(403, {error: '仅允许本机请求。'}); }
    if (!['127.0.0.1', 'localhost', '[::1]'].includes(host.hostname)) return json(403, {error: '仅允许本机请求。'});
    const allowed = new Set([`http://${req.headers.host}`, `https://${req.headers.host}`, api.origin]);
    if (req.headers.origin ? !allowed.has(req.headers.origin) : req.headers['sec-fetch-site'] === 'cross-site') return json(403, {error: '仅允许演示页面访问。'});
    if (req.headers.origin) { res.setHeader('Access-Control-Allow-Origin', req.headers.origin); res.setHeader('Vary', 'Origin'); }
    if (req.method === 'OPTIONS') { res.writeHead(204, {'Access-Control-Allow-Headers': 'authorization,content-type', 'Access-Control-Allow-Methods': 'GET,POST,OPTIONS'}); res.end(); return; }
    if (!['GET', 'POST'].includes(req.method)) return json(405, {error: '请求方式无效。'});
    if (active >= 32) return json(503, {error: '演示繁忙，请稍后重试。'});
    active++;
    const timer = setTimeout(() => { json(408, {error: '请求正文读取超时。'}); req.destroy(); }, 15000);
    try {
      let input = {};
      if (req.method === 'POST') {
        if (!req.headers['content-type']?.startsWith('application/json')) return json(415, {error: '需要 JSON 请求。'});
        const chunks = []; let bytes = 0;
        for await (const chunk of req) { bytes += chunk.length; if (bytes > 65536) return json(413, {error: '请求超过 64 KiB。'}); chunks.push(chunk); }
        try { input = JSON.parse(Buffer.concat(chunks)); if (!input || Array.isArray(input) || typeof input !== 'object') throw Error(); } catch { return json(400, {error: 'JSON 格式无效。'}); }
      }
      clearTimeout(timer);
      const result = await support.handle(req, res, url.pathname.slice('/supportdemo/api'.length), input);
      json(result.status, result.body);
    } finally { clearTimeout(timer); active--; }
    return;
  }
  if (!['GET', 'HEAD'].includes(req.method)) { res.writeHead(405); res.end(); return; }
  if (url.pathname === '/' || url.pathname === '/supportdemo') { res.writeHead(302, {Location: '/supportdemo/'}); res.end(); return; }
  let file;
  try { file = url.pathname === '/supportdemo/' ? 'index.html' : url.pathname.startsWith('/supportdemo/assets/') ? decodeURIComponent(url.pathname.slice('/supportdemo/'.length)) : ''; }
  catch { return json(400, {error: '资源地址无效。'}); }
  const filename = resolve(root, file);
  if (!file || !filename.startsWith(root + sep)) { res.writeHead(404); res.end(); return; }
  try {
    let body = await readFile(filename);
    if (file === 'index.html') body = Buffer.from(body.toString().replace('</head>', `<meta name="wk-support-backend" content="same-origin"><meta name="wk-demo-home" content="${api.origin.replaceAll('&', '&amp;').replaceAll('"', '&quot;')}/demos/"></head>`));
    res.writeHead(200, {'content-type': {'.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8'}[extname(filename)] || 'application/octet-stream', 'cache-control': file === 'index.html' ? 'no-store' : 'public, max-age=31536000, immutable'});
    res.end(req.method === 'HEAD' ? undefined : body);
  } catch { res.writeHead(404); res.end('Run npm run build first.'); }
}
server.listen(port, '127.0.0.1', () => console.log(`Support demo: http://127.0.0.1:${port}/supportdemo/`));
for (const signal of ['SIGTERM', 'SIGINT']) process.once(signal, async () => {
  server.close();
  await support.close(); server.closeAllConnections();
});
