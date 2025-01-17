<script setup lang="ts">
import { onMounted, ref } from 'vue';
import API from '../../services/API';
import { ellipsis, base64Decode, base64Encode } from '../../services/Utils';
import { useRouter } from "vue-router";
import App from '../../services/App';

const router = useRouter()

const nodeTotal = ref<any>({}); // 节点列表
const selectedNodeId = ref<number>() // 选中的节点ID
const loading = ref<boolean>(false);
const messages = ref<any>({}); // 消息列表
const fromUid = ref<string>() // 发送者
const channelId = ref<string>() // 接受频道id
const channelType = ref<number>() // 频道类型
const payload = ref<string>() // 消息内容
const messageId = ref<number>() // 消息id
const clientMsgNo = ref<string>() // 客户端唯一编号

const currentPage = ref<number>(1) // 当前页码
const pageSize = ref<number>(20) // 每页数量

const offsetMessageId = ref<number>() // 偏移的messageId
const offsetMessageSeq = ref<number>() // 偏移的messageSeq
const pre = ref<boolean>() // 是否向上分页
const content = ref<string>() // 当前要显示的内容



const query = router.currentRoute.value.query; //查询参数

onMounted(() => {

    App.shard().loadSystemSettingIfNeed()

    if (query.channelId) {
        channelId.value = query.channelId as string
    }

    if (query.channelType) {
        channelType.value = parseInt(query.channelType as string)
    }

    searchMessages()
    API.shared.simpleNodes().then((res) => {
        nodeTotal.value = res
    }).catch((err) => {
        alert(err)
    })
})

const searchMessages = () => {
    loading.value = true;
    let base64EncodePayload: string = ''
    if (payload.value && payload.value.trim() != '') {
        base64EncodePayload = base64Encode(payload.value)
        console.log(base64EncodePayload)
        base64EncodePayload = encodeURIComponent(base64EncodePayload)
    }
    API.shared.searchMessages({
        nodeId: selectedNodeId.value,
        fromUid: fromUid.value,
        channelId: channelId.value,
        channelType: channelType.value,
        payload: base64EncodePayload,
        messageId: messageId.value,
        limit: pageSize.value,
        offsetMessageId: offsetMessageId.value,
        offsetMessageSeq: offsetMessageSeq.value,
        pre: pre.value,
        clientMsgNo: clientMsgNo.value
    }).then((res) => {
        messages.value = res.data
    }).catch((err) => {
        alert(err)
    }).finally(() => {
        loading.value = false;
    })
}

// const onNodeChange = (e: any) => {
//     selectedNodeId.value = e.target.value
//     searchMessages()
// }

const onFromUidSearch = (e: any) => {
    fromUid.value = e.target.value
    resetFilter()
    searchMessages()
}

const onChannelIdSearch = (e: any) => {
    channelId.value = e.target.value
    if (!channelId.value || channelId.value.trim() == '') {
        channelType.value = 0
    }
    resetFilter()
    searchMessages()
}

const onChannelTypeSearch = (e: any) => {
    channelType.value = e.target.value
    if (!channelId.value || channelId.value.trim() == '') {
        return
    }
    resetFilter()
    searchMessages()
}

const onMessageIdSearch = (e: any) => {
    messageId.value = e.target.value
    resetFilter()
    searchMessages()
}


const onClientMsgNoSearch = (e: any) => {
    clientMsgNo.value = e.target.value
    resetFilter()
    searchMessages()
}

const resetFilter = () => {
    currentPage.value = 1
    offsetMessageId.value = 0
    offsetMessageSeq.value = 0
}

// 下一页
const nextPage = () => {
    if (messages.value.length < pageSize.value) {
        alert("没有更多数据了")
        return
    }
    currentPage.value += 1
    offsetMessageId.value = messages.value[messages.value.length - 1].message_id
    offsetMessageSeq.value = messages.value[messages.value.length - 1].message_seq
    pre.value = false
    searchMessages()
}

// 上一页
const prevPage = () => {
    if (currentPage.value <= 1) {
        return
    }
    currentPage.value -= 1
    offsetMessageId.value = messages.value[0].message_id
    offsetMessageSeq.value = messages.value[0].message_seq
    pre.value = true
    searchMessages()
}

// 显示消息内容
const onShowMessageContent = (message: any) => {
    content.value = base64Decode(message.payload)
    const dialog = document.getElementById('content') as HTMLDialogElement;
    dialog.showModal();
}

// 显示消息编号
const onShowClientMsgNo = (clientMsgNo: any) => {
    content.value = clientMsgNo
    const dialog = document.getElementById('content') as HTMLDialogElement;
    dialog.showModal();
}

