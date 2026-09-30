import { spawn } from 'node:child_process';
import { createServer } from 'node:net';
import { createWriteStream, constants } from 'node:fs';
import { access, mkdir, readFile, rename, writeFile } from 'node:fs/promises';
import { createHash, randomUUID } from 'node:crypto';
import { once } from 'node:events';
import { resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';

// One foreground owner manages a fresh loopback-only single-node cluster and
// the Demo processes. Existing clusters, credentials and listeners are untouched.
const root = fileURLToPath(new URL('../', import.meta.url));
const args = process.argv.slice(2);
const help = `WuKongIM Demo 一键启动\n\n  node demo/start.mjs [--no-open]\n\n需要 Go、Node.js 22.12+（或 20.19+）和 npm。\n默认打开首页；--no-open 仅输出地址。Ctrl+C 关闭本次启动的全部进程。\n\n可选环境变量：\n  WK_DEMO_HOME_PORT  固定首页端口；默认 5174，占用时自动选择空闲端口\n  WK_DEMO_RUN_DIR    本次日志、配置与数据目录（必须是新目录）\n  WK_DEMO_SERVER_BIN 使用已构建的 WuKongIM 二进制，跳过 Go 构建\n`;
if (args.includes('--help') && args.length === 1) { console.log(help); process.exit(0); }
if (args.some(arg => arg !== '--no-open')) { console.error('未知参数：'+args.filter(arg=>arg!=='--no-open').join(' ')); process.exit(1); }
const [major,minor] = process.versions.node.split('.').map(Number);
if (!(major > 22 || major === 22 && minor >= 12 || major === 20 && minor >= 19)) { console.error('需要 Node.js 22.12+（或 20.19+）。'); process.exit(1); }

const directory = resolve(process.env.WK_DEMO_RUN_DIR || join(root,'demo/.runs',new Date().toISOString().replaceAll(':','-')+'-'+process.pid));
// Inherited product overrides must not redirect this disposable cluster into
// another deployment. Only the documented launcher options are propagated.
const env = Object.fromEntries(Object.entries(process.env).filter(([key])=>!key.startsWith('WK_')));
const children = [], reservations = [];
const abort = new AbortController();
let stopping = false, created = false, failure, endRun;
const stopped = new Promise(resolve => {endRun = resolve;});
const report = {status:'starting',topology:'single-node cluster',hashSlots:256,ownerPid:process.pid,directory,services:[],demos:{}};
function stop(error) {
  if (error && !failure) failure = error;
  abort.abort(); endRun();
}
for (const signal of ['SIGINT','SIGTERM']) process.once(signal,()=>stop());
const pause = ms => new Promise(resolve=>setTimeout(resolve,ms));
async function saveReport(status) {
  report.status=status; report.updatedAt=new Date().toISOString();
  if(failure) report.error=failure.message;
  await writeFile(join(directory,'run.json.tmp'),JSON.stringify(report,null,2)+'\n');
  await rename(join(directory,'run.json.tmp'),join(directory,'run.json'));
}
function interrupted() { if(abort.signal.aborted) throw failure || Error('启动已取消。'); }

// Hold reservations through preparation so every generated address is unique.
async function reserve(preferred=0, exact=false) {
  async function listen(port) {
    const socket=createServer();
    await new Promise((resolve,reject)=>{socket.once('error',reject);socket.listen(port,'127.0.0.1',resolve);});
    reservations.push(socket); return socket;
  }
  let socket;
  try {socket=await listen(preferred);} catch(error) {
    if(error.code !== 'EADDRINUSE' || exact) throw Error(`端口 ${preferred} 不可用；请修改 WK_DEMO_HOME_PORT。`);
    socket=await listen(0);
  }
  return {port:socket.address().port,release:()=>new Promise(resolve=>socket.close(resolve))};
}
// Spawn argument arrays without a shell; child output is bounded and kept in
// the run directory. Process groups include build/install descendants on Unix.
function start(name, command, argv, cwd=root, extra={}, persistent=false, url='') {
  interrupted();
  const log=join(directory,name+'.log'), stream=createWriteStream(log,{flags:'wx'});
  let bytes=0;
  const child=spawn(command,argv,{cwd,env:{...env,...extra},stdio:['ignore','pipe','pipe'],detached:process.platform!=='win32'});
  const record={name,child,log,stream,done:undefined}; children.push(record);
  stream.on('error',()=>stop(Error(`${name} 日志无法写入：${log}`)));
  for(const output of [child.stdout,child.stderr]) output.on('data',chunk=>{
    const remaining=1048576-bytes;
    if(remaining>0) {const part=chunk.subarray(0,remaining);bytes+=part.length;stream.write(part);}
  });
  record.done=new Promise(resolve=>{
    child.once('error',error=>resolve({code:null,error}));
    child.once('exit',(code,signal)=>resolve({code,signal}));
  }).then(result=>{
    stream.end();
    if(persistent && !stopping && !abort.signal.aborted) stop(Error(`${name} 提前退出；日志：${log}`));
    return result;
  });
  if(persistent) report.services.push({name,pid:child.pid,url,ready:false,log});
  return record;
}
async function command(name, executable, argv, cwd=root, extra={}, timeout=240000) {
  const record=start(name,executable,argv,cwd,extra);
  let timer;
  const limit=new Promise((_,reject)=>{timer=setTimeout(()=>reject(Error(`${name} 超时；日志：${record.log}`)),timeout);});
  try {
    const result=await Promise.race([record.done,limit,stopped.then(()=>{throw failure || Error('启动已取消。');})]);
    if(result.code!==0) throw Error(`${name} 失败${result.error?.code==='ENOENT'?'（未找到所需命令）':''}；日志：${record.log}`);
  } finally {clearTimeout(timer);}
}
async function ready(name, url, valid=()=>true) {
  const deadline=Date.now()+30000;
  while(Date.now()<deadline) {
    interrupted();
    try {const response=await fetch(url,{signal:AbortSignal.any([abort.signal,AbortSignal.timeout(1500)]),redirect:'error'});if(response.ok && await valid(response)) {
      report.services.find(s=>s.name===name).ready=true;console.log('✓ '+name+' 就绪');return;
    }} catch {}
    await pause(100);
  }
  throw Error(`${name} 未在 30 秒内就绪；日志：${join(directory,name+'.log')}`);
}
async function prepareRuntime(name) {
  const cwd=join(root,'demo',name+'demo');
  const lock=await readFile(join(cwd,'package-lock.json'));
  const digest=createHash('sha256').update(lock).digest('hex'), stamp=join(cwd,'node_modules/.wk-demo-lock');
  let installed=false;
  try {
    installed=(await readFile(stamp,'utf8'))===digest;
    const require=createRequire(join(cwd,'package.json'));
    require.resolve('easyjssdk');require.resolve('typescript');
  } catch {installed=false;}
  if(!installed) {
    console.log('准备 '+name+' 依赖…');
    await command(name+'-install',process.platform==='win32'?'npm.cmd':'npm',['ci','--no-audit','--no-fund'],cwd);
    await writeFile(stamp,digest);
  }
  await command(name+'-runtime',process.execPath,['node_modules/typescript/bin/tsc','-p','tsconfig.model.json'],cwd);
}
async function openHome(url) {
  const executable=process.platform==='darwin'?'open':process.platform==='win32'?'explorer.exe':'xdg-open';
  // Browser opening is optional; a ready Demo stays running if the OS has no opener.
  try {await command('browser',executable,[url],root,{},5000);} catch {console.log('请在浏览器打开：'+url);}
}
function signal(record, value) {
  if(record.child.exitCode!==null || record.child.signalCode!==null) return;
  try {process.kill(process.platform==='win32'?record.child.pid:-record.child.pid,value);} catch {}
}
async function cleanup() {
  stopping=true;
  for(const socket of reservations) if(socket.listening) await new Promise(resolve=>socket.close(resolve));
  for(const record of [...children].reverse()) signal(record,'SIGTERM');
  let timer;
  await Promise.race([Promise.all(children.map(r=>r.done)),new Promise(resolve=>{timer=setTimeout(resolve,5000);})]);
  clearTimeout(timer);
  for(const record of children) signal(record,'SIGKILL');
  await Promise.all(children.map(r=>r.done));
  await Promise.all(children.map(r=>r.stream.closed?undefined:once(r.stream,'close').catch(()=>{})));
  for(const service of report.services) service.ready=false;
}

try {
  const explicit=process.env.WK_DEMO_HOME_PORT;
  if(explicit && (!/^\d+$/.test(explicit) || Number(explicit)<1 || Number(explicit)>65535)) throw Error('WK_DEMO_HOME_PORT 必须为 1–65535 的端口。');
  const homePort=await reserve(Number(explicit||5174),!!explicit);
  const [http,raft,ws,manager,stream,support,agent]=await Promise.all(Array.from({length:7},()=>reserve()));
  const supplied=process.env.WK_DEMO_SERVER_BIN;
  if(supplied) {try {await access(resolve(supplied),constants.X_OK);} catch {throw Error('WK_DEMO_SERVER_BIN 必须指向可执行的 WuKongIM 二进制。');}}
  for(const bundle of ['homedist','dist','streamdist','supportdist','agentdist']) {
    try {await access(join(root,'internal/access/api/demoui',bundle,'index.html'));} catch {throw Error('缺少 '+bundle+' 页面资源，请按 demo/README.md 构建。');}
  }
  await mkdir(resolve(directory,'..'),{recursive:true});
  try {await mkdir(directory);} catch {throw Error('运行目录已存在或不可写，请更换 WK_DEMO_RUN_DIR：'+directory);}
  created=true;
  report.api=`http://127.0.0.1:${http.port}`;
  report.home=`http://127.0.0.1:${homePort.port}/demos/`;
  report.demos={chat:report.api+'/demo/',stream:`http://127.0.0.1:${stream.port}/streamdemo/`,support:`http://127.0.0.1:${support.port}/supportdemo/`,agent:`http://127.0.0.1:${agent.port}/agentdemo/`};
  await saveReport('starting'); console.log('运行目录：'+directory);
  const binary=supplied?resolve(supplied):join(directory,process.platform==='win32'?'wukongim.exe':'wukongim');
  if(!supplied) {console.log('构建 WuKongIM…');await command('build','go',['build','-o',binary,'./cmd/wukongim'],root,{GOWORK:'off'});}
  await prepareRuntime('support');await prepareRuntime('agent');
  const config=`[node]\nid = 1\ndata_dir = ${JSON.stringify(join(directory,'data'))}\n[cluster]\nid = "demo-${randomUUID()}"\nlisten_addr = "127.0.0.1:${raft.port}"\nnodes = [{id = 1, addr = "127.0.0.1:${raft.port}"}]\ninitial_slot_count = 8\nhash_slot_count = 256\nslot_replica_n = 1\n[api]\nlisten_addr = "127.0.0.1:${http.port}"\nexternal_ws_addr = "ws://127.0.0.1:${ws.port}"\n[manager]\nlisten_addr = "127.0.0.1:${manager.port}"\n[gateway]\ntoken_auth_on = true\nlisteners = [{name = "ws", network = "websocket", address = "127.0.0.1:${ws.port}", transport = "gnet", protocol = "wsmux"}]\n[plugin]\nenable = false\n[log]\nlevel = "warn"\ndir = ${JSON.stringify(join(directory,'app-logs'))}\n`;
  await writeFile(join(directory,'wukongim.toml'),config);
  for(const port of [http,raft,ws,manager]) await port.release();
  start('wukongim',binary,['-config',join(directory,'wukongim.toml')],root,{},true,report.api);
  await ready('wukongim',report.api+'/readyz',async r=>(await r.json()).ready===true);
  const route=await fetch(report.api+'/route?uid=demo-launcher',{signal:AbortSignal.timeout(3000)}).then(r=>r.json());
  if(route.ws_addr!==`ws://127.0.0.1:${ws.port}`) throw Error('WuKongIM 未发布本次演示的 WebSocket 地址。');
  for(const [name,port] of [['stream',stream],['support',support],['agent',agent]]) {
    await port.release();
    start(name,process.execPath,['server.mjs'],join(root,'demo',name+'demo'),{WK_DEMO_PORT:String(port.port),WK_DEMO_API_URL:report.api},true,report.demos[name]);
    const health=name==='stream'?report.demos[name]:report.demos[name]+'api/health';
    await ready(name,health,name==='stream'?async r=>(await r.text()).includes('wk-model-proxy'):async r=>(await r.json()).ready===true);
  }
  await homePort.release();
  start('home',process.execPath,['server.mjs'],join(root,'demo/home'),{WK_DEMO_PORT:String(homePort.port),WK_DEMO_CHAT_URL:report.demos.chat,WK_DEMO_STREAM_URL:report.demos.stream,WK_DEMO_SUPPORT_URL:report.demos.support,WK_DEMO_AGENT_URL:report.demos.agent},true,report.home);
  await ready('home',report.home,async r=>(await r.text()).includes('data-demo="agent"'));
  interrupted(); await saveReport('ready');
  console.log('\n全部 Demo 已就绪：'+report.home+'\n按 Ctrl+C 退出；日志与数据保留在运行目录。');
  if(!args.includes('--no-open')) await openHome(report.home);
  await stopped;
} catch(error) {
  if(!abort.signal.aborted || failure) failure=failure||error;
} finally {
  await cleanup();
  if(created) await saveReport(failure?'failed':'stopped');
  if(failure) {console.error('启动或运行失败：'+failure.message);process.exitCode=1;}
}
