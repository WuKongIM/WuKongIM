<script name="RecvackTraces" lang="ts" setup>
// API 接口
import { monitorApi } from '@/api/modules/monitor-api.ts';

import type { VxeGridInstance, VxeGridProps } from 'vxe-table';

interface IProps {
  nodeId: string;
  messageId: string;
}
/**
 * Props
 */
const props = withDefaults(defineProps<IProps>(), {
  nodeId: '',
  messageId: ''
});

const showModel = defineModel<boolean>();
const loadingModel = ref(false);
watch(
  () => showModel.value,
  val => {
    if (val) {
      if (props.nodeId) {
        tableRef.value?.commitProxy('query');
      }
    }
  }
);
/**
 * 表格
 */
interface ITableItem {
  conn_id: string;
  uid: string;
  device_id: string;
  time: string;
}

const tableRef = ref<VxeGridInstance<ITableItem>>();

const apiLoadList = async () => {
  const res = await monitorApi.messageRecvackTraces({ ...props, since: 60 * 60 * 24 });
  return res.data || [];
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
    { field: 'replica_id', title: '连接ID', minWidth: 120 },
    { field: 'role_format', title: '用户UID', minWidth: 120 },
    { field: 'last_msg_seq', title: '设备ID', minWidth: 120 },
    { field: 'last_msg_time_format', title: '收到时间', minWidth: 120 }
  ]
});
</script>

<template>
  <vxe-modal
    v-model="showModel"
    title="内容"
    :width="860"
    :height="560"
    :confirm-closable="false"
    :padding="false"
    :loading="loadingModel"
    show-maximize
  >
    <wk-page class="flex-col wk-page-bg">
      <div class="flex-1 card pt-0px overflow-hidden">
        <!-- S 表格 -->
        <div class="flex-1 card pt-0px overflow-hidden">
          <vxe-grid ref="tableRef" v-bind="gridOptions"></vxe-grid>
        </div>
        <!-- E 表格 -->
      </div>
    </wk-page>
  </vxe-modal>
</template>

<style scoped lang="scss">
.wk-page-bg {
  background-color: var(--el-bg-color-page);
}
</style>
