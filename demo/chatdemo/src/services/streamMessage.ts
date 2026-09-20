import { Message, MessageText } from 'wukongimjssdk'

/** Replace the message identity so shallow lists notify their child renderers. */
export function updateStreamMessage(message: Message, type: string, payload: any, render: (text: string) => string): Message {
    const updated = Object.assign(new Message(), message)
    if (type === 'stream.delta' && payload?.kind === 'text' && payload.delta) {
        updated.streamText = (message.streamText || '') + payload.delta
        updated.content = new MessageText(render(updated.streamText))
    } else if (['stream.close', 'stream.error', 'stream.cancel'].includes(type) && payload?.snapshot?.kind === 'text' && payload.snapshot.text) {
        updated.streamText = payload.snapshot.text
        updated.content = new MessageText(render(payload.snapshot.text))
    } else if (type === 'stream.finish') {
        (updated as any).completed = true
    } else {
        return message
    }
    return updated
}
