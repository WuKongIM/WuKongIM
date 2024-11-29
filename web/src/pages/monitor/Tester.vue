<template>
    <div>
        <div class="overflow-x-auto h-5/6">
            <div class="flex items-center">
                <div class="flex">
                    <button class="btn btn-primary" v-on:click="onShowAddTester">添加压测机</button>
                </div>
                <div class="ml-10 text-red-500">
                    <label class="text text-sm font-semibold">流程：</label>

                    <ul class="text text-sm">
                        <li>1. 服务器配置调优和部署压测机（避免网络干扰，建议压测机部署在服务器内网，查看文档：<a class="text-blue-800" target="_blank"
                                href="https://githubim.com/server/advance/stress.html">https://githubim.com/server/advance/stress.html</a>）
                        </li>
                        <li>2. 添加压测机</li>
                        <li>3. 开始压测（点击压测机的开始，进行测试脚本配置）</li>
                        <li class="mt-4">可以通过查看监控来了解服务器的承压能力，可以通过查看压测机的报告，实时了解压测进度</li>
                        <li class="mt-2">如果部署了负载均衡，一般负载均衡由于系统端口数量限制，同时连接数不能超过6万，正式环境中可以增加负载均衡的数量来增加同时在线连接数</li>
                    </ul>
                </div>
            </div>
            <table class="table mt-5 table-pin-rows">
                <thead>
                    <tr>
                        <th>
                            <div class="flex items-center">
                                唯一标识
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                地址
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                配置
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                内存占用
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                cpu占用
                            </div>
                        </th>
                        <th>
                            <div class="flex items-center">
                                状态
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
                    <tr v-for="t in testers">
                        <td>{{ t.no }}</td>
                        <td>{{ t.addr }}</td>
                        <td>{{ t.desc }}</td>
                        <td>{{ t.memory_percent_desc }}</td>
                        <td>{{ t.cpu_percent_desc }}</td>
                        <td :class="statusColor(t.status)">{{ statusText(t.status) }}</td>
                        <td class="flex flex-wrap gap-2">
                            <button :class="buttonStatusClass(t.status)" :disabled="stoping"
                                v-on:click="() => onStartOrStop(t)">
                                {{ t.status == Status.Running ? "停止" : "开始" }}
                                <span class="loading loading-spinner" v-if="stoping"></span>
                            </button>
                            <button class="btn btn-primary btn-sm" v-on:click="() => onReport(t)">报告</button>
                            <button className="btn btn-warning btn-sm" v-on:click="() => onDelete(t.no)">删除</button>
                        </td>

                    </tr>
                </tbody>
            </table>

        </div>

        <!-- 开始 -->
        <dialog id="startModal" class="modal">
            <div class="modal-box flex flex-wrap gap-2 max-w-[48rem]">
                <Task :onCancel="onCancel" :onRun="onRun" v-model="config" :loading="runLoading" :templates="templates" />
            </div>
        </dialog>

        <!-- 添加压测机 -->
        <dialog id="addTesterModal" class="modal">
            <div class="modal-box flex flex-wrap gap-2">
                <label class="input input-bordered flex items-center gap-2 w-[20rem]">
                    压测机地址
                    <input type="text" class="grow" placeholder="http://192.168.1.13:9466" v-model="testerAddr" />
                </label>
                <button class="btn btn-primary" v-on:click="onAddTester">添加</button>
            </div>
            <form method="dialog" class="modal-backdrop">
                <button>close</button>
            </form>
        </dialog>

        <!-- 报告 -->
        <dialog id="reportModal" class="modal">
            <div class="modal-box flex flex-wrap gap-2 max-w-[34rem]">
                <Report v-model="currentReport" :task="currentTester?.task"></Report>
            </div>
            <form method="dialog" class="modal-backdrop">
                <button v-on:click="onReportClose">close</button>
            </form>
        </dialog>

    </div>

</template>


<script setup lang="ts">

import Task from '../../components/Task.vue';
import Report from '../../components/Report.vue';
import {  Config } from '../../components/Task';
import { onMounted, onUnmounted, ref } from 'vue';
import API from '../../services/API';
import { taskToConfig } from '../../services/Model';
import App from '../../services/App';


enum Status {
    Unknown = 0,
    Normal = 1,
    Running = 2,
    Error = 3
}

const config = ref<Config>(new Config())

