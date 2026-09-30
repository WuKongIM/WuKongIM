<script setup lang="ts">
import { onMounted, onUnmounted, ref, shallowRef } from 'vue'
import { CMDContent, Channel, ConnectStatus, Conversation, ConversationAction, Message, WKSDK } from 'wukongimjssdk'
import { ConversationWrap } from './ConversationWrap'
import APIClient, { CMDType } from '../../services/APIClient'
import { t } from '../../i18n'
import { avatarURLForUID } from '../../services/avatar'

const props = defineProps<{ onSelectChannel: (channel: Channel) => boolean, selectedChannel: Channel }>()
const conversationWraps = shallowRef<ConversationWrap[]>([])
const loading = ref(false)
const syncFailed = ref(false)
let disposed = false
let syncGeneration = 0
const sortConversations = (items: ConversationWrap[]) => [...items].sort((a, b) =>
    (b.timestamp + (b.extra?.top === 1 ? 1e12 : 0)) - (a.timestamp + (a.extra?.top === 1 ? 1e12 : 0)))
// Fetch metadata when the directory changes, never as a template render side effect.
const wrapConversation = (conversation: Conversation) => {
    const manager = WKSDK.shared().channelManager
    if (!manager.getChannelInfo(conversation.channel)) void manager.fetchChannelInfo(conversation.channel).catch(() => {})
    return new ConversationWrap(conversation)
}
const syncChats = async () => {
    const generation = ++syncGeneration
    loading.value = true
    syncFailed.value = false
    try {
        const remote = await WKSDK.shared().conversationManager.sync()
        if (disposed || generation !== syncGeneration) return
        conversationWraps.value = sortConversations(remote.map(wrapConversation))
    } catch {
        if (!disposed && generation === syncGeneration) syncFailed.value = true
    } finally {
        if (!disposed && generation === syncGeneration) loading.value = false
    }
}
const connectStatusListener = (status: ConnectStatus) => {
    if (status === ConnectStatus.Connected) void syncChats()
}
const clearConversationUnread = (channel: Channel) => {
    const conversation = WKSDK.shared().conversationManager.findConversation(channel)
    if (conversation) {
        conversation.unread = 0
        WKSDK.shared().conversationManager.notifyConversationListeners(conversation, ConversationAction.update)
    }
}
const cmdListener = (message: Message) => {
    const content = message.content as CMDContent
    if (content.cmd === CMDType.CMDTypeClearUnread) clearConversationUnread(new Channel(content.param.channelID, content.param.channelType))
}
const conversationListener = (conversation: Conversation, action: ConversationAction) => {
    const items = conversationWraps.value.filter(item => !item.channel.isEqual(conversation.channel))
    if (action !== ConversationAction.remove) items.push(wrapConversation(conversation))
    conversationWraps.value = sortConversations(items)
}
const channelInfoListener = () => { conversationWraps.value = [...conversationWraps.value] }
const selectChannel = (channel: Channel) => {
    if (!props.onSelectChannel(channel)) return
    void APIClient.shared.clearUnread(channel)
    clearConversationUnread(channel)
}
onMounted(() => {
    WKSDK.shared().connectManager.addConnectStatusListener(connectStatusListener)
    WKSDK.shared().conversationManager.addConversationListener(conversationListener)
    WKSDK.shared().chatManager.addCMDListener(cmdListener)
    WKSDK.shared().channelManager.addListener(channelInfoListener)
})
onUnmounted(() => {
    disposed = true
    syncGeneration++
    WKSDK.shared().connectManager.removeConnectStatusListener(connectStatusListener)
    WKSDK.shared().conversationManager.removeConversationListener(conversationListener)
    WKSDK.shared().chatManager.removeCMDListener(cmdListener)
    WKSDK.shared().channelManager.removeListener(channelInfoListener)
})
</script>

<template>
    <div class="conversations" :aria-busy="loading">
        <div v-if="syncFailed" class="directory-notice" role="alert"><p>{{ t('chatsSyncFailed') }}</p><button @click="syncChats">{{ t('retryEdit') }}</button></div>
        <div v-else-if="!conversationWraps.length" class="directory-notice"><p>{{ t(loading ? 'loadingMessages' : 'emptyChats') }}</p><small v-if="!loading">{{ t('emptyChatsHint') }}</small></div>
        <button v-for="item in conversationWraps" :key="`${item.channel.channelType}:${item.channel.channelID}`" class="conversation-item" :class="{ selected: selectedChannel.isEqual(item.channel) }" :aria-current="selectedChannel.isEqual(item.channel) ? 'true' : undefined" @click="selectChannel(item.channel)">
            <img class="avatar" :src="item.channelInfo?.logo || avatarURLForUID(item.channel.channelID)" alt="" />
            <div class="item-content"><div class="right-item1"><span class="title">{{ item.channel.channelID }}</span><time>{{ item.timestampString }}</time></div><div class="right-item2"><span class="last-msg">{{ item.conversationDigest || t('firstMessageHint') }}</span><span v-if="item.unread > 0" class="reddot">{{ item.unread > 99 ? '99+' : item.unread }}</span></div></div>
        </button>
    </div>
</template>

<style scoped>
.conversations { flex: 1; overflow-y: auto; min-height: 0; padding: 0 10px 12px; }
.conversation-item { display: flex; align-items: center; width: 100%; gap: 12px; padding: 14px 10px; border: 0; background: transparent; border-radius: 12px; text-align: left; margin: 3px 0; }
.conversation-item:hover { background: var(--hover); }
.conversation-item.selected { background: var(--accent-soft); }
.avatar { width: 42px; height: 42px; border-radius: 13px; flex-shrink: 0; }
.item-content { flex: 1; min-width: 0; }
.right-item1, .right-item2 { display: flex; align-items: center; gap: 8px; }
.right-item1 { margin-bottom: 5px; }
.title { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; font-size: 14px; }
time { font-size: 10px; color: var(--muted); flex-shrink: 0; }
.last-msg { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 12px; color: var(--muted); }
.reddot { min-width: 18px; height: 18px; padding: 0 5px; border-radius: 10px; background: var(--accent); color: white; text-align: center; line-height: 18px; font-size: 10px; }
.directory-notice { text-align: center; margin: 30px 12px; color: var(--muted); font-size: 13px; }
.directory-notice small { font-size: 12px; }
@media (max-width: 700px) { .conversations { padding-top: 14px; } }
</style>
