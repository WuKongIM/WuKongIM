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
