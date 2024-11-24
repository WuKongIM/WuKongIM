<script setup lang="ts">
// API 接口
import { clusterApi } from '@/api/modules/cluster-api';

import { useRoute } from 'vue-router';
import type { VxeGridInstance, VxeGridProps } from 'vxe-table';

const route = useRoute();

/**
 * 查询条件
 */
interface IFormData {
  node_id?: string;
  slot?: string;
  log_type?: string;
  pre: number;
  next: number;
}

const formData = reactive<IFormData>({
  node_id: '',
  slot: '',
  log_type: '',
  pre: 0,
  next: 0
});

/**
 * 表格
 */
const tableRef = ref<VxeGridInstance<any>>();
const applied = ref(0);
const currentPage = ref(1); // 当前页
const hasPrev = ref<boolean>(false); // 是否有上一页
const hasNext = ref<boolean>(true); // 是否有下一页

const loadList = async (query: any) => {
  const res = await clusterApi.clusterLogs({ ...query });
  applied.value = res.applied || 0;
  if (res.logs) {
    hasNext.value = res.more !== 1;
    hasPrev.value = currentPage.value <= 1;
    return res.logs;
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
        return loadList(formData);
      }
    }
  },
  columns: [
    { type: 'seq', title: '序号', width: 54 },
    { field: 'id', title: '日志ID', width: 180 },
    { field: 'index', title: '日志下标', width: 100 },
    { field: 'term', title: '日志任期', width: 100 },
    { field: 'cmd', title: '命令类型', minWidth: 160 },
    { field: 'data', title: '命令内容', minWidth: 420 },
    { field: 'time_format', title: '日志时间', width: 160 },
    {
      field: 'status',
      title: '状态',
      width: 90,
      formatter({ row }) {
        return row.index <= applied.value ? '已应用' : '未应用';
      }
    }
  ]
});
/**
 * 分页
 * @param type
 */
const onPage = (type: 0 | 1) => {
  // 下一页
  const tableData = tableRef.value?.getData();
  if (type === 0) {
    formData.pre = 0;
    currentPage.value = currentPage.value + 1;
    if (tableData && tableData.length > 0) {
      formData.next = tableData[tableData.length - 1].index;
    }
  }

  // 上一页
  if (type === 1 && currentPage.value > 1) {
    formData.next = 0;
    currentPage.value = currentPage.value - 1;
    if (tableData && tableData.length > 0) {
      formData.pre = tableData[0].index;
    }
  }

  tableRef.value?.commitProxy('query');
};

onMounted(() => {
  if (route.query) {
    formData.node_id = (route.query?.node_id as string) || '';
    formData.log_type = (route.query?.log_type as string) || '';
    formData.slot = (route.query?.slot as string) || '';
  }
});
</script>

<template>
  <wk-page class="flex-col">
    <!-- S 表格 -->
    <div class="flex-1 card !pt-4px overflow-hidden">
      <vxe-grid ref="tableRef" v-bind="gridOptions">
        <template #tools>
          <el-space>
            <el-button type="primary" :disabled="hasPrev" @click="onPage(1)">上一页</el-button>
            <el-button type="primary" :disabled="hasNext" @click="onPage(0)">下一页</el-button>
          </el-space>
        </template>
      </vxe-grid>
    </div>
    <!-- E 表格 -->
  </wk-page>
</template>

<route lang="yaml">
meta:
title: 日志
</route>
