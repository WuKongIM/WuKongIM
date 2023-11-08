

<script setup lang="ts">
import { onMounted, onUnmounted, ref,defineProps } from 'vue';
import { Channel, ChannelTypePerson, Conversation, ConversationAction, WKSDK } from 'wukongimjssdk';
import { ConversationWrap } from './ConversationWrap';

const conversationWraps = ref<ConversationWrap[]>() // 本地最近会话列表

const selectedChannel = ref<Channel>() // 选中的会话

const onSelectChannel = defineProps<{ onSelectChannel: (channel: Channel) => void }>()

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


onMounted(async () => {
    const remoteConversations = await WKSDK.shared().conversationManager.sync() // 同步最近会话列表
    if (remoteConversations && remoteConversations.length > 0) {
        conversationWraps.value = sortConversations(remoteConversations.map(conversation => new ConversationWrap(conversation)))
    }

    WKSDK.shared().conversationManager.addConversationListener(conversationListener)
})

onUnmounted(() => {
    WKSDK.shared().conversationManager.removeConversationListener(conversationListener)
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
    if(onSelectChannel) {
        onSelectChannel.onSelectChannel(channel)
    }
}

const getConversationItemCss = (conversationWrap: ConversationWrap) => {
    if(!selectedChannel.value) {
        return 'conversation-item'
    }
    if(selectedChannel.value.isEqual(conversationWrap.channel)) {
        return 'conversation-item selected'
    }
    return 'conversation-item'
}

</script>


<template>
    <div class="conversations">
        <div :class="getConversationItemCss(conversationWrap)" v-for="conversationWrap in conversationWraps" :onClick="()=>{
             onSelectChannelClick(conversationWrap.channel)
        }">
            <div class="item-content">
                <div class="left">
                    <div class="avatar" style="width: 48px;height: 48px;" v-if="conversationWrap.channel.channelType === ChannelTypePerson">
                        <img :src="conversationWrap.avatar" style="width: 48px;height: 48px;" />
                    </div>
                    <div class="avatar" style="width: 48px;height: 48px;" v-else>
                        {{conversationWrap.channel.channelID.substring(0,1).toUpperCase()}}
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
    width: 100%;
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

.last-msg {
    margin-top: 4px;
}

.title {
    font-size: 16px;
    font-weight: bold;
}

.last-msg {
    font-size: 14px;
    color: #999999;
}

.time {
    font-size: 12px;
    color: #999999;
    margin-right: 10px;
}

.selected {
    background-color: rgba(245, 245, 245);
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