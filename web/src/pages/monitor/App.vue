<script setup lang="ts">
import MonitorPanel from '../../components/MonitorPanel.vue'
import { onMounted, ref } from 'vue';
import API from '../../services/API';
import { Series } from '../../services/Model';
import App from '../../services/App';

const connectionsRef = ref<Series[]>([]); // 连接数
const onlineUserCountRef = ref<Series[]>([]); // 在线用户数

const sendPacketCountRateRef = ref<Series[]>([]); // 发送数据包数
const sendPacketBytesRateRef = ref<Series[]>([]); // 发送数据包字节数

const recvPacketCountRateRef = ref<Series[]>([]); // 接收数据包数
const recvPacketBytesRateRef = ref<Series[]>([]); // 接收数据包字节数

const connPacketCountRateRef = ref<Series[]>([]); // 连接数据包数
const connPacketBytesRateRef = ref<Series[]>([]); // 连接数据包字节数


const pingPacketCountRateRef = ref<Series[]>([]); // ping数据包数
const pingPacketBytesRateRef = ref<Series[]>([]); // ping数据包字节数

const nodeTotal = ref<any>({}); // 节点列表
const selectedNodeId = ref<number>(0) // 选中的节点ID
const latest = ref<number>(60 * 5) // 最近时间


onMounted(() => {

    if(!App.shard().systemSetting.prometheusOn) {
        return
    }

    loadMetrics()
    API.shared.simpleNodes().then((res) => {
        nodeTotal.value = res
    }).catch((err) => {
        alert(err)
    })
})

const loadMetrics = () => {
    API.shared.apppMetrics(selectedNodeId.value,latest.value).then((res) => {
        var connections = []
        var onlineUserCount = []
        var onlineDeviceCount = []

        var sendPacketCountRate = []
        var sendPacketBytesRate = []
        var sendackPacketCountRate = []
        var sendackPacketBytesRate = []

        var recvPacketCountRate = []
        var recvPacketBytesRate = []
        var recvackPacketCountRate = []
        var recvackPacketBytesRate = []

        var connPacketCountRate = []
        var connPacketBytesRate = []
        var connackPacketCountRate = []
        var connackPacketBytesRate = []

        var pingPacketCountRate = []
        var pingPacketBytesRate = []
        var pongPacketCountRate = []
        var pongPacketBytesRate = []
        for (let index = 0; index < res.length; index++) {
            const d = res[index];
            connections.push({ timestamp: d.timestamp, value: d.conn_count })
            onlineUserCount.push({ timestamp: d.timestamp, value: d.online_user_count })
            onlineDeviceCount.push({ timestamp: d.timestamp, value: d.online_device_count })

            sendPacketCountRate.push({ timestamp: d.timestamp, value: d.send_packet_count_rate })
            sendPacketBytesRate.push({ timestamp: d.timestamp, value: d.send_packet_bytes_rate })
            sendackPacketCountRate.push({ timestamp: d.timestamp, value: d.sendack_packet_count_rate })
            sendackPacketBytesRate.push({ timestamp: d.timestamp, value: d.sendack_packet_bytes_rate })

            recvPacketCountRate.push({ timestamp: d.timestamp, value: d.recv_packet_count_rate })
            recvPacketBytesRate.push({ timestamp: d.timestamp, value: d.recv_packet_bytes_rate })
            recvackPacketCountRate.push({ timestamp: d.timestamp, value: d.recvack_packet_count_rate })
            recvackPacketBytesRate.push({ timestamp: d.timestamp, value: d.recvack_packet_bytes_rate })

            connPacketCountRate.push({ timestamp: d.timestamp, value: d.conn_packet_count_rate })
            connPacketBytesRate.push({ timestamp: d.timestamp, value: d.conn_packet_bytes_rate })
            connackPacketCountRate.push({ timestamp: d.timestamp, value: d.connack_packet_count_rate })
            connackPacketBytesRate.push({ timestamp: d.timestamp, value: d.connack_packet_bytes_rate })

            pingPacketCountRate.push({ timestamp: d.timestamp, value: d.ping_packet_count_rate })
            pingPacketBytesRate.push({ timestamp: d.timestamp, value: d.ping_packet_bytes_rate })
            pongPacketCountRate.push({ timestamp: d.timestamp, value: d.pong_packet_count_rate })
            pongPacketBytesRate.push({ timestamp: d.timestamp, value: d.pong_packet_bytes_rate })
        }
        connectionsRef.value = [{
            name: "连接数",
            data: connections,

        }]

        pingPacketCountRateRef.value = [
            {
                name: "ping",
                data: pingPacketCountRate,
            },
            {
                name: "pong",
                data: pongPacketCountRate,
            },
        ]
        pingPacketBytesRateRef.value = [
            {
                name: "ping",
                data: pingPacketBytesRate,
            },
            {
                name: "pong",
                data: pongPacketBytesRate,
            },
        ]
        onlineUserCountRef.value = [
            {
                name: "在线用户",
                data: onlineUserCount,
            },
        ]
        sendPacketCountRateRef.value = [
            {
                name: "发送包",
                data: sendPacketCountRate,
            },
            {
                name: "发送应答包",
                data: sendackPacketCountRate
            },
        ]
        sendPacketBytesRateRef.value = [
            {
                name: "发送包",
                data: sendPacketBytesRate,
            },
            {
                name: "发送应答包",
                data: sendackPacketBytesRate
            },
        ]

        recvPacketCountRateRef.value = [
            {
                name: "接受包",
                data: recvPacketCountRate
            },
            {
                name: "接受应答包",
                data: recvackPacketCountRate,
            },
        ]
        recvPacketBytesRateRef.value = [
            {
                name: "接受包",
                data: recvPacketBytesRate,
            },
            {
                name: "接受应答包",
                data: recvackPacketBytesRate
            },
        ]

        connPacketCountRateRef.value = [
            {
                name: "连接包",
                data: connPacketCountRate
            },
            {
                name: "连接应答包",
                data: connackPacketCountRate
            },
        ]
        connPacketBytesRateRef.value = [
            {
                name: "连接包",
                data: connPacketBytesRate,
            },
            {
                name: "连接应答包",
                data: connackPacketBytesRate
            },
        ]

    }).catch((err) => {
        alert(err)
    })
}

