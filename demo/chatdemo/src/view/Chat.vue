<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, shallowRef, reactive, toRaw } from 'vue';
import APIClient from '../services/APIClient'
import { clearSession, loadSession } from '../services/session'
import { t } from '../i18n'
import { demoHomeURL } from '../../../shared/home'
import '../../../shared/home.css'
import { useRouter } from "vue-router";
import { WKSDK, Message, MessageText, Channel, ChannelTypePerson, ChannelTypeGroup, MessageStatus, PullMode, MessageContent, ConnectionInfo, SendPacket } from "wukongimjssdk";
import { ConnectStatus, ConnectStatusListener } from 'wukongimjssdk';
import { SendackPacket, Setting } from 'wukongimjssdk';
import { MessageListener, MessageStatusListener } from 'wukongimjssdk';
import Conversation from '../components/Conversation/index.vue'
import { CustomMessage } from '../customessage';
import MessageUI from '../messages/Message.vue';
import { demoLogoURL } from '../services/assets';
import { avatarURLForUID } from '../services/avatar';
import { enableMessageEditing } from '../services/datasource';
import { MessageEditor, canEditMessage, sameMessage } from '../services/messageEditor';
import type { MessageUpdateListener } from 'wukongimjssdk';

const router = useRouter();
const home = demoHomeURL(import.meta.env.DEV)
const chatRef = ref<HTMLElement | null>(null)
const showSettingPanel = ref(false)
const title = ref("")
const connectionState = ref('connecting')
const showTools = ref(false)
const toolsRef = ref<HTMLElement>()
const toolsButtonRef = ref<HTMLButtonElement>()
const channelInputRef = ref<HTMLInputElement>()
const showChat = ref(false)
const nearBottom = ref(true)
const unreadBelow = ref(0)
const submittingMessage = ref(false)
const openingChannel = ref(false)
const settingError = ref('')
// Keep the original SDK packet for rejected sends: retries must reuse clientMsgNo.
const retryPackets = new Map<number, SendPacket>()
const connected = computed(() => connectionState.value === 'connected')
const channelTitle = computed(() => (showChat.value && to.value.channelID) || t('chatList'))
const closeOverlays = (event: KeyboardEvent) => {
    if (event.key === 'Tab' && showSettingPanel.value) {
        const elements = channelInputRef.value?.closest('form')?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled)')
        if (elements?.length) {
            const first = elements[0], last = elements[elements.length - 1]
            if (!Array.from(elements).includes(document.activeElement as HTMLElement) ||
                (event.shiftKey && document.activeElement === first) || (!event.shiftKey && document.activeElement === last)) {
                event.preventDefault()
                ;(event.shiftKey ? last : first).focus()
            }
        }
    }
    if (event.key !== 'Escape') return
    if (showSettingPanel.value && !openingChannel.value) { showSettingPanel.value = false; toolsButtonRef.value?.focus() }
    if (showTools.value) { showTools.value = false; toolsButtonRef.value?.focus() }
}
const closeToolsOutside = (event: PointerEvent) => {
    if (!toolsRef.value?.contains(event.target as Node)) showTools.value = false
}
const text = ref("")
const editor = reactive(new MessageEditor())
const inputText = computed({ get: () => editor.target ? editor.draft : text.value,
    set: value => { if (editor.target) editor.draft = value; else text.value = value } })
const inputRef = ref<HTMLTextAreaElement>()
const syncError = ref('')
let disableEditing: (() => void) | undefined
let viewGeneration = 0
let disposed = false
const editError = computed(() => {
    if (editor.outcomeUnknown) return t('editOutcomeUnknown')
    if (editor.needsReload) return t('editReloadFailed')
    if (['version_conflict', 'content_epoch_conflict'].includes(editor.errorCode)) return t('editConflict')
    if (editor.errorCode === 'message_not_found') return t('editNotFound')
    return editor.errorCode ? t('editFailed') : ''
})

const leaveEdit = () => {
    if (!editor.target) return true
    if (!editor.leave(() => window.confirm(t('discardEdit')))) return false
    text.value = editor.sendingDraft
    return true
}
const beginEdit = (message: Message) => {
    if (!canEditMessage(message, uid || '') || !leaveEdit()) return
    editor.begin(toRaw(message), text.value)
    nextTick(() => inputRef.value?.focus())
}
const saveEdit = async () => {
    const saved = await editor.save(
        (message, content) => WKSDK.shared().chatManager.updateMessage(toRaw(message), content),
        async message => {
            const page = await WKSDK.shared().chatManager.syncMessages(message.channel, {
                startMessageSeq: message.messageSeq, endMessageSeq: message.messageSeq + 1,
                pullMode: PullMode.Up, limit: 1,
            })
            const current = page.find(item => sameMessage(item, message))
            if (!current) throw new Error('Message no longer available')
            return current
        },
    )
    if (saved) text.value = editor.sendingDraft
}
const beforeUnload = (event: BeforeUnloadEvent) => {
    if (editor.target && (editor.dirty || editor.saving || editor.outcomeUnknown)) {
        event.preventDefault(); event.returnValue = ''
    }
}

