<script setup lang="ts">

import MonitorPanel from '../../components/MonitorPanel.vue'
import { onMounted, ref } from 'vue';
import API from '../../services/API';
import { Series, setSeries } from '../../services/Model';
import App from '../../services/App';

const latest = ref<number>(60 * 5) // 最近时间

const intranetIncomingBytesRateRef = ref<Series[]>([])
const intranetOutgoingBytesRateRef = ref<Series[]>([])

const extranetIncomingBytesRateRef = ref<Series[]>([])
const extranetOutgoingBytesRateRef = ref<Series[]>([])
const memstatsAllocBytes = ref<Series[]>([])
const goroutines = ref<Series[]>([])

const gcDurationSecondsCount = ref<Series[]>([])
const cpuPercent = ref<Series[]>([])

const filefdAllocated = ref<Series[]>([]) // 文件打开数

onMounted(() => {
    if (!App.shard().systemSetting.prometheusOn) {
        return
    }
    loadMetrics()
})

const loadMetrics = () => {
    API.shared.systemMetrics(latest.value).then((res) => {
        intranetIncomingBytesRateRef.value = []
        intranetOutgoingBytesRateRef.value = []
        extranetIncomingBytesRateRef.value = []
        extranetOutgoingBytesRateRef.value = []

        memstatsAllocBytes.value = []
        goroutines.value = []
        gcDurationSecondsCount.value = []
        cpuPercent.value = []
        filefdAllocated.value = []

        var filefdAllocatedData = []

        for (let index = 0; index < res.length; index++) {
            const d = res[index];
            setSeries("intranet_incoming_bytes_rate", d, intranetIncomingBytesRateRef.value)
            setSeries("intranet_outgoing_bytes_rate", d, intranetOutgoingBytesRateRef.value)

            setSeries("extranet_incoming_bytes_rate", d, extranetIncomingBytesRateRef.value)
            setSeries("extranet_outgoing_bytes_rate", d, extranetOutgoingBytesRateRef.value)
            setSeries("memstats_alloc_bytes", d, memstatsAllocBytes.value)
            setSeries("goroutines", d, goroutines.value)
            setSeries("gc_duration_seconds_count", d, gcDurationSecondsCount.value)
            setSeries("cpu_percent", d, cpuPercent.value)
            filefdAllocatedData.push({ timestamp: d.timestamp, value: d.filefd_allocated })

        }
        filefdAllocated.value = [{
            name: "文件打开数",
            data: filefdAllocatedData
        }]

    }).catch((err) => {
        alert(err)

    })
}
const onLatestChange = (e: any) => {
    latest.value = e.target.value
    loadMetrics()
}
const onRefresh = () => {
    loadMetrics()
}


</script>

<template>
    <div class="overflow-auto h-5/6">
        <div class="flex">
            <div class="text-sm ml-10">
                <select class="select select-bordered  max-w-xs select-sm w-40 ml-2" v-on:change="onLatestChange">
                    <option selected :value="60 * 5">过去5分钟</option>
                    <option :value="60 * 30">过去30分钟</option>
                    <option :value="60 * 60">过去1小时</option>
                    <option :value="60 * 60 * 6">过去6小时</option>
                    <option :value="60 * 60 * 24">过去1天</option>
                    <option :value="60 * 60 * 24 * 3">过去3天</option>
                    <option :value="60 * 60 * 24 * 7">过去7天</option>
                </select>
            </div>
            <button class="btn btn-sm ml-10" v-on:click="onRefresh">立马刷新</button>
        </div>
        <br />
        <div class="flex flex-wrap justify-left" v-if="App.shard().systemSetting.prometheusOn">
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="intranetIncomingBytesRateRef" title="内网流入流量" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="intranetOutgoingBytesRateRef" title="内网流出流量" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="extranetIncomingBytesRateRef" title="外网流入流量" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="extranetOutgoingBytesRateRef" title="外网流出流量" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="memstatsAllocBytes" title="内存" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="goroutines" title="协程" />
                </div>
            </div class="pl-5">
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="gcDurationSecondsCount" title="GC" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="cpuPercent" title="CPU" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="filefdAllocated" title="文件打开数" />
                </div>
            </div>
        </div>
        <div class="text text-center mt-10 text-red-500" v-else>
            监控功能未开启，请查看官网文档 https://githubim.com
        </div>
    </div>
</template>