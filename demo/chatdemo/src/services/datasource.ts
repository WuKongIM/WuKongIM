import { Channel, SyncOptions, WKSDK } from "wukongimjssdk"
import APIClient from "./APIClient"



export function initDataSource() {

    // 同步消息的数据源
    WKSDK.shared().config.provider.syncMessagesCallback = async (channel: Channel, opts: SyncOptions) => {
        const resultMessages = await APIClient.shared.syncMessages(channel, opts)
        return resultMessages
    }

    WKSDK.shared().config.provider.syncConversationsCallback = async () => {
        const resultConversations = await APIClient.shared.syncConversations()
        return resultConversations
    }

}