const isComposing = ref(false) // 是否正在输入中,防中文干扰
const hasHandled = ref(false) // 是否已经处理过，防中文干扰


const channelID = ref("") // 设置聊天的频道ID
const p2p = ref(true) // 是否是单聊
const to = ref(new Channel("", 0)) // 对方的频道信息
const placeholder = computed(() => t(p2p.value ? 'recipientPlaceholder' : 'groupPlaceholder'))
const pulldowning = ref(false) // 下拉中
const pulldownFinished = ref(false) // 下拉完成

const msgInputPlaceholder = t('messagePlaceholder')

const messages = shallowRef<Message[]>(new Array<Message>())

const session = loadSession(window.sessionStorage)
const uid = session?.uid
const token = session?.token
if (session) APIClient.shared.config.apiURL = session.apiURL
type MessageAvatarSource = { fromUID: string, send: boolean }
const messageAvatarCache = new WeakMap<MessageAvatarSource, { uid: string, url: string }>()

// messageAvatarURL keeps generated avatar data scoped to the message lifetime.
const messageAvatarURL = (message: MessageAvatarSource): string => {
    const avatarUID = message.fromUID || (message.send ? uid : "") || ""
    const cachedAvatar = messageAvatarCache.get(message)
    if (cachedAvatar?.uid === avatarUID) {
        return cachedAvatar.url
    }

    const url = avatarURLForUID(avatarUID)
    messageAvatarCache.set(message, { uid: avatarUID, url })
    return url
}

title.value = t('notConnected', { uid: uid || '' })

let connectStatusListener!: ConnectStatusListener
let messageListener!: MessageListener
let messageStatusListener!: MessageStatusListener
const messageUpdateListener: MessageUpdateListener = updates => {
    const current = updates.filter(message => to.value.isEqual(message.channel))
    if (!current.length) return
    const replacements = new Map(current.map(message => [message.messageID, message]))
    messages.value = messages.value.map(message => replacements.get(message.messageID) || message)
}

onMounted(() => {
    window.addEventListener("beforeunload", beforeUnload)
    document.addEventListener('keydown', closeOverlays)
    document.addEventListener('pointerdown', closeToolsOutside)


    if (!session) {
        WKSDK.shared().connectManager.disconnect()
        void router.replace({ path: '/' })
        return
    }
    // 获取IM的长连接地址
    APIClient.shared.get('/route', {
        param: { uid }
    }).then((res) => {
        if (disposed) return
        let addr = res.wss_addr
        if (!addr || addr === "") {
            addr = res.ws_addr
        }
        connectIM(addr)

    }).catch((err) => {
        connectionState.value = 'disconnected'
        title.value = t('connectionFailed')
        syncError.value = t('connectionFailed')
    })
})


