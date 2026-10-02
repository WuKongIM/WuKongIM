import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { resolve, extname, sep } from 'node:path';
import { createModelProxy } from './model-proxy.mjs';

// WK_DEMO_PORT controls only this loopback-bound demo/relay process.
const port = Number(process.env.WK_DEMO_PORT || 5175);
// WK_DEMO_API_URL selects the real WuKongIM Product API injected into the page.
// Model endpoints and API keys remain request-scoped browser configuration.
const api = new URL(process.env.WK_DEMO_API_URL || 'http://127.0.0.1:5001');
if (!Number.isInteger(port) || port < 1 || port > 65535 || !['http:', 'https:'].includes(api.protocol) || api.username || api.password) throw Error('Invalid WK_DEMO_PORT or WK_DEMO_API_URL');
const root = resolve(fileURLToPath(new URL('../../internal/access/api/demoui/streamdist/', import.meta.url)));
const proxy = createModelProxy();
const escape = value => value.replaceAll('&', '&amp;').replaceAll('"', '&quot;').replaceAll('<', '&lt;');
const server = createServer(async (req, res) => {
  let pathname;
  try { pathname = decodeURIComponent(new URL(req.url, `http://127.0.0.1:${port}`).pathname); } catch { res.writeHead(400); res.end(); return; }
  if (pathname === '/streamdemo/api/chat') return proxy(req, res);
  if (!['GET', 'HEAD'].includes(req.method)) { res.writeHead(405); res.end(); return; }
  if (pathname === '/' || pathname === '/streamdemo') { res.writeHead(302, { Location: '/streamdemo/' }); res.end(); return; }
  const file = pathname === '/streamdemo/' ? 'index.html' : pathname.startsWith('/streamdemo/assets/') ? pathname.slice('/streamdemo/'.length) : '';
  const filename = resolve(root, file);
  if (!file || !filename.startsWith(root + sep)) { res.writeHead(404); res.end(); return; }
  try {
    let body = await readFile(filename);
    if (file === 'index.html') body = Buffer.from(body.toString().replace('</head>', `<meta name="wk-api-base" content="${escape(api.href.replace(/\/$/, ''))}"><meta name="wk-demo-home" content="${escape(api.origin)}/demos/"><meta name="wk-model-proxy" content="/streamdemo/api/chat"></head>`));
    const mime = { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8' }[extname(file)] || 'application/octet-stream';
    res.writeHead(200, { 'Content-Type': mime, 'Cache-Control': file === 'index.html' ? 'no-store' : 'public, max-age=31536000, immutable', 'X-Content-Type-Options': 'nosniff', 'Content-Length': body.length });
    res.end(req.method === 'HEAD' ? undefined : body);
  } catch { res.writeHead(404); res.end('Build the demo with npm run build first.'); }
});
server.listen(port, '127.0.0.1', () => console.log(`Stream demo: http://127.0.0.1:${port}/streamdemo/`));
for (const signal of ['SIGINT', 'SIGTERM']) process.once(signal, () => { proxy.abortAll(); server.close(); server.closeAllConnections(); });
