// Real-process browser acceptance, declared before the connection recovery fix.
// Failure cases: rejected saved reader/writer credentials, malformed storage,
// a reader-only connection reported as ready, and no way to create a new session.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const net = require('node:net');
const {spawn} = require('node:child_process');
const {once} = require('node:events');
const {chromium} = require(process.env.WK_DEMO_PLAYWRIGHT || 'playwright');
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
async function port() {
    const socket = net.createServer(); socket.listen(0, '127.0.0.1'); await once(socket, 'listening');
    const value = socket.address().port; await new Promise(resolve => socket.close(resolve)); return value;
}
async function until(check, label, ms = 20000) {
    const end = Date.now() + ms;
    while (Date.now() < end) { if (await check()) return; await delay(50); }
    throw Error('Timed out: ' + label);
}
async function main() {
    assert(process.env.WK_DEMO_SERVER_BIN, 'Supply a freshly built server with the embedded UI');
    const evidence = process.env.WK_DEMO_CONNECTION_REPORT_DIR || await fs.mkdtemp(path.join(os.tmpdir(), 'wk-stream-connect-'));
    await fs.mkdir(evidence, {recursive:true});
    const [api,raft,ws] = await Promise.all([port(),port(),port()]);
    const base = `http://127.0.0.1:${api}`;
    await fs.writeFile(path.join(evidence,'wukongim.toml'), `[node]\nid=1\ndata_dir="${evidence}/data"\n[cluster]\nid="stream-connection-acceptance"\nlisten_addr="127.0.0.1:${raft}"\nnodes=[{id=1,addr="127.0.0.1:${raft}"}]\ninitial_slot_count=8\nhash_slot_count=256\nslot_replica_n=1\n[api]\nlisten_addr="127.0.0.1:${api}"\nexternal_ws_addr="ws://127.0.0.1:${ws}"\n[manager]\nlisten_addr="127.0.0.1:0"\n[gateway]\ntoken_auth_on=true\nlisteners=[{name="ws",network="websocket",address="127.0.0.1:${ws}",transport="gnet",protocol="wsmux"}]\n[plugin]\nenable=false\n[log]\nlevel="warn"\ndir="${evidence}/logs"\n`);
    const service = spawn(process.env.WK_DEMO_SERVER_BIN,['-config',path.join(evidence,'wukongim.toml')],{cwd:evidence,env:Object.fromEntries(Object.entries(process.env).filter(([key])=>!key.startsWith('WK_'))),stdio:['ignore','pipe','pipe']});
    const exited = once(service,'exit'); let output='';
    service.stdout.on('data',v=>output+=v);service.stderr.on('data',v=>output+=v);
    let browser,page; const checks=[];
    try {
        await until(async()=>{try{return (await fetch(base+'/readyz')).ok}catch{return false}},'single-node cluster readiness');
        browser=await chromium.launch({headless:true});page=await browser.newPage();page.setDefaultTimeout(12000);
        await page.goto(base+'/streamdemo/');
        await page.getByRole('button',{name:'创建演示账号并连接',exact:true}).click();
        await page.getByTestId('connection').getByText('在线',{exact:true}).waitFor();
        await until(()=>page.getByRole('button',{name:'发送消息',exact:true}).isEnabled(),'both SDK peers ready');
        checks.push('fresh reader and writer connect');
        for (const peer of ['reader','writer']) {
            const stored=await page.evaluate(()=>JSON.parse(sessionStorage.getItem('wk-streamdemo-session')));
            assert(stored[peer].startsWith('streamdemo-'+peer+'-'));
            const response=await fetch(base+'/user/token',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({uid:stored[peer],token:'rotated-owned-test-token',device_flag:1,device_level:0})});
            assert.equal(response.status,200);
            await page.reload();
            await until(async()=>{const error=await page.getByRole('alert').textContent();return !!error && !error.includes('[object Object]')},'readable '+peer+' authentication error');
            assert.notEqual(await page.getByTestId('connection').innerText(),'在线');
            assert.equal(await page.getByRole('button',{name:'发送消息',exact:true}).isEnabled(),false);
            await page.getByRole('button',{name:'重新创建演示会话',exact:true}).click();
            await page.getByRole('button',{name:'创建演示账号并连接',exact:true}).click();
            await until(()=>page.getByRole('button',{name:'发送消息',exact:true}).isEnabled(),'new session after rejected '+peer);
            const next=await page.evaluate(()=>JSON.parse(sessionStorage.getItem('wk-streamdemo-session')));
            assert.notEqual(next.reader,stored.reader);assert.notEqual(next.writer,stored.writer);
            checks.push(peer+' rejection has readable error and fresh-session recovery');
        }
        await page.getByLabel('消息',{exact:true}).fill('恢复后真实流式验收');
        await page.getByRole('button',{name:'发送消息',exact:true}).click();
        await page.locator('.stream-message').last().getByText('完成',{exact:true}).waitFor({timeout:30000});
        checks.push('recovered session sends real SDK question and completed stream');
        await page.screenshot({path:path.join(evidence,'recovered.png')});
        for (const saved of ['{broken',JSON.stringify({api:base,reader:1,token:'bad'})]) {
            await page.evaluate(value=>sessionStorage.setItem('wk-streamdemo-session',value),saved);
            await page.reload();
            await page.getByRole('button',{name:'创建演示账号并连接',exact:true}).waitFor();
            assert(await page.getByRole('alert').textContent());
        }
        checks.push('malformed or incomplete saved session can be recreated');
        await fs.writeFile(path.join(evidence,'report.json'),JSON.stringify({passed:true,checks,evidence},null,2)+'\n');
        console.log(JSON.stringify({passed:true,checks:checks.length,evidence}));
    } catch(error) {
        await page?.screenshot({path:path.join(evidence,'failure.png')}).catch(()=>{});
        await fs.writeFile(path.join(evidence,'failure.json'),JSON.stringify({passed:false,checks,error:error.message},null,2)+'\n');
        throw error;
    } finally {
        await browser?.close();service.kill('SIGTERM');await Promise.race([exited,delay(5000)]);
        if(service.exitCode===null){service.kill('SIGKILL');await exited}
        await fs.writeFile(path.join(evidence,'server.log'),output);
    }
}
main().catch(error=>{console.error(error);process.exitCode=1});
