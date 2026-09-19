import { formatConversationTime, t } from '../../i18n'
import { Conversation, Message, MessageContentType } from "wukongimjssdk"

export class ConversationWrap {
    conversation: Conversation
    constructor(conversation: Conversation) {
        this.conversation = conversation
    }

    avatarHashTag?: string
    // channel: Channel;
    // private _channelInfo;
    // unread: number;
    // timestamp: number;
    // lastMessage: Message;
    // isMentionMe: boolean;
    // constructor();
    // get channelInfo(): ChannelInfo;
    // isEqual(c: Conversation): boolean;


    public get channel() {
        return this.conversation.channel
    }

    public get channelInfo() {
        return this.conversation.channelInfo
    }
    public get unread() {
        return this.conversation.unread
    }
  
    public get timestamp() {
        return this.conversation.timestamp
    }
    public set timestamp(timestamp:number) {
        this.conversation.timestamp = timestamp
    }

    public get timestampString() {
        return formatConversationTime(this.timestamp * 1000)
    }

    public get lastMessage() {
        return this.conversation.lastMessage
    }
    public set lastMessage(lastMessage: Message | undefined) {
        this.conversation.lastMessage = lastMessage
        
    }

    public get isMentionMe()  {
        return this.conversation.isMentionMe
    }

    public get remoteExtra() {
        return this.conversation.remoteExtra
    }

    public set isMentionMe(isMentionMe:boolean | undefined) {
        this.conversation.isMentionMe = isMentionMe
    }

    public get reminders() {
        return this.conversation.reminders
    }

    public get simpleReminders() {
        return this.conversation.simpleReminders
    }

    public get conversationDigest() {
        if(!this.lastMessage) {
            return ""
        }
        if (this.lastMessage.contentStale) return t('staleMessage')
        // 尝试从流文本中获取摘要
        if (this.lastMessage.streamText) {
            const raw = this.lastMessage.streamText.trim()
            // spec/代码块内容 → 显示为卡片消息
            if (raw.startsWith('```')) {
                return t('cardDigest')
            }
            // 普通文本：去除HTML标签后截取摘要
            const text = raw.replace(/<[^>]*>/g, '').trim()
            if (text) {
                return text.length > 30 ? text.substring(0, 30) + '...' : text
            }
        }
        if(this.lastMessage.setting.streamOn) {
            return t('streamDigest')
        }
        const content = this.lastMessage.content
        // SDK-generated labels are localized by type, never by matching user text.
        if (content?.contentType === MessageContentType.image) {
            return t('imageDigest')
        }
        if (content?.contentType === MessageContentType.unknown) {
            return t('unknownMessageDigest')
        }
        const digest = content?.conversationDigest
        if (digest) {
            return digest
        }
        return t('messageDigest')
    }

    reloadIsMentionMe(): void {
        return this.conversation.reloadIsMentionMe()
    }

    public get extra() {
        if(!this.conversation.extra) {
            this.conversation.extra = {}
        }
        return this.conversation.extra
    }
   


    isEqual(c: ConversationWrap): boolean {
        return this.conversation.isEqual(c.conversation)
    }
}
