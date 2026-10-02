// Process-level acceptance: the catalog and all five embedded Demo entrances.
// MQTT failures: missing scene card/bundle/assets or a cross-port return link.
// Checks are declared before implementing the catalog; no model calls are made.
import assert from 'node:assert/strict';
import { mkdir, mkdtemp, writeFile } from 'node:fs/promises';
import { createServer } from 'node:net';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const cwd = fileURLToPath(new URL('../', import.meta.url));
const evidence = process.env.WK_DEMO_HOME_REPORT_DIR || await mkdtemp(join(tmpdir(), 'wk-demo-home-'));
await mkdir(evidence, { recursive: true });
const children = [], logs = {}, checks = [];
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const check = (name, result) => { assert(result, name); checks.push(name); };
async function port() {
  const server = createServer(); server.listen(0, '127.0.0.1');
  await once(server, 'listening'); const value = server.address().port;
  await new Promise(resolve => server.close(resolve)); return value;
}
async function ready(url) {
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) {
    try { if ((await fetch(url, { signal: AbortSignal.timeout(1500) })).ok) return; } catch {}
    await pause(50);
  }
  throw Error('Readiness timed out: ' + url);
}
function start(name, command, args, env) {
  const child = spawn(command, args, { cwd, env, stdio: ['ignore', 'pipe', 'pipe'] });
  children.push(child); logs[name] = '';
  for (const stream of [child.stdout, child.stderr]) stream.on('data', chunk => { logs[name] = (logs[name] + chunk).slice(-131072); });
}
try {
  assert(process.env.WK_DEMO_SERVER_BIN, 'Supply a freshly built WuKongIM binary');
  const [http, raft, ws, preview] = await Promise.all(Array.from({ length: 4 }, port));
  const api = `http://127.0.0.1:${http}`, base = `http://127.0.0.1:${preview}`;
  const config = `[node]\nid = 1\ndata_dir = "${evidence}/data"\n[cluster]\nid = "demo-home-validation"\nlisten_addr = "127.0.0.1:${raft}"\nnodes = [{id = 1, addr = "127.0.0.1:${raft}"}]\ninitial_slot_count = 8\nhash_slot_count = 256\nslot_replica_n = 1\n[api]\nlisten_addr = "127.0.0.1:${http}"\nexternal_ws_addr = "ws://127.0.0.1:${ws}"\n[manager]\nlisten_addr = "127.0.0.1:0"\n[gateway]\ntoken_auth_on = true\nlisteners = [{name = "ws", network = "websocket", address = "127.0.0.1:${ws}", transport = "gnet", protocol = "wsmux"}]\n[plugin]\nenable = false\n[log]\nlevel = "warn"\ndir = "${evidence}/logs"\n`;
  await writeFile(join(evidence, 'wukongim.toml'), config);
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('WK_')));
  start('cluster', process.env.WK_DEMO_SERVER_BIN, ['-config', join(evidence, 'wukongim.toml')], env);
  await ready(api + '/readyz');
  const root = await fetch(api + '/', { redirect: 'manual' });
  check('product_root_opens_catalog', root.status === 308 && root.headers.get('location') === '/demos/');
  const redirect = await fetch(api + '/demos?from=test', { redirect: 'manual' });
  check('catalog_redirect_preserves_query', redirect.status === 308 && redirect.headers.get('location') === '/demos/?from=test');
  const response = await fetch(api + '/demos/'), html = await response.text();
  check('catalog_is_embedded', response.ok && html.includes('WuKongIM Demo'));
  check('five_scenario_cards', [...html.matchAll(/data-demo="[^"]+"/g)].length === 5);
  check('index_revalidates', response.headers.get('cache-control') === 'no-cache' && !!response.headers.get('etag'));
  check('conditional_index_304', (await fetch(api + '/demos/', { headers: { 'If-None-Match': response.headers.get('etag') } })).status === 304);
  const head = await fetch(api + '/demos/', { method: 'HEAD' });
  check('head_matches_index', head.ok && head.headers.get('etag') === response.headers.get('etag') && (await head.text()) === '');
  const homeAssets = [...html.matchAll(/(?:src|href)="(\/demos\/assets\/[^"]+)"/g)];
  check('local_home_assets', homeAssets.length > 0 && !/<(?:script|link|img)[^>]+(?:src|href)="https?:/i.test(html));
  for (const [, path] of homeAssets) {
    const asset = await fetch(api + path);
    check('home_asset_immutable', asset.ok && asset.headers.get('cache-control')?.includes('immutable'));
  }
  const demos = [['chat', '/demo/'], ['stream', '/streamdemo/'], ['support', '/supportdemo/'], ['agent', '/agentdemo/'], ['mqtt', '/mqttdemo/']];
  for (const [name, path] of demos) {
    check(name + '_card_destination', html.includes(`href="${path}"`));
    const page = await fetch(api + path), index = await page.text();
    check(name + '_entry_works', page.ok && index.includes('<div id="app">'));
    for (const [, asset] of index.matchAll(/(?:src|href)="(\/[^\"]+\/assets\/[^\"]+)"/g)) check(name + '_asset_works', (await fetch(api + asset)).ok);
  }
  check('missing_asset_is_404', (await fetch(api + '/demos/assets/missing.css')).status === 404);
  check('catalog_has_no_mutation_endpoint', (await fetch(api + '/demos/', { method: 'POST' })).status === 405);
  start('preview', process.execPath, ['server.mjs'], {
    ...env, WK_DEMO_PORT: String(preview), WK_DEMO_CHAT_URL: api + '/demo/', WK_DEMO_STREAM_URL: api + '/streamdemo/', WK_DEMO_SUPPORT_URL: api + '/supportdemo/', WK_DEMO_AGENT_URL: api + '/agentdemo/', WK_DEMO_MQTT_URL: api + '/mqttdemo/',
  });
  await ready(base + '/demos/');
  check('preview_home_works', (await fetch(base + '/demos/')).ok);
  for (const [name, path] of demos) {
    const entry = await fetch(base + path, { redirect: 'manual' });
    const destination = new URL(entry.headers.get('location'));
    check(name + '_preview_links_to_real_demo', entry.status === 302 && destination.origin + destination.pathname === api + path);
    check(name + '_preview_preserves_catalog_return', destination.searchParams.get('home') === base + '/demos/');
  }
  check('preview_malformed_asset_400', (await fetch(base + '/demos/assets/%XX.css')).status === 400);
  check('preview_rejects_post', (await fetch(base + '/demos/', { method: 'POST' })).status === 405);
  await writeFile(join(evidence, 'catalog.html'), html);
  await writeFile(join(evidence, 'report.json'), JSON.stringify({ passed: true, checks, hashSlots: 256, topology: 'single-node cluster' }, null, 2));
  console.log(JSON.stringify({ passed: true, checks: checks.length, evidence }));
} catch (error) {
  await writeFile(join(evidence, 'failure.json'), JSON.stringify({ passed: false, checks, error: error.message }, null, 2));
  console.error('Evidence:', evidence); throw error;
} finally {
  for (const child of children.reverse()) {
    const exited = child.exitCode === null ? once(child, 'exit') : Promise.resolve();
    child.kill('SIGTERM'); await Promise.race([exited, pause(3000)]);
    if (child.exitCode === null) { child.kill('SIGKILL'); await exited; }
  }
  for (const [name, value] of Object.entries(logs)) await writeFile(join(evidence, name + '.log'), value);
}
