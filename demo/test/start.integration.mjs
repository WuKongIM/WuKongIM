// Black-box acceptance for the one-command launcher, declared before implementation.
// Failure cases: missing binary, occupied explicit port, stale run directory,
// invalid options, startup child exit, runtime child exit, and interrupted cleanup.
// Successful setup must reach real 256-hash-slot cluster readiness and SDK delivery.
// MQTT failures: missing listener/provisioning process, wrong published address,
// absent scene entrance, or a ready scene that cannot deliver a real MQTT message.
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, writeFile } from 'node:fs/promises';
import { createServer } from 'node:net';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';
import { randomUUID } from 'node:crypto';

const root = fileURLToPath(new URL('../../', import.meta.url));
const evidence = process.env.WK_DEMO_START_REPORT_DIR || await mkdtemp(join(tmpdir(), 'wk-demo-start-'));
await mkdir(evidence, {recursive: true});
const checks = [], children = [], clients = [];
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
const check = (name, value) => { assert(value, name); checks.push(name); };
const clean = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('WK_')));
async function until(fn, label, timeout = 30000) {
  const end = Date.now() + timeout;
  while (Date.now() < end) { if (await fn()) return; await pause(80); }
  throw Error('Timed out: ' + label);
}
async function json(url, body, token) {
  const response = await fetch(url, {method: body === undefined ? 'GET' : 'POST', headers: {'content-type':'application/json', ...(token ? {authorization:'Bearer '+token} : {})}, body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(15000)});
  if(!response.ok) throw Error(url + ': HTTP ' + response.status + ' ' + (await response.text()).slice(0,1024));
  return response.json();
}
function launch(name, extra = {}, args = ['--no-open']) {
  const directory = join(evidence, name);
  const child = spawn(process.execPath, [join(root, 'demo/start.mjs'), ...args], {cwd: tmpdir(), env: {...clean, WK_DEMO_SERVER_BIN: process.env.WK_DEMO_SERVER_BIN, WK_DEMO_RUN_DIR: directory, ...extra}, stdio: ['ignore','pipe','pipe']});
  child.output = ''; for (const stream of [child.stdout, child.stderr]) stream.on('data', b => {child.output = (child.output+b).slice(-131072);});
  child.done = once(child, 'exit').then(([code, signal]) => ({code, signal})); children.push(child);
  return {child, directory, report: async () => JSON.parse(await readFile(join(directory,'run.json'),'utf8'))};
}
async function exited(run, timeout = 15000) {
  let timer;
  let result;
  try {result=await Promise.race([run.child.done,new Promise((_,reject)=>{timer=setTimeout(()=>reject(Error('Launcher did not exit')),timeout);})]);}
  finally {clearTimeout(timer);}
  await writeFile(join(evidence, run.directory.split('/').at(-1) + '.log'), run.child.output);
  return result;
}
const alive = pid => {try {process.kill(pid,0);return true;} catch {return false;}};
let foreign, defaultHome;
try {
  assert(process.env.WK_DEMO_SERVER_BIN, 'Supply a freshly built WuKongIM binary');
  const help = launch('help', {}, ['--help']);
  check('help_needs_no_services', (await exited(help)).code === 0 && help.child.output.includes('node demo/start.mjs'));
  const invalid = launch('invalid', {}, ['--unknown']);
  check('unknown_option_fails_clearly', (await exited(invalid)).code !== 0 && invalid.child.output.includes('--unknown'));
  const missing = launch('missing', {WK_DEMO_SERVER_BIN: join(evidence,'missing-binary')});
  check('missing_binary_fails_clearly', (await exited(missing)).code !== 0 && missing.child.output.includes('WK_DEMO_SERVER_BIN'));
  foreign = createServer(); foreign.listen(0,'127.0.0.1'); await once(foreign,'listening');
  const occupied = launch('occupied', {WK_DEMO_HOME_PORT: String(foreign.address().port)});
  check('explicit_port_collision_fails_clearly', (await exited(occupied)).code !== 0 && occupied.child.output.includes('端口'));
  check('foreign_listener_is_preserved', foreign.listening);
  const badPort = launch('invalid-port', {WK_DEMO_HOME_PORT:'0'});
  check('invalid_port_fails_clearly', (await exited(badPort)).code !== 0 && badPort.child.output.includes('WK_DEMO_HOME_PORT'));
  defaultHome=createServer();
  await new Promise((resolve,reject)=>{defaultHome.once('error',e=>e.code==='EADDRINUSE'?resolve():reject(e));defaultHome.listen(5174,'127.0.0.1',resolve);});
  const fixture = join(evidence,'exits-early');
  await writeFile(fixture, '#!/bin/sh\nexit 7\n', {mode:0o755});
  const startup = launch('startup-exit', {WK_DEMO_SERVER_BIN: fixture});
  check('startup_failure_is_reported', (await exited(startup,90000)).code !== 0 && (await startup.report()).status === 'failed');
  check('startup_failure_has_logs', startup.child.output.includes('wukongim.log'));

  const run = launch('success', {WK_NODE_ID:'999', WK_CLUSTER_NODES:'invalid inherited config'});
  let report;
  await until(async () => {try {report = await run.report();return report.status === 'ready';} catch {return false;}}, 'all services ready', 120000);
  check('every_owned_service_is_ready', report.services.length === 6 && report.services.every(s => s.ready && alive(s.pid)));
  check('occupied_default_home_port_uses_free_port', new URL(report.home).port !== '5174');
  check('real_cluster_ready', (await json(report.api+'/readyz')).ready === true);
  check('isolated_256_hash_slot_single_node_cluster', report.hashSlots === 256 && report.topology === 'single-node cluster' && (await readFile(join(run.directory,'wukongim.toml'),'utf8')).includes('id = 1'));
  const home = await fetch(report.home).then(r=>r.text());
  check('five_cards_on_launched_home', [...home.matchAll(/data-demo="/g)].length === 5);
  check('mqtt_ws_address_is_loopback', new URL(report.mqttWs).hostname === '127.0.0.1' && new URL(report.mqttWs).pathname === '/mqtt');
  const toml = await readFile(join(run.directory,'wukongim.toml'),'utf8');
  check('mqtt_enabled_with_independent_websocket', toml.includes('[mqtt]') && toml.includes('protocol = "mqtt"') && toml.includes('path = "/mqtt"'));
  for (const [name,path] of [['chat','/demo/'],['stream','/streamdemo/'],['support','/supportdemo/'],['agent','/agentdemo/'],['mqtt','/mqttdemo/']]) {
    const response = await fetch(new URL(path,report.home), {redirect:'manual'});
    const destination = response.headers.get('location');
    const entrance = new URL(destination);
    check(name+'_home_routes_to_owned_service', response.status === 302 && entrance.origin+entrance.pathname === report.demos[name]);
    check(name+'_entrance_carries_actual_catalog', entrance.searchParams.get('home') === report.home);
    const page = await fetch(destination), html = await page.text();
    check(name+'_page_and_assets_work', page.ok && (await Promise.all([...html.matchAll(/(?:src|href)="(\/[^\"]+\/assets\/[^\"]+)"/g)].map(async ([,asset]) => (await fetch(new URL(asset,destination))).ok))).every(Boolean));
    if(name !== 'chat') check(name+'_standalone_catalog_fallback', html.includes(`name="wk-demo-home" content="${report.api}/demos/"`));
    if(name === 'stream') check('stream_has_api_and_model_proxy', html.includes(report.api) && html.includes('wk-model-proxy'));
    if(['support','agent','mqtt'].includes(name)) check(name+'_uses_same_origin_backend', html.includes(`wk-${name}-backend`));
  }
  const mqttSession = await json(new URL('/mqttdemo/api/session',report.demos.mqtt),{});
  check('mqtt_provisioning_publishes_owned_listener', mqttSession.mqttWsUrl === report.mqttWs);
  const {connectAsync} = createRequire(join(root,'demo/mqttdemo/package.json'))('mqtt');
  const mqttConnect = identity => connectAsync(mqttSession.mqttWsUrl, {
    protocolVersion:5,clientId:identity.clientId,username:identity.uid,password:identity.token,
    clean:true,reconnectPeriod:0,connectTimeout:15000,
    properties:{userProperties:{'wk.device_flag':'1'}},
  });
  const staffMqtt = await mqttConnect(mqttSession.staff);
  clients.push({destroy:()=>staffMqtt.end(true)});
  const deviceMqtt = await mqttConnect(mqttSession.device);
  clients.push({destroy:()=>deviceMqtt.end(true)});
  const topic = 'wk/v1/groups/'+Buffer.from(mqttSession.groupId).toString('base64url')+'/messages';
  const receivedMqtt = [];
  staffMqtt.on('message',(_topic,payload,packet)=>receivedMqtt.push({text:payload.toString(),packet}));
  const granted = await staffMqtt.subscribeAsync(topic,{qos:1});
  check('mqtt_websocket_subscription_ready', granted[0].qos === 1);
  const mqttClientNumber = randomUUID();
  await deviceMqtt.publishAsync(topic,JSON.stringify({type:1,content:'launcher MQTT check'}),{
    qos:1,properties:{userProperties:{'wk.client_msg_no':mqttClientNumber}},
  });
  await until(()=>receivedMqtt.some(m=>m.text.includes('launcher MQTT check')), 'launcher MQTT group delivery');
  const packet = receivedMqtt.find(m=>m.text.includes('launcher MQTT check')).packet;
  check('mqtt_real_group_delivery_identity', /^\d+$/.test(packet.properties.userProperties['wk.message_id']) && packet.properties.userProperties['wk.from_uid'] === mqttSession.device.uid && packet.properties.userProperties['wk.client_msg_no'] === mqttClientNumber);
  const {WKIM, WKIMEvent} = createRequire(join(root,'demo/agentdemo/package.json'))('easyjssdk');
  const messages = [], events = [];
  const session = await json(new URL('/agentdemo/api/session',report.demos.agent), {});
  const sdk = WKIM.init(session.wsUrl,{uid:session.user.uid,token:session.user.token,deviceFlag:1},{singleton:false}); clients.push(sdk);
  sdk.on(WKIMEvent.Message,m=>messages.push(m)); sdk.on(WKIMEvent.CustomEvent,e=>events.push(e)); await sdk.connect();
  await json(new URL('/agentdemo/api/send',report.demos.agent), {conversationId:session.conversations[0].id,content:'检索流式消息 SDK 接入资料',requestId:randomUUID()},session.token);
  await until(()=>events.some(e=>e.type==='stream.finish'), 'Agent SDK completion');
  check('agent_real_SDK_message_and_stream', messages.length > 0 && events.some(e=>e.type==='stream.delta'));
  const support = await json(new URL('/supportdemo/api/session',report.demos.support),{});
  check('support_real_SDK_sessions_connect', support.visitors.length === 2 && support.rooms.length === 2 && support.wsUrl === session.wsUrl);
  for(const client of clients) client.destroy(); clients.length=0;
  run.child.kill('SIGINT');
  check('ctrl_c_exits_successfully', (await exited(run)).code === 0);
  check('ctrl_c_stops_all_owned_children', report.services.every(s=>!alive(s.pid)));
  check('run_logs_and_terminal_report_are_kept', (await run.report()).status === 'stopped' && await readFile(join(run.directory,'wukongim.log'),'utf8') !== undefined);
  const stale = launch('success');
  check('existing_run_directory_is_preserved', (await exited(stale)).code !== 0 && (await run.report()).status === 'stopped');

  const crash = launch('runtime-exit');
  let crashReport;
  await until(async()=>{try {crashReport=await crash.report();return crashReport.status==='ready';} catch{return false;}},'crash scenario ready',120000);
  process.kill(crashReport.services.find(s=>s.name==='agent').pid,'SIGTERM');
  check('runtime_child_failure_fails_launcher', (await exited(crash)).code !== 0 && (await crash.report()).status === 'failed');
  check('runtime_failure_stops_remaining_children', crashReport.services.every(s=>!alive(s.pid)));
  await writeFile(join(evidence,'report.json'),JSON.stringify({passed:true,checks,topology:'single-node cluster',hashSlots:256},null,2));
  console.log(JSON.stringify({passed:true,checks:checks.length,evidence}));
} catch(error) {
  await writeFile(join(evidence,'failure.json'),JSON.stringify({passed:false,checks,error:error.message},null,2));
  console.error('Evidence:',evidence); throw error;
} finally {
  for(const client of clients) client.destroy();
  if(foreign?.listening) await new Promise(resolve=>foreign.close(resolve));
  if(defaultHome?.listening) await new Promise(resolve=>defaultHome.close(resolve));
  for(const child of children.reverse()) {
    if(child.exitCode !== null || child.signalCode !== null) continue;
    child.kill('SIGTERM'); await Promise.race([child.done,pause(8000)]);
    if(child.exitCode === null && child.signalCode === null) {child.kill('SIGKILL');await child.done;}
  }
}
