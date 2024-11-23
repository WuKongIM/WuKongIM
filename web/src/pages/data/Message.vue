<script setup lang="ts">
// API 接口
import { dataApi } from '@/api/modules/data-api';
// 常量
import { CHANNEL_TYPE } from '@/constants';
import { base64Decode } from '@/utils';

import { VxeFormListeners, VxeFormProps } from 'vxe-pc-ui';

import type { VxeGridInstance, VxeGridProps } from 'vxe-table';

import { useRoute, useRouter } from 'vue-router';

const route = useRoute();
const router = useRouter();

/**
 * 查询条件
 * */
const formOptions = reactive<VxeFormProps<any>>({
  data: {
    node_id: '',
    from_uid: '',
    channel_type: null,
    channel_id: null,
    payload: '',
    message_id: '',
    offset_message_id: 0,
    offset_message_seq: 0,
    client_msg_no: '',
    limit: 20,
    pre: 0
  },
  items: [
    {
      field: 'from_uid',
      title: '发送者',
      itemRender: { name: 'ElInput', props: { placeholder: '请输入发送者' } }
    },
    {
      field: 'channel_type',
      title: '频道类型',
      itemRender: {
        name: 'ElSelect',
        props: {
          placeholder: '请选择频道类型',
          style: { width: '180px' }
        },
        options: CHANNEL_TYPE
      }
    },
    {
      field: 'channel_id',
      title: '频道ID',
      itemRender: { name: 'ElInput', props: { placeholder: '请输入频道ID' } }
    },
    {
      field: 'message_id',
      title: '消息ID',
      itemRender: { name: 'ElInput', props: { placeholder: '请输入消息ID' } }
    },
    {
      field: 'payload',
      title: '消息内容',
      itemRender: { name: 'ElInput', props: { placeholder: '请输入消息内容' } }
    },
    {
      field: 'client_msg_no',
      title: '客户端编号',
      itemRender: { name: 'ElInput', props: { placeholder: '请输入客户端编号' } }
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

/**
 * 表格
 **/
const tableRef = ref<VxeGridInstance<any>>();
const currentPage = ref(1); // 当前页
const hasPrev = ref<boolean>(false); // 是否有上一页
const hasNext = ref<boolean>(true); // 是否有下一页

const loadList = async (query: any) => {
  const res = await dataApi.searchMessages({ ...query });
  if (res.data) {
    hasNext.value = res.data.length < formOptions.data.limit;
    hasPrev.value = currentPage.value <= 1;
    return res.data;
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
    ajax: {
      query: () => {
        return loadList(formOptions.data);
      }
    }
  },
  columns: [
    { type: 'seq', title: '序号', width: 54 },
    { field: 'message_id', title: '消息ID', minWidth: 140 },
    { field: 'message_seq', title: '消息序号', minWidth: 100 },
    { field: 'from_uid', title: '发送者', minWidth: 100 },
    { field: 'channel_id', title: '接受频道', minWidth: 100 },
    {
      field: 'channel_type',
      title: '接受频道类型',
      minWidth: 80,
      formatter({ cellValue }) {
        const item = CHANNEL_TYPE.find(item => item.value === cellValue);
        return item ? item.label : '';
      }
    },
    {
      field: 'payload',
      title: '消息内容',
      minWidth: 280,
      formatter({ cellValue }) {
        return base64Decode(cellValue);
      }
    },
    { field: 'timestamp_format', title: '发送时间', minWidth: 120 },
    { field: 'client_msg_no', title: '客户端唯一编号', minWidth: 240 },
    { field: 'action', title: '操作', width: 90, fixed: 'right', slots: { default: 'action' } }
  ]
});

/**
 * 分页切换
 */
const onPage = (type: 0 | 1) => {
  let offset_message_id = 0;
  let offset_message_seq = 0;
  const tableData = tableRef.value?.getData();
  // 下一页
  if (type === 0) {
    currentPage.value = currentPage.value + 1;
    if (tableData && tableData.length > 0) {
      offset_message_id = tableData[tableData.length - 1].message_id;
      offset_message_seq = tableData[tableData.length - 1].message_seq;
    }
  }

  // 上一页
  if (type === 1 && currentPage.value > 1) {
    currentPage.value = currentPage.value - 1;
    if (tableData && tableData.length > 0) {
      offset_message_id = tableData[0].message_id;
      offset_message_seq = tableData[0].message_seq;
    }
  }

  console.log(currentPage.value);
  formOptions.data = {
    ...formOptions.data,
    offset_message_id,
    offset_message_seq,
    pre: type
  };

  tableRef.value?.commitProxy('query');
};

const onPageTrace = (row: any) => {
  router.push({
    path: '/monitor/trace',
    query: {
      clientMsgNo: row.client_msg_no
    }
  });
};

onMounted(() => {
  // 频道类型
  if (route.query?.channel_type) {
    formOptions.data = {
      ...formOptions.data,
      channel_type: Number(route.query.channel_type)
    };
  }
  // 频道ID
  if (route.query?.channel_id) {
    formOptions.data = {
      ...formOptions.data,
      channel_id: route.query.channel_id
    };
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
          <el-button native-type="reset">重置</el-button>
        </template>
      </vxe-form>
    </div>
    <!-- E 查询条件 -->

    <!-- S 表格 -->
    <div class="flex-1 card !pt-4px overflow-hidden">
      <vxe-grid ref="tableRef" v-bind="gridOptions">
        <template #tools>
          <el-space>
            <el-button type="primary" :disabled="hasPrev" @click="onPage(1)">上一页</el-button>
            <el-button type="primary" :disabled="hasNext" @click="onPage(0)">下一页</el-button>
          </el-space>
        </template>

        <template #action="{ row }">
          <el-space>
            <el-button type="primary" link @click="onPageTrace(row)">消息轨迹</el-button>
          </el-space>
        </template>
      </vxe-grid>
    </div>
    <!-- E 表格 -->
  </wk-page>
</template>

<style scoped lang="scss"></style>

<route lang="yaml">
meta:
title: 消息
</route>
