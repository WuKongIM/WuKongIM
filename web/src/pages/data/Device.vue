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
    uid: '',
    channel_type: null,
    channel_id: null,
    offset_created_at: 0,
    limit: 20,
    pre: 0
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
const currentPage = ref(1); // 当前页
const hasPrev = ref<boolean>(false); // 是否有上一页
const hasNext = ref<boolean>(true); // 是否有下一页

const loadList = async (query: any) => {
  const res = await dataApi.devices({ ...query });
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
    { field: 'uid', title: '所属用户', minWidth: 220 },
    { field: 'device_flag_format', title: '设备标识', minWidth: 120 },
    { field: 'device_level_format', title: '设备等级', minWidth: 120 },
    { field: 'token', title: '设备Token', minWidth: 120 },
    { field: 'conn_count', title: '连接数', minWidth: 160 },
    { field: 'send_msg_count', title: '发出消息数', minWidth: 120 },
    { field: 'recv_msg_count', title: '接受消息数', minWidth: 120 },
    { field: 'send_msg_bytes', title: '发出消息大小', minWidth: 140 },
    { field: 'recv_msg_bytes', title: '接受消息大小', minWidth: 140 },
    { field: 'created_at_format', title: '创建时间', minWidth: 140 },
    { field: 'updated_at_format', title: '更新时间', minWidth: 140 }
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

onMounted(() => {
  if (route.query?.uid) {
    formOptions.data = {
      ...formOptions.data,
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

<style scoped lang="scss"></style>

<route lang="yaml">
meta:
  title: 设备
</route>
