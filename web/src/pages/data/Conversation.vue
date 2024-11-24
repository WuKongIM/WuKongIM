<script setup lang="ts">
// API 接口
import { dataApi } from '@/api/modules/data-api';

import { VxeFormListeners, VxeFormProps } from 'vxe-pc-ui';

import type { VxeGridInstance, VxeGridProps } from 'vxe-table';

import { useRoute } from 'vue-router';

const route = useRoute();

/**
 * 查询条件
 * */
const formOptions = reactive<VxeFormProps<any>>({
  data: {
    uid: ''
  },
  items: [
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

/**
 * 表格
 **/
const tableRef = ref<VxeGridInstance<any>>();

const loadList = async (query: any) => {
  const res = await dataApi.conversations({ ...query });
  if (res.data) {
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
    { field: 'uid', title: '用户UID', minWidth: 220 },
    { field: 'type_format', title: '会话类型', minWidth: 120 },
    { field: 'channel_id', title: '频道ID', minWidth: 220 },
    { field: 'channel_type_format', title: '频道类型', minWidth: 120 },
    { field: 'last_msg_seq', title: '最新消息序号', minWidth: 120 },
    { field: 'readed_to_msg_seq', title: '已读消息序号', minWidth: 120 },
    { field: 'unread_count', title: '未读数量', minWidth: 120 },
    { field: 'updated_at_format', title: '最后会话时间', minWidth: 140 }
  ]
});

onMounted(() => {
  if (route.query?.uid) {
    formOptions.data = {
      uid: route.query.uid
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
      <vxe-grid ref="tableRef" v-bind="gridOptions"></vxe-grid>
    </div>
    <!-- E 表格 -->
  </wk-page>
</template>

<style scoped lang="scss"></style>

<route lang="yaml">
meta:
title: 会话
</route>
