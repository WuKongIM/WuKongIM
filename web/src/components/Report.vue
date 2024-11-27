<template>
    <div class="w-full">
        <table class="table mt-5 table-pin-rows">

            <tbody>
                <tr>
                    <td>运行时长：{{ props.modelValue.task_duration_desc }}</td>
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
                    <td>发送速率：{{ props.modelValue.send_rate }}条/秒</td>
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
                    <td>发送最大延迟：{{ formatMilliseconds(props.modelValue.send_max_latency) }}</td>
                </tr>
                <tr>
                    <td>发送平均延迟：{{ formatMilliseconds(props.modelValue.send_avg_latency) }}</td>
                    <td></td>
                </tr>
                
                <tr class="!border-b">
                </tr>

                <tr>
                    <td>接受消息：{{ props.modelValue.recv }}条</td>
                    <td>接受速率：{{ props.modelValue.recv_rate }}条/秒</td>
                </tr>

                <tr>
                    <td>接受大小：{{ formatBytes(props.modelValue.recv_bytes) }}</td>
                    <td>接受流量：{{ formatBytes(props.modelValue.recv_bytes_rate) }}/秒</td>
                </tr>

                <tr>
                    <td>接受最小延迟：{{ formatMilliseconds(props.modelValue.recv_min_latency) }}</td>
                    <td>接受最大延迟：{{ formatMilliseconds(props.modelValue.recv_max_latency) }}</td>
                </tr>
                <tr>
                    <td>接受平均延迟：{{ formatMilliseconds(props.modelValue.recv_avg_latency) }}</td>
                </tr>
                


            </tbody>
        </table>

    </div>
</template>

<script setup lang="ts">


const props = defineProps({
    modelValue: {
        type: Object,
        required: true
    }
})

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

.table-pin-rows tr{
    border-bottom-width: 0px;
}

</style>