// 连接IM
const connectIM = (addr: string) => {
    console.log("connectIM--->", addr)
    const config = WKSDK.shared().config
    if (uid && token) {
        config.uid = uid;
        config.token = token
    }
    config.addr = addr
    config.deviceFlag = 1
    config.debug = false
    config.sendCountOfEach = 100000
    WKSDK.shared().config = config
    disableEditing = enableMessageEditing(error => {
        if ((error as any).code !== 'cancelled') syncError.value = t('messageSyncFailed')
    })
    WKSDK.shared().chatManager.addMessageUpdateListener(messageUpdateListener)


    // 监听连接状态
    let authenticationRejected = false
    connectStatusListener = (status: ConnectStatus, reasonCode?: number, connectionInfo?: ConnectionInfo) => {
        connectionState.value = status === ConnectStatus.Connected ? 'connected' : 'disconnected'
        if (status == ConnectStatus.Connected) {
            authenticationRejected = false
            if (connectionInfo) {
                title.value = t('connectedNode', { uid: uid || '', node: connectionInfo.nodeId })
            } else {
                title.value = t('connected', { uid: uid || '' })
            }

        } else if (status === ConnectStatus.ConnectFail && reasonCode === 2) {
            authenticationRejected = true
            connectionState.value = 'rejected'
            title.value = t('authenticationFailed')
        } else if (!authenticationRejected) {
            title.value = t('disconnected', { uid: uid || '' })
        } else { connectionState.value = 'rejected' }
    }
    WKSDK.shared().connectManager.addConnectStatusListener(connectStatusListener)

    // 监听消息
    messageListener = (msg) => {
        if (!to.value.isEqual(msg.channel)) {
            return
        }
        if (msg.send && msg.status === MessageStatus.Wait) {
            const packet = WKSDK.shared().chatManager.sendingQueues.get(msg.clientSeq)
            if (packet) retryPackets.set(msg.clientSeq, packet)
        }
        const index = messages.value.findIndex(item => item.clientMsgNo === msg.clientMsgNo)
        if (index >= 0) {
            messages.value = messages.value.map((item, i) => i === index ? msg : item)
            return
        }
        const follow = msg.send || (nearBottom.value && showChat.value)
        messages.value = [...messages.value, msg]
        if (follow) scrollBottom()
        else unreadBelow.value++
    }
    WKSDK.shared().chatManager.addMessageListener(messageListener)


    messageStatusListener = (ack: SendackPacket) => {
        if (ack.reasonCode === 1) retryPackets.delete(ack.clientSeq)
        messages.value = messages.value.map(message => {
            if (message.clientSeq !== ack.clientSeq) return message
            const updated = Object.assign(new Message(), message)
            updated.status = ack.reasonCode == 1 ? MessageStatus.Normal : MessageStatus.Fail
            if (ack.reasonCode === 1) {
                updated.messageID = ack.messageID.toString()
                updated.messageSeq = ack.messageSeq
            }
            return updated
        })
    }
    WKSDK.shared().chatManager.addMessageStatusListener(messageStatusListener)

    WKSDK.shared().connect()
}

onUnmounted(() => {
    disposed = true
    viewGeneration++
    window.removeEventListener('beforeunload', beforeUnload)
    document.removeEventListener('keydown', closeOverlays)
    document.removeEventListener('pointerdown', closeToolsOutside)
    retryPackets.clear()
    WKSDK.shared().conversationManager.openConversation = undefined
    WKSDK.shared().chatManager.removeMessageUpdateListener(messageUpdateListener)
    disableEditing?.()
    WKSDK.shared().connectManager.removeConnectStatusListener(connectStatusListener)
    WKSDK.shared().chatManager.removeMessageListener(messageListener)
    WKSDK.shared().chatManager.removeMessageStatusListener(messageStatusListener)
    WKSDK.shared().disconnect()
})


const scrollBottom = () => {
    const generation = viewGeneration
    nextTick(() => {
        if (generation !== viewGeneration || !chatRef.value) return
        chatRef.value.scrollTop = chatRef.value.scrollHeight
        nearBottom.value = true
        unreadBelow.value = 0
    })
}
// Retry a rejected packet through the SDK queue without creating a second message.
const retryMessage = (message: Message) => {
    const packet = retryPackets.get(message.clientSeq)
    if (!connected.value || !packet || message.status !== MessageStatus.Fail) return
    const manager = WKSDK.shared().chatManager
    manager.sendingQueues.set(packet.clientSeq, packet)
    messages.value = messages.value.map(item => item.clientSeq === message.clientSeq
        ? Object.assign(new Message(), item, { status: MessageStatus.Wait }) : item)
    manager.sendSendPacket(packet)
}
const backToChats = () => {
    if (!leaveEdit()) return
    showChat.value = false
    WKSDK.shared().conversationManager.openConversation = undefined
}
// 拉取当前会话最新消息
const pullLast = async () => {
    const generation = viewGeneration
    const channel = toRaw(to.value)
    pulldowning.value = true
    pulldownFinished.value = false
    const msgs = await WKSDK.shared().chatManager.syncMessages(channel, {
        limit: 15, startMessageSeq: 0, endMessageSeq: 0,
        pullMode: PullMode.Up
    })

    if (generation !== viewGeneration) return
    pulldowning.value = false
    pulldownFinished.value = msgs.length < 15 || msgs[0]?.messageSeq === 1
    if (msgs && msgs.length > 0) {
        const known = new Set(messages.value.map(m => m.messageID))
        messages.value = [...msgs.filter(m => !known.has(m.messageID)), ...messages.value]
    }
    scrollBottom()

}
const pullDown = async () => {
    const generation = viewGeneration
    const channel = toRaw(to.value)
    if (messages.value.length == 0) {
        return
    }
    const scrollHeight = chatRef.value?.scrollHeight || 0
    const scrollTop = chatRef.value?.scrollTop || 0
    const firstMsg = messages.value[0]
    if (firstMsg.messageSeq == 1) {
        pulldownFinished.value = true
        return
    }
    const limit = 15
    const msgs = await WKSDK.shared().chatManager.syncMessages(channel, {
        limit: limit, startMessageSeq: firstMsg.messageSeq - 1, endMessageSeq: 0,
        pullMode: PullMode.Down
    })

    if (generation !== viewGeneration) return
    if (msgs.length < limit) {
        pulldownFinished.value = true
    }
    if (msgs && msgs.length > 0) {
        const known = new Set(messages.value.map(m => m.messageID))
        messages.value = [...msgs.filter(m => !known.has(m.messageID)), ...messages.value]
    }
    await nextTick()
    if (generation === viewGeneration && chatRef.value) {
        chatRef.value.scrollTop = scrollTop + chatRef.value.scrollHeight - scrollHeight
    }
}


