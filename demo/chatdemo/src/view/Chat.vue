<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref, toRaw, toRefs, unref } from 'vue';
import APIClient from '../services/APIClient'
import { useRouter } from "vue-router";
import { WKSDK, Message, MessageText, Channel, ChannelTypePerson, ChannelTypeGroup, MessageStatus, PullMode, MessageContent, MessageContentType, ConnectionInfo, AgentChangeListener } from "wukongimjssdk";
import { ConnectStatus, ConnectStatusListener } from 'wukongimjssdk';
import { SendackPacket, Setting } from 'wukongimjssdk';
import { Buffer } from 'buffer';
import { MessageListener, MessageStatusListener } from 'wukongimjssdk';
import Conversation from '../components/Conversation/index.vue'
import { CustomMessage, orderMessage } from '../customessage';
import MessageUI from '../messages/Message.vue';
import { Marked } from 'marked';
import { markedHighlight } from "marked-highlight";
import hljs from 'highlight.js';
import { Agent } from 'wukongimjssdk/lib/agent';

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

const isComposing = ref(false) // 是否正在输入中,防中文干扰
const hasHandled = ref(false) // 是否已经处理过，防中文干扰

let msgCount = 0

const channelID = ref("") // 设置聊天的频道ID
const p2p = ref(true) // 是否是单聊
const to = ref(new Channel("", 0)) // 对方的频道信息
const placeholder = ref("请输入对方登录名")
const pulldowning = ref(false) // 下拉中
const pulldownFinished = ref(false) // 下拉完成

const startStreamMessage = ref(false) // 开始流消息
const msgInputPlaceholder = ref("请输入消息")
const streamNo = ref<string>() // 流消息序号

const messages = ref<Message[]>(new Array<Message>())

const uid = router.currentRoute.value.query.uid as string || undefined;
const token = router.currentRoute.value.query.token as string || "token111";

title.value = `${uid || ""}(未连接)`

let connectStatusListener!: ConnectStatusListener
let messageListener!: MessageListener
let messageStatusListener!: MessageStatusListener
let agentListener!: AgentChangeListener // 流监听

