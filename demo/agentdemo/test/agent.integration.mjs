// Black-box acceptance with real EasySDK and a 256-hash-slot single-node cluster.
// Failure cases are declared before implementing the Agent runtime.
import assert from 'node:assert/strict';
import { mkdtemp, writeFile, mkdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createServer as netServer } from 'node:net';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { WKIM, WKIMEvent } from 'easyjssdk';
const cwd = fileURLToPath(new URL('../', import.meta.url));
const delay = ms => new Promise(r => setTimeout(r, ms));
async function until(fn, label, ms = 30000) { const deadline = Date.now() + ms; while (Date.now() < deadline) { if (await fn()) return; await delay(40); } throw Error('Timed out: ' + label); }
async function port() { const server = netServer(); server.listen(0, '127.0.0.1'); await once(server, 'listening'); const value = server.address().port; await new Promise(r => server.close(r)); return value; }
const evidence = process.env.WK_AGENT_REPORT_DIR || await mkdtemp(join(tmpdir(), 'wk-agent-demo-'));
await mkdir(evidence, {recursive:true});
const checks = [], children = [], clients = [], logs = {};
const check = (name, result) => { assert(result, name); checks.push(name); };
let fixture;
try {
  assert(process.env.WK_DEMO_SERVER_BIN, 'Set WK_DEMO_SERVER_BIN to a freshly built WuKongIM binary');
  const [apiPort, raft, ws, demo] = await Promise.all(Array.from({length:4}, port));
  const api = `http://127.0.0.1:${apiPort}`, base = `http://127.0.0.1:${demo}`;
  await writeFile(join(evidence,'wukongim.toml'), `[node]\nid = 1\ndata_dir = "${evidence}/data"\n[cluster]\nid = "agent-demo-validation"\nlisten_addr = "127.0.0.1:${raft}"\nnodes = [{id = 1, addr = "127.0.0.1:${raft}"}]\ninitial_slot_count = 8\nhash_slot_count = 256\nslot_replica_n = 1\n[api]\nlisten_addr = "127.0.0.1:${apiPort}"\nexternal_ws_addr = "ws://127.0.0.1:${ws}"\n[manager]\nlisten_addr = "127.0.0.1:0"\n[gateway]\ntoken_auth_on = true\nlisteners = [{name = "ws", network = "websocket", address = "127.0.0.1:${ws}", transport = "gnet", protocol = "wsmux"}]\n[plugin]\nenable = false\n[log]\nlevel = "warn"\ndir = "${evidence}/logs"\n`);
  const cleanEnv = Object.fromEntries(Object.entries(process.env).filter(([k]) => !k.startsWith('WK_')));
  function start(name, command, args, env) { const child = spawn(command,args,{cwd,env,stdio:['ignore','pipe','pipe']}); logs[name]=''; for(const stream of [child.stdout,child.stderr])stream.on('data',b=>{logs[name]=(logs[name]+b).slice(-262144);}); children.push(child); }
  start('cluster',process.env.WK_DEMO_SERVER_BIN,['-config',join(evidence,'wukongim.toml')],cleanEnv);
  await until(async()=>{try{return (await fetch(api+'/readyz')).ok;}catch{return false;}},'cluster readiness');
  start('demo',process.execPath,['server.mjs'],{...cleanEnv,WK_DEMO_API_URL:api,WK_DEMO_PORT:String(demo)});
  await until(async()=>{try{return (await fetch(base+'/agentdemo/api/health')).ok;}catch{return false;}},'Agent readiness',10000);
  check('standalone_page',(await fetch(base+'/agentdemo/')).ok);
  const embedded = await fetch(api+'/agentdemo/'); check('embedded_page',embedded.ok);
  for(const [,path] of (await embedded.text()).matchAll(/(?:src|href)="(\/agentdemo\/assets\/[^\"]+)"/g))check('embedded_asset',(await fetch(api+path)).ok);
  check('embedded_UI_is_read_only',(await fetch(api+'/agentdemo/api/session',{method:'POST',headers:{'content-type':'application/json'},body:'{}'})).status===405);
  check('cross_origin_rejected',(await fetch(base+'/agentdemo/api/session',{method:'POST',headers:{'content-type':'application/json',origin:'https://untrusted.example'},body:'{}'})).status===403);
  check('malformed_asset_rejected',(await fetch(base+'/agentdemo/assets/%XX.js')).status===400);
  const setup = await fetch(base+'/agentdemo/api/session',{method:'POST',headers:{'content-type':'application/json'},body:'{}'}).then(r=>r.json());
  const auth = {authorization:`Bearer ${setup.token}`,'content-type':'application/json'};
  async function request(route,body,status=200) { const response=await fetch(base+'/agentdemo/api'+route,{method:body===undefined?'GET':'POST',headers:auth,body:body===undefined?undefined:JSON.stringify(body)}); const result=await response.json();assert.equal(response.status,status,route+' '+JSON.stringify(result));return result; }
  const state = ()=>request('/state');
  let conversation=setup.conversations[0];
  const messages=[],events=[];
  const receiver=WKIM.init(setup.wsUrl,{uid:setup.user.uid,token:setup.user.token,deviceFlag:1},{singleton:false});clients.push(receiver);
  receiver.on(WKIMEvent.Message,m=>messages.push(m));receiver.on(WKIMEvent.CustomEvent,e=>events.push(e));await receiver.connect();
  const send=async(content,key=crypto.randomUUID())=>(await request('/send',{conversationId:conversation.id,content,requestId:key})).runs.at(-1);
  const run=id=>state().then(s=>s.runs.find(r=>r.id===id));
  const control=(route,id,extra={},status=200)=>request(route,{runId:id,requestId:crypto.randomUUID(),...extra},status);
  let current=await send('查询资料：WuKongIM 流式消息 SDK 接入');
  await until(async()=> (await run(current.id)).status==='completed','read-only Agent task');
  check('question_arrives_over_real_SDK',messages.some(m=>m.payload?.content?.includes('查询资料')));
  check('tool_results_arrive_over_real_SDK',messages.some(m=>m.payload?.agent?.steps?.some(s=>s.name==='search_knowledge'&&s.status==='completed')));
  check('final_summary_streams_over_real_SDK',events.some(e=>e.type==='stream.delta')&&events.some(e=>e.type==='stream.finish'));
  current=await send('检索流式消息资料并创建接入待办');
  await until(async()=> (await run(current.id)).status==='awaiting_approval','write tool approval');
  let waiting=await run(current.id), call=waiting.steps.find(s=>s.status==='awaiting_approval');
  check('write_tool_has_no_effect_before_confirmation',(await state()).todos.length===0);
  await request('/send',{conversationId:conversation.id,content:'任务还没有结束',requestId:crypto.randomUUID()},409);
  check('same_conversation_rejects_overlapping_run',true);
  await control('/approve',current.id,{callId:call.id,allow:false});
  await until(async()=> (await run(current.id)).status==='completed','rejected proposal summary');
  check('rejected_proposal_does_not_create_todo',(await state()).todos.length===0);
  current=await send('检索资料并创建流式接入待办');
  await until(async()=> (await run(current.id)).status==='awaiting_approval','second proposal');
  call=(await run(current.id)).steps.find(s=>s.status==='awaiting_approval');
  const approvalKey=crypto.randomUUID(),approval={runId:current.id,callId:call.id,allow:true,requestId:approvalKey};
  await request('/approve',approval);await request('/approve',approval);
  await until(async()=> (await run(current.id)).status==='completed','approved task complete');
  check('approval_is_idempotent_one_todo',(await state()).todos.length===1);
  await request('/approve',{...approval,allow:false},409);check('approval_payload_conflict_rejected',true);
  await request('/config',{mode:'simulation',interval:120});
  current=await send('查阅流式消息资料并解释接入流程');
  await until(()=>events.some(e=>e.type==='stream.delta'&&e.data.client_msg_no===current.replyKey),'first pause delta');
  await control('/pause',current.id);
  const beforePause=events.filter(e=>e.type==='stream.delta'&&e.data.client_msg_no===current.replyKey).length;
  await delay(300);
  check('pause_fences_new_stream_deltas',events.filter(e=>e.type==='stream.delta'&&e.data.client_msg_no===current.replyKey).length===beforePause);
  await control('/resume',current.id);await until(async()=> (await run(current.id)).status==='completed','resumed task complete');
  check('resume_completes_same_run',true);
  current=await send('创建一条待办供取消演示');await until(async()=> (await run(current.id)).status==='awaiting_approval','cancel proposal');
  call=(await run(current.id)).steps.find(s=>s.status==='awaiting_approval');
  await control('/cancel',current.id);await control('/approve',current.id,{callId:call.id,allow:true},409);
  check('cancelled_proposal_cannot_write',(await state()).todos.length===1&&(await run(current.id)).status==='cancelled');
  check('cancel_has_terminal_snapshot',events.some(e=>e.type==='stream.cancel'&&e.data.client_msg_no===current.replyKey));
  await request('/config',{mode:'simulation',failTool:true});current=await send('查询资料');
  await until(async()=> (await run(current.id)).status==='failed','tool failure');
  check('tool_failure_is_visible',(await run(current.id)).steps.some(s=>s.status==='failed'));
  check('tool_failure_saves_stream_error',events.some(e=>e.type==='stream.error'&&e.data.client_msg_no===current.replyKey));
  await request('/config',{mode:'simulation',failTool:false});
  const key=crypto.randomUUID();current=await send('检索 SDK 资料',key);await send('检索 SDK 资料',key);
  await until(async()=> (await run(current.id)).status==='completed','idempotent send');
  check('send_retry_does_not_duplicate_question',messages.filter(m=>m.clientMsgNo===key).length===1);
  receiver.disconnect();
  current=await send('离线时查询流式资料');await until(async()=> (await run(current.id)).status==='completed','offline task');await receiver.connect();
  const history=await request('/history',{conversationId:conversation.id});
  check('offline_history_restores_tool_trace',history.messages.some(m=>JSON.parse(Buffer.from(m.payload,'base64')).agent?.id===current.id));
  check('offline_history_restores_final_snapshot',history.messages.some(m=>m.client_msg_no===current.replyKey&&m.event_meta?.completed));
  const oldId=conversation.id;conversation=(await request('/new',{requestId:crypto.randomUUID()})).conversations.at(-1);
  check('new_conversation_isolated',conversation.id!==oldId&&!((await state()).runs.some(r=>r.conversationId===conversation.id)));
  let modelCalls=0,aborts=0;const contexts=[];
  fixture=createServer(async(req,res)=>{
    if(req.url==='/v1/models'){res.setHeader('content-type','application/json');res.end(JSON.stringify({data:[{id:'fixture-chat'}]}));return;}
    let raw='';for await(const b of req)raw+=b;const input=JSON.parse(raw);contexts.push(input.messages);modelCalls++;
    res.writeHead(200,{'content-type':'text/event-stream'});let done=false;res.on('close',()=>{if(!done)aborts++;});
    const user=input.messages.filter(m=>m.role==='user').at(-1).content;
    const chunk=delta=>'data: '+JSON.stringify({choices:[{index:0,delta}]})+'\r\n\r\n';
    if(user.includes('断流')){res.end(chunk({content:'部分内容'}));return;}
    if(user.includes('额外参数')){res.write(chunk({tool_calls:[{index:0,id:'bad-args',type:'function',function:{name:'create_todo',arguments:'{"title":"无效待办","extra":"ignored?"}'}}]}));done=true;res.end('data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}\n\ndata: [DONE]\n\n');return;}
    if(user.includes('未知工具')){res.write(chunk({tool_calls:[{index:0,id:'unknown-call',type:'function',function:{name:'run_shell',arguments:'{}'}}]}));done=true;res.end('data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}\n\ndata: [DONE]\n\n');return;}
    if(user.includes('中止')){res.write(chunk({content:'部分模型回复：'}));await delay(1500);if(res.destroyed)return;}
    if(user.includes('模型待办')&&input.messages.at(-1).role!=='tool'){res.write(chunk({tool_calls:[{index:0,id:'fixture-todo',type:'function',function:{name:'create_todo',arguments:'{"title":"模型提议的接入待办"}'}}]}));done=true;res.end('data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}\n\ndata: [DONE]\n\n');return;}
    if(input.messages.at(-1).role!=='tool'){
      res.write(chunk({tool_calls:[{index:0,id:'fixture-search',type:'function',function:{name:'search_knowledge',arguments:'{"query":"'}}]}));
      await delay(80);res.write(chunk({tool_calls:[{index:0,function:{arguments:'流式消息"}'}}]}));
      done=true;res.end('data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}\n\ndata: [DONE]\n\n');
    }else{res.write(chunk({content:'已根据工具返回的资料'}));await delay(120);if(res.destroyed)return;res.write(chunk({content:'整理接入步骤。'}));done=true;res.end('data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n');}
  });fixture.listen(0,'127.0.0.1');await once(fixture,'listening');
  await request('/config',{mode:'model',url:`http://127.0.0.1:${fixture.address().port}/v1`,apiKey:'agent-fixture-key',model:''});
  current=await send('请通过模型工具检索资料');await until(async()=> (await run(current.id)).status==='completed','real model tool loop');
  check('real_model_tool_arguments_reassembled',(await run(current.id)).steps.some(s=>s.args.query==='流式消息'&&s.status==='completed'));
  check('real_model_receives_tool_result',contexts.some(ms=>ms.some(m=>m.role==='tool'&&m.tool_call_id==='fixture-search')));
  check('real_model_auto_discovery',(await state()).config.model==='fixture-chat');
  current=await send('创建模型待办');await until(async()=> (await run(current.id)).status==='awaiting_approval','model proposal awaits user');
  check('real_model_cannot_bypass_approval',(await state()).todos.length===1);
  call=(await run(current.id)).steps.find(s=>s.status==='awaiting_approval');await control('/pause',current.id);await control('/approve',current.id,{callId:call.id,allow:true});await delay(150);
  check('approved_tool_still_waits_while_paused',(await state()).todos.length===1);await control('/resume',current.id);await until(async()=> (await run(current.id)).status==='completed','model todo complete');
  check('real_model_creates_exact_confirmed_title',(await state()).todos.at(-1).title==='模型提议的接入待办');
  check('real_model_receives_created_tool_result',contexts.some(ms=>ms.some(m=>m.role==='tool'&&m.tool_call_id==='fixture-todo'&&JSON.parse(m.content).status==='created')));
  current=await send('额外参数测试');await until(async()=> (await run(current.id)).status==='failed','extra tool argument rejection');check('invalid_tool_arguments_cannot_write',(await state()).todos.length===2);
  current=await send('未知工具测试');await until(async()=> (await run(current.id)).status==='failed','unknown tool rejection');
  check('unknown_model_tool_never_executes',(await run(current.id)).steps.some(s=>s.name==='run_shell'&&s.status==='failed'));
  current=await send('断流测试');await until(async()=> (await run(current.id)).status==='failed','truncated SSE failure');check('truncated_model_is_failure',true);
  current=await send('中止模型请求');await until(()=>events.some(e=>e.type==='stream.delta'&&e.data.client_msg_no===current.replyKey),'model first delta');await control('/cancel',current.id);await until(()=>aborts>0,'upstream abort');check('cancel_aborts_upstream_model',true);
  check('key_absent_from_state_and_logs',!JSON.stringify(await state()).includes('agent-fixture-key')&&!JSON.stringify(logs).includes('agent-fixture-key'));
  await control('/cancel','unknown-run',{},404);check('foreign_run_rejected',true);
  await writeFile(join(evidence,'history.json'),JSON.stringify(history,null,2));
  await writeFile(join(evidence,'report.json'),JSON.stringify({passed:true,checks,modelCalls,aborts,sdk:'easyjssdk@2.0.5',hashSlots:256,topology:'single-node cluster'},null,2));
  console.log(JSON.stringify({passed:true,checks:checks.length,evidence}));
}catch(error){await writeFile(join(evidence,'failure.json'),JSON.stringify({passed:false,checks,error:error.message},null,2));console.error('Evidence:',evidence);throw error;}
finally{for(const client of clients)client.destroy();fixture?.closeAllConnections();fixture?.close();for(const child of children.reverse()){const exited=child.exitCode===null?once(child,'exit'):Promise.resolve();child.kill('SIGTERM');await Promise.race([exited,delay(4000)]);if(child.exitCode===null){child.kill('SIGKILL');await exited;}}for(const [name,value]of Object.entries(logs))await writeFile(join(evidence,name+'.log'),value);}
