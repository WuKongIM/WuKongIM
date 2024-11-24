<script setup lang="ts">

import { onMounted, ref } from 'vue';
import API from '../../services/API';
import VueJsonPretty from 'vue-json-pretty';
import 'vue-json-pretty/lib/styles.css';
import { useRouter } from "vue-router";
import App, { ActionWrite } from '../../services/App';
import { alertNoPermission } from '../../services/Utils';

const router = useRouter()

const nodeTotal = ref<any>({}); // 节点列表
const selectedNodeId = ref<number>(1) // 选中的节点ID
const channelTotal = ref<any>({}); // 频道列表
const loading = ref<boolean>(false);
const selectedChannel = ref<any>({}); // 选中的频道
const selectedMigrateFrom = ref<number>() // 选中的源节点ID
const selectedMigrateTo = ref<number>() // 选中的目标节点ID
const config = ref<any>({}); // 频道配置
const channelId = ref<string>() // 接受频道id
const channelType = ref<number>() // 频道类型

const pageSize = ref<number>(20) // 每页数量
const currentPage = ref(1); // 当前页

const replicas = ref<any>({}); // 副本
const offsetCreatedAt = ref(0); // 偏移量
const pre = ref<boolean>() // 是否向上分页
const hasNext = ref<boolean>(true) // 是否有下一页
const hasPrev = ref<boolean>(false) // 是否有上一页



onMounted(() => {

    API.shared.simpleNodes().then((res) => {
        nodeTotal.value = res
        if (nodeTotal.value.data.length > 0) {
            selectedNodeId.value = nodeTotal.value.data[0].id
        }
        loadNodeChannels()
    }).catch((err) => {
        alert(err)
    })
})

const onNodeChange = (e: any) => {
    resetFilter()
    selectedNodeId.value = parseInt(e.target.value)
    loadNodeChannels()
}

const loadNodeChannels = () => {
    loading.value = true;
    API.shared.nodeChannelConfigs({
        channelId: channelId.value,
        channelType: channelType.value,
        nodeId: selectedNodeId.value,
        limit: pageSize.value,
        offsetCreatedAt: offsetCreatedAt.value,
        pre: pre.value
    }).then((res) => {
        channelTotal.value = res
        hasNext.value = channelTotal.value?.more === 1
        hasPrev.value = currentPage.value > 1
        if (pre.value) { // 如果是向上翻页，则有下页数据
            hasNext.value = true
        }
    }).catch((err) => {
        alert(err)
    }).finally(() => {
        loading.value = false;
    })
}

const onShowMigrateModal = (ch: any) => {

    if (!App.shard().loginInfo.hasPermission('channelMigrate', ActionWrite)) {
        alertNoPermission()
        return
    }

    selectedChannel.value = ch;
    const migrateModal = document.getElementById('migrateModal') as HTMLDialogElement;
    migrateModal.showModal();
}

const onMigrate = () => {

    const migrateModal = document.getElementById('migrateModal') as HTMLDialogElement;
    migrateModal.close();

    API.shared.migrateChannel({
        channelId: selectedChannel.value.channel_id,
        channelType: selectedChannel.value.channel_type,
        migrateFrom: selectedMigrateFrom.value || 0,
        migrateTo: selectedMigrateTo.value || 0,

    }).then((_) => {
        loadNodeChannels()
    }).catch((err) => {
        alert(err.msg)
    })
}

const onChannelClusterConfig = (ch: any) => {

    resetFilter()

    selectedChannel.value = ch;
    

    API.shared.channelClusterConfig({
        channelId: selectedChannel.value.channel_id,
        channelType: selectedChannel.value.channel_type,
        nodeId: selectedNodeId.value
    }).then((res) => {
        config.value = res
        const modal = document.getElementById('channelClusterModal') as HTMLDialogElement;
        modal.showModal();
    }).catch((err) => {
        alert(err)
    })


}

const onChannelStartOrStop = (ch: any) => {

    if (!App.shard().loginInfo.hasPermission('channelStart', ActionWrite)) {
        alertNoPermission()
        return
    }

    const req = {
        channelId: ch.channel_id,
        channelType: ch.channel_type,
        nodeId: selectedNodeId.value
    }
    if (ch.active == 1) {
        API.shared.channelStop(req).then((_) => {
            loadNodeChannels()
        }).catch((err) => {
            alert(err.msg)
        })
    } else {
        API.shared.channelStart(req).then((_) => {
            loadNodeChannels()
        }).catch((err) => {
            alert(err.msg)
        })
    }
}