// 消息轨迹惦记
const onMessageTrace = (clientMsgNo: string) => {
    if (!App.shard().systemSetting.messageTraceOn) {
        alert("消息追踪功能未开启,请查看官网文档: https://githubim.com")
        return
    }
    router.push(`/monitor/trace?clientMsgNo=${clientMsgNo}`)
}

</script>

<template>
    <div>
        <div class="overflow-x-auto h-5/6">
            <div class="flex flex-wrap gap-4">

                <!-- 发送者 -->
                <div class="text-sm ml-10">
                    <label>发送者</label>
                    <input type="text" placeholder="输入" class="input input-bordered  select-sm ml-2"
                        v-on:change="onFromUidSearch" />
                </div>

                <!-- 接受频道类型 -->
                <div class="text-sm ml-10">
                    <label>接受频道</label>
                    <select class="select select-bordered  max-w-xs select-sm w-20 ml-2"
                        v-on:change="onChannelTypeSearch" v-model="channelType">
                        <option value="1">个人</option>
                        <option value="2">群聊</option>
                        <option value="3">客服</option>
                        <option value="4">社区</option>
                        <option value="5">话题</option>
                        <option value="6">资讯</option>
                        <option value="7">数据</option>
                    </select>
                    <input type="text" placeholder="输入" v-model="channelId" class="input input-bordered  select-sm ml-2"
                        v-on:change="onChannelIdSearch" />
                </div>

                <!-- 消息id -->
                <div class="text-sm ml-10">
                    <label>消息ID</label>
                    <input type="text" placeholder="输入" class="input input-bordered  select-sm ml-2"
                        v-on:change="onMessageIdSearch" />
                </div>



                <!-- 节点 -->
                <!-- <div class="text-sm ml-3">
                    <label>节点</label>
                    <select class="select select-bordered  max-w-xs select-sm w-40 ml-2" v-on:change="onNodeChange">
                        <option value="0">所有</option>
                        <option v-for="node in nodeTotal.data" :selected="node.id == selectedNodeId">{{ node.id }}
                        </option>
                    </select>
                </div> -->
                <!-- 客户端唯一编号 -->
                <div class="text-sm ml-10">
                    <label>客户端唯一编号</label>
                    <input type="text" placeholder="输入" class="input input-bordered  select-sm ml-2"
                        v-on:change="onClientMsgNoSearch" />
                </div>
            </div>
            <table class="table mt-10 table-pin-rows">
                <!-- head -->
                <thead>
                    <tr>
                        <th>
                            <div class="flex items-center">
                                消息ID
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                消息序号
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                发送者
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                接受频道
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                接受频道类型
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                消息内容
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                发送时间
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                客户端唯一编号
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                任期
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                操作
                            </div>
                        </th>
                    </tr>
                </thead>
                <tbody>
                    <!-- row 1 -->
                    <tr v-for="message in messages">
                        <td>{{ message.message_id }}</td>
                        <td>{{ message.message_seq }}</td>
                        <td>{{ message.from_uid }}</td>
                        <td>{{ message.channel_id }}</td>
                        <td>{{ message.channel_type }}</td>
                        <td class="text-blue-800" v-on:click="() => onShowMessageContent(message)"><a href="#">{{
                            ellipsis(base64Decode(message.payload), 40) }}</a></td>
                        <td>{{ message.timestamp_format }}</td>
                        <td class="text-blue-800" v-on:click="() => onShowClientMsgNo(message.client_msg_no)"><a
                                href="#">{{ ellipsis(message.client_msg_no, 20) }}</a></td>
                        <td>{{ message.term }}</td>

                        <td class="flex">
                            <button class="btn btn-link btn-sm"
                                v-on:click="() => onMessageTrace(message.client_msg_no)">消息轨迹</button>
                        </td>
                    </tr>

                </tbody>
            </table>
            <!-- <div class="flex flex-col gap-4 w-full mt-2" v-if="loading">
                <div class="skeleton h-6 w-full"></div>
                <div class="skeleton h-6 w-full"></div>
                <div class="skeleton h-6 w-full"></div>
                <div class="skeleton h-6 w-full"></div>
                <div class="skeleton h-6 w-full"></div>
                <div class="skeleton h-6 w-full"></div>
            </div> -->
        </div>

        <div class="flex justify-end mt-10 mr-10">
            <div className="join">
                <button :class="{ 'join-item btn': true }" v-on:click="prevPage">«</button>
                <button className="join-item btn">{{ currentPage }}</button>
                <button :class="{ 'join-item btn': true }" v-on:click="nextPage">»</button>
            </div>
        </div>

        <dialog id="content" class="modal">
            <div class="modal-box flex flex-wrap gap-2">
                <div>{{ content }}</div>

            </div>
            <form method="dialog" class="modal-backdrop">
                <button>close</button>
            </form>
        </dialog>
    </div>
</template>