const settingClick = () => {
    showTools.value = false
    settingError.value = ''
    showSettingPanel.value = true
    nextTick(() => channelInputRef.value?.focus())
}

const activateChannel = (channel: Channel) => {
    showChat.value = true
    showTools.value = false
    showSettingPanel.value = false
    const manager = WKSDK.shared().conversationManager
    manager.openConversation = manager.findConversation(channel) || manager.createEmptyConversation(channel)
    if (to.value.isEqual(channel) && !syncError.value) return
    viewGeneration++
    nearBottom.value = true
    unreadBelow.value = 0
    retryPackets.clear()
    to.value = channel
    channelID.value = channel.channelID
    p2p.value = channel.channelType === ChannelTypePerson
    showSettingPanel.value = false
    messages.value = []
    syncError.value = ''
    const generation = viewGeneration
    pullLast().catch(error => {
        if (generation === viewGeneration) {
            pulldowning.value = false
            if (error?.code !== 'cancelled') syncError.value = t('messageSyncFailed')
        }
    })
}
const settingOKClick = async () => {
    if (openingChannel.value || !channelID.value.trim() || !leaveEdit()) return
    openingChannel.value = true
    settingError.value = ''
    const channel = new Channel(channelID.value.trim(), p2p.value ? ChannelTypePerson : ChannelTypeGroup)
    try {
        if (!p2p.value) await APIClient.shared.joinChannel(channel.channelID, channel.channelType, uid || '')
        if (!disposed) activateChannel(channel)
    } catch { settingError.value = t('openChatFailed') }
    finally { openingChannel.value = false }
}

const onSelectChannel = (channel: Channel): boolean => {
    if (!leaveEdit()) return false
    activateChannel(channel)
    return true
}

// The SDK resolves enqueueing, while SENDACK alone decides success or failure.
const sendContent = async (content: MessageContent) => {
    if (submittingMessage.value || !connected.value || !to.value.channelID) return false
    submittingMessage.value = true
    try {
        await WKSDK.shared().chatManager.send(content, toRaw(to.value), Setting.fromUint8(0))
        return true
    } catch {
        syncError.value = t('sendFailedDraft')
        return false
    } finally { submittingMessage.value = false }
}
const onSend = async () => {
    if (editor.target) { await saveEdit(); return }
    if (!text.value.trim()) return
    const draft = text.value
    if (await sendContent(new MessageText(draft)) && text.value === draft) text.value = ''
}
const onCustomMessageSend = async () => {
    showTools.value = false
    if (editor.target) return
    const message = new CustomMessage()
    message.orderNo = Date.now().toString()
    message.title = t('sampleProduct')
    message.num = 1
    message.price = 18
    message.imgUrl = demoLogoURL
    await sendContent(message)
}

// A full page transition drops account-specific SDK state as well as Vue state.
const logout = () => {
    if (!leaveEdit()) return
    clearSession(window.sessionStorage)
    WKSDK.shared().disconnect()
    window.location.replace(window.location.pathname + window.location.search + '#/')
}

const resync = () => {
    if (!leaveEdit()) return
    if (text.value.trim()) {
        return
    }
    WKSDK.shared().disconnect()
    window.location.reload()
}




// Manual and scroll paging share one in-flight guard. The button also works
// when a tall viewport cannot scroll a full first page of messages.
const loadEarlier = () => {
    if (pulldowning.value || pulldownFinished.value) return
    const generation = viewGeneration
    pulldowning.value = true
    pullDown().then(() => {
        if (generation === viewGeneration) pulldowning.value = false
    }).catch(error => {
        if (generation === viewGeneration) {
            pulldowning.value = false
            if (error?.code !== 'cancelled') syncError.value = t('messageSyncFailed')
        }
    })
}

