<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, shallowRef, reactive, toRaw } from 'vue';
import APIClient from '../services/APIClient'
import { clearSession, loadSession } from '../services/session'
import { t } from '../i18n'
import { useRouter } from "vue-router";
import { WKSDK, Message, MessageText, Channel, ChannelTypePerson, ChannelTypeGroup, MessageStatus, PullMode, MessageContent, ConnectionInfo, WKEventManager, WKEvent, MessageContentType, WKEventListener } from "wukongimjssdk";
import { ConnectStatus, ConnectStatusListener } from 'wukongimjssdk';
import { SendackPacket, Setting } from 'wukongimjssdk';
import { MessageListener, MessageStatusListener } from 'wukongimjssdk';
import Conversation from '../components/Conversation/index.vue'
import { CustomMessage, orderMessage } from '../customessage';
import MessageUI from '../messages/Message.vue';
import { Marked } from 'marked';
import { markedHighlight } from "marked-highlight";
import hljs from 'highlight.js';
import { demoLogoURL } from '../services/assets';
import { avatarURLForUID } from '../services/avatar';
import { enableMessageEditing } from '../services/datasource';
import { MessageEditor, canEditMessage, sameMessage } from '../services/messageEditor';
import { updateStreamMessage } from '../services/streamMessage';
import type { MessageUpdateListener } from 'wukongimjssdk';

const marked = new Marked(markedHighlight({
    emptyLangClass: 'hljs',
    langPrefix: 'hljs language-',
    highlight(code, lang, info) {
        const language = hljs.getLanguage(lang) ? lang : 'plaintext';
        return hljs.highlight(code, { language }).value;
    }
}));

marked.use({
    gfm: true,
});


const router = useRouter();
const chatRef = ref<HTMLElement | null>(null)
const showSettingPanel = ref(false)
const title = ref("")
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

let msgCount = 0

const channelID = ref("") // 设置聊天的频道ID
const p2p = ref(true) // 是否是单聊
const to = ref(new Channel("", 0)) // 对方的频道信息
const placeholder = computed(() => t(p2p.value ? 'recipientPlaceholder' : 'groupPlaceholder'))
const pulldowning = ref(false) // 下拉中
const pulldownFinished = ref(false) // 下拉完成

const startStreamMessage = ref(false) // 开始流消息
const msgInputPlaceholder = t('messagePlaceholder')
const streamNo = ref<string>() // 流消息序号

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

// renderStreamText 从 event_meta 或 stream_data 中提取流文本到 message.streamText
const renderStreamText = (m: any) => {
    // 优先使用 event_meta 中的 snapshot
    const eventMeta = m.eventMeta
    if (eventMeta && eventMeta.events && eventMeta.events.length > 0) {
        for (const ek of eventMeta.events) {
            if (ek.event_key === "main" || eventMeta.events.length === 1) {
                const snapshot = ek.snapshot
                if (snapshot && snapshot.kind === "text" && snapshot.text) {
                    m.streamText = snapshot.text
                    return
                }
            }
        }
    }
    // fallback: stream_data 已经在 convert.ts 中处理
}

let connectStatusListener!: ConnectStatusListener
let messageListener!: MessageListener
let messageStatusListener!: MessageStatusListener
let eventListener!: WKEventListener // 事件监听
const messageUpdateListener: MessageUpdateListener = updates => {
    const current = updates.filter(message => to.value.isEqual(message.channel))
    if (!current.length) return
    const replacements = new Map(current.map(message => [message.messageID, message]))
    messages.value = messages.value.map(message => replacements.get(message.messageID) || message)
}

