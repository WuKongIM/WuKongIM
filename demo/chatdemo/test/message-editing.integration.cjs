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
    const evidence = await fs.mkdtemp(path.join(os.tmpdir(), 'wk-demo-edit-'));
    const api = await port(), raft = await port(), ws = await port();
    const base = `http://127.0.0.1:${api}`;
    await fs.writeFile(path.join(evidence, 'wukongim.toml'), `
[node]
id = 1
data_dir = "${evidence}/data"
[cluster]
id = "demo-edit-validation"
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
    async function post(route, body) {
        const response = await fetch(base + route, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body) });
        const data = await response.json(); assert.equal(response.status, 200, `${route}: ${JSON.stringify(data)}`);
        return { data, epoch: response.headers.get('x-wk-content-epoch') };
    }
    const payload = text => Buffer.from(JSON.stringify({ type: 1, content: text })).toString('base64');
    async function open(page, channel) {
        await page.locator('.conversation-item').filter({ has: page.locator('.title', { hasText: channel }) }).click();
    }
    async function hasBody(page, text) { await page.locator('.message-list .text').getByText(text, { exact: true }).waitFor(); }
    const row = (page, text) => page.locator('.message').filter({ has: page.locator('.text', { hasText: text }) });
    try {
        await until(async () => { try { return (await fetch(base + '/route')).ok } catch { return false } }, 'cluster readiness');
        for (const uid of ['alice', 'bob']) await post('/user/token', { uid, token: `${uid}-demo-test`, device_flag: 1, device_level: 0 });
        await post('/channel/subscriber_add', { channel_id: 'edit-group', channel_type: 2, subscribers: ['alice', 'bob'] });
        for (const [channel, type, text] of [['bob', 1, 'older original'], ['bob', 1, 'tail original'], ['edit-group', 2, 'group original']]) {
            await post('/message/send', { from_uid: 'alice', channel_id: channel, channel_type: type, header: { red_dot: 1 }, payload: payload(text) });
        }
        await until(async () => (await post('/conversation/list', { uid: 'alice', limit: 200 })).data.conversations.length >= 2, 'directory projection');
        browser = await chromium.launch({ headless: true });
        const alice = await browser.newPage({ viewport: { width: 1440, height: 900 } });
        const bob = await browser.newPage({ viewport: { width: 1440, height: 900 } });
        for (const [page, uid] of [[alice, 'alice'], [bob, 'bob']]) {
            page.setDefaultTimeout(30000); page.on('pageerror', error => errors.push(error.message));
            await page.goto(base + '/demo/?lang=en');
            await page.getByPlaceholder('Enter an existing user UID').fill(uid);
            await page.getByPlaceholder('Enter the existing Web token').fill(`${uid}-demo-test`);
            await page.getByRole('button', { name: 'Log in', exact: true }).click();
            await page.getByText(new RegExp(`${uid} \\(Connected`)).waitFor();
        }
        await open(alice, 'bob'); await open(bob, 'alice');
        await hasBody(alice, 'older original'); await hasBody(bob, 'tail original');
        assert.equal(await bob.getByRole('button', { name: 'Edit', exact: true }).count(), 0);
        const draft = alice.getByRole('textbox', { name: 'Enter a message', exact: true });
        await draft.fill('preserved sending draft');
        await row(alice, 'older original').getByRole('button', { name: 'Edit', exact: true }).click();
        await alice.getByRole('textbox', { name: 'Editing message', exact: true }).fill('older edited');
        await alice.getByRole('button', { name: 'Save changes', exact: true }).click();
        await hasBody(alice, 'older edited'); await hasBody(bob, 'older edited');
        assert.equal(await draft.inputValue(), 'preserved sending draft');
        assert.equal(await bob.locator('.conversation-item.selected .last-msg').innerText(), 'tail original');
        await row(alice, 'tail original').getByRole('button', { name: 'Edit', exact: true }).click();
        await alice.getByRole('textbox', { name: 'Editing message', exact: true }).fill('tail edited');
        await alice.getByRole('button', { name: 'Save changes', exact: true }).click();
        await hasBody(bob, 'tail edited');
        await until(async () => await bob.locator('.conversation-item.selected .last-msg').innerText() === 'tail edited', 'tail preview');
        // A remote change while the editor is open must not silently upgrade its CAS snapshot.
        await row(alice, 'tail edited').getByRole('button', { name: 'Edit', exact: true }).click();
        await alice.getByRole('textbox', { name: 'Editing message', exact: true }).fill('my conflict draft');
        const history = await post('/channel/messagesync', { login_uid: 'alice', channel_id: 'bob', channel_type: 1, limit: 20 });
        const tail = history.data.messages.find(m => Buffer.from(m.payload, 'base64').toString().includes('tail edited'));
        await post('/message/update', { login_uid: 'alice', channel_id: 'bob', channel_type: 1,
            message_id: tail.message_idstr, expected_version: tail.version, expected_content_epoch: history.epoch,
            request_id: 'demo-browser-conflict', payload: payload('other device edit') });
        await hasBody(alice, 'other device edit');
        await alice.getByRole('button', { name: 'Save changes', exact: true }).click();
        await alice.getByText('This message changed on another device.', { exact: false }).waitFor();
        assert.equal(await alice.getByRole('textbox', { name: 'Editing message', exact: true }).inputValue(), 'my conflict draft');
        await alice.screenshot({ path: path.join(evidence, 'conflict.png') });
        await alice.getByRole('button', { name: 'Save changes', exact: true }).click();
        await hasBody(bob, 'my conflict draft');
        // Lose every write acknowledgement in one bounded SDK attempt; retry must reuse the request.
        const lost = [];
        await alice.route('**/message/update', async route => { lost.push(route.request().postDataJSON()); await route.fetch(); await route.abort('failed'); });
        await row(alice, 'my conflict draft').getByRole('button', { name: 'Edit', exact: true }).click();
        await alice.getByRole('textbox', { name: 'Editing message', exact: true }).fill('uncertain saved text');
        await alice.getByRole('button', { name: 'Save changes', exact: true }).click();
        await alice.getByText('The result is not confirmed.', { exact: false }).waitFor();
        assert.equal(lost.length, 3); assert(lost.every(request => JSON.stringify(request) === JSON.stringify(lost[0])));
        assert.equal(await alice.getByRole('textbox', { name: 'Editing message', exact: true }).getAttribute('readonly'), '');
        await alice.unroute('**/message/update');
        await alice.getByRole('button', { name: 'Retry', exact: true }).click();
        await hasBody(bob, 'uncertain saved text'); await draft.waitFor();
        // Discard guard must keep both active-channel selection and sending draft intact.
        await row(alice, 'uncertain saved text').getByRole('button', { name: 'Edit', exact: true }).click();
        await alice.getByRole('textbox', { name: 'Editing message', exact: true }).fill('discard me');
        alice.once('dialog', dialog => dialog.dismiss()); await open(alice, 'edit-group');
        assert.equal(await alice.locator('.conversation-item.selected .title').innerText(), 'bob');
        alice.once('dialog', dialog => dialog.accept()); await open(alice, 'edit-group');
        await hasBody(alice, 'group original'); assert.equal(await draft.inputValue(), 'preserved sending draft');
        await open(bob, 'edit-group'); await hasBody(bob, 'group original');
        await row(alice, 'group original').getByRole('button', { name: 'Edit', exact: true }).click();
        await alice.getByRole('textbox', { name: 'Editing message', exact: true }).fill('group edited');
        await alice.getByRole('button', { name: 'Save changes', exact: true }).click(); await hasBody(bob, 'group edited');
        await bob.reload(); await bob.getByText(/bob \(Connected/).waitFor(); await open(bob, 'edit-group'); await hasBody(bob, 'group edited');
        // Fresh SENDACK messages also need an exact ID and first-edit version calibration.
        await draft.fill('sent from the composer');
        await alice.getByRole('button', { name: 'Send', exact: true }).click();
        await hasBody(bob, 'sent from the composer');
        await row(alice, 'sent from the composer').getByRole('button', { name: 'Edit', exact: true }).click();
        await alice.getByRole('textbox', { name: 'Editing message', exact: true }).fill('edited after acknowledgement\nsecond line');
        await alice.getByRole('button', { name: 'Save changes', exact: true }).click();
        await hasBody(bob, 'edited after acknowledgement\nsecond line');
        await alice.screenshot({ path: path.join(evidence, 'alice.png') }); await bob.screenshot({ path: path.join(evidence, 'bob.png') });
        assert.deepEqual(errors, []);
        console.log(JSON.stringify({ passed: true, evidence, checks: ['person edit', 'own-only entry', 'old-message preview isolation', 'tail preview', 'CAS conflict', 'unknown outcome retry', 'draft preservation', 'discard guard', 'group edit', 'reload recovery', 'fresh SENDACK edit'], pageErrors: errors }));
    } catch (error) {
        if (browser) for (const [index, context] of browser.contexts().entries()) for (const page of context.pages()) await page.screenshot({ path: path.join(evidence, `failure-${index}.png`) }).catch(() => {});
        console.error('Browser evidence:', evidence); throw error;
    } finally {
        await browser?.close(); server.kill('SIGTERM');
        await Promise.race([exits, delay(5000)]);
        if (server.exitCode === null) { server.kill('SIGKILL'); await exits; }
        await fs.writeFile(path.join(evidence, 'server.log'), log);
    }
}
main().catch(error => { console.error(error); process.exitCode = 1 });
