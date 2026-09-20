import assert from 'node:assert/strict'
import test from 'node:test'
import { createRenderer, h, nextTick, shallowRef } from 'vue'
import { Message, MessageText } from 'wukongimjssdk'
import { updateStreamMessage } from '../src/services/streamMessage.ts'

test('stream events refresh child text in a shallow message list and preserve SDK identity fields', async () => {
    let renderedText = ''
    const renderer = createRenderer<any, any>({
        createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
        insert() {}, remove() {}, setText() {}, patchProp() {},
        parentNode: () => null, nextSibling: () => null,
        setElementText(_node, text) { renderedText = text },
    })
    const original = Object.assign(new Message(), { messageID: '123', clientMsgNo: 'stream', content: new MessageText('start') })
    const messages = shallowRef([original])
    const Child = { props: ['message'], setup: (props: any) => () => h('span', props.message.content.text) }
    const app = renderer.createApp({ setup: () => () => h(Child, { message: messages.value[0] }) })
    app.mount({})
    assert.equal(renderedText, 'start')
    const events = [
        ['stream.delta', { kind: 'text', delta: 'first' }, 'first'],
        ['stream.delta', { kind: 'text', delta: ' second' }, 'first second'],
        ['stream.close', { snapshot: { kind: 'text', text: 'closed' } }, 'closed'],
        ['stream.error', { snapshot: { kind: 'text', text: 'error' } }, 'error'],
        ['stream.cancel', { snapshot: { kind: 'text', text: 'cancelled' } }, 'cancelled'],
    ] as const
    try {
        for (const [type, payload, expected] of events) {
            const before = messages.value[0]
            const updated = updateStreamMessage(before, type, payload, text => text)
            messages.value = [updated]
            await nextTick()
            assert.equal(renderedText, expected)
            assert.notEqual(updated, before)
            assert(updated instanceof Message)
            assert.equal(updated.messageID, '123')
            assert.equal(updated.clientMsgNo, 'stream')
        }
        const before = messages.value[0]
        const finished = updateStreamMessage(before, 'stream.finish', {}, text => text)
        assert.equal((finished as any).completed, true)
        assert.notEqual(finished, before)
        assert.equal(original.content.text, 'start')
        assert.equal(updateStreamMessage(finished, 'unrelated', {}, text => text), finished)
    } finally { app.unmount() }
})
