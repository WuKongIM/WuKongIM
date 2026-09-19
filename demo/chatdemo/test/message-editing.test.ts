import assert from 'node:assert/strict'
import test from 'node:test'
import { Channel, Conversation, Message, MessageStatus, MessageContent, MessageText, MessageUpdateError, WKSDK } from 'wukongimjssdk'
import { MessageEditor, canEditMessage } from '../src/services/messageEditor.ts'
import { collectConversations, contentResponse } from '../src/services/messageEditingHTTP.ts'
import { Convert } from '../src/services/convert.ts'

const sdk = WKSDK.shared()
;(sdk.receiptManager as any).timer.unref()
function message(version = '0', text = 'original') {
    return Object.assign(new Message(), { messageID: '18446744073709551614', messageSeq: 10,
        fromUID: 'alice', channel: new Channel('bob', 1), status: MessageStatus.Normal,
        content: new MessageText(text), contentVersion: version, contentEpoch: '0' })
}

test('only acknowledged own ordinary text is editable, including uint64 IDs', () => {
    assert.equal(canEditMessage(message(), 'alice'), true)
    assert.equal(canEditMessage(message(), 'bob'), false)
    for (const change of [
        (m: Message) => { m.status = MessageStatus.Wait },
        (m: Message) => { m.messageID = '' },
        (m: Message) => { m.header.noPersist = true },
        (m: Message) => { m.header.syncOnce = true },
        (m: Message) => { m.contentStale = true },
        (m: Message) => { m.remoteExtra.revoke = true },
        (m: Message) => { m.content = Object.assign(new MessageContent(), { contentType: 99 }) },
        (m: Message) => { m.content = Object.assign(new MessageContent(), { contentType: 2 }) },
    ]) {
        const m = message(); change(m); assert.equal(canEditMessage(m, 'alice'), false)
    }
})

test('saving keeps the old body and send draft, and excludes concurrent submissions', async () => {
    const m = message(), editor = new MessageEditor()
    editor.begin(m, 'unsent message'); editor.draft = 'edited'
    let finish!: (value: Message) => void
    const pending = editor.save(async (target, content) => {
        assert.equal(target.contentVersion, '0'); assert.equal(content.text, 'edited')
        return new Promise(resolve => { finish = resolve })
    }, async () => m)
    assert.equal(m.content.text, 'original')
    assert.equal(editor.saving, true)
    assert.equal(editor.leave(() => true), false)
    assert.equal(await editor.save(async () => { assert.fail('duplicate write') }, async () => m), false)
    finish(message('1', 'edited')); assert.equal(await pending, true)
    assert.equal(editor.target, undefined); assert.equal(editor.sendingDraft, 'unsent message')
})

test('conflicts refresh the CAS snapshot but preserve the local draft until an explicit next save', async () => {
    const editor = new MessageEditor(); editor.begin(message(), 'send draft'); editor.draft = 'my edit'
    let writes = 0
    await editor.save(async () => { writes++; throw new MessageUpdateError('version_conflict', '', 409) }, async () => message('2', 'other device'))
    assert.equal(writes, 1); assert.equal(editor.draft, 'my edit'); assert.equal(editor.target?.contentVersion, '2')
    assert.equal(editor.errorCode, 'version_conflict')
    await editor.save(async target => { assert.equal(target.contentVersion, '2'); return message('3', 'my edit') }, async () => message())
    assert.equal(editor.target, undefined)
})

test('failed conflict reload prevents writing until the target has been read again', async () => {
    const editor = new MessageEditor(); editor.begin(message(), ''); editor.draft = 'edit'
    await editor.save(async () => { throw new MessageUpdateError('content_epoch_conflict') }, async () => { throw new Error('offline') })
    assert.equal(editor.needsReload, true)
    assert.equal(editor.errorCode, 'edit_reload_failed')
    await editor.save(async () => { assert.fail('must reload first') }, async () => message('4'))
    assert.equal(editor.needsReload, false); assert.equal(editor.draft, 'edit'); assert.equal(editor.target?.contentVersion, '4')
})

