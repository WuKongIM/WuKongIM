// Process-level UI checks against a real single-node cluster; opt-in only.
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
    assert(process.env.WK_DEMO_SERVER_BIN, 'Build the server with the current embedded Demo first');
    const evidence = await fs.mkdtemp(path.join(os.tmpdir(), 'wk-demo-ui-'));
    console.log('Browser evidence:', evidence);
    const api = await port(), raft = await port(), ws = await port();
    const base = `http://127.0.0.1:${api}`;
    await fs.writeFile(path.join(evidence, 'wukongim.toml'), `
[node]
id = 1
data_dir = "${evidence}/data"
[cluster]
id = "demo-ui-validation"
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
    const exits = once(server, 'exit');
    let browser; const errors = [], checks = [];
    async function post(route, body) {
        const response = await fetch(base + route, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body) });
        const data = await response.json(); assert.equal(response.status, 200, `${route}: ${JSON.stringify(data)}`); return data;
    }
    const send = text => post('/message/send', { from_uid: 'bob', channel_id: 'alice', channel_type: 1, header: { red_dot: 1 }, payload: Buffer.from(JSON.stringify({ type: 1, content: text })).toString('base64') });
    const open = (page, channel) => page.locator('.conversation-item').filter({ has: page.locator('.title', { hasText: channel }) }).click();
    const connected = page => page.locator('[data-testid="connection-status"][data-state="connected"]').waitFor();
    async function login(page, lang = 'en') {
        page.setDefaultTimeout(30000); page.on('pageerror', error => errors.push(error.message));
        await page.goto(base + `/demo/?lang=${lang}`);
        await page.getByPlaceholder(lang === 'en' ? 'Enter an existing user UID' : '请输入已有用户 UID').fill('alice');
        const token = page.getByPlaceholder(lang === 'en' ? 'Enter the existing Web token' : '请输入原有 Web Token');
        await page.screenshot({ path: path.join(evidence, `login-${lang}.png`) });
        await token.fill('alice-ui-test'); await token.press('Enter'); await connected(page);
    }
    async function fits(page) {
        assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'no horizontal page overflow');
        const rect = await page.locator('.footer textarea').boundingBox();
        assert(rect.width >= 200 && rect.x >= 0 && rect.x + rect.width <= (await page.viewportSize()).width, 'usable composer');
    }
    try {
        await until(async () => { try { return (await fetch(base + '/route')).ok } catch { return false } }, 'cluster readiness');
        await post('/user/token', { uid: 'alice', token: 'alice-ui-test', device_flag: 1, device_level: 0 });
        await post('/user/token', { uid: 'bob', token: 'bob-ui-test', device_flag: 1, device_level: 0 });
        await post('/channel/subscriber_add', { channel_id: 'ui-group', channel_type: 2, subscribers: ['alice', 'bob'] });
        for (let index = 0; index < 36; index++) await send(`History ${index}: a message long enough to wrap on a phone.\nSecond line for reading position.`);
        await until(async () => (await post('/conversation/list', { uid: 'alice', limit: 200 })).conversations.length > 0, 'directory projection');
        browser = await chromium.launch({ headless: true });
        const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
        await page.route('**/conversation/list', route => route.fulfill({ status: 503, contentType: 'application/json', body: '{"error":"fixture unavailable"}' }));
        await login(page); await page.getByText('Could not load chats.', { exact: true }).waitFor();
        await page.unroute('**/conversation/list');
        await page.locator('.conversation-box').getByRole('button', { name: 'Retry', exact: true }).click();
        await open(page, 'bob'); await page.locator('.text').getByText(/^History 35:/).waitFor(); await fits(page); checks.push('conversation sync failure and retry');
        const input = page.getByRole('textbox', { name: 'Enter a message', exact: true });
        const count = await page.locator('.message').count();
        assert(await page.getByRole('button', { name: 'Send', exact: true }).isDisabled());
        await input.fill('   '); await input.press('Enter');
        assert.equal(await page.locator('.message').count(), count); checks.push('blank messages blocked');
        await input.fill('Keyboard send'); await input.press('Enter');
        await page.locator('.message.right .text').getByText('Keyboard send', { exact: true }).waitFor();
        assert.equal(await input.inputValue(), ''); checks.push('Enter submits login and messages');
        await input.fill('line one'); await input.press('Shift+Enter'); await input.type('line two');
        assert.equal(await input.inputValue(), 'line one\nline two'); await input.fill('');
        const beforeComposition = await page.locator('.message').count();
        await input.dispatchEvent('compositionstart'); await input.fill('中文输入'); await input.press('Enter');
        assert.equal((await input.inputValue()).trim(), '中文输入');
        assert.equal(await page.locator('.message').count(), beforeComposition); await input.dispatchEvent('compositionend'); await input.fill(''); checks.push('newline and IME');
        // Reading history must not jump when a live message arrives. History prepend keeps its anchor.
        await page.getByRole('button', { name: 'Load earlier messages', exact: true }).click();
        await page.locator('.text').getByText(/^History 6:/).waitFor();
        await page.locator('.message-list').evaluate(el => { el.scrollTop = 350; el.dispatchEvent(new Event('scroll')); });
        const before = await page.locator('.message-list').evaluate(el => el.scrollTop);
        await send('New live message while reading'); await page.locator('.text').getByText('New live message while reading', { exact: true }).waitFor({ state: 'attached' });
        assert(Math.abs(await page.locator('.message-list').evaluate(el => el.scrollTop) - before) < 5, 'live arrival preserves scroll');
        await page.getByRole('button', { name: /new message/i }).click();
        await until(async () => await page.locator('.message-list').evaluate(el => el.scrollHeight - el.clientHeight - el.scrollTop < 5), 'jump to latest'); checks.push('history anchor and new-message button');
        await page.screenshot({ path: path.join(evidence, 'desktop-light.png') });
        // A rejected SEND retries the same identity and leaves only one delivered message.
        await page.getByRole('button', { name: 'Demo tools', exact: true }).click();
        await page.locator('#demo-tools').getByRole('button', { name: 'Start a chat', exact: true }).click();
        await page.getByRole('radio', { name: 'Group chat', exact: true }).check();
        await page.getByPlaceholder('Enter the group ID').fill('ui-group');
        await page.getByRole('button', { name: 'OK', exact: true }).focus(); await page.keyboard.press('Tab');
        assert.equal(await page.getByRole('dialog').getByRole('button', { name: 'Cancel', exact: true }).evaluate(el => el === document.activeElement), true);
        await page.getByRole('button', { name: 'OK', exact: true }).click();
        await page.getByRole('dialog').waitFor({ state: 'hidden' });
        await page.locator('.chat-title strong').getByText('ui-group', { exact: true }).waitFor();
        await post('/channel/blacklist_add', { channel_id: 'ui-group', channel_type: 2, uids: ['alice'] });
        await input.fill('Rejected and retried'); await input.press('Enter');
        const failed = page.locator('.message').filter({ hasText: 'Rejected and retried' });
        await failed.getByText('Send failed', { exact: true }).waitFor();
        const identity = await failed.getAttribute('id');
        await post('/channel/blacklist_remove', { channel_id: 'ui-group', channel_type: 2, uids: ['alice'] });
        await failed.getByRole('button', { name: 'Retry', exact: true }).click();
        await failed.getByRole('button', { name: 'Edit', exact: true }).waitFor();
        assert.equal(await failed.getAttribute('id'), identity);
        const history = await post('/channel/messagesync', { login_uid: 'alice', channel_id: 'ui-group', channel_type: 2, limit: 100 });
        assert.equal(history.messages.filter(m => Buffer.from(m.payload, 'base64').toString().includes('Rejected and retried')).length, 1); checks.push('rejected SEND same-identity retry');
        await page.emulateMedia({ colorScheme: 'dark' }); await page.screenshot({ path: path.join(evidence, 'desktop-dark.png') });
        await page.context().close();
        const mobile = await browser.newPage({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });
        await login(mobile, 'zh'); assert.equal(await mobile.locator('.conversation-box').isVisible(), true);
        assert.equal(await mobile.locator('.message-box').isVisible(), false);
        await mobile.getByRole('button', { name: '开始聊天', exact: true }).click();
        await mobile.getByRole('dialog').waitFor(); await mobile.keyboard.press('Escape');
        await mobile.getByRole('dialog').waitFor({ state: 'hidden' });
        await mobile.screenshot({ path: path.join(evidence, 'mobile-list.png') });
        await open(mobile, 'bob'); await fits(mobile);
        assert.equal(await mobile.locator('.conversation-box').isVisible(), false);
        await mobile.getByRole('textbox', { name: '请输入消息', exact: true }).fill('手机草稿');
        await mobile.getByRole('button', { name: '返回聊天列表', exact: true }).click();
        await open(mobile, 'bob'); assert.equal(await mobile.getByRole('textbox', { name: '请输入消息', exact: true }).inputValue(), '手机草稿');
        await mobile.screenshot({ path: path.join(evidence, 'mobile-chat.png') });
        await mobile.setViewportSize({ width: 320, height: 568 }); await fits(mobile);
        await mobile.getByRole('button', { name: '演示工具', exact: true }).click();
        assert(await mobile.getByRole('button', { name: '重新同步', exact: true }).isDisabled());
        await mobile.keyboard.press('Escape'); checks.push('mobile two-page navigation, 320px composer and draft');
        await mobile.context().close();
        const rejected = await browser.newPage({ viewport: { width: 390, height: 844 } });
        rejected.on('pageerror', error => errors.push(error.message));
        await rejected.goto(base + '/demo/?lang=en');
        await rejected.getByPlaceholder('Enter an existing user UID').fill('alice');
        await rejected.getByPlaceholder('Enter the existing Web token').fill('wrong-token');
        await rejected.getByRole('button', { name: 'Log in', exact: true }).click();
        await rejected.locator('[data-testid="connection-status"][data-state="rejected"]').waitFor();
        await rejected.getByRole('alert').getByText('Authentication failed: check the existing Web token', { exact: true }).waitFor();
        await rejected.screenshot({ path: path.join(evidence, 'authentication-failed.png') });
        checks.push('visible authentication rejection');
        assert.deepEqual(errors, []);
        const result = { passed: true, evidence, checks, pageErrors: errors };
        await fs.writeFile(path.join(evidence, 'result.json'), JSON.stringify(result, null, 2)); console.log(JSON.stringify(result));
    } catch (error) {
        if (browser) for (const [index, context] of browser.contexts().entries()) for (const page of context.pages()) await page.screenshot({ path: path.join(evidence, `failure-${index}.png`) }).catch(() => {});
        throw error;
    } finally {
        await browser?.close(); server.kill('SIGTERM'); await Promise.race([exits, delay(5000)]);
        if (server.exitCode === null) { server.kill('SIGKILL'); await exits; }
        await fs.writeFile(path.join(evidence, 'server.log'), log);
    }
}
main().catch(error => { console.error(error); process.exitCode = 1 });
