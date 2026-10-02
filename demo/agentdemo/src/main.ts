import { WKIM, WKIMEvent } from 'easyjssdk';
import './style.css';
import { demoHomeURL } from '../../shared/home';
import '../../shared/home.css';
type Conversation={id:string;channelId:string;title:string;activeRunId?:string;unconfirmed?:boolean};
type Step={id:string;name:string;args:Record<string,string>;status:string;decided?:boolean;result?:any;error?:string};
type Run={id:string;conversationId:string;replyKey:string;question:string;status:string;paused:boolean;revision:number;error:string;steps:Step[];createdAt:number};
type Todo={id:string;title:string};
type Workspace={id:string;token?:string;wsUrl:string;user:{uid:string;token:string};conversations:Conversation[];runs:Run[];todos:Todo[];config:{mode:string;url:string;model:string;keyConfigured:boolean;interval:number;failTool:boolean};logs:{time:string;kind:string}[]};
type Row={key:string;seq:string;author:string;text:string;stream:boolean;terminal:boolean;status:string;seen:Set<string>};
const $=<T extends HTMLElement=HTMLElement>(selector:string)=>document.querySelector<T>(selector)!;
const labels:Record<string,string>={pending:'准备执行',running:'执行中',paused:'已暂停',awaiting_approval:'等待确认',completed:'已完成',cancelled:'已取消',failed:'执行失败',rejected:'已拒绝'};
const toolLabels:Record<string,string>={search_knowledge:'检索资料',list_todos:'读取待办',create_todo:'创建待办'};
const done=(r:Run)=>['completed','cancelled','failed'].includes(r.status);
const cache=new Map<string,Map<string,Row>>(),drafts=new Map<string,{text:string;retry?:string}>();
const encoder=new TextEncoder(),decoder=new TextDecoder(),storage='wk-agentdemo-session';
let work:Workspace|undefined,capability='',conversationId='',online=false,busy=false,starting=false,restoring=false,generation=0;
let backend=document.querySelector('meta[name="wk-agent-backend"]')?location.origin:'http://127.0.0.1:5178';
let client:WKIM|undefined;const buffered:(()=>void)[]=[],logs:string[]=[];
const arrow='<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 12 6-6 6 6M12 6v13"/></svg>';
const sparkle='<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m12 3 2.8 6.2L21 12l-6.2 2.8L12 21l-2.8-6.2L3 12l6.2-2.8Z"/></svg>';
$('#app').innerHTML=`<div class="agent-app"><aside class="sidebar"><div class="brand"><span>W</span>WuKongIM</div><div class="lab-title"><span class="lab-icon">${sparkle}</span><div><h1>Agent Lab</h1><p>让任务真正向前一步</p></div></div><button id="new-conversation" class="new-button" disabled><span>＋</span> 新对话</button><div class="sidebar-label">最近的对话</div><nav id="conversations" aria-label="会话列表"><p class="empty-nav">从你的第一个任务开始</p></nav><div class="sidebar-bottom"><div><span>◈</span> 内置资料库 <b>3</b></div><div><span>⌘</span> 可用工具 <b>3</b></div><p>资料检索 · 待办管理</p></div></aside>
<main class="main"><header class="topbar"><div><span class="workspace-name">小悟任务助手</span><span class="version">AGENT DEMO</span></div><div class="header-actions"><a class="demo-home-link" data-demo-home><span aria-hidden="true">←</span>返回首页</a><span id="connection">未连接</span><button id="disconnect" disabled>断开连接</button><button id="settings-button">演示设置 <span aria-hidden="true">⚙</span></button></div></header><div id="error" role="alert"></div><div id="start-card"><div><strong>把想做的事交给小悟</strong><p>看它检索资料、调用工具，再把结果带回来。</p></div><button id="start" class="primary">开始演示 ↗</button></div><div class="mobile-controls"><select id="conversation-select" aria-label="切换对话" disabled><option>新的任务</option></select><button id="mobile-new" aria-label="新对话" disabled>＋</button><button data-view="chat" class="active">聊天</button><button data-view="inspector">执行面板</button></div>
<div class="workspace" data-view="chat"><section class="chat-panel" aria-label="Agent 聊天"><div class="chat-heading"><span class="agent-avatar">${sparkle}</span><div><h2 id="chat-title">想完成什么？</h2><p>从一个目标开始，让工具来帮忙。</p></div><span class="available"><i></i>任务助手</span></div><div id="messages" role="log" aria-label="Agent 聊天记录"><div class="welcome"><span>${sparkle}</span><h2>一个目标，一步步完成。</h2><p>我可以查阅接入资料，也可以帮你创建待办。<br>每一次工具调用，你都看得见。</p><div class="suggestions"><button data-question="帮我梳理 WuKongIM 流式消息的接入步骤">◈<strong>梳理接入步骤</strong><small>检索资料，生成清晰的总结</small></button><button data-question="检索 Agent 流式接入资料，并创建一条接入待办">✓<strong>整理并创建待办</strong><small>先给出建议，再由你确认</small></button></div></div></div><div class="composer-wrap"><div class="quick-actions"><button data-question="帮我梳理 WuKongIM 流式消息的接入步骤">◈ 查阅资料</button><button data-question="检索 Agent 流式接入资料，并创建一条接入待办">＋ 创建接入待办</button></div><div id="run-controls"><span id="run-status">准备好帮你处理下一件事</span><div><button id="pause" hidden>暂停任务</button><button id="cancel" hidden>取消任务</button></div></div><form id="composer"><textarea id="question" aria-label="任务消息" rows="2" maxlength="1000" placeholder="描述你想完成的任务…" disabled></textarea><div class="composer-bottom"><span>Enter 发送 · Shift + Enter 换行</span><button id="send" class="primary" aria-label="发送任务" disabled>${arrow}</button></div></form><p class="composer-note">新增待办前会征求你的确认</p></div></section>
<aside class="inspector" aria-label="执行面板"><div class="inspector-heading"><span>执行面板</span><span>◌</span></div><div class="run-overview"><span class="eyebrow">CURRENT TASK</span><h3 id="task-title">等待新的任务</h3><span id="task-status" class="status-pill">准备就绪</span></div><div class="section-title">工具与进度 <span id="step-count">0</span></div><ol id="timeline"><li class="empty-timeline">任务开始后，执行步骤会出现在这里。</li></ol><div class="todo-section"><div class="section-title">你的待办 <span id="todo-count">0</span></div><ul id="todos"><li class="empty-todo">确认创建后，待办会保存在这里。</li></ul></div><div class="inspector-footer"><i></i>进度与回复通过 WuKongIM 实时送达</div></aside></div></main></div>
<dialog id="settings"><form id="config-form"><header><div><span class="eyebrow">AGENT SETTINGS</span><h2>连接与模型</h2></div><button type="button" id="settings-close" aria-label="关闭演示设置">×</button></header><label>Agent 业务服务 URL<input id="backend-url" aria-label="Agent 业务服务 URL" type="url"></label><label>回复来源<select id="mode" aria-label="回复来源"><option value="simulation">模拟 Agent</option><option value="model">真实模型</option></select></label><div id="model-options" hidden><label>模型 URL<input id="model-url" aria-label="模型 URL" type="url" placeholder="https://provider.example/v1"></label><label>API Key<input id="model-key" aria-label="API Key" type="password" autocomplete="off" placeholder="仅保留在演示业务进程内存"></label><label>模型名称（可选）<input id="model-name" aria-label="模型名称" placeholder="留空自动选择"></label><p class="hint">模型需支持 Chat Completions 的 tools 工具调用。</p></div><div id="simulation-options"><label>输出间隔（毫秒）<input id="interval" aria-label="输出间隔（毫秒）" type="number" min="40" max="1000" value="45"></label><label class="check-label"><input id="fail-tool" type="checkbox">模拟工具失败</label></div><button id="save-config" class="primary" disabled>保存设置</button><p id="config-result" role="status"></p><details><summary>请求与事件日志</summary><button type="button" id="resync" disabled>重新同步</button><pre id="logs"></pre></details></form></dialog>`;
const selected=()=>work?.conversations.find(c=>c.id===conversationId);
const current=()=>work?.runs.filter(r=>r.conversationId===conversationId).sort((a,b)=>b.createdAt-a.createdAt)[0];
function log(kind:string){logs.push(`${new Date().toLocaleTimeString('zh-CN')} ${kind}`);if(logs.length>120)logs.shift();$('#logs').textContent=[...(work?.logs||[]).slice(-30).map(l=>`${l.time.slice(11,19)} 后端 ${l.kind}`),...logs].join('\n');}
function error(value:unknown){$('#error').textContent=value instanceof Error?value.message:'操作未完成，请重试。';}
function save(){if(work)sessionStorage.setItem(storage,JSON.stringify({backend,capability,conversationId}));}
function reset(){generation++;client?.destroy();client=undefined;work=undefined;online=false;capability=conversationId='';buffered.length=0;cache.clear();drafts.clear();sessionStorage.removeItem(storage);for(const id of ['messages','conversations','conversation-select'])$(`#${id}`).replaceChildren();$<HTMLTextAreaElement>('#question').value='';$<HTMLInputElement>('#model-key').value='';render();}
$<HTMLAnchorElement>('[data-demo-home]').href = demoHomeURL();
async function api(path:string,body?:unknown):Promise<any>{
  const headers:Record<string,string>={'content-type':'application/json'};if(capability)headers.Authorization=`Bearer ${capability}`;
  let response:Response;try{response=await fetch(backend+'/agentdemo/api'+path,{method:body===undefined?'GET':'POST',headers,body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(30000)});}catch{throw Error('Agent 业务服务未连接，请运行 agentdemo 的 npm start 并检查服务 URL。');}
  let value:any;try{value=await response.json();}catch{throw Error('请先运行 Agent 业务服务。');}
  if(!response.ok){if(response.status===401)reset();throw Object.assign(Error(value.error||'操作未完成。'),{status:response.status});}if(path!=='/config')log('HTTP '+path);return value;
}
function mergeRun(next:Run){if(!work)return;const old=work.runs.find(r=>r.id===next.id);if(old&&old.revision>next.revision)return;if(old)Object.assign(old,next);else work.runs.push(next);for(const step of next.steps)if(step.result?.todo&&!work.todos.some(t=>t.id===step.result.todo.id))work.todos.push(step.result.todo);}
function update(next:Workspace){
  if(work?.id===next.id){next.runs=next.runs.map(r=>{const old=work!.runs.find(v=>v.id===r.id);return old&&old.revision>r.revision?old:r;});next.todos=[...next.todos,...work.todos.filter(t=>!next.todos.some(v=>v.id===t.id))];}
  work=next;conversationId||=next.conversations[0].id;save();render();
}
function rows(c:Conversation){let values=cache.get(c.id);if(!values){values=new Map();cache.set(c.id,values);}return values;}
function row(c:Conversation,key:string){const values=rows(c);let item=values.get(key);if(!item){item={key,seq:'0',author:'',text:'',stream:false,terminal:false,status:'',seen:new Set()};values.set(key,item);}if(values.size>100){const oldest=[...values.values()].sort((a,b)=>Number(BigInt(a.seq)-BigInt(b.seq)))[0];if(oldest.key!==key)values.delete(oldest.key);}return item;}
const payload=(value:any)=>typeof value==='string'?JSON.parse(decoder.decode(Uint8Array.from(atob(value),c=>c.charCodeAt(0)))):value||{};
function ordinary(message:any){
  if(!work)return;const c=work.conversations.find(c=>c.channelId===(message.channelId||message.channel_id));if(!c)return;
  const body=payload(message.payload);if(body.agent){mergeRun(body.agent);return;}
  const key=message.clientMsgNo||message.client_msg_no;if(!key)return;const item=row(c,key);item.seq=String(message.messageSeq||message.message_seq||item.seq);item.author=message.fromUid||message.from_uid;
  item.stream=typeof message.setting==='number'?!!(message.setting&2):!!message.setting?.stream;if(!item.stream)item.text=body.content||'';
  const projection=message.event_meta?.events?.find((e:any)=>e.event_key==='main');
  if(projection&&(!item.terminal||['closed','cancelled','error'].includes(projection.status))){item.stream=true;item.text=projection.snapshot?.text||item.text;item.terminal=!!message.event_meta.completed||['closed','cancelled','error'].includes(projection.status);item.status={closed:'完成',cancelled:'已取消',error:'失败'}[projection.status as 'closed']||'生成中';}
  if(item.stream&&!item.status)item.status='生成中';
}
function event(value:any){
  if(!work||!value.type?.startsWith('stream.'))return;const data=value.data,c=work.conversations.find(c=>c.channelId===data?.channel_id);
  if(!c||data.channel_type!==2||!data.client_msg_no||data.event_key!=='main'&&value.type!=='stream.finish')return;
  const item=row(c,data.client_msg_no);if(item.seen.has(value.id))return;if(item.seen.size>=2048)item.seen.delete(item.seen.values().next().value!);item.seen.add(value.id);item.stream=true;item.author=data.from_uid;item.seq=String(data.message_seq||item.seq);
  const p=data.payload||{};if(value.type==='stream.delta'&&!item.terminal){const text=encoder.encode(item.text),delta=encoder.encode(p.delta||''),offset=data.text_offset??text.length;if(offset>text.length)item.status='等待最终快照';else if(offset+delta.length>text.length)item.text+=decoder.decode(delta.slice(text.length-offset));}
  if(['stream.cancel','stream.error','stream.finish'].includes(value.type)){if(typeof p.snapshot?.text==='string')item.text=p.snapshot.text;item.status=value.type==='stream.cancel'?'已取消':value.type==='stream.error'?'失败':['已取消','失败'].includes(item.status)?item.status:'完成';item.terminal=true;}
  log('SDK '+value.type);render();
}
function render(){
  const c=selected(),run=current(),running=!!run&&!done(run);
  $('#start-card').hidden=!!work;$('#start').toggleAttribute('disabled',starting);$('#start').textContent=starting?'正在连接…':'开始演示 ↗';
  $('#connection').textContent=online?'在线':work?'离线':'未连接';$('#connection').classList.toggle('online',online);$('#disconnect').textContent=online?'断开连接':'重新连接';$('#disconnect').toggleAttribute('disabled',!work||busy);
  for(const id of ['question','send'])$(`#${id}`).toggleAttribute('disabled',!work||!online||restoring||busy||running||c?.unconfirmed);
  for(const id of ['new-conversation','mobile-new'])$(`#${id}`).toggleAttribute('disabled',!work||busy);$('#conversation-select').toggleAttribute('disabled',!work||busy);
  $('#pause').hidden=$('#cancel').hidden=!running;$('#pause').textContent=run?.paused?'继续任务':'暂停任务';for(const id of ['pause','cancel'])$(`#${id}`).toggleAttribute('disabled',busy||!online);
  $('#run-status').textContent=run?.error||(!online&&work?'连接已断开，重连后恢复任务进度。':running?labels[run!.status]:'准备好帮你处理下一件事');$('#chat-title').textContent=c?.title||'想完成什么？';
  $('#task-title').textContent=run?.question||'等待新的任务';$('#task-status').textContent=run?labels[run.status]:'准备就绪';$('#task-status').dataset.status=run?.status||'';
  $('#step-count').textContent=String(run?.steps.length||0);$('#todo-count').textContent=String(work?.todos.length||0);
  const timeline=$('#timeline');timeline.replaceChildren();for(const step of run?.steps||[]){const li=document.createElement('li');li.dataset.status=step.status;li.innerHTML='<span class="step-dot"></span><div><strong></strong><small></small></div>';li.querySelector('strong')!.textContent=toolLabels[step.name]||step.name;li.querySelector('small')!.textContent=labels[step.status]||step.status;timeline.append(li);}if(!timeline.childNodes.length){const li=document.createElement('li');li.className='empty-timeline';li.textContent='任务开始后，执行步骤会出现在这里。';timeline.append(li);}
  const todos=$('#todos');todos.replaceChildren();for(const todo of work?.todos||[]){const li=document.createElement('li');li.textContent='□ '+todo.title;li.dataset.todoId=todo.id;todos.append(li);}if(!todos.childNodes.length){const li=document.createElement('li');li.className='empty-todo';li.textContent='确认创建后，待办会保存在这里。';todos.append(li);}
  $('#backend-url').toggleAttribute('disabled',!!work||starting);$('#save-config').toggleAttribute('disabled',!work||busy||work.runs.some(r=>!done(r)));$('#resync').toggleAttribute('disabled',!work||busy);
  if(work){renderConversations();renderChat();}document.querySelectorAll<HTMLButtonElement>('[data-question]').forEach(b=>{b.disabled=!work||busy||running||!online;});
}
function renderConversations(){
  const nav=$('#conversations');nav.replaceChildren();for(const c of [...work!.conversations].reverse()){const b=document.createElement('button');b.dataset.conversationId=c.id;b.textContent='◌ '+c.title;b.classList.toggle('active',c.id===conversationId);b.onclick=()=>void action(async()=>{if(busy)return;switchConversation(c.id);await recover();});nav.append(b);}
  const select=$<HTMLSelectElement>('#conversation-select');select.replaceChildren(...work!.conversations.map(c=>new Option(c.title,c.id)));select.value=conversationId;
}
function toolCard(run:Run,step:Step){
  const card=document.createElement('div');card.className='tool-card';card.dataset.callId=step.id;card.dataset.status=step.status;
  card.innerHTML='<div class="tool-header"><span class="tool-symbol">⌘</span><strong></strong><span class="tool-status"></span></div><p class="tool-input"></p><div class="tool-output"></div>';
  card.querySelector('strong')!.textContent=toolLabels[step.name]||step.name;card.querySelector('.tool-status')!.textContent=labels[step.status]||step.status;
  card.querySelector('.tool-input')!.textContent=step.args.title||step.args.query||'当前演示中的待办';
  const output=card.querySelector('.tool-output')!;
  if(step.result?.documents){for(const doc of step.result.documents){const item=document.createElement('details');item.dataset.documentId=doc.id;const summary=document.createElement('summary');summary.textContent='◈ '+doc.title;const p=document.createElement('p');p.textContent=doc.content;item.append(summary,p);output.append(item);}if(!step.result.documents.length)output.textContent='没有找到匹配的内置资料。';}
  else if(step.result?.todo)output.textContent='✓ 待办已创建';else if(step.result?.todos)output.textContent=step.result.todos.map((t:Todo)=>t.title).join('\n')||'暂无待办';else if(step.status==='rejected')output.textContent='已按你的选择跳过，未创建待办。';else if(step.error)output.textContent=step.error;
  if(step.status==='awaiting_approval'){const actions=document.createElement('div');actions.className='approval-actions';const note=document.createElement('p');note.textContent='确认后，将在本演示中新增这条待办。';actions.append(note);for(const [allow,title]of [[true,'确认创建'],[false,'拒绝']] as const){const b=document.createElement('button');b.textContent=title;b.className=allow?'primary':'secondary';b.disabled=busy||!!step.decided||!online;b.onclick=()=>void control('/approve',run,{callId:step.id,allow});actions.append(b);}card.append(actions);}
  return card;
}
function renderChat(){
  const c=selected();if(!c)return;const box=$('#messages'),atEnd=box.scrollHeight-box.scrollTop-box.clientHeight<100,position=box.scrollTop;
  const expanded=new Set([...box.querySelectorAll<HTMLDetailsElement>('details[open]')].map(e=>e.closest<HTMLElement>('[data-call-id]')!.dataset.callId+':'+e.dataset.documentId));
  const values=[...rows(c).values()].sort((a,b)=>Number(BigInt(a.seq)-BigInt(b.seq)));box.replaceChildren();
  const renderedRuns=new Set<string>();
  for(const item of values){
    if(item.stream){const run=work!.runs.find(r=>r.replyKey===item.key);if(run){const steps=document.createElement('div');steps.className='tool-group';steps.dataset.runId=run.id;for(const step of run.steps)steps.append(toolCard(run,step));box.append(steps);renderedRuns.add(run.id);}}
    const el=document.createElement('div');el.className='message'+(item.author===work!.user.uid?' own':'')+(item.stream?' stream-message':'');el.dataset.messageKey=item.key;el.dataset.status=item.status;
    el.innerHTML='<span class="message-author"></span><div class="bubble"></div><small></small>';el.querySelector('.message-author')!.textContent=item.author===work!.user.uid?'你':'小悟 Agent';el.querySelector('.bubble')!.textContent=item.text||(!item.terminal&&item.stream?'正在处理任务…':item.status==='已取消'?'任务已取消。':item.status==='失败'?'任务未能完成。':'');el.querySelector('small')!.textContent=item.status;box.append(el);
  }
  for(const run of work!.runs.filter(r=>r.conversationId===c.id&&!renderedRuns.has(r.id)&&r.steps.length)){const steps=document.createElement('div');steps.className='tool-group';steps.dataset.runId=run.id;for(const step of run.steps)steps.append(toolCard(run,step));box.append(steps);}
  for(const details of box.querySelectorAll<HTMLDetailsElement>('details'))details.open=expanded.has(details.closest<HTMLElement>('[data-call-id]')!.dataset.callId+':'+details.dataset.documentId);
  if(atEnd)box.scrollTop=box.scrollHeight;else box.scrollTop=position;
}
function switchConversation(id:string){const input=$<HTMLTextAreaElement>('#question');drafts.set(conversationId,{text:input.value,retry:input.dataset.retryId});conversationId=id;const draft=drafts.get(id);input.value=draft?.text||'';if(draft?.retry)input.dataset.retryId=draft.retry;else delete input.dataset.retryId;save();render();}
async function recover(){const c=selected();if(!c)return;restoring=true;render();try{const history=await api('/history',{conversationId:c.id});for(const m of history.messages||[])ordinary(m);for(const apply of buffered.splice(0))apply();}finally{restoring=false;render();}}
async function connect(){
  if(!work)return;const counter=++generation;client?.destroy();buffered.length=0;online=false;
  const next=WKIM.init(work.wsUrl,{uid:work.user.uid,token:work.user.token,deviceFlag:1},{singleton:false});client=next;
  const receive=(fn:()=>void)=>{if(counter!==generation)return;if(!restoring){fn();return;}if(buffered.length<512)buffered.push(()=>{if(counter===generation)fn();});else{next.disconnect();buffered.length=0;error(Error('历史恢复期间消息过多，请重连恢复。'));}};
  next.on(WKIMEvent.Message,m=>receive(()=>{ordinary(m);render();}));next.on(WKIMEvent.CustomEvent,e=>receive(()=>event(e)));
  next.on(WKIMEvent.Connect,()=>{if(counter!==generation)return;online=true;void action(async()=>{update(await api('/state'));await recover();});});next.on(WKIMEvent.Disconnect,()=>{if(counter===generation){online=false;render();}});next.on(WKIMEvent.Error,()=>{if(counter===generation)log('SDK 连接异常');});await next.connect();render();
}
async function action(fn:()=>Promise<void>){$('#error').textContent='';try{await fn();}catch(e){error(e);}finally{render();}}
async function control(path:string,run=current(),extra={}){if(!run||busy)return;busy=true;render();try{await action(async()=>{update(await api(path,{runId:run.id,requestId:crypto.randomUUID(),...extra}));});}finally{busy=false;render();}}
async function submit(){const input=$<HTMLTextAreaElement>('#question'),c=selected(),content=input.value.trim();if(!content||!c||busy||!online||current()&&!done(current()!))return;const requestId=input.dataset.retryId||crypto.randomUUID();input.dataset.retryId=requestId;busy=true;render();try{await action(async()=>{try{update(await api('/send',{conversationId:c.id,content,requestId}));input.value='';delete input.dataset.retryId;}catch(e){if([400,404,409,429].includes((e as {status?:number}).status||0))delete input.dataset.retryId;throw e;}});}finally{busy=false;render();}}
$('#start').onclick=()=>void action(async()=>{starting=true;render();try{const next=await api('/session',{});capability=next.token;update(next);await connect();}finally{starting=false;render();}});
const newConversation=()=>void action(async()=>{busy=true;render();try{const next=await api('/new',{requestId:crypto.randomUUID()});switchConversation(next.conversations.at(-1).id);update(next);await recover();}finally{busy=false;render();}});
$('#new-conversation').onclick=$('#mobile-new').onclick=newConversation;
$<HTMLSelectElement>('#conversation-select').onchange=()=>void action(async()=>{switchConversation($<HTMLSelectElement>('#conversation-select').value);await recover();});
$('#composer').onsubmit=e=>{e.preventDefault();void submit();};$('#question').onkeydown=e=>{if(e.key==='Enter'&&!e.shiftKey&&!e.isComposing&&e.keyCode!==229){e.preventDefault();void submit();}};$('#question').oninput=()=>{delete $('#question').dataset.retryId;};
document.querySelectorAll<HTMLButtonElement>('[data-question]').forEach(b=>{b.onclick=()=>{$<HTMLTextAreaElement>('#question').value=b.dataset.question!;void submit();};});
$('#pause').onclick=()=>void control(current()?.paused?'/resume':'/pause');$('#cancel').onclick=()=>void control('/cancel');$('#disconnect').onclick=()=>void action(async()=>{if(online)client?.disconnect();else await connect();});
document.querySelectorAll<HTMLButtonElement>('[data-view]').forEach(b=>{b.onclick=()=>{$('.workspace').dataset.view=b.dataset.view;document.querySelectorAll('[data-view]').forEach(e=>e.classList.toggle('active',e===b));};});
function mode(){const real=$<HTMLSelectElement>('#mode').value==='model';$('#model-options').hidden=!real;$('#simulation-options').hidden=real;}
$('#settings-button').onclick=()=>{const c=work?.config;$<HTMLInputElement>('#backend-url').value=backend;if(c){$<HTMLSelectElement>('#mode').value=c.mode;$<HTMLInputElement>('#model-url').value=c.url;$<HTMLInputElement>('#model-name').value=c.model;$<HTMLInputElement>('#interval').value=String(c.interval);$<HTMLInputElement>('#fail-tool').checked=c.failTool;$<HTMLInputElement>('#model-key').placeholder=c.keyConfigured?'已配置；留空保留':'仅保留在演示业务进程内存';}mode();$<HTMLDialogElement>('#settings').showModal();};
$('#settings-close').onclick=()=>$<HTMLDialogElement>('#settings').close();$('#settings').onclose=()=>{$<HTMLInputElement>('#model-key').value='';};$('#mode').onchange=mode;
$('#backend-url').onchange=()=>{if(work)return;try{const url=new URL($<HTMLInputElement>('#backend-url').value);if(!['http:','https:'].includes(url.protocol)||url.username||url.password)throw Error();backend=url.origin;}catch{error(Error('业务服务 URL 无效。'));}};
$('#config-form').onsubmit=e=>{e.preventDefault();void action(async()=>{const key=$<HTMLInputElement>('#model-key').value;update(await api('/config',{mode:$<HTMLSelectElement>('#mode').value,url:$<HTMLInputElement>('#model-url').value,model:$<HTMLInputElement>('#model-name').value,...(key?{apiKey:key}:{}),interval:Number($<HTMLInputElement>('#interval').value),failTool:$<HTMLInputElement>('#fail-tool').checked}));$<HTMLInputElement>('#model-key').value='';$('#config-result').textContent='已保存，下一个任务使用新配置。';});};
$('#resync').onclick=()=>void action(async()=>{update(await api('/state'));await recover();});window.addEventListener('pagehide',()=>{client?.disconnect();$<HTMLInputElement>('#model-key').value='';});
render();try{const stored=sessionStorage.getItem(storage);if(stored){const saved=JSON.parse(stored);backend=saved.backend;capability=saved.capability;conversationId=saved.conversationId;void action(async()=>{update(await api('/state'));await connect();});}}catch{sessionStorage.removeItem(storage);}
