<script setup lang="ts">
// API 接口
import { dataApi } from '@/api/modules/data-api.ts';
// 组件
import { VxeFormListeners, VxeFormProps } from 'vxe-pc-ui';
import Subscribers from './components/Subscribers.vue';
import Allowlist from './components/Allowlist.vue';
import Denylist from './components/Denylist.vue';

import type { VxeGridInstance, VxeGridProps } from 'vxe-table';
import { CHANNEL_TYPE } from '@/constants';

/**
 * 查询条件
 * */
const formOptions = reactive<VxeFormProps<any>>({
  data: {
    channel_type: null,
    channel_id: null,
    offset_created_at: null,
    limit: 20,
    pre: 0
  },
  items: [
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
  const res = await dataApi.searchChannels({ ...query });
  if (res.data) {
    hasNext.value = res.more !== 1;
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
    { field: 'channel_id', title: '频道ID', minWidth: 220 },
    {
      field: 'channel_type',
      title: '频道类型',
      minWidth: 120,
      formatter({ cellValue }) {
        const item = CHANNEL_TYPE.find(item => item.value === cellValue);
        return item ? item.label : cellValue;
      }
    },
    { field: 'subscriber_count', title: '订阅者数量', minWidth: 120 },
    { field: 'allowlist_count', title: '黑名单数量', minWidth: 120 },
    { field: 'status_format', title: '白名单数量', minWidth: 120 },
    { field: 'last_msg_seq', title: '最大序号', minWidth: 120 },
    { field: 'last_msg_time_format', title: '最后消息时间', minWidth: 140 },
    { field: 'slot', title: '槽位', minWidth: 120 },
    { field: 'created_at_format', title: '创建时间', minWidth: 140 },
    { field: 'updated_at_format', title: '更新时间', minWidth: 140 },
    { field: 'action', title: '操作', width: 184, fixed: 'right', slots: { default: 'action' } }
  ]
});

/**
 * 分页切换
 */
const onPage = (type: 0 | 1) => {
  // 下一页
  if (type === 0) {
    currentPage.value = currentPage.value + 1;
  }

  // 上一页
  if (type === 1 && currentPage.value > 1) {
    currentPage.value = currentPage.value - 1;
  }

  console.log(currentPage.value);
  formOptions.data = {
    ...formOptions.data,
    pre: type
  };

  tableRef.value?.commitProxy('query');
};

/**
 * 订阅者
 */
const modelSubscribers = ref(false);
const channelIdSubscribers = ref('');
const channelTypeSubscribers = ref(0);

const onSubscribers = (row: any) => {
  channelIdSubscribers.value = row.channel_id;
  channelTypeSubscribers.value = row.channel_type;
  modelSubscribers.value = true;
};

/**
 * 白名单
 */
const modelAllowlist = ref(false);
const channelIdAllowlist = ref('');
const channelTypeAllowlist = ref(0);

const onAllowlist = (row: any) => {
  channelIdAllowlist.value = row.channel_id;
  channelTypeAllowlist.value = row.channel_type;
  modelAllowlist.value = true;
};

/**
 * 黑名单
 */
const modelDenylist = ref(false);
const channelIdDenylist = ref('');
const channelTypeDenylist = ref(0);

const onDenylist = (row: any) => {
  channelIdDenylist.value = row.channel_id;
  channelTypeDenylist.value = row.channel_type;
  modelDenylist.value = true;
};
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
            <el-button type="primary" link @click="onSubscribers(row)">订阅者</el-button>
            <el-button type="primary" link @click="onAllowlist(row)">白名单</el-button>
            <el-button type="primary" link @click="onDenylist(row)">黑名单</el-button>
          </el-space>
        </template>
      </vxe-grid>
    </div>
    <!-- E 表格 -->

    <!-- 订阅者 -->
    <Subscribers v-model="modelSubscribers" :channel-id="channelIdSubscribers" :channel-type="channelTypeSubscribers" />
    <!-- 白名单 -->
    <Allowlist v-model="modelAllowlist" :channel-id="channelIdAllowlist" :channel-type="channelTypeAllowlist" />
    <!-- 黑名单 -->
    <Denylist v-model="modelDenylist" :channel-id="channelIdDenylist" :channel-type="channelTypeDenylist" />
  </wk-page>
</template>

<style scoped lang="scss"></style>

<route lang="yaml">
meta:
title: 频道
</route>
