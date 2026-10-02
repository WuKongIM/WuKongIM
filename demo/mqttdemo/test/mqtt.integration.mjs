// Process-level browser acceptance written before the Demo implementation.
// Failure scenarios: forbidden origin, oversized/invalid provisioning input,
// unavailable backend, failed MQTT CONNECT, duplicate command, staff offline,
// reload recovery, malformed message and SDK/MQTT interoperability.
// No broker mock or application internals substitute for recipient delivery.
import assert from 'node:assert/strict';
import { spawn, execFileSync } from 'node:child_process';
import { createServer } from 'node:net';
import { once } from 'node:events';
import { mkdir, mkdtemp, writeFile, readFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';
import mqtt from 'mqtt';

const root = fileURLToPath(new URL('../../../', import.meta.url));
const { chromium } = createRequire(import.meta.url)(process.env.WK_DEMO_PLAYWRIGHT || 'playwright');
const evidenceRoot = join(root, 'tmp/mqtt-demo-acceptance');
await mkdir(evidenceRoot, { recursive: true });
const evidence = process.env.WK_MQTT_DEMO_REPORT_DIR || await mkdtemp(join(evidenceRoot, 'run-'));
await mkdir(evidence, { recursive: true });
const children = [], logs = {}, checks = [], browserErrors = [], websocketURLs = [];
let browser, page;
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const check = (name, value) => { assert(value, name); checks.push(name); };
async function until(condition, label, timeout = 60000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) { if (await condition()) return; await pause(100); }
  throw Error('Timed out: ' + label);
}
async function port() {
  const server = createServer(); server.listen(0, '127.0.0.1'); await once(server, 'listening');
  const value = server.address().port; await new Promise(resolve => server.close(resolve)); return value;
}
async function ready(url) {
  const end = Date.now() + 45000;
  while (Date.now() < end) {
    try { if ((await fetch(url, { signal: AbortSignal.timeout(1000) })).ok) return; } catch {}
    await pause(100);
  }
  throw Error('Service did not become ready: ' + url);
}
function start(name, command, args, env, cwd = root) {
  const child = spawn(command, args, { cwd, env, stdio: ['ignore', 'pipe', 'pipe'] });
  children.push(child); logs[name] = '';
  for (const output of [child.stdout, child.stderr]) output.on('data', b => { logs[name] = (logs[name] + b).slice(-1048576); });
  child.on('error', error => { logs[name] += error.message; });
  return child;
}
async function text(selector, value) { await page.locator(selector).filter({ hasText: value }).waitFor(); }
async function screenshot(name) { await page.screenshot({ path: join(evidence, name + '.png'), fullPage: true }); }
try {
  assert(process.env.WK_DEMO_SERVER_BIN, 'Supply WK_DEMO_SERVER_BIN built from this worktree');
  const [http, raft, ws, mqttWS, mqttTCP, demoPort] = await Promise.all(Array.from({ length: 6 }, port));
  const api = `http://127.0.0.1:${http}`, base = `http://127.0.0.1:${demoPort}`;
  const mqttURL = `ws://127.0.0.1:${mqttWS}/mqtt`;
  const cleanEnv = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('WK_')));
  const config = `[node]\nid=1\ndata_dir=${JSON.stringify(join(evidence, 'data'))}\n[cluster]\nid="mqtt-demo-acceptance"\nlisten_addr="127.0.0.1:${raft}"\nnodes=[{id=1,addr="127.0.0.1:${raft}"}]\ninitial_slot_count=8\nhash_slot_count=256\nslot_replica_n=1\n[api]\nlisten_addr="127.0.0.1:${http}"\nexternal_ws_addr="ws://127.0.0.1:${ws}"\n[manager]\nlisten_addr="127.0.0.1:0"\n[gateway]\ntoken_auth_on=true\nlisteners=[{name="ws",network="websocket",address="127.0.0.1:${ws}",transport="gnet",protocol="wsmux"},{name="mqtt-ws",network="websocket",address="127.0.0.1:${mqttWS}",path="/mqtt",transport="gnet",protocol="mqtt"}]\n[mqtt]\nenable=true\nlisten_addr="127.0.0.1:${mqttTCP}"\nnamespace="mqtt-demo-acceptance"\n[plugin]\nenable=false\n[log]\nlevel="warn"\ndir=${JSON.stringify(join(evidence, 'app-logs'))}\n`;
  await writeFile(join(evidence, 'wukongim.toml'), config);
  start('wukongim', process.env.WK_DEMO_SERVER_BIN, ['-config', join(evidence, 'wukongim.toml')], cleanEnv, evidence);
  await ready(api + '/readyz');
  start('mqtt-demo', process.execPath, ['server.mjs'], { ...cleanEnv, WK_DEMO_PORT: String(demoPort), WK_DEMO_API_URL: api, WK_DEMO_MQTT_WS_URL: mqttURL }, join(root, 'demo/mqttdemo'));
  await ready(base + '/mqttdemo/api/health');
  const post = (body, extra = {}) => fetch(base + '/mqttdemo/api/session', { method: 'POST', headers: { 'content-type': 'application/json', ...extra }, body });
  check('rejects_foreign_origin', (await post('{}', { origin: 'https://foreign.invalid' })).status === 403);
  check('rejects_invalid_json', (await post('{')).status === 400);
  check('bounds_provisioning_body', (await post(JSON.stringify({ x: 'x'.repeat(5000) }))).status === 413);
  check('rejects_provisioning_get', (await fetch(base + '/mqttdemo/api/session')).status === 405);
  const session = await post('{}').then(r => r.json());
  check('provisions_distinct_identities', new Set([session.device.uid, session.staff.uid, session.colleague.uid]).size === 3);
  check('publishes_explicit_mqtt_websocket_url', session.mqttWsUrl === mqttURL);

  browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1024 } });
  let interruptReceipt = false, receiptInterrupted = false;
  // Forward real MQTT frames. One controlled network fault drops the device
  // receipt before product acceptance; it never replaces broker behavior.
  await context.routeWebSocket(mqttURL, socket => {
    let device = false;
    const upstream = socket.connectToServer();
    socket.onMessage(message => {
      const bytes = Buffer.from(message);
      if ((bytes[0] >> 4) === 1) device = bytes.toString().includes('-device');
      if (device && interruptReceipt && bytes.toString().includes('"kind":"receipt"')) {
        receiptInterrupted = true; socket.close({ code: 4001, reason: 'Controlled receipt network failure' }); upstream.close({ code: 4001 }); return;
      }
      upstream.send(message);
    });
    upstream.onMessage(message => socket.send(message));
  });
  page = await context.newPage(); page.setDefaultTimeout(60000);
  page.on('pageerror', e => browserErrors.push(e.message));
  let historyRequests = 0;
  page.on('request', req => { if (req.url().includes('/channel/messagesync')) historyRequests++; });
  let malformedReceived = 0;
  const receivedFrames = [];
  page.on('websocket', socket => {
    websocketURLs.push(socket.url()); let staff = false;
    socket.on('framesent', frame => { const bytes = Buffer.from(frame.payload); if ((bytes[0] >> 4) === 1) staff = bytes.toString().includes('-staff'); });
    socket.on('framereceived', frame => {
      const bytes = Buffer.from(frame.payload), text = bytes.toString();
      if (staff && text.includes('Malformed fixture: missing correlation IDs')) malformedReceived++;
      if (text.includes('"mqtt_demo"')) receivedFrames.push({ time: new Date().toISOString(), receiver: staff ? 'staff' : 'device', bytes: bytes.length });
    });
  });
  await page.goto(base + '/mqttdemo/?home=' + encodeURIComponent(api + '/demos/'));
  check('home_link_preserves_catalog', await page.locator('[data-demo-home]').getAttribute('href') === api + '/demos/');
  const provisioned = page.waitForResponse(response => response.url() === base + '/mqttdemo/api/session' && response.request().method() === 'POST');
  await page.locator('#start').click();
  const browserSession = await (await provisioned).json();
  await text('#device-status', '在线'); await text('#staff-status', '在线');
  await screenshot('01-connected');
  // A real WS client uses this page's authenticated device identity to deliver
  // incomplete business messages. It is a fault source, never a broker mock.
  const fixture = mqtt.connect(browserSession.mqttWsUrl, { manualConnect: true, protocolVersion: 5, clientId: browserSession.device.clientId + '-malformed', username: browserSession.device.uid, password: browserSession.device.token, clean: true, reconnectPeriod: 0, properties: { sessionExpiryInterval: 0, userProperties: { 'wk.device_flag': '1' } } });
  fixture.on('error', () => {});
  let fixtureTimer;
  const fixtureDeadline = new Promise((_, reject) => { fixtureTimer = setTimeout(() => reject(Error('Malformed-message fixture deadline')), 20000); });
  try {
    await Promise.race([new Promise((resolve, reject) => { fixture.once('connect', resolve); fixture.once('error', () => reject(Error('Malformed fixture CONNECT failed'))); fixture.connect(); }), fixtureDeadline]);
    const encode = value => Buffer.from(value).toString('base64url');
    for (const [kind, destination] of [['recovery', `wk/v1/groups/${encode(browserSession.groupId)}/messages`], ['receipt', `wk/v1/users/${encode(browserSession.staff.uid)}/messages`], ['alert', `wk/v1/groups/${encode(browserSession.groupId)}/messages`]]) {
      await Promise.race([fixture.publishAsync(destination, JSON.stringify({ type: 1, content: 'Malformed fixture: missing correlation IDs', mqtt_demo: { kind, status: 'executed' } }), { qos: 1, retain: false, properties: { userProperties: { 'wk.client_msg_no': crypto.randomUUID() } } }), fixtureDeadline]);
    }
    await Promise.race([fixture.endAsync(false), fixtureDeadline]);
  } finally { clearTimeout(fixtureTimer); fixture.end(true); }
  await until(() => malformedReceived >= 3, 'three malformed messages reach the browser staff connection');
  await pause(100);
  check('malformed_messages_do_not_raise_browser_errors', browserErrors.length === 0);
  check('malformed_receipt_does_not_fake_execution', await page.locator('#execution-state').textContent() === '等待回执');
  check('malformed_recovery_does_not_close_nonexistent_alert', await page.locator('#alert-state').textContent() === '运行正常');
  await page.locator('#trigger').click();
  await text('#alert-state', '等待处置');
  await page.locator('#send-command').click();
  await text('#command-puback', '服务端已确认');
  await text('#execution-state', '设备已执行');
  await screenshot('02-command-executed');
  await text('#recovery-state', '已恢复');
  check('device_temperature_recovers', Number(await page.locator('#temperature-value').textContent()) <= -18);
  check('device_executes_once', await page.locator('#device-actions').getAttribute('data-executions') === '1');
  await page.locator('#repeat-command').click();
  await pause(500);
  check('same_command_retry_does_not_execute_twice', await page.locator('#device-actions').getAttribute('data-executions') === '1');
  await screenshot('03-recovered');

  // The SDK joins the same provisioned group; no HTTP send route simulates interop.
  await page.locator('#colleague-start').click(); await text('#colleague-status', '在线');
  await page.locator('#staff-disconnect').click(); await text('#staff-status', '离线');
  const alertsBefore = await page.locator('#staff-events [data-kind="alert"]').count();
  await page.locator('#trigger').click();
  await page.locator('#colleague-messages [data-kind="alert"]').first().waitFor();
  await pause(300);
  check('offline_staff_does_not_receive_live_alert', await page.locator('#staff-events [data-kind="alert"]').count() === alertsBefore);
  await screenshot('04-offline-alert');
  await page.locator('#staff-disconnect').click(); await text('#staff-status', '在线');
  await text('#staff-session-status', '已恢复');
  await page.waitForFunction(expected => document.querySelectorAll('#staff-events [data-kind="alert"]').length === expected, alertsBefore + 1);
  check('persistent_mqtt_session_restores_backlog', true);
  await page.locator('#colleague-note').fill('我已安排巡检，请继续观察冷柜。');
  await page.locator('#colleague-send').click();
  await page.locator('#staff-events [data-kind="note"]').filter({ hasText: '我已安排巡检' }).waitFor();
  check('mqtt_alert_reaches_sdk_and_sdk_note_reaches_mqtt', true);
  await page.locator('#send-command').click(); await text('#recovery-state', '已恢复');
  await screenshot('05-sdk-interop');

  await page.locator('#trigger').click(); await text('#alert-state', '等待处置');
  const executionsBeforeFault = Number(await page.locator('#device-actions').getAttribute('data-executions'));
  interruptReceipt = true;
  await page.locator('#send-command').click();
  await text('#device-status', '离线');
  check('real_network_fault_interrupts_receipt', receiptInterrupted);
  check('execution_without_receipt_is_not_staff_success', await page.locator('#execution-state').textContent() === '等待回执');
  await text('#device-progress', '回执待确认');
  check('device_effect_happens_once_before_receipt_fault', Number(await page.locator('#device-actions').getAttribute('data-executions')) === executionsBeforeFault + 1);
  await screenshot('06-receipt-pending');
  interruptReceipt = false;
  await page.locator('#device-reconnect').click(); await text('#device-status', '在线');
  await text('#execution-state', '设备已执行'); await text('#recovery-state', '已恢复');
  check('reconnect_resends_pending_receipt_without_reexecution', Number(await page.locator('#device-actions').getAttribute('data-executions')) === executionsBeforeFault + 1);
  await page.reload(); await text('#device-status', '在线'); await text('#staff-status', '在线');
  await text('#staff-session-status', '已恢复');
  check('reload_restores_stable_client_session', true);
  check('mqtt_recovery_does_not_poll_im_history', historyRequests === 0);
  check('browser_uses_product_mqtt_websocket', websocketURLs.filter(url => url === mqttURL).length >= 4);
  check('all_mqtt_message_ids_remain_decimal_strings', await page.locator('#staff-events [data-message-id]').evaluateAll(items => items.every(item => /^[1-9]\d*$/.test(item.getAttribute('data-message-id')))));
  check('no_browser_exceptions', browserErrors.length === 0);
  const observations = await page.locator('#staff-events [data-message-id]').evaluateAll(items => items.map(item => ({ kind: item.getAttribute('data-kind'), message_id: item.getAttribute('data-message-id'), text: item.textContent })));
  await writeFile(join(evidence, 'messages.json'), JSON.stringify(observations, null, 2));
  const mobile = await browser.newPage({ viewport: { width: 390, height: 844 } });
  await mobile.goto(base + '/mqttdemo/');
  await mobile.locator('[data-view="staff"]').click();
  check('mobile_staff_tab_works', await mobile.locator('#staff-panel').isVisible());
  check('mobile_has_no_horizontal_overflow', await mobile.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
  await mobile.screenshot({ path: join(evidence, '07-mobile.png'), fullPage: true }); await mobile.close();

  const failure = await browser.newPage();
  await failure.goto(api + '/mqttdemo/');
  await failure.locator('#settings-toggle').click();
  await failure.locator('#backend-url').fill('http://127.0.0.1:1');
  await failure.locator('#settings-save').click();
  await failure.locator('#start').click();
  await failure.locator('#error').filter({ hasText: '演示服务' }).waitFor();
  check('unavailable_backend_is_actionable', await failure.locator('#start').isEnabled());
  await failure.screenshot({ path: join(evidence, '08-unavailable-backend.png'), fullPage: true }); await failure.close();
  const authFailure = await browser.newPage(); authFailure.setDefaultTimeout(30000);
  await authFailure.route(base + '/mqttdemo/api/session', async route => {
    const response = await route.fetch(), session = await response.json(); session.staff.token = 'invalid-fixture-token';
    await route.fulfill({ response, json: session });
  });
  await authFailure.goto(base + '/mqttdemo/'); await authFailure.locator('#start').click();
  await authFailure.locator('#error').filter({ hasText: 'MQTT 连接失败' }).waitFor();
  check('real_broker_rejects_invalid_staff_credentials', await authFailure.locator('#staff-status').textContent() === '离线');
  await authFailure.screenshot({ path: join(evidence, '09-mqtt-auth-failure.png'), fullPage: true }); await authFailure.close();
  const sha256 = bytes => createHash('sha256').update(bytes).digest('hex');
  const sourceDigests = {};
  for (const path of ['AGENTS.md', 'demo/mqttdemo/server.mjs', 'demo/mqttdemo/src/main.ts', 'demo/mqttdemo/src/style.css', 'demo/mqttdemo/test/mqtt.integration.mjs', 'demo/mqttdemo/package-lock.json']) sourceDigests[path] = sha256(await readFile(join(root, path)));
  await writeFile(join(evidence, 'report.json'), JSON.stringify({ passed: true, revision: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim(), worktreeStatus: execFileSync('git', ['status', '--short'], { cwd: root, encoding: 'utf8' }), binarySha256: sha256(await readFile(process.env.WK_DEMO_SERVER_BIN)), sourceDigests, hashSlots: 256, topology: 'single-node cluster', client: 'mqtt@5.16.0', checks, websocketURLs, receivedFrames, browserErrors }, null, 2));
  console.log(JSON.stringify({ passed: true, checks: checks.length, evidence }));
} catch (error) {
  if (page) await screenshot('failure').catch(() => {});
  if (page) await writeFile(join(evidence, 'browser-state.json'), JSON.stringify(await page.evaluate(() => {
    const saved = JSON.parse(sessionStorage.getItem('wk-mqtt-demo-v1') || '{}');
    if (saved.state) delete saved.state.session;
    return saved.state || {};
  }).catch(() => ({})), null, 2));
  await writeFile(join(evidence, 'failure.json'), JSON.stringify({ passed: false, checks, browserErrors, error: error.message }, null, 2));
  console.error('Evidence: ' + evidence); throw error;
} finally {
  await browser?.close();
  for (const child of children.reverse()) {
    const exited = child.exitCode === null ? once(child, 'exit') : Promise.resolve();
    child.kill('SIGTERM'); await Promise.race([exited, pause(5000)]);
    if (child.exitCode === null) { child.kill('SIGKILL'); await exited; }
  }
  for (const [name, log] of Object.entries(logs)) await writeFile(join(evidence, name + '.log'), log);
}
