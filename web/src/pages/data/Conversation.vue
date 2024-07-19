<script setup lang="ts">
import { onMounted, ref } from 'vue';
import API from '../../services/API';
import { useRouter } from "vue-router";

const conversationTotal = ref<any>({}); // 会话列表
const uid = ref<string>(); // 用户UID

const router = useRouter()

const query = router.currentRoute.value.query; //查询参数

if(query.uid){
    uid.value = query.uid as string
}

onMounted(() => {
    searchConversation()
})

const searchConversation = () => {
    API.shared.conversations({uid:uid.value}).then((res) => {
        conversationTotal.value = res
    }).catch((err) => {
        alert(err)
    })
}

const onUidSearch = (e: any) => {
    uid.value = e.target.value
    searchConversation()
}

</script>

<template>
   <div>
        <div class="overflow-x-auto h-5/6">
            <div class="flex flex-wrap gap-4">
                <div class="text-sm ml-10">
                    <label>用户UID</label>
                    <input type="text" placeholder="输入" class="input input-bordered  select-sm ml-2"
                        v-on:change="onUidSearch" v-model="uid"/>
                </div>
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
                                会话类型
                            </div>
                        </th>
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
                                最新消息序号
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                已读消息序号
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                未读数量
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                最后会话时间
                            </div>
                        </th>
                    </tr>
                </thead>
                <tbody>
                    <tr v-for="conversation in conversationTotal.data">
                        <td> {{ conversation.uid }}</td>
                        <td>{{conversation.type_format}}</td>
                        <td>{{conversation.channel_id}}</td>
                        <td>{{conversation.channel_type_format}}</td>
                        <td>{{conversation.last_msg_seq}}</td>
                        <td>{{conversation.readed_to_msg_seq}}</td>
                        <td>{{conversation.unread_count}}</td>
                        <td>{{conversation.updated_at_format}}</td>

                    </tr>

                </tbody>
            </table>

        </div>
    </div>
</template>