<script setup lang="ts">
import { onMounted, ref } from 'vue';
import API from '../../services/API';
import { useRouter } from "vue-router";



const deviceTotal = ref<any>({}); // 设备列表
const uid = ref<string>(''); // 用户UID
const offsetCreatedAt = ref(0); // 偏移量
const pre = ref<boolean>() // 是否向上分页
const pageSize = ref(20); // 每页显示数量
const currentPage = ref(1); // 当前页
const hasNext = ref<boolean>(true) // 是否有下一页
const hasPrev = ref<boolean>(false) // 是否有上一页


const router = useRouter()

const query = router.currentRoute.value.query; //查询参数

if (query.uid) {
    uid.value = query.uid as string
}


onMounted(() => {
    searchDevice()
});

const searchDevice = () => {
    API.shared.devices({
        uid: uid.value,
        offsetCreatedAt: offsetCreatedAt.value,
        pre: pre.value,
        limit: pageSize.value
    }).then((res) => {
        deviceTotal.value = res
        hasNext.value = deviceTotal.value?.more === 1
        hasPrev.value = currentPage.value > 1
        if(pre.value) { // 如果是向上翻页，则有下页数据
            hasNext.value = true
        }
    }).catch((err) => {
        alert(err)
    })
}

const onUidSearch = (e: any) => {
    uid.value = e.target.value
    searchDevice()
}


const prevPage = () => {
    if (currentPage.value <= 1) {
        hasPrev.value = false
        return
    }
    hasPrev.value = true
    currentPage.value -= 1
    pre.value = true
    if (deviceTotal.value?.data?.length > 0) {
        offsetCreatedAt.value = deviceTotal.value.data[0].created_at
    }
    searchDevice()
}

const nextPage = () => {
    if (deviceTotal.value?.more === 0 && !pre.value) {
        return
    }
    currentPage.value += 1
    pre.value = false
    if (deviceTotal.value?.data?.length > 0) {
        offsetCreatedAt.value = deviceTotal.value.data[deviceTotal.value.data.length - 1].created_at
    }
    searchDevice()
}

</script>

<template>

    <div>
        <div class="overflow-x-auto h-5/6">
            <div class="flex flex-wrap gap-4">
                <div class="text-sm ml-4">
                    <label>用户UID</label>
                    <input type="text" placeholder="输入" class="input input-bordered  select-sm ml-2"
                        v-on:change="onUidSearch" v-model="uid" />
                </div>
            </div>
            <table class="table mt-5 table-pin-rows">
                <thead>
                    <tr>
                        <th>
                            <div class="flex items-center">
                                所属用户
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                设备标识
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                设备等级
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                设备Token
                            </div>
                        </th>
                        <!-- <th>
                            <div class="flex items-center">
                                连接数
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                发出消息数
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                接受消息数
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                发出消息大小
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                接受消息大小
                            </div>
                        </th> -->
                        <th>
                            <div class="flex items-center">
                                创建时间
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                更新时间
                            </div>
                        </th>
                        <!-- <th>
                            <div class="flex items-center">
                                操作
                            </div>
                        </th> -->
                    </tr>
                </thead>
                <tbody>
                    <tr v-for="device in deviceTotal.data">
                        <td>
                            {{ device.uid }}
                        </td>
                        <td>{{ device.device_flag_format }}</td>
                        <td>{{ device.device_level_format }}</td>
                        <td>{{ device.token }}</td>
                        <!-- <td>{{ device.conn_count }}</td>
                        <td>{{ device.send_msg_count }}</td>
                        <td>{{ device.recv_msg_count }}</td>
                        <td>{{ device.send_msg_bytes }}</td>
                        <td>{{ device.recv_msg_bytes }}</td> -->
                        <td>{{ device.created_at_format }}</td>
                        <td>{{ device.updated_at_format }}</td>
                        <!-- <td class="flex">
                            <button class="btn btn-sm btn-warning">踢出</button>
                        </td> -->
                    </tr>

                </tbody>
            </table>
        </div>
        <div class="flex justify-end mt-10 mr-10">
            <div className="join">
                <button :class="{ 'join-item btn': true, 'btn-disabled': !hasPrev }" v-on:click="prevPage">«</button>
                <button className="join-item btn">{{ currentPage }}</button>
                <button :class="{ 'join-item btn': true, 'btn-disabled': !hasNext }" v-on:click="nextPage">»</button>
            </div>
        </div>
    </div>
</template>