const resetFilter = () => {
    currentPage.value = 1
    offsetCreatedAt.value = 0
    pre.value = false
    hasNext.value = true
    hasPrev.value = false
}



// 上一页
const prevPage = () => {
    if (currentPage.value <= 1) {
        hasPrev.value = false
        return
    }
    hasPrev.value = true
    currentPage.value -= 1
    pre.value = true
    if (channelTotal.value?.data?.length > 0) {
        offsetCreatedAt.value = channelTotal.value.data[0].created_at
    }
    loadNodeChannels()
}

// 下一页
const nextPage = () => {
    if (channelTotal.value?.more === 0 && !pre.value) {
        return
    }
    currentPage.value += 1
    pre.value = false
    if (channelTotal.value?.data?.length > 0) {
        offsetCreatedAt.value = channelTotal.value.data[channelTotal.value.data.length - 1].created_at
    }
    loadNodeChannels()
}


const onChannelIdSearch = (e: any) => {
    channelId.value = e.target.value
    if (!channelId.value || channelId.value.trim() == '') {
        channelType.value = 0
    }
    currentPage.value = 1
    loadNodeChannels()
}

const onChannelTypeSearch = (e: any) => {
    channelType.value = e.target.value
    if (!channelId.value || channelId.value.trim() == '') {
        return
    }
    currentPage.value = 1
    loadNodeChannels()
}

// const onRunningSearch = (e: any) => {
//     running.value = e.target.checked
//     currentPage.value = 1
//     loadNodeChannels()
// }

// 查看副本
const onReplicas = (ch: any) => {
    const modal = document.getElementById('replicas') as HTMLDialogElement;
    modal.showModal();

    API.shared.channelReplicas({
        channelId: ch.channel_id,
        channelType: ch.channel_type,
    }).then((res) => {
        replicas.value = res
        const modal = document.getElementById('replicas') as HTMLDialogElement;
        modal.showModal();
    }).catch((err) => {
        alert(err)
    })

}

// 查看消息
const onMessage = (ch: any) => {
    router.push({
        path: '/data/message',
        query: {
            channelId: ch.channel_id,
            channelType: ch.channel_type,
        }
    })

}

</script>

