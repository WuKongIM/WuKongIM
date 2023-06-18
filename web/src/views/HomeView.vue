<script  lang="ts" setup>
// import { RouterLink, RouterView } from 'vue-router'
import { onBeforeUnmount, onMounted, ref } from 'vue';
import ConnChart from '../components/ConnChart.vue'
import MsgRateChart from '../components/MsgRateChart.vue'
import MsgTrafficsChart from '../components/MsgTrafficsChart.vue'
import VarzChart from '../components/VarzChart.vue'

import APIClient from '../services/APIClient';
import { ConnInfo, Varz, newConnInfo } from '../services/Model';
import { formatMemory, formatNumber } from '../services/Utils';


const varz = ref<Varz>(new Varz())
// 当前连接数
const connData = ref<Array<number>>([])
// 上行
const upstreamPackages = ref<Array<number>>([])
const upstreamTraffics = ref<Array<number>>([])
// 下行
const downstreamPackages = ref<Array<number>>([])
const downstreamTraffics = ref<Array<number>>([])


let realtimeIntervalId: number
let varzIntervalId: number
const startFetchData = async (last: boolean) => {
  const result = await APIClient.shared.get(`/api/chart/realtime?last=${last ? "1" : ""}`)
  connData.value = result.conn_nums
  upstreamPackages.value = result.upstream_packets
  downstreamPackages.value = result.downstream_packets

  downstreamTraffics.value = result.downstream_traffics
  upstreamTraffics.value = result.upstream_traffics
  if (!last) {
    startRealtimeData()
  }

}

const startFetchVarz = async () => {
  const result = await APIClient.shared.get(`/api/varz?show=conn`)
  const v = new Varz()
  v.inMsgs = result.in_msgs
  v.inBytes = result.in_bytes
  v.outMsgs = result.out_msgs
  v.outBytes = result.out_bytes
  v.cpu = result.cpu
  v.mem = result.mem
  v.uptime = result.uptime

  const connObjs = result.conns
  const conns = new Array<ConnInfo>()
  if (connObjs) {
    for (const connObj of connObjs) {
      const connInfo = newConnInfo(connObj)
      conns.push(connInfo)
    }
  }
  v.conns = conns
  varz.value = v
}

const starVarzData = () => {
  varzIntervalId = window.setInterval(async () => {
    startFetchVarz()
  }, 1000)
};

const startRealtimeData = () => {
  realtimeIntervalId = window.setInterval(async () => {
    startFetchData(true)
  }, 1000);
};

onMounted(() => {
  startFetchData(false)
  starVarzData()
})

onBeforeUnmount(() => {
  window.clearInterval(realtimeIntervalId)
  window.clearInterval(varzIntervalId)
})

</script>

<template>
  <div class="home">
    <VarzChart :varz="varz"></VarzChart>
    <!-- charts -->
    <div class="flex flex-wrap justify-between">
      <div class="w-[30%] h-[20rem] max-w-[30rem] min-w-[20rem] shadow-md p-5">
        <ConnChart class="w-full h-full" :data="connData"></ConnChart>
      </div>
      <div class="w-[30%] h-[20rem] max-w-[30rem] min-w-[20rem] shadow-md p-5">
        <MsgRateChart class="w-full h-full" :upstream="upstreamPackages" :downstream="downstreamPackages">
        </MsgRateChart>
      </div>

      <div class="w-[30%] h-[20rem] max-w-[30rem] min-w-[20rem] shadow-md p-5">
        <MsgTrafficsChart class="w-full h-full" :upstream="upstreamTraffics" :downstream="downstreamTraffics">
        </MsgTrafficsChart>
      </div>
    </div>
    <div class="mt-20">
      <div class="overflow-x-auto">
        <table class="table">
          <!-- head -->
          <thead>
            <tr>
              <th>ID</th>
              <th>用户UID</th>
              <th>收到消息</th>
              <th>发出消息</th>
              <th>收到字节数</th>
              <th>发出字节数</th>
              <th>待发送字节</th>
              <th>连接地址</th>
              <th>存活时间</th>
              <th>空闲时间</th>
              <th>协议版本</th>
              <th>设备</th>
              <th>设备ID</th>
            </tr>
          </thead>
          <tbody>
            <!-- row 1 -->
            <tr v-for="conn in varz.conns">
              <th>{{ conn.id }}</th>
              <td>{{ conn.uid }}</td>
              <td>{{ formatNumber(conn.inMsgs) }}</td>
              <td>{{ formatNumber(conn.outMsgs) }}</td>
              <td>{{ formatMemory(conn.inBytes) }}</td>
              <td>{{ formatMemory(conn.outBytes) }}</td>
              <td>{{ formatMemory(conn.pendingBytes) }}</td>
              <td>{{ conn.ip }}:{{ conn.port }}</td>
              <td>{{ conn.uptime }}</td>
              <td>{{ conn.idle }}</td>
              <td>{{ conn.version }}</td>
              <td>{{ conn.device }}</td>
              <td>{{ conn.deviceID }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  </div>
</template>
<style>
.content {
  padding: 48px 48px 48px 48px;
  background-color: #fff;
  border-top-left-radius: 10px;
  border-top-right-radius: 10px;
  box-shadow: 0 8px 24px #0000000d;
}
</style>