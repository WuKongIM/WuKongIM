import { Conversation, messageFromHTTP, WKSDK, Message, Channel, MessageExtra } from "wukongimjssdk";
import BigNumber from "bignumber.js";
import { Buffer } from 'buffer';
export class Convert {
    static toMessage(msgMap: any): Message {
        // Keep SDK identity/version validation and the Demo's stream/extra fields together.
        const message = messageFromHTTP(msgMap);
        message.clientSeq = msgMap.client_seq;
        if (msgMap.revoke) message.remoteExtra.revoke = msgMap.revoke === 1;
        if (msgMap.message_extra) message.remoteExtra = this.toMessageExtra(msgMap.message_extra);

        // 解析 event_meta（新协议）
        const eventMeta = msgMap["event_meta"]
        if (eventMeta) {
            (message as any).eventMeta = eventMeta
            if (eventMeta.completed) {
                (message as any).completed = true
            }
        }

        // 从 event_meta snapshot 或 legacy stream_data 中提取流文本
        let streamTextResolved = false
        if (eventMeta && eventMeta.events && eventMeta.events.length > 0) {
            for (const ek of eventMeta.events) {
                if (ek.event_key === "main" || eventMeta.events.length === 1) {
                    const snapshot = ek.snapshot
                    if (snapshot && snapshot.kind === "text" && snapshot.text) {
                        message.streamText = snapshot.text
                        streamTextResolved = true
                        break
                    }
                }
            }
        }
        if (!streamTextResolved) {
            const streamBase64Data = msgMap["stream_data"]
            if (streamBase64Data) {
                const streamText = Buffer.from(streamBase64Data, 'base64')
                message.streamText = streamText.toString('utf8')
            }
        }
       
        message.isDeleted = msgMap["is_deleted"] === 1

        return message
    }

    static toConversation(conversationMap: any): Conversation {
        const conversation = new Conversation()
        conversation.channel = new Channel(conversationMap['channel_id'], conversationMap['channel_type'])
        conversation.unread = conversationMap['unread'] || 0;
        const lastMessage = conversationMap["last_message"];
        if (lastMessage) {
            const timestamp = Math.floor((lastMessage["server_timestamp_ms"] || 0) / 1000)
            const messageModel = this.toMessage({
                ...lastMessage,
                channel_id: conversationMap['channel_id'],
                channel_type: conversationMap['channel_type'],
                timestamp,
            });
            conversation.lastMessage = messageModel
            conversation.timestamp = timestamp
        } else {
            conversation.timestamp = Math.floor((conversationMap['active_at'] || 0) / 1_000_000_000)
        }
        conversation.extra = {}

        return conversation
    }

    static toMessageExtra(msgExtraMap: any) :MessageExtra {
        const messageExtra = new MessageExtra()
        if (msgExtraMap['message_id_str']) {
            messageExtra.messageID = msgExtraMap['message_id_str'];
        } else {
            messageExtra.messageID = new BigNumber(msgExtraMap['message_id']).toString();
        }
        messageExtra.messageSeq = msgExtraMap["message_seq"]
        messageExtra.readed = msgExtraMap["readed"] === 1
        if(msgExtraMap["readed_at"] && msgExtraMap["readed_at"]>0) {
            messageExtra.readedAt = new Date(msgExtraMap["readed_at"] )
        }
        messageExtra.revoke = msgExtraMap["revoke"] === 1
        if(msgExtraMap["revoker"]) {
            messageExtra.revoker = msgExtraMap["revoker"]
        }
        messageExtra.readedCount = msgExtraMap["readed_count"] || 0
        messageExtra.unreadCount = msgExtraMap["unread_count"] || 0
        messageExtra.extraVersion = msgExtraMap["extra_version"] || 0
        messageExtra.editedAt = msgExtraMap["edited_at"] || 0

        const contentEditObj = msgExtraMap["content_edit"]
        if(contentEditObj) {
            const contentEditContentType = contentEditObj.type
            const contentEditContent = WKSDK.shared().getMessageContent(contentEditContentType)
            const contentEditPayloadData = this.stringToUint8Array(JSON.stringify(contentEditObj))
            contentEditContent.decode(contentEditPayloadData)
            messageExtra.contentEditData = contentEditPayloadData
            messageExtra.contentEdit = contentEditContent

            messageExtra.isEdit = true
        }

        return messageExtra
    }
    static stringToUint8Array(str: string): Uint8Array {
        const newStr = unescape(encodeURIComponent(str))
        var arr = [];
        for (var i = 0, j = newStr.length; i < j; ++i) {
            arr.push(newStr.charCodeAt(i));
        }
        var tmpUint8Array = new Uint8Array(arr);
        return tmpUint8Array
    }
   
}
