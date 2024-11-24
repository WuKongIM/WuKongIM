<script setup lang="ts">
// API 接口
import { clusterApi } from '@/api/modules/cluster-api';

import type { VxeGridInstance, VxeGridProps } from 'vxe-table';
import { useRouter } from 'vue-router';

const router = useRouter();
/**
 * 表格
 */
const tableRef = ref<VxeGridInstance<any>>();
const toolNum = ref(0);
const loadList = async () => {
  const res = await clusterApi.nodes();
  toolNum.value = res.total;
  return res.data;
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
        return loadList();
      }
    }
  },
  columns: [
    { type: 'seq', title: '序号', width: 54, fixed: 'left' },
    { field: 'id', title: '节点ID', minWidth: 80, fixed: 'left' },
    {
      field: 'role',
      title: '角色',
      minWidth: 120,
      formatter({ row }) {
        if (row.is_leader == 1) {
          return '领导';
        }
        if (row.role == 1) {
          return '代理';
        }
        return '副本';
      }
    },
    { field: 'term', title: '任期', minWidth: 100 },
    {
      field: 'slot_leader_count',
      title: '槽领导/槽数量',
      minWidth: 120,
      formatter({ row }) {
        return `${row.slot_leader_count}/${row.slot_count}`;
      }
    },
    {
      field: 'allow_vote',
      title: '投票权',
      minWidth: 100,
      formatter({ cellValue }) {
        return cellValue === 1 ? '有' : '无';
      }
    },
    {
      field: 'online',
      title: '在线',
      minWidth: 100,
      formatter({ cellValue }) {
        return cellValue === 1 ? '在线' : '离线';
      }
    },
    {
      field: 'offline_count',
      title: '离线次数',
      minWidth: 100,
      formatter({ row }) {
        return row.offline_count > 0 ? `${row.offline_count}(${row.last_offline})` : `0`;
      }
    },
    { field: 'uptime', title: '运行时间', minWidth: 120 },
    { field: 'cluster_addr', title: '地址', minWidth: 160 },
    { field: 'app_version', title: '程序版本', minWidth: 120 },
    { field: 'config_version', title: '配置版本', minWidth: 120 },
    { field: 'status_format', title: '状态', minWidth: 100 },
    { field: 'action', title: '操作', width: 60, fixed: 'right', slots: { default: 'action' } }
  ]
});

/**
 * 日志
 * @param row
 */
const onLog = (row: any) => {
  router.push({
    path: '/cluster/log',
    query: {
      node_id: row.id,
    }
  })
};
</script>

<template>
  <wk-page class="flex-col">
    <!-- S 表格 -->
    <div class="flex-1 card !pt-4px overflow-hidden">
      <vxe-grid ref="tableRef" v-bind="gridOptions">
        <template #tools>
          <el-text type="primary" tag="b">共计节点总数：{{ toolNum }}</el-text>
        </template>

        <template #action="{ row }">
          <el-space>
            <el-button type="primary" link @click="onLog(row)">日志</el-button>
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
title: 节点
</route>