const handleScroll = (event: Event) => {
    const list = event.target as HTMLElement
    nearBottom.value = list.scrollHeight - list.clientHeight - list.scrollTop < 80
    if (nearBottom.value) unreadBelow.value = 0
    if (list.scrollTop <= 100) loadEarlier()
}

const onEnter = (event: KeyboardEvent) => {
    if (event.shiftKey) return
    // 中文输入法正在组合输入时，不触发
    if (hasHandled.value || isComposing.value || event.isComposing) return
    onSend()
}

const onKeydown = (e: any) => {
    if (!isComposing.value && !e.isComposing) {
        if (!e.shiftKey) e.preventDefault()
        hasHandled.value = false
        return
    }
    // 中文输入法状态下回车确认输入
    hasHandled.value = true
}

</script>
<template>
    <div class="chat" :class="{ 'show-chat': showChat }">
        <header class="header">
            <div class="brand"><img :src="demoLogoURL" alt="" /><div><strong>WuKongIM</strong><small>{{ t('chatDemo') }}</small></div></div>
            <div class="chat-heading">
                <button v-if="showChat" class="back-button icon-button" :aria-label="t('backToChats')" @click="backToChats">‹</button>
                <div class="chat-title"><strong>{{ channelTitle }}</strong><small v-if="to.channelID">{{ t(to.channelType === ChannelTypeGroup ? 'groupChat' : 'directChat') }}</small></div>
            </div>
            <div class="header-actions">
                <a class="demo-home-link" :href="home" data-demo-home><span aria-hidden="true">←</span>{{ t('backToHome') }}</a>
                <span class="connection-status" data-testid="connection-status" :data-state="connectionState" :title="title" role="status"><i></i>{{ t(connected ? 'online' : connectionState === 'rejected' ? 'authRejected' : connectionState === 'connecting' ? 'connecting' : 'offline') }}</span>
                <button v-if="!showChat" class="icon-button new-chat-mobile" :aria-label="t('chooseChat')" @click="settingClick">＋</button>
                <div class="tools" ref="toolsRef">
                    <button ref="toolsButtonRef" class="icon-button tools-button" :aria-label="t('demoTools')" :aria-expanded="showTools" aria-controls="demo-tools" @click="showTools = !showTools">⋯</button>
                    <div v-if="showTools" id="demo-tools" class="tools-menu">
                        <button @click="settingClick"><span aria-hidden="true">＋</span>{{ t('chooseChat') }}</button>
                        <button :disabled="!to.channelID || !!editor.target || !connected" @click="onCustomMessageSend"><span aria-hidden="true">◇</span>{{ t('customMessage') }}</button>
                        <button @click="resync" :disabled="(!editor.target && !!text.trim()) || editor.saving || editor.outcomeUnknown" :title="text.trim() ? t('finishDraftBeforeSync') : ''"><span aria-hidden="true">↻</span>{{ t('resync') }}</button>
                        <a href="https://github.com/WuKongIM/WuKongIM" target="_blank" rel="noopener"><span aria-hidden="true">↗</span>GitHub</a>
                        <button class="logout-button" @click="logout"><span aria-hidden="true">⇥</span>{{ t('logout') }}</button>
                    </div>
                </div>
            </div>
        </header>
        <div v-if="syncError || connectionState === 'rejected'" class="sync-error" role="alert">{{ connectionState === 'rejected' ? t('authenticationFailed') : syncError }}</div>
        <main class="content">
            <aside class="conversation-box" :aria-label="t('chatList')">
                <div class="sidebar-heading"><h1>{{ t('chatList') }}</h1><button class="icon-button" :aria-label="t('chooseChat')" @click="settingClick">＋</button></div>
                <Conversation :onSelectChannel="onSelectChannel" :selectedChannel="to" />
                <div class="account"><img :src="avatarURLForUID(uid || '')" alt="" /><div><strong>{{ uid }}</strong><small>{{ t('currentAccount') }}</small></div></div>
            </aside>
            <section class="message-box" :aria-label="channelTitle">
                <div v-if="!to.channelID" class="empty-chat">
                    <div class="empty-icon" aria-hidden="true"><svg viewBox="0 0 32 32"><path d="M7 6h18a3 3 0 0 1 3 3v12a3 3 0 0 1-3 3H14l-7 4v-4a3 3 0 0 1-3-3V9a3 3 0 0 1 3-3Z"/><path d="M10 12h12M10 18h8"/></svg></div>
                    <h2>{{ t('welcomeChat') }}</h2><p>{{ t('selectChatHint') }}</p>
                    <button class="primary" @click="settingClick">{{ t('chooseChat') }} <span aria-hidden="true">↗</span></button>
                </div>
                <template v-else>
                    <div class="message-stage">
                        <div class="message-list" @scroll="handleScroll" ref="chatRef" :aria-busy="pulldowning">
                            <div class="history-control"><button v-if="messages.length && !pulldownFinished" @click="loadEarlier" :disabled="pulldowning">{{ t(pulldowning ? 'loadingMessages' : 'loadEarlier') }}</button><span v-else>{{ t(pulldowning ? 'loadingMessages' : 'historyStart') }}</span></div>
                            <div v-if="!messages.length && !pulldowning && !syncError" class="empty-messages">{{ t('firstMessageHint') }}</div>
                            <div v-for="m in messages" :key="m.clientMsgNo" class="message" :class="{ right: m.send }" :id="m.clientMsgNo">
                                <img class="avatar" :src="messageAvatarURL(m)" alt="" />
                                <div class="message-body">
                                    <span v-if="!m.send && to.channelType === ChannelTypeGroup" class="sender-name">{{ m.fromUID }}</span>
                                    <div class="bubble" :class="{ right: m.send }"><MessageUI :message="m" /></div>
                                    <div v-if="m.send" class="message-meta">
                                        <span v-if="m.status === MessageStatus.Wait" class="status">{{ t('sending') }}…</span>
                                        <span v-if="m.status === MessageStatus.Fail" class="status failed" role="status">{{ t('sendFailed') }}</span>
                                        <button v-if="m.status === MessageStatus.Fail && retryPackets.has(m.clientSeq)" :disabled="!connected" @click="retryMessage(m)">{{ t('retryEdit') }}</button>
                                        <button v-if="canEditMessage(m, uid || '')" class="edit-button" :disabled="editor.saving || editor.outcomeUnknown" @click="beginEdit(m)">{{ t('edit') }}</button>
                                    </div>
                                </div>
                            </div>
                        </div>
                        <button v-if="!nearBottom" class="jump-latest" @click="scrollBottom">↓ {{ unreadBelow ? t('newMessages', { count: unreadBelow }) : t('jumpLatest') }}</button>
                    </div>
                    <div v-if="editor.target" class="edit-banner" role="status"><strong>{{ t('editingMessage') }}</strong><span v-if="editError" role="alert">{{ editError }}</span></div>
                    <div class="footer">
                        <div class="composer">
                            <textarea ref="inputRef" rows="2" :aria-label="editor.target ? t('editingMessage') : msgInputPlaceholder" :placeholder="msgInputPlaceholder" v-model="inputText" :readonly="editor.saving || editor.outcomeUnknown" @keyup.enter="onEnter" @keydown.enter="onKeydown" @compositionstart="isComposing = true" @compositionend="isComposing = false"></textarea>
                            <div class="composer-actions"><small>{{ t('keyboardHint') }}</small><div>
                                <template v-if="editor.target"><button :disabled="editor.saving || editor.outcomeUnknown" @click="leaveEdit">{{ t('cancelEdit') }}</button><button class="primary" :disabled="!editor.canSave" @click="saveEdit">{{ t(editor.saving ? 'savingEdit' : editor.needsReload ? 'reloadEdit' : editor.outcomeUnknown ? 'retryEdit' : 'saveEdit') }}</button></template>
                                <button v-else class="primary" :disabled="!text.trim() || !connected || submittingMessage" @click="onSend">{{ t('send') }} <span aria-hidden="true">↑</span></button>
                            </div></div>
                        </div>
                    </div>
                </template>
            </section>
        </main>
        <div v-if="showSettingPanel" class="setting" @click.self="!openingChannel && (showSettingPanel = false)">
            <form class="setting-content" role="dialog" aria-modal="true" :aria-label="t('chooseChat')" @submit.prevent="settingOKClick">
                <div class="dialog-heading"><h2>{{ t('chooseChat') }}</h2><button type="button" class="icon-button" :aria-label="t('cancelEdit')" :disabled="openingChannel" @click="showSettingPanel = false">×</button></div>
                <div class="switch"><label><input type="radio" name="chat-type" :disabled="openingChannel" :value="true" v-model="p2p" />{{ t('directChat') }}</label><label><input type="radio" name="chat-type" :disabled="openingChannel" :value="false" v-model="p2p" />{{ t('groupChat') }}</label></div>
                <label for="channel-id">{{ t(p2p ? 'recipientLabel' : 'groupLabel') }}</label>
                <input ref="channelInputRef" id="channel-id" :placeholder="placeholder" v-model="channelID" :disabled="openingChannel" required />
                <p v-if="settingError" role="alert" class="failed">{{ settingError }}</p>
                <button class="primary" type="submit" :disabled="openingChannel || !channelID.trim()">{{ t(openingChannel ? 'openingChat' : 'confirm') }}</button>
            </form>
        </div>
    </div>
