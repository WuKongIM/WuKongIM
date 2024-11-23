<script setup lang="ts">
import { monitorApi } from '@/api/modules/monitor-api';

// 常量
import { LATEST_TIME } from '@/constants';

import { setSeries } from '@/utils';

import { VxeFormListeners, VxeFormProps } from 'vxe-pc-ui';

const formOptions = reactive<VxeFormProps<any>>({
  data: {
    latest: 300
  },
  items: [
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
      latest: 300
    };
    loadMetrics();
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

const channelActiveCountRef = ref<Series[]>([]);

const channelProposeCountRateRef = ref<Series[]>([]);
const channelProposeFailedCountRate = ref<Series[]>([]);
const channelProposeLatencyOver500msRate = ref<Series[]>([]);
const channelProposeLatencyUnder500msRate = ref<Series[]>([]);

const msgPingIncomingCountRateRef = ref<Series[]>([]);
const msgPingIncomingBytesRateRef = ref<Series[]>([]);
const msgPingOutgoingCountRateRef = ref<Series[]>([]);
const msgPingOutgoingBytesRateRef = ref<Series[]>([]);

const loadMetrics = () => {
  monitorApi.clusterMetrics(formOptions.data).then(res => {
    msgIncomingCountRateRef.value = [];
    msgIncomingBytesRateRef.value = [];
    msgOutgoingCountRateRef.value = [];
    msgOutgoingBytesRateRef.value = [];

    sendPacketIncomingCountRateRef.value = [];
    sendPacketIncomingBytesRateRef.value = [];
    sendPacketOutgoingBytesRateRef.value = [];
    sendPacketOutgoingCountRateRef.value = [];

    msgSyncIncomingCountRateRef.value = [];
    msgSyncIncomingBytesRateRef.value = [];
    msgSyncOutgoingCountRateRef.value = [];
    msgSyncOutgoingBytesRateRef.value = [];

    channelActiveCountRef.value = [];

    channelProposeCountRateRef.value = [];
    channelProposeFailedCountRate.value = [];
    channelProposeLatencyOver500msRate.value = [];
    channelProposeLatencyUnder500msRate.value = [];

    msgPingIncomingCountRateRef.value = [];
    msgPingIncomingBytesRateRef.value = [];
    msgPingOutgoingCountRateRef.value = [];
    msgPingOutgoingBytesRateRef.value = [];

    for (let index = 0; index < res.length; index++) {
      const d = res[index];
      setSeries('msg_incoming_count_rate', d, msgIncomingCountRateRef.value);
      setSeries('msg_incoming_bytes_rate', d, msgIncomingBytesRateRef.value);
      setSeries('msg_outgoing_count_rate', d, msgOutgoingCountRateRef.value);
      setSeries('msg_outgoing_bytes_rate', d, msgOutgoingBytesRateRef.value);

      setSeries('channel_msg_incoming_count_rate', d, channelMsgIncomingCountRateRef.value);
      setSeries('channel_msg_incoming_bytes_rate', d, channelMsgIncomingBytesRateRef.value);
      setSeries('channel_msg_outgoing_count_rate', d, channelMsgOutgoingCountRateRef.value);
      setSeries('channel_msg_outgoing_bytes_rate', d, channelMsgOutgoingBytesRateRef.value);

      setSeries('sendpacket_incoming_count_rate', d, sendPacketIncomingCountRateRef.value);
      setSeries('sendpacket_incoming_bytes_rate', d, sendPacketIncomingBytesRateRef.value);
      setSeries('sendpacket_outgoing_bytes_rate', d, sendPacketOutgoingBytesRateRef.value);
      setSeries('sendpacket_outgoing_count_rate', d, sendPacketOutgoingCountRateRef.value);

      setSeries('msg_sync_incoming_bytes_rate', d, msgSyncIncomingBytesRateRef.value);
      setSeries('msg_sync_incoming_count_rate', d, msgSyncIncomingCountRateRef.value);
      setSeries('msg_sync_outgoing_bytes_rate', d, msgSyncOutgoingBytesRateRef.value);
      setSeries('msg_sync_outgoing_count_rate', d, msgSyncOutgoingCountRateRef.value);

      setSeries('channel_active_count', d, channelActiveCountRef.value);

      setSeries('channel_propose_count_rate', d, channelProposeCountRateRef.value);
      setSeries('channel_propose_failed_count_rate', d, channelProposeFailedCountRate.value);
      setSeries('channel_propose_latency_over_500ms_rate', d, channelProposeLatencyOver500msRate.value);
      setSeries('channel_propose_latency_under_500ms_rate', d, channelProposeLatencyUnder500msRate.value);

      setSeries('msg_ping_incoming_count_rate', d, msgPingIncomingCountRateRef.value);
      setSeries('msg_ping_incoming_bytes_rate', d, msgPingIncomingBytesRateRef.value);
      setSeries('msg_ping_outgoing_count_rate', d, msgPingOutgoingCountRateRef.value);
      setSeries('msg_ping_outgoing_bytes_rate', d, msgPingOutgoingBytesRateRef.value);
    }
  });
};

onMounted(() => {
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
            <wk-monitor-panel :data="channelProposeCountRateRef" title="频道提案(次)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="channelProposeLatencyUnder500msRate" title="频道提案小于500ms(次)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="channelProposeLatencyOver500msRate" title="频道提案大于500ms(次)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="channelProposeFailedCountRate" title="频道失败提案(次)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="msgIncomingCountRateRef" title="流入总消息(个)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="msgIncomingBytesRateRef" title="流入总消息(字节)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="msgOutgoingCountRateRef" title="流出总消息(个)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="msgOutgoingBytesRateRef" title="流出总消息(字节)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="channelMsgIncomingBytesRateRef" title="流入频道消息(字节)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="channelMsgOutgoingCountRateRef" title="流出频道消息(个)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="channelMsgOutgoingBytesRateRef" title="流出频道消息(字节)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="msgSyncIncomingCountRateRef" title="流入同步消息(个)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="msgSyncIncomingBytesRateRef" title="流入同步消息(字节)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="msgSyncOutgoingCountRateRef" title="流出同步消息(个)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="msgOutgoingBytesRateRef" title="流出同步消息(字节)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="sendPacketIncomingCountRateRef" title="转入的发送包(个)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="sendPacketIncomingBytesRateRef" title="转入的发送包(字节)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="sendPacketOutgoingCountRateRef" title="转出的发送包(个)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="sendPacketOutgoingBytesRateRef" title="转出的发送包(字节)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="msgPingIncomingCountRateRef" title="流入的Ping(个)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="msgPingIncomingBytesRateRef" title="流入的Ping(字节)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="msgPingOutgoingCountRateRef" title="流出的Ping(个)" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="msgPingOutgoingBytesRateRef" title="流出的Ping(字节)" />
          </div>
        </div>
      </el-scrollbar>
    </div>
  </wk-page>
</template>

<style scoped lang="scss"></style>

<route lang="yaml">
meta:
  title: 分布式
</route>
