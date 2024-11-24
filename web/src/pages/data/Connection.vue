<script setup lang="ts">
// API 接口
import { clusterApi } from '@/api/modules/cluster-api';
import { dataApi } from '@/api/modules/data-api';

import { VxeFormListeners, VxeFormProps } from 'vxe-pc-ui';

import type { VxeGridInstance, VxeGridProps } from 'vxe-table';

/**
 * 查询条件
 * */
const nodeList = ref<any[]>([]);
const formOptions = reactive<VxeFormProps<any>>({
  data: {
    uid: '',
    node_id: null,
    sort: 'id',
    limit: 100
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
      field: 'uid',
      title: '用户UID',
      itemRender: { name: 'ElInput', props: { placeholder: '请输入用户UID' } }
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
    tableRef.value?.commitProxy('query');
  },
  /** 重置 **/
  reset() {
    tableRef.value?.commitProxy('query');
  }
};

const getNodes = async () => {
  const nodes: any[] = [];
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
    tableRef.value?.commitProxy('query');
  }
};

/**
 * 表格
 **/
const tableRef = ref<VxeGridInstance<any>>();
const toolNum = ref(0);

const loadList = async (query: any) => {
  const res = await dataApi.connections({ ...query });
  if (res.connections) {
    toolNum.value = res.total;
    return res.connections;
  }
  return [];
};

const gridOptions = reactive<VxeGridProps<any>>({
  showOverflow: true,
  height: 'auto',
  border: true,
  stripe: true,
  rowConfig: {
    isCurrent: true,
    isHover: true
  },
  scrollY: {
    enabled: true,
    gt: 0
  },
  toolbarConfig: {
    slots: {
      buttons: 'tools'
    },
    refresh: {
      icon: 'vxe-icon-refresh',
      iconLoading: 'vxe-icon-refresh roll'
    },
    zoom: {
      iconIn: 'vxe-icon-fullscreen',
      iconOut: 'vxe-icon-minimize'
    },
    custom: true
  },
  proxyConfig: {
    autoLoad: false,
    ajax: {
      query: () => {
        return loadList(formOptions.data);
      }
    }
  },
  columns: [
    { type: 'seq', title: '序号', width: 54 },
    { field: 'id', title: '连接ID', minWidth: 120 },
    { field: 'uid', title: '用户UID', minWidth: 120 },
    { field: 'in_msgs', title: '发出消息数', minWidth: 100 },
    { field: 'out_msgs', title: '收到消息数', minWidth: 100 },
    { field: 'in_msg_bytes', title: '发出消息字节数', minWidth: 120, formatter: 'formatMemory' },
    { field: 'out_msg_bytes', title: '收到消息字节数', minWidth: 120, formatter: 'formatMemory' },
    { field: 'in_packets', title: '发出报文数', minWidth: 100 },
    { field: 'out_packets', title: '收到报文数', minWidth: 100 },
    { field: 'in_packet_bytes', title: '发出报文字节数', minWidth: 120, formatter: 'formatMemory' },
    { field: 'out_packet_bytes', title: '收到报文字节数', minWidth: 120, formatter: 'formatMemory' },
    {
      field: 'address',
      title: '连接地址',
      minWidth: 180,
      formatter({ row }) {
        return `${row.ip}:${row.port}`;
      }
    },
    { field: 'uptime', title: '存活时间', minWidth: 120 },
    { field: 'idle', title: '空闲时间', minWidth: 120 },
    { field: 'version', title: '协议版本', minWidth: 100 },
    { field: 'device', title: '设备', minWidth: 100 },
    { field: 'device_id', title: '设备编号', minWidth: 290 },
    { field: 'proxy_type_format', title: '代理类型', minWidth: 100 },
    { field: 'leader_id', title: '领导节点', minWidth: 100 }
  ]
});

onMounted(() => {
  getNodes();
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

    <!-- S 表格 -->
    <div class="flex-1 card !pt-4px overflow-hidden">
      <vxe-grid ref="tableRef" v-bind="gridOptions"
        >total
        <template #tools>
          <el-text type="primary" tag="b">共计节点总数：{{ toolNum }}</el-text>
        </template>
      </vxe-grid>
    </div>
    <!-- E 表格 -->
  </wk-page>
</template>

<style scoped lang="scss"></style>

<route lang="yaml">
meta:
  title: 连接
</route>
