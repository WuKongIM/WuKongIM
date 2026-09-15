// Bounded opt-in experiment: merged SDK, authenticated BFF, 3 real processes.
// Run with WK_EDIT_SERVER_BIN, WK_EDIT_SDK_ROOT and WK_EDIT_PRESSURE_REPORT.
// Optional WK_EDIT_MIN_INTERVAL_MS caps each closed-loop channel's edit cadence.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const vm = require('node:vm');
const net = require('node:net');
const { spawn, spawnSync } = require('node:child_process');
const { webcrypto, createHash } = require('node:crypto');
const delay = ms => new Promise(r => setTimeout(r, ms));
async function until(check, label, ms = 30000) {
  const deadline = Date.now() + ms;
  while (Date.now() < deadline) { if (await check()) return; await delay(5); }
  throw new Error(`Timeout: ${label}`);
}
async function port() {
  const s = net.createServer(); await new Promise(r => s.listen(0, '127.0.0.1', r));
  const p = s.address().port; await new Promise(r => s.close(r)); return p;
}
function quantiles(values) {
  const v = [...values].sort((a,b) => a-b);
  return { samples:v.length, p50_ms:v[Math.ceil(v.length*.50)-1], p95_ms:v[Math.ceil(v.length*.95)-1], p99_ms:v[Math.ceil(v.length*.99)-1], max_ms:v.at(-1) };
}
async function main() {
  const binary=process.env.WK_EDIT_SERVER_BIN, sdkRoot=process.env.WK_EDIT_SDK_ROOT, output=process.env.WK_EDIT_PRESSURE_REPORT;
  assert.ok(binary && sdkRoot && output, 'required explicit binary/SDK/report paths');
  const editIntervalMs=Number(process.env.WK_EDIT_MIN_INTERVAL_MS||0);
  assert.ok(Number.isInteger(editIntervalMs)&&editIntervalMs>=0&&editIntervalMs<=5000, 'bounded edit interval 0..5000ms');
  const {createEditingDemo}=require(path.join(sdkRoot,'examples/message-editing/server.cjs'));
  const library=await fs.readFile(path.join(sdkRoot,'lib/wukongimjssdk.umd.js'),'utf8');
  const report={complete:false, started_at:new Date().toISOString(), node:process.version, platform:`${os.platform()}/${os.arch()}`, logical_cpus:os.cpus().length,
    driver_sha256:createHash('sha256').update(await fs.readFile(__filename)).digest('hex'),
    binary_sha256:createHash('sha256').update(await fs.readFile(binary)).digest('hex'), sdk_bundle_sha256:createHash('sha256').update(library).digest('hex'),
    nodes:3, hash_slots:256, physical_slots:8, replicas:3, channels:16, online_clients:32, rounds_per_channel:32, closed_loop_min_interval_ms:editIntervalMs};
  const dir=await fs.mkdtemp(path.join(os.tmpdir(),'wk-edit-pressure-'));
  const nodes=[], clients=[], bffs=[];
  let sampler,loopSampler;
  const loopDelays=[],sampleDurations=[];
  try {
    for(let i=0;i<3;i++) nodes.push({id:i+1,api:await port(),raft:await port(),ws:await port(),log:'',max_rss_kib:0});
    const voters=nodes.map(n=>`{id=${n.id},addr="127.0.0.1:${n.raft}"}`).join(',');
    for(const n of nodes) {
      const config=`[node]\nid=${n.id}\ndata_dir="${dir}/data-${n.id}"\n[cluster]\nid="sdk-pressure"\nlisten_addr="127.0.0.1:${n.raft}"\nnodes=[${voters}]\ninitial_slot_count=8\nhash_slot_count=256\nslot_replica_n=3\nstart_timeout="60s"\n[api]\nlisten_addr="127.0.0.1:${n.api}"\nexternal_ws_addr="ws://127.0.0.1:${n.ws}"\n[manager]\nlisten_addr="127.0.0.1:0"\n[gateway]\ntoken_auth_on=true\nlisteners=[{name="ws",network="websocket",address="127.0.0.1:${n.ws}",transport="gnet",protocol="wsmux"}]\n[log]\nlevel="warn"\ndir="${dir}/logs-${n.id}"\n`;
      const file=path.join(dir,`node-${n.id}.toml`); await fs.writeFile(file,config);
      n.process=spawn(binary,['-config',file],{cwd:dir,env:{...Object.fromEntries(Object.entries(process.env).filter(([k])=>!k.startsWith('WK_'))),WK_CLUSTER_CHANNEL_REPLICA_N:'3',GOMAXPROCS:'2'},stdio:['ignore','pipe','pipe']});
      n.exit=new Promise(r=>n.process.once('exit',r));
      for(const stream of [n.process.stdout,n.process.stderr]) stream.on('data',b=>{n.log=(n.log+b).slice(-65536)});
      n.url=`http://127.0.0.1:${n.api}`;
    }
    await until(async()=>{const ready=await Promise.all(nodes.map(async n=>{try{return (await fetch(n.url+'/route',{signal:AbortSignal.timeout(1000)})).ok}catch{return false}}));return ready.every(Boolean)},'three-node readiness',75000);
    const users={};for(let i=0;i<16;i++) for(const p of ['a','b']) users[`${p}${i}`]=`pressure-credential-${p}${i}`;
    for(const n of nodes) {const bff=createEditingDemo({productURL:n.url,users});await new Promise(r=>bff.listen(0,'127.0.0.1',r));bffs.push(bff);n.bff=`http://127.0.0.1:${bff.address().port}`}
    const apiSamples=[],visibleSamples=[],calibrationSamples=[],slowSamples=[];
    async function client(uid,peer,nodeIndex) {
      const timers=new Set(), rows=new Map(), visible=new Map(), errors=[], events=[], hintTimes=new Map(),feedTimes=[];
      const context=vm.createContext({console:{log(){},warn(){},error(){}},WebSocket,AbortController,Uint8Array,ArrayBuffer,TextEncoder,TextDecoder,crypto:webcrypto,setTimeout,clearTimeout,
        setInterval:(...args)=>{const t=setInterval(...args);t.unref();timers.add(t);return t},clearInterval});
      vm.runInContext(library,context);const wk=context.wk,sdk=wk.WKSDK.shared();
      let measure=false;
      const transport=async(route,body,signal)=>{
        const start=performance.now();
        const r=await fetch(nodes[nodeIndex].bff+route,{method:'POST',headers:{'content-type':'application/json',authorization:`Bearer ${uid}:${users[uid]}`},body:JSON.stringify(body),signal:signal||AbortSignal.timeout(10000)});
        const data=await r.json();
        if(measure&&route==='/message/update') apiSamples.push(performance.now()-start);
        if(route==='/channel/messageupdates'){feedTimes.push({start,done:performance.now(),status:r.status,reset_required:data.reset_required,content_epoch:r.headers.get('x-wk-content-epoch')||data.content_epoch,versions:data.updates?.map(m=>m.version)});if(feedTimes.length>32)feedTimes.shift()}
        return {status:r.status,body:data,contentEpoch:r.headers.get('x-wk-content-epoch')||undefined};
      };
      const session=(await transport('/session',{})).body;
      sdk.config.uid=uid;sdk.config.token=session.token;sdk.config.addr=session.websocket;
      const uninstall=wk.installMessageEditing(sdk,{transport,onError:e=>errors.push(e.code||e.message)});
      sdk.chatManager.addMessageUpdateListener(messages=>messages.forEach(m=>{if(rows.has(m.messageID)){rows.set(m.messageID,m);const key=`${m.messageID}/${m.contentVersion}`;if(!m.contentStale&&!visible.has(key))visible.set(key,performance.now())}}));
      sdk.eventManager.addEventListener(e=>{if(e.type==='message_updated'){assert.ok(!('payload' in e.dataJson));events.push(e.dataJson);if(!hintTimes.has(`${e.dataJson.message_id}/${e.dataJson.version}`))hintTimes.set(`${e.dataJson.message_id}/${e.dataJson.version}`,performance.now())}});
      const channel=new wk.Channel(peer,1);sdk.conversationManager.openConversation=Object.assign(new wk.Conversation(),{channel});sdk.connect();
      const result={sdk,wk,rows,visible,errors,events,hintTimes,feedTimes,channel,node:nodeIndex+1,measure(){measure=true},async history(){const list=await sdk.chatManager.syncMessages(channel,Object.assign(new wk.SyncOptions(),{pullMode:wk.PullMode.Up}));list.forEach(m=>rows.set(m.messageID,m));return list},close(){uninstall();sdk.disconnect();for(const t of timers)clearInterval(t)}};
      clients.push(result);await until(()=>sdk.messageUpdateManager.hintsReady,`${uid} hint opt-in`);return result;
    }
    const pairs=[];
    for(let i=0;i<16;i++) {
      const a=await client(`a${i}`,`b${i}`,i%3),b=await client(`b${i}`,`a${i}`,(i+1)%3);
      const response=await fetch(nodes[i%3].url+'/message/send',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({from_uid:`a${i}`,channel_id:`b${i}`,channel_type:1,header:{red_dot:1},payload:Buffer.from(JSON.stringify({type:1,content:'original'})).toString('base64')})});
      assert.equal(response.status,200,await response.text());
      await until(async()=>{try{return (await a.history()).length===1&&(await b.history()).length===1}catch{return false}},'initial history');
      await b.sdk.conversationManager.sync();const conversation=b.sdk.conversationManager.findConversation(b.channel);
      pairs.push({a,b,conversation,unread:conversation.unread,timestamp:conversation.timestamp,id:[...a.rows.keys()][0]});
    }
    function sample() {
      const sampleStarted=performance.now();
      const p=spawnSync('ps',['-p',nodes.map(n=>n.process.pid).join(','),'-o','pid=,rss=,time='],{encoding:'utf8'});
      sampleDurations.push(performance.now()-sampleStarted);
      if(p.status!==0) throw new Error(`ps failed: ${p.stderr}`);
      const result=[];
      for(const line of p.stdout.trim().split('\n')) {const [pid,rss,cpu]=line.trim().split(/\s+/);const n=nodes.find(n=>n.process.pid===Number(pid));if(n){n.max_rss_kib=Math.max(n.max_rss_kib,Number(rss));result.push({node:n.id,rss_kib:Number(rss),cumulative_cpu:cpu})}}
      assert.equal(result.length,3);return result;
    }
    report.resources_before=sample();sampler=setInterval(()=>{try{sample()}catch(e){report.sampling_error=e.message}},500);
    console.log('Measuring 16 concurrent channels, 512 edits, real cross-node SDK recipients');
    const start=performance.now();let lastLoop=start;
    loopSampler=setInterval(()=>{const now=performance.now();if(now-lastLoop>150&&loopDelays.length<32)loopDelays.push({start_ms:lastLoop-start,end_ms:now-start,excess_ms:now-lastLoop-50});lastLoop=now},50);
    const outcomes=await Promise.allSettled(pairs.map(async({a,b,id})=>{
      a.measure();let original=a.rows.get(id),nextStart=0;
      for(let v=1;v<=32;v++) {
        if(editIntervalMs&&nextStart>performance.now())await delay(nextStart-performance.now());
        // Follow the sample UI: use the listener's current object and wait while
        // a reset recalibrates it. A retained acknowledgement may now be stale.
        if(original?.contentStale) {
          report.stale_ack_replacements ||= [];
          if(report.stale_ack_replacements.length<32)report.stale_ack_replacements.push({round:v,old_epoch:original.contentEpoch,current_epoch:a.rows.get(id)?.contentEpoch,
            current_stale:a.rows.get(id)?.contentStale,manager_epoch:a.sdk.messageUpdateManager.epoch,recent_resets:a.feedTimes.slice(-8).filter(f=>f.reset_required).map(f=>({content_epoch:f.content_epoch,status:f.status}))});
        }
        const calibrationStart=performance.now();
        while((original=a.rows.get(id))?.contentStale||original?.contentVersion!==String(v-1)) {
          assert.ok(performance.now()-calibrationStart<30000, 'sender view did not recalibrate');await delay(5);
        }
        calibrationSamples.push(performance.now()-calibrationStart);
        const started=performance.now();nextStart=started+editIntervalMs;try { original=await a.sdk.chatManager.updateMessage(original,new a.wk.MessageText(`version-${v}`)); }
        catch(error) {
          const snapshot=m=>m&&({version:m.contentVersion,epoch:m.contentEpoch,stale:m.contentStale});
          report.edit_failure_context ||= {round:v,input:snapshot(original),current_view:snapshot(a.rows.get(id)),manager_epoch:a.sdk.messageUpdateManager.epoch,
            input_is_current_view:original===a.rows.get(id),recent_feed:a.feedTimes.slice(-8).map(f=>({...f,start:f.start-started,done:f.done-started}))};
          throw error;
        }
        assert.equal(original.contentVersion,String(v));
        await until(()=>b.visible.has(`${id}/${v}`),`visible version ${v}`);
        const visible=b.visible.get(`${id}/${v}`);visibleSamples.push(visible-started);
        if(visible-started>2000&&slowSamples.length<32) slowSamples.push({sender_node:a.node,receiver_node:b.node,version:v,visible_ms:visible-started,hint_ms:b.hintTimes.has(`${id}/${v}`)?b.hintTimes.get(`${id}/${v}`)-started:null,feed:b.feedTimes.filter(f=>f.done>=started&&f.start<=visible).map(f=>({start_ms:f.start-started,duration_ms:f.done-f.start,status:f.status,versions:f.versions}))});
        assert.equal(b.rows.get(id).content.text,`version-${v}`);
      }
    }));
    for(const outcome of outcomes) if(outcome.status==='rejected') throw outcome.reason;
    report.measured_seconds=(performance.now()-start)/1000;clearInterval(sampler);sampler=undefined;clearInterval(loopSampler);loopSampler=undefined;
    report.event_loop_delays=loopDelays;report.resource_sample_ms=quantiles(sampleDurations);
    report.resources_after=sample();report.peak_rss_kib=nodes.map(n=>({node:n.id,rss_kib:n.max_rss_kib}));
    report.bff_edit_roundtrip=quantiles(apiSamples);report.sdk_visible_latency=quantiles(visibleSamples);report.sender_calibration_wait=quantiles(calibrationSamples);
    for(const {a,b,id,conversation,unread,timestamp} of pairs) {
      assert.equal(conversation.lastMessage.contentVersion,'32');assert.equal(conversation.lastMessage.contentStale,false);assert.equal(conversation.unread,unread);assert.equal(conversation.timestamp,timestamp);
      assert.equal(b.rows.get(id).contentVersion,'32');assert.equal(b.rows.get(id).contentStale,false);assert.equal(a.errors.length,0,JSON.stringify(a.errors));assert.equal(b.errors.length,0,JSON.stringify(b.errors));
      assert.ok(b.events.some(e=>e.message_id===id&&e.version==='32'));
    }
    assert.equal(apiSamples.length,512);assert.equal(visibleSamples.length,512);
    report.slow_samples=slowSamples;assert.equal(report.sampling_error,undefined);
    report.complete=true;console.log(JSON.stringify({api:report.bff_edit_roundtrip,visible:report.sdk_visible_latency,seconds:report.measured_seconds}));
  } catch(e) {report.error=e.stack;for(const n of nodes)console.error(`node ${n.id} tail: ${n.log.slice(-4096)}`);throw e}
  finally {
    if(sampler)clearInterval(sampler);
    if(loopSampler)clearInterval(loopSampler);
    for(const c of clients)c.close();
    await Promise.all(bffs.map(b=>new Promise(r=>{b.closeAllConnections();b.close(r)})));
    for(const n of nodes)if(n.process&&n.process.exitCode===null)n.process.kill('SIGTERM');
    await Promise.all(nodes.filter(n=>n.process).map(async n=>{await Promise.race([n.exit,delay(15000)]);if(n.process.exitCode===null&&n.process.signalCode===null){n.process.kill('SIGKILL');await n.exit}}));
    await fs.mkdir(path.dirname(output),{recursive:true});await fs.writeFile(output,JSON.stringify(report,null,2)+'\n');
    await fs.rm(dir,{recursive:true,force:true});
  }
}
main().catch(e=>{console.error(e);process.exitCode=1});
