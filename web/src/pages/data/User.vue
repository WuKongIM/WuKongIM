<script setup lang="ts">
import { onMounted, ref } from 'vue';
import API from '../../services/API';

const userTotal = ref<any>({}); // 用户列表
const currentPage = ref(1); // 当前页
const pageSize = ref(20); // 每页显示数量
const offsetId = ref(0); // 偏移量
const pre = ref(false); // 上一页


onMounted(() => {
    searchUser()
});

const searchUser = () => {
    API.shared.users({
        offsetId: offsetId.value,
        pre: pre.value,
        limit: pageSize.value,
    }).then((res) => {
        userTotal.value = res
    }).catch((err) => {
        alert(err)
    })
}

// 上一页
const prevPage = () => {
    currentPage.value -= 1
    offsetId.value = userTotal.value.data[0].id
    pre.value = true
    searchUser()
}

// 下一页
const nextPage = () => {
    if (userTotal.value.data.length < pageSize.value) {
        alert("没有更多数据了")
        return
    }
    currentPage.value += 1
    offsetId.value = userTotal.value.data[userTotal.value.data.length - 1].id
    pre.value = false
    searchUser()
}

</script>

<template>
    <div>
        <div class="overflow-x-auto h-5/6">
            <div class="flex flex-wrap gap-4">
            </div>
            <table class="table mt-10 table-pin-rows">
                <thead>
                    <tr>
                        <th>
                            <div class="flex items-center">
                                用户UID
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                设备数
                            </div>
                        </th>
                        <th>
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
                        </th>
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
                        <th>
                            <div class="flex items-center">
                                操作
                            </div>
                        </th>
                    </tr>
                </thead>
                <tbody>
                    <!-- row 1 -->
                    <tr v-for="user in userTotal.data">
                        <td>
                            {{ user.uid }}
                        </td>
                        <td>{{ user.device_count }}</td>
                        <td>{{ user.conn_count }}</td>
                        <td>{{ user.send_msg_count }}</td>
                        <td>{{ user.recv_msg_count }}</td>
                        <td>{{ user.send_msg_bytes }}</td>
                        <td>{{ user.recv_msg_bytes }}</td>
                        <td>{{ user.created_at_format }}</td>
                        <td>{{ user.updated_at_format }}</td>
                        <td class="flex">
                            <button class="btn btn-link btn-sm"
                                @click="$router.push(`device?uid=${user.uid}`)">白名单</button>
                            <button class="btn btn-link btn-sm"
                                @click="$router.push(`device?uid=${user.uid}`)">黑明单</button>
                            <button class="btn btn-link btn-sm"
                                @click="$router.push(`device?uid=${user.uid}`)">设备</button>
                            <button class="btn btn-link btn-sm"
                            @click="$router.push(`conversation?uid=${user.uid}`)"
                            >最近会话</button>
                        </td>


                    </tr>

                </tbody>
            </table>

        </div>
        <div class="flex justify-end mt-10 mr-10">
            <div className="join">
                <button className="join-item btn" v-on:click="prevPage">«</button>
                <button className="join-item btn">{{ currentPage }}</button>
                <button className="join-item btn" v-on:click="nextPage">»</button>
            </div>
        </div>
    </div>
</template>