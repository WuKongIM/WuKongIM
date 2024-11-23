<script setup lang="ts">
// API 接口
import { clusterApi } from '@/api/modules/cluster-api';
import { monitorApi } from '@/api/modules/monitor-api';

// 常量
import { LATEST_TIME } from '@/constants';

import { VxeFormListeners, VxeFormProps } from 'vxe-pc-ui';

/**
 * 查询条件
 * */
const nodeList = ref<any[]>([]);
const formOptions = reactive<VxeFormProps<any>>({
  data: {
    node_id: 0,
    latest: 300
  },
  items: [
    {
      field: 'node_id',
      title: '节点',
      itemRender: {
        name: 'ElSelect',
        props: {
          placeholder: '请选择',
          style: {
            width: '180px'
          }
        },
        options: nodeList
      }
    },
    {
      field: 'latest',
      title: '时间',
      itemRender: {
        name: 'ElSelect',
        props: {
          placeholder: '请选择',
          style: {
            width: '180px'
          }
        },
        options: LATEST_TIME
      }
    },
    {
      align: 'center',
      slots: { default: 'action' }
    }
  ]
});
/** 搜索事件 **/
const formEvents: VxeFormListeners<any> = {
  /** 查询 **/
  submit() {
    loadMetrics();
  },
  /** 重置 **/
  reset() {
    formOptions.data = {
      node_id: 0,
      latest: 300
    };
    loadMetrics();
  }
};

const getNodes = async () => {
  const nodes: any[] = [
    {
      value: 0,
      label: '所有'
    }
  ];
  const res = await clusterApi.simpleNodes();
  if (res.data.length > 0) {
    res.data.map((item: any) => {
      nodes.push({
        value: item.id,
        label: item.id
      });
    });
  }
  nodeList.value = nodes;
  if (nodes.length > 0) {
    formOptions.data = {
      ...formOptions.data,
      node_id: nodes[0].value
    };
  }
};

interface Series {
  name?: string;
  type?: string;
  data: Point[];
}

interface Point {
  timestamp?: number;
  value?: number;
}

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

