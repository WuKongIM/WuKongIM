<script name="Replicas" lang="ts" setup>
// API 接口
import { clusterApi } from '@/api/modules/cluster-api.ts';

import type { VxeGridInstance, VxeGridProps } from 'vxe-table';

interface IProps {
  channelId: string;
  channelType: number;
}

/**
 * Props
 */
const props = withDefaults(defineProps<IProps>(), {
  channelId: '',
  channelType: 0
});

const showModel = defineModel<boolean>();
const loadingModel = ref(false);

watch(
  () => showModel.value,
  val => {
    if (val) {
      if (props.channelId) {
        tableRef.value?.commitProxy('query');
      }
    }
  }
);

/**
 * 表格
 */
interface ITableItem {
  uid: string;
}

const tableRef = ref<VxeGridInstance<ITableItem>>();

const apiLoadList = async () => {
  const res = await clusterApi.channelReplicas({ channelId: props.channelId, channelType: props.channelType });
  return res || [];
};

const gridOptions = reactive<VxeGridProps<ITableItem>>({
  border: true,
  showOverflow: true,
  height: 'auto',
  stripe: true,
  rowConfig: { isCurrent: true, isHover: true },
  scrollY: { enabled: true, gt: 0 },
  proxyConfig: {
    ajax: {
      query: () => {
        return apiLoadList();
      }
    }
  },
  columns: [
    { type: 'seq', title: '序号', width: 64 },
    { field: 'replica_id', title: '节点ID', minWidth: 120 },
    { field: 'role_format', title: '节点角色', minWidth: 120 },
    { field: 'last_msg_seq', title: '消息高度', minWidth: 120 },
    { field: 'last_msg_time_format', title: '最后消息时间', minWidth: 120 },
    {
      field: 'running',
      title: '是否运行',
      width: 120,
      formatter({ cellValue }) {
        return cellValue === 1 ? '运行中' : '未运行';
      }
    }
  ]
});
</script>

<template>
  <vxe-modal
    v-model="showModel"
    title="副本"
    :width="860"
    :height="560"
    :confirm-closable="false"
    :padding="false"
    :loading="loadingModel"
    show-maximize
  >
    <wk-page class="flex-col wk-page-bg">
      <!-- S 表格 -->
      <div class="flex-1 card pt-0px overflow-hidden">
        <vxe-grid ref="tableRef" v-bind="gridOptions"></vxe-grid>
      </div>
      <!-- E 表格 -->
    </wk-page>
  </vxe-modal>
</template>

<style scoped lang="scss">
.wk-page-bg {
  background-color: var(--el-bg-color-page);
}
</style>
