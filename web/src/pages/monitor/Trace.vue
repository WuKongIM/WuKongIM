<script setup lang="ts">
// API接口
import { monitorApi } from '@/api/modules/monitor-api';
import { VxeFormListeners, VxeFormProps } from 'vxe-pc-ui';

import WkSpanNode from '@/components/WkSpanNode.vue';
import WkTextNode from '@/components/WkTextNode.vue';

import { Graph } from '@antv/x6';
import { register } from '@antv/x6-vue-shape';

import { useRoute } from 'vue-router';
const route = useRoute();

/**
 * 查询条件
 * */
const formOptions = reactive<VxeFormProps<any>>({
  data: {
    message_id: 0,
    client_msg_no: null
  },
  items: [
    {
      field: 'client_msg_no',
      title: '消息编号',
      itemRender: {
        name: 'ElInput',
        props: { placeholder: '请输入消息编号' }
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
    drawFlow();
  }
};

const nodeWidth = 180;
const nodeHeight = 70;
const loading = ref(false);
const error = ref('');
const recvackResult = ref();

const modalContent = ref('');

register({
  shape: 'spanNode',
  width: nodeWidth,
  height: nodeHeight,
  component: WkSpanNode
});

register({
  shape: 'textNode',
  width: nodeWidth,
  height: nodeHeight,
  component: WkTextNode
});

const requestMessageTrace = ({ width, height }: any) => {
  return monitorApi.messageTraces({
    ...formOptions.data,
    width: width,
    height: height,
    since: 60 * 60 * 24
  });
};

const requestMessageRecvackTraces = ({ nodeId, messageId }: any) => {
  return monitorApi.messageRecvackTraces({
    nodeId: nodeId,
    messageId: messageId,
    since: 60 * 60 * 24
  });
};

const drawFlow = async () => {
  const container = document.getElementById('container');
  const containerWidth = container!.offsetWidth;
  const containerHeight = container!.offsetHeight;

  error.value = '';
  loading.value = true;
  const result = await requestMessageTrace({
    width: containerWidth,
    height: containerHeight
  }).catch(e => {
    loading.value = false;
    error.value = e.msg;
  });
  loading.value = false;

  const nodes = [];
  const edges = [];
  if (result.nodes) {
    for (let i = 0; i < result.nodes.length; i++) {
      const node = result.nodes[i];
      nodes.push({
        id: node.id,
        shape: node.shape,
        x: node.x,
        y: node.y,
        data: {
          name: node.name,
          time: node.time,
          icon: node.icon,
          duration: node.duration,
          description: node.description,
          data: node.data
        }
      });
    }
  }

  if (result.edges) {
    for (let i = 0; i < result.edges.length; i++) {
      const edge = result.edges[i];

      const edgeObj = {
        source: edge.source,
        target: edge.target,
        connector: { name: 'smooth' },
        attrs: {
          line: {
            stroke: '#1890ff',
            targetMarker: 'classic',
            strokeDasharray: 0
          }
        }
      };

      if (edge.shape == 'dashed') {
        edgeObj.attrs.line.strokeDasharray = 5;
      }
      edges.push(edgeObj);
    }
  }

  const graph = new Graph({
    container: container!,
    width: containerWidth,
    height: containerHeight,
    panning: true,
    background: {
      color: '#F2F7FA'
    },
    grid: true,
    mousewheel: true
  });

  graph.addNodes(nodes);
  graph.addEdges(edges);

  graph.on('node:click', async ({ node }) => {
    const nodeId = node.id as string;
    if (nodeId === 'processMessage') {
      const data = node.data.data;
      modalContent.value = `
        发送者: ${data.uid} <br>
        发送设备: ${data.deviceId} <br>
        设备类型: ${data.deviceFlag} <br>
        设备等级: ${data.deviceLevel} <br>
        接受频道: ${data.channelId} <br>
        频道类型: ${data.channelType} <br>
      `;
    } else if (nodeId.startsWith('deliverOnline') || nodeId.startsWith('deliverOffline')) {
      const data = node.data.data;
      var uids: string[] = [];
      if (data.uids) {
        data.uids.split(',').forEach((uid: string) => {
          uids.push(uid);
        });
      }
      modalContent.value = '<div class="flex flex-wrap space-x-2">';
      for (let i = 0; i < uids.length; i++) {
        modalContent.value += `
            <a href="#" class="inline-block"> ${uids[i]}</a>
        `;
      }
      modalContent.value += '</div>';
    } else if (nodeId.startsWith('processRecvack')) {
      const data = node.data.data;
      const nodeId = data.nodeId;
      const messageId = data.messageId;
      recvackResult.value = await requestMessageRecvackTraces({
        node_id: nodeId,
        message_id: messageId
      });
      if (recvackResult.value) {
        const dialog = document.getElementById('recvackTable') as HTMLDialogElement;
        dialog.showModal();
      }
      return;
    } else {
      return;
    }
    const dialog = document.getElementById('content') as HTMLDialogElement;
    dialog.showModal();
  });
};

onMounted(() => {
  if (route.query?.clientMsgNo) {
    formOptions.data = {
      ...formOptions.data,
      client_msg_no: route.query.clientMsgNo
    };
    drawFlow();
  }
});
</script>

<template>
  <wk-page class="flex-col">
    <!-- S 查询条件 -->
    <div class="mb-12px pt-4px pb-4px card">
      <vxe-form v-bind="formOptions" v-on="formEvents">
        <template #action>
          <el-button native-type="submit" type="primary">查询</el-button>
        </template>
      </vxe-form>
    </div>
    <!-- E 查询条件 -->

    <div v-loading="loading" class="flex-1 card overflow-hidden">
      <div id="container" class="w-full h-full"></div>
    </div>
  </wk-page>
</template>

<style scoped lang="scss"></style>

<route lang="yaml">
meta:
  title: 消息追踪
</route>