const loadMetrics = () => {
  monitorApi.apppMetrics(formOptions.data).then(res => {
    const connections = [];
    const onlineUserCount = [];
    const onlineDeviceCount = [];

    const sendPacketCountRate = [];
    const sendPacketBytesRate = [];
    const sendackPacketCountRate = [];
    const sendackPacketBytesRate = [];

    const recvPacketCountRate = [];
    const recvPacketBytesRate = [];
    const recvackPacketCountRate = [];
    const recvackPacketBytesRate = [];

    const connPacketCountRate = [];
    const connPacketBytesRate = [];
    const connackPacketCountRate = [];
    const connackPacketBytesRate = [];

    const pingPacketCountRate = [];
    const pingPacketBytesRate = [];
    const pongPacketCountRate = [];
    const pongPacketBytesRate = [];
    for (let index = 0; index < res.length; index++) {
      const d = res[index];
      connections.push({ timestamp: d.timestamp, value: d.conn_count });
      onlineUserCount.push({ timestamp: d.timestamp, value: d.online_user_count });
      onlineDeviceCount.push({ timestamp: d.timestamp, value: d.online_device_count });

      sendPacketCountRate.push({ timestamp: d.timestamp, value: d.send_packet_count_rate });
      sendPacketBytesRate.push({ timestamp: d.timestamp, value: d.send_packet_bytes_rate });
      sendackPacketCountRate.push({ timestamp: d.timestamp, value: d.sendack_packet_count_rate });
      sendackPacketBytesRate.push({ timestamp: d.timestamp, value: d.sendack_packet_bytes_rate });

      recvPacketCountRate.push({ timestamp: d.timestamp, value: d.recv_packet_count_rate });
      recvPacketBytesRate.push({ timestamp: d.timestamp, value: d.recv_packet_bytes_rate });
      recvackPacketCountRate.push({ timestamp: d.timestamp, value: d.recvack_packet_count_rate });
      recvackPacketBytesRate.push({ timestamp: d.timestamp, value: d.recvack_packet_bytes_rate });

      connPacketCountRate.push({ timestamp: d.timestamp, value: d.conn_packet_count_rate });
      connPacketBytesRate.push({ timestamp: d.timestamp, value: d.conn_packet_bytes_rate });
      connackPacketCountRate.push({ timestamp: d.timestamp, value: d.connack_packet_count_rate });
      connackPacketBytesRate.push({ timestamp: d.timestamp, value: d.connack_packet_bytes_rate });

      pingPacketCountRate.push({ timestamp: d.timestamp, value: d.ping_packet_count_rate });
      pingPacketBytesRate.push({ timestamp: d.timestamp, value: d.ping_packet_bytes_rate });
      pongPacketCountRate.push({ timestamp: d.timestamp, value: d.pong_packet_count_rate });
      pongPacketBytesRate.push({ timestamp: d.timestamp, value: d.pong_packet_bytes_rate });
    }
    connectionsRef.value = [
      {
        name: '连接数',
        data: connections
      }
    ];

    pingPacketCountRateRef.value = [
      {
        name: 'ping',
        data: pingPacketCountRate
      },
      {
        name: 'pong',
        data: pongPacketCountRate
      }
    ];
    pingPacketBytesRateRef.value = [
      {
        name: 'ping',
        data: pingPacketBytesRate
      },
      {
        name: 'pong',
        data: pongPacketBytesRate
      }
    ];
    onlineUserCountRef.value = [
      {
        name: '在线用户',
        data: onlineUserCount
      }
    ];
    sendPacketCountRateRef.value = [
      {
        name: '发送包',
        data: sendPacketCountRate
      },
      {
        name: '发送应答包',
        data: sendackPacketCountRate
      }
    ];
    sendPacketBytesRateRef.value = [
      {
        name: '发送包',
        data: sendPacketBytesRate
      },
      {
        name: '发送应答包',
        data: sendackPacketBytesRate
      }
    ];

    recvPacketCountRateRef.value = [
      {
        name: '接受包',
        data: recvPacketCountRate
      },
      {
        name: '接受应答包',
        data: recvackPacketCountRate
      }
    ];
    recvPacketBytesRateRef.value = [
      {
        name: '接受包',
        data: recvPacketBytesRate
      },
      {
        name: '接受应答包',
        data: recvackPacketBytesRate
      }
    ];

    connPacketCountRateRef.value = [
      {
        name: '连接包',
        data: connPacketCountRate
      },
      {
        name: '连接应答包',
        data: connackPacketCountRate
      }
    ];
    connPacketBytesRateRef.value = [
      {
        name: '连接包',
        data: connPacketBytesRate
      },
      {
        name: '连接应答包',
        data: connackPacketBytesRate
      }
    ];
  });
};

onMounted(() => {
  getNodes();
  loadMetrics();
});
</script>

<template>
  <wk-page class="flex-col">
    <!-- S 查询条件 -->
    <div class="mb-12px pt-4px pb-4px card">
      <vxe-form v-bind="formOptions" v-on="formEvents">
        <template #action>
          <el-button native-type="submit" type="primary">查询</el-button>
          <el-button native-type="reset">重置</el-button>
        </template>
      </vxe-form>
    </div>
    <!-- E 查询条件 -->

    <div class="flex-1 card overflow-hidden">
      <el-scrollbar>
        <div class="flex flex-wrap justify-left">
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="connectionsRef" title="长连接数" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="onlineUserCountRef" title="在线用户" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="sendPacketCountRateRef" title="发送包(个)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="sendPacketBytesRateRef" title="发送包(字节)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="recvPacketCountRateRef" title="接收包(个)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="recvPacketBytesRateRef" title="接收包字节(字节)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="connPacketCountRateRef" title="连接包(个)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="connPacketBytesRateRef" title="连接包字节(字节" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="pingPacketCountRateRef" title="ping包(个)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="pingPacketBytesRateRef" title="ping包字节(字节)" />
          </div>
        </div>
      </el-scrollbar>
    </div>
  </wk-page>
</template>

<style scoped lang="scss"></style>

<route lang="yaml">
meta:
  title: 应用
</route>
