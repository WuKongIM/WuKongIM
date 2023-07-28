<script setup lang="ts">
import APIClient from '@/services/APIClient';
import { ChannelInfo, channelTypeToString, newChannelInfo } from '@/services/Model';
import { ref } from 'vue';
import { ChannelTypePerson } from 'wukongimjssdk';

const channelInfos = ref<Array<ChannelInfo>>([])
const channelType = ref<number>(2)
const channelID = ref<string>("")
const fromUID = ref<string>("")
const toUID = ref<string>("")
const errMsg = ref<string>()

const requestChannels = async (chID: string, chType: number) => {
    console.log("chID--->",chID)
    const result = await APIClient.shared.get('/api/channels', {
        param: { "channel_id": chID, "channel_type": chType },
    }).catch((err) => {
        errMsg.value = err.msg
        channelInfos.value = []
    })
    if (result) {
        errMsg.value = ""
        channelInfos.value = [newChannelInfo(result)]
    }
}

const onSearch = () => {
    if(channelType.value.toString() === ChannelTypePerson.toString()) {
        requestChannels(`${fromUID.value}@${toUID.value}`, ChannelTypePerson)
    }else {
        requestChannels(channelID.value, channelType.value)
    }
    
}
</script>

<template>
    <div>
        <div className="join">
            <div>
                <div v-if="channelType.toString()!=='1'">
                    <input className="input input-bordered join-item" placeholder="频道ID" v-model="channelID" />
                </div>
                <div v-if="channelType.toString()==='1'">
                    <input className="input input-bordered join-item" placeholder="发送者UID" v-model="fromUID" />
                    <input className="input input-bordered join-item" placeholder="接受者UID" v-model="toUID" />
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
            <div class="overflow-x-auto min-h-[30rem]">
                <table class="table">
                    <thead>
                        <tr>
                            <th>频道ID</th>
                            <th>频道类型</th>
                            <th>订阅者</th>
                            <th>黑名单</th>
                            <th>白名单</th>
                            <th>超级频道</th>
                            <th>封禁</th>
                            <th>最大序号</th>
                            <th>槽</th>
                        </tr>
                    </thead>
                    <tbody>
                        <tr v-if="errMsg && errMsg !== ''" >
                            <div class="w-full  absolute text-center p-10">{{ errMsg }}</div>
                        </tr>
                        <tr v-for="channelInfo in channelInfos">
                            <td>{{ channelInfo.channelID }}</td>
                            <td>{{ channelTypeToString(channelInfo.channelType) }}</td>
                            <td>{{ channelInfo.subscribers }}</td>
                            <td>{{ channelInfo.denyList || '无' }}</td>
                            <td>{{ channelInfo.allowList || '无' }}</td>
                            <td>{{ channelInfo.large ? '是' : '否' }}</td>
                            <td>{{ channelInfo.ban ? '是' : '否' }}</td>
                            <td>{{ channelInfo.lastMsgSeq }}</td>
                            <td>{{ channelInfo.slotNum }}</td>
                        </tr>
                    </tbody>
                </table>
            </div>
        </div>
    </div>
</template>