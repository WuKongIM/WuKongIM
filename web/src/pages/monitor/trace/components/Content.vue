<script name="Content" lang="ts" setup>
interface IProps {
  contentData: string;
}
/**
 * Props
 */
withDefaults(defineProps<IProps>(), {
  contentData: ''
});

const showModel = defineModel<boolean>();
const loadingModel = ref(false);
</script>

<template>
  <vxe-modal
    v-model="showModel"
    title="内容"
    :width="560"
    :height="460"
    :confirm-closable="false"
    :padding="false"
    :loading="loadingModel"
    show-maximize
  >
    <wk-page class="flex-col wk-page-bg">
      <div class="flex-1 card pt-0px overflow-hidden">
        <div v-html="contentData" class="break-words"></div>
      </div>
    </wk-page>
  </vxe-modal>
</template>

<style lang="scss" scoped>
.wk-page-bg {
  background-color: var(--el-bg-color-page);
}
</style>
