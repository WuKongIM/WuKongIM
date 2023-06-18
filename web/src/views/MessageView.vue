<script setup lang="ts">
import APIClient from '@/services/APIClient';
import { newMessage, type Message, MessagePage, newMessagePage, channelTypeToString } from '@/services/Model';
import { ref } from 'vue';
import { Buffer } from 'buffer';

declare const payloadModal: any;

const messagePage = ref<MessagePage>(new MessagePage())

const channelType = ref<number>(2)
const channelID = ref<string>("")
const errMsg = ref<string>()
const payload = ref<string>()
const page = ref<number>(0)



const requestMessages = async () => {
    let startMessageSeq = 0
    if (messagePage.value.data.length > 0) { 
        const len = messagePage.value.data.length
        startMessageSeq = messagePage.value.data[len-1].messageSeq
    }
       
    const messagePageObj = await APIClient.shared.get('/api/messages', {
        param: { "channel_id": channelID.value, "channel_type": channelType.value,"start_message_seq":startMessageSeq+1,"limit": 20 },
    })
    page.value+=1
    messagePage.value = newMessagePage(messagePageObj)
}
const onSearch = () => {
    page.value = 0
    requestMessages()
}

const onPayloadView = (msg: Message) => {
    payload.value = Buffer.from(msg?.payload, 'base64').toString('utf-8')
    payloadModal.showModal()
}

const onNextPage = () => {
    requestMessages()
}


</script>

<template>
    <div>
        <div className="join">
            <div>
                <div>
                    <input className="input input-bordered join-item" placeholder="频道ID" v-model="channelID" />
                </div>
            </div>
            <select className="select select-bordered join-item" v-model="channelType">
                <option value="2">群组频道</option>
                <option value="1">个人频道</option>
                <option value="3">客服频道</option>
                <option value="4">社区频道</option>
                <option value="5">社区话题频道</option>
                <option value="6">资讯频道</option>
            </select>
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
            <div class="">
                <div class="overflow-x-auto h-[30rem]">
                    <table class="table">
                        <thead>
                            <tr>
                                <th>消息序号</th>
                                <th>消息ID</th>
                                <th>消息头</th>
                                <th>消息设置</th>
                                <th>消息内容</th>
                                <th>唯一编号</th>
                                <th>发送者</th>
                                <th>投递频道ID</th>
                                <th>投递频道类型</th>
                                <th>消息时间</th>
                            </tr>
                        </thead>
                        <tbody>
                            <tr v-if="errMsg && errMsg !== ''" >
                            <div class="w-full  absolute text-center p-10">{{ errMsg }}</div>
                        </tr>
                            <tr v-for="message in messagePage?.data">
                                <td>{{ message.messageSeq }}</td>
                                <td>{{ message.messageID }}</td>
                                <td>{{ message.header }}
                                </td>
                                <td>{{ message.setting }}</td>
                                <td><button className="theme-color" v-on:click="() => onPayloadView(message)">查看</button>
                                </td>
                                <td>{{ message.clientMsgNo }}</td>
                                <td>{{ message.fromUID }}</td>
                                <td>{{ message.channelID }}</td>
                                <td>{{ channelTypeToString(message.channelType) }}</td>
                                <td>{{ message.timestamp }}</td>
                            </tr>
                        </tbody>
                    </table>
                </div>
                <div class="flex w-full justify-end pt-10">
                    <div class="join" v-if="messagePage && messagePage.data.length>0">
                        <button class="join-item btn">«</button>
                        <button class="join-item btn">{{page}}</button>
                        <button class="join-item btn" v-on:click="onNextPage">»</button>
                    </div>
                </div>
            </div>
        </div>
        <!-- Open the modal using ID.showModal() method -->
        <dialog id="payloadModal" class="modal">
            <form method="dialog" class="modal-box">
                <p class="py-4">{{ payload }}</p>
            </form>
            <form method="dialog" class="modal-backdrop">
                <button>close</button>
            </form>
        </dialog>
    </div>
</template>