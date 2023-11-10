

<script setup lang="ts">
import { onMounted, onUnmounted, ref, nextTick } from 'vue';
import { CMDContent, Channel, ChannelInfo, ChannelTypePerson, ConnectStatus, ConnectStatusListener, Conversation, ConversationAction, Message, WKSDK } from 'wukongimjssdk';
import { ConversationWrap } from './ConversationWrap';
import APIClient, { CMDType } from '../../services/APIClient';

const conversationWraps = ref<ConversationWrap[]>() // 本地最近会话列表

const selectedChannel = ref<Channel>() // 选中的频道

const onSelectChannel = defineProps<{ onSelectChannel: (channel: Channel) => void }>()

// 监听连接状态
const connectStatusListener = async (status: ConnectStatus) => {
    console.log("connectStatusListener", status)
    if (status === ConnectStatus.Connected) {
        const remoteConversations = await WKSDK.shared().conversationManager.sync() // 同步最近会话列表
        if (remoteConversations && remoteConversations.length > 0) {
            conversationWraps.value = sortConversations(remoteConversations.map(conversation => new ConversationWrap(conversation)))
        }
    }
}

// 监听cmd消息  
const cmdListener = (msg: Message) => {
    console.log("收到CMD：", msg)
    const cmdContent = msg.content as CMDContent
    if (cmdContent.cmd === CMDType.CMDTypeClearUnread) {
        const clearChannel = new Channel(cmdContent.param.channelID, cmdContent.param.channelType)
        clearConversationUnread(clearChannel)
    }
}

// 监听最近会话列表的变化
const conversationListener = (conversation: Conversation, action: ConversationAction) => { // 监听最近会话列表的变化
    console.log("conversationListener", conversation, action)
    if (action === ConversationAction.add) {
        conversationWraps.value = [new ConversationWrap(conversation), ...(conversationWraps.value || [])]
    } else if (action === ConversationAction.update) {
        const index = conversationWraps.value?.findIndex(item => item.channel.channelID === conversation.channel.channelID && item.channel.channelType === conversation.channel.channelType)
        if (index !== undefined && index >= 0) {
            conversationWraps.value![index] = new ConversationWrap(conversation)
            conversationWraps.value = sortConversations()
        }
    } else if (action === ConversationAction.remove) {
        const index = conversationWraps.value?.findIndex(item => item.channel.channelID === conversation.channel.channelID && item.channel.channelType === conversation.channel.channelType)
        if (index && index >= 0) {
            conversationWraps.value?.splice(index, 1)
        }
    }
}

const channelInfoListener = (channelInfo: ChannelInfo) => {
    conversationWraps.value = [...conversationWraps.value || []] // 强制刷新
    // const index = conversationWraps.value?.findIndex(item => item.channel.channelID === channelInfo.channel.channelID && item.channel.channelType === channelInfo.channel.channelType)
    // if (index !== undefined && index >= 0) {
    //     conversationWraps.value![index].channelInfo = channelInfo
    // }
}

const clearConversationUnread = (channel: Channel) => {
    const conversation = WKSDK.shared().conversationManager.findConversation(channel)
    if (conversation) {
        conversation.unread = 0
        WKSDK.shared().conversationManager.notifyConversationListeners(conversation, ConversationAction.update)
    }
}


onMounted(async () => {

    WKSDK.shared().connectManager.addConnectStatusListener(connectStatusListener) // 监听连接状态
    WKSDK.shared().conversationManager.addConversationListener(conversationListener) // 监听最近会话列表的变化
    WKSDK.shared().chatManager.addCMDListener(cmdListener) // 监听cmd消息
    WKSDK.shared().channelManager.addListener(channelInfoListener) // 监听频道信息变化
})

onUnmounted(() => {
    WKSDK.shared().conversationManager.removeConversationListener(conversationListener)
    WKSDK.shared().connectManager.removeConnectStatusListener(connectStatusListener)
    WKSDK.shared().chatManager.removeCMDListener(cmdListener)
    WKSDK.shared().channelManager.removeListener(channelInfoListener)
})

