<script setup lang="ts">

import { onMounted, ref } from 'vue';
import API from '../../services/API';
import { useRouter } from "vue-router";

const router = useRouter()

const nodeTotal = ref<any>({});
const loading = ref<boolean>(false);

onMounted(() => {
    console.log('mounted');
    loading.value = true;
    API.shared.nodes().then((res) => {
        nodeTotal.value = res
    }).catch((err) => {
        if(err.msg) {
            alert(err)
        }
        
    }).finally(() => {
        loading.value = false;
    })
});

const getRole = (node: any) => {
    if (node.is_leader == 1) {
        return '领导'
    }
    if (node.role == 1) {
        return '代理'
    }
    return '副本'
}

const onLog = (node: any) => {
    console.log(node)

    router.push({
        path: '/cluster/log',
        query: {
            node_id: node.id
        }
    })

}

</script>

<template>
    <div>
        <div class="overflow-x-auto">
            <table class="table">
                <!-- head -->
                <thead>
                    <tr>
                        <th>节点ID</th>
                        <th>角色</th>
                        <th>任期</th>
                        <th>槽领导/槽数量</th>
                        <th>投票权</th>
                        <th>在线</th>
                        <th>离线次数</th>
                        <th>运行时间</th>
                        <th>地址</th>
                        <th>程序版本</th>
                        <th>配置版本</th>
                        <th>状态</th>
                        <th>操作</th>
                    </tr>
                </thead>
                <tbody>
                    <!-- row 1 -->
                    <tr v-for="node in nodeTotal.data">
                        <td class="text-blue-800">
                            <RouterLink to="/node/detail">{{ node.id }}</RouterLink>
                        </td>
                        <td :class="node.is_leader == 1 ? 'text-blue-400' : ''">{{ getRole(node) }}</td>
                        <td>{{node.term}}</td>
                        <td>{{ node.slot_leader_count }}/{{ node.slot_count }}</td>
                        <td>{{ node.allow_vote == 1 ? '有' : '无' }}</td>
                        <td :class="node.online == 1 ? 'text-green-500' : ''">{{ node.online == 1 ? '在线' : '离线' }}</td>
                        <td>{{node.offline_count>0?`${node.offline_count}(${node.last_offline})`:`0`}}</td>
                        <td>{{ node.uptime }}</td>
                        <td>{{ node.cluster_addr }}</td>
                        <td>{{ node.app_version }}</td>
                        <td>{{ node.config_version }}</td>
                        <td>{{ node.status_format }}</td>
                        <td>
                            <button class="btn btn-sm btn-primary" v-on:click="()=>onLog(node)">日志</button>
                        </td>
                    </tr>
                </tbody>
            </table>
            <div class="flex flex-col gap-4 w-full mt-2" v-if="loading">
                <div class="skeleton h-6 w-full"></div>
                <div class="skeleton h-6 w-full"></div>
                <div class="skeleton h-6 w-full"></div>
                <div class="skeleton h-6 w-full"></div>
                <div class="skeleton h-6 w-full"></div>
                <div class="skeleton h-6 w-full"></div>
            </div>
        </div>
    </div>
</template>