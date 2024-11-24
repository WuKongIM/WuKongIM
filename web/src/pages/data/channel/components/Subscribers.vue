<script name="Subscribers" lang="ts" setup>
// API 接口
import { dataApi } from '@/api/modules/data-api.ts';

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
  const res = await dataApi.subscribers(props.channelId, props.channelType);
  const getData: ITableItem[] = [];
  if (res.length > 0) {
    res.map((item: string) => {
      getData.push({
        uid: item
      });
    });
  }
  return getData;
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
    { field: 'uid', title: '订阅者ID', minWidth: 160 }
  ]
});
</script>

<template>
  <vxe-modal
    v-model="showModel"
    title="订阅者"
    :width="640"
    :height="460"
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

<style lang="scss" scoped>
.wk-page-bg {
  background-color: var(--el-bg-color-page);
}
</style>
