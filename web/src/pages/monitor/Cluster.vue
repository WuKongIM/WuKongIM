<script setup lang="ts">

import MonitorPanel from '../../components/MonitorPanel.vue'
import { onMounted, ref } from 'vue';
import API from '../../services/API';
import { Series,setSeries } from '../../services/Model';
import App from '../../services/App';

const latest = ref<number>(60 * 5) // 最近时间

const msgIncomingCountRateRef = ref<Series[]>([]);
const msgIncomingBytesRateRef = ref<Series[]>([]);
const msgOutgoingCountRateRef = ref<Series[]>([]);
const msgOutgoingBytesRateRef = ref<Series[]>([]);

const channelMsgIncomingCountRateRef = ref<Series[]>([]);
const channelMsgIncomingBytesRateRef = ref<Series[]>([]);
const channelMsgOutgoingCountRateRef = ref<Series[]>([]);
const channelMsgOutgoingBytesRateRef = ref<Series[]>([]);

const sendPacketIncomingCountRateRef = ref<Series[]>([]);
const sendPacketIncomingBytesRateRef = ref<Series[]>([]);
const sendPacketOutgoingBytesRateRef = ref<Series[]>([]);
const sendPacketOutgoingCountRateRef = ref<Series[]>([]);

const msgSyncIncomingCountRateRef = ref<Series[]>([]);
const msgSyncIncomingBytesRateRef = ref<Series[]>([]);
const msgSyncOutgoingCountRateRef = ref<Series[]>([]);
const msgSyncOutgoingBytesRateRef = ref<Series[]>([]);

const channelActiveCountRef = ref<Series[]>([])

const channelProposeCountRateRef = ref<Series[]>([])
const channelProposeFailedCountRate = ref<Series[]>([])
const channelProposeLatencyOver500msRate = ref<Series[]>([])
const channelProposeLatencyUnder500msRate = ref<Series[]>([])

const msgPingIncomingCountRateRef = ref<Series[]>([])
const msgPingIncomingBytesRateRef = ref<Series[]>([])
const msgPingOutgoingCountRateRef = ref<Series[]>([])
const msgPingOutgoingBytesRateRef = ref<Series[]>([])


onMounted(() => {
    if(!App.shard().systemSetting.prometheusOn) {
        return
    }
    loadMetrics()
})


