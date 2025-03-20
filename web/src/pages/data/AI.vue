<script setup lang="ts">
import { onMounted, ref } from 'vue';
import API from '../../services/API';
import 'vue-json-pretty/lib/styles.css';

const selectedNodeId = ref<number>(0); // 选中的节点ID
const pluginUsers = ref<any[]>([]); // 插件用户列表
const plugins = ref<any[]>([]); // 插件列表
const currentSelectPluginUser = ref<any>({}); // 当前选择的插件用户

const pluginUser = ref<any>({
    pluginNo: "",
    Uid: ""
}); // 查询条件

const formPluginUser = ref<any>({}); // 表单数据


onMounted(() => {
    pluginBindList()
})



// 加载AI插件数据
const loadAIPlugins = () => {
    API.shared.plugins({
        nodeId: selectedNodeId.value,
        type: "ai"
    }).then((res) => {
        plugins.value = res
    }).catch((err) => {
        alert(err.msg)
    })
}

const pluginBindList = () => {
    API.shared.pluginBindList({
        uid: pluginUser.value.uid,
        pluginNo: pluginUser.value.pluginNo
    }).then((res) => {
        pluginUsers.value = res
    }).catch((err) => {
        alert(err.msg)
    })
}


// 显示插件配置
const onShowAddPlugin = () => {
    loadAIPlugins()
    const dialog = document.getElementById('addPlugin') as HTMLDialogElement;
    dialog.showModal();
}

const onHideAddPlugin = () => {
    const dialog = document.getElementById('addPlugin') as HTMLDialogElement;
    dialog.close();
}


// 绑定插件
const pluginBind = () => {
    API.shared.pluginBind({
        uid: formPluginUser.value.uid,
        pluginNo: formPluginUser.value.pluginNo
    }).then(() => {
        pluginBindList()
        onHideAddPlugin()
    }).catch((err) => {
        alert(err.msg)
    })
}

// 通过uid搜索
const onUidSearch = (e: any) => {
    pluginUser.value.uid = e.target.value
    pluginBindList()
}

// 通过插件搜索
const onPluginSearch = (e: any) => {
    pluginUser.value.pluginNo = e.target.value
    pluginBindList()
}

// 显示解绑插件
const onShowUnbind = (pluginUser: any) => {
    currentSelectPluginUser.value = pluginUser
    const dialog = document.getElementById('unbind') as HTMLDialogElement;
    dialog.showModal();
}

const onHideUnbind = () => {
    const dialog = document.getElementById('unbind') as HTMLDialogElement;
    dialog.close();
}

// 解绑插件
const unbind = () => {
    API.shared.pluginUnbind({
        uid: currentSelectPluginUser.value.uid,
        pluginNo: currentSelectPluginUser.value.plugin_no
    }).then(() => {
        pluginBindList()
        onHideUnbind()
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
                    <label>UID</label>
                    <input type="text" placeholder="输入" class="input input-bordered  select-sm ml-2"
                        v-on:change="onUidSearch" />
                </div>
                <div class="text-sm ml-10">
                    <label>插件</label>
                    <input type="text" placeholder="输入" class="input input-bordered  select-sm ml-2"
                        v-on:change="onPluginSearch" />
                </div>
                <div class="text-sm ml-10">
                    <button class="btn btn-sm btn-primary" v-on:click="() => onShowAddPlugin()">添加AI</button>
                </div>
            </div>
            <ul class="text text-xs ml-4 mt-4 text-red-500">
                <li>插件绑定用户后，给用户发消息即给插件发消息，插件收到消息可以处理自己的逻辑并回复</li>
            </ul>
            <table class="table mt-2 table-pin-rows">
                <thead>
                    <tr>
                        <th>UID</th>
                        <th>AI插件</th>
                        <th>操作</th>
                    </tr>
                </thead>
                <tbody>
                    <tr v-for="pluginUser in pluginUsers">
                        <td>{{ pluginUser.uid }}</td>
                        <td>{{ pluginUser.plugin_no }}</td>
                        <td>
                            <button class="btn btn-link btn-sm text-red-500" v-on:click="()=>onShowUnbind(pluginUser)">删除</button>
                        </td>
                    </tr>
                </tbody>
            </table>
        </div>

        <dialog id="addPlugin" class="modal">
            <div class="modal-box flex flex-col justify-center items-center">
                <div class="flex gap-2 flex-col">

                    <fieldset class="fieldset rounded-box w-xs   p-2">
                        <legend class="fieldset-legend text-xs">UID</legend>
                        <input type="text" class="input input-bordered input-sm" v-model="formPluginUser.uid"
                            placeholder="输入" />
                    </fieldset>

                    <fieldset class="fieldset rounded-box w-xs   p-2">
                        <legend class="fieldset-legend text-xs">AI插件</legend>
                        <select class="select select-bordered select-sm" v-model="formPluginUser.pluginNo">
                            <option :value="plugin.no" v-for="plugin in plugins">{{ plugin.no }}</option>
                        </select>
                    </fieldset>

                </div>
                <div class="mt-4 flex justify-center space-x-8">
                    <button class="btn btn-primary" type="button" v-on:click="() => pluginBind()">确认</button>
                    <button class="btn" type="button" v-on:click="() => onHideAddPlugin()">取消</button>
                </div>
            </div>
            <form method="dialog" class="modal-backdrop">
                <button>close</button>
            </form>
        </dialog>

        <dialog id="unbind" class="modal">
            <div class="modal-box flex flex-col">
                确定删除用户"{{currentSelectPluginUser.uid}}"的插件"{{currentSelectPluginUser.plugin_no}}"吗？
                <div class="mt-4 flex justify-center space-x-8">
                    <button class="btn btn-primary" v-on:click="()=>unbind()">确认</button>
                    <button class="btn" v-on:click="()=>onHideUnbind()">取消</button>
                </div>
            </div>
            <form method="dialog" class="modal-backdrop">
                <button>close</button>
            </form>
        </dialog>

    </div>
</template>