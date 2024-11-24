<script setup lang="ts">
// API 接口
import { clusterApi } from '@/api/modules/cluster-api';

import { ElMessage } from 'element-plus';
import MigrateSlot from './components/MigrateSlot.vue';

import type { VxeGridInstance, VxeGridProps } from 'vxe-table';
import { useRouter } from 'vue-router';
import { useUserStore } from '@/stores/modules/user';
import { ActionWrite } from '@/constants';

const router = useRouter();
const userStore = useUserStore();
/**
 * 表格
 */
const tableRef = ref<VxeGridInstance<any>>();
const toolNum = ref(0);
const loadList = async () => {
  const res = await clusterApi.slots();
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
    { field: 'id', title: '分区（槽）', minWidth: 120, fixed: 'left' },
    { field: 'leader_id', title: '领导节点', minWidth: 120 },
    { field: 'term', title: '任期', minWidth: 160 },
    { field: 'replicas', title: '副本节点', minWidth: 120 },
    { field: 'channel_count', title: '频道数量', sortable: true, minWidth: 120 },
    { field: 'log_index', title: '日志高度', sortable: true, minWidth: 120 },
    { field: 'status_format', title: '状态', minWidth: 100 },
    { field: 'action', title: '操作', width: 100, fixed: 'right', slots: { default: 'action' } }
  ]
});

/**
 * 迁移
 */
const modelMigrateSlot = ref(false);
const slotMigrateSlot = ref(0);
const replicasMigrateSlot = ref([]);
const onMigrateSlot = (row: any) => {
  if (!userStore.hasPermission('slotMigrate', ActionWrite)) {
    return ElMessage({
      message: '没有操作权限！',
      type: 'warning',
      plain: true
    });
  }
  slotMigrateSlot.value = row.id;
  replicasMigrateSlot.value = row.replicas;
  modelMigrateSlot.value = true;
};

const onSubmit = () => {
  tableRef.value?.commitProxy('query');
};

/**
 * 日志
 * @param row
 */
const onLog = (row: any) => {
  router.push({
    path: '/cluster/log',
    query: {
      slot: row.id,
      node_id: row.leader_id,
      log_type: 2
    }
  });
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
            <el-button type="primary" link @click="onMigrateSlot(row)">迁移</el-button>
            <el-button type="primary" link @click="onLog(row)">日志</el-button>
          </el-space>
        </template>
      </vxe-grid>
    </div>
    <!-- E 表格 -->

    <!-- 迁移 -->
    <MigrateSlot v-model="modelMigrateSlot" :slot="slotMigrateSlot" :replicas="replicasMigrateSlot" @submit="onSubmit" />
  </wk-page>
</template>

<style scoped lang="scss"></style>

<route lang="yaml">
meta:
  title: 分区（槽）
</route>