const onNodeChange = (e: any) => {
    selectedNodeId.value = e.target.value
    loadMetrics()
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
            <div class="text-sm ml-3">
                <label>节点</label>
                <select class="select select-bordered  max-w-xs select-sm w-40 ml-2" v-on:change="onNodeChange">
                    <option value="0" :selected="selectedNodeId==0">所有</option>
                    <option v-for="node in nodeTotal.data" :selected="node.id == selectedNodeId">{{ node.id }}</option>
                </select>
            </div>

            <div class="text-sm ml-10">
                <select class="select select-bordered  max-w-xs select-sm w-40 ml-2" v-on:change="onLatestChange">
                    <option selected :value="60*5">过去5分钟</option>
                    <option :value="60*30">过去30分钟</option>
                    <option :value="60*60">过去1小时</option>
                    <option :value="60*60*6">过去6小时</option>
                    <option :value="60*60*24">过去1天</option>
                    <option :value="60*60*24*3">过去3天</option>
                    <option :value="60*60*24*7">过去7天</option>
                </select>
            </div>
            <button class="btn btn-sm ml-10" v-on:click="onRefresh">立马刷新</button>
        </div>
        <br />
        <div class="flex flex-wrap justify-left" v-if="App.shard().systemSetting.prometheusOn">
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="connectionsRef" title="长连接数" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="onlineUserCountRef" title="在线用户" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="sendPacketCountRateRef" title="发送包(个)" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="sendPacketBytesRateRef" title="发送包(字节)" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="recvPacketCountRateRef" title="接收包(个)" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="recvPacketBytesRateRef" title="接收包字节(字节)" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="connPacketCountRateRef" title="连接包(个)" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="connPacketBytesRateRef" title="连接包字节(字节)" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="pingPacketCountRateRef" title="ping包(个)" />
                </div>
            </div>
            <div class="pl-5">
                <div class="w-[30%] h-[20rem]  min-w-[20rem] shadow-md p-5">
                    <MonitorPanel :data="pingPacketBytesRateRef" title="ping包字节(字节)" />
                </div>
            </div>
        </div>
        <div class="text text-center mt-10 text-red-500" v-else>
                监控功能未开启，请查看官网文档 https://githubim.com
        </div>
    </div>

</template>