test('uncertain writes retain exactly the same draft and block leaving until resolved', async () => {
    const editor = new MessageEditor(); editor.begin(message(), 'send draft'); editor.draft = 'same edit'
    await editor.save(async () => { throw new MessageUpdateError('timeout') }, async () => message())
    assert.equal(editor.outcomeUnknown, true); assert.equal(editor.leave(() => true), false)
    await editor.save(async (_target, content) => { assert.equal(content.text, 'same edit'); return message('1') }, async () => message())
    assert.equal(editor.outcomeUnknown, false); assert.equal(editor.sendingDraft, 'send draft')
})

test('discard requires confirmation only for a changed draft, and whitespace cannot be saved', () => {
    const editor = new MessageEditor(); editor.begin(message(), 'send draft')
    assert.equal(editor.canSave, false); editor.draft = 'changed'
    assert.equal(editor.leave(() => false), false); assert.equal(editor.draft, 'changed')
    editor.draft = '  '; assert.equal(editor.canSave, false)
    assert.equal(editor.leave(() => true), true); assert.equal(editor.sendingDraft, 'send draft')
})

test('HTTP content responses retain conflict codes and require a valid epoch', () => {
    assert.throws(() => contentResponse({ status: 409, body: { code: 'version_conflict' } }), { code: 'version_conflict', status: 409 })
    assert.throws(() => contentResponse({ status: 200, body: {} }), { code: 'content_epoch_missing' })
    assert.throws(() => contentResponse({ status: 200, body: {}, contentEpoch: '01' }), { code: 'invalid_response' })
    assert.equal(contentResponse({ status: 200, body: {}, contentEpoch: '0' }).contentEpoch, '0')
})

test('conversation directory keeps all pages and explicit deletions without mixing epochs', async () => {
    const cursors: string[] = []
    const convert = (row: any) => Object.assign(new Conversation(), { channel: new Channel(row.channel_id, row.channel_type) })
    const result = await collectConversations(async cursor => {
        cursors.push(cursor)
        return { contentEpoch: '7', data: cursor ? {
            conversations: [{ channel_id: 'second', channel_type: 2 }], deletes: [{ channel_id: 'first', channel_type: 2 }], done: true,
        } : { conversations: [{ channel_id: 'first', channel_type: 2 }], next_cursor: 'page2', done: false } }
    }, convert)
    assert.deepEqual(cursors, ['', 'page2']); assert.equal(result.data[0].channel.channelID, 'second')
    assert.equal(result.removedChannels?.[0].channelID, 'first')
    await assert.rejects(collectConversations(async cursor => ({ contentEpoch: cursor ? '8' : '7', data: {
        conversations: [], next_cursor: 'page2', done: !!cursor,
    } }), convert), { code: 'content_epoch_conflict' })
    await assert.rejects(collectConversations(async () => ({ contentEpoch: '7', data: {
        conversations: [], next_cursor: 'loop', done: false,
    } }), convert), { code: 'cursor_not_advancing' })
})

test('Demo conversion retains edit identity/version, exclusion headers and stream snapshot', () => {
    const row = { message_idstr: '18446744073709551614', message_seq: 10, from_uid: 'alice',
        channel_id: 'bob', channel_type: 1, version: '12345678901234567890', updated_at_ms: 123,
        header: { no_persist: 1, sync_once: 1 }, payload: Buffer.from('{"type":1,"content":"text"}').toString('base64'),
        event_meta: { events: [{ event_key: 'main', snapshot: { kind: 'text', text: 'stream text' } }] } }
    const m = Convert.toMessage(row)
    assert.equal(m.messageID, row.message_idstr); assert.equal(m.contentVersion, row.version)
    assert.equal(m.updatedAtMs, 123); assert.equal(m.header.noPersist, true); assert.equal(m.header.syncOnce, true)
    assert.equal(m.streamText, 'stream text')
})
