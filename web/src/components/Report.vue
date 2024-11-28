<template>
    <div class="w-full">
        <div class="mt-2 text text-sm font-semibold">测试指标：</div>
        <table class="table table-pin-rows table-xs mt-2 text-gray-500">
            <tbody>
                <tr>
                    <th>1</th>
                    <td>{{ props.task?.online }}人同时在线</td>
                    <td></td>
                    <td></td>
                </tr>
                <tr v-for="(channel, i) in props.task?.channels">
                    <th>{{ i + 2 }}</th>
                    <td>{{ channel.count }}个{{ channel.subscriber.count }}人的群</td>
                    <td>成员在线率: {{(channel.subscriber.online/channel.subscriber.count)*100}}% ({{ channel.subscriber.online }}人)</td>
                    <td>群内一分钟发送{{ channel.msg_rate }}条消息</td>
                </tr>

                <tr v-if="props.task?.p2p">
                    <th>{{ props.task.channels?.length + 2 }}</th>
                    <td>{{ props.task.p2p.count }}对单聊</td>
                    <td>全在线</td>
                    <td>每对一分钟发送{{ props.task.p2p.msg_rate }}条消息</td>
                </tr>

            </tbody>
        </table>

        <div class="mt-4 text text-sm font-semibold">测试报告：</div>
        <table class="table  table-pin-rows table-sm mt-2">

            <tbody>
                <tr>
                    <td>运行时长：<label :class="props.modelValue.task_duration > 0 ? 'text-green-500' : 'text-red-500'">{{
                        props.modelValue.task_duration_desc }}</label></td>
                    <td></td>
                </tr>
                <tr>
                    <td>在线人数：{{ props.modelValue.online }}人</td>
                    <td>离线人数：{{ props.modelValue.offline }}人</td>
                </tr>
                <tr class="!border-b">
                </tr>
                <tr>
                    <td>发送消息：{{ props.modelValue.send }}条</td>
                    <td>发送速率：<label class="text text-orange-500">{{ props.modelValue.send_rate }}条/秒</label></td>
                </tr>
                <tr>
                    <td>发送大小：{{ formatBytes(props.modelValue.send_bytes) }}</td>
                    <td>发送流量：{{ formatBytes(props.modelValue.send_bytes_rate) }}/秒</td>
                </tr>
                <tr>
                    <td>发送成功：{{ props.modelValue.send_success }}条</td>
                    <td>发送错误：{{ props.modelValue.send_err }}条</td>
                </tr>
                <tr>
                    <td>发送最小延迟：{{ formatMilliseconds(props.modelValue.send_min_latency) }}</td>
                    <td class="flex">
                        <div>发送最大延迟：{{ formatMilliseconds(props.modelValue.send_max_latency) }}</div>
                        <button class="btn btn-xs btn-circle mt-[-2px] ml-2" v-on:click="onSendMaxLatencyHelp">
                            <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24"
                                fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"
                                stroke-linejoin="round" class="lucide lucide-circle-help">
                                <circle cx="12" cy="12" r="10" />
                                <path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3" />
                                <path d="M12 17h.01" />
                            </svg>
                        </button>
                    </td>
                </tr>
                <tr>
                    <td>发送平均延迟：<label class="text text-orange-500">{{
                        formatMilliseconds(props.modelValue.send_avg_latency) }}</label></td>
                    <td></td>
                </tr>

                <tr class="!border-b">
                </tr>

                <tr>
                    <td class="flex">
                       <div> 接受消息：{{ props.modelValue.recv }}条</div>
                        <button class="btn btn-xs btn-circle mt-[-2px] ml-2" v-on:click="onRecvHelp">
                            <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24"
                                fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"
                                stroke-linejoin="round" class="lucide lucide-circle-help">
                                <circle cx="12" cy="12" r="10" />
                                <path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3" />
                                <path d="M12 17h.01" />
                            </svg>
                        </button>

                    </td>
                    <td>接受速率：<label class="text text-orange-500">{{ props.modelValue.recv_rate }}条/秒</label></td>
                </tr>

                <tr>
                    <td>接受大小：{{ formatBytes(props.modelValue.recv_bytes) }}</td>
                    <td>接受流量：{{ formatBytes(props.modelValue.recv_bytes_rate) }}/秒</td>
                </tr>

                <tr>
                    <td>接受最小延迟：{{ formatMilliseconds(props.modelValue.recv_min_latency) }}</td>
                    <td class="flex">
                        <div> 接受最大延迟：{{ formatMilliseconds(props.modelValue.recv_max_latency) }}</div>
                        <button class="btn btn-xs btn-circle mt-[-2px] ml-2" v-on:click="onRecvMaxLatencyHelp">
                            <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24"
                                fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"
                                stroke-linejoin="round" class="lucide lucide-circle-help">
                                <circle cx="12" cy="12" r="10" />
                                <path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3" />
                                <path d="M12 17h.01" />
                            </svg>
                        </button>
                    </td>
                </tr>
                <tr>
                    <td>接受平均延迟：<label class="text text-orange-500">{{
                        formatMilliseconds(props.modelValue.recv_avg_latency) }}</label></td>
                </tr>
            </tbody>
        </table>

        <dialog id="showAlert" class="modal">
            <div class="modal-box">
                <p class="py-4">{{tip}}</p>
                <div class="modal-action">
                    <form method="dialog">
                        <!-- if there is a button in form, it will close the modal -->
                        <button class="btn">关闭</button>
                    </form>
                </div>
            </div>
        </dialog>

    </div>
</template>

<script setup lang="ts">
import { ref } from 'vue';


const tip = ref('');


const props = defineProps({
    modelValue: {
        type: Object,
        required: true
    },
    task: {
        type: Object,
    }
})

const onSendMaxLatencyHelp = () => {
    tip.value = '第一条消息需要激活频道需要耗时，所以会出现延迟比较大，延迟数据看平均延迟比较准确';
    const dialog = document.getElementById('showAlert') as HTMLDialogElement;
    dialog.showModal();
}

const onRecvMaxLatencyHelp = () => {
    tip.value = '第一条消息需要激活频道需要耗时，所以会出现延迟比较大，延迟数据看平均延迟比较准确';
    const dialog = document.getElementById('showAlert') as HTMLDialogElement;
    dialog.showModal();
}

const onRecvHelp = () => {
    tip.value = `期望收到：${props.modelValue.expect_recv} （这个值不一定准，只是个大概）`
    const dialog = document.getElementById('showAlert') as HTMLDialogElement;
    dialog.showModal();
}

const formatBytes = (bytes: number, decimals = 2) => {
    if (bytes === 0) return '0 Bytes';
    const k = 1024;
    const dm = decimals < 0 ? 0 : decimals;
    const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB', 'ZB', 'YB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(dm)) + ' ' + sizes[i];
}

const formatMilliseconds = (ms: number) => {
    if (ms >= 1000) {
        const seconds = Math.floor(ms / 1000);
        const milliseconds = ms % 1000;
        return `${seconds}s ${milliseconds}ms`;
    } else {
        return `${ms}ms`;
    }
}

</script>

<style scoped>
.table-pin-rows tr {
    border-bottom-width: 0px;
}
</style>