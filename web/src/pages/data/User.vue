<script setup lang="ts">
import { onMounted, ref } from 'vue';
import API from '../../services/API';

const userTotal = ref<any>({}); // 用户列表


onMounted(() => {
    searchUser()
});

const searchUser = () => {
    API.shared.users().then((res) => {
        userTotal.value = res
    }).catch((err) => {
        alert(err)
    })
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
                                @click="$router.push(`device?uid=${user.uid}`)">设备</button>
                            <button class="btn btn-link btn-sm"
                            @click="$router.push(`conversation?uid=${user.uid}`)"
                            >最近会话</button>
                        </td>


                    </tr>

                </tbody>
            </table>

        </div>
    </div>
</template>