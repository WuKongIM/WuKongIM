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
                        <li>1. 开启压力测试配置（查看文档： <a class="text-blue-800" target="_blank"
                                href="https://githubim.com/server/config/stress.html">https://githubim.com/server/config/stress.html</a>）
                        </li>
                        <li>2. 部署压测机（避免网络干扰，建议压测机部署在服务器内网，查看文档：<a class="text-blue-800" target="_blank"
                                href="https://githubim.com/server/advance/stress.html">https://githubim.com/server/advance/stress.html</a>）
                        </li>
                        <li>3. 添加压测机</li>
                        <li>4. 开始压测（点击压测机的开始，进行测试脚本配置）</li>
                        <li class="mt-4">可以通过查看监控来了解服务器的承压能力，可以通过查看压测机的报告，实时了解压测进度</li>
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
                            <button :class="buttonStatusClass(t.status)" v-on:click="() => onStartOrStop(t)">{{ t.status
                                ==
                                Status.Running?"停止":"开始"}}</button>
                            <button class="btn btn-primary btn-sm" v-on:click="()=>onReport(t)">报告</button>
                            <button className="btn btn-warning btn-sm" v-on:click="() => onDelete(t.no)">删除</button>
                        </td>

                    </tr>
                </tbody>
            </table>

        </div>

        <dialog id="startModal" class="modal">
            <div class="modal-box flex flex-wrap gap-2 max-w-[48rem]">
                <Task :onCancel="onCancel" :onRun="onRun" v-model="config" />
            </div>
        </dialog>

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

        <dialog id="reportModal" class="modal">
            <div class="modal-box flex flex-wrap gap-2 max-w-[30rem]">
                <Report v-model="currentReport"></Report>
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
import { ChannelItem, Config, P2pItem } from '../../components/Task';
import { onMounted, onUnmounted, ref } from 'vue';
import API from '../../services/API';

enum Status {
    Unknown = 0,
    Normal = 1,
    Running = 2,
    Error = 3
}

const config = ref<Config>(new Config())

// 压测机地址
const testerAddr = ref<string>("")

const testers = ref<any>([])
// 当前压测机
const currentTester = ref<any>()

// 当前报告
const currentReport = ref<any>({})

let intervalObj: number
let intervalReportObj: number

onMounted(() => {
    config.value = getDefaultConfig()
    loadTesters()

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
        await stopTester()
    } else {
        config.value = getDefaultConfig()
        const dialog = document.getElementById('startModal') as HTMLDialogElement;
        dialog.showModal();
    }
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
    }finally {
        await API.shared.removeTester(no)
        loadTesters()
    }
}

// 运行
const onRun = async (cfg: Config) => {
    try {
        await API.shared.testerStart(currentTester.value.no, cfg)
        loadTesters()
    } catch (error: any) {
        console.log(error)
        alert(error.msg)
    }finally {
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
    // 加载压测机列表
    testers.value = await API.shared.testers()
}

// 加载报告
const loadReport = async () => {
    currentReport.value = await API.shared.testerReport(currentTester.value.no)
}

// 显示报告
const onReport = (t:any) => {
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



// 初获取默认配置
const getDefaultConfig = () => {

    const channelItems = []
    // 200人群
    let item = new ChannelItem()
    item.count = 100
    item.subscriberCount = 200
    item.subscriberOnlineCount = 50
    item.msgRate = 60
    channelItems.push(item)

    // 500人群
    item = new ChannelItem()
    item.count = 50
    item.subscriberCount = 500
    item.subscriberOnlineCount = 125
    item.msgRate = 60
    channelItems.push(item)

    // 1000人群
    item = new ChannelItem()
    item.count = 20
    item.subscriberCount = 1000
    item.subscriberOnlineCount = 250
    item.msgRate = 60
    channelItems.push(item)

    // 1000人群
    item = new ChannelItem()
    item.count = 1
    item.subscriberCount = 10000
    item.subscriberOnlineCount = 2500
    item.msgRate = 60
    channelItems.push(item)

    const p2pItem = new P2pItem()
    p2pItem.count = 1000
    p2pItem.msgRate = 60

    const cfg = new Config()
    cfg.channels = channelItems
    cfg.p2pItem = p2pItem
    cfg.onlineCount = 10000

    return cfg

}



</script>