const loadMetrics = () => {

    API.shared.clusterMetrics(latest.value).then((res) => {
        msgIncomingCountRateRef.value = []
        msgIncomingBytesRateRef.value = []
        msgOutgoingCountRateRef.value = []
        msgOutgoingBytesRateRef.value = []

        sendPacketIncomingCountRateRef.value = []
        sendPacketIncomingBytesRateRef.value = []
        sendPacketOutgoingBytesRateRef.value = []
        sendPacketOutgoingCountRateRef.value = []

        msgSyncIncomingCountRateRef.value = []
        msgSyncIncomingBytesRateRef.value = []
        msgSyncOutgoingCountRateRef.value = []
        msgSyncOutgoingBytesRateRef.value = []

        channelActiveCountRef.value = []

        channelProposeCountRateRef.value = []
        channelProposeFailedCountRate.value = []
        channelProposeLatencyOver500msRate.value = []
        channelProposeLatencyUnder500msRate.value = []

        msgPingIncomingCountRateRef.value = []
        msgPingIncomingBytesRateRef.value = []
        msgPingOutgoingCountRateRef.value = []
        msgPingOutgoingBytesRateRef.value = []

        for (let index = 0; index < res.length; index++) {
            const d = res[index];
            setSeries("msg_incoming_count_rate", d, msgIncomingCountRateRef.value)
            setSeries("msg_incoming_bytes_rate", d, msgIncomingBytesRateRef.value)
            setSeries("msg_outgoing_count_rate", d, msgOutgoingCountRateRef.value)
            setSeries("msg_outgoing_bytes_rate", d, msgOutgoingBytesRateRef.value)

            setSeries("channel_msg_incoming_count_rate", d, channelMsgIncomingCountRateRef.value)
            setSeries("channel_msg_incoming_bytes_rate", d, channelMsgIncomingBytesRateRef.value)
            setSeries("channel_msg_outgoing_count_rate", d, channelMsgOutgoingCountRateRef.value)
            setSeries("channel_msg_outgoing_bytes_rate", d, channelMsgOutgoingBytesRateRef.value)

            setSeries("sendpacket_incoming_count_rate", d, sendPacketIncomingCountRateRef.value)
            setSeries("sendpacket_incoming_bytes_rate", d, sendPacketIncomingBytesRateRef.value)
            setSeries("sendpacket_outgoing_bytes_rate", d, sendPacketOutgoingBytesRateRef.value)
            setSeries("sendpacket_outgoing_count_rate", d, sendPacketOutgoingCountRateRef.value)

            setSeries("msg_sync_incoming_bytes_rate", d, msgSyncIncomingBytesRateRef.value)
            setSeries("msg_sync_incoming_count_rate", d, msgSyncIncomingCountRateRef.value)
            setSeries("msg_sync_outgoing_bytes_rate", d, msgSyncOutgoingBytesRateRef.value)
            setSeries("msg_sync_outgoing_count_rate", d, msgSyncOutgoingCountRateRef.value)

            setSeries("channel_active_count", d, channelActiveCountRef.value)

            setSeries("channel_propose_count_rate", d, channelProposeCountRateRef.value)
            setSeries("channel_propose_failed_count_rate", d, channelProposeFailedCountRate.value)
            setSeries("channel_propose_latency_over_500ms_rate", d, channelProposeLatencyOver500msRate.value)
            setSeries("channel_propose_latency_under_500ms_rate", d, channelProposeLatencyUnder500msRate.value)

            setSeries("msg_ping_incoming_count_rate", d, msgPingIncomingCountRateRef.value)
            setSeries("msg_ping_incoming_bytes_rate", d, msgPingIncomingBytesRateRef.value)
            setSeries("msg_ping_outgoing_count_rate", d, msgPingOutgoingCountRateRef.value)
            setSeries("msg_ping_outgoing_bytes_rate", d, msgPingOutgoingBytesRateRef.value)

        }
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

            <!-- propose  -->
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="channelProposeCountRateRef" title="频道提案(次)" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="channelProposeLatencyUnder500msRate" title="频道提案小于500ms(次)" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="channelProposeLatencyOver500msRate" title="频道提案大于500ms(次)" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="channelProposeFailedCountRate" title="频道失败提案(次)" />
                </div>
            </div>
            <!-- message  -->
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="msgIncomingCountRateRef" title="流入总消息(个)" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="msgIncomingBytesRateRef" title="流入总消息(字节)" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="msgOutgoingCountRateRef" title="流出总消息(个)" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="msgOutgoingBytesRateRef" title="流出总消息(字节)" />
                </div>
            </div>

            <!-- message  -->
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="channelMsgIncomingCountRateRef" title="流入频道消息(个)" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="channelMsgIncomingBytesRateRef" title="流入频道消息(字节)" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="channelMsgOutgoingCountRateRef" title="流出频道消息(个)" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="channelMsgOutgoingBytesRateRef" title="流出频道消息(字节)" />
                </div>
            </div>

            <!-- channel active  -->
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="channelActiveCountRef" title="激活的频道(个)" />
                </div>
            </div>

            <!-- sync  -->
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="msgSyncIncomingCountRateRef" title="流入同步消息(个)" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="msgSyncIncomingBytesRateRef" title="流入同步消息(字节)" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="msgSyncOutgoingCountRateRef" title="流出同步消息(个)" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="msgOutgoingBytesRateRef" title="流出同步消息(字节)" />
                </div>
            </div>


        </div>
        <div class="text text-center mt-10 text-red-500" v-else>
             监控功能未开启，请查看官网文档 https://githubim.com
        </div>
    </div>
</template>