// 排序最近会话列表
const sortConversations = (conversations?: Array<ConversationWrap>) => {
    let newConversations = conversations;
    if (!newConversations) {
        newConversations = conversationWraps.value
    }
    if (!newConversations || newConversations.length <= 0) {
        return [];
    }
    let sortAfter = newConversations.sort((a, b) => {
        let aScore = a.timestamp;
        let bScore = b.timestamp;
        if (a.extra?.top === 1) {
            aScore += 1000000000000;
        }
        if (b.extra?.top === 1) {
            bScore += 1000000000000;
        }
        return bScore - aScore;
    });
    return sortAfter
}

const onSelectChannelClick = (channel: Channel) => {
    selectedChannel.value = channel
    if (onSelectChannel) {
        onSelectChannel.onSelectChannel(channel)
    }
    APIClient.shared.clearUnread(channel)
    clearConversationUnread(channel)
}

const getConversationItemCss = (conversationWrap: ConversationWrap) => {
    if (!selectedChannel.value) {
        return 'conversation-item'
    }
    if (selectedChannel.value.isEqual(conversationWrap.channel)) {
        return 'conversation-item selected'
    }
    return 'conversation-item'
}

const fetchChannelInfoIfNeed = (channel: Channel) => {
    const channelInfo = WKSDK.shared().channelManager.getChannelInfo(channel)
    if (!channelInfo) {
        WKSDK.shared().channelManager.fetchChannelInfo(channel)
    }

}

</script>


<template>
    <div class="conversations">
        <div :class="getConversationItemCss(conversationWrap)" v-for="conversationWrap in conversationWraps" :onClick="() => {
            onSelectChannelClick(conversationWrap.channel)
        }">
            {{ fetchChannelInfoIfNeed(conversationWrap.channel) }}
            <div class="item-content">
                <div class="left">
                    <div class="avatar" style="width: 48px;height: 48px;"
                        v-if="conversationWrap.channel.channelType === ChannelTypePerson">
                        <img :src="conversationWrap.channelInfo?.logo" style="width: 48px;height: 48px;" />
                    </div>
                    <div class="avatar" style="width: 48px;height: 48px;" v-else>
                        {{ conversationWrap.channelInfo?.title }}
                    </div>
                </div>
                <div class="right">
                    <div class="right-item1">
                        <div class="title">
                            {{ conversationWrap.channel.channelID }}
                        </div>
                        <div class="time">
                            {{ conversationWrap.timestampString }}
                        </div>
                    </div>
                    <div class="right-item2">
                        <div class="last-msg">
                            {{ conversationWrap.lastMessage?.content.conversationDigest }}
                        </div>
                        <div v-if="conversationWrap.unread > 0" className="reddot">
                            {{ conversationWrap.unread }}
                        </div>
                    </div>
                </div>
            </div>
        </div>
    </div>
</template>

<style scoped>
.conversations {
    width: 100%;
    height: 100%;
    overflow-y: auto;
}

.item-content {
    display: flex;
    flex-direction: row;
    align-items: center;
    width: 100%;
    text-align: left;
}

.conversation-item {
    display: flex;
    height: 80px;
    width: 100%;
    background-color: white;
    cursor: pointer;
    overflow: hidden;
}

.left {
    height: 100%;
    display: flex;
    align-items: center;
    margin-left: 10px;
}

.right {
    margin-left: 10px;
    height: 100%;
    width: calc(300px - 100px);
    display: flex;
    flex-direction: column;
    justify-content: center;
}

.right-item1 {
    display: flex;
    flex-direction: row;
    align-items: center;
    justify-content: space-between;
    width: 100%;

}


.right-item2 {
    display: flex;
    flex-direction: row;
    align-items: center;
    justify-content: space-between;
    width: 100%;
}

.reddot {
    width: 20px;
    height: 20px;
    border-radius: 10px;
    background-color: rgb(228, 98, 64);
    color: white;
    font-size: 12px;
    text-align: center;
    line-height: 20px;
    margin-right: 10px;
}



.right-item1 .title {
    font-size: 16px;
    font-weight: bold;
    max-width: 100px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    display: block;
}

.last-msg {
    font-size: 14px;
    color: #999999;
    margin-top: 4px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    display: block;

}

.time {
    font-size: 12px;
    color: #999999;
    margin-right: 10px;
}

.selected {
    background-color: #eee;
}

.avatar {
    display: flex;
    align-items: center;
    justify-content: center;
    border-radius: 50%;
    background-color: rgb(228, 98, 64);
    color: white;
    font-size: 20px;
    text-align: center;
}
</style>