onMounted(() => {

    console.log("WKSDK.shared().config.apiURL--->", APIClient.shared.config.apiURL)

    if (!APIClient.shared.config.apiURL || APIClient.shared.config.apiURL === '') {
        WKSDK.shared().connectManager.disconnect()
        router.push({ path: '/' })
    }
    // 获取IM的长连接地址
    APIClient.shared.get('/route', {
        param: { uid: router.currentRoute.value.query.uid }
    }).then((res) => {
        console.log(res)
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
    config.sendCountOfEach = 100000
    WKSDK.shared().config = config


    // 监听连接状态
    connectStatusListener = (status: ConnectStatus, reasonCode?: number, connectionInfo?: ConnectionInfo) => {
        if (status == ConnectStatus.Connected) {
            if (connectionInfo) {
                title.value = `${uid || ""}(连接成功-节点:${connectionInfo.nodeId})`
            } else {
                title.value = `${uid || ""}(连接成功)`
            }

        } else {
            title.value = `${uid || ""}(断开)`
        }
    }
    WKSDK.shared().connectManager.addConnectStatusListener(connectStatusListener)

    // 监听消息
    messageListener = (msg) => {
        console.log("messageListener-->", msg)
        if (!to.value.isEqual(msg.channel)) {
            return
        }
        messages.value.push(msg)

        scrollBottom()
    }
    WKSDK.shared().chatManager.addMessageListener(messageListener)

    // 流监听
    agentListener = (agent: Agent) => {
        if (agent.channel && !to.value.isEqual(agent.channel)) {
            return
        }
        for (const message of messages.value) {
            if (message.messageID === agent.messageID) {
                if (agent.events && agent.events.length > 0) {
                    message.content = new MessageText(agent.text)
                } else {
                    message.content = agent.initMessage?.content
                }
                break
            }
            // 刷新ui
            messages.value = [...messages.value]
            scrollBottom()
        }
    }
    WKSDK.shared().agentManager.addAgentChangeListener(agentListener)

    messageStatusListener = (ack: SendackPacket) => {
        console.log(ack)
        messages.value.forEach((m) => {
            if (m.clientSeq == ack.clientSeq) {
                m.status = ack.reasonCode == 1 ? MessageStatus.Normal : MessageStatus.Fail
                return
            }
        })
    }
    WKSDK.shared().chatManager.addMessageStatusListener(messageStatusListener)

    WKSDK.shared().connect()
}

onUnmounted(() => {
    WKSDK.shared().connectManager.removeConnectStatusListener(connectStatusListener)
    WKSDK.shared().chatManager.removeMessageListener(messageListener)
    WKSDK.shared().chatManager.removeMessageStatusListener(messageStatusListener)
    WKSDK.shared().agentManager.removeAgentChangeListener(agentListener)
    WKSDK.shared().disconnect()
})


const chatP2pClick = (v: any) => {
    p2p.value = v.target.checked
    if (p2p.value) {
        placeholder.value = "请输入对方登录名"
    } else {
        placeholder.value = "请输入群组ID"
    }
}
const chatGroupClick = (v: any) => {
    p2p.value = !v.target.checked
    if (p2p.value) {
        placeholder.value = "请输入对方登录名"
    } else {
        placeholder.value = "请输入群组ID"
    }
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
    pulldowning.value = true
    pulldownFinished.value = false
    const msgs = await WKSDK.shared().chatManager.syncMessages(to.value, {
        limit: 15, startMessageSeq: 0, endMessageSeq: 0,
        pullMode: PullMode.Up
    })

    // 渲染流消息
    for (const m of msgs) {
        if (m.setting.streamOn) {
            if (m.streamText && m.streamText.length > 0) {
                m.content = new MessageText(m.streamText)
            }
        }
    }

    pulldowning.value = false
    if (msgs && msgs.length > 0) {
        msgs.forEach((m) => {
            messages.value.push(m)
        })
    }
    scrollBottom()

}
const pullDown = async () => {
    if (messages.value.length == 0) {
        return
    }
    const firstMsg = messages.value[0]
    if (firstMsg.messageSeq == 1) {
        pulldownFinished.value = true
        return
    }
    const limit = 15
    const msgs = await WKSDK.shared().chatManager.syncMessages(to.value, {
        limit: limit, startMessageSeq: firstMsg.messageSeq - 1, endMessageSeq: 0,
        pullMode: PullMode.Down
    })

    // 渲染流消息
    for (const m of msgs) {
        if (m.setting.streamOn) {
            if (m.streamText && m.streamText.length > 0) {
                m.content = new MessageText(m.streamText)
            }
        }
    }

    if (msgs.length < limit) {
        pulldownFinished.value = true
    }
    if (msgs && msgs.length > 0) {
        msgs.reverse().forEach((m) => {
            messages.value.unshift(m)
        })
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

const settingOKClick = () => {
    if (p2p.value) {
        to.value = new Channel(channelID.value, ChannelTypePerson)
    } else {
        to.value = new Channel(channelID.value, ChannelTypeGroup)
    }
    if (!p2p.value) {
        APIClient.shared.joinChannel(to.value.channelID, to.value.channelType, WKSDK.shared().config.uid || "") // 加入频道
    }
    const conversation = WKSDK.shared().conversationManager.findConversation(to.value)
    if (!conversation) {
        // 如果最近会话不存在，则创建一个空的会话
        WKSDK.shared().conversationManager.createEmptyConversation(to.value)
    }
    showSettingPanel.value = false

    messages.value = []

    pullLast() // 拉取最新消息
}

const onSelectChannel = (channel: Channel) => {
    to.value = channel
    channelID.value = channel.channelID
    p2p.value = channel.channelType == ChannelTypePerson
    showSettingPanel.value = false
    messages.value = []
    pullLast() // 拉取最新消息
}

const onSend = () => {
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

    if (!to.value || to.value.channelID.trim() == "") {
        showSettingPanel.value = true
        return
    }

    const customMessage = new CustomMessage()

    // 当前时间戳
    const timestamp = new Date().getTime()
    customMessage.orderNo = `${timestamp.toString()}`
    customMessage.title = "可可柠檬鲜美奶茶"
    customMessage.num = 1
    customMessage.price = 18
    customMessage.imgUrl = "https://img1.baidu.com/it/u=3855634790,2542680254&fm=253&fmt=auto&app=138&f=JPEG?w=750&h=496"

    WKSDK.shared().chatManager.send(customMessage, to.value)
    scrollBottom()
}

const logout = () => {
    WKSDK.shared().connectManager.disconnect()
    router.push({ path: '/' })
}




const handleScroll = (e: any) => {
    const targetScrollTop = e.target.scrollTop;
    const scrollOffsetTop = e.target.scrollHeight - (targetScrollTop + e.target.clientHeight);
    if (targetScrollTop <= 250) { // 下拉
        if (pulldowning.value || pulldownFinished.value) {
            console.log("不允许下拉", "pulldowning", pulldowning.value, "pulldownFinished", pulldownFinished.value)
            return
        }
        console.log("下拉")
        pulldowning.value = true
        pullDown().then(() => {
            pulldowning.value = false
        }).catch(() => {
            pulldowning.value = false
        })
    }
}

const onEnter = () => {
    // 中文输入法正在组合输入时，不触发
    if (hasHandled.value || isComposing.value) return
    onSend()
}

const onKeydown = (e: any) => {
    if (!isComposing.value) {
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
                <button v-on:click="logout">退出</button>
                <button>聊天列表</button>
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
                    &nbsp;&nbsp; 吴彦祖，点个Star呗
                </a>
                <button v-on:click="settingClick">{{ to.channelID.length == 0 ? '与谁会话？' : `${to.channelType ==
                    ChannelTypeGroup ? '群' : '单聊'}${to.channelID}` }}</button>
            </div>
        </div>
        <div class="content">
            <div class="conversation-box">
                <Conversation :onSelectChannel="onSelectChannel"></Conversation>
            </div>
            <div class="message-box">
                <div class="message-list" v-on:scroll="handleScroll" ref="chatRef">
                    <template v-for="m in messages">
                        <div class="message right" v-if="m.send" :id="m.clientMsgNo">
                            <div class="status" v-if="m.status != MessageStatus.Normal">发送中</div>
                            <div class="bubble right">
                                <MessageUI :message="m"></MessageUI>
                            </div>
                            <div class="avatar">
                                <img :src="`https://api.dicebear.com/9.x/adventurer/svg?seed=${m.fromUID}&radius=50&backgroundType=gradientLinear&backgroundColor=ffd5dc`"
                                    style="width: 40px;height: 40px;" />
                            </div>
                        </div>
                        <div class="message" v-if="!m.send" :id="m.clientMsgNo">
                            <div class="avatar">
                                <img :src="`https://api.dicebear.com/9.x/adventurer/svg?seed=${m.fromUID}&radius=50&backgroundType=gradientLinear&backgroundColor=ffd5dc`"
                                    style="width: 40px;height: 40px;" />
                            </div>
                            <div class="bubble">
                                <MessageUI :message="m"></MessageUI>
                            </div>
                        </div>
                    </template>
                </div>
                <div class="footer">
                    <input :placeholder="msgInputPlaceholder" v-model="text" style="height: 40px;"
                        @keyup.enter="onEnter" @keydown.enter="onKeydown" @compositionstart="isComposing = true"
                        @compositionend="isComposing = false" />
                    <!-- <button class="message-stream" v-on:click="onMessageStream">{{ startStreamMessage ? '停止流消息' : '开启流消息'
                    }}</button> -->
                    <button class="message-custom" v-on:click="onCustomMessageSend">自定义消息</button>
                    <button v-on:click="onSend">发送</button>
                </div>
            </div>
        </div>


    </div>
    <transition name="fade">
        <div class="setting" v-if="showSettingPanel" v-on:click="settingClick">
            <div class="setting-content" @click.stop="">
                <div class="switch">
                    <div class="item">
                        <input type="radio" @click.stop="chatP2pClick" v-bind:checked="p2p" />单聊
                    </div>
                    <div class="item">
                        <input type="radio" @click.stop="chatGroupClick" v-bind:checked="!p2p" />群聊
                    </div>
                </div>
                <input :placeholder="placeholder" class="to" v-model="channelID" />
                <button class="ok" v-on:click="settingOKClick">确定</button>
            </div>
        </div>
    </transition>
</template>

<style scoped>
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
    width: 80px;
    height: 40px;
    margin: 5px;
    border: none;
    outline: none;
    background-color: rgb(228, 98, 64);
    color: white;
    font-size: 15px;
}

.footer input {
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
    height: calc(100% - 60px);
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
    width: 120px !important;
    height: 40px;
}
</style>