</template>

<style scoped>
.chat { height: 100%; display: flex; flex-direction: column; background: var(--surface); }
.header { display: flex; align-items: center; flex-shrink: 0; min-height: 76px; border-bottom: 1px solid var(--line); padding: env(safe-area-inset-top) 24px 0; gap: 24px; }
.brand { width: 256px; flex-shrink: 0; display: flex; align-items: center; gap: 12px; }
.brand img { width: 38px; height: 38px; border-radius: 12px; }
.brand strong { font-size: 17px; letter-spacing: -.4px; }
.brand small, .chat-title small { display: block; color: var(--muted); font-size: 12px; margin-top: 2px; }
.chat-heading { display: flex; align-items: center; flex: 1; min-width: 0; gap: 8px; }
.chat-title { min-width: 0; }
.chat-title strong { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 16px; }
.header-actions { display: flex; align-items: center; gap: 16px; flex-shrink: 0; }
.connection-status { display: flex; align-items: center; gap: 6px; font-size: 12px; color: var(--muted); white-space: nowrap; }
.connection-status i { width: 7px; height: 7px; background: var(--muted); border-radius: 50%; }
.connection-status[data-state="connected"] i { background: #27a77b; }
.connection-status[data-state="rejected"] i { background: var(--danger); }
.icon-button { width: 36px; height: 36px; padding: 0; font-size: 25px; background: transparent; border: 0; display: inline-flex; align-items: center; justify-content: center; }
.new-chat-mobile { display: none; }
.back-button { display: none; font-size: 34px; }
.tools { position: relative; }
.tools-button { font-size: 28px; letter-spacing: 2px; }
.tools-menu { position: absolute; top: 44px; right: 0; width: 228px; padding: 6px; background: var(--surface); border: 1px solid var(--line); border-radius: 14px; box-shadow: var(--shadow); z-index: 20; }
.tools-menu button, .tools-menu a { display: flex; align-items: center; gap: 12px; width: 100%; text-align: left; padding: 11px 12px; border: 0; border-radius: 8px; font-size: 14px; color: var(--text); background: transparent; }
.tools-menu span { width: 18px; color: var(--muted); text-align: center; font-size: 18px; }
.tools-menu .logout-button { border-top: 1px solid var(--line); margin-top: 4px; border-radius: 0 0 8px 8px; }
.content { display: flex; flex: 1; min-height: 0; }
.conversation-box { width: 304px; flex-shrink: 0; display: flex; flex-direction: column; border-right: 1px solid var(--line); background: var(--sidebar); }
.sidebar-heading { display: flex; justify-content: space-between; align-items: center; padding: 20px 20px 14px; }
h1 { font-size: 18px; margin: 0; }
.account { padding: 16px 20px; border-top: 1px solid var(--line); display: flex; align-items: center; gap: 10px; }
.account img { width: 34px; height: 34px; border-radius: 11px; }
.account div { min-width: 0; }
.account strong { font-size: 13px; display: block; overflow: hidden; text-overflow: ellipsis; }
.account small { display: block; font-size: 11px; color: var(--muted); }
.message-box { flex: 1; min-width: 0; display: flex; flex-direction: column; background: var(--chat-background); }
.empty-chat { margin: auto; padding: 32px; text-align: center; }
.empty-icon { margin: 0 auto 22px; width: 76px; height: 76px; display: grid; place-items: center; background: var(--accent-soft); border-radius: 24px; }
.empty-icon svg { width: 34px; fill: none; stroke: var(--accent); stroke-width: 1.6; }
.empty-chat h2 { font-size: 22px; letter-spacing: -.5px; margin: 0 0 8px; }
.empty-chat p { color: var(--muted); font-size: 14px; margin: 0 0 26px; }
.message-stage { position: relative; flex: 1; min-height: 0; }
.message-list { height: 100%; overflow-y: auto; padding: 16px 32px 24px; overscroll-behavior: contain; overflow-anchor: none; }
.history-control { text-align: center; padding: 4px 0 20px; font-size: 12px; color: var(--muted); }
.history-control button { border: 0; background: transparent; color: var(--muted); font-size: 12px; }
.empty-messages { text-align: center; color: var(--muted); margin-top: 30px; font-size: 14px; }
.message { display: flex; align-items: flex-start; gap: 10px; margin-bottom: 22px; }
.message.right { flex-direction: row-reverse; }
.avatar { width: 32px; height: 32px; border-radius: 10px; flex-shrink: 0; margin-top: 2px; }
.message-body { min-width: 0; max-width: min(75%, 640px); }
.bubble { padding: 12px 16px; border-radius: 4px 16px 16px; background: var(--surface); border: 1px solid var(--line); box-shadow: 0 2px 3px #00000003; }
.bubble.right { background: var(--sent); border-color: transparent; border-radius: 16px 4px 16px 16px; }
.sender-name { display: block; margin: 0 0 5px 2px; color: var(--muted); font-size: 11px; overflow-wrap: anywhere; }
.message-meta { display: flex; justify-content: flex-end; align-items: center; gap: 8px; font-size: 11px; min-height: 24px; color: var(--muted); }
.message-meta button { background: transparent; border: 0; padding: 3px 2px; color: var(--muted); font-size: 11px; }
.message-meta button:hover { color: var(--accent); }
.failed { color: var(--danger); }
.jump-latest { position: absolute; bottom: 14px; left: 50%; transform: translateX(-50%); border: 1px solid var(--line); border-radius: 24px; background: var(--surface); box-shadow: var(--shadow); color: var(--accent); padding: 9px 16px; font-size: 13px; white-space: nowrap; }
.footer { padding: 0 28px max(22px, env(safe-area-inset-bottom)); flex-shrink: 0; }
.composer { border: 1px solid var(--line); border-radius: 16px; background: var(--surface); padding: 14px 16px 10px; box-shadow: 0 3px 16px #00000003; }
.composer:focus-within { border-color: var(--accent); }
textarea { display: block; width: 100%; resize: none; border: 0; padding: 0; min-height: 48px; max-height: 180px; background: transparent; color: var(--text); font-size: 15px; line-height: 1.6; outline: none; }
.composer-actions { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.composer-actions small { font-size: 11px; color: var(--muted); }
.composer-actions > div { display: flex; gap: 8px; margin-left: auto; }
.primary span { margin-left: 10px; font-size: 16px; }
.edit-banner, .sync-error { flex-shrink: 0; padding: 12px 24px; font-size: 13px; background: var(--notice); color: var(--notice-text); }
.edit-banner { display: flex; flex-wrap: wrap; gap: 6px 14px; margin-bottom: 12px; }
.setting { position: fixed; inset: 0; z-index: 30; display: grid; place-items: center; padding: 20px; background: #131b2b55; backdrop-filter: blur(3px); }
.setting-content { width: min(100%, 400px); padding: 24px; border-radius: 20px; background: var(--surface); box-shadow: var(--shadow); }
.dialog-heading { display: flex; align-items: center; justify-content: space-between; margin-bottom: 20px; }
.dialog-heading h2 { margin: 0; font-size: 19px; }
.switch { display: flex; gap: 20px; margin-bottom: 22px; }
.switch label { display: flex; align-items: center; gap: 6px; font-size: 14px; }
.switch input { accent-color: var(--accent); }
.setting-content > label { display: block; font-size: 13px; margin-bottom: 8px; }
.setting-content > input { width: 100%; margin-bottom: 20px; }
.setting-content > .primary { width: 100%; }
@media (max-width: 700px) {
    .header { min-height: 68px; padding-left: 14px; padding-right: 14px; gap: 8px; }
    .brand { display: none; }
    .back-button, .new-chat-mobile { display: inline-flex; }
    .header-actions { gap: 8px; }
    .chat-title strong { font-size: 16px; }
    .connection-status { font-size: 11px; }
    .conversation-box { width: 100%; border-right: 0; }
    .message-box { display: none; }
    .show-chat .conversation-box { display: none; }
    .show-chat .message-box { display: flex; }
    .sidebar-heading { display: none; }
    .message-list { padding: 14px 16px 20px; }
    .message { gap: 8px; margin-bottom: 18px; }
    .message-body { max-width: calc(100% - 48px); }
    .bubble { padding: 10px 13px; }
    .footer { padding: 0 12px max(12px, env(safe-area-inset-bottom)); }
    .composer { padding: 12px; }
    textarea { font-size: 16px; }
    .composer-actions small { display: none; }
    .edit-banner, .sync-error { padding: 10px 16px; }
}
</style>
