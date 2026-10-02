// Opt-in real single-node cluster and Chromium check; never runs in the unit tier.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
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
    assert(process.env.WK_DEMO_SERVER_BIN, 'Set WK_DEMO_SERVER_BIN to the freshly built server with embedded Demo');
    const evidence = await fs.mkdtemp(path.join(os.tmpdir(), 'wk-stream-demo-'));
    const api = await port(), raft = await port(), ws = await port();
    const base = `http://127.0.0.1:${api}`;
    await fs.writeFile(path.join(evidence, 'wukongim.toml'), `
[node]
id = 1
data_dir = "${evidence}/data"
[cluster]
id = "stream-demo-validation"
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
    const server = spawn(process.env.WK_DEMO_SERVER_BIN, ['-config', path.join(evidence, 'wukongim.toml')], {
        cwd: evidence, env: Object.fromEntries(Object.entries(process.env).filter(([name]) => !name.startsWith('WK_'))), stdio: ['ignore', 'pipe', 'pipe'],
    });
    let log = ''; server.stdout.on('data', data => { log += data }); server.stderr.on('data', data => { log += data });
    let browser;
    const exits = once(server, 'exit');
    const errors = [];
    try {
        await until(async () => { try { return (await fetch(base + '/readyz')).ok } catch { return false } }, 'cluster readiness');
        browser = await chromium.launch({ headless: true });
        const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
        page.setDefaultTimeout(30000); page.on('pageerror', error => errors.push(error.message));
        let syncCount=0, onlineSync=0;
        page.on('request',request=>{if(request.url().includes('/channel/messagesync'))syncCount++});
        await page.goto(base + '/streamdemo/');
        await page.getByRole('button',{name:'创建演示账号并连接',exact:true}).click();
        await page.getByTestId('connection').getByText('在线', {exact:true}).waitFor();
        await page.locator('#settings > summary').click();
        await page.getByLabel('模拟内容').fill('你好，流式消息。每一段都经过真实 WuKongIM。');
        await page.getByLabel('发送间隔（毫秒）').fill('80');
        await until(()=>page.getByRole('button',{name:'开始生成',exact:true}).isEnabled(),'connected writer');
        await delay(300); onlineSync=syncCount;
        await page.locator('#settings > summary').click();
        await page.getByLabel('消息',{exact:true}).fill('你能帮我做什么？');
        await page.getByRole('button',{name:'发送消息',exact:true}).click();
        await page.locator('.user-message').getByText('你能帮我做什么？',{exact:true}).waitFor();
        await page.locator('.stream-message').last().getByText('完成', {exact:true}).waitFor();
        assert.equal(await page.locator('.stream-message').last().locator('.body').innerText(),'你好，流式消息。每一段都经过真实 WuKongIM。');
        assert.equal(syncCount,onlineSync,'No history polling while online');
        assert(await page.locator('#logs').textContent().then(t=>t.includes('stream.delta')));
        await page.screenshot({path:path.join(evidence,'completed.png')});
        assert.equal(await page.locator('.user-message').count(),1,'One actual SDK question bubble');
        await page.locator('#settings > summary').click();
        // Retry after finish: the server must preserve the completed projection.
        const delta=await page.evaluate(()=>JSON.parse(document.querySelector('#logs').dataset.lastDelta));
        const response=await fetch(base+'/message/event',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(delta)});assert.equal(response.status,200);
        await delay(100);assert.equal(await page.locator('.stream-message').last().locator('.body').innerText(),'你好，流式消息。每一段都经过真实 WuKongIM。');
        for(const [action,status] of [['取消生成','已取消'],['模拟失败','失败']]){
            await page.getByLabel('模拟内容').fill('这是一段足够长的模拟回复，用来在生成中触发取消和失败，确保不会只看到最终结果。');
            await page.getByLabel('发送间隔（毫秒）').fill('250');
            await page.getByRole('button',{name:'开始生成',exact:true}).click();
            await until(async()=>await page.locator('.stream-message').last().locator('.body').innerText()!=='','first delta');
            await page.getByRole('button',{name:action,exact:true}).click();
            await page.locator('.stream-message').last().getByText(status,{exact:true}).waitFor();
        }
        await page.getByRole('button',{name:'断开接收端',exact:true}).click();
        await page.getByTestId('connection').getByText('离线',{exact:true}).waitFor();
        const before=await page.locator('.stream-message').count();
        await page.getByLabel('模拟内容').fill('离线期间生成的完整内容');
        await page.getByLabel('发送间隔（毫秒）').fill('40');
        await page.getByRole('button',{name:'开始生成',exact:true}).click();
        await until(()=>page.getByRole('button',{name:'开始生成',exact:true}).isEnabled(),'offline generation completed');
        assert.equal(await page.locator('.stream-message').count(),before);
        await page.getByRole('button',{name:'重连接收端',exact:true}).click();
        await page.locator('.stream-message').last().getByText('离线期间生成的完整内容',{exact:true}).waitFor();
        assert(syncCount>onlineSync);
        await page.reload();
        await page.locator('.stream-message').last().getByText('离线期间生成的完整内容',{exact:true}).waitFor();
        assert.equal(await page.locator('.user-message').count(),1,'Question is recovered with the stream history');
        const contentOrder=await page.locator('#messages > [data-message-key]').evaluateAll(nodes=>nodes.map(node=>node.textContent));
        assert(contentOrder[0].includes('你能帮我做什么？'),'Recovered conversation preserves message order');
        await page.locator('#settings > summary').click();
        await page.getByLabel('模拟内容').fill('重连时快照和实时增量交错，也不应重复文本。这里继续输出一些内容来覆盖恢复边界。');
        await page.getByLabel('发送间隔（毫秒）').fill('120');
        await page.getByRole('button',{name:'开始生成',exact:true}).click();
        await until(async()=>await page.locator('.stream-message').last().locator('.body').innerText()!=='','midstream delta');
        await page.getByRole('button',{name:'断开接收端',exact:true}).click();await delay(300);
        await page.getByRole('button',{name:'重连接收端',exact:true}).click();
        await page.locator('.stream-message').last().getByText('完成',{exact:true}).waitFor();
        assert.equal(await page.locator('.stream-message').last().locator('.body').innerText(),'重连时快照和实时增量交错，也不应重复文本。这里继续输出一些内容来覆盖恢复边界。');
        assert.deepEqual(errors,[]);
        await page.locator('#settings > summary').click();
        await page.screenshot({path:path.join(evidence,'recovered.png')});
        await page.setViewportSize({width:390,height:844});
        const mobileStreams=await page.locator('.stream-message').count();
        await page.getByLabel('消息',{exact:true}).fill('手机上也能聊天吗？');
        await page.getByRole('button',{name:'发送消息',exact:true}).click();
        await page.locator('.user-message').getByText('手机上也能聊天吗？',{exact:true}).waitFor();
        await until(async()=>await page.locator('.stream-message').count()===mobileStreams+1,'mobile assistant message');
        await page.locator('.stream-message').last().getByText('完成',{exact:true}).waitFor();
        assert.equal(await page.getByLabel('消息',{exact:true}).inputValue(),'');
        assert(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth),'Mobile has no horizontal overflow');
        await page.screenshot({path:path.join(evidence,'mobile.png')});
        const report={passed:true,evidence,sdk:'easyjssdk@2.0.5',checks:['actual SDK user messages','real EasySDK EVENT delivery','no online history polling','complete','cancel','error','retry deduplication','offline and reload recovery with conversation order','reconnect during generation','mobile chat layout'],pageErrors:errors};
        await fs.writeFile(path.join(evidence,'report.json'),JSON.stringify(report,null,2));console.log(JSON.stringify(report));
    } catch(error){
        if(browser)for(const context of browser.contexts())for(const page of context.pages())await page.screenshot({path:path.join(evidence,'failure.png')}).catch(()=>{});
        console.error('Browser evidence:',evidence);throw error;
    } finally{
        await browser?.close();server.kill('SIGTERM');await Promise.race([exits,delay(5000)]);
        if(server.exitCode===null){server.kill('SIGKILL');await exits}
        await fs.writeFile(path.join(evidence,'server.log'),log);
    }
}
main().catch(error=>{console.error(error);process.exitCode=1});
