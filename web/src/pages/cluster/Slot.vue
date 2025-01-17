<script setup lang="ts">

import { onMounted, ref } from 'vue';
import API from '../../services/API';
import { useRouter } from "vue-router";
import App, { ActionWrite } from '../../services/App';
import { alertNoPermission } from '../../services/Utils';

const router = useRouter()

const nodeTotal = ref<any>({}); // 节点列表

const slotTotal = ref<any>({});
const loading = ref<boolean>(false);

const orderBy = ref<string>('') // 排序字段
const desc = ref<boolean>(false) // 是否降序

const selectedMigrateFrom = ref<number>() // 选中的源节点ID
const selectedMigrateTo = ref<number>() // 选中的目标节点ID
const selectedSlot = ref<any>({}); // 选中的槽

onMounted(() => {
   
    loadData()

    API.shared.simpleNodes().then((res) => {
        nodeTotal.value = res
    }).catch((err) => {
        alert(err)
    })
   
});

const loadData = () => {
    loading.value = true;
    API.shared.slots().then((res) => {
        slotTotal.value = res
    }).catch((err) => {
        alert(err)
    }).finally(() => {
        loading.value = false;
    })
}

const onSort = (by :string) => {
    orderBy.value = by
    sort()
}

const sort = () => {
    if(orderBy.value === 'channel_count') {
        if(desc.value) {
            slotTotal.value.data.sort((a: any, b: any) => {
                return a.channel_count - b.channel_count
            })
            desc.value = false
        } else {
            slotTotal.value.data.sort((a: any, b: any) => {
                return b.channel_count - a.channel_count
            })
            desc.value = true
        }
    } else if(orderBy.value === 'log_index') {
        if(desc.value) {
            slotTotal.value.data.sort((a: any, b: any) => {
                return a.log_index - b.log_index
            })
            desc.value = false
        } else {
            slotTotal.value.data.sort((a: any, b: any) => {
                return b.log_index - a.log_index
            })
            desc.value = true
        }
    }
}


const onLog = (slot: any) => {

    router.push({
        path: '/cluster/log',
        query: {
            slot: slot.id,
            node_id: slot.leader_id,
            log_type: 2,
        }
    })

}

const onShowMigrateModal = (slot: any) => {

     if(!App.shard().loginInfo.hasPermission('slotMigrate',ActionWrite)) {
        alertNoPermission()
        return
     }

    selectedSlot.value = slot
    const migrateModal = document.getElementById('migrateModal') as HTMLDialogElement
    migrateModal.showModal()
}

const onMigrate = () => {

    

    const migrateModal = document.getElementById('migrateModal') as HTMLDialogElement;
    migrateModal.close();

   API.shared.migrateSlot({
         slot: selectedSlot.value.id,
         migrateFrom: selectedMigrateFrom.value||0,
         migrateTo: selectedMigrateTo.value||0
    }).then(() => {
         loadData()
    }).catch((err) => {
         alert(err.msg)
    
   })
}


</script>

<template>
    <div>
        <div class="overflow-x-auto h-5/6">
            <div class="flex">
                <div class="text-sm ml-3">
                    <button class="btn" :onclick="() => { onSort('log_index') }">日志高度</button>
                </div>
            </div>
            <table class="table table-pin-rows">
                <!-- head -->
                <thead>
                    <tr>
                        <th>分区（槽）</th>
                        <th>领导节点</th>
                        <th>任期</th>
                        <th>副本节点</th>
                        <th>日志高度</th>
                        <th>状态</th>
                        <th>操作</th>
                    </tr>
                </thead>
                <tbody>
                    <!-- row 1 -->
                    <tr v-for="slot in slotTotal.data">
                        <td>{{ slot.id }}</td>
                        <td>{{ slot.leader_id }}</td>
                        <td>{{ slot.term }}</td>
                        <td>{{ slot.replicas }}</td>
                        <td>{{slot.log_index}}</td>
                        <td :class="slot.status===0?'text-green-500':'text-red-500'">{{slot.status_format}}</td>
                        <td class="flex flex-wrap gap-2">
                            <button class="btn btn-sm btn-primary" v-on:click="()=>onShowMigrateModal(slot)">迁移</button>
                            <button class="btn btn-sm btn-primary" v-on:click="()=>onLog(slot)">日志</button>
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
                <div class="skeleton h-6 w-full"></div>
                <div class="skeleton h-6 w-full"></div>
                <div class="skeleton h-6 w-full"></div>
                <div class="skeleton h-6 w-full"></div>
                <div class="skeleton h-6 w-full"></div>
                <div class="skeleton h-6 w-full"></div>
            </div>
        </div>

        <dialog id="migrateModal" class="modal">
            <div class="modal-box flex flex-wrap gap-2 justify-center">
                <select class="select select-bordered" v-model="selectedMigrateFrom">
                    <option v-for="nodeId in selectedSlot.replicas" :value="nodeId">{{ nodeId }}</option>
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

    </div>
</template>