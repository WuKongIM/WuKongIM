<script setup lang="ts">
import APIClient from '@/services/APIClient';
import { newConversationPage, ConversationPage,channelTypeToString } from '@/services/Model';
import { ref } from 'vue';


const conversationPage = ref<ConversationPage>()
const uid = ref<string>("")

const requestConversations = async () => {
    const conversationPageObj = await APIClient.shared.get("/api/conversations", {
        param: {
            "uid": uid.value,
        }
    })
    conversationPage.value = newConversationPage(conversationPageObj)

}

const onSearch = () => {
    requestConversations()
}
</script>

<template>
    <div>
        <div className="join">
            <div>
                <div>
                    <input className="input input-bordered join-item" placeholder="UID" v-model="uid" />
                </div>
            </div>
            <div className="indicator">
                <button className="btn join-item" v-on:click="onSearch">
                    <svg xmlns="http://www.w3.org/2000/svg" class="h-6 w-6" fill="none" viewBox="0 0 24 24"
                        stroke="currentColor">
                        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2"
                            d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
                    </svg>
                </button>
            </div>
        </div>
        <div class="pt-10">
            <div class="overflow-x-auto min-h-[30rem]">
                <table class="table">
                    <thead>
                        <tr>
                            <th>会话频道ID</th>
                            <th>会话频道类型</th>
                            <th>最后消息序号</th>
                            <th>最后消息客户端编号</th>
                            <th>最后会话时间</th>
                            <th>未读数</th>
                            <th>数据版本</th>
                        </tr>
                    </thead>
                    <tbody>
                        <tr v-if="conversationPage&&!conversationPage.on" >
                            <div class="w-full  absolute text-center p-10">最近会话未开启，如果需要请在配置里打开</div>
                        </tr>
                        <tr v-if="conversationPage?.on" v-for="conversation in conversationPage?.conversations">
                            <td>{{ conversation.channelID }}</td>
                            <td>{{ channelTypeToString(conversation.channelType) }}</td>
                            <td>{{ conversation.lastMsgSeq }}</td>
                            <td>{{ conversation.lastClientMsgNo }}</td>
                            <td>{{ conversation.timestamp }}</td>
                            <td>{{ conversation.Unread }}</td>
                            <td>{{ conversation.version }}</td>
                        </tr>
                    </tbody>
                </table>
            </div>
        </div>
    </div>
</template>