<template>
    <div>
        <div class="overflow-x-auto h-5/6">
            <div class="flex">
                <div class="text-sm ml-3">
                    <label>节点</label>
                    <select class="select select-bordered  max-w-xs select-sm w-40 ml-2" v-on:change="onNodeChange">
                        <option v-for="node in nodeTotal.data" :selected="node.id == selectedNodeId" :value="node.id">{{
                            node.id }}
                        </option>
                    </select>
                </div>

                <!-- 频道 -->
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
                    <input type="text" placeholder="输入" class="input input-bordered  select-sm ml-2"
                        v-on:change="onChannelIdSearch" />
                </div>

            </div>

            <table class="table mt-10 table-pin-rows">
                <!-- head -->
                <thead>
                    <tr>
                        <th>频道ID</th>
                        <th>频道类型</th>
                        <th>所属槽</th>
                        <th>槽领导</th>
                        <th>频道领导</th>

                        <th>任期</th>
                        <th>副本节点</th>
                        <th>消息高度</th>
                        <th>最后消息时间</th>
                        <th>是否运行</th>
                        <th>状态</th>
                        <th>操作</th>
                    </tr>
                </thead>
                <tbody>
                    <!-- row 1 -->
                    <tr v-for="channel in channelTotal.data">
                        <td class="text-blue-800">{{ channel.channel_id }}</td>
                        <td>{{ channel.channel_type_format }}</td>
                        <td>{{ channel.slot_id }}</td>
                        <td>{{ channel.slot_leader_id }}</td>
                        <td>{{ channel.leader_id }}</td>
                        <td>{{ channel.term }}</td>
                        <td>{{ channel.replicas }}&nbsp;&nbsp;<label class="text-red-500"
                                v-if="channel.migrate_from != 0">[{{ channel.migrate_from }} 迁移至 {{ channel.migrate_to
                                }}
                                ]</label></td>
                        <td>{{ channel.last_message_seq }}</td>
                        <td>{{ channel.last_append_time }}</td>
                        <td :class="channel.active == 1 ? 'text-green-500' : 'text-red-500'">{{ channel.active_format }}
                        </td>
                        <td :class="channel.status == 0 ? 'text-green-500' : 'text-red-500'">{{ channel.status_format }}
                        </td>
                        <td class="flex flex-wrap gap-2">

                            <button
                                :class="channel.active == 1 ? 'btn btn-primary btn-sm btn-secondary' : 'btn btn-primary btn-sm'"
                                v-on:click="() => onChannelStartOrStop(channel)">{{ channel.active
                                    == 1 ? "停止" : "开始" }}</button>
                            <button class="btn btn-primary btn-sm"
                                v-on:click="() => onShowMigrateModal(channel)">迁移</button>
                            <button class="btn btn-primary btn-sm"
                                v-on:click="() => onChannelClusterConfig(channel)">配置</button>
                            <button class="btn btn-primary btn-sm" v-on:click="() => onReplicas(channel)">副本</button>
                            <button class="btn btn-primary btn-sm" v-on:click="() => onMessage(channel)">消息</button>
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
                <div class="skeleton h-6 w-full"></div>
            </div> -->
        </div>

        <div class="flex items-center justify-end mt-10">

            <div class="flex">
                <div className="join">
                    <button :class="{ 'join-item btn': true, 'btn-disabled': !hasPrev }"
                        v-on:click="prevPage">«</button>
                    <button className="join-item btn">{{ currentPage }}</button>
                    <button :class="{ 'join-item btn': true, 'btn-disabled': !hasNext }"
                        v-on:click="nextPage">»</button>
                </div>
            </div>
        </div>


        <dialog id="migrateModal" class="modal">
            <div class="modal-box flex flex-wrap gap-2 justify-center">
                <select class="select select-bordered" v-model="selectedMigrateFrom">
                    <option v-for="nodeId in selectedChannel.replicas" :value="nodeId">{{ nodeId }}</option>
                </select>
                <div class="flex items-center">&nbsp;&nbsp;迁移至&nbsp;&nbsp;</div>
                <select class="select select-bordered" v-model="selectedMigrateTo">
                    <option v-for="destNode in nodeTotal.data" :value="destNode.id">{{ destNode.id }}</option>
                </select>
                &nbsp;&nbsp;
                <button className="btn-primary btn" v-on:click="onMigrate">确认</button>
                <!-- <a class="link" v-for="uid in currentUids">{{ uid }}</a> -->
            </div>
            <form method="dialog" class="modal-backdrop">
                <button>close</button>
            </form>
        </dialog>

        <dialog id="channelClusterModal" class="modal">
            <div class="modal-box flex flex-wrap gap-2 justify-center">
                <vue-json-pretty :data="config" class="overflow-auto" />
            </div>
            <form method="dialog" class="modal-backdrop">
                <button>close</button>
            </form>
        </dialog>

        <dialog id="replicas" class="modal">
            <div class="modal-box flex flex-wrap gap-2 justify-center">
                <div class="overflow-x-auto">
                    <table class="table table-xs">
                        <thead>
                            <tr>
                                <th>节点ID</th>
                                <th>节点角色</th>
                                <th>消息高度</th>
                                <th>最后消息时间</th>
                                <th>是否运行</th>
                            </tr>
                        </thead>
                        <tbody>
                            <tr v-for="replica in replicas">
                                <td>{{ replica.replica_id }}</td>
                                <td>{{ replica.role_format }}</td>
                                <td>{{ replica.last_msg_seq }}</td>
                                <td>{{ replica.last_msg_time_format }}</td>
                                <td :class="replica.running == 1 ? 'text-green-500' : 'text-red-500'">
                                    {{ replica.running == 1 ? '运行中' : '未运行' }}</td>
                            </tr>
                        </tbody>
                    </table>
                </div>
            </div>
            <form method="dialog" class="modal-backdrop">
                <button>close</button>
            </form>
        </dialog>
    </div>
</template>
