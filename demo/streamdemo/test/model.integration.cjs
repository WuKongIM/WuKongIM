// Process-level acceptance: Chromium -> demo proxy -> streaming model fixture,
// then real WuKongIM HTTP events -> EasySDK. No external keys or paid calls.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const http = require('node:http');
const net = require('node:net');
const { spawn } = require('node:child_process');
const { once } = require('node:events');
const { chromium } = require(process.env.WK_DEMO_PLAYWRIGHT || 'playwright');
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(check, label, ms = 30000) {
  const deadline = Date.now() + ms;
  while (Date.now() < deadline) { if (await check()) return; await delay(50); }
  throw Error(`Timed out: ${label}`);
}
async function port() {
  const server = net.createServer(); server.listen(0, '127.0.0.1'); await once(server, 'listening');
  const result = server.address().port; await new Promise(resolve => server.close(resolve)); return result;
}
async function main() {
  assert(process.env.WK_DEMO_SERVER_BIN, 'Set WK_DEMO_SERVER_BIN to the freshly built server');
  const evidence = await fs.mkdtemp(path.join(os.tmpdir(), 'wk-model-demo-'));
  const api = await port(), raft = await port(), ws = await port(), demoPort = await port();
  const apiBase = `http://127.0.0.1:${api}`;
  await fs.writeFile(path.join(evidence, 'wukongim.toml'), `
[node]
id = 1
data_dir = "${evidence}/data"
[cluster]
id = "model-demo-validation"
listen_addr = "127.0.0.1:${raft}"
nodes = [{id = 1, addr = "127.0.0.1:${raft}"}]
initial_slot_count = 8
hash_slot_count = 256
slot_replica_n = 1
[api]
listen_addr = "127.0.0.1:${api}"
external_ws_addr = "ws://127.0.0.1:${ws}"
[manager]
listen_addr = "127.0.0.1:0"
[gateway]
token_auth_on = true
listeners = [{name = "ws", network = "websocket", address = "127.0.0.1:${ws}", transport = "gnet", protocol = "wsmux"}]
[log]
level = "warn"
dir = "${evidence}/logs"
`);
  const requests = []; let aborted = 0;
  const key = 'fixture-secret-model-key';
  const model = http.createServer(async (req, res) => {
    if (req.headers.authorization !== `Bearer ${key}`) { res.writeHead(401); res.end(JSON.stringify({error:{message:'Invalid API key'}})); return; }
    if (req.url === '/v1/models') { res.setHeader('content-type','application/json'); res.end(JSON.stringify({data:[{id:'demo-chat'}]})); return; }
    if (!req.url.startsWith('/v1/chat/completions')) { res.writeHead(404); res.end(); return; }
    let bytes=''; for await (const chunk of req) bytes+=chunk;
    const body=JSON.parse(bytes); requests.push(body);
    if (body.model === 'http-error') { res.writeHead(429); res.end(JSON.stringify({error:{message:key}})); return; }
    res.writeHead(200, {'content-type':'text/event-stream'});
    let done=false; res.on('close',()=>{if(!done)aborted++});
    const event = packet => `data: ${JSON.stringify(packet)}\r\n\r\n`;
    const packet = content => ({choices:[{index:0,delta:{content},finish_reason:null}]});
    res.write(': keepalive\r\n\r\n');
    res.write(event({choices:[{index:0,delta:{role:'assistant'},finish_reason:null}]}));
    const first=Buffer.from(event(packet('你好🙂')));
    for (let offset=0;offset<first.length;offset+=3) { res.write(first.subarray(offset,offset+3)); await delay(2); }
    await delay(body.model === 'slow-chat' ? 2000 : 250);
    if(res.destroyed)return;
    if(body.model === 'truncated-chat'){done=true;res.end();return;}
    if(body.model === 'sse-error'){res.write(event({error:{message:key}}));done=true;res.end();return;}
    // Multiple data lines form one JSON event, delivered with CRLF framing.
    res.write('data: {"choices":\r\ndata: [{"index":0,"delta":{"content":"，真实模型回复。"},"finish_reason":null}]}\r\n\r\n');
    await delay(150);
    res.write(event({choices:[{index:0,delta:{},finish_reason:'stop'}]}));
    res.write(event({choices:[],usage:{completion_tokens:12}}));
    res.write('data: [DONE]\r\n\r\n'); done=true; res.end();
  });
  model.listen(0,'127.0.0.1'); await once(model,'listening');
  const modelURL=`http://127.0.0.1:${model.address().port}/v1`;
  const cleanEnv=Object.fromEntries(Object.entries(process.env).filter(([name])=>!name.startsWith('WK_')));
  const server=spawn(process.env.WK_DEMO_SERVER_BIN,['-config',path.join(evidence,'wukongim.toml')],{cwd:evidence,env:cleanEnv,stdio:['ignore','pipe','pipe']});
  const dev=process.env.WK_DEMO_MODEL_DEV==='1';
  const demo=spawn(process.execPath,dev?[path.resolve(__dirname,'../node_modules/vite/bin/vite.js'),'--host','127.0.0.1','--port',String(demoPort),'--strictPort']:[path.resolve(__dirname,'../server.mjs')],{cwd:path.resolve(__dirname,'..'),env:{...cleanEnv,WK_DEMO_PORT:String(demoPort),WK_DEMO_API_URL:apiBase},stdio:['ignore','pipe','pipe']});
  let log=''; for(const proc of [server,demo])for(const stream of [proc.stdout,proc.stderr])stream.on('data',chunk=>log+=chunk);
  const exits=[once(server,'exit'),once(demo,'exit')];
  let browser; const errors=[];
  try {
    await until(async()=>{try{return (await fetch(apiBase+'/readyz')).ok}catch{return false}},'WuKongIM readiness');
    const base=`http://127.0.0.1:${demoPort}`;
    await until(async()=>{try{return (await fetch(base+'/streamdemo/')).ok}catch{return false}},'demo readiness');
    // The local proxy must not accept cross-site POSTs or upstream redirects.
    const denied=await fetch(base+'/streamdemo/api/chat',{method:'POST',headers:{origin:'https://other.example','content-type':'application/json'},body:'{}'});assert.equal(denied.status,403);
    browser=await chromium.launch({headless:true});
    const page=await browser.newPage({viewport:{width:1440,height:1000}});page.setDefaultTimeout(30000);page.on('pageerror',error=>errors.push(error.message));
    let syncCount=0;page.on('request',req=>{if(req.url().includes('/channel/messagesync'))syncCount++});
    await page.goto(base+'/streamdemo/?apiurl='+encodeURIComponent(apiBase));
    await page.getByRole('button',{name:'创建演示账号并连接',exact:true}).click();
    await page.getByTestId('connection').getByText('在线',{exact:true}).waitFor();
    await page.locator('#settings > summary').click();
    await page.getByLabel('回复来源').selectOption('model');
    await page.getByLabel('模型 URL',{exact:true}).fill(modelURL);
    await page.getByLabel('API Key',{exact:true}).fill(key);
    await page.getByLabel('模型名称',{exact:true}).fill('');
    await page.locator('#settings > summary').click();
    async function ask(text,status='完成') {
      const before=await page.locator('.stream-message').count();
      await until(()=>page.getByRole('button',{name:'发送消息',exact:true}).isEnabled(),'ready composer');
      await page.getByLabel('消息',{exact:true}).fill(text);await page.getByRole('button',{name:'发送消息',exact:true}).click();
      await until(async()=>await page.locator('.stream-message').count()===before+1,'new stream');
      await page.locator('.stream-message').last().getByText(status,{exact:true}).waitFor();
      return page.locator('.stream-message').last().locator('.body').innerText();
    }
    const before=syncCount;
    assert.equal(await ask('第一轮问题'),'你好🙂，真实模型回复。');
    assert.equal(syncCount,before,'No online history polling');
    assert.equal(requests[0].stream,true);assert.equal(requests[0].model,'demo-chat');
    assert.deepEqual(requests[0].messages,[{role:'user',content:'第一轮问题'}]);
    assert.equal(await page.getByLabel('模型名称',{exact:true}).inputValue(),'demo-chat','URL and key alone discover a model');
    assert.equal(await ask('第二轮问题'),'你好🙂，真实模型回复。');
    assert.deepEqual(requests[1].messages,[{role:'user',content:'第一轮问题'},{role:'assistant',content:'你好🙂，真实模型回复。'},{role:'user',content:'第二轮问题'}]);
    await page.screenshot({path:path.join(evidence,'model-chat.png')});
    async function setModel(name,url=modelURL){await page.locator('#settings > summary').click();await page.getByLabel('模型名称',{exact:true}).fill(name);await page.getByLabel('模型 URL',{exact:true}).fill(url);await page.locator('#settings > summary').click()}
    await setModel('slow-chat',modelURL+'/chat/completions?fixture=full-url');
    const count=await page.locator('.stream-message').count();
    await page.getByLabel('消息',{exact:true}).fill('中止这一轮');await page.getByRole('button',{name:'发送消息',exact:true}).click();
    await until(async()=>await page.locator('.stream-message').count()===count+1,'cancel stream');
    await page.locator('.stream-message').last().getByText('你好🙂',{exact:true}).waitFor();
    await page.getByRole('button',{name:'取消生成',exact:true}).click();
    await page.locator('.stream-message').last().getByText('已取消',{exact:true}).waitFor();
    await until(()=>aborted>0,'upstream cancellation');
    for(const name of ['http-error','sse-error','truncated-chat']){await until(()=>page.getByRole('button',{name:'发送消息',exact:true}).isEnabled(),'end cancellation');await setModel(name);await ask(name,'失败')}
    const logged=await page.locator('#logs').textContent();assert(!logged.includes(key),'Key absent from logs');
    const storage=await page.evaluate(()=>JSON.stringify({session:{...sessionStorage},local:{...localStorage}}));assert(!storage.includes(key),'Key is never persisted');
    const session=JSON.parse(await page.evaluate(()=>sessionStorage.getItem('wk-streamdemo-session')));
    const persisted=await fetch(apiBase+'/channel/messagesync',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({login_uid:session.reader,channel_id:session.channel,channel_type:2,limit:40,event_summary_mode:'full'})}).then(r=>r.text());assert(!persisted.includes(key),'Key absent from message history');
    await page.reload();
    await page.locator('.stream-message').last().getByText('失败',{exact:true}).waitFor();
    assert.equal(await page.getByLabel('API Key',{exact:true}).inputValue(),'','Reload clears API key');
    assert.equal(await page.getByLabel('回复来源').inputValue(),'model');
    assert.deepEqual(errors,[]);
    const report={passed:true,evidence,mode:dev?'vite':'standalone',checks:['OpenAI-compatible SSE through local proxy and real EasySDK','URL normalization and automatic model discovery','multi-turn context','UTF-8 and multiline SSE','upstream cancellation','HTTP and SSE errors','truncated stream is failure','key excluded from logs/storage/history','offline recovery','no online polling','cross-origin proxy rejection'],pageErrors:errors};
    await fs.writeFile(path.join(evidence,'report.json'),JSON.stringify(report,null,2));console.log(JSON.stringify(report));
  } catch(error){if(browser)for(const context of browser.contexts())for(const page of context.pages())await page.screenshot({path:path.join(evidence,'failure.png')}).catch(()=>{});console.error('Model evidence:',evidence);throw error}
  finally {
    await browser?.close();for(const proc of [server,demo])proc.kill('SIGTERM');await Promise.race([Promise.all(exits),delay(5000)]);
    for(const proc of [server,demo])if(proc.exitCode===null)proc.kill('SIGKILL');await Promise.all(exits);
    model.closeAllConnections();await new Promise(resolve=>model.close(resolve));await fs.writeFile(path.join(evidence,'server.log'),log);
  }
}
main().catch(error=>{console.error(error);process.exitCode=1});
