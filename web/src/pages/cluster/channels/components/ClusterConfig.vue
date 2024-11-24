<script name="ClusterConfig" lang="ts" setup>
// API 接口
import { clusterApi } from '@/api/modules/cluster-api';
import VueJsonPretty from 'vue-json-pretty';
import 'vue-json-pretty/lib/styles.css';

interface IProps {
  channelId: string;
  channelType: number;
  nodeId: number;
}

/**
 * Props
 */
const props = withDefaults(defineProps<IProps>(), {
  channelId: '',
  channelType: 0,
  nodeId: 0
});

const showModel = defineModel<boolean>();
const loadingModel = ref(false);

watch(
  () => showModel.value,
  val => {
    if (val) {
      if (props.channelId) {
        getConfig();
      } else {
        config.value = {};
      }
    }
  }
);

const config = ref<any>({});

const getConfig = async () => {
  const res = await clusterApi.channelClusterConfig({ ...props });
  config.value = res || {};
};
</script>

<template>
  <vxe-modal
    v-model="showModel"
    title="配置"
    :width="560"
    :height="460"
    :confirm-closable="false"
    :padding="false"
    :loading="loadingModel"
    show-maximize
  >
    <wk-page class="flex-col wk-page-bg">
      <div class="flex-1 card pt-0px overflow-hidden">
        <VueJsonPretty :data="config" />
      </div>
    </wk-page>
  </vxe-modal>
</template>

<style scoped lang="scss">
.wk-page-bg {
  background-color: var(--el-bg-color-page);
}
</style>
