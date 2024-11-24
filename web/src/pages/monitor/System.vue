<script setup lang="ts">
import { monitorApi } from '@/api/modules/monitor-api';

// 常量
import {APP_DOC, LATEST_TIME} from '@/constants';

import { setSeries } from '@/utils';

import { VxeFormListeners, VxeFormProps } from 'vxe-pc-ui';
import { useUserStore } from '@/stores/modules/user';

const userStore = useUserStore();

const formOptions = reactive<VxeFormProps<any>>({
  data: {
    latest: 300
  },
  items: [
    {
      field: 'latest',
      title: '时间',
      itemRender: {
        name: 'ElSelect',
        props: {
          placeholder: '请选择',
          style: {
            width: '180px'
          }
        },
        options: LATEST_TIME
      }
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
    loadMetrics();
  },
  /** 重置 **/
  reset() {
    formOptions.data = {
      latest: 300
    };
    loadMetrics();
  }
};

interface Series {
  name?: string;
  type?: string;
  data: Point[];
}

interface Point {
  timestamp?: number;
  value?: number;
}

const intranetIncomingBytesRateRef = ref<Series[]>([]);
const intranetOutgoingBytesRateRef = ref<Series[]>([]);

const extranetIncomingBytesRateRef = ref<Series[]>([]);
const extranetOutgoingBytesRateRef = ref<Series[]>([]);
const memstatsAllocBytes = ref<Series[]>([]);
const goroutines = ref<Series[]>([]);

const gcDurationSecondsCount = ref<Series[]>([]);
const cpuPercent = ref<Series[]>([]);

const filefdAllocated = ref<Series[]>([]); // 文件打开数

const loadMetrics = () => {
  monitorApi.systemMetrics(formOptions.data).then(res => {
    intranetIncomingBytesRateRef.value = [];
    intranetOutgoingBytesRateRef.value = [];
    extranetIncomingBytesRateRef.value = [];
    extranetOutgoingBytesRateRef.value = [];

    memstatsAllocBytes.value = [];
    goroutines.value = [];
    gcDurationSecondsCount.value = [];
    cpuPercent.value = [];
    filefdAllocated.value = [];

    var filefdAllocatedData = [];

    for (let index = 0; index < res.length; index++) {
      const d = res[index];
      setSeries('intranet_incoming_bytes_rate', d, intranetIncomingBytesRateRef.value);
      setSeries('intranet_outgoing_bytes_rate', d, intranetOutgoingBytesRateRef.value);

      setSeries('extranet_incoming_bytes_rate', d, extranetIncomingBytesRateRef.value);
      setSeries('extranet_outgoing_bytes_rate', d, extranetOutgoingBytesRateRef.value);
      setSeries('memstats_alloc_bytes', d, memstatsAllocBytes.value);
      setSeries('goroutines', d, goroutines.value);
      setSeries('gc_duration_seconds_count', d, gcDurationSecondsCount.value);
      setSeries('cpu_percent', d, cpuPercent.value);
      filefdAllocatedData.push({ timestamp: d.timestamp, value: d.filefd_allocated });
    }
    filefdAllocated.value = [
      {
        name: '文件打开数',
        data: filefdAllocatedData
      }
    ];
  });
};

const isShow = computed(() => {
  return userStore.systemSetting.prometheusOn;
});

onMounted(() => {
  // 是否开启prometheus
  if (!userStore.systemSetting.prometheusOn) {
    return;
  }

  loadMetrics();
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

    <div v-if="isShow" class="flex-1 card overflow-hidden">
      <el-scrollbar>
        <div class="flex flex-wrap justify-left">
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="intranetIncomingBytesRateRef" title="内网流入流量" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="intranetOutgoingBytesRateRef" title="内网流出流量" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="extranetIncomingBytesRateRef" title="外网流入流量" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="extranetOutgoingBytesRateRef" title="外网流出流量" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="memstatsAllocBytes" title="内存" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="goroutines" title="协程" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="gcDurationSecondsCount" title="GC" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="cpuPercent" title="CPU" />
          </div>
          <div class="w-320px h-320px shadow-md p-12px mr-12px mb-12px">
            <wk-monitor-panel :data="filefdAllocated" title="文件打开数" />
          </div>
        </div>
      </el-scrollbar>
    </div>
    <div v-else class="flex-1 card flex items-center justify-center overflow-hidden">
      监控功能未开启，请查看官网文档：
      <el-link type="primary" :href="APP_DOC" target="_blank">{{ APP_DOC }}</el-link>
    </div>
  </wk-page>
</template>

<style scoped lang="scss"></style>

<route lang="yaml">
meta:
  title: 系统
</route>
