<script setup lang="ts">
import { onMounted, ref } from 'vue';
import API from '../../services/API';
import VueJsonPretty from 'vue-json-pretty';
import 'vue-json-pretty/lib/styles.css';

const selectedNodeId = ref<number>(1); // 选中的节点ID
const nodeTotal = ref<any>({}); // 节点列表
const tags = ref<any[]>([]); // 标签列表
const currentTag = ref<any>({}); // 当前标签

const channelType = ref<number>(0); // 频道类型
const channelId = ref<string>(''); // 频道ID
const tagKey = ref<string>(''); // 标签key

onMounted(() => {
    API.shared.simpleNodes().then((res) => {
        nodeTotal.value = res
        if (nodeTotal.value.data.length > 0) {
            selectedNodeId.value = nodeTotal.value.data[0].id
        }
        loadTags()
    }).catch((err) => {
        alert(err)
    })
})

const onNodeChange = (e: any) => {
    selectedNodeId.value = parseInt(e.target.value)
    loadTags()
}

// 加载标签数据
const loadTags = () => {
    API.shared.tags({ 
        nodeId: selectedNodeId.value,
        channelId:channelId.value,
        channelType:channelType.value,
        tagKey:tagKey.value
    }).then((res) => {
        tags.value = res
    }).catch((err) => {
        alert(err.msg)
    })
}

// 显示标签数据
const showData = (tag:any) => {
    currentTag.value = tag
    const dialog = document.getElementById('detail') as HTMLDialogElement;
    dialog.showModal();
}

// 删除标签
const removeTag = (tag:any) => {
    API.shared.removeTag({ nodeId: selectedNodeId.value, key: tag.key }).then(() => {
        loadTags()
    }).catch((err) => {
        alert(err.msg)
    })
}

const onChannelTypeSearch = (e: any) => {
    channelType.value = parseInt(e.target.value)
    loadTags()
}

// 通过频道ID搜索
const onChannelIdSearch = (e: any) => {
    channelId.value = e.target.value
    loadTags()
}

// 通过标签key搜索
const onKeySearch = (e: any) => {
    tagKey.value = e.target.value
    loadTags()
}

</script>

<template>
    <div>
        <div class="overflow-x-auto h-5/6">
            <div class="flex">
                <div class="text-sm ml-3">
                    <label>节点</label>
                    <select class="select select-bordered  max-w-xs select-sm w-40 ml-2" v-on:change="onNodeChange">
                        <option v-for="node in nodeTotal.data" :selected="node.id == selectedNodeId">{{ node.id }}
                        </option>
                    </select>
                </div>
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
                <div class="text-sm ml-10">
                    <label>编号</label>
                    <input type="text" placeholder="输入" class="input input-bordered  select-sm ml-2"
                        v-on:change="onKeySearch"  />
                </div>
            </div>
            <table class="table mt-5 table-pin-rows">
                <thead>
                    <tr>
                        <th>编号</th>
                        <th>数据</th>
                        <th>创建时间</th>
                        <th>最后获取时间</th>
                        <th>过期时间</th>
                        <th>频道ID</th>
                        <th>频道类型</th>
                        <th>获取次数</th>
                        <th>操作</th>
                    </tr>
                </thead>
                <tbody>
                    <tr v-for="tag in tags">
                        <td>{{ tag.key }}</td>
                        <td> <button class="btn btn-link btn-sm" @click="()=>showData(tag)">查看</button>
                        </td>
                        <td>{{ tag.created_at }}</td>
                        <td>{{ tag.last_get_at }}</td>
                        <td>{{ tag.expire_at }}</td>
                        <td>{{ tag.channel_id }}</td>
                        <td>{{ tag.channel_type }}</td>
                        <td>{{ tag.get_count }}</td>
                        <td>
                            <button class="btn btn-sm btn-error w-14" @click="()=>removeTag(tag)">删除</button>
                        </td>
                    </tr>
                </tbody>
            </table>
        </div>
        <dialog id="detail" class="modal">
            <div class="modal-box flex flex-wrap gap-2">
                <vue-json-pretty :data="currentTag.nodes" class="overflow-auto"/>
            </div>
            <form method="dialog" class="modal-backdrop">
                <button>close</button>
            </form>
        </dialog>
    </div>
</template>