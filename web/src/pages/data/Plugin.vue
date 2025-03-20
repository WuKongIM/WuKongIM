<script setup lang="ts">
import { onMounted, ref } from 'vue';
import API from '../../services/API';
import 'vue-json-pretty/lib/styles.css';

const selectedNodeId = ref<number>(0); // 选中的节点ID
const nodeTotal = ref<any>({}); // 节点列表
const plugins = ref<any[]>([]); // 插件列表
const currentPlugin = ref<any>({}); // 当前插件

const form = ref<any>({}); // 表单数据

onMounted(() => {
    API.shared.simpleNodes().then((res) => {
        nodeTotal.value = res
        if (nodeTotal.value.data.length > 0) {
            selectedNodeId.value = nodeTotal.value.data[0].id
        }
        loadPlugins()
    }).catch((err) => {
        alert(err)
    })
})

const onNodeChange = (e: any) => {
    selectedNodeId.value = parseInt(e.target.value)
    loadPlugins()
}

// 加载插件数据
const loadPlugins = () => {
    API.shared.plugins({
        nodeId: selectedNodeId.value,
    }).then((res) => {
        plugins.value = res
    }).catch((err) => {
        alert(err.msg)
    })
}

// 显示插件配置
const onShowConfig = (plugin: any) => {
    currentPlugin.value = plugin
    if (plugin.config) {
        form.value = {...plugin.config}
    } else {
        form.value = {}
    }
    const dialog = document.getElementById('pluginConfig') as HTMLDialogElement;
    dialog.showModal();
}

const onHideConfig = () => {
    const dialog = document.getElementById('pluginConfig') as HTMLDialogElement;
    dialog.close();
}


// 提交配置
const submitConfig = () => {
    console.log(form.value)
    API.shared.pluginConfig({
        nodeId: selectedNodeId.value,
        pluginNo: currentPlugin.value.no,
        config: form.value
    }).then(() => {
        loadPlugins()
        onHideConfig()
    }).catch((err) => {
        alert(err.msg)
    })
}

// 判断config_template是否存在属性
const hasConfigTemplate = (plugin: any) => {
    return plugin.config_template && plugin.config_template.fields && plugin.config_template.fields.length > 0
}

// 显示卸载插件确认框
const onShowUninstall = (plugin: any) => {
    currentPlugin.value = plugin
    const dialog = document.getElementById('uninstall') as HTMLDialogElement;
    dialog.showModal();
}

// 隐藏卸载插件确认框
const onHideUninstall = () => {
    const dialog = document.getElementById('uninstall') as HTMLDialogElement;
    dialog.close();
}

// 卸载插件
const uninstall = () => {
    API.shared.pluginUninstall({
        nodeId: selectedNodeId.value,
        pluginNo: currentPlugin.value.no
    }).then(() => {
        loadPlugins()
        onHideUninstall()
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
                    <label>节点</label>
                    <select class="select select-bordered  max-w-xs select-sm w-40 ml-2" v-on:change="onNodeChange">
                        <option v-for="node in nodeTotal.data" :selected="node.id == selectedNodeId">{{ node.id }}
                        </option>
                    </select>
                </div>
            </div>
            <ul class="text text-xs ml-4 mt-4 text-red-500">
                <li>插件开发和使用文档：<a  class="text-blue-800" target="_blank"  href="https://githubim.com/server/plugin/use.html">文档地址</a></li>
            </ul>
            <table class="table mt-5 table-pin-rows">
                <thead>
                    <tr>
                        <th>节点ID</th>
                        <th>插件ID</th>
                        <th>名字</th>
                        <th>支持的方法</th>
                        <th>版本</th>
                        <th>优先级</th>
                        <th>AI插件</th>
                        <th>状态</th>
                        <th>操作</th>
                    </tr>
                </thead>
                <tbody>
                    <tr v-for="plugin in plugins">
                        <td>{{ plugin.node_id }}</td>
                        <td>{{ plugin.no }}</td>
                        <td>{{ plugin.name }}</td>
                        <td>{{ plugin.methods }}</td>
                        <td>{{ plugin.version }}</td>
                        <td>{{ plugin.priority }}</td>
                        <td>{{plugin.is_ai===1?"是":"否"}}</td>
                        <td v-if="plugin.status == 1" class="text-green-500">正常</td>
                        <td v-else-if="plugin.status == 3" class="text-red-500">断开</td>
                        <td v-else class="text-red-500">异常</td>
                        <td>
                            <button class="btn btn-link btn-sm" v-if="hasConfigTemplate(plugin)" v-on:click="() => onShowConfig(plugin)">配置</button>
                            <button class="btn btn-link btn-sm text-red-500" v-on:click="()=>onShowUninstall(plugin)">卸载</button>
                        </td>
                    </tr>
                </tbody>
            </table>
        </div>

        <dialog id="pluginConfig" class="modal">
            <div class="modal-box flex flex-col">
                <form @submit.prevent="submitConfig">
                    <div class="flex gap-2 flex-col justify-center items-center">

                        <fieldset class="fieldset rounded-box w-xs   p-2" v-for="field in currentPlugin.config_template?.fields">
                            <legend class="fieldset-legend text-xs">{{field.label}}</legend>
                            <input v-if="field.type=='number'" type="number" class="input input-bordered input-sm" v-model="form[field.name]" :name="field.name"  placeholder="输入" />
                            <input v-else-if="field.type=='secret'" type="password" class="input input-bordered input-sm" v-model="form[field.name]" :name="field.name"  placeholder="输入" />
                            <input v-else type="text" class="input input-bordered input-sm" v-model="form[field.name]" :name="field.name"  placeholder="输入" />
                        </fieldset>
                       
                    </div>
                    <div class="mt-4 flex justify-center space-x-8">
                        <button class="btn btn-primary" type="submit" >确认</button>
                        <button class="btn" type="button" v-on:click="()=>onHideConfig()">取消</button>
                    </div>
                </form>
            </div>
            <form method="dialog" class="modal-backdrop">
                <button>close</button>
            </form>
        </dialog>

        <dialog id="uninstall" class="modal">
            <div class="modal-box flex flex-col">
                确定要卸载插件"{{currentPlugin.no}}"吗？
                <div class="mt-4 flex justify-center space-x-8">
                    <button class="btn btn-primary" v-on:click="()=>uninstall()">确认</button>
                    <button class="btn" v-on:click="()=>onHideUninstall()">取消</button>
                </div>
            </div>
            <form method="dialog" class="modal-backdrop">
                <button>close</button>
            </form>
        </dialog>

    </div>
</template>