// 压测机地址
const testerAddr = ref<string>("")

const testers = ref<any[]>([])
// 当前压测机
const currentTester = ref<any>()

// 当前报告
const currentReport = ref<any>({})

// 模版
const templates = ref<any[]>([])


// 运行是否正在加载
const runLoading = ref(false)

// 停止中
const stoping = ref(false)

let intervalObj: number
let intervalReportObj: number

onMounted(() => {
    loadTesters()

    App.shard().loadSystemSettingIfNeed()

    
    // 每秒中刷新列表
    intervalObj = window.setInterval(function () {
        loadTesters()
    }, 4000)

})

onUnmounted(() => {
    clearInterval(intervalObj)
    clearInterval(intervalReportObj)
})


// 准备开始
const onStartOrStop = async (t: any) => {
    
    currentTester.value = t

    if (t.status == Status.Running) {
        stoping.value = true
        await stopTester()
        stoping.value = false
    } else {
        if (t.task) {
            config.value = taskToConfig(t.task)
        }
        const dialog = document.getElementById('startModal') as HTMLDialogElement;
        dialog.showModal();

        loadTemplates()
    }
}

// 加载模版
const loadTemplates = async () => {
    templates.value = await API.shared.testTemplates()
}   

const hideStartModal = () => {
    const dialog = document.getElementById('startModal') as HTMLDialogElement;
    dialog.close();
}

const stopTester = async () => {
    await API.shared.testerStop(currentTester.value.no)
    loadTesters()
}

// 删除
const onDelete = async (no: string) => {

    try {
        await API.shared.testerStop(no)
    } catch (error: any) {
        console.log(error)
    } finally {
        await API.shared.removeTester(no)
        loadTesters()
    }
}

// 运行
const onRun = async (cfg: Config) => {
    runLoading.value = true
    try {
        await API.shared.testerStart(currentTester.value.no, cfg)
        loadTesters()
    } catch (error: any) {
        console.log(error)
        alert(error.msg)
    } finally {
        runLoading.value = false
        hideStartModal()
    }
}

// 取消
const onCancel = () => {
    const dialog = document.getElementById('startModal') as HTMLDialogElement;
    dialog.close()
}

// 显示添加压测机
const onShowAddTester = () => {
     if(!App.shard().systemSetting.StressOn) {
        alert("压测功能未开启，请查看文档开启。")
        return
     }

    const dialog = document.getElementById('addTesterModal') as HTMLDialogElement;
    dialog.showModal();
}

const hideAddTester = () => {
    const dialog = document.getElementById('addTesterModal') as HTMLDialogElement;
    dialog.close();
}

// 添加压测机
const onAddTester = async () => {
    const addr = testerAddr.value
    if (!addr) {
        alert("压测机地址不能为空！")
        return
    }
    // 添加压测机器
    try {
        await API.shared.addTester({
            addr: addr,
        })
        hideAddTester()

    } catch (error: any) {
        alert(error.msg)
    }
}

// 加载压测机列表
const loadTesters = async () => {
    if(!App.shard().systemSetting.StressOn) {
        return
    }
    // 加载压测机列表
    testers.value = await API.shared.testers()
}

// 加载报告
const loadReport = async () => {
    currentReport.value = await API.shared.testerReport(currentTester.value.no)
}

// 显示报告
const onReport = (t: any) => {
    currentTester.value = t
    const dialog = document.getElementById('reportModal') as HTMLDialogElement;
    dialog.showModal();

    intervalReportObj = window.setInterval(() => {
        loadReport()
    }, 1000)
}

const onReportClose = () => {
    console.log("onReportClose...")
    clearInterval(intervalReportObj)
}

const statusText = (status: Status) => {
    switch (status) {
        case Status.Unknown:
            return "未知"
        case Status.Normal:
            return "正常"
        case Status.Running:
            return "运行中"
        case Status.Error:
            return "错误"
        default:
            return "未知"
    }
}

const statusColor = (status: Status) => {
    switch (status) {
        case Status.Unknown:
        case Status.Normal:
            return "text-gray-500"
        case Status.Running:
            return "text-green-500"
        case Status.Error:
            return "text-red-500"
    }
}

const buttonStatusClass = (status: Status) => {

    switch (status) {
        case Status.Running:
            return "btn btn-secondary btn-sm"
        default:
            return "btn btn-primary btn-sm"
    }
}





</script>