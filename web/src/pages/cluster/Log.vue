<script setup lang="ts">

import { onMounted, ref } from 'vue';
import API from '../../services/API';
import { useRouter } from "vue-router";

const router = useRouter()
const query = router.currentRoute.value.query; //查询参数

const logTotal = ref<any>({});

const nextIndex = ref<number>(0);
const preIndex = ref<number>(0);

onMounted(() => {
    console.log('mounted');
    loadData();
});

const loadData = () => {
    const nodeId = Number(query.node_id);
    const slot = Number(query.slot);
    const logType = query.log_type as string || "";

    API.shared.clusterLogs({
        nodeId: nodeId,
        slot: slot,
        next: nextIndex.value,
        pre: preIndex.value,
        logType: logType
    }).then((res) => {
        logTotal.value = res
    });
}

const prevPage = () => {
    preIndex.value = logTotal.value.pre || 0;
    nextIndex.value =  0;
    loadData();
}

const nextPage = () => {
    preIndex.value = 0;
    nextIndex.value = logTotal.value.next || 0;
    loadData();
}

</script>


<template>
    <div>
        <div class="overflow-x-auto h-5/6">
            <table class="table">
                <!-- head -->
                <thead>
                    <tr>
                        <th>日志Id</th>
                        <th>日志下标</th>
                        <th>日志任期</th>
                        <th>命令类型</th>
                        <th>命令内容</th>
                        <th>日志时间</th>
                        <th>状态</th>
                    </tr>
                </thead>
                <tbody>
                    <!-- row 1 -->
                    <tr v-for="log in logTotal.logs">
                        <td>{{ log.id }}</td>
                        <td>{{ log.index }}</td>
                        <td>{{ log.term }}</td>
                        <td>{{ log.cmd }}</td>
                        <td>{{ log.data }}</td>
                        <td>{{ log.time_format }}</td>
                        <td v-if="log.index <= logTotal.applied">已应用</td>
                        <td v-if="log.index > logTotal.applied" class="text-red-500">未应用</td>
                    </tr>

                </tbody>
            </table>
        </div>
        <div class="flex items-center mt-10 justify-end">

            <div class="flex">
                <div className="join">
                    <button  :class="{'join-item btn':true,'btn-disabled':logTotal.logs && logTotal.logs.length>0&&logTotal.logs[0].index==logTotal.last}" v-on:click="prevPage">«</button>
                    <!-- <button className="join-item btn">{{ currentPage }}</button> -->
                    <button :class="{'join-item btn':true,'btn-disabled':logTotal.logs && logTotal.logs.length>0&&logTotal.logs[logTotal.logs.length-1].index==1}" v-on:click="nextPage">»</button>
                </div>
            </div>
        </div>
    </div>
</template>