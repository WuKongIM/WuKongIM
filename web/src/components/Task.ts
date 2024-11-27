

export class ChannelItem {
    count: number = 0 // 创建数量
    type: number = 2 // 频道类型
    subscriberCount: number = 0 // 订阅者数量
    subscriberOnlineCount: number = 0  // 订阅者在线数量
    msgRate: number = 0 // 消息速率（条/分钟）
}

export class P2pItem {
    count: number = 0 // 单聊数量
    msgRate: number = 0 // 消息速率（条/分钟）
}

export class Config {
    onlineCount:number = 0
    channels:ChannelItem[] = []
    p2pItem:P2pItem = new P2pItem()
}