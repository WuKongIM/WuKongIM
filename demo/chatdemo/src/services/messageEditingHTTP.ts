import axios from 'axios'
import { Channel, decimal, MessageUpdateError } from 'wukongimjssdk'
import type { ContentResponse, Conversation, MessageEditingHTTPResponse } from 'wukongimjssdk'

/** Demo-only direct product API adapter. Production applications authorize edits in their BFF. */
export function createEditingTransport(context: () => { apiURL: string, uid: string, token?: string }) {
    const client = axios.create({ timeout: 15_000, validateStatus: () => true })
    return async (path: string, body: any, signal?: AbortSignal): Promise<MessageEditingHTTPResponse> => {
        const session = context()
        try {
            const response = await client.post(path, { ...body, login_uid: session.uid, uid: session.uid }, {
                baseURL: session.apiURL, signal,
                headers: session.token ? { token: session.token } : undefined,
            })
            return { status: response.status, body: response.data, contentEpoch: response.headers['x-wk-content-epoch'] }
        } catch (error) {
            const code = axios.isCancel(error) ? 'cancelled'
                : axios.isAxiosError(error) && error.code === 'ECONNABORTED' ? 'timeout' : 'network_error'
            throw new MessageUpdateError(code)
        }
    }
}

export function contentResponse(response: MessageEditingHTTPResponse): ContentResponse<any> {
    if (response.status < 200 || response.status >= 300) {
        throw new MessageUpdateError(response.body?.code || 'http_error', response.body?.msg, response.status)
    }
    if (response.contentEpoch === undefined) throw new MessageUpdateError('content_epoch_missing')
    return { contentEpoch: decimal(response.contentEpoch), data: response.body }
}

/** Commit a whole directory only when every page belongs to the same restore generation. */
export async function collectConversations(
    read: (cursor: string) => Promise<ContentResponse<any>>,
    convert: (row: any) => Conversation,
): Promise<ContentResponse<Conversation[]>> {
    const conversations = new Map<string, Conversation>()
    const removed = new Map<string, Channel>()
    const cursors = new Set<string>()
    let cursor = '', epoch: string | undefined
    const key = (row: any) => JSON.stringify([row.channel_id, row.channel_type])
    for (;;) {
        const page = await read(cursor)
        const currentEpoch = decimal(page.contentEpoch)
        if (epoch !== undefined && currentEpoch !== epoch) throw new MessageUpdateError('content_epoch_conflict')
        epoch = currentEpoch
        if (!Array.isArray(page.data.conversations)) throw new MessageUpdateError('invalid_response')
        for (const row of page.data.conversations) {
            conversations.set(key(row), convert(row)); removed.delete(key(row))
        }
        for (const row of page.data.deletes || []) {
            conversations.delete(key(row)); removed.set(key(row), new Channel(row.channel_id, row.channel_type))
        }
        if (page.data.done === true) return { contentEpoch: epoch, data: [...conversations.values()], removedChannels: [...removed.values()] }
        const next = page.data.next_cursor
        if (typeof next !== 'string' || !next || next === cursor || cursors.has(next)) throw new MessageUpdateError('cursor_not_advancing')
        cursors.add(next); cursor = next
    }
}
