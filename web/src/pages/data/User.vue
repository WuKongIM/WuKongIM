<script setup lang="ts">
import { onMounted, ref } from 'vue';
import API from '../../services/API';

const userTotal = ref<any>({}); // 用户列表
const currentPage = ref(1); // 当前页
const pageSize = ref(20); // 每页显示数量
const offsetCreatedAt = ref(0); // 偏移量
const pre = ref(false); // 上一页
const uid = ref<string>(''); // 用户UID
const currentUids = ref<string[]>() // 当前用户列表

const hasNext = ref<boolean>(true) // 是否有下一页
const hasPrev = ref<boolean>(false) // 是否有上一页


onMounted(() => {
    searchUser()
});

const searchUser = () => {

    API.shared.users({
        uid: uid.value || "",
        offsetCreatedAt: offsetCreatedAt.value,
        pre: pre.value,
        limit: pageSize.value,
    }).then((res) => {
        userTotal.value = res
        hasNext.value = userTotal.value?.more === 1
        hasPrev.value = currentPage.value > 1
        if(pre.value) { // 如果是向上翻页，则有下页数据
            hasNext.value = true
        }
    }).catch((err) => {
        alert(err)
    })
}

// 通过uid搜索
const onUidSearch = (e: any) => {
    resetFilter()
    uid.value = e.target.value
    searchUser()
}

// 显示白名单
const onShowAllowlist = (uid:string) => {
    getAllowlist(uid).then(() => {
        const dialog = document.getElementById('userlist') as HTMLDialogElement;
        dialog.showModal();
    })
}


// 获取白名单列表
const getAllowlist = (uid: string) => {
    return API.shared.allowlist(uid, 1).then((res) => {
        currentUids.value = res
    }).catch((err) => {
        alert(err)
    })
}

// 显示黑名单
const onShowDenylist = (uid:string) => {
    getDenylist(uid).then(() => {
        const dialog = document.getElementById('userlist') as HTMLDialogElement;
        dialog.showModal();
    })
}

const getDenylist = (uid: string) => {
    return API.shared.denylist(uid, 1).then((res) => {
        currentUids.value = res
    }).catch((err) => {
        alert(err)
    })
}


const resetFilter = () => {
    currentPage.value = 1
    pre.value = false
    offsetCreatedAt.value = 0
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
    if (userTotal.value?.data?.length > 0) {
        offsetCreatedAt.value = userTotal.value.data[0].created_at
    }
    searchUser()
}

// 下一页
const nextPage = () => {
    if (userTotal.value?.more === 0 && !pre.value) {
        return
    }
    currentPage.value += 1
    pre.value = false
    if (userTotal.value?.data?.length > 0) {
        offsetCreatedAt.value = userTotal.value.data[userTotal.value.data.length - 1].created_at
    }

    searchUser()
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
                                用户UID
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                槽
                            </div>
                        </th>
                        <!-- <th>
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
                        <td>
                            {{ user.slot }}
                        </td>
                        <!-- <td>{{ user.device_count }}</td>
                        <td>{{ user.conn_count }}</td>
                        <td>{{ user.send_msg_count }}</td>
                        <td>{{ user.recv_msg_count }}</td>
                        <td>{{ user.send_msg_bytes }}</td>
                        <td>{{ user.recv_msg_bytes }}</td> -->
                        <td>{{ user.created_at_format }}</td>
                        <td>{{ user.updated_at_format }}</td>
                        <td class="flex">
                            <button class="btn btn-link btn-sm"
                                @click="()=>onShowAllowlist(user.uid)">白名单</button>
                            <button class="btn btn-link btn-sm"
                                @click="()=>onShowDenylist(user.uid)">黑名单</button>
                            <button class="btn btn-link btn-sm"
                                @click="$router.push(`device?uid=${user.uid}`)">设备</button>
                            <button class="btn btn-link btn-sm"
                                @click="$router.push(`conversation?uid=${user.uid}`)">最近会话</button>
                        </td>


                    </tr>

                </tbody>
            </table>

        </div>
        <div class="flex justify-end mt-10 mr-10">
            <div className="join">
                <button :class="{ 'join-item btn': true, 'btn-disabled': !hasPrev  }" v-on:click="prevPage">«</button>
                <button className="join-item btn">{{ currentPage }}</button>
                <button :class="{ 'join-item btn': true, 'btn-disabled': !hasNext }" v-on:click="nextPage">»</button>
            </div>
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