onMounted(() => {
    window.addEventListener("beforeunload", beforeUnload)


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
        console.log(err)
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
        if (status == ConnectStatus.Connected) {
            authenticationRejected = false
            if (connectionInfo) {
                title.value = t('connectedNode', { uid: uid || '', node: connectionInfo.nodeId })
            } else {
                title.value = t('connected', { uid: uid || '' })
            }

        } else if (status === ConnectStatus.ConnectFail && reasonCode === 2) {
            authenticationRejected = true
            title.value = t('authenticationFailed')
        } else if (!authenticationRejected) {
            title.value = t('disconnected', { uid: uid || '' })
        }
    }
    WKSDK.shared().connectManager.addConnectStatusListener(connectStatusListener)

    // 监听消息
    messageListener = (msg) => {
        if (!to.value.isEqual(msg.channel)) {
            return
        }
        messages.value = [...messages.value, msg]

        scrollBottom()
    }
    WKSDK.shared().chatManager.addMessageListener(messageListener)

    // 事件监听 —— 使用 dataJson 获取推送数据
    eventListener = (event: WKEvent) => {
        if (!event.dataJson) return

        const pushData = event.dataJson

        const clientMsgNo = pushData.client_msg_no
        if (!clientMsgNo) return

        for (const message of messages.value) {
            if (message.clientMsgNo !== clientMsgNo) continue

            const updated = updateStreamMessage(message, event.type, pushData.payload,
                text => marked.parse(text, { async: false }) as string)
            if (updated === message) return
            messages.value = messages.value.map(current => current === message ? updated : current)
            nextTick(() => {
                scrollBottom()
            })
            break
        }
    }
    WKSDK.shared().eventManager.addEventListener(eventListener)

    messageStatusListener = (ack: SendackPacket) => {
        console.log(ack)
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
    WKSDK.shared().conversationManager.openConversation = undefined
    WKSDK.shared().chatManager.removeMessageUpdateListener(messageUpdateListener)
    disableEditing?.()
    WKSDK.shared().connectManager.removeConnectStatusListener(connectStatusListener)
    WKSDK.shared().chatManager.removeMessageListener(messageListener)
    WKSDK.shared().chatManager.removeMessageStatusListener(messageStatusListener)
    WKSDK.shared().eventManager.removeEventListener(eventListener)
    WKSDK.shared().disconnect()
})


const chatP2pClick = (v: any) => {
    p2p.value = v.target.checked
}
const chatGroupClick = (v: any) => {
    p2p.value = !v.target.checked
}
const scrollBottom = () => {
    const chat = chatRef.value
    if (chat) {
        nextTick(function () {
            chat.scrollTop = chat.scrollHeight
        })
    }
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

    // 渲染流消息
    for (const m of msgs) {
        if (m.setting.streamOn) {
            renderStreamText(m)
            if (m.streamText && m.streamText.length > 0) {
                const htmlText = await marked.parse(m.streamText)
                m.content = new MessageText(htmlText)
            }
        }
    }

    if (generation !== viewGeneration) return
    pulldowning.value = false
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

    // 渲染流消息
    for (const m of msgs) {
        if (m.setting.streamOn) {
            renderStreamText(m)
            if (m.streamText && m.streamText.length > 0) {
                const htmlText = await marked.parse(m.streamText)
                m.content = new MessageText(htmlText)
            }
        }
    }

    if (generation !== viewGeneration) return
    if (msgs.length < limit) {
        pulldownFinished.value = true
    }
    if (msgs && msgs.length > 0) {
        const known = new Set(messages.value.map(m => m.messageID))
        messages.value = [...msgs.filter(m => !known.has(m.messageID)), ...messages.value]
    }
    nextTick(function () {
        const chat = chatRef.value
        const firstMsgEl = document.getElementById(firstMsg.clientMsgNo)
        if (firstMsgEl) {
            chat!.scrollTop = firstMsgEl.offsetTop
        }
    })
}


const settingClick = () => {
    showSettingPanel.value = !showSettingPanel.value
}

const activateChannel = (channel: Channel) => {
    viewGeneration++
    to.value = channel
    channelID.value = channel.channelID
    p2p.value = channel.channelType === ChannelTypePerson
    showSettingPanel.value = false
    messages.value = []
    syncError.value = ''
    const manager = WKSDK.shared().conversationManager
    manager.openConversation = manager.findConversation(channel) || manager.createEmptyConversation(channel)
    const generation = viewGeneration
    pullLast().catch(error => {
        if (generation === viewGeneration) {
            pulldowning.value = false
            if (error?.code !== 'cancelled') syncError.value = t('messageSyncFailed')
        }
    })
}
const settingOKClick = async () => {
    if (!channelID.value.trim() || !leaveEdit()) return
    const channel = new Channel(channelID.value, p2p.value ? ChannelTypePerson : ChannelTypeGroup)
    try {
        if (!p2p.value) await APIClient.shared.joinChannel(channel.channelID, channel.channelType, uid || '')
        if (!disposed) activateChannel(channel)
    } catch { syncError.value = t('messageSyncFailed') }
}

