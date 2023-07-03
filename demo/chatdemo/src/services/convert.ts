import { Setting } from "wukongimjssdk/lib/proto";
import { WKSDK, Message, StreamItem, Channel, ChannelTypePerson, ChannelTypeGroup, MessageStatus, SyncOptions, MessageExtra, MessageContent } from "wukongimjssdk/lib/sdk";
import BigNumber from "bignumber.js";
import { Buffer } from 'buffer';
export class Convert {
    static toMessage(msgMap: any): Message {
        const message = new Message();
        if (msgMap['message_idstr']) {
            message.messageID = msgMap['message_idstr'];
        } else {
            message.messageID = new BigNumber(msgMap['message_id']).toString();
        }
        if (msgMap["header"]) {
            message.header.reddot = msgMap["header"]["red_dot"] === 1 ? true : false
        }
        if (msgMap["setting"]) {
            message.setting = Setting.fromUint8(msgMap["setting"])
        }
        if (msgMap["revoke"]) {
            message.remoteExtra.revoke = msgMap["revoke"] === 1 ? true : false
        }
        if(msgMap["message_extra"]) {
            const messageExtra = msgMap["message_extra"]
           message.remoteExtra = this.toMessageExtra(messageExtra)
        }
        
        message.clientSeq = msgMap["client_seq"]
        message.channel = new Channel(msgMap['channel_id'], msgMap['channel_type']);
        message.messageSeq = msgMap["message_seq"]
        message.clientMsgNo = msgMap["client_msg_no"]
        message.streamNo = msgMap["stream_no"]
        message.streamFlag = msgMap["stream_flag"]
        message.fromUID = msgMap["from_uid"]
        message.timestamp = msgMap["timestamp"]
        message.status = MessageStatus.Normal
       
        const decodedBuffer = Buffer.from(msgMap["payload"], 'base64')
        const contentObj = JSON.parse(decodedBuffer.toString('utf8'))
        let contentType = 0
        if (contentObj) {
            contentType = contentObj.type
        }
        const messageContent = WKSDK.shared().getMessageContent(contentType)
        if (contentObj) {
            messageContent.decode(this.stringToUint8Array(JSON.stringify(contentObj)))
        }
        message.content = messageContent

        message.isDeleted = msgMap["is_deleted"] === 1

        const streamMaps = msgMap["streams"]
        if(streamMaps && streamMaps.length>0) {
            const streams = new Array<StreamItem>()
            for (const streamMap of streamMaps) {
                const streamItem = new StreamItem()
                streamItem.clientMsgNo = streamMap["client_msg_no"]
                streamItem.streamSeq = streamMap["stream_seq"]
                if(streamMap["blob"] && streamMap["blob"].length>0) {
                    const blob = Buffer.from(streamMap["blob"], 'base64')
                    const blobObj = JSON.parse(blob.toString('utf8'))
                    const blobType = blobObj.type
                    const blobContent = WKSDK.shared().getMessageContent(contentType)
                    if (blobObj) {
                        blobContent.decode(this.stringToUint8Array(JSON.stringify(blobObj)))
                    }
                    streamItem.clientMsgNo = streamMap["client_msg_no"]
                    streamItem.streamSeq = streamMap["stream_seq"]
                    streamItem.content = blobContent
                }
                streams.push(streamItem)
            }
            message.streams = streams
        }

        return message
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