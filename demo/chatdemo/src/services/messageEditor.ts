import { Message, MessageContentType, MessageStatus, MessageText } from 'wukongimjssdk'

export function canEditMessage(message: Message, uid: string): boolean {
    return !!uid && message.fromUID === uid && message.status === MessageStatus.Normal
        && !!message.messageID && message.contentType === MessageContentType.text
        && !message.header.noPersist && !message.header.syncOnce && !message.setting.streamOn
        && !message.isDeleted && !message.remoteExtra?.revoke && !message.contentStale
}

export function sameMessage(a: Message, b: Message): boolean {
    return a.channel.isEqual(b.channel) && a.messageID === b.messageID
}

/** One visible edit keeps a fixed CAS snapshot and an independent sending draft. */
export class MessageEditor {
    target: Message | undefined
    draft = ''
    sendingDraft = ''
    originalText = ''
    saving = false
    outcomeUnknown = false
    needsReload = false
    errorCode = ''

    begin(message: Message, sendingDraft: string) {
        this.acceptSnapshot(message)
        this.draft = this.originalText
        this.sendingDraft = sendingDraft
        this.errorCode = ''
        this.outcomeUnknown = false
        this.needsReload = false
    }

    /** Recalibrate CAS and discard checks against the confirmed body without changing the draft. */
    private acceptSnapshot(message: Message) {
        this.target = Object.assign(new Message(), message)
        this.originalText = (message.content as MessageText).text || ''
    }

    get dirty() { return this.draft !== this.originalText }
    get canSave() { return !!this.target && !this.saving && !!this.draft.trim() && (this.dirty || this.outcomeUnknown || this.needsReload) }

    /** Unknown writes must resolve with the same SDK-owned request before switching drafts. */
    leave(confirmDiscard: () => boolean): boolean {
        if (this.saving || this.outcomeUnknown) return false
        if (this.target && this.dirty && !confirmDiscard()) return false
        this.target = undefined
        this.errorCode = ''
        return true
    }

    async save(
        update: (message: Message, content: MessageText) => Promise<Message>,
        reload: (message: Message) => Promise<Message>,
    ): Promise<boolean> {
        if (!this.canSave) return false
        this.saving = true
        const target = this.target!
        try {
            if (this.needsReload) {
                this.acceptSnapshot(await reload(target))
                this.needsReload = false
                this.errorCode = 'version_conflict'
                return false
            }
            await update(target, new MessageText(this.draft))
            this.target = undefined
            this.errorCode = ''
            this.outcomeUnknown = false
            return true
        } catch (error: any) {
            const code = error?.code || 'edit_failed'
            this.errorCode = code
            this.outcomeUnknown = ['network_error', 'timeout', 'outcome_unknown', 'invalid_response', 'content_epoch_missing', 'stale_response'].includes(code)
                || error?.status === 503 || error?.name === 'TypeError'
            if (code === 'version_conflict' || code === 'content_epoch_conflict') {
                this.needsReload = true
                try {
                    this.acceptSnapshot(await reload(target))
                    this.needsReload = false
                } catch { this.errorCode = 'edit_reload_failed' }
            }
            return false
        } finally { this.saving = false }
    }
}
