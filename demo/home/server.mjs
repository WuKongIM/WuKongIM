import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { extname, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

// This loopback preview serves only the catalog; each Demo owns its business process.
const port = Number(process.env.WK_DEMO_PORT || 5174);
if (!Number.isInteger(port) || port < 1 || port > 65535) throw Error('Invalid preview port');
const root = resolve(fileURLToPath(new URL('../../internal/access/api/demoui/homedist/', import.meta.url)));
const destinations = new Map([
  ['/demo/', process.env.WK_DEMO_CHAT_URL || 'http://127.0.0.1:5176/demo/'],
  ['/streamdemo/', process.env.WK_DEMO_STREAM_URL || 'http://127.0.0.1:5175/streamdemo/'],
  ['/supportdemo/', process.env.WK_DEMO_SUPPORT_URL || 'http://127.0.0.1:5177/supportdemo/'],
  ['/agentdemo/', process.env.WK_DEMO_AGENT_URL || 'http://127.0.0.1:5178/agentdemo/'],
  ['/mqttdemo/', process.env.WK_DEMO_MQTT_URL || 'http://127.0.0.1:5179/mqttdemo/'],
  ['/livedemo/', process.env.WK_DEMO_LIVE_URL || 'http://127.0.0.1:5180/livedemo/'],
]);
for (const [path, value] of destinations) {
  const url = new URL(value);
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash) throw Error('Invalid Demo destination');
  destinations.set(path, url.href);
}
const server = createServer((req, res) => { void serve(req, res).catch(() => { if (!res.headersSent) res.writeHead(503); res.end(); }); });
async function serve(req, res) {
  res.setHeader('X-Content-Type-Options', 'nosniff');
  if (!['GET', 'HEAD'].includes(req.method)) { res.writeHead(405); res.end(); return; }
  const url = new URL(req.url, `http://127.0.0.1:${port}`);
  if (url.pathname === '/' || url.pathname === '/demos') { res.writeHead(302, { Location: '/demos/' + url.search }); res.end(); return; }
  if (destinations.has(url.pathname)) {
    // The catalog's own loopback address survives cross-port navigation/reloads.
    const destination = new URL(destinations.get(url.pathname));
    destination.searchParams.set('home', `http://127.0.0.1:${port}/demos/`);
    res.writeHead(302, { Location: destination.href }); res.end(); return;
  }
  let file;
  try { file = url.pathname === '/demos/' ? 'index.html' : url.pathname.startsWith('/demos/assets/') ? decodeURIComponent(url.pathname.slice('/demos/'.length)) : ''; }
  catch { res.writeHead(400); res.end(); return; }
  const filename = resolve(root, file);
  if (!file || !filename.startsWith(root + sep)) { res.writeHead(404); res.end(); return; }
  try {
    const body = await readFile(filename);
    res.writeHead(200, { 'Content-Type': extname(file) === '.css' ? 'text/css; charset=utf-8' : 'text/html; charset=utf-8', 'Cache-Control': file === 'index.html' ? 'no-store' : 'public, max-age=31536000, immutable', 'Content-Length': body.length });
    res.end(req.method === 'HEAD' ? undefined : body);
  } catch { res.writeHead(404); res.end('Build the catalog with node build.mjs first.'); }
}
server.listen(port, '127.0.0.1', () => console.log(`Demo catalog: http://127.0.0.1:${port}/demos/`));
for (const signal of ['SIGINT', 'SIGTERM']) process.once(signal, () => { server.close(); server.closeAllConnections(); });
