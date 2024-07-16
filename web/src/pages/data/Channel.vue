<script setup lang="ts">
import API from '../../services/API';
import { onMounted, ref } from 'vue';

const channelTotal = ref<any>({}); // 频道列表

const currentUids = ref<string[]>() // 当前用户列表
const loadingOfSubscribers = ref<boolean>(false) // 是否正在加载订阅者
const loadingOfDenylist = ref<boolean>(false) // 是否正在加载黑名单
const loadingOfAllowlist = ref<boolean>(false) // 是否正在加载白名单
const channelType = ref<number>(0) // 频道类型
const channelId = ref<string>() // 频道ID

onMounted(() => {
    searchChannels()
})

const searchChannels = () => {
    API.shared.searchChannels({
        channelId: channelId.value,
        channelType: channelType.value
    }).then((res) => {
        channelTotal.value = res
    }).catch((err) => {
        alert(err)
    })
}

const getSubscribers = (channelId: string, channelType: number) => {
    loadingOfSubscribers.value = true;
    return API.shared.subscribers(channelId, channelType).then((res) => {
        currentUids.value = res
    }).catch((err) => {
        alert(err)
    }).finally(() => {
        loadingOfSubscribers.value = false;
    })
}

const getDenylist = (channelId: string, channelType: number) => {
    loadingOfDenylist.value = true;
    return API.shared.denylist(channelId, channelType).then((res) => {
        currentUids.value = res
    }).catch((err) => {
        alert(err)
    }).finally(() => {
        loadingOfDenylist.value = false;
    })
}

const getAllowlist = (channelId: string, channelType: number) => {
    loadingOfAllowlist.value = true;
    return API.shared.allowlist(channelId, channelType).then((res) => {
        currentUids.value = res
    }).catch((err) => {
        alert(err)
    }).finally(() => {
        loadingOfAllowlist.value = false;
    })
}

// 显示订阅者
const onShowSubscriber = (channelId: string, channelType: number) => {
    getSubscribers(channelId, channelType).then(() => {
        const dialog = document.getElementById('userlist') as HTMLDialogElement;
        dialog.showModal();
    })

}

const onShowDenylist = (channelId: string, channelType: number) => {
    getDenylist(channelId, channelType).then(() => {
        const dialog = document.getElementById('userlist') as HTMLDialogElement;
        dialog.showModal();
    })
}

const onShowAllowlist = (channelId: string, channelType: number) => {
    getAllowlist(channelId, channelType).then(() => {
        const dialog = document.getElementById('userlist') as HTMLDialogElement;
        dialog.showModal();
    })
}

const onChannelTypeSearch = (e: any) => {
    channelType.value = e.target.value
    searchChannels()
}

const onChannelIdSearch = (e: any) => {
    channelId.value = e.target.value
    searchChannels()
}

</script>

<template>
    <div>
        <div class="overflow-x-auto h-5/6">
            <div class="flex flex-wrap gap-4">
                <!-- 频道类型 -->
                <div class="text-sm ml-10">
                    <label>频道</label>
                    <select class="select select-bordered  max-w-xs select-sm w-20 ml-2"
                        v-on:change="onChannelTypeSearch" v-model="channelType">
                        <option value="0">所有</option>
                        <option value="1">个人</option>
                        <option value="2">群聊</option>
                        <option value="3">客服</option>
                        <option value="4">社区</option>
                        <option value="5">话题</option>
                        <option value="6">资讯</option>
                        <option value="7">数据</option>
                    </select>
                    <input type="text" placeholder="频道ID" class="input input-bordered  select-sm ml-2"
                        v-on:change="onChannelIdSearch" />
                </div>

            </div>
            <table class="table mt-10 table-pin-rows">
                <!-- head -->
                <thead>
                    <tr>
                        <th>
                            <div class="flex items-center">
                                频道ID
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                频道类型
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                订阅者数量
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                黑名单数量
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                白名单数量
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                状态
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                最大序号
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                最后消息时间
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                槽位
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
                    <tr v-for="channel in channelTotal.data">
                        <td>
                            {{ channel.channel_id }}
                        </td>
                        <td>{{ channel.channel_type }}</td>
                        <td>{{ channel.subscriber_count }}</td>
                        <td>{{ channel.denylist_count }}</td>
                        <td>{{ channel.allowlist_count }}</td>
                        <td>{{ channel.status_format }}</td>
                        <td>{{ channel.last_msg_seq }}</td>
                        <td>{{ channel.last_msg_time_format }}</td>
                        <td>{{ channel.slot }}</td>
                        <td class="flex">
                            <button class="btn btn-link btn-sm"
                                :class="{ 'loading loading-spinner': loadingOfSubscribers }"
                                v-on:click="() => { onShowSubscriber(channel.channel_id, channel.channel_type) }">订阅者</button>
                            <button class="btn btn-link btn-sm"
                                :class="{ 'loading loading-spinner': loadingOfAllowlist }" v-on:click="() => {
                                    onShowAllowlist(channel.channel_id, channel.channel_type)

                                }">白名单</button>
                            <button class="btn btn-link btn-sm"
                                :class="{ 'loading loading-spinner': loadingOfDenylist }" v-on:click="() => {
                                    onShowDenylist(channel.channel_id, channel.channel_type)
                                }">黑名单</button>
                        </td>
                    </tr>

                </tbody>
            </table>
        </div>
        <dialog id="userlist" class="modal">
            <div class="modal-box flex flex-wrap gap-2">
                <div v-if="currentUids?.length == 0">无数据</div>
                <a class="link" v-for="uid in currentUids">{{ uid }}</a>
            </div>
            <form method="dialog" class="modal-backdrop">
                <button>close</button>
            </form>
        </dialog>
    </div>
</template>