const onSelectChannel = (channel: Channel): boolean => {
    if (!leaveEdit()) return false
    activateChannel(channel)
    return true
}

const onSend = () => {
    if (editor.target) { void saveEdit(); return }
    if (!text.value || text.value.trim() === "") {
        msgCount++
        text.value = `${msgCount}`
    }
    const setting = Setting.fromUint8(0)
    if (to.value && to.value.channelID != "") {
        var content: MessageContent
        content = new MessageText(text.value)
        WKSDK.shared().chatManager.send(content, to.value, setting)
        text.value = ""
    } else {
        showSettingPanel.value = true
    }
    scrollBottom()

}


// 发送自定义消息
const onCustomMessageSend = () => {
    if (editor.target) return

    if (!to.value || to.value.channelID.trim() == "") {
        showSettingPanel.value = true
        return
    }

    const customMessage = new CustomMessage()

    // 当前时间戳
    const timestamp = new Date().getTime()
    customMessage.orderNo = `${timestamp.toString()}`
    customMessage.title = t('sampleProduct')
    customMessage.num = 1
    customMessage.price = 18
    customMessage.imgUrl = demoLogoURL

    WKSDK.shared().chatManager.send(customMessage, to.value)
    scrollBottom()
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

const handleScroll = (e: any) => {
    if (e.target.scrollTop <= 250) loadEarlier()
}

const onEnter = (event: KeyboardEvent) => {
    if (event.shiftKey) return
    // 中文输入法正在组合输入时，不触发
    if (hasHandled.value || isComposing.value) return
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
    <div class="chat">
        <div class="header">
            <div class="left">
                <button v-on:click="logout">{{ t('logout') }}</button>
                <button>{{ t('chatList') }}</button>
                <button v-on:click="resync" :disabled="(!editor.target && text.trim().length > 0) || editor.saving || editor.outcomeUnknown"
                    :title="text.trim() ? t('finishDraftBeforeSync') : ''">{{ t('resync') }}</button>
                <button v-if="messages.length > 0 && !pulldownFinished" v-on:click="loadEarlier"
                    :disabled="pulldowning">{{ t('loadEarlier') }}</button>
            </div>
            <div class="center">
                {{ title }}
            </div>
            <div class="right" style="display: flex;align-items: center;">
                <a style="margin-right: 40px;display: flex;align-items: center;font-size: 12px;"
                    href="https://github.com/WuKongIM/WuKongIM" aria-label="github" target="_blank" rel="noopener"
                    data-v-7bc22406="" data-v-36371990="">
                    <svg role="img" width="32px" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
                        <title>GitHub</title>
                        <path
                            d="M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12">
                        </path>
                    </svg>
                    &nbsp;&nbsp; {{ t('starProject') }}
                </a>
                <button v-on:click="settingClick">{{ to.channelID.length == 0 ? t('chooseChat') : t(to.channelType ==
                    ChannelTypeGroup ? 'groupChatTitle' : 'directChatTitle', { id: to.channelID }) }}</button>
            </div>
        </div>
        <div class="content">
            <div class="conversation-box">
                <Conversation :onSelectChannel="onSelectChannel"></Conversation>
            </div>
            <div class="message-box">
                <div v-if="syncError" class="sync-error" role="alert">{{ syncError }}</div>
                <div class="message-list" v-on:scroll="handleScroll" ref="chatRef">
                    <template v-for="m in messages" :key="m.clientMsgNo">
                        <div class="message right" v-if="m.send" :id="m.clientMsgNo">
                            <div class="status" v-if="m.status != MessageStatus.Normal">{{ t('sending') }}</div>
                            <button v-if="canEditMessage(m, uid || '')" class="edit-button"
                                :disabled="editor.saving || editor.outcomeUnknown" @click="beginEdit(m)">{{ t('edit') }}</button>
                            <div class="bubble right">
                                <MessageUI :message="m"></MessageUI>
                            </div>
                            <div class="avatar">
                                <img :src="messageAvatarURL(m)"
                                    style="width: 40px;height: 40px;" />
                            </div>
                        </div>
                        <div class="message" v-if="!m.send" :id="m.clientMsgNo">
                            <div class="avatar">
                                <img :src="messageAvatarURL(m)"
                                    style="width: 40px;height: 40px;" />
                            </div>
                            <div class="bubble">
                                <MessageUI :message="m"></MessageUI>
                            </div>
                        </div>
                    </template>
                </div>
                <div v-if="editor.target" class="edit-banner" role="status">
                    <strong>{{ t('editingMessage') }}</strong>
                    <span v-if="editError" role="alert">{{ editError }}</span>
                </div>
                <div class="footer">
                    <textarea ref="inputRef" rows="2" :aria-label="editor.target ? t('editingMessage') : msgInputPlaceholder"
                        :placeholder="msgInputPlaceholder" v-model="inputText" :readonly="editor.saving || editor.outcomeUnknown" style="height: 40px;"
                        @keyup.enter="onEnter" @keydown.enter="onKeydown" @compositionstart="isComposing = true"
                        @compositionend="isComposing = false"></textarea>
                    <!-- <button class="message-stream" v-on:click="onMessageStream">{{ startStreamMessage ? '停止流消息' : '开启流消息'
                    }}</button> -->
                    <button v-if="!editor.target" class="message-custom" v-on:click="onCustomMessageSend">{{ t('customMessage') }}</button>
                    <template v-if="editor.target">
                        <button :disabled="editor.saving || editor.outcomeUnknown" @click="leaveEdit">{{ t('cancelEdit') }}</button>
                        <button :disabled="!editor.canSave" @click="saveEdit">{{ t(editor.saving ? 'savingEdit' : editor.needsReload ? 'reloadEdit' : editor.outcomeUnknown ? 'retryEdit' : 'saveEdit') }}</button>
                    </template>
                    <button v-else v-on:click="onSend">{{ t('send') }}</button>
                </div>
            </div>
        </div>


    </div>
    <transition name="fade">
        <div class="setting" v-if="showSettingPanel" v-on:click="settingClick">
            <div class="setting-content" @click.stop="">
                <div class="switch">
                    <div class="item">
                        <input type="radio" @click.stop="chatP2pClick" v-bind:checked="p2p" />{{ t('directChat') }}
                    </div>
                    <div class="item">
                        <input type="radio" @click.stop="chatGroupClick" v-bind:checked="!p2p" />{{ t('groupChat') }}
                    </div>
                </div>
                <input :placeholder="placeholder" class="to" v-model="channelID" />
                <button class="ok" v-on:click="settingOKClick">{{ t('confirm') }}</button>
            </div>
        </div>
    </transition>
</template>

<style scoped>
.edit-button { align-self: center; padding: 6px; font-size: 12px; }
.edit-banner, .sync-error { padding: 10px 16px; text-align: left; background: #fff3e6; color: #713900; }
.edit-banner { display: flex; flex-wrap: wrap; gap: 10px; }
.footer, .edit-banner, .sync-error { flex-shrink: 0; }
button:disabled { opacity: .5; cursor: not-allowed; }

.chat {
    width: 100%;
    height: 100vh;
    display: flex;
    flex-direction: column;
    overflow: hidden;
    position: relative;
}

.header {
    height: 60px;
    background-color: #f5f5f5;
    display: flex;
    align-items: center;
    justify-content: center;
    border-bottom: 1px;
    position: fixed;
    top: env(safe-area-inset-top);
    left: 0;
    right: 0;
    z-index: 9999;
    width: 100%;
}

@media (prefers-color-scheme: dark) {
    .header {
        background-color: #000;
    }
}

.header .left {
    display: flex;
}

.header .left button {
    margin-left: 10px;
    height: 40px;
    display: flex;
    align-items: center;
    font-size: 15px;
    background-color: transparent;
}

.header .center {
    flex: 1;
    font-size: 18px;
    font-weight: bold;
}

.header .right button {
    margin-right: 10px;
    height: 40px;
    display: flex;
    align-items: center;
    font-size: 15px;
    background-color: transparent;
    color: rgb(228, 98, 64);
}



.content {
    background-color: #f5f5f5;
    position: relative;
    display: flex;
    height: calc(100vh - 60px);
    /* header + footer */
    /* header height */
    padding-top: calc(60px + env(safe-area-inset-top));
    /* padding-top: 60px; */
    /* padding-bottom: 60px; */
    /* overflow-y: auto; */
    /* footer height */
}

@media (prefers-color-scheme: dark) {
    .content {
        background-color: #000;
    }
}

.message {
    display: flex;
    margin: 10px;
}

.message.right {
    justify-content: flex-end;
}

.message .bubble {
    background-color: white;
    display: flex;
    align-items: center;
    justify-content: center;
    border-radius: 0px 10px 10px 10px;
    padding: 10px;
    color: black;
    margin-left: 10px;
}

.bubble.right {
    border-radius: 10px 0px 10px 10px;
    background-color: rgb(228, 98, 64);
    color: white;
    margin-right: 10px;
}

.message .avatar {
    width: 40px;
    height: 40px;
    background-color: green;
    border-radius: 20px;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 16px;
    color: white;
}

.message .status {
    font-size: 12px;
    color: rgb(228, 98, 64);
    margin-left: 10px;
    margin-bottom: 5px;
    display: flex;
    align-items: center;
}




.footer {
    height: 60px;
    background-color: white;
    display: flex;
    bottom: env(safe-area-inset-bottom);
    width: 100%;
    align-items: center;
}

@media (prefers-color-scheme: dark) {
    .footer {
        background-color: #333;
    }
}

.footer button {
    min-width: 80px;
    width: auto;
    white-space: nowrap;
    padding: 0 14px;
    flex-shrink: 0;
    height: 40px;
    margin: 5px;
    border: none;
    outline: none;
    background-color: rgb(228, 98, 64);
    color: white;
    font-size: 15px;
}

.footer textarea {
    resize: none;
    min-width: 0;
    font: inherit;
    flex: 1;
    border: none;
    outline: none;
    margin-left: 10px;
}

.setting {
    position: absolute;
    top: 0px;
    left: 0px;
    width: 100%;
    height: 100%;
}

.setting .setting-content {
    position: absolute;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    width: 250px;
    background-color: white;
    box-shadow: 0 0 10px rgba(0, 0, 0, 0.1);
    padding: 20px;
    border-radius: 4px;
}

@media (prefers-color-scheme: dark) {
    .setting .setting-content {
        background-color: #333;
        color: white;
    }
}

.switch {
    display: flex;
    align-items: center;
    width: 100%;
    justify-content: center;
    margin-top: 10px;
}

.switch .item {
    margin-left: 20px;
    font-size: 14px;
}

.to {
    border: none;
    width: 100%;
    height: 40px;
    margin-top: 20px;
    box-sizing: border-box;
}

.ok {
    width: 100%;
    height: 40px;
    margin-top: 30px;
    border: none;
    outline: none;
    background-color: rgb(228, 98, 64);
    color: white;
    font-size: 15px;
    cursor: pointer;
}

.fade-enter-active,
.fade-leave-active {
    transition: opacity 0.5s;
}

.fade-enter,
.fade-leave-to {
    opacity: 0;
}

.message-stream {
    width: 120px !important;
    height: 40px;
}

.message-box {
    width: 100%;
    height: 100%;
    overflow: hidden;
    display: flex;
    flex-direction: column;
}

.message-list {
    width: 100%;
    flex: 1;
    min-height: 0;
    overflow: auto;
}

.conversation-box {
    display: flex;
    position: relative;
    width: 300px;
    height: 100%;
    left: 0px;
    z-index: 10000;
}

.message-custom {
    min-width: 140px;
    width: auto !important;
    height: 40px;
}
</style>
