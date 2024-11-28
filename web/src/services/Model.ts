import { ChannelItem, Config, P2pItem } from "../components/Task";

export class Series {
    name?: string;
    type?: string = "line";
    data: Point[] = [];
}

export class Point {
    timestamp!: number
    value!: number
}

export const setSeries = (field: string, data: any, results: Array<Series>) => {
    const result = data[field]
    if (result && result.length > 0) {
        for (let index = 0; index < result.length; index++) {
            const label = result[index];
            var exist: any
            for (let index = 0; index < results.length; index++) {
                const element = results[index];
                if (element.name == label.label) {
                    exist = element
                    break
                }
            }
            if (exist) {
                exist.data.push({ timestamp: data.timestamp, value: label.value })
            } else {
                results.push({ name: label.label, data: [{ timestamp: data.timestamp, value: label.value }] })
            }
        }
    }
}


// 初获取默认配置
export const taskToConfig = (task: any) => {

    const channelItems = []

    const cfg = new Config()
    cfg.onlineCount = task.online

    if (task.channels && task.channels.length > 0) {
        for (const c of task.channels) {
            const item = new ChannelItem()
            item.count = c.count
            item.subscriberCount = c.subscriber.count
            item.subscriberOnlineCount = c.subscriber.online
            item.msgRate = c.msg_rate
            channelItems.push(item)
        }
    }
    cfg.channels = channelItems

    if (task.p2p) {
        const p2pItem = new P2pItem()
        p2pItem.count = task.p2p.count
        p2pItem.msgRate = task.p2p.msg_rate
        cfg.p2pItem = p2pItem
    }

    return cfg

}