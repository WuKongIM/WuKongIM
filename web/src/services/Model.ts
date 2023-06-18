import { formatDate2 } from "./Utils"

export class Varz {
    uptime: string = ''  // 上线时间
    inMsgs: number = 0 // 流入消息数量
    inBytes: number = 0 // 流入字节数量
    outMsgs: number = 0 // 流出消息数量
    outBytes: number = 0 // 流出字节数量
    cpu: number = 0  // cpu
    mem: number = 0 // 内存
    slowClients = 0 // 慢客户端数量
    conns!: ConnInfo[] // 连接
}

export class ConnInfo {
    id: number = 0  // 连接ID
    uid!: string  // 用户uid
    ip!: string   // 客户端IP
    port!: number // 客户端端口
    uptime!: string //  启动时间
    idle!: string // 客户端闲置时间
    pendingBytes!: number // 等待发送的字节数
    inMsgs!: number  // 流入的消息数
    outMsgs!: number // 流出的消息数量
    inBytes!: number // 流入的字节数量
    outBytes!: number //  流出的字节数量
    device!: string // 设备
    deviceID!: string //  设备ID
    version!: number  // 客户端协议版本

}

export const newConnInfo = (connObj: any) => {
    const connInfo = new ConnInfo()
    connInfo.id = connObj.id
    connInfo.uid = connObj.uid
    connInfo.ip = connObj.ip
    connInfo.port = connObj.port
    connInfo.uptime = connObj.uptime
    connInfo.idle = connObj.idle
    connInfo.pendingBytes = connObj.pending_bytes
    connInfo.inMsgs = connObj.in_msgs
    connInfo.inBytes = connObj.in_bytes
    connInfo.outMsgs = connObj.out_msgs
    connInfo.outBytes = connObj.out_bytes
    connInfo.device = connObj.device
    connInfo.deviceID = connObj.device_id
    connInfo.version = connObj.version
    return connInfo
}


export class ChannelInfo {
    channelID: string = '' // 频道ID
    channelType: number = 0 // 频道类型
    ban: boolean = false // 是否被封
    large: boolean = false // 是否是超大群
    subscribers: string[] = [] // 订阅者
    allowList: string[] = [] // 白名单
    denyList: string[] = [] // 黑名单
    lastMsgSeq: number = 0 // 最后一条消息的序列号
    slotNum: number = 0 // 槽位号
}

export const newChannelInfo = (connInfoObj: any) => {
    const c = new ChannelInfo()
    c.channelID = connInfoObj.channel_id
    c.channelType = connInfoObj.channel_type
    c.ban = connInfoObj.ban
    c.large = connInfoObj.large
    c.subscribers = connInfoObj.subscribers
    c.allowList = connInfoObj.allow_list
    c.denyList = connInfoObj.deny_list
    c.lastMsgSeq = connInfoObj.last_msg_seq
    c.slotNum = connInfoObj.slot_num
    return c
}

export class MessagePage {
    data: Message[] = []
    limit: number = 0
    maxMessageSeq: number = 0
    startMessageSeq: number = 0
}

export const newMessagePage = (messagePageObj: any) => {
    const m = new MessagePage()
    m.limit = messagePageObj.limit
    m.maxMessageSeq = messagePageObj.max_message_seq
    m.startMessageSeq = messagePageObj.start_message_seq
    m.data = messagePageObj.data.map((messageObj: any) => newMessage(messageObj))
    return m
}

export class Message {
    header: MessageHeader = new MessageHeader()
    setting: number = 0
    messageID!: string
    clientMsgNo!: string
    messageSeq: number = 0
    fromUID!: string
    channelID!: string
    channelType: number = 0
    topic: string = ""
    timestamp: string = '' // 服务器消息时间
    payload!: string // 消息内容

}

export const newMessage = (messageObj: any) => {
    const m = new Message()
    m.header = newMessageHeader(messageObj.header)
    m.setting = messageObj.setting
    m.messageID = messageObj.message_idstr
    m.clientMsgNo = messageObj.client_msg_no
    m.messageSeq = messageObj.message_seq
    m.fromUID = messageObj.from_uid
    m.channelID = messageObj.channel_id
    m.channelType = messageObj.channel_type
    m.topic = messageObj.topic
    const date = new Date(messageObj.timestamp * 1000)

    m.timestamp = formatDate2(date)
    m.payload = messageObj.payload
    return m
}

export class MessageHeader {
    noPersist: number = 0
    redDot: number = 0
    syncOnce: number = 0
}

const newMessageHeader = (headerObj: any) => {
    const header = new MessageHeader()
    header.noPersist = headerObj.no_persist
    header.redDot = headerObj.red_dot
    header.syncOnce = headerObj.sync_once
    return header
}

export class Connz {
    connections: ConnInfo[] = []
    total: number = 0
    offset: number = 0
    limit: number = 0
}

export const newConnz = (connzObj: any) => {
    const connz = new Connz()
    connz.connections = connzObj.connections.map((connObj: any) => newConnInfo(connObj))
    connz.total = connzObj.total
    connz.offset = connzObj.offset
    connz.limit = connzObj.limit
    return connz
}

export class Conversation {
    channelID!: string
    channelType!:number
    Unread!:number
    timestamp!:string
    lastMsgSeq!:number
    lastClientMsgNo!:string
    offsetMsgSeq!:number
    version!:number
}

const newConversation = (conversationObj: any) => {
    const conversation = new Conversation()
    conversation.channelID = conversationObj.channel_id
    conversation.channelType = conversationObj.channel_type
    conversation.Unread = conversationObj.unread
    conversation.timestamp = formatDate2(new Date(conversationObj.timestamp*1000))
    conversation.lastMsgSeq = conversationObj.last_msg_seq
    conversation.lastClientMsgNo = conversationObj.last_client_msg_no
    conversation.offsetMsgSeq = conversationObj.offset_msg_seq
    conversation.version = conversationObj.version
    return conversation
}

export class ConversationPage {
    on:boolean = false
    conversations: Conversation[] = []
}

export const newConversationPage = (conversationPageObj: any) => {
    const conversationPage = new ConversationPage()
    conversationPage.on = conversationPageObj.on
    conversationPage.conversations = conversationPageObj.conversations.map((conversationObj: any) => newConversation(conversationObj))
    return conversationPage
}

export const channelTypeToString = (channelType: number) => {
    if(channelType == 1) {
        return "个人频道"
    }
    if(channelType == 2) {
        return "群组频道"
    }
    if(channelType == 3) {
        return "客服频道"
    }
    if (channelType == 4) {
        return "社区频道"
    }
    if(channelType == 5) {
        return "社区话题频道"
    }
    if(channelType == 6) {
        return "资讯频道"
    }
    return `${channelType}`
}